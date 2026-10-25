package memoryagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// The consolidated items' words index (view.layerRecall,
// docs/superpowers/specs/2026-10-26-neutral-memory-and-single-pass-search-design.md):
// every event (under its topic's title), every value history and every
// directive of a scope's timelines state is a row of an FTS5 table beside
// the state, its words the records' tokens (records.Tokens). The rows follow
// the state they were built from by its token: a read that finds another
// token rebuilds the scope's rows from the state, and the reads after it
// query the rows as they are. Adds never touch the index.
const layerIndexSchema = `CREATE VIRTUAL TABLE IF NOT EXISTS layer_fts USING fts5(tokens, scope UNINDEXED, item UNINDEXED, payload UNINDEXED,
 tokenize="unicode61 remove_diacritics 0 categories 'L* N* M* S* Co Cn' tokenchars '-_:'");
CREATE TABLE IF NOT EXISTS layer_fts_state(scope TEXT PRIMARY KEY,token TEXT NOT NULL)`

// layerEntry is one indexed item: an event ("e<i>"), a value history
// ("v:" and its key) or a directive ("d<i>"), with what showing it needs.
type layerEntry struct {
	Item      string      `json:"item"`
	Kind      string      `json:"kind"` // "event", "value" or "directive"
	Index     int         `json:"index"`
	Title     string      `json:"title,omitempty"`
	Event     *event      `json:"event,omitempty"`
	Key       string      `json:"key,omitempty"`
	Values    []factValue `json:"values,omitempty"`
	Directive *directive  `json:"directive,omitempty"`
}

// words is what the index searches of an entry: an event under its topic's
// title, a key with its values, a directive's text.
func (e layerEntry) words() string {
	switch e.Kind {
	case "event":
		return e.Title + "\n" + e.Event.Text
	case "value":
		parts := []string{e.Key}
		for _, v := range e.Values {
			parts = append(parts, v.Value)
		}
		return strings.Join(parts, "\n")
	}
	return e.Directive.Text
}

// judgeText is what JEV reads of an entry: an event with its topic and
// date, a value history as the layer shows it.
func (e layerEntry) judgeText() string {
	if e.Kind == "event" {
		return e.Title + ": " + e.Event.Date + " " + e.Event.Text
	}
	return valueText(e.Key, e.Values)
}

// layerEntries is every item of m as an entry, in memory order.
func layerEntries(m timelineMemory) []layerEntry {
	titles := map[string]string{}
	for _, t := range m.Threads {
		titles[t.ID] = t.Title
	}
	out := []layerEntry{}
	for i := range m.Events {
		out = append(out, layerEntry{Item: itemID("e", i), Kind: "event", Index: i, Title: titles[m.Events[i].Thread], Event: &m.Events[i]})
	}
	for i, key := range m.FactOrder {
		out = append(out, layerEntry{Item: "v:" + key, Kind: "value", Index: i, Key: key, Values: m.Facts[key]})
	}
	for i := range m.Directives {
		out = append(out, layerEntry{Item: itemID("d", i), Kind: "directive", Index: i, Directive: &m.Directives[i]})
	}
	return out
}

// syncLayerIndex makes the scope's rows those of its current timelines
// state: when the rows were built from another state, they are rebuilt from
// the current one in one transaction. A readonly overlay cannot rebuild.
func (o *Overlay) syncLayerIndex(ctx context.Context, scope records.Scope) error {
	key := scopeKey(scope)
	current := ""
	err := o.db.QueryRowContext(ctx, `SELECT token FROM timelines WHERE scope=?`, key).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	built := ""
	err = o.db.QueryRowContext(ctx, `SELECT token FROM layer_fts_state WHERE scope=?`, key).Scan(&built)
	if err == nil && built == current {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if o.readonly {
		return errors.New("the consolidated items' index is stale and the overlay is readonly")
	}
	state, token, err := o.loadTimelines(ctx, scope)
	if err != nil {
		return err
	}
	tx, err := o.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM layer_fts WHERE scope=?`, key); err != nil {
		return err
	}
	insert, err := tx.PrepareContext(ctx, `INSERT INTO layer_fts(tokens,scope,item,payload) VALUES(?,?,?,?)`)
	if err != nil {
		return err
	}
	defer insert.Close()
	for _, entry := range layerEntries(state.Memory) {
		payload, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		if _, err = insert.ExecContext(ctx, strings.Join(records.Tokens(entry.words()), " "), key, entry.Item, string(payload)); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO layer_fts_state(scope,token) VALUES(?,?)`, key, token); err != nil {
		return err
	}
	return tx.Commit()
}

// searchLayer is the scope's events and value histories the query's words
// find, best first by FTS5's BM25, at most limit; directives are not
// searched.
func (o *Overlay) searchLayer(ctx context.Context, scope records.Scope, query string, limit int) ([]layerEntry, error) {
	terms := []string{}
	for _, token := range records.Tokens(query) {
		quoted := `"` + strings.ReplaceAll(token, `"`, `""`) + `"`
		if !slices.Contains(terms, quoted) {
			terms = append(terms, quoted)
		}
	}
	if len(terms) == 0 {
		return nil, nil
	}
	rows, err := o.db.QueryContext(ctx, `SELECT payload FROM layer_fts WHERE layer_fts MATCH ? AND scope=? AND item NOT LIKE 'd%' ORDER BY bm25(layer_fts) LIMIT ?`,
		strings.Join(terms, " OR "), scopeKey(scope), limit)
	if err != nil {
		return nil, err
	}
	return layerRows(rows)
}

// layerDirectives is the scope's directives in memory order.
func (o *Overlay) layerDirectives(ctx context.Context, scope records.Scope) ([]layerEntry, error) {
	rows, err := o.db.QueryContext(ctx, `SELECT payload FROM layer_fts WHERE scope=? AND item LIKE 'd%'`, scopeKey(scope))
	if err != nil {
		return nil, err
	}
	out, err := layerRows(rows)
	slices.SortFunc(out, func(a, b layerEntry) int { return a.Index - b.Index })
	return out, err
}

func layerRows(rows *sql.Rows) ([]layerEntry, error) {
	defer rows.Close()
	out := []layerEntry{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var entry layerEntry
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}
