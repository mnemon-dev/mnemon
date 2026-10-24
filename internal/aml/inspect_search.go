package aml

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"runtime/debug"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
	"github.com/mnemon-dev/mnemon/internal/records"
)

// POST /v1/search: a retrieval and its trace (docs/inspect-api.md). Lexical
// mode is rawRanking, what aml-serve answers a degraded Search with; full
// mode is the read a Search makes, without its flush, traced
// (memoryagent.ReadTrace) and billed to the debug budget.

// inspectSearch is a checked search request; key is the store's.
type inspectSearch struct {
	key           string
	query         string
	options       []string
	topK          int
	full, tracing bool
}

// question is what a full read reads, as /search builds it.
func (q inspectSearch) question() string {
	return SearchRequest{Query: q.query, Options: q.options}.question()
}

// present is a field's value, unless it is absent or null.
func present(fields map[string]json.RawMessage, name string) (json.RawMessage, bool) {
	raw, ok := fields[name]
	if !ok || strings.TrimSpace(string(raw)) == "null" {
		return nil, false
	}
	return raw, true
}

// parseSearch checks a search request; on failure it has answered 400 (or
// 413) naming the field.
func (i *Inspector) parseSearch(w http.ResponseWriter, r *http.Request) (inspectSearch, bool) {
	var fields map[string]json.RawMessage
	if !decodeBody(w, r, &fields) {
		return inspectSearch{}, false
	}
	q := inspectSearch{topK: i.topKDefault(), tracing: true}
	rawKey, hasKey := present(fields, "key")
	rawUser, hasUser := present(fields, "user_id")
	switch {
	case hasKey == hasUser:
		invalid(w, "key")
		return q, false
	case hasKey:
		if json.Unmarshal(rawKey, &q.key) != nil || !isUserKey(q.key) {
			invalid(w, "key")
			return q, false
		}
	default:
		user := scalarText(rawUser)
		if strings.TrimSpace(user) == "" {
			invalid(w, "user_id")
			return q, false
		}
		q.key = userKey(user)
	}
	if q.query = scalarText(fields["query"]); strings.TrimSpace(q.query) == "" || len(q.query) > inspectQueryMax {
		invalid(w, "query")
		return q, false
	}
	if raw, ok := present(fields, "options"); ok && !parseOptions(raw, &q.options) {
		invalid(w, "options")
		return q, false
	}
	if raw, ok := present(fields, "top_k"); ok {
		if json.Unmarshal(raw, &q.topK) != nil || q.topK < 1 || q.topK > inspectTopKMax {
			invalid(w, "top_k")
			return q, false
		}
	}
	if raw, ok := present(fields, "mode"); ok {
		var mode string
		if json.Unmarshal(raw, &mode) != nil || mode != "lexical" && mode != "full" {
			invalid(w, "mode")
			return q, false
		}
		q.full = mode == "full"
	}
	if raw, ok := present(fields, "trace"); ok && json.Unmarshal(raw, &q.tracing) != nil {
		invalid(w, "trace")
		return q, false
	}
	return q, true
}

// parseOptions reads the options as /search does (strings, or numbers by
// their decimal text, the empty ones dropped); anything but an array of
// strings and numbers is refused.
func parseOptions(raw json.RawMessage, into *[]string) bool {
	var options []json.RawMessage
	if json.Unmarshal(raw, &options) != nil {
		return false
	}
	for _, option := range options {
		trimmed := strings.TrimSpace(string(option))
		if trimmed == "" || trimmed[0] != '"' && trimmed[0] != '-' && (trimmed[0] < '0' || trimmed[0] > '9') {
			return false
		}
		if text := scalarText(option); text != "" {
			*into = append(*into, text)
		}
	}
	return true
}

// serveSearch answers POST /v1/search.
func (i *Inspector) serveSearch(w http.ResponseWriter, r *http.Request) {
	q, ok := i.parseSearch(w, r)
	if !ok {
		return
	}
	noteStore(r, q.key)
	if q.full && !i.full {
		inspectError(w, http.StatusConflict, "full_disabled")
		return
	}
	var answer searchOutcome
	err := i.readFrom(q.key, func(s *readStore) error {
		var err error
		if q.full {
			answer, err = i.fullSearch(r.Context(), s, q)
		} else {
			answer, err = i.lexicalSearch(r.Context(), s, q)
		}
		return err
	})
	switch {
	case err != nil:
		i.storeFailure(w, r, q.key, "search", err)
		return
	case !q.tracing:
		answer.body["trace"] = nil
	}
	inspectJSON(w, answer.status, answer.body)
}

