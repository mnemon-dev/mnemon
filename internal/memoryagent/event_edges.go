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
	"strconv"
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

// Hot clusters: a cluster named by more than hotShare of a memory's events,
// and by at least hotEvents of them (the speakers of a conversation), says
// nothing about which events go together.
const (
	hotShare  = 0.25
	hotEvents = 8
)

// hotClusters is the clusters named by more than a quarter of the nodes
// and by at least 8 of them. A frequency stop-list, not a similarity
// threshold: it never links two events, only keeps a speaker from making
// every event a candidate of every other.
func hotClusters(nodes []eventNode) map[string]bool {
	counts := map[string]int{}
	for _, n := range nodes {
		for _, c := range n.clusters {
			counts[c]++
		}
	}
	hot := map[string]bool{}
	for c, count := range counts {
		if count >= hotEvents && float64(count) > hotShare*float64(len(nodes)) {
			hot[c] = true
		}
	}
	return hot
}

// sharesCluster reports whether f and e name a cluster in common that is
// not hot.
func sharesCluster(f, e eventNode, hot map[string]bool) bool {
	return slices.ContainsFunc(f.clusters, func(c string) bool { return !hot[c] && slices.Contains(e.clusters, c) })
}

// edgeCandidates is the up to limit nodes the i-th node is asked about for
// kind, best first, among the nodes that precede it; similar is each
// node's rank by similarity to it (0 the most similar), -1 for no
// similarity; hot the clusters that never count as shared (hotClusters).
// Similarity only selects and orders candidates:
//
//   - semantic: the similar nodes, by similarity, of any thread;
//   - temporal: the nodes of its thread, then the nodes sharing a cluster
//     with it, each the nearest day first, then by similarity;
//   - causal: the nodes of its thread, by similarity, then the similar
//     nodes of other threads, by similarity;
//   - entity: the nodes sharing a cluster with it, by similarity.
//
// Ties go to the node nearer in the memory.
func edgeCandidates(kind string, nodes []eventNode, i int, similar []int, hot map[string]bool, limit int) []int {
	type candidate struct{ node, group, distance, rank int }
	e, found := nodes[i], []candidate{}
	for j, f := range nodes {
		if j == i || !precedes(f, e) {
			continue
		}
		thread := f.event.Thread == e.event.Thread
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
			if !thread {
				if !sharesCluster(f, e, hot) {
					continue
				}
				c.group = 1
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
			if !sharesCluster(f, e, hot) {
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

const (
	// eventEdgeRules versions the candidate rules (edgeCandidates): the
	// events decided under other rules are decided again, asking only the
	// pairs never answered. Version 1 added the hot-cluster stop-list.
	eventEdgeRules = 1
	// pairAttempts is the failed calls after which a pair is given up:
	// never asked again, and no edge.
	pairAttempts = 3
	// edgeChunkCalls is the calls a step makes between commits of its
	// answers.
	edgeChunkCalls = 32
	// itemNames bounds the names of an event as JEV reads it, so that one
	// pair always fits a call.
	itemNames = 16
)

// eventEdges is JEV's decisions about the events of a memory. Rules is
// the candidate rules they were made under; by kind, Decided lists the
// events (eventID) whose candidate pairs were all answered or given up, in
// the order decided; Answers holds, for each event asked about, each
// candidate's answer: the edge's weight, its yes probability to two
// decimals (at least 0.5), or 0 for no; Failures the failed calls of each
// pair not answered, given up at pairAttempts.
type eventEdges struct {
	Rules    int                                      `json:"rules,omitempty"`
	Decided  map[string][]string                      `json:"decided,omitempty"`
	Answers  map[string]map[string]map[string]float64 `json:"answers,omitempty"`
	Failures map[string]map[string]map[string]int     `json:"failures,omitempty"`
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

// givenUp reports whether the pair failed pairAttempts calls.
func (e *eventEdges) givenUp(kind, from, to string) bool {
	return e != nil && e.Failures[kind][from][to] >= pairAttempts
}

// nested is m[kind][from], made when missing.
func nested[V any](m *map[string]map[string]map[string]V, kind, from string) map[string]V {
	if *m == nil {
		*m = map[string]map[string]map[string]V{}
	}
	if (*m)[kind] == nil {
		(*m)[kind] = map[string]map[string]V{}
	}
	if (*m)[kind][from] == nil {
		(*m)[kind][from] = map[string]V{}
	}
	return (*m)[kind][from]
}

// answer keeps JEV's answer about a pair and forgets its failures.
func (e *eventEdges) answer(kind, from, to string, weight float64) {
	nested(&e.Answers, kind, from)[to] = weight
	if e.Failures[kind][from] == nil {
		return
	}
	delete(e.Failures[kind][from], to)
	if len(e.Failures[kind][from]) == 0 {
		delete(e.Failures[kind], from)
	}
	if len(e.Failures[kind]) == 0 {
		delete(e.Failures, kind)
	}
	if len(e.Failures) == 0 {
		e.Failures = nil
	}
}

// eventItem is an event as JEV reads it, under id: its date, thread title,
// text and first itemNames names, each cut to simpleListing units.
func eventItem(id string, n eventNode, titles map[string]string) linkItem {
	title, ok := titles[n.event.Thread]
	if !ok {
		title = n.event.Thread
	}
	var names []string
	for _, name := range n.event.Entities[:min(itemNames, len(n.event.Entities))] {
		names = append(names, authorExcerpt(name, simpleListing))
	}
	return linkItem{ID: id, Date: authorExcerpt(n.event.Date, simpleListing), Thread: authorExcerpt(title, simpleListing), Text: authorExcerpt(n.event.Text, simpleListing), Entities: names}
}

// decideEventEdges asks JEV, for each of kinds, about the events of the
// state's memory not yet decided for that kind (eventNodes; new since the
// last step, or left undecided by a failed call, or all of them when the
// candidate rules changed), and keeps every answer in state.EventEdges. An
// event is compared with its candidates (edgeCandidates, up to
// LinkCandidates, 8 in timelines mode; the memory's hot clusters never
// count as shared), found by the similarity of event texts: the cosine of
// their document vectors (the decided events' kept in the derived vector
// cache, the new ones' not), else, or when embedding fails (EdgeLexical),
// shared words (rankWith). A pair answered or given up before is not asked
// again. The questions of all kinds are packed into few calls (packLinks);
// JEV reads each event as eventItem does, the new ones in "new", their
// candidates in "candidates". A yes (at least 0.5) is an edge weighted by
// its probability, to two decimals; a no is kept as 0.
//
// A call that fails for itself (countedError) counts a failure against
// each of its pairs and the asking goes on; a pair is given up at its
// pairAttempts-th failure. A spent budget, an unavailable provider or no
// decision model ends the asking, counting nothing; the step goes on. The
// pairs left unanswered (EdgeFallbacks) keep their events undecided, to be
// asked again by the next step. After every edgeChunkCalls calls, and
// when the job is canceled, the answers so far go into the state and
// commit (when not nil) saves it, the latter with a context that is not
// canceled, so a step cut off resumes where it stopped; a canceled job
// then returns its error. changed reports whether the state changed.
func (l *linker) decideEventEdges(ctx context.Context, state *timelineState, kinds []string, result *ConsolidateResult, commit func(context.Context) error) (changed bool, err error) {
	nodes := eventNodes(*state)
	hot := hotClusters(nodes)
	asked := []eventEdgeKind{}
	for _, k := range eventEdgeKinds {
		if slices.Contains(kinds, k.name) {
			asked = append(asked, k)
		}
	}
	edges := state.EventEdges
	if edges == nil {
		edges = &eventEdges{Rules: eventEdgeRules}
	}
	// Decided under other candidate rules, every event is decided again.
	reset := edges.Rules != eventEdgeRules
	// open[k][i]: node i is undecided for the k-th kind asked; each such
	// node's text is a query.
	open, queryOf, queries := make([][]bool, len(asked)), map[int]int{}, []string{}
	for k, kind := range asked {
		open[k] = make([]bool, len(nodes))
		decided := map[string]bool{}
		if !reset {
			for _, id := range edges.Decided[kind.name] {
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
	var ranks [][]int
	if len(queries) > 0 {
		docs := make([]string, len(nodes))
		for i, n := range nodes {
			docs[i] = n.event.Text
		}
		var lexical bool
		if ranks, lexical, err = l.rankWith(ctx, queries, docs, len(docs), true); err != nil {
			return false, err
		}
		if lexical {
			result.EdgeLexical++
		}
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
	// for each event and kind, the questions to ask.
	type question struct {
		pair         *linkPair
		to           string
		failed, kept bool
	}
	type decision struct {
		kind, from string
		questions  []*question
		marked     bool
	}
	news, cands, newOf, candOf := []linkItem{}, []linkItem{}, map[int]int{}, map[int]int{}
	decisions, pairs, questionOf := []*decision{}, []*linkPair{}, map[*linkPair]*question{}
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
			d := &decision{kind: kind.name, from: n.id}
			for _, j := range edgeCandidates(kind.name, nodes, i, similar, hot, limit) {
				to := nodes[j].id
				if edges.answered(kind.name, n.id, to) || edges.givenUp(kind.name, n.id, to) {
					continue
				}
				if _, ok := newOf[i]; !ok {
					newOf[i] = len(news)
					news = append(news, eventItem(fmt.Sprintf("n%d", len(news)+1), n, titles))
				}
				if _, ok := candOf[j]; !ok {
					candOf[j] = len(cands)
					cands = append(cands, eventItem(fmt.Sprintf("c%d", len(cands)+1), nodes[j], titles))
				}
				p := &linkPair{kind: kind.pair, item: newOf[i], candidate: candOf[j]}
				questionOf[p] = &question{pair: p, to: to}
				d.questions = append(d.questions, questionOf[p])
				pairs = append(pairs, p)
			}
			decisions = append(decisions, d)
		}
	}
	// apply keeps what the calls so far answered or failed, and marks the
	// events whose pairs are all answered or given up; it reports whether
	// that changed the state.
	apply := func() (dirty bool) {
		if reset {
			edges.Rules, edges.Decided, reset, dirty = eventEdgeRules, nil, false, true
		}
		for _, d := range decisions {
			complete := true
			for _, q := range d.questions {
				switch {
				case q.kept:
				case q.pair.answered:
					weight := 0.0
					if q.pair.same >= 0.5 {
						weight = math.Round(q.pair.same*100) / 100
						count(&result.EdgeYes, d.kind)
					}
					count(&result.EdgeAsked, d.kind)
					edges.answer(d.kind, d.from, q.to, weight)
					q.kept, dirty = true, true
				case q.failed:
					nested(&edges.Failures, d.kind, d.from)[q.to]++
					q.kept, dirty = true, true
				}
				if !q.pair.answered && !edges.givenUp(d.kind, d.from, q.to) {
					complete = false
				}
			}
			if complete && !d.marked {
				if edges.Decided == nil {
					edges.Decided = map[string][]string{}
				}
				edges.Decided[d.kind] = append(edges.Decided[d.kind], d.from)
				d.marked, dirty = true, true
			}
		}
		if dirty {
			state.EventEdges, changed = edges, true
		}
		return dirty
	}
	defer func() {
		for _, p := range pairs {
			if !p.answered {
				result.EdgeFallbacks++
			}
		}
	}()
	calls := packLinks(news, cands, pairs, l.questionLimit())
	if len(calls) == 0 {
		apply()
		return changed, nil
	}
	var stop error
	if l.agent.models.Judge == nil {
		stop = errors.New("no decision model")
	}
	for start := 0; start < len(calls) && stop == nil; start += edgeChunkCalls {
		for _, call := range calls[start:min(start+edgeChunkCalls, len(calls))] {
			answers, err := l.decide(ctx, call.state, call.questions, state, result, &result.EdgeCalls)
			if err != nil {
				if !countedError(ctx, err) {
					stop = err
					break
				}
				for _, p := range call.pairs {
					questionOf[p].failed = true
				}
				continue
			}
			for j, p := range call.pairs {
				p.same, p.answered = answers[edgeKindOf(p.kind).key+strconv.Itoa(j)], true
			}
		}
		if apply() && commit != nil {
			saveCtx := ctx
			if ctx.Err() != nil {
				saveCtx = context.WithoutCancel(ctx)
			}
			if err := commit(saveCtx); err != nil {
				return changed, err
			}
		}
	}
	return changed, ctx.Err()
}

// count adds one to counts[kind], making counts when nil.
func count(counts *map[string]int, kind string) {
	if *counts == nil {
		*counts = map[string]int{}
	}
	(*counts)[kind]++
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
// any edge gives it. An entity edge counts only while its events share a
// cluster that is not hot (hotClusters, of the memory as it is now), so
// the overlay never joins events by a speaker alone; its decision stays.
func (b *edgeBuilder) addEventEdges(state timelineState) {
	if state.EventEdges == nil {
		return
	}
	nodes := eventNodes(state)
	hot, node := hotClusters(nodes), map[string]eventNode{}
	for _, n := range nodes {
		node[n.id] = n
	}
	keep := func(l judgedLink, weight float64) {
		if weight > b.judged[l] {
			b.judged[l] = weight
		}
	}
	for _, kind := range b.kinds {
		for from, answers := range state.EventEdges.Answers[kind] {
			for to, weight := range answers {
				if weight < 0.5 || kind == "entity" && !sharesCluster(node[from], node[to], hot) {
					continue
				}
				for _, x := range node[from].event.Records {
					for _, y := range node[to].event.Records {
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
