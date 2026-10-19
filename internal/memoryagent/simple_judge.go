package memoryagent

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// What JEV is asked during recall: the screen ("should the reply use
// this?", the author's scan N:271-282 with compactScanRequest J:77-84) and
// the need table ("does this give that need?", needRequest J:108-119).
// Candidates are cleaned short excerpts; lengths are JavaScript string
// lengths (UTF-16 units).

const (
	simpleListing        = 600    // the author's `listing`: excerpt size and scan cost cap
	scanChunkItems       = 120    // items a scan call
	scanChunkSize        = 24_000 // cost units a scan call
	tableBatch           = 24     // candidates a table call
	simpleDecisionBudget = 128    // JEV decision calls a read
	judgeSource          = "journal"
)

var (
	// /^\s*\{.*\}\s*$/ — a JSON metadata line.
	dataLine = regexp.MustCompile(`^` + jsSpace + `*\{` + jsDot + `*\}` + jsSpace + `*$`)
	// /\[?[a-z-]*:?[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\]?/gi.
	// JavaScript's /i without u folds no non-ASCII letter onto these, so
	// ASCII classes are exact.
	opaqueID = regexp.MustCompile(`\[?[a-zA-Z-]*:?[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\]?`)
	// /[ \t]*\n\s*/g
	lineBreak = regexp.MustCompile(`[ \t]*\n` + jsSpace + `*`)
)

// recallText drops what carries no meaning for recall (J:72-75): JSON
// metadata lines and opaque identifiers; line breaks become " / ".
func recallText(s string) string {
	lines := strings.Split(s, "\n")
	kept := []string{}
	for _, line := range lines {
		if !dataLine.MatchString(line) {
			kept = append(kept, line)
		}
	}
	s = opaqueID.ReplaceAllLiteralString(strings.Join(kept, "\n"), "")
	return strings.TrimFunc(lineBreak.ReplaceAllLiteralString(s, " / "), isJSSpace)
}

// authorExcerpt is the author's excerpt (J:33-40): a text over limit units
// keeps its head and tail of floor((limit-3)/2) units each around " … ". A
// cut inside a surrogate pair drops the pair: the head stops before it, the
// tail starts after it. limit must be at least 3.
func authorExcerpt(s string, limit int) string {
	n := jsLen(s)
	if n <= limit {
		return s
	}
	half := (limit - 3) / 2
	head := jsOffset(s, half)
	tail := jsOffset(s, n-half)
	if jsLen(s[:tail]) < n-half { // n-half is the low half of a pair
		tail = jsOffset(s, n-half+1)
	}
	return s[:head] + " … " + s[tail:]
}

// The request states keep the author's key order.
type judgeMessage struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

type judgeCandidate struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Text   string `json:"text"`
}

type judgeRubric struct {
	Question string `json:"question"`
	Yes      string `json:"yes"`
	No       string `json:"no"`
}

type judgeNeed struct {
	ID   string `json:"id"`
	Need string `json:"need"`
}

type scanState struct {
	Dialogue   []judgeMessage   `json:"dialogue"`
	Candidates []judgeCandidate `json:"candidates"`
	Rubric     judgeRubric      `json:"rubric"`
}

type needState struct {
	Dialogue   []judgeMessage   `json:"dialogue"`
	Needs      []judgeNeed      `json:"needs"`
	Candidates []judgeCandidate `json:"candidates"`
	Rubric     judgeRubric      `json:"rubric"`
}

// judgeDialogue is the last count messages, each excerpted to limit units.
func judgeDialogue(dialogue []Message, count, limit int) []judgeMessage {
	out := []judgeMessage{}
	for _, message := range dialogue[max(0, len(dialogue)-count):] {
		out = append(out, judgeMessage{Role: message.Role, Text: authorExcerpt(message.Text, limit)})
	}
	return out
}

// judgeCandidates labels items c0, c1, … with their cleaned excerpts.
func judgeCandidates(items []*simpleItem) []judgeCandidate {
	out := []judgeCandidate{}
	for i, item := range items {
		out = append(out, judgeCandidate{ID: fmt.Sprintf("c%d", i), Source: judgeSource, Text: authorExcerpt(recallText(item.Evidence.Text), simpleListing)})
	}
	return out
}

// decisionsLeft is how many JEV calls the read may still make.
func (r *simpleRun) decisionsLeft() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return max(0, simpleDecisionBudget-r.stats.DecisionCalls)
}

