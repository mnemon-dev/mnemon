package records

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"sync"
)

// A selection borrows immutable snapshot records. It retains membership and
// aggregate df, not a second full document/term index beyond the 150k budget.
type querySelection struct {
	docs          []record
	wanted        map[int64]int
	byID          map[string]int
	total         int
	ready         bool
	prefixLimit   int
	first, last   int64
	mu            sync.Mutex
	df            map[string]int
	loading       chan struct{}
	vectors       map[string][][]float32
	vectorOrder   []string
	vectorLoading map[string]chan struct{}
	recentOnce    sync.Once
	recent        []int
}

func searchLower(r record) string {
	if r.SearchLower != "" || r.SearchText == "" {
		return r.SearchLower
	}
	return lower(r.SearchText)
}

func (v *querySelection) queryIDs(ctx context.Context, term string, recent bool) ([]string, error) {
	if recent {
		v.prepareRecent()
	}
	capacity := len(v.docs)
	if term != "" {
		capacity = min(capacity, 64)
	}
	ids := make([]string, 0, capacity)
	for i := range v.docs {
		if i%2048 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		pos := i
		if recent {
			pos = v.recent[i]
		}
		r := &v.docs[pos]
		if term == "" || strings.Contains(searchLower(*r), term) {
			ids = append(ids, r.ID)
		}
	}
	return ids, nil
}

func (v *querySelection) prepareRecent() {
	// Pure, bounded CPU work on this immutable view. Never sort borrowed
	// records: lexical/vector positions and historical readers share them.
	v.recentOnce.Do(func() {
		v.recent = make([]int, len(v.docs))
		for i := range v.recent {
			v.recent[i] = i
		}
		slices.SortFunc(v.recent, func(i, j int) int {
			a, b := &v.docs[i], &v.docs[j]
			if a.CreatedAt == b.CreatedAt {
				return cmp.Compare(a.ID, b.ID)
			}
			return cmp.Compare(b.CreatedAt, a.CreatedAt)
		})
	})
}

func (s *Store) prepareSnapshot(ctx context.Context) error {
	rev, hash, err := revision(ctx, s.readDB(), "")
	if err != nil {
		return err
	}
	all, err := s.queryRecords(ctx, rev, hash)
	if err != nil || len(all) > 200000 {
		return err
	}
	c := s.cache
	c.mu.Lock()
	if c.snapshotHash != hash {
		c.mu.Unlock()
		return nil
	}
	v := c.allSelection
	c.mu.Unlock()
	if v == nil {
		v = s.keepSelection(hash+"\nall", hash, newSelection(all), true)
	}
	if !v.ready {
		return nil
	}
	v.prepareRecent()
	_, err = s.selectionDF(ctx, v)
	return err
}

func newSelection(docs []record) *querySelection {
	v := &querySelection{docs: docs, wanted: make(map[int64]int, len(docs)), byID: make(map[string]int, len(docs)), ready: uint64(len(docs)) <= uint64(^uint32(0))}
	for i, r := range docs {
		v.wanted[r.VersionID] = i
		v.byID[r.ID] = i
		v.total += r.TokenCount
		v.ready = v.ready && r.VersionID != 0 && r.TokenCount >= 0 && uint64(r.TokenCount) <= uint64(^uint32(0))
		if i == 0 || r.VersionID < v.first {
			v.first = r.VersionID
		}
		if r.VersionID > v.last {
			v.last = r.VersionID
		}
	}
	return v
}

// Called with cache.mu held. Old requests may finish using their immutable
// selection, but cannot publish it into the new snapshot's selection map.
func (c *queryCache) clearSelections() {
	c.selections = map[string]*querySelection{}
	c.selectionOrder, c.selectionDocs, c.allSelection = nil, 0, nil
	c.prepared = nil
}

func (s *Store) selectRecords(all []record, hash string, req QueryRequest) *querySelection {
	key := searchKey(hash, req)
	c := s.cache
	if c != nil {
		c.mu.Lock()
		v := c.selections[key]
		c.mu.Unlock()
		if v != nil {
			return v
		}
	}
	docs := candidates(all, req, "", true)
	full := len(docs) == len(all)
	if c != nil && full {
		c.mu.Lock()
		v := c.allSelection
		if c.snapshotHash != hash {
			v = nil
		}
		c.mu.Unlock()
		if v != nil {
			return s.keepSelection(key, hash, v, true)
		}
	}
	return s.keepSelection(key, hash, newSelection(docs), full)
}

