package memoryagent

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// view.profiles adds a fifth block to the timelines layer: the memory's
// events and values grouped by the entities layered extraction names.

func TestEntityKey(t *testing.T) {
	for name, want := range map[string]string{
		"Greg Hall":         "greg hall",
		"  Greg \t Hall\n ": "greg hall",
		"Python 3.11":       "python",
		"python 3.12.1":     "python",
		"React v18.2":       "react",
		"V2.0 Engine":       "engine",
		"Node.js v20.10.0":  "node.js",
		"Django (4.2)":      "django",
		`"Greg"`:            "greg",
		"Acme, Inc.":        "acme, inc",
		"(ORBIT)":           "orbit",
		"C#":                "c#",
		"C++":               "c++",
		// A version inside a word is no version token.
		"GPT-4.1-mini": "gpt-4.1-mini",
		"python3.11":   "python3.11",
		// Without a dot a number is no version.
		"React 18": "react 18",
		"Room 101": "room 101",
		// Nothing but a version or punctuation names nothing.
		"3.11": "", "v2.0": "", "...": "", "": "", " ": "", "« »": "",
	} {
		if got := entityKey(name); got != want {
			t.Errorf("entityKey(%q) = %q, want %q", name, got, want)
		}
	}
}

// profileMemory names five entities, events before values: Greg Hall (by
// three spellings), Cabin Co, Acme (by two), Greg, Python (by versions), and
// HR Dept, which only a value names.
func profileMemory() timelineMemory {
	return timelineMemory{
		Threads: []thread{{ID: "T1", Title: "Cabin trip"}, {ID: "T2", Title: "Job"}},
		Events: []event{
			{Thread: "T1", Date: "2024/01/05", Text: "Greg Hall booked the cabin.", Records: []string{"r1"}, Entities: []string{"Greg Hall", "Cabin Co"}},
			{Thread: "T2", Date: "2024/01/03", Text: "Started at Acme.", Records: []string{"r2"}, Entities: []string{"Acme", "acme"}},
			{Thread: "T1", Date: "2024/01/02", Text: "Greg suggested a cabin trip.", Records: []string{"r3"}, Entities: []string{"Greg"}},
			{Thread: "T1", Date: "2024/01/01", Text: "Greg Hall found the cabin listing.", Records: []string{"r4", "r1"}, Entities: []string{"greg hall", "Greg  Hall"}},
			{Thread: "T2", Date: "2024/01/04", Text: "Moved to Python 3.12.", Records: []string{"r5"}, Entities: []string{"Python 3.12", "Acme", "3.12"}},
		},
		Facts: map[string][]factValue{
			"cabin trip · date": {
				{Value: "May 3", Date: "2024/01/02", Records: []string{"r3"}, Entities: []string{"Greg"}},
				{Value: "May 3", Date: "2024/01/05", Records: []string{"r1"}, Entities: []string{"Greg Hall"}},
				{Value: "May 10", Date: "2024/01/09", Records: []string{"r4"}, Entities: []string{"Greg Hall"}},
			},
			"job · language": {{Value: "Python", Date: "2024/01/04", Records: []string{"r5"}, Entities: []string{"python 3.11", "Python"}}},
			"job · title": {
				{Value: "Engineer", Date: "2024/01/03", Records: []string{"r2"}, Entities: []string{"HR Dept"}},
				{Value: "Manager", Records: []string{"r6"}, Entities: []string{"Acme"}},
			},
		},
		FactOrder:  []string{"cabin trip · date", "job · language", "job · title"},
		Directives: []directive{},
	}
}

func entityKeys(entities []*profileEntity) []string {
	out := []string{}
	for _, e := range entities {
		out = append(out, e.key)
	}
	return out
}