// decide makes one JEV call within the read's budget of 128; a spent budget
// returns errCallBudget without calling.
func (r *simpleRun) decide(ctx context.Context, state any, questions map[string]Question) (map[string]float64, error) {
	r.mu.Lock()
	if r.stats.DecisionCalls >= simpleDecisionBudget {
		r.mu.Unlock()
		return nil, errCallBudget
	}
	r.stats.DecisionCalls++
	r.mu.Unlock()
	start := time.Now()
	answers, usage, err := r.agent.models.Judge.Decide(ctx, state, questions)
	r.mu.Lock()
	r.stats.ModelMS += float64(time.Since(start).Microseconds()) / 1000
	r.stats.Usage.add(usage)
	r.mu.Unlock()
	if err == nil {
		err = validateAnswers(answers, questions)
	}
	return answers, err
}

// scanChunks splits items for the screen (N:273-279): a new chunk when the
// current one holds 120 items or the next item's cost, min(units, 600)+40,
// would take it past 24,000.
func scanChunks(items []*simpleItem) [][]*simpleItem {
	chunks, chunk, size := [][]*simpleItem{}, []*simpleItem{}, 0
	for _, item := range items {
		cost := min(jsLen(item.Evidence.Text), simpleListing) + 40
		if len(chunk) > 0 && (len(chunk) >= scanChunkItems || size+cost > scanChunkSize) {
			chunks, chunk, size = append(chunks, chunk), []*simpleItem{}, 0
		}
		chunk = append(chunk, item)
		size += cost
	}
	if len(chunk) > 0 {
		chunks = append(chunks, chunk)
	}
	return chunks
}

// scan gives every item JEV's screen, its "needed?" answer (N:271-282), in
// parallel chunks, and sets Screen. When the decision budget cannot pay for
// every chunk, the first chunks it can pay for are screened and scan returns
// errCallBudget. On another error the read fails.
func (r *simpleRun) scan(ctx context.Context, items []*simpleItem) error {
	chunks := scanChunks(items)
	afford := min(len(chunks), r.decisionsLeft())
	dialogue := judgeDialogue(r.dialogue, 4, 300)
	rubric := judgeRubric{Question: neededQuestion("the candidate"), Yes: neededYes, No: neededNo}
	errs := make([]error, afford)
	var work sync.WaitGroup
	for c, chunk := range chunks[:afford] {
		work.Add(1)
		go func() {
			defer work.Done()
			errs[c] = recovered(func() error {
				questions := map[string]Question{}
				for i := range chunk {
					questions[fmt.Sprintf("n%d", i)] = Question{Type: "noul", Instructions: scanInstruction(i)}
				}
				answers, err := r.decide(ctx, scanState{Dialogue: dialogue, Candidates: judgeCandidates(chunk), Rubric: rubric}, questions)
				if err != nil {
					return err
				}
				for i, item := range chunk {
					item.Screen, item.HasScreen = answers[fmt.Sprintf("n%d", i)], true
				}
				return nil
			})
		}()
	}
	work.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	if afford < len(chunks) {
		return errCallBudget
	}
	return nil
}

// tabled reports whether the needs are asked of JEV one by one. One single
// need asks what the screen already asked, so its screen stands in (N:710).
func (r *simpleRun) tabled() bool {
	for _, need := range r.needs {
		if need.All {
			return true
		}
	}
	return len(r.needs) > 1
}

// table sets each item's Row, its answer for every need, and adds it to the
// table (N:711-719): JEV's need table in sequential batches of 24, or the
// screen when one single need is untabled. A spent decision budget returns
// errCallBudget between batches; rows already set stay.
func (r *simpleRun) table(ctx context.Context, items []*simpleItem) error {
	if !r.tabled() {
		for _, item := range items {
			r.setRow(item, []float64{item.Screen})
		}
		return nil
	}
	needs := []judgeNeed{}
	for k, need := range r.needs {
		needs = append(needs, judgeNeed{ID: fmt.Sprintf("q%d", k), Need: need.Text})
	}
	dialogue := judgeDialogue(r.dialogue, 6, simpleListing)
	rubric := judgeRubric{Question: needQuestion, Yes: needYes, No: needNo}
	for start := 0; start < len(items); start += tableBatch {
		part := items[start:min(start+tableBatch, len(items))]
		questions := map[string]Question{}
		for i := range part {
			for k := range r.needs {
				questions[fmt.Sprintf("c%dq%d", i, k)] = Question{Type: "noul", Instructions: needInstruction(i, k)}
			}
		}
		answers, err := r.decide(ctx, needState{Dialogue: dialogue, Needs: needs, Candidates: judgeCandidates(part), Rubric: rubric}, questions)
		if err != nil {
			return err
		}
		for i, item := range part {
			row := make([]float64, len(r.needs))
			for k := range r.needs {
				row[k] = answers[fmt.Sprintf("c%dq%d", i, k)]
			}
			r.setRow(item, row)
		}
	}
	return nil
}

// setRow records an item's row; the table keeps the order items first
// entered it.
func (r *simpleRun) setRow(item *simpleItem, row []float64) {
	if item.Row == nil {
		r.rows = append(r.rows, item.ID)
	}
	item.Row = row
}
