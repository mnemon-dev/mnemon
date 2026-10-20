package memoryagent

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// L1a links one batch's extracted items (batch-local threads, keys and
// directives) into the stored memory M, in batch order. Each new thread is
// compared with up to LinkCandidates threads of M, each new key with up to
// LinkCandidates keys of M, retrieved by similarity; JEV answers yes/no
// questions about each pair, packed into few calls. A thread joins the
// thread JEV likes best (≥ 0.5) or starts one; a key appends its values to
// the key JEV likes best, or to the key of the same name, or starts one,
// and a value JEV finds contradicts the stored latest is marked a conflict.
// Directives keep the author's de-duplication. When JEV fails, the batch
// links nothing but its keys of the same name. Lengths are UTF-16 units.

// The linking questions, in the style of the author's (simple_questions.go):
// one sentence naming the two items by id, with yes and no criteria.

func continuesQuestion(n, c string) string {
	return "Does the new thread whose `id` is \"" + n + "\" follow the same specific sub-topic as the candidate whose `id` is \"" + c + "\", so that its events continue that thread?"
}

const (
	continuesYes = "Both are about the same specific feature, component, problem, decision, document, exam, event, relationship or goal, and the new events continue or update it."
	continuesNo  = "They only share a project, person, place or general topic, or are different parts of a larger project."
)

func sameKeyQuestion(n, c string) string {
	return "Do the new fact whose `id` is \"" + n + "\" and the candidate whose `id` is \"" + c + "\" give the value of the same attribute of the same thing?"
}

const (
	sameKeyYes = "The same thing and the same attribute (the same deadline, count, place, status, choice or name), even if worded differently."
	sameKeyNo  = "Different things, or different attributes of the same thing."
)

func contradictsQuestion(n, c string) string {
	return "Does the value of the new fact whose `id` is \"" + n + "\" contradict the latest value of the candidate whose `id` is \"" + c + "\" without the records saying that it changed?"
}

const (
	contradictsYes = "Both values cannot be true at once and nothing says the value was changed, moved, rescheduled or corrected."
	contradictsNo  = "The values agree, or the new value is stated as a change, update or correction."
)

// linkItem is a thread or a key as JEV reads it: a new one, n1, n2, …, or a
// stored one, by thread id or K<i> (the i-th key of M, from 1). An entity
// name (edges.go) is an item with its Name and the texts naming it; an
// event (event_edges.go) one with its date, thread title and names.
type linkItem struct {
	ID       string   `json:"id"`
	Name     string   `json:"name,omitempty"`
	Date     string   `json:"date,omitempty"`
	Thread   string   `json:"thread,omitempty"`
	Text     string   `json:"text"`
	Entities []string `json:"entities,omitempty"`
}

// linkState is a linking call's state: the new items and the candidates its
// questions name, each once.
type linkState struct {
	New        []linkItem `json:"new"`
	Candidates []linkItem `json:"candidates"`
}

// linkCounts is what linking one batch did: its threads that continued a
// stored thread or started one, its keys that joined a stored key or
// started one, the joined keys whose values JEV found contradict the stored
// latest, whether JEV failed (Fallback), and whether the embedder failed,
// so that shared words found the candidates (Lexical).
type linkCounts struct {
	ThreadsLinked, ThreadsNew, KeysLinked, KeysNew, Conflicts int
	Fallback, Lexical                                         bool
}

// addLinks adds a linked batch's counts to the job's.
func (r *ConsolidateResult) addLinks(c linkCounts) {
	r.ThreadsLinked += c.ThreadsLinked
	r.ThreadsNew += c.ThreadsNew
	r.KeysLinked += c.KeysLinked
	r.KeysNew += c.KeysNew
	r.Conflicts += c.Conflicts
	if c.Fallback {
		r.LinkFallbacks++
	}
	if c.Lexical {
		r.LinkLexical++
	}
}

// The kinds of pair: does a new thread continue a stored one; is a new key
// a stored one, and does its value contradict it; does a new key's value
// contradict the stored key of the same name; do two entity names name the
// same one (edges.go); does a kind of event edge join a new event and an
// earlier one (event_edges.go); does a new insight state the same
// conclusion as a stored one (insights.go).
const (
	linkThread = iota
	linkKey
	linkExact
	linkAlias
	linkSemantic
	linkTemporal
	linkCausal
	linkEntity
	linkInsight
)

