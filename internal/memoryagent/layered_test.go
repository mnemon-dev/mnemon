package memoryagent

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestLayeredSystemPromptEdits(t *testing.T) {
	edits := []struct{ old, new string }{
		{`Reuse an existing thread id when the event continues that sub-topic; otherwise start a new thread ("new: <short title>").`,
			`Name the thread with a short title ("new: <short title>") and use the same title for every event of that sub-topic in this batch.`},
		{`When an existing key changes, repeat that exact key with the new value.`,
			`When a key's value changes within the batch, repeat that exact key with the new value.`},
		{`contradicts an earlier one and`, `contradicts an earlier one in this batch and`},
		{`Use only what the records say.`,
			`For each event and fact, list in "entities" the names of the people, organizations, places, products or documents it is about. Use only what the records say.`},
		{`{"events":[{"thread":"T3","date":"YYYY/MM/DD","text":"...","records":["r12"]}],"facts":[{"key":"...","value":"...","date":"YYYY/MM/DD","records":["r15"],"conflict":false}],"directives":[{"text":"...","date":"YYYY/MM/DD","records":["r20"]}]}`,
			`{"events":[{"thread":"new: <short title>","date":"YYYY/MM/DD","text":"...","entities":["..."],"records":["r12"]}],"facts":[{"key":"...","value":"...","date":"YYYY/MM/DD","entities":["..."],"records":["r15"],"conflict":false}],"directives":[{"text":"...","date":"YYYY/MM/DD","records":["r20"]}]}`},
	}
	want := timelineSystem
	for _, edit := range edits {
		if strings.Count(want, edit.old) != 1 {
			t.Fatalf("the author's prompt does not hold %q once", edit.old)
		}
		want = strings.Replace(want, edit.old, edit.new, 1)
	}
	if layeredSystem != want {
		t.Fatalf("layered system prompt:\n%s\nwant:\n%s", layeredSystem, want)
	}
	for _, gone := range []string{"Reuse an existing thread id", "When an existing key changes", `"thread":"T3"`} {
		if strings.Contains(layeredSystem, gone) {
			t.Fatal("the layered prompt still refers to an index:", gone)
		}
	}
	if !strings.Contains(layeredSystem, `it is about. Use only what the records say. Dates are the session dates`) {
		t.Fatal("the entities sentence is not where it belongs")
	}
}

func TestLayeredPromptHasNoIndex(t *testing.T) {
	memory, batch := kayakMemory()
	batch = append(batch, pendingRecord{ID: "y", Header: "Note", Text: "Buy milk."})
	want := "New records:\n\n[r0] 2024/02/01 (session 9)\nuser: We booked the kayak for the lake next week.\n\n[r1] Note\nBuy milk."
	for _, m := range []timelineMemory{{}, memory} {
		if got := layeredFold.prompt(m, batch); got != want {
			t.Fatalf("layered prompt:\n%s\nwant:\n%s", got, want)
		}
	}
	if !strings.HasSuffix(timelinePrompt(memory, batch), "\n\n"+want) {
		t.Fatal("the layered prompt is not the batch part of the author's")
	}
	if layeredFold.system != layeredSystem || !layeredFold.entities || timelinesFold.entities {
		t.Fatal("fold specs")
	}
}

func TestApplyReplyEntities(t *testing.T) {
	batch := []pendingRecord{{ID: "rec-a", Header: "h", Text: "x"}}
	reply := `{"events":[{"thread":"new: Demo","date":"2024/03/15","text":"Demo with Acme set.","entities":[" Acme ","Bob","Acme","",7,null],"records":["r0"]},` +
		`{"thread":"new: Demo","date":"2024/03/15","text":"No names.","records":["r0"]},{"thread":"new: Demo","text":"Bad names.","entities":"Acme"}],` +
		`"facts":[{"key":"demo · day","value":"Friday","date":"2024/03/15","entities":["Acme"],"records":["r0"]}]}`
	var layered timelineMemory
	if err := applyReplyWith(&layered, batch, reply, true); err != nil {
		t.Fatal(err)
	}
	if got := layered.Events[0].Entities; !reflect.DeepEqual(got, []string{"Acme", "Bob"}) {
		t.Fatal("event entities", got)
	}
	if layered.Events[1].Entities != nil || layered.Events[2].Entities != nil {
		t.Fatal("entities from nothing", layered.Events[1:])
	}
	if got := layered.Facts["demo · day"][0].Entities; !reflect.DeepEqual(got, []string{"Acme"}) {
		t.Fatal("fact entities", got)
	}
	// Timelines consolidation never keeps them, so its state bytes stay as
	// they were.
	var timelines timelineMemory
	if err := applyReply(&timelines, batch, reply); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(timelines)
	if strings.Contains(string(raw), "entities") {
		t.Fatal("timelines kept entities", string(raw))
	}
	layered.Events[0].Entities = nil
	layered.Facts["demo · day"][0].Entities = nil
	if again, _ := json.Marshal(layered); string(again) != string(raw) {
		t.Fatalf("entities changed more than themselves:\n%s\n%s", again, raw)
	}
}