// searchOutcome is a search's answer: its status and body.
type searchOutcome struct {
	status int
	body   map[string]any
}

// readFrom opens the store with key as a reader, runs read on it, and closes
// it, checking that what it read stands (closeStore); a panic in read is its
// error. A missing store is an error wrapping fs.ErrNotExist.
func (i *Inspector) readFrom(key string, read func(*readStore) error) (err error) {
	s, err := i.openStore(key)
	if err != nil {
		return err
	}
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("panic: %v\n%s", value, debug.Stack())
		}
		err = errors.Join(err, i.closeStore(s))
	}()
	return read(s)
}

// rawWarn logs, by the store's prefix, what a raw ranking fell back from.
func (i *Inspector) rawWarn(key string) func(event string, err error) {
	return func(event string, err error) {
		i.log.Warn(fmt.Sprintf("%s (store %s): %v", event, short(key), withoutPath(err)))
	}
}

// lexicalSearch is the raw ranking (rawRanking), scored by position as a
// Search's items, and its trace: pending, candidates, compose.
func (i *Inspector) lexicalSearch(ctx context.Context, s *readStore, q inspectSearch) (searchOutcome, error) {
	pending, err := i.pendingStage(ctx, s)
	if err != nil {
		return searchOutcome{}, err
	}
	start := time.Now()
	ranking, err := rawRanking(ctx, s.store, s.agent, i.config.MemoryConfig, q.query, q.topK, i.rawWarn(s.key))
	if err != nil {
		return searchOutcome{}, err
	}
	scoreByPosition(ranking.items)
	scores, err := s.store.LexicalScores(ctx, ranking.revision, records.Scope{}, []string{q.query}, [][]string{ranking.hydrated})
	if err != nil {
		return searchOutcome{}, err
	}
	candidates := candidatesStage{stageHead: stageHead{Name: "candidates", MS: millis(time.Since(start))},
		Searches: []traceSearch{{Search: 0, Text: excerpt(q.query), Kind: "query", Source: "record", Ranked: ranking.ranked, IDs: ranking.hydrated, Scores: scores[0]}}}
	compose := composeStage{stageHead: stageHead{Name: "compose"}, Blocks: composeBlocks(ranking.items, nil), Order: i.recordOrder()}
	return searchOutcome{status: http.StatusOK, body: map[string]any{
		"store": short(s.key), "mode": "lexical", "result": ranking.items, "degraded": []string{},
		"trace": []any{pending, candidates, compose}, "usage": searchUsage{Tokens: tokenCount{}, Cost: usageByKind{}},
	}}, nil
}

// fullSearch runs the read a Search runs, without the flush, traced and
// billed to the debug budget. A read the debug budget refused answers 402;
// one that failed otherwise 502 with the raw ranking, as a Search answers.
func (i *Inspector) fullSearch(ctx context.Context, s *readStore, q inspectSearch) (searchOutcome, error) {
	pending, err := i.pendingStage(ctx, s)
	if err != nil {
		return searchOutcome{}, err
	}
	exhausted, err := i.debugExhausted(ctx)
	if err != nil {
		return searchOutcome{}, err
	}
	if exhausted {
		return searchOutcome{status: http.StatusPaymentRequired, body: map[string]any{"error": "debug_budget_exhausted", "trace": nil, "usage": searchUsage{}}}, nil
	}
	trace := &memoryagent.ReadTrace{}
	maxItems := 0
	if i.maxItems > 0 {
		maxItems = min(q.topK, i.maxItems)
	}
	view, readErr := i.read(ctx, s, trace, memoryagent.ReadRequest{Question: q.question(), MaxItems: maxItems})
	usage := traceUsage(trace)
	i.countFull(usage.Cost)
	var result []SearchItem
	if readErr == nil {
		result = searchItems(view, q.topK)
		scoreByPosition(result)
	}
	stages, err := i.fullStages(ctx, s, pending, trace, view, result)
	if err != nil {
		return searchOutcome{}, err
	}
	switch {
	case readErr == nil:
		degraded := view.Stats.Degraded
		if degraded == nil {
			degraded = []string{}
		}
		return searchOutcome{status: http.StatusOK, body: map[string]any{"store": short(s.key), "mode": "full", "result": result, "degraded": degraded, "trace": stages, "usage": usage}}, nil
	case ctx.Err() != nil:
		return searchOutcome{}, ctx.Err()
	case errors.Is(readErr, memoryagent.ErrBudgetCap):
		return searchOutcome{status: http.StatusPaymentRequired, body: map[string]any{"error": "debug_budget_exhausted", "trace": stages, "usage": usage}}, nil
	}
	i.log.Warn(fmt.Sprintf("full search failed (store %s): %v", short(s.key), withoutPath(readErr)))
	ranking, err := rawRanking(ctx, s.store, s.agent, i.config.MemoryConfig, q.query, q.topK, i.rawWarn(s.key))
	if err != nil {
		return searchOutcome{}, err
	}
	scoreByPosition(ranking.items)
	return searchOutcome{status: http.StatusBadGateway, body: map[string]any{"error": "provider_failed", "result": ranking.items, "trace": stages, "usage": usage}}, nil
}

