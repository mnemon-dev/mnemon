package aml

import (
	"context"
	"math"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
	"github.com/mnemon-dev/mnemon/internal/records"
)

// The trace's JSON (docs/inspect-api.md, "The trace").
type (
	stageHead struct {
		Name   string      `json:"name"`
		MS     float64     `json:"ms"`
		Calls  int         `json:"calls"`
		Tokens tokenCount  `json:"tokens"`
		Cost   usageByKind `json:"cost"`
	}
	tokenCount struct {
		Input  int `json:"input"`
		Output int `json:"output"`
	}
	searchUsage struct {
		Calls  int         `json:"calls"`
		Tokens tokenCount  `json:"tokens"`
		Cost   usageByKind `json:"cost"`
	}
	pendingStage struct {
		stageHead
		Count     int      `json:"count"`
		IDs       []string `json:"ids"`
		Watermark int64    `json:"watermark"`
		Folded    bool     `json:"folded"`
	}
	cuesStage struct {
		stageHead
		Cues     []traceCue  `json:"cues"`
		Needs    []traceNeed `json:"needs"`
		Needless bool        `json:"needless"`
	}
	traceCue struct {
		Text string `json:"text"`
		Kind string `json:"kind"`
	}
	traceNeed struct {
		Text string `json:"text"`
		All  bool   `json:"all"`
	}
	candidatesStage struct {
		stageHead
		Searches []traceSearch     `json:"searches"`
		Pool     *[]string         `json:"pool,omitempty"`
		Layer    *[]layerCandidate `json:"layer,omitempty"`
	}
	traceSearch struct {
		Search int       `json:"search"`
		Text   string    `json:"text"`
		Kind   string    `json:"kind"`
		Source string    `json:"source"`
		Ranked int       `json:"ranked"`
		IDs    []string  `json:"ids"`
		Scores []float64 `json:"scores"`
	}
	layerCandidate struct {
		ID     string `json:"id"`
		Kind   string `json:"kind"`
		Source string `json:"source"`
		Lit    int    `json:"lit"`
		Near   *int   `json:"near"`
	}
	judgeStage struct {
		stageHead
		Records []judgedRecord `json:"records"`
		Layer   []judgedLayer  `json:"layer"`
		Linked  []judgedLinked `json:"linked"`
	}
	judgedRecord struct {
		ID         string    `json:"id"`
		Source     string    `json:"source"`
		Excerpt    string    `json:"excerpt"`
		Screen     *float64  `json:"screen"`
		Row        []float64 `json:"row"`
		Needed     float64   `json:"needed"`
		Superseded float64   `json:"superseded"`
		Judged     bool      `json:"judged"`
		Decision   string    `json:"decision"`
		Shown      bool      `json:"shown"`
	}
	judgedLayer struct {
		ID       string  `json:"id"`
		Kind     string  `json:"kind"`
		Source   string  `json:"source"`
		Excerpt  string  `json:"excerpt"`
		Needed   float64 `json:"needed"`
		Decision string  `json:"decision"`
	}
	judgedLinked struct {
		ID       string  `json:"id"`
		Source   string  `json:"source"`
		Excerpt  string  `json:"excerpt"`
		Needed   float64 `json:"needed"`
		Decision string  `json:"decision"`
		Shown    bool    `json:"shown"`
	}
	needStage struct {
		stageHead
		Rounds []traceRound `json:"rounds"`
		States []string     `json:"states"`
		Stop   string       `json:"stop"`
	}
	traceRound struct {
		Round    int           `json:"round"`
		Steps    []traceStep   `json:"steps"`
		Pages    []tracePage   `json:"pages"`
		Searches []traceSearch `json:"searches"`
		Fresh    []string      `json:"fresh"`
	}
	traceStep struct {
		Need int    `json:"need"`
		Step string `json:"step"`
	}
	tracePage struct {
		Search int       `json:"search"`
		IDs    []string  `json:"ids"`
		Scores []float64 `json:"scores"`
	}
	composeStage struct {
		stageHead
		Blocks    []traceBlock `json:"blocks"`
		Truncated bool         `json:"truncated"`
		Order     string       `json:"order"`
	}
	traceBlock struct {
		Position int      `json:"position"`
		ID       string   `json:"id"`
		Layer    string   `json:"layer"`
		Items    []string `json:"items"`
		Records  []string `json:"records"`
		Excerpt  string   `json:"excerpt"`
	}
)