func TestMemoryEntities(t *testing.T) {
	m := profileMemory()
	x := memoryEntities(m)
	// In the order first named, events before values.
	if !slices.Equal(entityKeys(x.list), []string{"greg hall", "cabin co", "acme", "greg", "python", "hr dept"}) {
		t.Fatal(entityKeys(x.list))
	}
	for key, want := range map[string]profileEntity{
		// "Greg Hall" four times (one collapsed), "greg hall" once.
		"greg hall": {name: "Greg Hall", events: []int{3, 0}, keys: []string{"cabin trip · date"}},
		"cabin co":  {name: "Cabin Co", events: []int{0}, keys: nil},
		"acme":      {name: "Acme", events: []int{1, 4}, keys: []string{"job · title"}},
		"greg":      {name: "Greg", events: []int{2}, keys: []string{"cabin trip · date"}},
		// "Python" twice (once without its version), "python" once.
		"python":  {name: "Python", events: []int{4}, keys: []string{"job · language"}},
		"hr dept": {name: "HR Dept", events: nil, keys: []string{"job · title"}},
	} {
		e := x.byKey[key]
		if e == nil || e.name != want.name || !slices.Equal(e.events, want.events) || !slices.Equal(e.keys, want.keys) {
			t.Errorf("%s: %+v, want %+v", key, e, want)
		}
	}
	// Each item names an entity once.
	if got := entityKeys(x.events[1]); !slices.Equal(got, []string{"acme"}) {
		t.Fatal(got)
	}
	if got := entityKeys(x.events[4]); !slices.Equal(got, []string{"python", "acme"}) {
		t.Fatal(got)
	}
	if got := entityKeys(x.values["job · title"][1]); !slices.Equal(got, []string{"acme"}) || len(x.values["cabin trip · date"]) != 3 {
		t.Fatal(got)
	}
	// Equal counts: the spelling seen first.
	tie := timelineMemory{Events: []event{{Entities: []string{"ORBIT"}}, {Entities: []string{"Orbit"}}, {Entities: []string{"orbit 2.0"}}}, Facts: map[string][]factValue{}}
	if got := memoryEntities(tie).byKey["orbit"]; got == nil || got.name != "ORBIT" || !slices.Equal(got.events, []int{0, 1, 2}) {
		t.Fatal(got)
	}
	// A timelines state names none.
	if x := memoryEntities(layerMemory()); len(x.list) != 0 {
		t.Fatal(entityKeys(x.list))
	}
}

func TestEntityTexts(t *testing.T) {
	m := profileMemory()
	for key, want := range map[string]string{
		"cabin trip · date": "cabin trip · date: May 10 (2024/01/09, earlier 1 values)",
		"job · language":    "job · language: Python (2024/01/04)",
		// No date: none shown.
		"job · title": "job · title: Manager (earlier 1 values)",
	} {
		if got := factLine(key, m.Facts[key]); got != want {
			t.Errorf("%q, want %q", got, want)
		}
	}
	// Only other values count, each once; a repeated latest value is none.
	for _, tc := range []struct {
		values []factValue
		want   string
	}{
		{[]factValue{{Value: "a", Date: "d1"}, {Value: "b", Date: "d2"}, {Value: "a", Date: "d3"}, {Value: "c", Date: "d4"}, {Value: "a", Date: "d5"}}, "k: a (d5, earlier 2 values)"},
		{[]factValue{{Value: "a", Date: "d1"}, {Value: "a", Date: "d2"}}, "k: a (d2)"},
		{[]factValue{{Value: "a"}}, "k: a"},
	} {
		if got := factLine("k", tc.values); got != tc.want {
			t.Errorf("%q, want %q", got, tc.want)
		}
	}
	x := memoryEntities(m)
	// Built like a topic's text, over the events by date.
	if got := entityText(m, x.byKey["greg hall"]); got != "Greg Hall: 2 events, 2024/01/01 to 2024/01/05. Latest: 2024/01/01 Greg Hall found the cabin listing. / 2024/01/05 Greg Hall booked the cabin." {
		t.Fatal(got)
	}
	// An entity only values name: its keys' fact lines.
	if got := entityText(m, x.byKey["hr dept"]); got != "HR Dept: 1 facts. Latest: job · title: Manager (earlier 1 values)" {
		t.Fatal(got)
	}
}

func TestLitEntities(t *testing.T) {
	m := profileMemory()
	x := memoryEntities(m)
	anchors := map[string]bool{"r1": true, "r4": true}
	var lit litSets
	lit.Threads, lit.Counts, lit.Events, lit.Values = lightUp(m, anchors)
	keys, counts := litEntities(m, x, lit, anchors)
	// Events 0 and 3 light Greg Hall (1 + 2 links) and Cabin Co (1); the lit
	// key's values light Greg (0 links) and Greg Hall (1 + 1).
	if !slices.Equal(keys, []string{"greg hall", "cabin co", "greg"}) || !reflect.DeepEqual(counts, map[string]int{"greg hall": 5, "cabin co": 1, "greg": 0}) {
		t.Fatal(keys, counts)
	}
	// Candidates: the lit by anchor links (ties first lit), then the near
	// ones, each once; the order: JEV's yes by score (ties in candidate
	// order), then the lit below 0.5 by anchor links.
	ranked := rankedEntities(keys, counts)
	candidates, _ := layerCandidates(litSets{Threads: ranked}, entityKeys(x.list), nil, []int{5, 0, 2}, nil)
	if !slices.Equal(candidates, []string{"greg hall", "cabin co", "greg", "hr dept", "acme"}) {
		t.Fatal(candidates)
	}
	score := map[string]float64{"greg hall": 0.2, "cabin co": 0.6, "greg": 0.4, "hr dept": 0.9, "acme": 0.3}
	order, _ := layerOrder(candidates, nil, litSets{Threads: keys, Counts: counts}, score, nil)
	if !slices.Equal(order, []string{"hr dept", "cabin co", "greg hall", "greg"}) {
		t.Fatal(order)
	}
	// Equal links keep the order they first lit up.
	if got := rankedEntities([]string{"a", "b", "c"}, map[string]int{"a": 1, "b": 2, "c": 2}); !slices.Equal(got, []string{"b", "c", "a"}) {
		t.Fatal(got)
	}
}

