package memoryagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// Native edges (nativeEdges; L1b–L1e of
// docs/superpowers/specs/2026-10-20-layered-consolidation-design.md) turn
// the consolidated state into derived metadata of the records
// (records.Store.ReplaceDerivedMetadata, source "consolidation"), which the
// records' native index routes read (records.IndexPolicy), so a read can
// switch each kind of edge on alone:
//
//   - L1b entities: a record's entities are the names of the events and
//     values citing it (layered extraction's; timelines names none), and its
//     aliases the other names of their clusters, then the words of its
//     multi-word names (nameWords). Each name is decided once, by JEV, into
//     the cluster of a known name that names the same one, or into a
//     cluster of its own (resolveAliases).
//   - L1c relations: in date order, the records of each event of a thread
//     link (entity, relationWeight) to those of its relationNeighbors
//     nearest events, and (temporal, temporalWeight) to those of the
//     previous and the next event. Nothing extracts causes: no causal links.
//   - L1d supersedes: the records of a key's value link (supersedes, 1) to
//     those of the value before it, when the two values differ. The index
//     takes them as hints that expand, never as corrections that demote.
//   - L1e event time: a record's eventAt is the earliest session date of an
//     event or value citing it; the index uses it only when the record's
//     writer gave none.
//
// A job that changed the memory since the metadata was last built (or was
// reset), unless it defers the step (deferEdges), and a rebuildEdges
// request rebuild it from every stored scope state; the records write only
// the rows that changed. Rows and links bind the records' current versions.

// edgesSource is the consolidation's source of derived metadata.
const edgesSource = "consolidation"

const (
	relationNeighbors = 4
	relationWeight    = 0.6
	temporalWeight    = 0.5
	supersedesWeight  = 1
	// The records' bounds of a row.
	maxEdgeNames = 64
	maxEdgeLinks = 50
	// aliasCandidates is the names a new name, and the events a new event
	// for each kind of event edge, is compared with when the mode sets no
	// LinkCandidates (timelines).
	aliasCandidates = 8
)

// The alias question, in the style of the linking questions.

func aliasQuestion(n, c string) string {
	return "Do the name in the new item whose `id` is \"" + n + "\" and the name in the candidate whose `id` is \"" + c + "\" refer to the same person, organization, place, product or document?"
}

const (
	aliasYes = "The texts show they are the same one (a full name and a short name, a nickname, a renamed product, an abbreviation)."
	aliasNo  = "Different ones that only share a word, a role or a topic, or the texts do not show that they are the same."
)

// entityNames lists the entity names of m in the order they first appear,
// events (in order) before values (by key, in order), and for each name the
// texts of the items naming it, in that order: an event's text, a value's
// "<key>: <value>".
func entityNames(m timelineMemory) ([]string, map[string][]string) {
	names, texts := []string{}, map[string][]string{}
	add := func(entities []string, text string) {
		seen := map[string]bool{}
		for _, name := range entities {
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			if _, ok := texts[name]; !ok {
				names = append(names, name)
			}
			texts[name] = append(texts[name], text)
		}
	}
	for _, e := range m.Events {
		add(e.Entities, e.Text)
	}
	for _, key := range m.FactOrder {
		for _, v := range m.Facts[key] {
			add(v.Entities, key+": "+v.Value)
		}
	}
	return names, texts
}

// nameTexts is what JEV reads of the items naming a name: the first three.
func nameTexts(texts []string) string {
	return strings.Join(texts[:min(3, len(texts))], " / ")
}

// nameItem is a name as JEV reads it.
func nameItem(id, name string, texts []string) linkItem {
	return linkItem{ID: id, Name: authorExcerpt(name, simpleListing), Text: authorExcerpt(nameTexts(texts), simpleListing)}
}

