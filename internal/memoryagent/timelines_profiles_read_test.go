package memoryagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/records"
)

func TestViewProfilesConfig(t *testing.T) {
	s, _ := agentStore(t)
	both := Models{Planner: codenamePlanner("NEED: the codename"), Judge: layerJudge()}
	for _, mode := range []string{"timelines", "layered"} {
		for _, view := range []ViewConfig{{Profiles: true}, {Profiles: true, Layer: "scaled"}} {
			a, err := New(s, Config{Version: 1, Strategy: "simple", Consolidation: &ConsolidationConfig{Mode: mode}, View: &view}, both, nil)
			if err != nil || !a.config.View.Profiles {
				t.Error("profiles refused", mode, view, err)
			}
		}
	}
	// Without the timelines layer the setting could only be inert.
	for _, without := range []Config{{}, {Consolidation: &ConsolidationConfig{}}, {Consolidation: &ConsolidationConfig{Mode: "timelines"}, Policy: &ReadPolicy{}}} {
		without.Version, without.Strategy, without.View = 1, "simple", &ViewConfig{Profiles: true}
		if _, err := New(s, without, both, nil); err == nil || !strings.Contains(err.Error(), "profiles require timelines or layered consolidation") {
			t.Error("profiles without the timelines layer accepted", without.Consolidation, without.Policy, err)
		}
	}
	// Off, it stays out of the config's JSON, and so out of the policy hash;
	// the stat is absent at zero.
	if raw, _ := json.Marshal(ViewConfig{MaxItems: 16, MaxCharacters: 12000}); strings.Contains(string(raw), "profiles") {
		t.Fatal(string(raw))
	}
	if raw, _ := json.Marshal(ReadStats{}); strings.Contains(string(raw), "layerProfiles") {
		t.Fatal(string(raw))
	}
}

// profiledMemory is layerMemory as layered extraction names its entities:
// ORBIT (three events, both keys), Rocket project, Finance team and Launch
// pad 39A.
func profiledMemory() timelineMemory {
	m := layerMemory()
	for i, names := range [][]string{{"Finance team"}, {"ORBIT", "Rocket project"}, {"ORBIT"}, {"ORBIT", "Launch pad 39A"}} {
		m.Events[i].Entities = names
	}
	for i := range m.Facts["launch day"] {
		m.Facts["launch day"][i].Entities = []string{"ORBIT"}
	}
	m.Facts["codename"][0].Entities = []string{"ORBIT", "Rocket project"}
	return m
}

// profileJudge answers like layerJudge, and of the entities calls Rocket
// project needed (0.8) and Finance team (0.6).
func profileJudge() *scriptedJudge {
	base := layerJudge()
	return &scriptedJudge{answer: func(text string, need int) float64 {
		switch {
		case strings.HasPrefix(text, "Rocket project: "):
			return 0.8
		case strings.HasPrefix(text, "Finance team: "):
			return 0.6
		}
		return base.answer(text, need)
	}}
}

// requestOf decodes a recorded request's state and questions.
func requestOf(t *testing.T, raw string) (state judgeCall, questions map[string]Question) {
	t.Helper()
	encoded, asked, _ := strings.Cut(raw, "\n")
	if err := json.Unmarshal([]byte(encoded), &state.State); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(asked), &questions); err != nil {
		t.Fatal(err)
	}
	return state, questions
}

// profilesWant is profiledMemory's block when JEV chooses Rocket project and
// Finance team and ORBIT is lit.
const profilesWant = profilesHead + "Profile: Rocket project\n- Facts:\ncodename: ORBIT (2024/02/01)\n- 2024/02/01: The codename is ORBIT." +
	"\n\nProfile: Finance team\n- 2024/01/10: The budget is 5 million." +
	"\n\nProfile: ORBIT\n- Facts:\nlaunch day: Friday (2024/03/05, earlier 1 values)\ncodename: ORBIT (2024/02/01)\n- 2024/02/01: The codename is ORBIT.\n- 2024/03/01: Launch set for Thursday.\n- 2024/03/05: Launch moved to Friday."

