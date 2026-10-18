package records

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

// get needs opaque JSON only; query needs the indexed retrieval fields only.
// Neither path decodes a record's content/history/data into Go objects.
func (s *Store) readRecords(ctx context.Context, rev int64, hash string, ids []string) (Snapshot, error) {
	out := Snapshot{Revision: hash, Records: []string{}}
	if ids != nil && len(ids) == 0 {
		return out, nil
	}
	query := `SELECT id,record_json FROM record_versions WHERE rev_from<=? AND (rev_to IS NULL OR rev_to>?)`
	args := []any{rev, rev}
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	if ids != nil && len(ids) <= 500 {
		query += " AND id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")"
		for _, id := range ids {
			args = append(args, id)
		}
	}
	query += " ORDER BY seq"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return Snapshot{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, raw string
		if err = rows.Scan(&id, &raw); err != nil {
			return Snapshot{}, err
		}
		if ids == nil || wanted[id] {
			out.Records = append(out.Records, raw)
		}
	}
	return out, rows.Err()
}

func (s *Store) queryRecords(ctx context.Context, rev int64, hash string) ([]record, error) {
	current := ""
	var lexicalBuild uint64
	if c := s.cache; c != nil {
		_, value, err := revision(ctx, s.db, "")
		if err != nil {
			return nil, err
		}
		current = value
		c.mu.Lock()
		lexicalBuild = c.lexicalBuild
		if c.current != current {
			c.current = current
			c.indexes = map[string]*lexicalIndex{}
			c.order = nil
			c.documents = 0
			c.snapshotHash = ""
			c.snapshot = nil
			c.clearSelections()
		}
		if c.snapshotHash == hash {
			snapshot := c.snapshot
			c.mu.Unlock()
			return snapshot, nil
		}
		c.mu.Unlock()
	}
	columns, join := "-1", ""
	if s.fts {
		columns = "COALESCE(l.token_count,-1)"
		join = " LEFT JOIN lexical_documents l ON l.version_id=r.rowid"
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.id,r.kind,r.scope,r.workspace_id,r.session_id,r.date,r.state,r.status_json,r.created_at,r.search_text,r.vector_text,r.seq,r.rowid,`+columns+`
FROM record_versions r`+join+` WHERE r.rev_from<=? AND (r.rev_to IS NULL OR r.rev_to>?) ORDER BY r.seq`, rev, rev)
	if err != nil {
		return nil, err
	}
	out := []record{}
	for rows.Next() {
		var r record
		var status sql.NullString
		if err = rows.Scan(&r.ID, &r.Kind, &r.Scope, &r.WorkspaceID, &r.SessionID, &r.Date, &r.State, &status, &r.CreatedAt, &r.SearchText, &r.VectorText, &r.Seq, &r.VersionID, &r.TokenCount); err != nil {
			rows.Close()
			return nil, err
		}
		if status.Valid {
			r.Data = map[string]json.RawMessage{"status": json.RawMessage(status.String)}
		}
		if s.cache != nil {
			r.SearchLower = lower(r.SearchText)
		}
		out = append(out, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if c := s.cache; c != nil && len(out) <= 200000 {
		c.mu.Lock()
		if c.current == current && c.lexicalBuild == lexicalBuild {
			if c.snapshotHash != hash {
				c.clearSelections()
			}
			c.snapshotHash = hash
			c.snapshot = out
		}
		c.mu.Unlock()
	}
	return out, nil
}
