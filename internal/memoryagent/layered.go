package memoryagent

import "strings"

// Layered consolidation splits the author's fold in two
// (docs/superpowers/specs/2026-10-20-layered-consolidation-design.md). L0:
// a model reads each batch of records alone, with no index of the memory so
// far, and writes its events, facts and directives as timelines do, each
// naming the entities it is about. L1: JEV links each batch's items into
// the stored memory, in batch order, by yes/no decisions over a few
// retrieved candidates (layered_link.go). The state, its table and the read
// layer are those of timelines consolidation.

// layeredSystem is the author's consolidation prompt (timelineSystem) with
// the edits that remove its references to an index of the memory so far,
// and one sentence and JSON field that ask for entity names.
var layeredSystem = edited(timelineSystem,
	`Reuse an existing thread id when the event continues that sub-topic; otherwise start a new thread ("new: <short title>").`,
	`Name the thread with a short title ("new: <short title>") and use the same title for every event of that sub-topic in this batch.`,
	`When an existing key changes, repeat that exact key with the new value.`,
	`When a key's value changes within the batch, repeat that exact key with the new value.`,
	`contradicts an earlier one and`,
	`contradicts an earlier one in this batch and`,
	`Use only what the records say.`,
	`For each event and fact, list in "entities" the names of the people, organizations, places, products or documents it is about. Use only what the records say.`,
	`{"events":[{"thread":"T3","date":"YYYY/MM/DD","text":"...","records":["r12"]}],"facts":[{"key":"...","value":"...","date":"YYYY/MM/DD","records":["r15"],"conflict":false}],"directives":[{"text":"...","date":"YYYY/MM/DD","records":["r20"]}]}`,
	`{"events":[{"thread":"new: <short title>","date":"YYYY/MM/DD","text":"...","entities":["..."],"records":["r12"]}],"facts":[{"key":"...","value":"...","date":"YYYY/MM/DD","entities":["..."],"records":["r15"],"conflict":false}],"directives":[{"text":"...","date":"YYYY/MM/DD","records":["r20"]}]}`,
)

// layeredFold is L0: the batch alone, under layeredSystem, keeping the
// entity names of its items.
var layeredFold = foldSpec{system: layeredSystem, prompt: func(_ timelineMemory, batch []pendingRecord) string { return batchSection(batch) }, entities: true}

// edited replaces, in order, each old text of pairs (old, new, …) that s
// holds exactly once; it panics otherwise, so a changed prompt cannot lose
// an edit silently.
func edited(s string, pairs ...string) string {
	for i := 0; i+1 < len(pairs); i += 2 {
		if strings.Count(s, pairs[i]) != 1 {
			panic("prompt edit does not apply: " + pairs[i])
		}
		s = strings.Replace(s, pairs[i], pairs[i+1], 1)
	}
	return s
}