func TestComposeProfiles(t *testing.T) {
	m := profileMemory()
	x := memoryEntities(m)
	anchors := map[string]bool{"r1": true, "r4": true}
	var lit litSets
	lit.Threads, lit.Counts, lit.Events, lit.Values = lightUp(m, anchors)
	block, shown := composeProfiles(m, x, []string{"hr dept", "cabin co", "greg hall", "greg"}, lit, referenceBudgets)
	want := profilesHead +
		"Profile: HR Dept\n- Facts:\njob · title: Manager (earlier 1 values)" +
		"\n\nProfile: Cabin Co\n- 2024/01/05: Greg Hall booked the cabin." +
		"\n\nProfile: Greg Hall\n- Facts:\ncabin trip · date: May 10 (2024/01/09, earlier 1 values)\n- 2024/01/01: Greg Hall found the cabin listing.\n- 2024/01/05: Greg Hall booked the cabin." +
		"\n\nProfile: Greg\n- Facts:\ncabin trip · date: May 10 (2024/01/09, earlier 1 values)\n- 2024/01/02: Greg suggested a cabin trip."
	if shown != 4 || block.Text != want || block.ID != "5-profiles" || block.Kind != "5-profiles" || block.Title != "" || block.Date != "" {
		t.Fatalf("%d %q", shown, block.Text)
	}
	// The records of the latest values and the events shown, each once.
	if !slices.Equal(linkedIDs(block), []string{"r6", "r1", "r4", "r3"}) {
		t.Fatal(linkedIDs(block))
	}
	if block, shown := composeProfiles(m, x, nil, lit, referenceBudgets); shown != 0 || block.ID != "" {
		t.Fatal(block, shown)
	}
	if jsLen(profilesHead) != 49 {
		t.Fatal(jsLen(profilesHead))
	}
}

// eventProfiles is a memory whose entities A, B, C and D each name one event
// of the given size of text, and the four profiles' block.
func eventProfiles(t *testing.T, sizes []int, b layerBudgets) (string, int) {
	t.Helper()
	m := timelineMemory{Threads: []thread{{ID: "T1", Title: "T"}}, Facts: map[string][]factValue{}, FactOrder: []string{}}
	order := []string{}
	for i, size := range sizes {
		name := string(rune('A' + i))
		m.Events = append(m.Events, event{Thread: "T1", Date: "2024/01/01", Text: sized(name, size), Entities: []string{name}})
		order = append(order, entityKey(name))
	}
	block, shown := composeProfiles(m, memoryEntities(m), order, litSets{}, b)
	return block.Text, shown
}

// A profile takes at most a timeline's 1500, the block 3500; one is begun in
// a room of exactly 300 (A and B fill 1500 each and C 196, with the
// separators 3200), not in 299. The header is not counted.
func TestComposeProfilesBudgets(t *testing.T) {
	// "Profile: X" is 10, an event line 15 and its text.
	text, shown := eventProfiles(t, []int{1475, 1475, 171, 4}, referenceBudgets)
	if shown != 4 || !strings.HasSuffix(text, "\n\nProfile: D\n- 2024/01/01: "+sized("D", 4)) || jsLen(text) != jsLen(profilesHead)+3200+2+29 {
		t.Fatalf("room 300: %d %d", shown, jsLen(text))
	}
	if text, shown = eventProfiles(t, []int{1475, 1475, 172, 4}, referenceBudgets); shown != 3 || strings.Contains(text, "Profile: D") {
		t.Fatal("room 299 began a profile", shown)
	}
	// A profile over its room is cut, though the block has room left: one of
	// 1476 shows only its title, so it is left out, and the next is shown.
	if text, shown = eventProfiles(t, []int{1476, 4}, referenceBudgets); shown != 1 || strings.Contains(text, "Profile: A") || !strings.Contains(text, "Profile: B") {
		t.Fatal(shown, text)
	}
	// Under view.layer "scaled" both budgets scale (twice: 3000 and 7000);
	// the floor of 300 does not.
	if text, shown = eventProfiles(t, []int{2975, 2975, 671, 4}, referenceBudgets.times(2)); shown != 4 || jsLen(text) != jsLen(profilesHead)+6700+2+29 {
		t.Fatal(shown, jsLen(text))
	}
	if _, shown = eventProfiles(t, []int{2975, 2975, 672, 4}, referenceBudgets.times(2)); shown != 3 {
		t.Fatal(shown)
	}
	if _, shown = eventProfiles(t, []int{1476, 1476, 1476}, referenceBudgets.times(2)); shown != 3 {
		t.Fatal(shown)
	}
}