// resolveAliases decides the cluster of every entity name of the state's
// memory not decided yet, in the order the names first appear, and keeps
// the decisions in state.EntityClusters (name → the first name of its
// cluster). A new name is compared, one JEV question each, with up to
// LinkCandidates names decided before it (by earlier jobs, or earlier in
// this order) whose contexts ("<name>: <its first three texts>") are the
// most similar: by the cosine of their document vectors (the decided
// names' kept in the derived vector cache, as linking keeps the stored
// side's, the new names' not), else, or when embedding fails, by shared
// words (rankWith); names with no similarity are not compared. It joins
// the cluster of its best yes (bestLink), or starts its own. A name whose
// questions JEV did not answer (an error or a spent budget, which ends the
// asking) starts its own cluster and counts as a fallback; it is decided
// all the same. Only a canceled job is an error, and it decides nothing.
// changed reports whether a name was decided.
func (l *linker) resolveAliases(ctx context.Context, state *timelineState, result *ConsolidateResult) (changed bool, err error) {
	names, texts := entityNames(state.Memory)
	undecided, position := []int{}, map[int]int{}
	for i, name := range names {
		if _, ok := state.EntityClusters[name]; !ok {
			position[i] = len(undecided)
			undecided = append(undecided, i)
		}
	}
	if len(undecided) == 0 {
		return false, nil
	}
	limit := l.k
	if limit < 1 {
		limit = aliasCandidates
	}
	contexts, queries := make([]string, len(names)), []string{}
	for i, name := range names {
		contexts[i] = name + ": " + nameTexts(texts[name])
	}
	for _, i := range undecided {
		queries = append(queries, contexts[i])
	}
	ranks, _, err := l.rankWith(ctx, queries, contexts, len(names), true)
	if err != nil {
		return false, err
	}
	// The new names n1, n2, … and their candidates c1, c2, …, each once.
	news, cands, candidateName, candidateOf := []linkItem{}, []linkItem{}, []int{}, map[int]int{}
	pairs, all := make([][]*linkPair, len(undecided)), []*linkPair{}
	for at, i := range undecided {
		news = append(news, nameItem(fmt.Sprintf("n%d", at+1), names[i], texts[names[i]]))
		for _, j := range ranks[at] {
			if len(pairs[at]) == limit {
				break
			}
			if p, isNew := position[j]; isNew && p >= at {
				continue // itself, or a new name after it
			}
			c, ok := candidateOf[j]
			if !ok {
				c = len(cands)
				candidateOf[j] = c
				candidateName = append(candidateName, j)
				cands = append(cands, nameItem(fmt.Sprintf("c%d", c+1), names[j], texts[names[j]]))
			}
			pairs[at] = append(pairs[at], &linkPair{kind: linkAlias, item: at, candidate: c})
		}
		all = append(all, pairs[at]...)
	}
	if len(all) > 0 {
		err := errors.New("no decision model")
		if l.agent.models.Judge != nil {
			err = l.judge(ctx, news, cands, all, state, result, &result.AliasCalls)
		}
		if err != nil && ctx.Err() != nil {
			return false, err
		}
	}
	clusters := maps.Clone(state.EntityClusters)
	if clusters == nil {
		clusters = map[string]string{}
	}
	for at, i := range undecided {
		name := names[i]
		clusters[name] = name
		answers := []float64{}
		for _, p := range pairs[at] {
			if !p.answered {
				answers = nil
				break
			}
			answers = append(answers, p.same)
		}
		if answers == nil && len(pairs[at]) > 0 {
			result.AliasFallbacks++
			continue
		}
		if b := bestLink(answers); b >= 0 {
			clusters[name] = clusters[names[candidateName[pairs[at][b].candidate]]]
			result.AliasMerges++
		}
	}
	state.EntityClusters = clusters
	return true, nil
}

// recordEdges is what the edges give one record so far.
type recordEdges struct {
	entities, aliases []string
	links             []rankedLink
	eventAt           string
}

// rankedLink is a link and its place among a record's links: supersedes
// first, then by the distance of the events it joins.
type rankedLink struct {
	link records.Link
	rank int
}

