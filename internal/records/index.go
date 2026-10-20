package records

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/mnemon-dev/mnemon/internal/retrieval"
)

// IndexPolicy selects additional, rebuildable candidate indexes. An absent or
// empty policy retains records-v1, including its original response shape.
type IndexPolicy struct {
	Version    int  `json:"version,omitempty"`
	Lexical    bool `json:"lexical,omitempty"`
	Entities   bool `json:"entities,omitempty"`
	Relations  bool `json:"relations,omitempty"`
	Temporal   bool `json:"temporal,omitempty"`
	Supersedes bool `json:"supersedes,omitempty"`
	// Recency adds native Smart Recall's recency anchor signal: the
	// selection's newest records by index time (recall.go).
	Recency bool `json:"recency,omitempty"`
	// Traversal adds native Smart Recall's beam search over the links from
	// the fused seeds (recall.go).
	Traversal bool `json:"traversal,omitempty"`
}

func (p *IndexPolicy) UnmarshalJSON(raw []byte) error {
	type value IndexPolicy
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode((*value)(p))
}

func (p *IndexPolicy) Enabled() bool {
	return p != nil && (p.Lexical || p.Entities || p.Relations || p.Temporal || p.Supersedes || p.Recency || p.Traversal)
}

func (p *IndexPolicy) Validate() error {
	if p != nil && (p.Version != 0 && p.Version != 1 || p.Enabled() && p.Version != 1) {
		return errors.New("index policy requires version 1")
	}
	return nil
}

func (p *IndexPolicy) Identity() string {
	raw, _ := json.Marshal(p)
	h := sha256.Sum256(raw)
	return "records-index/v1/" + hex.EncodeToString(h[:])
}

type IndexResult struct {
	Policy    string         `json:"policy"`
	Revision  string         `json:"revision"`
	Routes    map[string]int `json:"routes"`
	Indexed   int            `json:"indexed"`
	Truncated bool           `json:"truncated"`
}

// Link is an explicit claim by the record writer. Version binds the target's
// record version; model suggestions do not get installed here automatically.
type Link struct {
	ID            string  `json:"id"`
	Version       int64   `json:"version"`
	Type          string  `json:"type"`
	Weight        float64 `json:"weight"`
	authoritative bool
}

type indexMetadata struct {
	Version    int64    `json:"version"`
	Entities   []string `json:"entities"`
	Aliases    []string `json:"aliases"`
	Links      []Link   `json:"links"`
	EventAt    string   `json:"eventAt"`
	Supersedes *struct {
		ID      string `json:"id"`
		Version int64  `json:"version"`
	} `json:"supersedes"`
	// The derived metadata's names (derived.go), indexed after the writer's
	// and the extracted ones.
	derivedEntities, derivedAliases []string
}

type indexDocument struct {
	meta indexMetadata
	at   time.Time
}

type derivedIndex struct {
	generation   string // of the derived metadata it was built from
	docs         []record
	meta         map[string]indexDocument
	entities     map[string][]string
	known        map[string]bool
	words        map[string][]string
	links        map[string][]Link
	replacements map[string][]Link
	// recent is the recency route of an index with Recency, and position
	// each record's place in docs for one with Traversal (recall.go).
	recent   []string
	position map[string]int
}

func (s *Store) queryIndexed(ctx context.Context, req QueryRequest) (QueryResult, error) {
	if req.CandidateLimit < 0 || req.CandidateLimit > 2000 {
		return QueryResult{}, errors.New("candidateLimit must be between 1 and 2000")
	}
	limit := req.CandidateLimit
	if limit == 0 {
		limit = 200
	}
	var err error
	if req.derived, err = derivedGeneration(ctx, s.readDB()); err != nil {
		return QueryResult{}, err
	}
	hash, index, selection, err := s.indexedSelection(ctx, req)
	if err != nil {
		return QueryResult{}, err
	}
	// Later caches are keyed by the overlay the index was built from.
	req.derived = index.generation
	result, vector, err := s.rankSelectionVector(ctx, hash, req, selection)
	if err != nil {
		return QueryResult{}, err
	}
	info := &IndexResult{Policy: req.Index.Identity(), Revision: hash, Indexed: len(index.docs), Routes: map[string]int{"base": len(result.IDs)}}
	result.Index = info
	// Literal enumeration and explicit ID reads retain completeness and order.
	query := stringValue(req.Input["query"])
	if query == "" || req.Input["id"] != nil || req.Input["ids"] != nil {
		return result, nil
	}
	lists := [][]string{result.IDs[:min(limit, len(result.IDs))]}
	appendRoute := func(name string, ids []string) {
		info.Routes[name] = len(ids)
		if len(ids) > limit {
			info.Truncated = true
		}
		if len(ids) != 0 {
			lists = append(lists, ids[:min(limit, len(ids))])
		}
	}
	if req.Index.Lexical {
		appendRoute("lexical", index.wordRank(query))
	}
	if req.Index.Entities {
		appendRoute("entities", index.entityRank(query, req.Input["entities"]))
	}
	if req.Index.Recency {
		appendRoute("recency", index.recent)
	}
	seeds := fuse(lists...)
	var anchors []scored
	if req.Index.Traversal {
		anchors = recallSeeds(seeds, lists)
	}
	intent := retrieval.DetectIntent(query)
	if raw := stringValue(req.Input["intent"]); raw != "" {
		intent, err = retrieval.IntentFromString(raw)
		if err != nil {
			return QueryResult{}, err
		}
	}
	if req.Index.Relations {
		appendRoute("relations", index.related(seeds, intent, false, limit))
	}
	if req.Index.Traversal {
		// Semantic scores need the query's vector, which only a hybrid base
		// route has, and read stored vectors only: traversal never embeds.
		var similarity func([]string) (map[string]float64, error)
		if vector != nil {
			similarity = s.storedSimilarity(ctx, index, *req.Embedding, vector)
		}
		route, err := index.traverse(ctx, anchors, intent, similarity, limit)
		if err != nil {
			return QueryResult{}, err
		}
		appendRoute("traversal", route)
	}
	if req.Index.Supersedes && !trueValue(req.Input["history"]) && req.Input["until"] == nil {
		appendRoute("supersedes", index.related(seeds, intent, true, limit))
	}
	if req.Index.Temporal && req.Input["timeOrder"] != nil && stringValue(req.Input["timeOrder"]) != "oldest" && stringValue(req.Input["timeOrder"]) != "newest" {
		return QueryResult{}, errors.New("timeOrder must be oldest or newest")
	}
	if req.Index.Temporal && (req.Input["timeOrder"] != nil || req.Input["since"] != nil || req.Input["until"] != nil) {
		appendRoute("temporal", index.byTime(seeds, req.Input))
	}
	result.IDs = fuse(lists...)
	if req.Index.Temporal && req.Input["timeOrder"] != nil {
		result.IDs = index.byTime(result.IDs, req.Input)
	} else if req.Index.Supersedes && !trueValue(req.Input["history"]) && req.Input["until"] == nil {
		index.demoteSuperseded(result.IDs)
	}
	info.Truncated = info.Truncated || len(result.IDs) > limit || info.Routes["base"] > limit
	result.IDs = result.IDs[:min(limit, len(result.IDs))]
	return result, nil
}