// A profile over its room keeps its anchor lines first (the facts of the lit
// keys, then the lit events), then the others newest first, facts and events
// alike, each that fits, shown in order; "- Facts:" counts only with a fact.
func TestComposeProfilesKeepsAnchorsThenNewest(t *testing.T) {
	value := func(key, date string) []factValue {
		return []factValue{{Value: sized("v"+key, 300), Date: date, Records: []string{"r-" + key}, Entities: []string{"E"}}}
	}
	m := timelineMemory{
		Threads: []thread{{ID: "T1", Title: "T"}},
		Events: []event{
			{Thread: "T1", Date: "2024/01/08", Text: sized("e3", 300), Records: []string{"r-e3"}, Entities: []string{"E"}},
			{Thread: "T1", Date: "2024/01/02", Text: sized("e1", 300), Records: []string{"r-e1"}, Entities: []string{"E"}},
			{Thread: "T1", Date: "2024/01/04", Text: sized("e2", 300), Records: []string{"r-e2"}, Entities: []string{"E"}},
		},
		Facts:     map[string][]factValue{"k1": value("k1", "2024/01/01"), "k2": value("k2", "2024/01/05"), "k3": value("k3", "2024/01/09")},
		FactOrder: []string{"k1", "k2", "k3"},
	}
	x := memoryEntities(m)
	lit := litSets{Events: map[int]bool{1: true}, Values: []string{"k1"}}
	// Title 10, "- Facts:" 9, fact lines 318, event lines 315: 1918 in all.
	// Anchors k1 and e1 (652), then k3 (01/09) and e3 (01/08) fit (1285);
	// k2 and e2 would pass 1500.
	block, shown := composeProfiles(m, x, []string{"e"}, lit, referenceBudgets)
	want := profilesHead + "Profile: E\n- Facts:\n" + factLine("k1", m.Facts["k1"]) + "\n" + factLine("k3", m.Facts["k3"]) +
		"\n- 2024/01/02: " + sized("e1", 300) + "\n- 2024/01/08: " + sized("e3", 300)
	if shown != 1 || block.Text != want || jsLen(block.Text) != jsLen(profilesHead)+1285 {
		t.Fatalf("%d %q", jsLen(block.Text), block.Text)
	}
	if !slices.Equal(linkedIDs(block), []string{"r-k1", "r-k3", "r-e1", "r-e3"}) {
		t.Fatal(linkedIDs(block))
	}
	// Whole within its room: everything, facts in key order, events by date.
	block, _ = composeProfiles(m, x, []string{"e"}, lit, referenceBudgets.times(2))
	if !strings.HasSuffix(block.Text, "\n- 2024/01/02: "+sized("e1", 300)+"\n- 2024/01/04: "+sized("e2", 300)+"\n- 2024/01/08: "+sized("e3", 300)) ||
		!slices.Equal(linkedIDs(block), []string{"r-k1", "r-k2", "r-k3", "r-e1", "r-e2", "r-e3"}) {
		t.Fatalf("%q", block.Text)
	}
	// No fact that fits (10 + 9 + 1488 pass 1500, by the header): no "- Facts:" line.
	m.Facts = map[string][]factValue{"k1": {{Value: sized("big", 1470), Date: "2024/01/09", Records: []string{"r-big"}, Entities: []string{"E"}}}}
	m.FactOrder = []string{"k1"}
	block, shown = composeProfiles(m, memoryEntities(m), []string{"e"}, litSets{Events: map[int]bool{}}, referenceBudgets)
	if shown != 1 || strings.Contains(block.Text, "Facts") || !strings.Contains(block.Text, sized("e1", 300)) || slices.Contains(linkedIDs(block), "r-big") {
		t.Fatalf("%q", block.Text)
	}
}