// edgeBuilder collects the edges of the states' memories.
type edgeBuilder struct {
	versions map[string]int64
	records  map[string]*recordEdges
	// pairs holds the record pairs given an entity link, either way: the
	// index reads an entity link both ways, so each pair is written once.
	pairs map[[2]string]bool
}

// buildEdges is the derived metadata of the records the states' memories
// cite (see the top of this file), for the records in versions (id →
// current record version): a record not in it gets no row, and no link
// points to it. A name a state has not decided is a cluster of its own.
// Entities, aliases and links are cut to the records' bounds, links
// supersedes first, then the nearest.
func buildEdges(states []timelineState, versions map[string]int64) map[string]records.DerivedMetadata {
	b := &edgeBuilder{versions: versions, records: map[string]*recordEdges{}, pairs: map[[2]string]bool{}}
	for _, state := range states {
		b.add(state)
	}
	out := map[string]records.DerivedMetadata{}
	for id, r := range b.records {
		item := records.DerivedMetadata{Version: versions[id], Entities: r.entities[:min(maxEdgeNames, len(r.entities))], EventAt: r.eventAt}
		alias := func(name string) {
			if len(item.Aliases) < maxEdgeNames && !slices.Contains(item.Entities, name) && !slices.Contains(item.Aliases, name) {
				item.Aliases = append(item.Aliases, name)
			}
		}
		// The other names of its clusters, then the words of its
		// multi-word names, which the index can match in a query.
		for _, name := range r.aliases {
			alias(name)
		}
		for _, name := range append(slices.Clone(r.entities), r.aliases...) {
			for _, word := range nameWords(name) {
				alias(word)
			}
		}
		slices.SortStableFunc(r.links, func(x, y rankedLink) int { return x.rank - y.rank })
		for _, l := range r.links[:min(maxEdgeLinks, len(r.links))] {
			item.Links = append(item.Links, l.link)
		}
		if len(item.Entities) > 0 || len(item.Aliases) > 0 || len(item.Links) > 0 || item.EventAt != "" {
			out[id] = item
		}
	}
	return out
}

func (b *edgeBuilder) record(id string) *recordEdges {
	r := b.records[id]
	if r == nil {
		r = &recordEdges{}
		b.records[id] = r
	}
	return r
}

// add collects the edges of one state.
func (b *edgeBuilder) add(state timelineState) {
	m := state.Memory
	names, _ := entityNames(m)
	cluster := func(name string) string {
		if root, ok := state.EntityClusters[name]; ok {
			return root
		}
		return name
	}
	members := map[string][]string{}
	for _, name := range names {
		members[cluster(name)] = append(members[cluster(name)], name)
	}
	cite := func(ids, entities []string, date string) {
		day := sessionDay(date)
		for _, id := range ids {
			if _, ok := b.versions[id]; !ok {
				continue
			}
			r := b.record(id)
			for _, name := range entities {
				if name == "" {
					continue
				}
				if !slices.Contains(r.entities, name) {
					r.entities = append(r.entities, name)
				}
				for _, alias := range members[cluster(name)] {
					if !slices.Contains(r.aliases, alias) {
						r.aliases = append(r.aliases, alias)
					}
				}
			}
			if day != "" && (r.eventAt == "" || day < r.eventAt) {
				r.eventAt = day
			}
		}
	}
	for _, e := range m.Events {
		cite(e.Records, e.Entities, e.Date)
	}
	for _, key := range m.FactOrder {
		for _, v := range m.Facts[key] {
			cite(v.Records, v.Entities, v.Date)
		}
	}
	// L1c: each thread's events in date order (ties in memory order).
	threads, events := eventThreads(m)
	for _, id := range threads {
		order := slices.Clone(events[id])
		slices.SortStableFunc(order, func(x, y int) int { return strings.Compare(m.Events[x].Date, m.Events[y].Date) })
		for p := range order {
			for _, q := range nearest(p, len(order), relationNeighbors) {
				distance := max(p, q) - min(p, q)
				later, earlier := m.Events[order[max(p, q)]], m.Events[order[min(p, q)]]
				for _, source := range later.Records {
					for _, target := range earlier.Records {
						b.entity(source, target, distance)
					}
				}
				if distance == 1 {
					for _, source := range m.Events[order[p]].Records {
						for _, target := range m.Events[order[q]].Records {
							b.link(source, target, "temporal", temporalWeight, 1)
						}
					}
				}
			}
		}
	}
	// L1d: a key's value supersedes the value before it when they differ.
	for _, key := range m.FactOrder {
		values := m.Facts[key]
		for i := 1; i < len(values); i++ {
			if values[i].Value == values[i-1].Value {
				continue
			}
			for _, source := range values[i].Records {
				for _, target := range values[i-1].Records {
					b.link(source, target, "supersedes", supersedesWeight, 0)
				}
			}
		}
	}
}