// read is the store's read within fullSearchTimeout, traced; a read that
// panics fails, its panic and stack logged.
func (i *Inspector) read(ctx context.Context, s *readStore, trace *memoryagent.ReadTrace, req memoryagent.ReadRequest) (view memoryagent.View, err error) {
	ctx, cancel := context.WithTimeout(memoryagent.WithReadTrace(ctx, trace), fullSearchTimeout)
	defer cancel()
	defer func() {
		if value := recover(); value != nil {
			i.log.Error(fmt.Sprintf("full search panicked (store %s): %v\n%s", short(s.key), value, debug.Stack()))
			view, err = memoryagent.View{}, fmt.Errorf("panic: %v", value)
		}
	}()
	return s.agent.Read(ctx, req)
}

// debugExhausted reports whether a paid kind of the read has nothing left in
// the debug budget.
func (i *Inspector) debugExhausted(ctx context.Context) (bool, error) {
	spent, held, err := i.debugAccounts(ctx)
	if err != nil {
		return false, err
	}
	for _, kind := range i.readKinds() {
		if i.remaining(kind, spent, held) <= 0 {
			return true, nil
		}
	}
	return false, nil
}

// countFull counts a full search that read, and its cost.
func (i *Inspector) countFull(cost usageByKind) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.fullSearches++
	i.fullCost["deepseek"] += cost.DeepSeek
	i.fullCost["jev"] += cost.JEV
}

// recordOrder is how the View lists its records: time or rank.
func (i *Inspector) recordOrder() string {
	if view := i.config.MemoryConfig.View; view != nil && view.Order == "time" {
		return "time"
	}
	return "rank"
}