// linkPair is a new item (an index into the new items) and a candidate (an
// index into the candidates), and JEV's answers about them (answered once
// its call succeeded).
type linkPair struct {
	kind              int
	item, candidate   int
	same, contradicts float64
	answered          bool
}

// linker links the batches of one job in order. It keeps the document
// vectors it reads, by text, for the job's later batches.
type linker struct {
	agent   *Agent
	k       int
	vectors map[string][]float32
}

func (a *Agent) newLinker() *linker {
	return &linker{agent: a, k: a.config.Consolidation.LinkCandidates, vectors: map[string][]float32{}}
}

// latestText is a key as linking reads it: "<key>: <latest value>".
func latestText(key string, values []factValue) string {
	return key + ": " + values[len(values)-1].Value
}

// link returns state.Memory with batch linked into it, leaving state.Memory
// unchanged, and what linking did. Every JEV call it makes adds its usage to
// the state and the result. A JEV error or spent budget links nothing but
// the keys of the same name (Fallback); only a canceled job is an error.
func (l *linker) link(ctx context.Context, state *timelineState, batch timelineMemory, result *ConsolidateResult) (timelineMemory, linkCounts, error) {
	m := state.Memory
	newThreads, newEvents := eventThreads(batch)
	oldThreads, oldEvents := eventThreads(m)
	titles := map[string]string{}
	for _, t := range batch.Threads {
		titles[t.ID] = t.Title
	}
	oldTitles := map[string]string{}
	for _, t := range m.Threads {
		oldTitles[t.ID] = t.Title
	}
	// The new items, threads then keys, and the candidates, threads then
	// keys, as JEV reads them; and the texts retrieval compares.
	news, cands := []linkItem{}, []linkItem{}
	threadQueries, threadDocs, keyQueries, keyDocs := []string{}, []string{}, []string{}, []string{}
	for _, id := range newThreads {
		text := threadText(titles[id], batch, newEvents[id])
		news = append(news, linkItem{ID: fmt.Sprintf("n%d", len(news)+1), Text: authorExcerpt(text, simpleListing)})
		threadQueries = append(threadQueries, text)
	}
	for _, key := range batch.FactOrder {
		text := latestText(key, batch.Facts[key])
		news = append(news, linkItem{ID: fmt.Sprintf("n%d", len(news)+1), Text: authorExcerpt(text, simpleListing)})
	}
	for _, id := range oldThreads {
		text := threadText(oldTitles[id], m, oldEvents[id])
		cands = append(cands, linkItem{ID: id, Text: authorExcerpt(text, simpleListing)})
		threadDocs = append(threadDocs, text)
	}
	stored := map[string]int{}
	for i, key := range m.FactOrder {
		text := latestText(key, m.Facts[key])
		stored[key] = i
		cands = append(cands, linkItem{ID: fmt.Sprintf("K%d", i+1), Text: authorExcerpt(text, simpleListing)})
		keyDocs = append(keyDocs, text)
	}
	// Keys not stored by name are compared with the nearest stored keys:
	// query[j] is the j-th batch key's query.
	query := map[int]int{}
	for j, key := range batch.FactOrder {
		if _, ok := stored[key]; !ok {
			query[j] = len(keyQueries)
			keyQueries = append(keyQueries, latestText(key, batch.Facts[key]))
		}
	}
	threadRanks, threadsLexical, err := l.rank(ctx, threadQueries, threadDocs)
	if err != nil {
		return timelineMemory{}, linkCounts{}, err
	}
	keyRanks, keysLexical, err := l.rank(ctx, keyQueries, keyDocs)
	if err != nil {
		return timelineMemory{}, linkCounts{}, err
	}
	counts := linkCounts{Lexical: threadsLexical || keysLexical}
	pairs := []*linkPair{}
	threadPairs, keyPairs := make([][]*linkPair, len(newThreads)), make([][]*linkPair, len(batch.FactOrder))
	for i, ranked := range threadRanks {
		for _, c := range ranked {
			threadPairs[i] = append(threadPairs[i], &linkPair{kind: linkThread, item: i, candidate: c})
		}
		pairs = append(pairs, threadPairs[i]...)
	}
	for j, key := range batch.FactOrder {
		item := len(newThreads) + j
		if at, ok := stored[key]; ok {
			keyPairs[j] = []*linkPair{{kind: linkExact, item: item, candidate: len(oldThreads) + at}}
		} else {
			for _, c := range keyRanks[query[j]] {
				keyPairs[j] = append(keyPairs[j], &linkPair{kind: linkKey, item: item, candidate: len(oldThreads) + c})
			}
		}
		pairs = append(pairs, keyPairs[j]...)
	}
	if err := l.judge(ctx, news, cands, pairs, state, result, &result.LinkCalls); err != nil {
		if ctx.Err() != nil {
			return timelineMemory{}, linkCounts{}, err
		}
		counts.Fallback = true
		for _, p := range pairs {
			p.same, p.contradicts = 0, 0
		}
	}

	out := cloneMemory(m)
	threadOf := map[string]string{}
	for i, id := range newThreads {
		answers := []float64{}
		for _, p := range threadPairs[i] {
			answers = append(answers, p.same)
		}
		if b := bestLink(answers); b >= 0 {
			threadOf[id] = oldThreads[threadPairs[i][b].candidate]
			counts.ThreadsLinked++
			continue
		}
		fresh := freshThreadID(out)
		out.Threads = append(out.Threads, thread{ID: fresh, Title: titles[id]})
		threadOf[id] = fresh
		counts.ThreadsNew++
	}
	for _, e := range batch.Events {
		e.Thread = threadOf[e.Thread]
		out.Events = append(out.Events, e)
	}
	for j, key := range batch.FactOrder {
		values := slices.Clone(batch.Facts[key])
		target, contradicts := "", 0.0
		if ps := keyPairs[j]; len(ps) > 0 && ps[0].kind == linkExact {
			target, contradicts = key, ps[0].contradicts
		} else {
			answers := []float64{}
			for _, p := range ps {
				answers = append(answers, p.same)
			}
			if b := bestLink(answers); b >= 0 {
				target, contradicts = m.FactOrder[ps[b].candidate-len(oldThreads)], ps[b].contradicts
			}
		}
		if target == "" {
			out.FactOrder = append(out.FactOrder, key)
			out.Facts[key] = values
			counts.KeysNew++
			continue
		}
		counts.KeysLinked++
		if contradicts >= 0.5 {
			counts.Conflicts++
			for i := range values {
				values[i].Conflict = true
			}
		}
		out.Facts[target] = append(out.Facts[target], values...)
	}
	for _, d := range batch.Directives {
		if !slices.ContainsFunc(out.Directives, func(old directive) bool { return restates(old.Text, d.Text) }) {
			out.Directives = append(out.Directives, d)
		}
	}
	return out, counts, nil
}

