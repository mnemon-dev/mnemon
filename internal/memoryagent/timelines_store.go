package memoryagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// The timelines state lives in memory-index.db beside the v1 overlays, one
// row per scope, written whole. Like the overlays, a write names the token of
// the state it read and fails if another writer got there first.
const timelinesSchema = `CREATE TABLE IF NOT EXISTS timelines(scope TEXT PRIMARY KEY,token TEXT NOT NULL,payload TEXT NOT NULL)`

// loadTimelines returns the scope's state and its token; a scope never
// written, or a readonly database from before the table, gives the empty
// state and token "".
func (o *Overlay) loadTimelines(ctx context.Context, scope records.Scope) (timelineState, string, error) {
	state := emptyTimelineState()
	if o == nil {
		return state, "", nil
	}
	var raw, token string
	err := o.db.QueryRowContext(ctx, `SELECT token,payload FROM timelines WHERE scope=?`, scopeKey(scope)).Scan(&token, &raw)
	if errors.Is(err, sql.ErrNoRows) || err != nil && o.readonly && strings.Contains(err.Error(), "no such table") {
		return state, "", nil
	}
	if err == nil && token != reference("timelines", raw).Hash {
		err = errors.New("timelines payload digest changed")
	}
	if err == nil {
		err = json.Unmarshal([]byte(raw), &state)
	}
	if err == nil {
		err = state.normalize()
	}
	return state, token, err
}

// saveTimelines replaces the state read with token previous ("" for none)
// and returns the new token.
func (o *Overlay) saveTimelines(ctx context.Context, scope records.Scope, previous string, state timelineState) (string, error) {
	raw, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	token := reference("timelines", string(raw)).Hash
	var result sql.Result
	if previous == "" {
		result, err = o.db.ExecContext(ctx, `INSERT OR IGNORE INTO timelines(scope,token,payload) VALUES(?,?,?)`, scopeKey(scope), token, string(raw))
	} else {
		result, err = o.db.ExecContext(ctx, `UPDATE timelines SET token=?,payload=? WHERE scope=? AND token=?`, token, string(raw), scopeKey(scope), previous)
	}
	if err != nil {
		return "", err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		err = errors.New("timelines state changed concurrently; rerun the consolidation job")
	}
	if err != nil {
		return "", err
	}
	return token, nil
}

// allTimelines returns every scope's stored state and its scope key
// (scopeKey), in key order.
func (o *Overlay) allTimelines(ctx context.Context) (keys []string, states []timelineState, err error) {
	rows, err := o.db.QueryContext(ctx, `SELECT scope,token,payload FROM timelines ORDER BY scope`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, token, raw string
		if err := rows.Scan(&key, &token, &raw); err != nil {
			return nil, nil, err
		}
		if token != reference("timelines", raw).Hash {
			return nil, nil, errors.New("timelines payload digest changed")
		}
		state := emptyTimelineState()
		if err := json.Unmarshal([]byte(raw), &state); err != nil {
			return nil, nil, err
		}
		if err := state.normalize(); err != nil {
			return nil, nil, err
		}
		keys, states = append(keys, key), append(states, state)
	}
	return keys, states, rows.Err()
}