// The trace's JSON (docs/inspect-api.md, "The trace").
type (
	stageHead struct {
		Name   string      `json:"name"`
		MS     float64     `json:"ms"`
		Calls  int         `json:"calls"`
		Tokens tokenCount  `json:"tokens"`
		Cost   usageByKind `json:"cost"`
	}
	tokenCount struct {
		Input  int `json:"input"`
		Output int `json:"output"`
	}
	searchUsage struct {
		Calls  int         `json:"calls"`
		Tokens tokenCount  `json:"tokens"`
		Cost   usageByKind `json:"cost"`
	}
	pendingStage struct {
		stageHead
		Count     int      `json:"count"`
		IDs       []string `json:"ids"`
		Watermark int64    `json:"watermark"`
		Folded    bool     `json:"folded"`
	}
	cuesStage struct {
		stageHead
		Cues     []traceCue  `json:"cues"`
		Needs    []traceNeed `json:"needs"`
		Needless bool        `json:"needless"`
	}
	traceCue struct {
		Text string `json:"text"`
		Kind string `json:"kind"`
	}
	traceNeed struct {
		Text string `json:"text"`
		All  bool   `json:"all"`
	}
	candidatesStage struct {
		stageHead
		Searches []traceSearch     `json:"searches"`
		Pool     *[]string         `json:"pool,omitempty"`
		Layer    *[]layerCandidate `json:"layer,omitempty"`
	}
	traceSearch struct {
		Search int       `json:"search"`
		Text   string    `json:"text"`
		Kind   string    `json:"kind"`
		Source string    `json:"source"`
		Ranked int       `json:"ranked"`
		IDs    []string  `json:"ids"`
		Scores []float64 `json:"scores"`
	}
	layerCandidate struct {
		ID     string `json:"id"`
		Kind   string `json:"kind"`
		Source string `json:"source"`
		Lit    int    `json:"lit"`
		Near   *int   `json:"near"`
	}
	judgeStage struct {
		stageHead
		Records []judgedRecord `json:"records"`
		Layer   []judgedLayer  `json:"layer"`
		Linked  []judgedLinked `json:"linked"`
	}
	judgedRecord struct {
		ID         string    `json:"id"`
		Source     string    `json:"source"`
		Excerpt    string    `json:"excerpt"`
		Screen     *float64  `json:"screen"`
		Row        []float64 `json:"row"`
		Needed     float64   `json:"needed"`
		Superseded float64   `json:"superseded"`
		Judged     bool      `json:"judged"`
		Decision   string    `json:"decision"`
		Shown      bool      `json:"shown"`
	}
	judgedLayer struct {
		ID       string  `json:"id"`
		Kind     string  `json:"kind"`
		Source   string  `json:"source"`
		Excerpt  string  `json:"excerpt"`
		Needed   float64 `json:"needed"`
		Decision string  `json:"decision"`
	}
	judgedLinked struct {
		ID       string  `json:"id"`
		Source   string  `json:"source"`
		Excerpt  string  `json:"excerpt"`
		Needed   float64 `json:"needed"`
		Decision string  `json:"decision"`
		Shown    bool    `json:"shown"`
	}
	needStage struct {
		stageHead
		Rounds []traceRound `json:"rounds"`
		States []string     `json:"states"`
		Stop   string       `json:"stop"`
	}
	traceRound struct {
		Round    int           `json:"round"`
		Steps    []traceStep   `json:"steps"`
		Pages    []tracePage   `json:"pages"`
		Searches []traceSearch `json:"searches"`
		Fresh    []string      `json:"fresh"`
	}
	traceStep struct {
		Need int    `json:"need"`
		Step string `json:"step"`
	}
	tracePage struct {
		Search int       `json:"search"`
		IDs    []string  `json:"ids"`
		Scores []float64 `json:"scores"`
	}
	composeStage struct {
		stageHead
		Blocks    []traceBlock `json:"blocks"`
		Truncated bool         `json:"truncated"`
		Order     string       `json:"order"`
	}
	traceBlock struct {
		Position int      `json:"position"`
		ID       string   `json:"id"`
		Layer    string   `json:"layer"`
		Items    []string `json:"items"`
		Records  []string `json:"records"`
		Excerpt  string   `json:"excerpt"`
	}
)

// millis is a duration in milliseconds, rounded to 0.1.
func millis(d time.Duration) float64 { return math.Round(float64(d.Microseconds())/100) / 10 }

// excerpt is text cut to excerptMax characters: its first excerptMax-1 and
// "…" when it is longer.
func excerpt(text string) string {
	if utf8.RuneCountInString(text) <= excerptMax {
		return text
	}
	return string([]rune(text)[:excerptMax-1]) + "…"
}

// costOf is a use's cost by kind: DeepSeek's CNY and JEV's USD.
func costOf(usage memoryagent.Usage) usageByKind {
	return usageByKind{DeepSeek: usage.CNY, JEV: usage.USD}
}

// headOf is the head of a traced stage.
func headOf(s memoryagent.TraceStage) stageHead {
	return stageHead{Name: s.Name, MS: millis(s.Duration), Calls: s.PlannerCalls + s.DecisionCalls,
		Tokens: tokenCount{Input: s.Usage.InputTokens, Output: s.Usage.OutputTokens}, Cost: costOf(s.Usage)}
}

// traceUsage is a read's use, the sum of its stages.
func traceUsage(trace *memoryagent.ReadTrace) searchUsage {
	var usage memoryagent.Usage
	calls := 0
	for _, s := range trace.Stages {
		calls += s.PlannerCalls + s.DecisionCalls
		usage.InputTokens += s.Usage.InputTokens
		usage.OutputTokens += s.Usage.OutputTokens
		usage.USD += s.Usage.USD
		usage.CNY += s.Usage.CNY
	}
	return searchUsage{Calls: calls, Tokens: tokenCount{Input: usage.InputTokens, Output: usage.OutputTokens}, Cost: costOf(usage)}
}