// entity gives the pair source–target an entity link, once either way.
func (b *edgeBuilder) entity(source, target string, distance int) {
	pair := [2]string{min(source, target), max(source, target)}
	if b.pairs[pair] || !b.link(source, target, "entity", relationWeight, distance) {
		return
	}
	b.pairs[pair] = true
}

// link gives source a link to target, binding target's version, unless it
// links itself, either record is unknown, or source already has it.
func (b *edgeBuilder) link(source, target, kind string, weight float64, rank int) bool {
	version, ok := b.versions[target]
	if _, known := b.versions[source]; source == target || !ok || !known {
		return false
	}
	r := b.record(source)
	if slices.ContainsFunc(r.links, func(l rankedLink) bool { return l.link.ID == target && l.link.Type == kind }) {
		return false
	}
	r.links = append(r.links, rankedLink{records.Link{ID: target, Version: version, Type: kind, Weight: weight}, rank})
	return true
}

// aliasStopwords are the words, lowercase, that nameWords never gives:
// common capitalized words, months and weekdays, roles and the first
// person.
var aliasStopwords = map[string]bool{}

func init() {
	for _, word := range strings.Fields(`the and for with from this that
january february march april may june july august september october november december
jan feb mar apr jun jul aug sep sept oct nov dec
monday tuesday wednesday thursday friday saturday sunday mon tue tues wed thu thur thurs fri sat sun
user assistant our ours ourselves mine myself`) {
		aliasStopwords[word] = true
	}
}

