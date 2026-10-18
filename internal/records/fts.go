package records

import (
	"context"
	"errors"
	"fmt"
	"os"
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
			stop()
			if err != nil && ctx.Err() == nil {
				fmt.Fprintf(os.Stderr, "records lexical build: %v\n", err)
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
		if err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version_id),0) FROM lexical_documents").Scan(&last); err != nil {
			return err
		}
		rows, err := s.db.QueryContext(ctx, "SELECT rowid,search_text FROM record_versions WHERE rowid>? ORDER BY rowid LIMIT 512", last)
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

func (s *Store) precomputedLexical(ctx context.Context, docs []record, query string) (*lexicalIndex, bool, error) {
	if !s.fts {
		return nil, false, nil
	}
	wanted := make(map[int64]int, len(docs))
	index := &lexicalIndex{docs: make([]lexicalDoc, len(docs)), df: map[string]int{}, n: len(docs)}
	for i, r := range docs {
		if r.VersionID == 0 || r.TokenCount < 0 {
			if r.TokenCount == -1 {
				s.wakeLexical()
			}
			return nil, false, nil
		}
		wanted[r.VersionID] = i
		index.docs[i] = lexicalDoc{id: r.ID, length: r.TokenCount}
		index.total += r.TokenCount
	}
	terms := []string{}
	seen := map[string]bool{}
	for _, token := range Tokens(query) {
		if len(token) > 32768 {
			return nil, false, nil
		}
		if !seen[token] {
			terms = append(terms, token)
			seen[token] = true
		}
	}
	counts, err := s.ftsCounts(ctx, terms, wanted)
	if err != nil {
		return nil, false, err
	}
	for token, posting := range counts {
		index.df[token] = len(posting)
		for pos, count := range posting {
			if index.docs[pos].tf == nil {
				index.docs[pos].tf = map[string]int{}
			}
			index.docs[pos].tf[token] = count
		}
	}
	return index, true, nil
}

func (s *Store) ftsCounts(ctx context.Context, terms []string, wanted map[int64]int) (map[string]map[int]int, error) {
	out := map[string]map[int]int{}
	for start := 0; start < len(terms); start += 512 {
		part := terms[start:min(start+512, len(terms))]
		args := make([]any, len(part))
		for i, term := range part {
			args[i] = term
		}
		query := "SELECT term,doc,count(*) FROM lexical_terms WHERE term IN (" + strings.TrimSuffix(strings.Repeat("?,", len(part)), ",") + ") GROUP BY term,doc"
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var token string
			var row int64
			var count int
			if err = rows.Scan(&token, &row, &count); err != nil {
				rows.Close()
				return nil, err
			}
			if pos, ok := wanted[row]; ok {
				if out[token] == nil {
					out[token] = map[int]int{}
				}
				out[token][pos] = count
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) precomputedFeedback(ctx context.Context, docs []record, ids []string, query string) (*lexicalIndex, error) {
	selected := map[string]bool{}
	for _, id := range ids {
		selected[id] = true
	}
	wanted := make(map[int64]int, len(docs))
	top := []record{}
	for i, r := range docs {
		wanted[r.VersionID] = i
		if selected[r.ID] {
			top = append(top, r)
		}
	}
	index := newLexical(top)
	index.n = len(docs)
	exclude := map[string]bool{}
	for _, token := range Tokens(query) {
		exclude[token] = true
	}
	terms := []string{}
	for token := range index.df {
		if feedbackToken(token, exclude) {
			terms = append(terms, token)
		}
	}
	counts, err := s.ftsCounts(ctx, terms, wanted)
	if err != nil {
		return nil, err
	}
	for _, token := range terms {
		index.df[token] = len(counts[token])
	}
	return index, nil
}