func TestReadSimpleWithProfiles(t *testing.T) {
	for _, needs := range []string{
		// The records fill the View: the linked records are not judged.
		"NEED: the codename",
		// ORBIT alone: the linked launch record joins.
		"nothing stored is needed",
	} {
		views, judged, planned := layerReads(t, profiledMemory(), "layered", needs, profileJudge, ViewConfig{MaxItems: 6}, ViewConfig{MaxItems: 6, Profiles: true})
		plain, profiled := views[0], views[1]
		// The profiles follow the values: the chosen by JEV's answer, then
		// the lit ORBIT. The other blocks and the records are as they were.
		if !slices.Equal(blockIDs(profiled.Index), []string{"1-instructions", "2-topics", "3-timelines", "4-values", "5-profiles"}) || profiled.Index[4].Text != profilesWant {
			t.Fatalf("%s: %q", needs, profiled.Index)
		}
		if !reflect.DeepEqual(plain.Index, profiled.Index[:4]) || !slices.Equal(recordIDs(plain.Records), recordIDs(profiled.Records)) || plain.Truncated != profiled.Truncated {
			t.Fatal(needs, recordIDs(plain.Records), recordIDs(profiled.Records))
		}
		if profiled.Stats.LayerProfiles != 3 || plain.Stats.LayerProfiles != 0 || profiled.Stats.Degraded != nil || profiled.Stats.DecisionCalls != plain.Stats.DecisionCalls+1 {
			t.Fatal(needs, profiled.Stats, plain.Stats)
		}
		checkIndexRefs(t, profiled, map[string][]string{"1-instructions": {"orbit"}, "3-timelines": {"launch", "orbit"}, "4-values": {"orbit"}, "5-profiles": {"orbit", "gone-1", "launch"}})
		// Every request the read makes without profiles it makes with them,
		// byte for byte; profiles add one call of their own.
		entity := func(raw string) bool { return strings.Contains(raw, `"source":"entity"`) }
		own := slices.DeleteFunc(slices.Clone(judged[1]), func(raw string) bool { return !entity(raw) })
		if rest := slices.DeleteFunc(slices.Clone(judged[1]), entity); !slices.Equal(rest, judged[0]) || len(own) != 1 || !slices.Equal(planned[0], planned[1]) {
			t.Fatal(needs, len(judged[0]), len(judged[1]), len(own))
		}
		if !slices.ContainsFunc(judged[0], func(raw string) bool { return strings.Contains(raw, `"source":"topic"`) }) {
			t.Fatal("no layer call")
		}
		// The entity call: the lit by anchor links (ORBIT and Rocket project
		// by two, in the order they lit), then the near Finance team; the
		// needed question only.
		call, questions := requestOf(t, own[0])
		texts := []string{}
		for i, c := range call.State.Candidates {
			if c.ID != fmt.Sprintf("c%d", i) || c.Source != "entity" {
				t.Fatal(c)
			}
			texts = append(texts, c.Text)
		}
		if !slices.Equal(texts, []string{
			"ORBIT: 3 events, 2024/02/01 to 2024/03/05. Latest: 2024/02/01 The codename is ORBIT. / 2024/03/01 Launch set for Thursday. / 2024/03/05 Launch moved to Friday.",
			"Rocket project: 1 events, 2024/02/01 to 2024/02/01. Latest: 2024/02/01 The codename is ORBIT.",
			"Finance team: 1 events, 2024/01/10 to 2024/01/10. Latest: 2024/01/10 The budget is 5 million.",
		}) || len(questions) != 3 || questions["n0"].Instructions != neededQuestion(byID("c0")) {
			t.Fatal(texts, questions)
		}
	}
}