// pendingStage is what a Search would fold before it reads.
func (i *Inspector) pendingStage(ctx context.Context, s *readStore) (pendingStage, error) {
	start := time.Now()
	c, _, err := s.agent.Consolidated(ctx, records.Scope{}, pendingIDsMax)
	if err != nil {
		return pendingStage{}, err
	}
	ids := c.PendingIDs
	if ids == nil {
		ids = []string{}
	}
	return pendingStage{stageHead: stageHead{Name: "pending", MS: millis(time.Since(start))}, Count: c.Pending, IDs: ids, Watermark: c.Watermark}, nil
}

// fullStages is a full read's trace: pending, then the read's stages, the
// searches' words scores computed here (the read records ids only), and
// the judgements' decisions and the View's blocks as result shows them.
func (i *Inspector) fullStages(ctx context.Context, s *readStore, pending pendingStage, trace *memoryagent.ReadTrace, view memoryagent.View, result []SearchItem) ([]any, error) {
	scores, err := searchScores(ctx, s.store, trace)
	if err != nil {
		return nil, err
	}
	shown, items := map[string]bool{}, map[string]bool{}
	for _, block := range composeBlocks(result, view.Index) {
		shown[block.ID] = true
		for _, item := range block.Items {
			items[item] = true
		}
	}
	stages := []any{pending}
	for _, traced := range trace.Stages {
		head := headOf(traced)
		switch traced.Name {
		case "cues":
			stages = append(stages, cuesOf(head, trace))
		case "candidates":
			stages = append(stages, candidatesOf(head, trace, scores))
		case "judge":
			stages = append(stages, judgeOf(head, trace, shown, items))
		case "need":
			stages = append(stages, needOf(head, trace, scores))
		case "compose":
			stages = append(stages, composeStage{stageHead: head, Blocks: composeBlocks(result, view.Index), Truncated: view.Truncated, Order: i.recordOrder()})
		}
	}
	return stages, nil
}

// searchScores computes the words score of every id each traced search read
// (its first page, and the pages the rounds read), by search: first page
// then the rounds' pages in order.
func searchScores(ctx context.Context, store *records.Store, trace *memoryagent.ReadTrace) (map[[2]int][]float64, error) {
	queries, ids, keys := []string{}, [][]string{}, [][2]int{}
	for n, search := range trace.Searches {
		queries, ids, keys = append(queries, search.Text), append(ids, search.First), append(keys, [2]int{n, -1})
	}
	for r, round := range trace.Rounds {
		for _, page := range round.Pages {
			queries, ids, keys = append(queries, trace.Searches[page.Search].Text), append(ids, page.IDs), append(keys, [2]int{page.Search, r})
		}
	}
	scores, err := store.LexicalScores(ctx, trace.Revision, records.Scope{}, queries, ids)
	if err != nil {
		return nil, err
	}
	out := map[[2]int][]float64{}
	for k, key := range keys {
		out[key] = scores[k]
	}
	return out, nil
}

func cuesOf(head stageHead, trace *memoryagent.ReadTrace) cuesStage {
	stage := cuesStage{stageHead: head, Cues: []traceCue{}, Needs: []traceNeed{}, Needless: trace.Needless}
	for _, c := range trace.Cues {
		stage.Cues = append(stage.Cues, traceCue{Text: excerpt(c.Text), Kind: c.Kind})
	}
	for _, need := range trace.Needs {
		stage.Needs = append(stage.Needs, traceNeed{Text: excerpt(need.Text), All: need.All})
	}
	return stage
}

// tracedSearch is search n of the trace with its first page's scores.
func tracedSearch(trace *memoryagent.ReadTrace, n int, scores map[[2]int][]float64) traceSearch {
	search := trace.Searches[n]
	return traceSearch{Search: n, Text: excerpt(search.Text), Kind: search.Kind, Source: "record", Ranked: search.Ranked, IDs: nonNil(search.First), Scores: nonNilScores(scores[[2]int{n, -1}])}
}

func candidatesOf(head stageHead, trace *memoryagent.ReadTrace, scores map[[2]int][]float64) candidatesStage {
	stage := candidatesStage{stageHead: head, Searches: []traceSearch{}}
	for n := range trace.Cues {
		if n < len(trace.Searches) {
			stage.Searches = append(stage.Searches, tracedSearch(trace, n, scores))
		}
	}
	pool, layer := nonNil(trace.Pool), []layerCandidate{}
	for _, item := range trace.Layer {
		candidate := layerCandidate{ID: item.ID, Kind: item.Kind, Source: "layer", Lit: item.Lit}
		if item.Near >= 0 {
			near := item.Near
			candidate.Near = &near
		}
		layer = append(layer, candidate)
	}
	stage.Pool, stage.Layer = &pool, &layer
	return stage
}