func (s *Store) keepSelection(key, hash string, v *querySelection, full bool) *querySelection {
	c := s.cache
	if c == nil || len(v.docs) > 200000 {
		return v
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snapshotHash != hash {
		return v
	}
	if old := c.selections[key]; old != nil {
		return old
	}
	if full {
		if c.allSelection == nil {
			c.allSelection = v
		}
		v = c.allSelection
	}
	// One shared full-snapshot view, at most 200k additional filtered references
	// and 16 filter keys. Identical visible sets share the full view even when
	// requests carry different scopes; only a proven full match takes this path.
	for len(c.selectionOrder) >= 16 || !full && c.selectionDocs+len(v.docs) > 200000 {
		oldest := c.selectionOrder[0]
		c.selectionOrder = c.selectionOrder[1:]
		if old := c.selections[oldest]; old != c.allSelection {
			c.selectionDocs -= len(old.docs)
		}
		delete(c.selections, oldest)
	}
	c.selections[key] = v
	c.selectionOrder = append(c.selectionOrder, key)
	if !full {
		c.selectionDocs += len(v.docs)
	}
	return v
}

// Only one caller prepares aggregate statistics for this immutable selection.
// I/O runs without a mutex. Cancellation never poisons later retries, and
// waiters retain their own cancellation rather than inheriting the builder's.
func (s *Store) selectionDF(ctx context.Context, v *querySelection) (map[string]int, error) {
	for {
		v.mu.Lock()
		if v.df != nil {
			df := v.df
			v.mu.Unlock()
			return df, nil
		}
		if pending := v.loading; pending != nil {
			v.mu.Unlock()
			select {
			case <-pending:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		v.loading = make(chan struct{})
		v.mu.Unlock()
		df, prepared, err := s.buildStatistics(ctx, v)
		if err == nil && len(df) <= 150000 && prepared != nil {
			s.keepPrepared(v, prepared)
		}
		v.mu.Lock()
		// A bounded aggregate vocabulary; oversized selections still execute
		// exactly, but do not retain arbitrarily large statistics in Serve.
		if err == nil && len(df) <= 150000 {
			v.df = df
		}
		close(v.loading)
		v.loading = nil
		v.mu.Unlock()
		return df, err
	}
}

// Read already-tokenized immutable versions. Small candidate sets use primary
// keys, independent of historical-version count. Broad searches use MATCH to
// retrieve only matching documents, then enforce exact candidate membership.
// There is no instance expansion or GROUP BY over all historical occurrences.
func (s *Store) selectionTokens(ctx context.Context, v *querySelection, match string, visit func(int64, string)) error {
	versions := []int64{}
	if match != "" && len(v.docs) > 2048 {
		rows, err := s.readDB().QueryContext(ctx, `SELECT rowid FROM lexical_fts
WHERE lexical_fts MATCH ? AND rowid>=? AND rowid<=?`, match, v.first, v.last)
		if err != nil {
			return err
		}
		for rows.Next() {
			var version int64
			if err = rows.Scan(&version); err != nil {
				rows.Close()
				return err
			}
			if _, ok := v.wanted[version]; ok {
				versions = append(versions, version)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	} else {
		versions = make([]int64, len(v.docs))
		for i, r := range v.docs {
			versions[i] = r.VersionID
		}
	}
	return s.readTokenVersions(ctx, versions, visit)
}

func (s *Store) readTokenVersions(ctx context.Context, versions []int64, visit func(int64, string)) error {
	for start := 0; start < len(versions); start += 512 {
		part := versions[start:min(start+512, len(versions))]
		args := make([]any, len(part))
		for i, version := range part {
			args[i] = version
		}
		rows, err := s.readDB().QueryContext(ctx, "SELECT version_id,tokens FROM lexical_documents WHERE version_id IN ("+strings.TrimSuffix(strings.Repeat("?,", len(part)), ",")+")", args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var version int64
			var text string
			if err = rows.Scan(&version, &text); err != nil {
				rows.Close()
				return err
			}
			visit(version, text)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
