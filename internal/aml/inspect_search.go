package aml

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

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
	if q.full && !i.fullOn() {
		inspectError(w, http.StatusConflict, "full_disabled")
		return
	}
	if q.full {
		// aml-serve may have replaced or reverted the account's keys.
		i.reloadKeys()
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
		i.searchFailed(w, r, q, answer, err)
		return
	case !q.tracing:
		answer.body["trace"] = nil
	}
	inspectJSON(w, answer.status, answer.body)
}

// searchFailed answers a search that failed with err, answer what it did.
// Once a full search's read has run, what it cost is answered with the
// error, and its trace when the search asked for one: the store changing
// under it, or SQLite busy, answers 503 store_busy, and any other failure
// but the client leaving 500 internal (the final review's parked T18-e).
func (i *Inspector) searchFailed(w http.ResponseWriter, r *http.Request, q inspectSearch, answer searchOutcome, err error) {
	trace := answer.trace
	if !q.tracing {
		trace = nil
	}
	switch {
	case answer.read && storeBusy(err):
		w.Header().Set("Retry-After", storeBusyRetry)
		inspectJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "store_busy", "trace": trace, "usage": answer.usage})
	case answer.read && r.Context().Err() == nil && !errors.Is(err, fs.ErrNotExist):
		i.log.Error(fmt.Sprintf("search failed (store %s): %v", short(q.key), withoutPath(err)))
		inspectJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal", "trace": trace, "usage": answer.usage})
	default:
		i.storeFailure(w, r, q.key, "search", err)
	}
}

// searchOutcome is a search's answer: its status and body. Once a full
// search's read has run (read), it also keeps what the read cost (usage) and
// the trace (nil before its stages are built), for an answer that cannot
// give the result.
type searchOutcome struct {
	status int
	body   map[string]any
	read   bool
	usage  searchUsage
	trace  []any
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
	// From here on every outcome, failed or not, keeps what the read cost.
	paid := searchOutcome{read: true, usage: usage}
	var result []SearchItem
	if readErr == nil {
		result = searchItems(view, q.topK)
		scoreByPosition(result)
	}
	stages, err := i.fullStages(ctx, s, pending, trace, view, result)
	if err != nil {
		return paid, err
	}
	paid.trace = stages
	switch {
	case readErr == nil:
		degraded := view.Stats.Degraded
		if degraded == nil {
			degraded = []string{}
		}
		paid.status, paid.body = http.StatusOK, map[string]any{"store": short(s.key), "mode": "full", "result": result, "degraded": degraded, "trace": stages, "usage": usage}
		return paid, nil
	case ctx.Err() != nil:
		return paid, ctx.Err()
	case errors.Is(readErr, memoryagent.ErrBudgetCap):
		paid.status, paid.body = http.StatusPaymentRequired, map[string]any{"error": "debug_budget_exhausted", "trace": stages, "usage": usage}
		return paid, nil
	}
	i.log.Warn(fmt.Sprintf("full search failed (store %s): %v", short(s.key), withoutPath(readErr)))
	ranking, err := rawRanking(ctx, s.store, s.agent, i.config.MemoryConfig, q.query, q.topK, i.rawWarn(s.key))
	if err != nil {
		return paid, err
	}
	scoreByPosition(ranking.items)
	paid.status, paid.body = http.StatusBadGateway, map[string]any{"error": "provider_failed", "result": ranking.items, "trace": stages, "usage": usage}
	return paid, nil
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