// bestLink is the index of the highest answer of at least 0.5, the first of
// equal ones (the better-ranked candidate); -1 when there is none.
func bestLink(answers []float64) int {
	best := -1
	for i, p := range answers {
		if p >= 0.5 && (best < 0 || p > answers[best]) {
			best = i
		}
	}
	return best
}

// freshThreadID is the first T<n>, from the number of threads plus one,
// that no thread of m has.
func freshThreadID(m timelineMemory) string {
	for n := len(m.Threads) + 1; ; n++ {
		id := fmt.Sprintf("T%d", n)
		if !slices.ContainsFunc(m.Threads, func(t thread) bool { return t.ID == id }) {
			return id
		}
	}
}

// rank returns, for each query, the indexes of the l.k docs most similar to
// it, best first: by the cosine of their document vectors when the agent has
// an embedder (queries and docs both embedded as documents; the docs, stored
// texts, through the layer's vector cache, the queries, read once, without
// storing them), ties in doc order; else, or when embedding fails
// (lexical), by shared words (BM25, as the layer falls back to). A doc with
// no similarity at all (a cosine of 0 or less, no shared word) is not
// ranked.
func (l *linker) rank(ctx context.Context, queries, docs []string) (ranks [][]int, lexical bool, err error) {
	return l.rankWith(ctx, queries, docs, l.k, true)
}

