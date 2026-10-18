package records

import (
	"context"
	"strings"
	"sync"
)

// A selection borrows immutable snapshot records. It retains membership and
// aggregate df, not a second full document/term index beyond the 150k budget.
type querySelection struct {
	docs          []record
	wanted        map[int64]int
	total         int
	ready         bool
	first, last   int64
	mu            sync.Mutex
	df            map[string]int
	loading       chan struct{}
	vectors       map[string][][]float32
	vectorOrder   []string
	vectorLoading map[string]chan struct{}
}

func newSelection(docs []record) *querySelection {
	v := &querySelection{docs: docs, wanted: make(map[int64]int, len(docs)), ready: true}
	for i, r := range docs {
		v.wanted[r.VersionID] = i
		v.total += r.TokenCount
		v.ready = v.ready && r.VersionID != 0 && r.TokenCount >= 0
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
		df, seen := map[string]int{}, map[string]int64{}
		err := s.selectionTokens(ctx, v, "", func(version int64, text string) {
			for token := range strings.SplitSeq(text, " ") {
				if token == "" || seen[token] == version {
					continue
				}
				if _, ok := seen[token]; !ok {
					token = strings.Clone(token)
				}
				seen[token] = version
				df[token]++
			}
		})
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
	if match != "" && len(v.docs) > 2048 {
		rows, err := s.db.QueryContext(ctx, `SELECT l.version_id,l.tokens FROM lexical_fts
JOIN lexical_documents l ON l.version_id=lexical_fts.rowid
WHERE lexical_fts MATCH ? AND lexical_fts.rowid>=? AND lexical_fts.rowid<=?`, match, v.first, v.last)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var version int64
			var text string
			if err := rows.Scan(&version, &text); err != nil {
				return err
			}
			if _, ok := v.wanted[version]; ok {
				visit(version, text)
			}
		}
		return rows.Err()
	}
	for start := 0; start < len(v.docs); start += 512 {
		part := v.docs[start:min(start+512, len(v.docs))]
		args := make([]any, len(part))
		for i, r := range part {
			args[i] = r.VersionID
		}
		rows, err := s.db.QueryContext(ctx, "SELECT version_id,tokens FROM lexical_documents WHERE version_id IN ("+strings.TrimSuffix(strings.Repeat("?,", len(part)), ",")+")", args...)
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
