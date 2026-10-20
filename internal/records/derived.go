package records

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
)

// Derived metadata is a rebuildable overlay of model-derived index metadata
// kept beside the records, never in them: a derivation job (a source) owns
// its rows, one per record, and replaces them whole. A row applies only
// while its record has the version the row names, and only adds to what the
// record writer gave: its entities and aliases join the writer's, its links
// follow the writer's and, like every writer link, never carry correction
// authority (the reserved mnemonSupersedes channel alone does), and its event
// time stands in only when the writer gave none. Each replace bumps the
// overlay's generation (meta key "derived"), which keys the derived-index,
// selection and lexical caches of indexed queries, so none serves a replaced
// overlay. A store whose overlay was never written has no generation and
// reads exactly as before.

// DerivedMetadata is one record's overlay row. Version is the record version
// it describes (the record JSON's version, which links also bind). At most 64
// entities, 64 aliases and 50 links; weights in (0,1].
type DerivedMetadata struct {
	Version  int64    `json:"version"`
	Entities []string `json:"entities,omitempty"`
	Aliases  []string `json:"aliases,omitempty"`
	Links    []Link   `json:"links,omitempty"`
	EventAt  string   `json:"eventAt,omitempty"`
}

const derivedSchema = `CREATE TABLE IF NOT EXISTS derived_metadata(source TEXT NOT NULL, id TEXT NOT NULL, version INTEGER NOT NULL,
 meta TEXT NOT NULL, PRIMARY KEY(source,id));`

func (m DerivedMetadata) validate() error {
	if m.Version < 1 {
		return errors.New("derived metadata requires a record version of at least 1")
	}
	if len(m.Entities) > 64 || len(m.Aliases) > 64 || len(m.Links) > 50 {
		return errors.New("derived metadata allows at most 64 entities, 64 aliases and 50 links")
	}
	if slices.Contains(m.Entities, "") || slices.Contains(m.Aliases, "") {
		return errors.New("derived entities and aliases must not be empty")
	}
	for _, link := range m.Links {
		if link.ID == "" || link.Version < 1 || !(link.Weight > 0 && link.Weight <= 1) || !validLinkType(link.Type) {
			return errors.New("derived links require an id, a version of at least 1, a weight in (0,1] and a known type")
		}
	}
	if m.EventAt != "" {
		if _, err := parseTime(m.EventAt); err != nil {
			return errors.New("derived eventAt must be an ISO date or RFC3339 timestamp")
		}
	}
	return nil
}

// ReplaceDerivedMetadata replaces every overlay row of source with items
// (record id → row), atomically. Only changes are written: a new or changed
// row is upserted, a row no longer given is deleted, and an unchanged row is
// left alone; the generation moves only when something changed, so the
// caches stay warm otherwise. A refused replace changes nothing.
func (s *Store) ReplaceDerivedMetadata(ctx context.Context, source string, items map[string]DerivedMetadata) error {
	if s.readonly {
		return errors.New("records derived metadata is unavailable with --readonly")
	}
	if source == "" || len(source) > 64 {
		return errors.New("derived metadata requires a source of 1–64 bytes")
	}
	if len(items) > 200000 {
		return errors.New("derived metadata allows at most 200000 records per source")
	}
	ids := make([]string, 0, len(items))
	for id, item := range items {
		if id == "" {
			return errors.New("derived metadata requires record ids")
		}
		if err := item.validate(); err != nil {
			return fmt.Errorf("record %q: %w", id, err)
		}
		ids = append(ids, id)
	}
	slices.Sort(ids)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// The source's rows as stored: id → version and meta.
	stored := map[string]string{}
	rows, err := tx.QueryContext(ctx, "SELECT id,version,meta FROM derived_metadata WHERE source=?", source)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, meta string
		var version int64
		if err := rows.Scan(&id, &version, &meta); err != nil {
			rows.Close()
			return err
		}
		stored[id] = fmt.Sprint(version, " ", meta)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	changed := false
	for _, id := range ids {
		raw, err := json.Marshal(items[id])
		if err != nil {
			return err
		}
		old, ok := stored[id]
		delete(stored, id)
		if ok && old == fmt.Sprint(items[id].Version, " ", string(raw)) {
			continue
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO derived_metadata VALUES(?,?,?,?)
 ON CONFLICT(source,id) DO UPDATE SET version=excluded.version,meta=excluded.meta`, source, id, items[id].Version, string(raw)); err != nil {
			return err
		}
		changed = true
	}
	gone := slices.Sorted(maps.Keys(stored))
	for _, id := range gone {
		if _, err = tx.ExecContext(ctx, "DELETE FROM derived_metadata WHERE source=? AND id=?", source, id); err != nil {
			return err
		}
		changed = true
	}
	if !changed {
		return nil
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO meta VALUES('derived','1') ON CONFLICT(key) DO UPDATE SET value=CAST(value AS INTEGER)+1"); err != nil {
		return err
	}
	return tx.Commit()
}

// derivedGeneration is the overlay's generation, "" when no overlay was ever
// written (then there are no rows).
func derivedGeneration(ctx context.Context, db reader) (string, error) {
	var value string
	err := db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key='derived'").Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

// derivedRows reads, in one statement and so from one snapshot, the overlay
// generation and the rows of the wanted records, each record's in source
// order. A row that does not decode is left out.
func (s *Store) derivedRows(ctx context.Context, wanted map[string]bool) (string, map[string][]DerivedMetadata, error) {
	rows, err := s.readDB().QueryContext(ctx, `SELECT NULL,NULL,NULL,value FROM meta WHERE key='derived'
UNION ALL SELECT source,id,version,meta FROM derived_metadata ORDER BY 1,2`)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	generation, out := "", map[string][]DerivedMetadata{}
	for rows.Next() {
		var source, id sql.NullString
		var version sql.NullInt64
		var raw string
		if err := rows.Scan(&source, &id, &version, &raw); err != nil {
			return "", nil, err
		}
		if !source.Valid {
			generation = raw
			continue
		}
		var row DerivedMetadata
		if !wanted[id.String] || json.Unmarshal([]byte(raw), &row) != nil {
			continue
		}
		row.Version = version.Int64
		out[id.String] = append(out[id.String], row)
	}
	return generation, out, rows.Err()
}

// merge adds an overlay row to a record writer's metadata. Its names are
// kept apart: the entity index takes them after the writer's entities and
// aliases and the names extracted from the text.
func (m *indexMetadata) merge(row DerivedMetadata) {
	m.derivedEntities = appendNew(m.derivedEntities, row.Entities)
	m.derivedAliases = appendNew(m.derivedAliases, row.Aliases)
	for _, link := range row.Links {
		link.authoritative = false
		m.Links = append(m.Links, link)
	}
	if m.EventAt == "" {
		m.EventAt = row.EventAt
	}
}

// appendNew appends the values of b that a does not hold, each once.
func appendNew(a, b []string) []string {
	for _, value := range b {
		if value != "" && !slices.Contains(a, value) {
			a = append(a, value)
		}
	}
	return a
}
