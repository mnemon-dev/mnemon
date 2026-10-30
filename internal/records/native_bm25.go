package records

import (
	"context"
	"strings"
)

// nativeLexical ranks the selection's records by SQLite FTS5's own bm25()
// over the query's tokens, any of them (match "bm25"): the index the
// background build keeps (lexical_fts) and the scores SQLite computes, with
// none of the frozen rules. Ready is false while the index does not cover the
// selection; the caller then ranks by the frozen rules. limit > 0 keeps the
// best limit.
func (s *Store) nativeLexical(ctx context.Context, v *querySelection, query string, limit int) ([]string, bool, error) {
	if !s.fts || !v.ready {
		s.wakeLexical()
		return nil, false, nil
	}
	terms, seen := []string{}, map[string]bool{}
	for _, token := range Tokens(query) {
		if len(token) > 32768 {
			return nil, false, nil
		}
		if !seen[token] {
			seen[token] = true
			terms = append(terms, `"`+strings.ReplaceAll(token, `"`, `""`)+`"`)
		}
	}
	ids := []string{}
	if len(terms) == 0 {
		return ids, true, nil
	}
	rows, err := s.readDB().QueryContext(ctx, `SELECT rowid FROM lexical_fts WHERE lexical_fts MATCH ? ORDER BY bm25(lexical_fts)`, strings.Join(terms, " OR "))
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			return nil, false, err
		}
		if i, ok := v.wanted[version]; ok {
			ids = append(ids, v.docs[i].ID)
			if limit > 0 && len(ids) == limit {
				break
			}
		}
	}
	return ids, true, rows.Err()
}
