package memoryagent

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"time"
)

// Event edges (edgeKinds; stage 3 of
// docs/superpowers/specs/2026-10-20-layered-consolidation-design.md) are
// native mnemon's four graphs between consolidation events, every edge a
// JEV decision: no similarity threshold ever links two events. For each
// listed kind, the edge step (nativeEdges) asks about each event not yet
// decided for it (decideEventEdges): up to LinkCandidates earlier events,
// picked by the kind's rule (edgeCandidates), one yes/no question each.
// The state keeps every answer, so a pair is asked once; the records'
// derived metadata gets each yes as links between the two events' records
// (addEventEdges, placeJudged), in place of the thread links of edges.go.

// eventEdgeKind is a kind of event edge: its name (the records' link type),
// its linking pair kind, the key prefix of its questions, its question about
// a new event n and a candidate c, and the question's yes and no criteria.
type eventEdgeKind struct {
	name     string
	pair     int
	key      string
	question func(n, c string) string
	yes, no  string
}

// eventEdgeKinds are the kinds of event edge, in the order a step asks them.
var eventEdgeKinds = []eventEdgeKind{
	{name: "semantic", pair: linkSemantic, key: "es", question: semanticQuestion, yes: semanticYes, no: semanticNo},
	{name: "temporal", pair: linkTemporal, key: "et", question: temporalQuestion, yes: temporalYes, no: temporalNo},
	{name: "causal", pair: linkCausal, key: "ec", question: causalQuestion, yes: causalYes, no: causalNo},
	{name: "entity", pair: linkEntity, key: "en", question: entityQuestion, yes: entityYes, no: entityNo},
}

// edgeKindOf is the kind of event edge a linking pair kind asks about.
func edgeKindOf(pair int) eventEdgeKind {
	for _, k := range eventEdgeKinds {
		if k.pair == pair {
			return k
		}
	}
	panic(fmt.Sprintf("no event edge kind for pair kind %d", pair))
}

// The event edge questions, in the style of the linking questions.

func semanticQuestion(n, c string) string {
	return "Are the new event whose `id` is \"" + n + "\" and the candidate whose `id` is \"" + c + "\" about the same matter, so that one helps to answer a question about the other?"
}

const (
	semanticYes = "They concern the same specific matter (the same plan, problem, decision, activity, object or relationship), even in different words."
	semanticNo  = "They only share a general topic, a person or a time, or concern different matters."
)

func temporalQuestion(n, c string) string {
	return "Is the new event whose `id` is \"" + n + "\" a later development of the same matter as the candidate whose `id` is \"" + c + "\"?"
}

const (
	temporalYes = "It continues, follows up, changes, completes or reports on the same matter after the candidate."
	temporalNo  = "It concerns a different matter, or only happens to be later."
)

func causalQuestion(n, c string) string {
	return "Did the candidate event whose `id` is \"" + c + "\" cause, enable or prevent the new event whose `id` is \"" + n + "\"?"
}

const (
	causalYes = "The records state or clearly imply that the candidate led to, made possible or stopped the new event."
	causalNo  = "No such link is stated or clearly implied; being earlier or related is not a cause."
)

func entityQuestion(n, c string) string {
	return "Are the new event whose `id` is \"" + n + "\" and the candidate whose `id` is \"" + c + "\" about the same particular person, organization, place, product or document?"
}

const (
	entityYes = "Both concern that same particular one, named or clearly referred to."
	entityNo  = "They only share a common name or a general kind of thing, or concern different ones."
)