func (index *derivedIndex) wordRank(query string) []string {
	scores := map[string]int{}
	for term := range retrieval.Tokenize(query) {
		for _, id := range index.words[term] {
			scores[id]++
		}
	}
	return orderedScores(scores)
}

func (index *derivedIndex) entityRank(query string, raw json.RawMessage) []string {
	entities := retrieval.ExtractEntitiesIndexed(query, index.known)
	var explicit []string
	_ = json.Unmarshal(raw, &explicit)
	entities = retrieval.MergeEntities(entities, explicit)
	scores := map[string]int{}
	for _, entity := range entities {
		// Like native indexed extraction, ignore a hot inferred entity. An
		// explicit entity filter is still useful for exact enumeration.
		ids := index.entities[entity]
		if len(ids) > max(20, len(index.docs)/5) && !slices.Contains(explicit, entity) {
			continue
		}
		for _, id := range ids {
			scores[id]++
		}
	}
	return orderedScores(scores)
}

func orderedScores(scores map[string]int) []string {
	ids := make([]string, 0, len(scores))
	for id := range scores {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b string) int {
		if scores[a] != scores[b] {
			return scores[b] - scores[a]
		}
		return compareID(a, b)
	})
	return ids
}

func compareID(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func copyInput(input map[string]json.RawMessage) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func parseTime(value string) (time.Time, error) {
	if len(value) == 10 {
		return time.Parse("2006-01-02", value)
	}
	return time.Parse(time.RFC3339Nano, value)
}

func indexTime(r record, meta indexMetadata) time.Time {
	values := []string{meta.EventAt}
	if r.Date != nil {
		values = append(values, *r.Date)
	}
	values = append(values, r.CreatedAt)
	for _, value := range values {
		if at, err := parseTime(value); err == nil {
			return at
		}
	}
	return time.Time{}
}

func timeBounds(input map[string]json.RawMessage) (time.Time, time.Time, error) {
	var since, until time.Time
	for _, field := range []string{"since", "until"} {
		if input[field] == nil {
			continue
		}
		value := stringValue(input[field])
		at, err := parseTime(value)
		if err != nil {
			return since, until, fmt.Errorf("%s must be an ISO date or RFC3339 timestamp", field)
		}
		if field == "since" {
			since = at
		} else {
			until = at
			if len(value) == 10 {
				until = until.AddDate(0, 0, 1).Add(-time.Nanosecond)
			}
		}
	}
	if !since.IsZero() && !until.IsZero() && since.After(until) {
		return since, until, errors.New("since exceeds until")
	}
	return since, until, nil
}

func (s *Store) indexedSelection(ctx context.Context, req QueryRequest) (string, *derivedIndex, *querySelection, error) {
	rev, hash, err := revision(ctx, s.readDB(), req.Revision)
	if err != nil {
		return "", nil, nil, err
	}
	all, err := s.queryRecords(ctx, rev, hash)
	if err != nil {
		return "", nil, nil, err
	}
	index, err := s.derived(ctx, rev, hash, all, req)
	if err != nil {
		return "", nil, nil, err
	}
	req.derived = index.generation
	key := searchKey(hash, req)
	if c := s.cache; c != nil {
		c.mu.Lock()
		selection := c.selections[key]
		c.mu.Unlock()
		if selection != nil {
			return hash, index, selection, nil
		}
	}
	selection := s.keepSelection(key, hash, newSelection(index.docs), false)
	return hash, index, selection, nil
}