// Both settings: the profiles take the scaled budgets, and the topics' and
// values' call is the one the reference budgets make.
func TestReadSimpleScaledProfiles(t *testing.T) {
	views, judged, _ := layerReads(t, profiledMemory(), "layered", "NEED: the codename", profileJudge,
		ViewConfig{MaxItems: 6, MaxCharacters: 24000}, ViewConfig{MaxItems: 6, MaxCharacters: 24000, Layer: "scaled", Profiles: true})
	if got := views[1].Index[len(views[1].Index)-1]; got.ID != "5-profiles" || got.Text != profilesWant {
		t.Fatalf("%q", got.Text)
	}
	topics := func(requests []string) []string {
		return slices.DeleteFunc(slices.Clone(requests), func(raw string) bool { return !strings.Contains(raw, `"source":"topic"`) })
	}
	if layer := topics(judged[0]); len(layer) != 1 || !slices.Equal(layer, topics(judged[1])) {
		t.Fatal(len(layer))
	}
}

// The linked-record join reads the profiles' records: a profile that shows
// an event no other block shows lets its record be judged and join.
func TestProfilesJoinTheirRecords(t *testing.T) {
	s, _ := agentStore(t)
	seedNote(t, s, "orbit", "codename ORBIT")
	seedNote(t, s, "l1", "linked note l1")
	memory := timelineMemory{
		Threads:    []thread{{ID: "T9", Title: "Side notes"}},
		Events:     []event{{Thread: "T9", Date: "2024/01/01", Text: "Greg mentioned the side project.", Records: []string{"l1"}, Entities: []string{"Greg"}}},
		Facts:      map[string][]factValue{"greg · role": {{Value: "lead", Date: "2024/01/02", Records: []string{"orbit"}, Entities: []string{"Greg"}}}},
		FactOrder:  []string{"greg · role"},
		Directives: []directive{},
	}
	read := func(profiles bool) (View, *scriptedJudge) {
		judge := &scriptedJudge{answer: func(text string, _ int) float64 {
			if text == "linked note l1" {
				return 0.9
			}
			return 0.1
		}}
		r, base := layerRun(t, s, memory, judge, ViewConfig{MaxItems: 5, Profiles: profiles}, "orbit")
		view, err := r.layer(context.Background(), base)
		if err != nil {
			t.Fatal(err)
		}
		return view, judge
	}
	// Greg is lit by the role his value cites; the side note's topic is not
	// chosen, so only his profile shows the event.
	view, judge := read(true)
	if !slices.Equal(recordIDs(view.Records), []string{"orbit", "l1"}) || judge.count() != 3 || view.Records[1].Score != 0.9 {
		t.Fatal(recordIDs(view.Records), judge.count())
	}
	if got := view.Index[len(view.Index)-1]; got.Text != profilesHead+"Profile: Greg\n- Facts:\ngreg · role: lead (2024/01/02)\n- 2024/01/01: Greg mentioned the side project." || !slices.Equal(linkedIDs(got), []string{"orbit", "l1"}) {
		t.Fatalf("%q %v", got.Text, linkedIDs(got))
	}
	if view, judge = read(false); !slices.Equal(recordIDs(view.Records), []string{"orbit"}) || judge.count() != 1 {
		t.Fatal(recordIDs(view.Records), judge.count())
	}
}