// eventID names an event by its date, text and records: the first 16 hex
// digits of the sha256 of their JSON. It survives rebuilds of the state and
// a change of the event's thread; two events alike in all three are one.
func eventID(e event) string {
	records := e.Records
	if records == nil {
		records = []string{}
	}
	raw, _ := json.Marshal([]any{e.Date, e.Text, records})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

// eventNode is an event as event edges see it: its id, its first position
// in the memory (at), its session day (sessionDay, "" for none) and the
// clusters of its names, each once.
type eventNode struct {
	id       string
	at       int
	day      string
	event    event
	clusters []string
}

// eventNodes is the events of the state's memory that cite records, each
// id once (at its first position), in memory order. A name the state has
// not decided is a cluster of its own.
func eventNodes(state timelineState) []eventNode {
	nodes, seen := []eventNode{}, map[string]bool{}
	for at, e := range state.Memory.Events {
		if len(e.Records) == 0 {
			continue
		}
		id := eventID(e)
		if seen[id] {
			continue
		}
		seen[id] = true
		var clusters []string
		for _, name := range e.Entities {
			root, ok := state.EntityClusters[name]
			if !ok {
				root = name
			}
			if name != "" && !slices.Contains(clusters, root) {
				clusters = append(clusters, root)
			}
		}
		nodes = append(nodes, eventNode{id: id, at: at, day: sessionDay(e.Date), event: e, clusters: clusters})
	}
	return nodes
}

// precedes reports whether f comes before e: on an earlier day, or, on the
// same day or when either has no date, earlier in the memory. Of two events
// one precedes the other.
func precedes(f, e eventNode) bool {
	if f.day != "" && e.day != "" && f.day != e.day {
		return f.day < e.day
	}
	return f.at < e.at
}

// dayDistance is the days between two session days, the most for an
// unknown day.
func dayDistance(a, b string) int {
	x, errX := time.Parse(time.DateOnly, a)
	y, errY := time.Parse(time.DateOnly, b)
	if errX != nil || errY != nil {
		return math.MaxInt
	}
	days := int(y.Sub(x).Hours() / 24)
	return max(days, -days)
}

// edgeCandidates is the up to limit nodes the i-th node is asked about for
// kind, best first, among the nodes that precede it; similar is each
// node's rank by similarity to it (0 the most similar), -1 for no
// similarity. Similarity only selects and orders candidates:
//
//   - semantic: the similar nodes, by similarity, of any thread;
//   - temporal: the nodes of its thread or sharing a cluster with it, the
//     nearest day first, then by similarity;
//   - causal: the nodes of its thread, by similarity, then the similar
//     nodes of other threads, by similarity;
//   - entity: the nodes sharing a cluster with it, by similarity.
//
// Ties go to the node nearer in the memory.
func edgeCandidates(kind string, nodes []eventNode, i int, similar []int, limit int) []int {
	type candidate struct{ node, group, distance, rank int }
	e, found := nodes[i], []candidate{}
	for j, f := range nodes {
		if j == i || !precedes(f, e) {
			continue
		}
		thread := f.event.Thread == e.event.Thread
		shared := slices.ContainsFunc(f.clusters, func(c string) bool { return slices.Contains(e.clusters, c) })
		c := candidate{node: j, rank: similar[j]}
		if c.rank < 0 {
			c.rank = math.MaxInt
		}
		switch kind {
		case "semantic":
			if similar[j] < 0 {
				continue
			}
		case "temporal":
			if !thread && !shared {
				continue
			}
			c.distance = dayDistance(f.day, e.day)
		case "causal":
			if !thread {
				if similar[j] < 0 {
					continue
				}
				c.group = 1
			}
		case "entity":
			if !shared {
				continue
			}
		default:
			continue
		}
		found = append(found, c)
	}
	slices.SortFunc(found, func(x, y candidate) int {
		return cmp.Or(cmp.Compare(x.group, y.group), cmp.Compare(x.distance, y.distance), cmp.Compare(x.rank, y.rank), cmp.Compare(nodes[y.node].at, nodes[x.node].at))
	})
	out := []int{}
	for _, c := range found[:min(limit, len(found))] {
		out = append(out, c.node)
	}
	return out
}

// eventEdges is JEV's decisions about the events of a memory, by kind:
// Decided lists the events (eventID) whose candidates were all answered,
// in the order decided; Answers holds, for each event asked about, each
// candidate's answer: the edge's weight, its yes probability to two
// decimals (at least 0.5), or 0 for no.
type eventEdges struct {
	Decided map[string][]string                      `json:"decided,omitempty"`
	Answers map[string]map[string]map[string]float64 `json:"answers,omitempty"`
}

// answered reports whether JEV answered whether kind joins events from and
// to.
func (e *eventEdges) answered(kind, from, to string) bool {
	if e == nil {
		return false
	}
	_, ok := e.Answers[kind][from][to]
	return ok
}

// eventItem is an event as JEV reads it, under id.
func eventItem(id string, n eventNode, titles map[string]string) linkItem {
	title, ok := titles[n.event.Thread]
	if !ok {
		title = n.event.Thread
	}
	var names []string
	for _, name := range n.event.Entities[:min(maxEdgeNames, len(n.event.Entities))] {
		names = append(names, authorExcerpt(name, simpleListing))
	}
	return linkItem{ID: id, Date: authorExcerpt(n.event.Date, simpleListing), Thread: authorExcerpt(title, simpleListing), Text: authorExcerpt(n.event.Text, simpleListing), Entities: names}
}

// decideEventEdges asks JEV, for each of kinds, about the events of the
// state's memory not yet decided for that kind (eventNodes; new since the
// last step, or left undecided by a failed call), and keeps every answer
// in state.EventEdges. An event is compared with its candidates
// (edgeCandidates, up to LinkCandidates, 8 in timelines mode), found by
// the similarity of event texts: the cosine of their document vectors (the
// decided events' kept in the derived vector cache, the new ones' not),
// else, or when embedding fails, shared words (rankWith). A pair answered
// before is not asked again. The questions of all kinds are packed into
// few calls (judge); JEV reads each event as its date, thread title, text
// and names (eventItem), the new ones in "new", their candidates in
// "candidates". A yes (at least 0.5) is an edge weighted by its
// probability, to two decimals; a no is kept as 0. A pair whose call failed
// (an error or a spent budget, which ends the asking, or no decision model)
// stays undecided, counted in EdgeFallbacks, and its event with it, to be
// asked again by the next step; the step goes on. Only a canceled job is an
// error, and it decides nothing. changed reports whether a decision was
// kept.
func (l *linker) decideEventEdges(ctx context.Context, state *timelineState, kinds []string, result *ConsolidateResult) (changed bool, err error) {
	nodes := eventNodes(*state)
	asked := []eventEdgeKind{}
	for _, k := range eventEdgeKinds {
		if slices.Contains(kinds, k.name) {
			asked = append(asked, k)
		}
	}
	// open[k][i]: node i is undecided for the k-th kind asked; each such
	// node's text is a query.
	open, queryOf, queries := make([][]bool, len(asked)), map[int]int{}, []string{}
	for k, kind := range asked {
		open[k] = make([]bool, len(nodes))
		decided := map[string]bool{}
		if state.EventEdges != nil {
			for _, id := range state.EventEdges.Decided[kind.name] {
				decided[id] = true
			}
		}
		for i, n := range nodes {
			if !decided[n.id] {
				open[k][i] = true
				if _, ok := queryOf[i]; !ok {
					queryOf[i] = len(queries)
					queries = append(queries, n.event.Text)
				}
			}
		}
	}
	if len(queries) == 0 {
		return false, nil
	}
	docs := make([]string, len(nodes))
	for i, n := range nodes {
		docs[i] = n.event.Text
	}
	ranks, _, err := l.rankWith(ctx, queries, docs, len(docs), true)
	if err != nil {
		return false, err
	}
	limit := l.k
	if limit < 1 {
		limit = aliasCandidates
	}
	titles := map[string]string{}
	for _, t := range state.Memory.Threads {
		titles[t.ID] = t.Title
	}
	// The new events n1, n2, … and their candidates c1, c2, …, each once;
	// for each event and kind, its candidates and the pairs to ask.
	type decision struct {
		kind, node int
		pairs      []*linkPair
	}
	news, cands, newOf, candOf, candNode := []linkItem{}, []linkItem{}, map[int]int{}, map[int]int{}, []int{}
	decisions, pairs := []*decision{}, []*linkPair{}
	for i, n := range nodes {
		q, ok := queryOf[i]
		if !ok {
			continue
		}
		similar := make([]int, len(nodes))
		for j := range similar {
			similar[j] = -1
		}
		for rank, j := range ranks[q] {
			similar[j] = rank
		}
		for k, kind := range asked {
			if !open[k][i] {
				continue
			}
			d := &decision{kind: k, node: i}
			for _, j := range edgeCandidates(kind.name, nodes, i, similar, limit) {
				if state.EventEdges.answered(kind.name, n.id, nodes[j].id) {
					continue
				}
				if _, ok := newOf[i]; !ok {
					newOf[i] = len(news)
					news = append(news, eventItem(fmt.Sprintf("n%d", len(news)+1), n, titles))
				}
				if _, ok := candOf[j]; !ok {
					candOf[j] = len(cands)
					candNode = append(candNode, j)
					cands = append(cands, eventItem(fmt.Sprintf("c%d", len(cands)+1), nodes[j], titles))
				}
				d.pairs = append(d.pairs, &linkPair{kind: kind.pair, item: newOf[i], candidate: candOf[j]})
			}
			decisions, pairs = append(decisions, d), append(pairs, d.pairs...)
		}
	}
	if len(pairs) > 0 {
		err := errors.New("no decision model")
		if l.agent.models.Judge != nil {
			err = l.judge(ctx, news, cands, pairs, state, result, &result.EdgeCalls)
		}
		if err != nil && ctx.Err() != nil {
			return false, err
		}
	}
	edges := state.EventEdges
	if edges == nil {
		edges = &eventEdges{}
	}
	count := func(counts *map[string]int, kind string) {
		if *counts == nil {
			*counts = map[string]int{}
		}
		(*counts)[kind]++
	}
	for _, d := range decisions {
		kind, from, complete := asked[d.kind].name, nodes[d.node].id, true
		for _, p := range d.pairs {
			if !p.answered {
				complete = false
				result.EdgeFallbacks++
				continue
			}
			weight := 0.0
			if p.same >= 0.5 {
				weight = math.Round(p.same*100) / 100
				count(&result.EdgeYes, kind)
			}
			count(&result.EdgeAsked, kind)
			if edges.Answers == nil {
				edges.Answers = map[string]map[string]map[string]float64{}
			}
			if edges.Answers[kind] == nil {
				edges.Answers[kind] = map[string]map[string]float64{}
			}
			if edges.Answers[kind][from] == nil {
				edges.Answers[kind][from] = map[string]float64{}
			}
			edges.Answers[kind][from][nodes[candNode[p.candidate]].id] = weight
			changed = true
		}
		if complete {
			if edges.Decided == nil {
				edges.Decided = map[string][]string{}
			}
			edges.Decided[kind] = append(edges.Decided[kind], from)
			changed = true
		}
	}
	if changed {
		state.EventEdges = edges
	}
	return changed, nil
}

// judgedLink is a record link an event edge gives: from source to target,
// or, for a kind the records' index reads both ways (mirrored), between
// the two, source the lesser id.
type judgedLink struct{ source, target, kind string }

// mirrored reports whether the records' index reads a link of kind both
// ways, so that one written link joins both records.
func mirrored(kind string) bool {
	return kind == "semantic" || kind == "entity"
}

// addEventEdges collects the record links of the state's event edges of
// the builder's kinds: each yes links every record of one event to every
// record of the other, with the edge's kind and weight, both ways (a
// mirrored kind once per pair). A pair of records keeps the highest weight
// any edge gives it.
func (b *edgeBuilder) addEventEdges(state timelineState) {
	if state.EventEdges == nil {
		return
	}
	cited := map[string][]string{}
	for _, e := range state.Memory.Events {
		cited[eventID(e)] = e.Records
	}
	keep := func(l judgedLink, weight float64) {
		if weight > b.judged[l] {
			b.judged[l] = weight
		}
	}
	for _, kind := range b.kinds {
		for from, answers := range state.EventEdges.Answers[kind] {
			for to, weight := range answers {
				if weight < 0.5 {
					continue
				}
				for _, x := range cited[from] {
					for _, y := range cited[to] {
						switch {
						case x == y:
						case mirrored(kind):
							keep(judgedLink{min(x, y), max(x, y), kind}, weight)
						default:
							keep(judgedLink{x, y, kind}, weight)
							keep(judgedLink{y, x, kind}, weight)
						}
					}
				}
			}
		}
	}
}

// placeJudged writes the collected record links, the heaviest first (ties
// by kind, then ids), after the supersedes links: a link on its source, a
// mirrored one on whichever of its records has fewer links so far (the
// lesser id on a tie), so that the per-record cap keeps the heaviest links
// and drops a mirrored one only when both records are full.
func (b *edgeBuilder) placeJudged() {
	links := slices.Collect(maps.Keys(b.judged))
	kind := func(name string) int {
		return slices.IndexFunc(eventEdgeKinds, func(k eventEdgeKind) bool { return k.name == name })
	}
	slices.SortFunc(links, func(x, y judgedLink) int {
		return cmp.Or(cmp.Compare(b.judged[y], b.judged[x]), cmp.Compare(kind(x.kind), kind(y.kind)), cmp.Compare(x.source, y.source), cmp.Compare(x.target, y.target))
	})
	count := func(id string) int {
		if r := b.records[id]; r != nil {
			return len(r.links)
		}
		return 0
	}
	for _, l := range links {
		source, target := l.source, l.target
		if mirrored(l.kind) && count(target) < count(source) {
			source, target = target, source
		}
		b.link(source, target, l.kind, b.judged[l], 1)
	}
}