// rankWith is rank keeping the limit best docs of each query; storeDocs
// says whether the docs' vectors go through the layer's vector cache, or are
// embedded without storing them, as the queries are.
func (l *linker) rankWith(ctx context.Context, queries, docs []string, limit int, storeDocs bool) (ranks [][]int, lexical bool, err error) {
	out := make([][]int, len(queries))
	if len(queries) == 0 || len(docs) == 0 {
		return out, false, nil
	}
	queryVectors, ok := l.embed(ctx, queries, false)
	var docVectors [][]float32
	if ok {
		docVectors, ok = l.embed(ctx, docs, storeDocs)
	}
	if ok && len(queryVectors[0]) == len(docVectors[0]) {
		for i, query := range queryVectors {
			order, score := []int{}, make([]float64, len(docVectors))
			for j, doc := range docVectors {
				if score[j] = records.VectorDot(query, doc); score[j] > 0 {
					order = append(order, j)
				}
			}
			slices.SortStableFunc(order, func(a, b int) int { return probabilityOrder(score[a], score[b]) })
			out[i] = order[:min(limit, len(order))]
		}
		return out, false, nil
	}
	if ctx.Err() != nil {
		return nil, false, ctx.Err()
	}
	index := newBM25(docs)
	for i, query := range queries {
		out[i] = index.top(query, limit)
	}
	return out, l.agent.config.Embedding != nil, nil
}

// embed is the document vector of each text, from the job's own cache, else
// the layer's (layerDocuments) when store, else embedded without storing it
// (documentVectors); ok is false without an embedder, when embedding fails,
// or when the vectors differ in size. texts is not empty.
func (l *linker) embed(ctx context.Context, texts []string, store bool) ([][]float32, bool) {
	if l.agent.config.Embedding == nil {
		return nil, false
	}
	missing, seen := []string{}, map[string]bool{}
	for _, text := range texts {
		if l.vectors[text] == nil && !seen[text] {
			missing, seen[text] = append(missing, text), true
		}
	}
	if len(missing) > 0 {
		vectors, err := l.agent.documentVectors(ctx, missing, store)
		if err != nil || len(vectors) != len(missing) {
			return nil, false
		}
		for i, text := range missing {
			l.vectors[text] = vectors[i]
		}
	}
	out := make([][]float32, len(texts))
	for i, text := range texts {
		if out[i] = l.vectors[text]; len(out[i]) == 0 || len(out[i]) != len(out[0]) {
			return nil, false
		}
	}
	return out, true
}

// linkRequest is one packed linking call: its state, its questions and the
// pairs they ask about, in order (pair j asks t<j>, or i<j> and x<j>, or
// x<j>, or a<j>, or an event edge kind's key and j, or g<j>), and the
// request's size
// so far, as JEV's client measures it, plus at most one unit per list item.
type linkRequest struct {
	state     linkState
	questions map[string]Question
	pairs     []*linkPair
	size      int
	news      map[int]bool
	cands     map[int]bool
}

// pairQuestions is the questions of pair p as the j-th pair of its call.
func pairQuestions(p *linkPair, news, cands []linkItem, j int) map[string]Question {
	n, c := news[p.item].ID, cands[p.candidate].ID
	contradicts := Question{Type: "noul", Instructions: contradictsQuestion(n, c), Criteria: map[string]string{"true": contradictsYes, "false": contradictsNo}}
	switch p.kind {
	case linkThread:
		return map[string]Question{fmt.Sprintf("t%d", j): {Type: "noul", Instructions: continuesQuestion(n, c), Criteria: map[string]string{"true": continuesYes, "false": continuesNo}}}
	case linkKey:
		return map[string]Question{fmt.Sprintf("i%d", j): {Type: "noul", Instructions: sameKeyQuestion(n, c), Criteria: map[string]string{"true": sameKeyYes, "false": sameKeyNo}}, fmt.Sprintf("x%d", j): contradicts}
	case linkAlias:
		return map[string]Question{fmt.Sprintf("a%d", j): {Type: "noul", Instructions: aliasQuestion(n, c), Criteria: map[string]string{"true": aliasYes, "false": aliasNo}}}
	case linkSemantic, linkTemporal, linkCausal, linkEntity:
		k := edgeKindOf(p.kind)
		return map[string]Question{k.key + strconv.Itoa(j): {Type: "noul", Instructions: k.question(n, c), Criteria: map[string]string{"true": k.yes, "false": k.no}}}
	case linkInsight:
		return map[string]Question{fmt.Sprintf("g%d", j): {Type: "noul", Instructions: sameInsightQuestion(n, c), Criteria: map[string]string{"true": sameInsightYes, "false": sameInsightNo}}}
	}
	return map[string]Question{fmt.Sprintf("x%d", j): contradicts}
}