// millis is a duration in milliseconds, rounded to 0.1.
func millis(d time.Duration) float64 { return math.Round(float64(d.Microseconds())/100) / 10 }

// excerpt is text cut to excerptMax characters: its first excerptMax-1 and
// "…" when it is longer.
func excerpt(text string) string {
	if utf8.RuneCountInString(text) <= excerptMax {
		return text
	}
	return string([]rune(text)[:excerptMax-1]) + "…"
}

// costOf is a use's cost by kind: DeepSeek's CNY and JEV's USD.
func costOf(usage memoryagent.Usage) usageByKind {
	return usageByKind{DeepSeek: usage.CNY, JEV: usage.USD}
}

// headOf is the head of a traced stage.
func headOf(s memoryagent.TraceStage) stageHead {
	return stageHead{Name: s.Name, MS: millis(s.Duration), Calls: s.PlannerCalls + s.DecisionCalls,
		Tokens: tokenCount{Input: s.Usage.InputTokens, Output: s.Usage.OutputTokens}, Cost: costOf(s.Usage)}
}

// traceUsage is a read's use, the sum of its stages.
func traceUsage(trace *memoryagent.ReadTrace) searchUsage {
	var usage memoryagent.Usage
	calls := 0
	for _, s := range trace.Stages {
		calls += s.PlannerCalls + s.DecisionCalls
		usage.InputTokens += s.Usage.InputTokens
		usage.OutputTokens += s.Usage.OutputTokens
		usage.USD += s.Usage.USD
		usage.CNY += s.Usage.CNY
	}
	return searchUsage{Calls: calls, Tokens: tokenCount{Input: usage.InputTokens, Output: usage.OutputTokens}, Cost: costOf(usage)}
}

// pendingStage is what a Search would fold before it reads.
func (i *Inspector) pendingStage(ctx context.Context, s *readStore) (pendingStage, error) {
	start := time.Now()
	c, _, err := s.agent.Consolidated(ctx, records.Scope{}, pendingIDsMax)
	if err != nil {
		return pendingStage{}, err
	}
	ids := c.PendingIDs
	if ids == nil {
		ids = []string{}
	}
	return pendingStage{stageHead: stageHead{Name: "pending", MS: millis(time.Since(start))}, Count: c.Pending, IDs: ids, Watermark: c.Watermark}, nil
}

// fullStages is a full read's trace: pending, then the read's stages, the
// searches' words scores computed here (the read records ids only), and
// the judgements' decisions and the View's blocks as result shows them.
func (i *Inspector) fullStages(ctx context.Context, s *readStore, pending pendingStage, trace *memoryagent.ReadTrace, view memoryagent.View, result []SearchItem) ([]any, error) {
	scores, err := searchScores(ctx, s.store, trace)
	if err != nil {
		return nil, err
	}
	shown, items := map[string]bool{}, map[string]bool{}
	for _, block := range composeBlocks(result, view.Index) {
		shown[block.ID] = true
		for _, item := range block.Items {
			items[item] = true
		}
	}
	stages := []any{pending}
	for _, traced := range trace.Stages {
		head := headOf(traced)
		switch traced.Name {
		case "cues":
			stages = append(stages, cuesOf(head, trace))
		case "candidates":
			stages = append(stages, candidatesOf(head, trace, scores))
		case "judge":
			stages = append(stages, judgeOf(head, trace, shown, items))
		case "need":
			stages = append(stages, needOf(head, trace, scores))
		case "compose":
			stages = append(stages, composeStage{stageHead: head, Blocks: composeBlocks(result, view.Index), Truncated: view.Truncated, Order: i.recordOrder()})
		}
	}
	return stages, nil
}