func TestReadSimpleProfilesDegrade(t *testing.T) {
	ctx := context.Background()
	question := ReadRequest{Question: "What is the codename?"}
	store := func(memory timelineMemory) (*records.Store, *Overlay) {
		s, overlay := layerStore(t)
		state, token, err := overlay.loadTimelines(ctx, records.Scope{})
		if err != nil {
			t.Fatal(err)
		}
		state.Memory = memory
		if _, err := overlay.saveTimelines(ctx, records.Scope{}, token, state); err != nil {
			t.Fatal(err)
		}
		return s, overlay
	}
	s, overlay := store(profiledMemory())
	plain, err := layerAgent(t, s, overlay, Config{View: &ViewConfig{MaxItems: 6}}, codenamePlanner("NEED: the codename"), profileJudge()).Read(ctx, question)
	if err != nil {
		t.Fatal(err)
	}
	// A failed entity call shows no profiles; the rest of the layer stands.
	judge := profileJudge()
	failing := decisionFunc(func(ctx context.Context, state any, questions map[string]Question) (map[string]float64, Usage, error) {
		if raw, _ := json.Marshal(state); strings.Contains(string(raw), `"source":"entity"`) {
			return nil, Usage{}, errors.New("judge down")
		}
		return judge.Decide(ctx, state, questions)
	})
	view, err := layerAgent(t, s, overlay, Config{View: &ViewConfig{MaxItems: 6, Profiles: true}}, codenamePlanner("NEED: the codename"), failing).Read(ctx, question)
	if err != nil || !reflect.DeepEqual(view.Index, plain.Index) || !slices.Equal(view.Stats.Degraded, []string{"layer-profiles"}) || view.Stats.LayerProfiles != 0 {
		t.Fatal(blockIDs(view.Index), view.Stats.Degraded, err)
	}
	// A call the budget cannot pay for is not made (nor, then, the linked
	// records' judgement: expand).
	for _, tc := range []struct {
		left     int
		degraded []string
		calls    int
	}{{1, []string{"layer-profiles", "expand"}, 1}, {0, []string{"layer", "layer-profiles", "expand"}, 0}} {
		judge := profileJudge()
		a := layerAgent(t, s, overlay, Config{View: &ViewConfig{MaxItems: 6, Profiles: true}}, codenamePlanner("NEED: the codename"), judge)
		r := &simpleRun{agent: a, req: ReadRequest{MaxItems: 6}, dialogue: []Message{user("What is the codename?")}, items: map[string]*simpleItem{}}
		r.stats.DecisionCalls = simpleDecisionBudget - tc.left
		view, err := r.layer(ctx, View{Records: []Evidence{}, Needs: []Need{}})
		if err != nil || !slices.Equal(view.Stats.Degraded, tc.degraded) || judge.count() != tc.calls || slices.Contains(blockIDs(view.Index), "5-profiles") {
			t.Fatal(tc, view.Stats.Degraded, judge.count(), blockIDs(view.Index), err)
		}
	}
	// A canceled read fails.
	canceled, cancel := context.WithCancel(ctx)
	cancelling := decisionFunc(func(_ context.Context, state any, questions map[string]Question) (map[string]float64, Usage, error) {
		if raw, _ := json.Marshal(state); strings.Contains(string(raw), `"source":"entity"`) {
			cancel()
			return nil, Usage{}, canceled.Err()
		}
		return profileJudge().Decide(canceled, state, questions)
	})
	if _, err = layerAgent(t, s, overlay, Config{View: &ViewConfig{MaxItems: 6, Profiles: true}}, codenamePlanner("NEED: the codename"), cancelling).Read(canceled, question); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// A timelines state names no entity: no profiles, no call, nothing
	// degraded.
	s, overlay = store(layerMemory())
	judge = profileJudge()
	view, err = layerAgent(t, s, overlay, Config{View: &ViewConfig{MaxItems: 6, Profiles: true}}, codenamePlanner("NEED: the codename"), judge).Read(ctx, question)
	if err != nil || len(view.Index) != 4 || view.Stats.Degraded != nil || view.Stats.DecisionCalls != 3 || slices.ContainsFunc(judge.calls, func(c judgeCall) bool { return strings.Contains(c.Raw, `"source":"entity"`) }) {
		t.Fatal(blockIDs(view.Index), view.Stats, err)
	}
}

