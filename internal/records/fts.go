package records

import (
	"context"
	"errors"
	"strings"
	"time"
)

func (s *Store) runLexicalWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.cache.lexicalWake:
			buildCtx, stop := context.WithTimeout(ctx, 5*time.Minute)
			err := s.buildLexical(buildCtx)
			if err == nil {
				err = s.prepareSnapshot(buildCtx)
			}
			stop()
			if err != nil && ctx.Err() == nil {
				s.report("records query preparation: %v", err)
			}
		}
	}
}

// This is a derived, append-only index of immutable record versions. The
// original tokenizer runs once in the background build, not on every query.
// FTS supplies postings only: all scores, candidate-local statistics and ties
// still use the frozen TS rules rather than SQLite's built-in bm25().
func (s *Store) buildLexical(ctx context.Context) error {
	if !s.fts || s.readonly {
		return nil
	}
	changed := false
	for {
		var last int64
		if err := s.readDB().QueryRowContext(ctx, "SELECT COALESCE(MAX(version_id),0) FROM lexical_documents").Scan(&last); err != nil {
			return err
		}
		rows, err := s.readDB().QueryContext(ctx, "SELECT rowid,search_text FROM record_versions WHERE rowid>? ORDER BY rowid LIMIT 512", last)
		if err != nil {
			return err
		}
		batch := []record{}
		for rows.Next() {
			var r record
			if err = rows.Scan(&r.VersionID, &r.SearchText); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, r)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		// Tokenize outside the write transaction; at most one bounded batch lives
		// here. A token beyond FTS5's limit keeps the exact in-memory fallback.
		for i := range batch {
			tokens := Tokens(batch[i].SearchText)
			batch[i].TokenCount = len(tokens)
			for _, token := range tokens {
				if len(token) > 32768 {
					batch[i].TokenCount = -2
				}
			}
			batch[i].SearchText = strings.Join(tokens, " ")
		}
		if err := s.saveLexical(ctx, batch); err != nil {
			return err
		}
		changed = true
	}
	if changed && s.cache != nil {
		c := s.cache
		c.mu.Lock()
		c.lexicalBuild++ // Do not publish a metadata read started before this build.
		c.snapshotHash, c.snapshot = "", nil
		c.indexes, c.order, c.documents = map[string]*lexicalIndex{}, nil, 0
		c.clearSelections()
		c.mu.Unlock()
	}
	return nil
}

func (s *Store) saveLexical(ctx context.Context, batch []record) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	docs, err := tx.PrepareContext(ctx, "INSERT OR IGNORE INTO lexical_documents VALUES(?,?,?)")
	if err != nil {
		return err
	}
	defer docs.Close()
	fts, err := tx.PrepareContext(ctx, "INSERT INTO lexical_fts(rowid,tokens) VALUES(?,?)")
	if err != nil {
		return err
	}
	defer fts.Close()
	for _, r := range batch {
		result, err := docs.ExecContext(ctx, r.VersionID, r.SearchText, r.TokenCount)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 { // A concurrent builder already committed this exact version.
			continue
		}
		if n != 1 {
			return errors.New("invalid lexical document insert cardinality")
		}
		if _, err = fts.ExecContext(ctx, r.VersionID, r.SearchText); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) selectionLexical(ctx context.Context, v *querySelection, query string, reference *preparedLexical) (*lexicalIndex, bool, error) {
	if !s.fts || !v.ready {
		s.wakeLexical()
		return nil, false, nil
	}
	wanted := map[string]bool{}
	terms := []string{}
	for _, token := range Tokens(query) {
		if len(token) > 32768 {
			return nil, false, nil
		}
		if !wanted[token] {
			wanted[token] = true
			terms = append(terms, `"`+token+`"`)
		}
	}
	index := &lexicalIndex{df: map[string]int{}, n: len(v.docs), total: v.total, postings: map[string][]posting{}}
	if len(terms) == 0 {
		return index, true, nil
	}
	match := strings.Join(terms, " OR ")
	if reference != nil {
		frequency := 0
		for token := range wanted {
			frequency += reference.index.df[token]
		}
		index.docs = make([]lexicalDoc, 0, min(len(v.docs), frequency))
		// Broad queries read the short remainder directly. Sparse MATCH uses
		// only row IDs; cap its historical range relative to current documents.
		if frequency > len(v.docs)/4 || reference.owner.last > 2*int64(len(reference.owner.docs)) {
			match = ""
		}
	}
	err := s.selectionTokens(ctx, v, match, func(version int64, text string) {
		tf := map[string]int{}
		for token := range strings.SplitSeq(text, " ") {
			if wanted[token] {
				tf[token]++
			}
		}
		if len(tf) == 0 {
			return
		}
		r := v.docs[v.wanted[version]]
		pos := len(index.docs)
		index.docs = append(index.docs, lexicalDoc{id: r.ID, length: r.TokenCount})
		for token, count := range tf {
			index.df[token]++
			index.postings[token] = append(index.postings[token], posting{uint32(pos), uint32(count)})
		}
	})
	return index, true, err
}

func (s *Store) queryLexical(ctx context.Context, hash string, req QueryRequest, v *querySelection, query string) (*lexicalIndex, bool, error) {
	key := searchKey(hash, req) + "\npostings:" + query
	if c := s.cache; c != nil {
		c.mu.Lock()
		index := c.indexes[key]
		prepared := c.prepared
		c.mu.Unlock()
		if index != nil {
			return index, true, nil
		}
		if prepared != nil && prepared.owner == v {
			if len(prepared.remainder.docs) == 0 {
				return prepared.index, true, nil
			}
			tail, ready, err := s.selectionLexical(ctx, prepared.remainder, query, prepared)
			if err != nil || !ready {
				return tail, ready, err
			}
			return &lexicalIndex{n: len(v.docs), total: v.total, df: prepared.index.df, parts: []*lexicalIndex{prepared.index, tail}}, true, nil
		}
		if prepared != nil && v.ready {
			return s.filteredPrepared(ctx, v, query, prepared)
		}
	}
	index, ready, err := s.selectionLexical(ctx, v, query, nil)
	if err == nil && ready && s.cache != nil && len(index.docs) <= 150000 {
		c := s.cache
		c.mu.Lock()
		if c.snapshotHash == hash && c.indexes[key] == nil && len(index.docs) <= c.lexicalCapacity() {
			for len(c.order) >= 256 || c.documents+len(index.docs) > c.lexicalCapacity() {
				oldest := c.order[0]
				c.order = c.order[1:]
				c.documents -= len(c.indexes[oldest].docs)
				delete(c.indexes, oldest)
			}
			c.indexes[key] = index
			c.order = append(c.order, key)
			c.documents += len(index.docs)
		}
		c.mu.Unlock()
	}
	return index, ready, err
}

func (s *Store) selectionFeedback(ctx context.Context, v *querySelection, ids []string) (*lexicalIndex, error) {
	top := []record{}
	for _, id := range ids {
		if pos, ok := v.byID[id]; ok {
			top = append(top, v.docs[pos])
		}
	}
	index := newLexical(top)
	index.n = len(v.docs)
	if len(top) == 0 {
		return index, nil
	}
	df, err := s.selectionDF(ctx, v)
	if err != nil {
		return nil, err
	}
	index.df = df
	return index, nil
}
