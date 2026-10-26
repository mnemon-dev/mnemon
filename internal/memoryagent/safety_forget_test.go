package memoryagent

import (
	"context"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// TestForgettingWithdrawsWhatWasForgotten: a key whose last value is a
// forget mark shows only that it was forgotten; the records of its values
// and of the mark are withheld; an event or a directive that cited only
// those records goes, one that cited others keeps them; another key keeps
// its value without the withheld records; a value told after a mark shows.
func TestForgettingWithdrawsWhatWasForgotten(t *testing.T) {
	m := timelineMemory{
		Threads: []thread{{ID: "T1", Title: "food"}},
		Events: []event{
			{Thread: "T1", Date: "2024/01/02", Text: "said she is vegetarian", Records: []string{"r1"}},
			{Thread: "T1", Date: "2024/01/03", Text: "planned a dinner", Records: []string{"r1", "r2"}},
			{Thread: "T1", Date: "2024/01/04", Text: "no records", Records: []string{}},
		},
		Facts: map[string][]factValue{
			"user · diet":  {{Value: "vegetarian", Date: "2024/01/02", Records: []string{"r1"}}, {Value: "her diet", Date: "2024/02/01", Records: []string{"r5"}, Forget: true}},
			"user · city":  {{Value: "Lisbon", Date: "2024/01/02", Records: []string{"r1", "r3"}}},
			"user · phone": {{Value: "old", Records: []string{"r6"}}, {Value: "forget it", Records: []string{"r7"}, Forget: true}, {Value: "new", Date: "2024/03/01", Records: []string{"r8"}}},
		},
		FactOrder:  []string{"user · diet", "user · city", "user · phone"},
		Directives: []directive{{Text: "never mention my diet", Records: []string{"r5"}}, {Text: "be brief", Records: []string{"r4"}}},
	}
	out, withheld := forgetting(m)
	if want := map[string]bool{"r1": true, "r5": true, "r6": true, "r7": true}; !maps.Equal(withheld, want) {
		t.Fatalf("withheld %v", withheld)
	}
	if got := valueText("user · diet", out.Facts["user · diet"]); got != "user · diet: 2024/02/01 forgotten at the user's request" {
		t.Fatalf("diet: %q", got)
	}
	if city := out.Facts["user · city"]; len(city) != 1 || city[0].Value != "Lisbon" || !slices.Equal(city[0].Records, []string{"r3"}) {
		t.Fatalf("city: %+v", city)
	}
	if phone := out.Facts["user · phone"]; len(phone) != 1 || phone[0].Value != "new" {
		t.Fatalf("phone: %+v", phone)
	}
	texts := []string{}
	for _, e := range out.Events {
		texts = append(texts, e.Text+"="+strings.Join(e.Records, ","))
	}
	if strings.Join(texts, " | ") != "planned a dinner=r2 | no records=" {
		t.Fatalf("events: %v", texts)
	}
	if len(out.Directives) != 1 || out.Directives[0].Text != "be brief" {
		t.Fatalf("directives: %+v", out.Directives)
	}
	// The memory forgetting reads is not changed, and one without a mark is
	// returned as it is.
	if len(m.Events) != 3 || m.Facts["user · city"][0].Records[0] != "r1" {
		t.Fatal("forgetting changed its input")
	}
	delete(m.Facts, "user · diet")
	delete(m.Facts, "user · phone")
	if _, none := forgetting(m); none != nil {
		t.Fatal("a memory without a mark withheld", none)
	}
}

// TestForgetMarkIsAskedAndRead: under safety.forget the extraction prompt
// asks for the mark, and a reply's mark is kept; without it the prompt is
// the one it was.
func TestForgetMarkIsAskedAndRead(t *testing.T) {
	off := (&Agent{}).extractionFold().system
	on := (&Agent{config: Config{Safety: &SafetyConfig{Forget: true}}}).extractionFold().system
	if strings.Contains(off, "forget") || !strings.Contains(on, `"forget" set to true`) || !strings.Contains(on, `"conflict":false,"forget":false}`) {
		t.Fatal("the forget edit")
	}
	memory := emptyTimelineState().Memory
	batch := []pendingRecord{{ID: "rec-1", Text: "user: please forget that I am vegetarian"}}
	reply := `{"events":[],"facts":[{"key":"user · diet","value":"being vegetarian","date":"2024/02/01","records":["r0"],"conflict":false,"forget":true}],"directives":[]}`
	if err := applyReply(&memory, batch, reply); err != nil {
		t.Fatal(err)
	}
	if values := memory.Facts["user · diet"]; len(values) != 1 || !values[0].Forget || !slices.Equal(values[0].Records, []string{"rec-1"}) {
		t.Fatalf("parsed %+v", values)
	}
}

// TestReadSimpleForgetWithholds: with the codename forgotten (its value from
// a2, the request in b0), a read under safety.forget shows neither record,
// no event made from a2 alone, and the key as forgotten; without the check
// the codename reaches the View.
func TestReadSimpleForgetWithholds(t *testing.T) {
	s, dir := expandFixtureIn(t)
	overlay, err := OpenOverlay(filepath.Join(dir, "memory-index.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { overlay.Close() })
	state := emptyTimelineState()
	state.Memory = eventsMemory()
	state.Memory.Facts = map[string][]factValue{"rocket project · codename": {
		{Value: "ORBIT", Date: "2023/05/08", Records: []string{"a2"}},
		{Value: "the codename", Date: "2023/05/09", Records: []string{"b0"}, Forget: true},
	}}
	state.Memory.FactOrder = []string{"rocket project · codename"}
	if _, err := overlay.saveTimelines(context.Background(), records.Scope{}, "", state); err != nil {
		t.Fatal(err)
	}
	read := func(forget bool) (View, string) {
		t.Helper()
		config := Config{View: &ViewConfig{MaxItems: 100, MaxCharacters: 240000, Order: "time"}, Consolidation: &ConsolidationConfig{Mode: "layered"}}
		if forget {
			config.Safety = &SafetyConfig{Forget: true}
		}
		view, err := layerAgent(t, s, overlay, config, expandPlanner(), expandJudge()).Read(context.Background(), ReadRequest{Question: "What is the codename?"})
		if err != nil {
			t.Fatal(err)
		}
		texts := []string{}
		for _, block := range view.Index {
			texts = append(texts, block.Text)
		}
		for _, record := range view.Records {
			texts = append(texts, record.ID+": "+record.Text)
		}
		return view, strings.Join(texts, "\n")
	}
	if _, shown := read(false); !strings.Contains(shown, "ORBIT") {
		t.Fatalf("without the check the codename is not shown:\n%s", shown)
	}
	view, shown := read(true)
	if strings.Contains(shown, "ORBIT") || strings.Contains(shown, "a2:") || strings.Contains(shown, "b0:") {
		t.Fatalf("a forgotten codename is shown:\n%s", shown)
	}
	if len(view.Stats.Degraded) != 0 {
		t.Fatal("degraded:", view.Stats.Degraded)
	}
}