func TestNearestEntities(t *testing.T) {
	s, _ := agentStore(t)
	server := &embeddingServer{}
	url := server.start(t, func(text string) []float64 {
		text = strings.TrimPrefix(strings.TrimPrefix(text, "search_query: "), "search_document: ")
		switch {
		case strings.HasPrefix(text, "Who"):
			return []float64{1, 0, 0}
		case strings.HasPrefix(text, "Greg"):
			return []float64{0.6, 0.8, 0}
		case strings.HasPrefix(text, "Acme"):
			return []float64{1, 0, 0}
		case strings.HasPrefix(text, "Odd"):
			return []float64{1, 0}
		}
		return []float64{0, 0, 1}
	})
	config := Config{Version: 1, Strategy: "simple", Embedding: &records.EmbeddingConfig{URL: url, Model: "test", QueryPrefix: "search_query: ", DocumentPrefix: "search_document: "}}
	a, err := New(s, config, Models{Planner: completeFunc(nil), Judge: decisionFunc(fixtureJudge)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	texts := []string{"Cabin Co: x", "Greg: y", "Acme: z"}
	// By meaning: dot products 0, 0.6, 1, best first; the texts are stored.
	r := testRun(a, user("Who?"))
	if got := r.nearestEntities(context.Background(), "Who?", texts, false); !slices.Equal(got, []int{2, 1, 0}) || r.stats.Degraded != nil {
		t.Fatal(got, r.stats.Degraded)
	}
	if vectors, err := s.DerivedVectors(context.Background(), texts, *config.Embedding, false); err != nil || slices.ContainsFunc(vectors, func(v []float32) bool { return v == nil }) {
		t.Fatal(vectors, err)
	}
	// A vector of another size: shared words, degraded.
	r = testRun(a, user("Who works at Acme?"))
	if got := r.nearestEntities(context.Background(), "Who works at Acme?", []string{"Odd: works", "Acme: works at"}, false); !slices.Equal(got, []int{1, 0}) || !slices.Equal(r.stats.Degraded, []string{"layer-lexical"}) {
		t.Fatal(got, r.stats.Degraded)
	}
	// When the topics fell back to shared words, the entities are not
	// embedded.
	server.mu.Lock()
	before := len(server.inputs)
	server.mu.Unlock()
	r = testRun(a, user("Who?"))
	if got := r.nearestEntities(context.Background(), "greg", []string{"Never embedded: greg", "Other"}, true); !slices.Equal(got, []int{0}) || r.stats.Degraded != nil {
		t.Fatal(got, r.stats.Degraded)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.inputs) != before {
		t.Fatal(server.inputs[before:])
	}
}

func TestWarmLayerEmbedsEntityTexts(t *testing.T) {
	ctx := context.Background()
	server := &embeddingServer{}
	url := server.start(t, func(string) []float64 { return []float64{1, 0} })
	embedding := &records.EmbeddingConfig{URL: url, Model: "test", QueryPrefix: "search_query: ", DocumentPrefix: "search_document: "}
	m := profiledMemory()
	entities := []string{}
	for _, e := range memoryEntities(m).list {
		entities = append(entities, "search_document: "+entityText(m, e))
	}
	warm := func(profiles bool) []string {
		s, overlay := layerStore(t)
		state, token, err := overlay.loadTimelines(ctx, records.Scope{})
		if err != nil {
			t.Fatal(err)
		}
		state.Memory = m
		if _, err := overlay.saveTimelines(ctx, records.Scope{}, token, state); err != nil {
			t.Fatal(err)
		}
		server.mu.Lock()
		before := len(server.inputs)
		server.mu.Unlock()
		if err := layerAgent(t, s, overlay, Config{Embedding: embedding, View: &ViewConfig{Profiles: profiles}}, completeFunc(nil), layerJudge()).WarmLayer(ctx, records.Scope{}); err != nil {
			t.Fatal(err)
		}
		server.mu.Lock()
		defer server.mu.Unlock()
		return slices.Clone(server.inputs[before:])
	}
	with, without := warm(true), warm(false)
	if len(entities) != 4 || len(with) != len(without)+4 {
		t.Fatal(len(with), len(without))
	}
	for _, text := range entities {
		if !slices.Contains(with, text) || slices.Contains(without, text) {
			t.Fatal(text)
		}
	}
}

// Gate and target count only the records within the View's characters
// less the most the blocks can take: with profiles their block's 3551
// (3500, the 2 over and the header) counts, scaled with "scaled".
func TestProfilesBoundTheWouldShowView(t *testing.T) {
	for _, tc := range []struct {
		view  ViewConfig
		bound int
	}{
		{ViewConfig{MaxCharacters: 24000}, 8059},
		{ViewConfig{MaxCharacters: 24000, Layer: "scaled"}, 16059},
		{ViewConfig{MaxCharacters: 24000, Profiles: true}, 8059 + 3551},
		{ViewConfig{MaxCharacters: 24000, Layer: "scaled", Profiles: true}, 16059 + 7051},
	} {
		if got := scaleAgent(t, tc.view).layerBound(); got != tc.bound {
			t.Error(tc.view, got)
		}
	}
	scored := []*simpleItem{scoredItem("a", 0.9, 0, sized("a", 4000)), scoredItem("b", 0.8, 0, sized("b", 4000)), scoredItem("c", 0.7, 0, sized("c", 4000))}
	shown := func(view ViewConfig) []string {
		r := &simpleRun{agent: scaleAgent(t, view), req: ReadRequest{MaxItems: 16}, items: map[string]*simpleItem{}}
		got, err := r.wouldShow(context.Background(), scored)
		if err != nil {
			t.Fatal(err)
		}
		return itemIDs(got)
	}
	// 21,000 less 8,059 leaves 12,941; less 11,610, 9,390.
	if got := shown(ViewConfig{MaxCharacters: 21000}); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatal(got)
	}
	if got := shown(ViewConfig{MaxCharacters: 21000, Profiles: true}); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatal(got)
	}
	// 24,000 less 23,110 leaves 890.
	if got := shown(ViewConfig{MaxCharacters: 24000, Layer: "scaled", Profiles: true}); len(got) != 0 {
		t.Fatal(got)
	}
}