// judgeOf is JEV's answers per candidate, with their decisions: shown are
// the ids result shows, items the memory items its blocks show.
func judgeOf(head stageHead, trace *memoryagent.ReadTrace, shown, items map[string]bool) judgeStage {
	stage := judgeStage{stageHead: head, Records: []judgedRecord{}, Layer: []judgedLayer{}, Linked: []judgedLinked{}}
	for _, item := range trace.Items {
		record := judgedRecord{ID: item.ID, Source: "record", Excerpt: excerpt(item.Text), Row: item.Row, Needed: item.Needed, Superseded: item.Superseded, Judged: item.Judged, Shown: shown[item.ID]}
		if item.HasScreen {
			screen := item.Screen
			record.Screen = &screen
		}
		switch {
		case item.Superseded >= 0.5:
			record.Decision = "superseded"
		case item.Needed >= 0.5:
			record.Decision = "needed"
		case record.Shown:
			record.Decision = "kept"
		default:
			record.Decision = "dropped"
		}
		stage.Records = append(stage.Records, record)
	}
	keptOr := func(kept bool) string {
		if kept {
			return "kept"
		}
		return "dropped"
	}
	for _, item := range trace.Layer {
		stage.Layer = append(stage.Layer, judgedLayer{ID: item.ID, Kind: item.Kind, Source: "layer", Excerpt: excerpt(item.Text), Needed: item.Needed, Decision: keptOr(items[item.ID])})
	}
	for _, item := range trace.Linked {
		stage.Linked = append(stage.Linked, judgedLinked{ID: item.ID, Source: "layer", Excerpt: excerpt(item.Text), Needed: item.Needed, Decision: keptOr(shown[item.ID]), Shown: shown[item.ID]})
	}
	return stage
}

func needOf(head stageHead, trace *memoryagent.ReadTrace, scores map[[2]int][]float64) needStage {
	stage := needStage{stageHead: head, Rounds: []traceRound{}, States: nonNil(trace.NeedStates), Stop: trace.Stop}
	for r, round := range trace.Rounds {
		traced := traceRound{Round: round.Round, Steps: []traceStep{}, Pages: []tracePage{}, Searches: []traceSearch{}, Fresh: nonNil(round.Fresh)}
		for k, step := range round.Steps {
			traced.Steps = append(traced.Steps, traceStep{Need: k, Step: step})
		}
		for _, page := range round.Pages {
			traced.Pages = append(traced.Pages, tracePage{Search: page.Search, IDs: nonNil(page.IDs), Scores: nonNilScores(scores[[2]int{page.Search, r}])})
		}
		for _, n := range round.Searches {
			traced.Searches = append(traced.Searches, tracedSearch(trace, n, scores))
		}
		stage.Rounds = append(stage.Rounds, traced)
	}
	return stage
}

// composeBlocks is result item by item: a layer block (index names the
// View's blocks) with the items it shows and the records it links to that
// result shows, or a record.
func composeBlocks(result []SearchItem, index []memoryagent.IndexItem) []traceBlock {
	blocks, byID, records := []traceBlock{}, map[string]memoryagent.IndexItem{}, map[string]bool{}
	for _, block := range index {
		byID[blockID(block)] = block
	}
	for _, item := range result {
		records[item.ID] = true
	}
	layers := map[string]string{"1-instructions": "instructions", "2-topics": "topics", "3-timelines": "timelines", "4-values": "facts", "5-insights": "insights"}
	for p, item := range result {
		block, ok := byID[item.ID]
		if !ok {
			blocks = append(blocks, traceBlock{Position: p, ID: item.ID, Layer: "records", Items: []string{item.ID}, Records: []string{item.ID}, Excerpt: excerpt(item.Content)})
			continue
		}
		linked := []string{}
		for _, ref := range block.Records {
			if records[ref.ID] && !slices.Contains(linked, ref.ID) {
				linked = append(linked, ref.ID)
			}
		}
		layer := layers[block.Kind]
		if layer == "" {
			layer = block.Kind
		}
		blocks = append(blocks, traceBlock{Position: p, ID: item.ID, Layer: layer, Items: nonNil(block.Items), Records: linked, Excerpt: excerpt(item.Content)})
	}
	return blocks
}

func nonNil(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}

func nonNilScores(scores []float64) []float64 {
	if scores == nil {
		return []float64{}
	}
	return scores
}
