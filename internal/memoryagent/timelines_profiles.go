package memoryagent

import (
	"cmp"
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// Entity profiles (view.profiles) are a fifth block of the timelines layer:
// the consolidated events and values grouped by the entities they name, as
// layered extraction (L0) lists them; timelines consolidation names none.
// The records a View shows light up the entities of the events and values
// they cite; JEV judges those and the entities nearest the last user
// message in a call of its own, and the block shows the chosen entities'
// profiles: the latest value of each of their keys and their events, within
// the block's budgets. Lengths are JavaScript string lengths (UTF-16 units).

const (
	// layerProfiles is the profiles block's budget; a profile takes at most
	// a timeline's (layerThread) and is begun only with layerThreadFloor of
	// room. view.layer "scaled" scales both budgets, not the floor.
	layerProfiles = 3500
	// The profiles block's header, outside its budget, and the line that
	// heads a profile's facts.
	profilesHead = "Profiles of the people and things this concerns:\n"
	factsHead    = "\n- Facts:"
)

// versionWord is a version token: a word that, lowercased and with its outer
// punctuation trimmed, is all a version number.
var versionWord = regexp.MustCompile(`^v?\d+(\.\d+)+$`)

// entityWords is a name's words, split at whitespace, without its version
// tokens.
func entityWords(name string) []string {
	words := []string{}
	for _, word := range strings.FieldsFunc(name, isJSSpace) {
		if !versionWord.MatchString(trimOuterPunct(strings.ToLower(word))) {
			words = append(words, word)
		}
	}
	return words
}

// entityKey is what groups the names of an entity: the name lowercased,
// whitespace collapsed, its version token removed and its outer punctuation
// trimmed; "" names nothing.
func entityKey(name string) string {
	return trimOuterPunct(strings.ToLower(strings.Join(entityWords(name), " ")))
}

// entitySpelling is a name as a profile may show it: its own spelling,
// whitespace collapsed, without its version token.
func entitySpelling(name string) string {
	return strings.Join(entityWords(name), " ")
}

// trimOuterPunct trims punctuation and whitespace from both ends of s. A
// '#' stays: it ends names such as C# and F#.
func trimOuterPunct(s string) string {
	return strings.TrimFunc(s, func(r rune) bool { return unicode.IsPunct(r) && r != '#' || isJSSpace(r) })
}

// profileEntity is an entity of the memory: its key, its name as profiles
// show it (the most frequent spelling of its names, ties the first seen),
// the events naming it (indexes, by date, ties in memory order) and the keys
// with a value naming it (in key order).
type profileEntity struct {
	key, name string
	events    []int
	keys      []string
}

// entityIndex is the memory's entities in the order they are first named
// (the events in memory order, then the values, keys in order), by key, and
// the entities each event and each value names, each once, in the order
// they are named.
type entityIndex struct {
	list   []*profileEntity
	byKey  map[string]*profileEntity
	events [][]*profileEntity
	values map[string][][]*profileEntity
}

// memoryEntities indexes the entities the memory's events and values name.
func memoryEntities(m timelineMemory) entityIndex {
	x := entityIndex{byKey: map[string]*profileEntity{}, events: make([][]*profileEntity, len(m.Events)), values: map[string][][]*profileEntity{}}
	type spelling struct {
		text  string
		count int
	}
	spellings := map[*profileEntity][]*spelling{}
	named := func(names []string) []*profileEntity {
		out := []*profileEntity{}
		for _, name := range names {
			key := entityKey(name)
			if key == "" {
				continue
			}
			e := x.byKey[key]
			if e == nil {
				e = &profileEntity{key: key}
				x.byKey[key], x.list = e, append(x.list, e)
			}
			text := entitySpelling(name)
			if i := slices.IndexFunc(spellings[e], func(s *spelling) bool { return s.text == text }); i >= 0 {
				spellings[e][i].count++
			} else {
				spellings[e] = append(spellings[e], &spelling{text, 1})
			}
			if !slices.Contains(out, e) {
				out = append(out, e)
			}
		}
		return out
	}
	for i, ev := range m.Events {
		x.events[i] = named(ev.Entities)
		for _, e := range x.events[i] {
			e.events = append(e.events, i)
		}
	}
	for _, key := range m.FactOrder {
		lists := [][]*profileEntity{}
		for _, v := range m.Facts[key] {
			entities := named(v.Entities)
			lists = append(lists, entities)
			for _, e := range entities {
				if n := len(e.keys); n == 0 || e.keys[n-1] != key {
					e.keys = append(e.keys, key)
				}
			}
		}
		x.values[key] = lists
	}
	for _, e := range x.list {
		best := spellings[e][0]
		for _, s := range spellings[e][1:] {
			if s.count > best.count {
				best = s
			}
		}
		e.name = best.text
		slices.SortStableFunc(e.events, func(a, b int) int { return jsCompare(m.Events[a].Date, m.Events[b].Date) })
	}
	return x
}

// factLine is a key as a profile shows it: "<key>: <latest value>
// (<date>)", the latest value's date, with ", earlier <n> values" when the
// key had n other distinct values; an empty date, and an empty parenthesis,
// are left out.
func factLine(key string, values []factValue) string {
	latest := values[len(values)-1]
	other := map[string]bool{}
	for _, v := range values[:len(values)-1] {
		if v.Value != latest.Value {
			other[v.Value] = true
		}
	}
	notes := []string{}
	if latest.Date != "" {
		notes = append(notes, latest.Date)
	}
	if len(other) > 0 {
		notes = append(notes, fmt.Sprintf("earlier %d values", len(other)))
	}
	line := key + ": " + latest.Value
	if len(notes) > 0 {
		line += " (" + strings.Join(notes, ", ") + ")"
	}
	return line
}

// entityText is what JEV and the embedder read of an entity, built like a
// topic's (threadText): "<name>: N events, <first> to <last>. Latest: …",
// its events by date and the latest three; for an entity only values name,
// "<name>: N facts. Latest: …", the fact lines of its last three keys.
func entityText(m timelineMemory, e *profileEntity) string {
	if len(e.events) > 0 {
		return threadText(e.name, m, e.events)
	}
	latest := []string{}
	for _, key := range e.keys[max(0, len(e.keys)-3):] {
		latest = append(latest, factLine(key, m.Facts[key]))
	}
	return fmt.Sprintf("%s: %d facts. Latest: %s", e.name, len(e.keys), strings.Join(latest, " / "))
}

// litEntities is the entities the View's records light up, by key, in the
// order they first light up, and their anchor links: the entities of the
// lit events (lightUp's), each event adding its anchor links (its records
// that are anchors, as lightUp counts them), then those of the lit keys'
// values, each value adding its own.
func litEntities(m timelineMemory, x entityIndex, lit litSets, anchors map[string]bool) (keys []string, counts map[string]int) {
	keys, counts = []string{}, map[string]int{}
	links := func(records []string) int {
		n := 0
		for _, id := range records {
			if anchors[id] {
				n++
			}
		}
		return n
	}
	add := func(entities []*profileEntity, n int) {
		for _, e := range entities {
			if _, ok := counts[e.key]; !ok {
				keys = append(keys, e.key)
			}
			counts[e.key] += n
		}
	}
	for i := range m.Events {
		if lit.Events[i] {
			add(x.events[i], links(m.Events[i].Records))
		}
	}
	for _, key := range lit.Values {
		for j, v := range m.Facts[key] {
			add(x.values[key][j], links(v.Records))
		}
	}
	return keys, counts
}

// rankedEntities is the lit entities by anchor links, most first, ties in
// the order they lit up.
func rankedEntities(keys []string, counts map[string]int) []string {
	out := slices.Clone(keys)
	slices.SortStableFunc(out, func(a, b string) int { return cmp.Compare(counts[b], counts[a]) })
	return out
}

// profileLine is a line of a profile: a key's fact line (key set) or an
// event's, with the date it is as new as (a key's latest value's) and
// whether the View's records light it up.
type profileLine struct {
	text, date, key string
	event           int
	anchor          bool
}

// composeProfiles is the block 5-profiles: the profiles of the entities of
// order (keys of x), in order, each within min(b.thread, b.profiles - the
// profiles so far), none begun with less than 300 of room, separated by a
// blank line; the header is not counted. A profile is "Profile: <name>",
// the fact lines of its keys, in key order, after "- Facts:", then its event
// lines ("- <date>: <text>"), by date. A profile over its room keeps, as a
// timeline does, its anchor lines first (the facts of the lit keys, then the
// lit events), then the others newest first (a fact line as new as its
// latest value; ties the later line first), each that fits, shown in order;
// one with nothing that fits is left out. Records name the records of the
// latest values and the events shown, each once. It returns the block and
// the profiles it shows (none: no block).
func composeProfiles(m timelineMemory, x entityIndex, order []string, lit litSets, b layerBudgets) (IndexItem, int) {
	litKey := map[string]bool{}
	for _, key := range lit.Values {
		litKey[key] = true
	}
	text, size, shown, linked := "", 0, 0, []string{}
	for _, id := range order {
		e := x.byKey[id]
		room := min(b.thread, b.profiles-size)
		if room < layerThreadFloor {
			break
		}
		title := "Profile: " + e.name
		lines := []profileLine{}
		for _, key := range e.keys {
			values := m.Facts[key]
			lines = append(lines, profileLine{text: "\n" + factLine(key, values), date: values[len(values)-1].Date, key: key, anchor: litKey[key]})
		}
		for _, i := range e.events {
			lines = append(lines, profileLine{text: "\n- " + m.Events[i].Date + ": " + m.Events[i].Text, date: m.Events[i].Date, event: i, anchor: lit.Events[i]})
		}
		keep := make([]bool, len(lines))
		whole := jsLen(title)
		if len(e.keys) > 0 {
			whole += jsLen(factsHead)
		}
		for _, line := range lines {
			whole += jsLen(line.text)
		}
		if whole <= room {
			for k := range keep {
				keep[k] = true
			}
		} else {
			used, facts := jsLen(title), false
			take := func(k int) {
				n := jsLen(lines[k].text)
				if lines[k].key != "" && !facts {
					n += jsLen(factsHead)
				}
				if used+n <= room {
					keep[k], used = true, used+n
					facts = facts || lines[k].key != ""
				}
			}
			rest := []int{}
			for k := range lines {
				if lines[k].anchor {
					take(k)
				}
			}
			for k := len(lines) - 1; k >= 0; k-- {
				if !lines[k].anchor {
					rest = append(rest, k)
				}
			}
			slices.SortStableFunc(rest, func(p, q int) int { return jsCompare(lines[q].date, lines[p].date) })
			for _, k := range rest {
				take(k)
			}
		}
		if !slices.Contains(keep, true) {
			continue
		}
		part, facts := title, false
		for k, line := range lines {
			if !keep[k] {
				continue
			}
			if line.key != "" {
				if !facts {
					part, facts = part+factsHead, true
				}
				values := m.Facts[line.key]
				linked = append(linked, values[len(values)-1].Records...)
			} else {
				linked = append(linked, m.Events[line.event].Records...)
			}
			part += line.text
		}
		if text != "" {
			text, size = text+"\n\n", size+2
		}
		text, size, shown = text+part, size+jsLen(part), shown+1
	}
	if shown == 0 {
		return IndexItem{}, 0
	}
	refs, seen := []Reference{}, map[string]bool{}
	for _, record := range linked {
		if !seen[record] {
			seen[record] = true
			refs = append(refs, Reference{ID: record})
		}
	}
	return IndexItem{ID: "5-profiles", Kind: "5-profiles", Text: profilesHead + text, Records: refs}, shown
}

// entitySource is the source the profiles' judgement gives its candidates.
const entitySource = "entity"

// profiles is the block 5-profiles of a read with view.profiles, or none.
// The candidates are the entities the View's records light up (anchors),
// by anchor links (ties in the order they first lit up), then the 12
// entities nearest the last user message (nearestEntities), each once. JEV
// judges them, by their entity texts, in a call of its own (judgeNeeded,
// source "entity"), so the topics' and values' call is the same with
// profiles and without. The chosen are those it calls needed (≥ 0.5), by
// its answer (ties in candidate order), then the lit entities below 0.5 by
// anchor links, as layerOrder chooses topics. A judgement that fails, or
// that the read's decision budget cannot pay for, shows no profiles and
// degrades the read (layer-profiles). A memory whose events and values name
// no entity (a timelines state) shows none and asks nothing. Only a
// canceled read is an error.
func (r *simpleRun) profiles(ctx context.Context, m timelineMemory, lit litSets, anchors map[string]bool, message string, lexical bool, b layerBudgets) ([]IndexItem, error) {
	x := memoryEntities(m)
	if len(x.list) == 0 {
		return nil, nil
	}
	keys, texts := []string{}, []string{}
	for _, e := range x.list {
		keys, texts = append(keys, e.key), append(texts, entityText(m, e))
	}
	litKeys, counts := litEntities(m, x, lit, anchors)
	near := r.nearestEntities(ctx, message, texts, lexical)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	candidates, _ := layerCandidates(litSets{Threads: rankedEntities(litKeys, counts)}, keys, nil, near, nil)
	if len(candidates) == 0 {
		return nil, nil
	}
	text := map[string]string{}
	for i, key := range keys {
		text[key] = texts[i]
	}
	judged := []judgeCandidate{}
	for _, key := range candidates {
		judged = append(judged, judgeCandidate{Source: entitySource, Text: text[key]})
	}
	if calls := (len(judged) + judgeBatch - 1) / judgeBatch; r.decisionsLeft() < calls {
		r.degrade("layer-profiles")
		return nil, nil
	}
	scores, err := r.judgeNeeded(ctx, judged)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		r.degrade("layer-profiles")
		return nil, nil
	}
	score := map[string]float64{}
	for i, key := range candidates {
		score[key] = scores[i]
	}
	order, _ := layerOrder(candidates, nil, litSets{Threads: litKeys, Counts: counts}, score, nil)
	block, shown := composeProfiles(m, x, order, lit, b)
	if shown == 0 {
		return nil, nil
	}
	r.mu.Lock()
	r.stats.LayerProfiles = shown
	r.mu.Unlock()
	return []IndexItem{block}, nil
}

// nearestEntities is the indexes of the 12 entity texts nearest the message,
// best first, as nearest ranks the topics: by meaning with an embedder (the
// texts' document vectors, stored like the topics'), else, or when that
// fails (layer-lexical), by shared words. When the topics fell back to
// shared words (lexical), so do the entities, embedding nothing.
func (r *simpleRun) nearestEntities(ctx context.Context, message string, texts []string, lexical bool) []int {
	if !lexical {
		if query, vectors, _, ok := r.layerVectors(ctx, message, texts, nil); ok {
			return byDot(query, vectors, layerNearThreads)
		}
		if r.agent.config.Embedding != nil && ctx.Err() == nil {
			r.degrade("layer-lexical")
		}
	}
	return bm25Top(texts, message, layerNearThreads)
}
