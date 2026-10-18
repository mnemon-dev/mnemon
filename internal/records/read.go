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
	if c := s.cache; c != nil {
		_, value, err := revision(ctx, s.db, "")
		if err != nil {
			return nil, err
		}
		current = value
		c.mu.Lock()
		if c.current != current {
			c.current = current
			c.indexes = map[string]*lexicalIndex{}
			c.order = nil
			c.documents = 0
			c.snapshotHash = ""
			c.snapshot = nil
		}
		if c.snapshotHash == hash {
			snapshot := c.snapshot
			c.mu.Unlock()
			return snapshot, nil
		}
		c.mu.Unlock()
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,kind,scope,workspace_id,session_id,date,state,status_json,created_at,search_text,vector_text,seq
FROM record_versions WHERE rev_from<=? AND (rev_to IS NULL OR rev_to>?) ORDER BY seq`, rev, rev)
	if err != nil {
		return nil, err
	}
	out := []record{}
	for rows.Next() {
		var r record
		var status sql.NullString
		if err = rows.Scan(&r.ID, &r.Kind, &r.Scope, &r.WorkspaceID, &r.SessionID, &r.Date, &r.State, &status, &r.CreatedAt, &r.SearchText, &r.VectorText, &r.Seq); err != nil {
			rows.Close()
			return nil, err
		}
		if status.Valid {
			r.Data = map[string]json.RawMessage{"status": json.RawMessage(status.String)}
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
		if c.current == current {
			c.snapshotHash = hash
			c.snapshot = out
		}
		c.mu.Unlock()
	}
	return out, nil
}