// A judgement of more than 40 entities takes a call per 40; one the budget
// cannot pay for whole is not begun, since a part shows nothing.
func TestProfilesJudgementIsPaidWhole(t *testing.T) {
	s, _ := agentStore(t)
	seedNote(t, s, "orbit", "codename ORBIT")
	memory := timelineMemory{Threads: []thread{{ID: "T1", Title: "Codename"}}, Facts: map[string][]factValue{}, FactOrder: []string{}, Directives: []directive{}}
	for i := range 45 {
		memory.Events = append(memory.Events, event{Thread: "T1", Date: "2024/01/01", Text: fmt.Sprintf("Person %02d met.", i), Records: []string{"orbit"}, Entities: []string{fmt.Sprintf("Person %02d", i)}})
	}
	for _, tc := range []struct {
		left, calls, shown int
		degraded           []string
	}{
		// The layer's call, then the entities' two; the 45 profiles (of 49
		// units, with the separators) fit the 3500.
		{3, 3, 45, nil},
		// The layer's call leaves one: the entities' are not begun.
		{2, 1, 0, []string{"layer-profiles"}},
	} {
		judge := &scriptedJudge{answer: func(string, int) float64 { return 0.1 }}
		r, base := layerRun(t, s, memory, judge, ViewConfig{MaxItems: 1, Profiles: true}, "orbit")
		r.stats.DecisionCalls = simpleDecisionBudget - tc.left
		view, err := r.layer(context.Background(), base)
		if err != nil || judge.count() != tc.calls || view.Stats.LayerProfiles != tc.shown || !slices.Equal(view.Stats.Degraded, tc.degraded) {
			t.Fatal(tc, judge.count(), view.Stats.LayerProfiles, view.Stats.Degraded, err)
		}
	}
}