// jsonUnits is the UTF-16 length of v as JSON.stringify writes it.
func jsonUnits(v any) int {
	text, _ := stringifyJSON(v)
	return jsLen(text)
}

// packLinks packs the pairs, in order, into as few calls as hold at most
// limit questions and maxJEVRequest units each; a pair's questions share
// one call.
func packLinks(news, cands []linkItem, pairs []*linkPair, limit int) []*linkRequest {
	empty, _ := jevRequest(linkState{New: []linkItem{}, Candidates: []linkItem{}}, map[string]Question{})
	base := jsLen(empty)
	cost := func(call *linkRequest, p *linkPair) (int, map[string]Question) {
		questions, units := pairQuestions(p, news, cands, len(call.pairs)), 0
		if !call.news[p.item] {
			units += jsonUnits(news[p.item]) + 1
		}
		if !call.cands[p.candidate] {
			units += jsonUnits(cands[p.candidate]) + 1
		}
		for key, q := range questions {
			units += jsonUnits(key) + 1 + jsonUnits(q) + 1
		}
		return units, questions
	}
	calls := []*linkRequest{}
	for _, p := range pairs {
		var call *linkRequest
		var units int
		var questions map[string]Question
		if len(calls) > 0 {
			call = calls[len(calls)-1]
			if units, questions = cost(call, p); len(call.questions)+len(questions) > limit || call.size+units > maxJEVRequest {
				call = nil
			}
		}
		if call == nil {
			call = &linkRequest{state: linkState{New: []linkItem{}, Candidates: []linkItem{}}, questions: map[string]Question{}, size: base, news: map[int]bool{}, cands: map[int]bool{}}
			calls = append(calls, call)
			units, questions = cost(call, p)
		}
		if !call.news[p.item] {
			call.news[p.item] = true
			call.state.New = append(call.state.New, news[p.item])
		}
		if !call.cands[p.candidate] {
			call.cands[p.candidate] = true
			call.state.Candidates = append(call.state.Candidates, cands[p.candidate])
		}
		for key, q := range questions {
			call.questions[key] = q
		}
		call.pairs = append(call.pairs, p)
		call.size += units
	}
	return calls
}

// judge asks JEV the pairs' questions, packed by packLinks within the
// judge's question limit (128 unless it states fewer), one call after
// another, and sets the pairs' answers; calls counts the calls. The first
// failed call ends the judging with its error.
func (l *linker) judge(ctx context.Context, news, cands []linkItem, pairs []*linkPair, state *timelineState, result *ConsolidateResult, calls *int) error {
	for _, call := range packLinks(news, cands, pairs, l.questionLimit()) {
		answers, err := l.decide(ctx, call.state, call.questions, state, result, calls)
		if err != nil {
			return err
		}
		for j, p := range call.pairs {
			switch p.kind {
			case linkThread:
				p.same = answers[fmt.Sprintf("t%d", j)]
			case linkKey:
				p.same, p.contradicts = answers[fmt.Sprintf("i%d", j)], answers[fmt.Sprintf("x%d", j)]
			case linkAlias:
				p.same = answers[fmt.Sprintf("a%d", j)]
			case linkInsight:
				p.same = answers[fmt.Sprintf("g%d", j)]
			default:
				p.contradicts = answers[fmt.Sprintf("x%d", j)]
			}
			p.answered = true
		}
	}
	return nil
}

// questionLimit is the questions a linking call may ask: 128, or fewer
// when the judge states fewer.
func (l *linker) questionLimit() int {
	if limited, ok := l.agent.models.Judge.(interface{ MaxQuestions() int }); ok {
		return min(128, limited.MaxQuestions())
	}
	return 128
}

// decide makes one linking call and meters it: its usage is the job's and
// the state's, and the job's linking usage; calls counts it.
func (l *linker) decide(ctx context.Context, s linkState, questions map[string]Question, state *timelineState, result *ConsolidateResult, calls *int) (map[string]float64, error) {
	start := time.Now()
	answers, usage, err := l.agent.models.Judge.Decide(ctx, s, questions)
	result.ModelMS += float64(time.Since(start).Microseconds()) / 1000
	result.Usage.add(usage)
	state.Usage.add(usage)
	if result.LinkUsage == nil {
		result.LinkUsage = &Usage{}
	}
	result.LinkUsage.add(usage)
	*calls++
	if err == nil {
		err = validateAnswers(answers, questions)
	}
	return answers, err
}