// nameWords is the words of a multi-word name by which a query can name it
// (the records' entity route matches single words): each word, split at
// anything but letters and digits, that starts with an uppercase letter,
// has three letters or more and is no stopword (aliasStopwords). A
// one-word name has none.
func nameWords(name string) []string {
	words := strings.FieldsFunc(name, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	if len(words) < 2 {
		return nil
	}
	var out []string
	for _, word := range words {
		first, _ := utf8.DecodeRuneInString(word)
		letters := 0
		for _, r := range word {
			if unicode.IsLetter(r) {
				letters++
			}
		}
		if unicode.IsUpper(first) && letters >= 3 && !aliasStopwords[strings.ToLower(word)] {
			out = append(out, word)
		}
	}
	return out
}

// nearest is the up to k positions other than p among n nearest to p,
// nearer first, the earlier first of two as near.
func nearest(p, n, k int) []int {
	out := []int{}
	for d := 1; len(out) < k && (p-d >= 0 || p+d < n); d++ {
		if p-d >= 0 {
			out = append(out, p-d)
		}
		if len(out) < k && p+d < n {
			out = append(out, p+d)
		}
	}
	return out
}

// sessionDay is a session date (YYYY/MM/DD, as the folds write it, or
// YYYY-MM-DD) as an ISO date, or "" for anything else: nothing is inferred.
func sessionDay(date string) string {
	for _, layout := range []string{"2006/01/02", "2006-01-02"} {
		if at, err := time.Parse(layout, date); err == nil {
			return at.Format("2006-01-02")
		}
	}
	return ""
}

// edgeCounts is the records with a row and their links by type.
func edgeCounts(items map[string]records.DerivedMetadata) (int, map[string]int) {
	links := map[string]int{}
	for _, item := range items {
		for _, link := range item.Links {
			links[link.Type]++
		}
	}
	if len(links) == 0 {
		links = nil
	}
	return len(items), links
}

// edgesStale is the EdgesMemory of a reset state: its old edges are still
// stored, whatever the memory.
const edgesStale = "stale"

// edgesDigest names a memory whose edges were built (EdgesMemory); "" for
// an empty memory, which has none.
func edgesDigest(m timelineMemory) string {
	if memorySize(m) == 0 {
		return ""
	}
	raw, _ := json.Marshal(m)
	return reference("edges", string(raw)).Hash
}

// nativeEdges decides the new entity names of scope's state, replaces the
// records' derived metadata with the edges of every stored state (the
// scope's as it is in state) and saves the state, marked as built, when
// that changed it. It holds the agent's edges lock, so a replacement always
// reads the states the replacements before it saved.
func (a *Agent) nativeEdges(ctx context.Context, scope records.Scope, state *timelineState, token *string, result *ConsolidateResult) error {
	a.edges.Lock()
	defer a.edges.Unlock()
	keys, stored, err := a.overlay.allTimelines(ctx)
	if err != nil {
		return err
	}
	decided, err := a.newLinker().resolveAliases(ctx, state, result)
	if err != nil {
		return err
	}
	states := []timelineState{}
	for i, other := range stored {
		if keys[i] != scopeKey(scope) {
			states = append(states, other)
		}
	}
	states = append(states, *state)
	ids := []string{}
	for _, s := range states {
		for _, e := range s.Memory.Events {
			ids = append(ids, e.Records...)
		}
		for _, values := range s.Memory.Facts {
			for _, v := range values {
				ids = append(ids, v.Records...)
			}
		}
	}
	slices.Sort(ids)
	revision, versions, err := a.recordVersions(ctx, slices.Compact(ids))
	if err != nil {
		return err
	}
	items := buildEdges(states, versions)
	if err := a.store.ReplaceDerivedMetadata(ctx, edgesSource, items); err != nil {
		return err
	}
	result.EdgeRecords, result.EdgeLinks = edgeCounts(items)
	if result.Revision == "" {
		result.Revision = revision
	}
	if digest := edgesDigest(state.Memory); decided || state.EdgesMemory != digest {
		state.EdgesMemory = digest
		*token, err = a.overlay.saveTimelines(ctx, scope, *token, *state)
	}
	return err
}

// recordVersions is the current version (the record JSON's, which the
// records' derived links bind) of each of ids that exists, and the
// revision read.
func (a *Agent) recordVersions(ctx context.Context, ids []string) (string, map[string]int64, error) {
	snapshot, err := a.store.Get(ctx, records.GetRequest{IDs: []string{}})
	if err != nil {
		return "", nil, err
	}
	versions := map[string]int64{}
	for start := 0; start < len(ids); start += 500 {
		part, err := a.store.Get(ctx, records.GetRequest{Revision: snapshot.Revision, IDs: ids[start:min(start+500, len(ids))]})
		if err != nil {
			return "", nil, err
		}
		for _, raw := range part.Records {
			var value recordValue
			if json.Unmarshal([]byte(raw), &value) == nil && value.Version >= 1 {
				versions[value.ID] = int64(value.Version)
			}
		}
	}
	return snapshot.Revision, versions, nil
}

// clearEdges is RebuildEdges without nativeEdges: it removes the
// consolidation's derived metadata from the records.
func (a *Agent) clearEdges(ctx context.Context, req ConsolidateRequest) error {
	if req.Reset {
		return errors.New("rebuildEdges folds nothing and cannot reset")
	}
	if a.store.ReadOnly() {
		return errors.New("consolidation is unavailable with readonly storage")
	}
	a.edges.Lock()
	defer a.edges.Unlock()
	return a.store.ReplaceDerivedMetadata(ctx, edgesSource, nil)
}
