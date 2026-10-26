package memoryagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	raw, token, found, err := o.timelinesPayload(ctx, scope)
	if err == nil && found {
		if err = json.Unmarshal([]byte(raw), &state); err == nil {
			err = state.normalize()
		}
	}
	return state, token, err
}

// timelinesPayload reads the scope's stored state and its token, its digest
// checked; found is false for a scope never written, or a readonly database
// from before the table.
func (o *Overlay) timelinesPayload(ctx context.Context, scope records.Scope) (raw, token string, found bool, err error) {
	err = o.db.QueryRowContext(ctx, `SELECT token,payload FROM timelines WHERE scope=?`, scopeKey(scope)).Scan(&token, &raw)
	if errors.Is(err, sql.ErrNoRows) || err != nil && o.readonly && strings.Contains(err.Error(), "no such table") {
		return "", "", false, nil
	}
	if err == nil && token != reference("timelines", raw).Hash {
		err = errors.New("timelines payload digest changed")
	}
	return raw, token, err == nil, err
}

// loadProgress returns the watermark of the scope's state and how many
// records it holds back (Pending), as loadTimelines would give them, checking
// the state as loadTimelines does (its digest and format) but decoding no
// more of it: neither its memory nor a held-back record's text. A scope never
// written gives 0 and 0.
func (o *Overlay) loadProgress(ctx context.Context, scope records.Scope) (watermark int64, held int, err error) {
	if o == nil {
		return 0, 0, nil
	}
	raw, _, found, err := o.timelinesPayload(ctx, scope)
	if err != nil || !found {
		return 0, 0, err
	}
	var head struct {
		Format    string     `json:"format"`
		Watermark int64      `json:"watermark"`
		Pending   []struct{} `json:"pending"`
	}
	if err := json.Unmarshal([]byte(raw), &head); err != nil {
		return 0, 0, err
	}
	if head.Format != timelineFormat {
		return 0, 0, fmt.Errorf("unsupported timelines format %q", head.Format)
	}
	return head.Watermark, len(head.Pending), nil
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
