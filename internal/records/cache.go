package records

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
)

// Immutable indexes/vectors are built outside mu; mu protects publication only.
// The BM25 budget is a hard 150k documents, so an oversized single index is not
// retained. Historical reads remain valid but never substitute a newer revision.
type queryCache struct {
	mu              sync.Mutex
	derived         map[string]*derivedIndex
	derivedOrder    []string
	derivedDocs     int
	derivedLoading  chan struct{}
	current         string
	indexes         map[string]*lexicalIndex
	order           []string
	documents       int
	vectors         map[string][]float32
	queries         map[string][]float32
	queryOrder      []string
	snapshotHash    string
	snapshot        []record
	snapshotLoading chan struct{}
	lexicalWake     chan struct{}
	lexicalBuild    uint64
	selections      map[string]*querySelection
	selectionOrder  []string
	selectionDocs   int
	allSelection    *querySelection
	prepared        *preparedLexical
}

func newQueryCache() *queryCache {
	return &queryCache{indexes: map[string]*lexicalIndex{}, selections: map[string]*querySelection{}, vectors: map[string][]float32{}, queries: map[string][]float32{}, lexicalWake: make(chan struct{}, 1)}
}

func (s *Store) wakeLexical() {
	if s.cache != nil {
		select {
		case s.cache.lexicalWake <- struct{}{}:
		default: // One queued build also covers every intervening committed write.
		}
	}
}

func searchKey(hash string, req QueryRequest) string {
	var ids []string
	var values []json.RawMessage
	if json.Unmarshal(req.Input["ids"], &values) == nil && string(req.Input["ids"]) != "null" {
		ids = []string{}
		seen := map[string]bool{}
		for _, v := range values {
			var id string
			if json.Unmarshal(v, &id) == nil && !seen[id] {
				ids = append(ids, id)
				seen[id] = true
			}
		}
		sort.Strings(ids)
	}
	in := req.Input
	present := []bool{}
	for _, field := range []string{"id", "kind", "date", "status", "since", "until"} {
		present = append(present, in[field] != nil)
	}
	// Same filter tuple as Source.searchCache, with the visible snapshot's scope
	// explicit because the engine receives the full, rather than scoped, journal.
	// JSON null and an absent filter are different candidate sets, even though
	// JSON.stringify maps undefined tuple elements to null in TS's cache key.
	raw, _ := json.Marshal([]any{hash, trueValue(in["archived"]), in["id"], ids, in["kind"], in["date"], in["status"], in["since"], in["until"], req.Scope, present})
	if req.Index.Enabled() {
		raw, _ = json.Marshal([]any{string(raw), req.Index})
		if req.derived != "" {
			raw, _ = json.Marshal([]any{string(raw), req.derived})
		}
	}
	return string(raw)
}

func (s *Store) lexical(ctx context.Context, hash string, req QueryRequest, docs []record) (*lexicalIndex, error) {
	if s.cache == nil {
		return newLexical(docs), nil
	}
	_, current, err := revision(ctx, s.readDB(), "")
	if err != nil {
		return nil, err
	}
	key := searchKey(hash, req)
	c := s.cache
	c.mu.Lock()
	if c.current != current {
		c.current = current
		c.indexes = map[string]*lexicalIndex{}
		c.order = nil
		c.documents = 0
		c.snapshotHash = ""
		c.snapshot = nil
		c.clearSelections()
	}
	index := c.indexes[key]
	c.mu.Unlock()
	if index != nil {
		return index, nil
	}
	index = newLexical(docs)
	if len(docs) > 150000 {
		return index, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current != current {
		return index, nil
	}
	if len(docs) > c.lexicalCapacity() {
		return index, nil
	}
	if existing := c.indexes[key]; existing != nil {
		return existing, nil
	}
	for c.documents+len(docs) > c.lexicalCapacity() && len(c.order) > 0 {
		oldest := c.order[0]
		c.order = c.order[1:]
		c.documents -= len(c.indexes[oldest].docs)
		delete(c.indexes, oldest)
	}
	c.indexes[key] = index
	c.order = append(c.order, key)
	c.documents += len(docs)
	return index, nil
}

// mu is held: cached full document terms and sparse query indexes share the
// original hard budget, rather than giving a large snapshot a second budget.
func (c *queryCache) lexicalCapacity() int {
	if c.prepared != nil {
		return 150000 - len(c.prepared.index.docs)
	}
	return 150000
}

func vectorKey(hash string, cfg EmbeddingConfig, prefix string) string {
	return hash + "\x00" + cfg.Model + "\x00" + prefix
}
func (s *Store) cachedVector(hash string, cfg EmbeddingConfig, prefix string) []float32 {
	if s.cache == nil {
		return nil
	}
	c := s.cache
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.vectors[vectorKey(hash, cfg, prefix)]
}
func (s *Store) keepVector(hash string, cfg EmbeddingConfig, prefix string, value []float32) {
	if s.cache == nil {
		return
	}
	c := s.cache
	c.mu.Lock()
	defer c.mu.Unlock()
	c.vectors[vectorKey(hash, cfg, prefix)] = value
}
func (s *Store) queryVector(ctx context.Context, query string, cfg EmbeddingConfig) ([]float32, error) {
	key := vectorKey(textHash(query), cfg, cfg.QueryPrefix)
	if s.cache != nil {
		c := s.cache
		c.mu.Lock()
		value := c.queries[key]
		c.mu.Unlock()
		if value != nil {
			return value, nil
		}
	}
	values, err := embed(ctx, []string{query}, cfg, cfg.QueryPrefix)
	if err != nil {
		return nil, err
	}
	if s.cache != nil {
		c := s.cache
		c.mu.Lock()
		if c.queries[key] == nil {
			c.queries[key] = values[0]
			c.queryOrder = append(c.queryOrder, key)
		}
		if len(c.queryOrder) > 256 {
			delete(c.queries, c.queryOrder[0])
			c.queryOrder = c.queryOrder[1:]
		}
		c.mu.Unlock()
	}
	return values[0], nil
}