// searchScores computes the words score of every id each traced search read
// (its first page, and the pages the rounds read), by search: first page
// then the rounds' pages in order.
func searchScores(ctx context.Context, store *records.Store, trace *memoryagent.ReadTrace) (map[[2]int][]float64, error) {
	queries, ids, keys := []string{}, [][]string{}, [][2]int{}
	for n, search := range trace.Searches {
		queries, ids, keys = append(queries, search.Text), append(ids, search.First), append(keys, [2]int{n, -1})
	}
	for r, round := range trace.Rounds {
		for _, page := range round.Pages {
			queries, ids, keys = append(queries, trace.Searches[page.Search].Text), append(ids, page.IDs), append(keys, [2]int{page.Search, r})
		}
	}
	scores, err := store.LexicalScores(ctx, trace.Revision, records.Scope{}, queries, ids)
	if err != nil {
		return nil, err
	}
	out := map[[2]int][]float64{}
	for k, key := range keys {
		out[key] = scores[k]
	}
	return out, nil
}

func cuesOf(head stageHead, trace *memoryagent.ReadTrace) cuesStage {
	stage := cuesStage{stageHead: head, Cues: []traceCue{}, Needs: []traceNeed{}, Needless: trace.Needless}
	for _, c := range trace.Cues {
		stage.Cues = append(stage.Cues, traceCue{Text: excerpt(c.Text), Kind: c.Kind})
	}
	for _, need := range trace.Needs {
		stage.Needs = append(stage.Needs, traceNeed{Text: excerpt(need.Text), All: need.All})
	}
	return stage
}

// tracedSearch is search n of the trace with its first page's scores.
func tracedSearch(trace *memoryagent.ReadTrace, n int, scores map[[2]int][]float64) traceSearch {
	search := trace.Searches[n]
	return traceSearch{Search: n, Text: excerpt(search.Text), Kind: search.Kind, Source: "record", Ranked: search.Ranked, IDs: nonNil(search.First), Scores: nonNilScores(scores[[2]int{n, -1}])}
}

func candidatesOf(head stageHead, trace *memoryagent.ReadTrace, scores map[[2]int][]float64) candidatesStage {
	stage := candidatesStage{stageHead: head, Searches: []traceSearch{}}
	for n := range trace.Cues {
		if n < len(trace.Searches) {
			stage.Searches = append(stage.Searches, tracedSearch(trace, n, scores))
		}
	}
	pool, layer := nonNil(trace.Pool), []layerCandidate{}
	for _, item := range trace.Layer {
		candidate := layerCandidate{ID: item.ID, Kind: item.Kind, Source: "layer", Lit: item.Lit}
		if item.Near >= 0 {
			near := item.Near
			candidate.Near = &near
		}
		layer = append(layer, candidate)
	}
	stage.Pool, stage.Layer = &pool, &layer
	return stage
}

