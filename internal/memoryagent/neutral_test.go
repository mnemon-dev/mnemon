package memoryagent

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// neutralJudge answers the neutral linking questions from the texts the
// request's state gives: choose picks a group for a new group's text among
// the candidates' texts (or "none"), update answers whether a new note's
// text updates a candidate's. err fails every call.
type neutralJudge struct {
	choose func(text string, candidates map[string]string) string
	update func(text, candidate string) bool
	err    error
	asked  []map[string]Question
}

var questionIDs = regexp.MustCompile(`"([nc]\d+|T\d+)"`)

func neutralTexts(state any) map[string]string {
	s := state.(linkState)
	texts := map[string]string{}
	for _, it := range append(slices.Clone(s.New), s.Candidates...) {
		texts[it.ID] = it.Text
	}
	return texts
}

func (j *neutralJudge) Decide(_ context.Context, state any, questions map[string]Question) (map[string]float64, Usage, error) {
	j.asked = append(j.asked, questions)
	if j.err != nil {
		return nil, Usage{}, j.err
	}
	texts, out := neutralTexts(state), map[string]float64{}
	for key, q := range questions {
		ids := questionIDs.FindAllStringSubmatch(q.Instructions, -1)
		out[key] = 0.1
		if len(ids) == 2 && j.update(texts[ids[0][1]], texts[ids[1][1]]) {
			out[key] = 0.9
		}
	}
	return out, Usage{}, nil
}

func (j *neutralJudge) Choose(_ context.Context, state any, questions map[string]Question) (map[string]Choice, Usage, error) {
	j.asked = append(j.asked, questions)
	if j.err != nil {
		return nil, Usage{}, j.err
	}
	texts, out := neutralTexts(state), map[string]Choice{}
	for key, q := range questions {
		id := questionIDs.FindStringSubmatch(q.Instructions)[1]
		candidates := map[string]string{}
		for option := range q.Criteria {
			if option != "none" {
				candidates[option] = texts[option]
			}
		}
		out[key] = Choice{Option: j.choose(texts[id], candidates), P: 0.8}
	}
	return out, Usage{}, nil
}