// judgeOf is JEV's answers per candidate, with their decisions: shown are
// the ids result shows, items the memory items its blocks show. A topic is
// shown when the topics block lists it, or when the timelines block shows
// any of its events (it lists a topic only while it has room).
func judgeOf(head stageHead, trace *memoryagent.ReadTrace, shown, items map[string]bool) judgeStage {
	stage := judgeStage{stageHead: head, Records: []judgedRecord{}, Layer: []judgedLayer{}, Linked: []judgedLinked{}}
	for _, item := range trace.Items {
		record := judgedRecord{ID: item.ID, Source: "record", Excerpt: excerpt(item.Text), Row: item.Row, Needed: item.Needed, Superseded: item.Superseded, Judged: item.Judged, Shown: shown[item.ID]}
		if item.HasScreen {
			screen := item.Screen
			record.Screen = &screen
		}
		switch {
		case item.Superseded >= 0.5:
			record.Decision = "superseded"
		case item.Needed >= 0.5:
			record.Decision = "needed"
		case record.Shown:
			record.Decision = "kept"
		default:
			record.Decision = "dropped"
		}
		stage.Records = append(stage.Records, record)
	}
	keptOr := func(kept bool) string {
		if kept {
			return "kept"
		}
		return "dropped"
	}
	for _, item := range trace.Layer {
		kept := items[item.ID] || slices.ContainsFunc(item.Events, func(event string) bool { return items[event] })
		stage.Layer = append(stage.Layer, judgedLayer{ID: item.ID, Kind: item.Kind, Source: "layer", Excerpt: excerpt(item.Text), Needed: item.Needed, Decision: keptOr(kept)})
	}
	for _, item := range trace.Linked {
		stage.Linked = append(stage.Linked, judgedLinked{ID: item.ID, Source: "layer", Excerpt: excerpt(item.Text), Needed: item.Needed, Decision: keptOr(shown[item.ID]), Shown: shown[item.ID]})
	}
	return stage
}

func needOf(head stageHead, trace *memoryagent.ReadTrace, scores map[[2]int][]float64) needStage {
	stage := needStage{stageHead: head, Rounds: []traceRound{}, States: nonNil(trace.NeedStates), Stop: trace.Stop}
	for r, round := range trace.Rounds {
		traced := traceRound{Round: round.Round, Steps: []traceStep{}, Pages: []tracePage{}, Searches: []traceSearch{}, Fresh: nonNil(round.Fresh)}
		for k, step := range round.Steps {
			traced.Steps = append(traced.Steps, traceStep{Need: k, Step: step})
		}
		for _, page := range round.Pages {
			traced.Pages = append(traced.Pages, tracePage{Search: page.Search, IDs: nonNil(page.IDs), Scores: nonNilScores(scores[[2]int{page.Search, r}])})
		}
		for _, n := range round.Searches {
			traced.Searches = append(traced.Searches, tracedSearch(trace, n, scores))
		}
		stage.Rounds = append(stage.Rounds, traced)
	}
	return stage
}

// composeBlocks is result item by item: a layer block (index names the
// View's blocks) with the items it shows and the records it links to that
// result shows, or a record.
func composeBlocks(result []SearchItem, index []memoryagent.IndexItem) []traceBlock {
	blocks, byID, records := []traceBlock{}, map[string]memoryagent.IndexItem{}, map[string]bool{}
	for _, block := range index {
		byID[blockID(block)] = block
	}
	for _, item := range result {
		records[item.ID] = true
	}
	layers := map[string]string{"1-instructions": "instructions", "2-topics": "topics", "3-timelines": "timelines", "4-values": "facts"}
	for p, item := range result {
		block, ok := byID[item.ID]
		if !ok {
			blocks = append(blocks, traceBlock{Position: p, ID: item.ID, Layer: "records", Items: []string{item.ID}, Records: []string{item.ID}, Excerpt: excerpt(item.Content)})
			continue
		}
		linked := []string{}
		for _, ref := range block.Records {
			if records[ref.ID] && !slices.Contains(linked, ref.ID) {
				linked = append(linked, ref.ID)
			}
		}
		layer := layers[block.Kind]
		if layer == "" {
			layer = block.Kind
		}
		blocks = append(blocks, traceBlock{Position: p, ID: item.ID, Layer: layer, Items: nonNil(block.Items), Records: linked, Excerpt: excerpt(item.Content)})
	}
	return blocks
}

func nonNil(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}

func nonNilScores(scores []float64) []float64 {
	if scores == nil {
		return []float64{}
	}
	return scores
}