func neutralAgent(t *testing.T, judge Decider) *Agent {
	t.Helper()
	s, dir := agentStore(t)
	overlay, err := OpenOverlay(filepath.Join(dir, "memory-index.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { overlay.Close() })
	model := &limitPlanner{reply: func(_, prompt string) (string, error) { return `{"notes":[]}`, nil }}
	a, err := New(s, Config{Version: 1, Policy: &ReadPolicy{Consolidation: true}, Consolidation: &ConsolidationConfig{Mode: "neutral"}}, Models{Consolidator: model, Judge: judge}, overlay)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestNeutralFoldReadsNotesAsGroupedEvents(t *testing.T) {
	batch := []pendingRecord{{ID: "a"}, {ID: "b"}}
	reply := `{"notes":[{"group":"Trip to Rome","date":"2024/03/01","text":"Booked flights.","records":["r0"]},` +
		`{"group":"Trip to Rome","date":"2024/03/02","text":"Hotel chosen.","records":["r1"]},` +
		`{"group":"Job","date":"2024/03/02","text":"Started at Acme.","records":["r1","r9"]}],` +
		`"events":[{"thread":"new: Other","text":"not read"}],"facts":[{"key":"k","value":"v"}]}`
	m := emptyTimelineState().Memory
	if err := applyReplyWith(&m, batch, reply, false, true); err != nil {
		t.Fatal(err)
	}
	titles := []string{}
	for _, th := range m.Threads {
		titles = append(titles, th.Title)
	}
	if !slices.Equal(titles, []string{"Trip to Rome", "Job"}) || len(m.Events) != 3 || len(m.Facts) != 0 {
		t.Fatalf("threads %v, events %d, facts %d", titles, len(m.Events), len(m.Facts))
	}
	if !slices.Equal(m.Events[0].Records, []string{"a"}) || !slices.Equal(m.Events[2].Records, []string{"b"}) || m.Events[1].Thread != m.Events[0].Thread {
		t.Fatalf("events %+v", m.Events)
	}
}

func TestNeutralModeExtractsUnderTheNeutralPrompt(t *testing.T) {
	a := neutralAgent(t, &neutralJudge{})
	if spec := a.extractionFold(); spec.system != neutralSystem || !spec.notes || spec.entities {
		t.Fatal("neutral mode does not extract under the neutral prompt")
	}
	if !strings.Contains(neutralSystem, `{"notes":[{"group":"<short title>"`) || strings.Contains(neutralSystem, "facts") {
		t.Fatal("neutral prompt changed")
	}
	if extractionKey(neutralSystem, false, nil) == extractionKey(layeredFold.system, false, nil) {
		t.Fatal("neutral and layered extractions share a key")
	}
}

func TestNeutralReadsEveryRecordWholeAndAlike(t *testing.T) {
	heading := "Conversation beam-1-s1 on 2024/03/15 (Fri) 00:00:\n"
	continuation := heading + "so we can tailor this schedule further?"
	longAnswer := heading + "assistant: " + strings.Repeat("step ", 100)
	// The layered reader passes a continuation over and cuts an answer.
	if _, _, ok := readable("Original record", continuation, 240); ok {
		t.Fatal("readable read a continuation")
	}
	if _, body, _ := readable("Original record", longAnswer, 240); !strings.HasSuffix(body, " …") {
		t.Fatal("readable did not cut a long answer")
	}
	a := neutralAgent(t, &neutralJudge{})
	read := a.recordReader(240)
	if header, body, ok := read("Original record", continuation); !ok || header != "2024/03/15 (session beam-1-s1)" || body != "so we can tailor this schedule further?" {
		t.Fatalf("neutral read %q %q %v", header, body, ok)
	}
	if _, body, ok := read("Original record", longAnswer); !ok || body != longAnswer[len(heading):] {
		t.Fatal("neutral mode cut a long answer")
	}
	if header, body, ok := read("A note", "plain text, no heading"); !ok || header != "A note" || body != "plain text, no heading" {
		t.Fatal("neutral read of a record without a heading", header, body)
	}
	if _, _, ok := read("Empty", heading+"  \n"); ok {
		t.Fatal("neutral mode read an empty record")
	}
	if strings.Contains(neutralSystem, "cut short") {
		t.Fatal("the neutral prompt says assistant messages are cut")
	}
}

func TestNeutralModeNeedsAChoosingJudge(t *testing.T) {
	s, dir := agentStore(t)
	overlay, err := OpenOverlay(filepath.Join(dir, "memory-index.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { overlay.Close() })
	model := &limitPlanner{reply: func(_, prompt string) (string, error) { return `{"notes":[]}`, nil }}
	_, err = New(s, Config{Version: 1, Policy: &ReadPolicy{Consolidation: true}, Consolidation: &ConsolidationConfig{Mode: "neutral"}}, Models{Consolidator: model, Judge: &scriptedJudge{}}, overlay)
	if err == nil || !strings.Contains(err.Error(), "choice questions") {
		t.Fatal("neutral mode accepted a judge without choice questions:", err)
	}
}

// romeMemory is a stored memory of two groups, and a batch of two new ones:
// one continues the Rome trip and moves its flight, one is new.
func romeMemory() (stored, batch timelineMemory) {
	stored = emptyTimelineState().Memory
	stored.Threads = []thread{{ID: "T1", Title: "Trip to Rome"}, {ID: "T2", Title: "Job"}}
	stored.Events = []event{
		{Thread: "T1", Date: "2024/03/01", Text: "Flight to Rome on May 3.", Records: []string{"r-a"}},
		{Thread: "T2", Date: "2024/03/01", Text: "Works at Acme.", Records: []string{"r-b"}},
	}
	batch = emptyTimelineState().Memory
	batch.Threads = []thread{{ID: "T1", Title: "Rome trip plans"}, {ID: "T2", Title: "Painting"}}
	batch.Events = []event{
		{Thread: "T1", Date: "2024/04/02", Text: "The Rome flight moved to May 5.", Records: []string{"r-c"}},
		{Thread: "T2", Date: "2024/04/02", Text: "Started painting watercolors.", Records: []string{"r-d"}},
	}
	return stored, batch
}

func romeJudge() *neutralJudge {
	return &neutralJudge{
		choose: func(text string, candidates map[string]string) string {
			for id, c := range candidates {
				if strings.Contains(text, "Rome") && strings.Contains(c, "Rome") {
					return id
				}
			}
			return "none"
		},
		update: func(text, candidate string) bool {
			return strings.Contains(text, "moved") && strings.Contains(candidate, "May 3")
		},
	}
}

func TestNeutralLinkJoinsTheChosenGroupAndMarksWhatANoteUpdates(t *testing.T) {
	judge := romeJudge()
	a := neutralAgent(t, judge)
	stored, batch := romeMemory()
	state := &timelineState{Memory: stored}
	out, counts, err := a.newLinker().link(context.Background(), state, batch, &ConsolidateResult{})
	if err != nil {
		t.Fatal(err)
	}
	if counts.ThreadsLinked != 1 || counts.ThreadsNew != 1 || counts.Fallback || len(out.Threads) != 3 || len(out.Events) != 4 {
		t.Fatalf("counts %+v, threads %v, events %d", counts, out.Threads, len(out.Events))
	}
	if out.Events[2].Thread != "T1" || out.Events[3].Thread == "T1" || out.Events[3].Thread == "T2" {
		t.Fatalf("notes joined %q and %q", out.Events[2].Thread, out.Events[3].Thread)
	}
	if !out.Events[0].Updated || out.Events[1].Updated || out.Events[2].Updated || out.Events[3].Updated {
		t.Fatalf("updated marks %+v", out.Events)
	}
	if stored.Events[0].Updated {
		t.Fatal("linking changed the stored memory")
	}
	for _, questions := range judge.asked {
		for _, q := range questions {
			if q.Type == "choice" && q.Criteria["none"] != groupNone {
				t.Fatal("a group question without none")
			}
		}
	}
}

func TestNeutralLinkFallsBackWithoutJEV(t *testing.T) {
	judge := romeJudge()
	judge.err = errors.New("jev refused the request")
	a := neutralAgent(t, judge)
	stored, batch := romeMemory()
	out, counts, err := a.newLinker().link(context.Background(), &timelineState{Memory: stored}, batch, &ConsolidateResult{})
	if err != nil {
		t.Fatal(err)
	}
	if !counts.Fallback || counts.ThreadsNew != 2 || counts.ThreadsLinked != 0 || slices.ContainsFunc(out.Events, func(e event) bool { return e.Updated }) {
		t.Fatalf("counts %+v, events %+v", counts, out.Events)
	}
}

func TestNeutralLayerMarksUpdatedNotesAndOrdersYesByAnchors(t *testing.T) {
	m := emptyTimelineState().Memory
	m.Threads = []thread{{ID: "T1", Title: "Trip to Rome"}}
	m.Events = []event{
		{Thread: "T1", Date: "2024/03/01", Text: "Flight on May 3.", Records: []string{"r-a"}, Updated: true},
		{Thread: "T1", Date: "2024/04/02", Text: "Flight moved to May 5.", Records: []string{"r-c"}},
	}
	blocks := composeLayer(m, []string{"T1"}, nil, litSets{Counts: map[string]int{}, Events: map[int]bool{}})
	text := ""
	for _, b := range blocks {
		text += b.Text
	}
	if !strings.Contains(text, "Flight on May 3. (later updated)") || strings.Contains(text, "May 5. (later updated)") {
		t.Fatalf("layer text %q", text)
	}
	order := neutralLayerOrder([]string{"A", "B", "C"}, litSets{Counts: map[string]int{"A": 1, "B": 3}}, map[string]float64{"A": 0.9, "B": 0.6, "C": 0.2}, []string{"A", "B", "L"})
	if !slices.Equal(order, []string{"B", "A", "L"}) {
		t.Fatal("neutral layer order", order)
	}
}

// neutralReply is the neutral L0 over notes "<topic> note <n> …": per note,
// a note in the topic's group.
func neutralReply(prompt string) string {
	notes := []map[string]any{}
	for _, m := range layeredNote.FindAllStringSubmatch(prompt, -1) {
		notes = append(notes, map[string]any{"group": m[2], "date": "2024/03/15", "text": m[0][strings.Index(m[0], "\n")+1:], "records": []string{"r" + m[1]}})
	}
	raw, _ := json.Marshal(map[string]any{"notes": notes})
	return string(raw)
}

func TestConsolidateNeutralGroupsByChoiceAndMarksUpdates(t *testing.T) {
	s, dir := agentStore(t)
	seedTopics(t, s, 3)
	overlay, err := OpenOverlay(filepath.Join(dir, "memory-index.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { overlay.Close() })
	model := &limitPlanner{reply: func(_, prompt string) (string, error) { return neutralReply(prompt), nil }}
	topic := func(text string) string { return strings.Fields(text)[0] }
	judge := &neutralJudge{
		choose: func(text string, candidates map[string]string) string {
			for id, c := range candidates {
				if strings.HasPrefix(c, topic(text)) {
					return id
				}
			}
			return "none"
		},
		// Every garden note updates the earlier garden notes.
		update: func(text, candidate string) bool { return strings.HasPrefix(text, "garden") },
	}
	a, err := New(s, Config{Version: 1, Policy: &ReadPolicy{Consolidation: true}, Consolidation: &ConsolidationConfig{Mode: "neutral", BatchCharacters: 100}}, Models{Consolidator: model, Judge: judge}, overlay)
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.Consolidate(context.Background(), ConsolidateRequest{})
	if err != nil || result.Folded != 3 || result.ThreadsLinked != 6 || result.ThreadsNew != 3 || result.LinkFallbacks != 0 {
		t.Fatalf("result %+v, %v", result, err)
	}
	state := loadState(t, overlay)
	want := map[string][]string{"kayak": {"n00", "n03", "n06"}, "taxes": {"n01", "n04", "n07"}, "garden": {"n02", "n05", "n08"}}
	if got := threadEvents(state); state.Mode != "neutral" || !reflect.DeepEqual(got, want) || len(state.Memory.Threads) != 3 || len(state.Memory.Facts) != 0 {
		t.Fatal("state", state.Mode, got, state.Memory.Threads)
	}
	updated := []string{}
	for _, e := range state.Memory.Events {
		if e.Updated {
			updated = append(updated, e.Records...)
		}
	}
	if slices.Sort(updated); !slices.Equal(updated, []string{"n02", "n05"}) {
		t.Fatal("updated", updated)
	}
	for _, call := range model.calls {
		if call.system != neutralSystem {
			t.Fatal("L0 call not under the neutral prompt")
		}
	}
}
