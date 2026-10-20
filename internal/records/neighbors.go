package records

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
)

// Neighbors of known records for a reader that expands from evidence it has
// already judged (the memory agent's view.expand): the links the derived
// index follows, and the records written just before and after a record in
// its session. Neither ranks, embeds nor calls a model.

// linkPolicy is the smallest index policy whose derived index holds the
// links; with the query runtime its index is cached like any other.
var linkPolicy = IndexPolicy{Version: 1, Relations: true}

// Links returns, for each of ids that has any, the links the derived index
// follows from it at revision ("" is the current one) in scope: the record
// writer's and the derived-metadata overlay's, as buildLinks keeps them
// (the target visible in scope, in the same place and at the version named,
// at most 50 a record), in its order and mirrored as it mirrors them:
// semantic and entity links both ways, temporal and causal as written. A
// correction (supersedes) is given both ways: from the record that corrects
// to the one it corrects, and back. A target may appear more than once, as
// the writer's link and the overlay's, or as a link and its mirror.
func (s *Store) Links(ctx context.Context, revisionHash string, scope Scope, ids []string) (map[string][]Link, error) {
	rev, hash, err := revision(ctx, s.readDB(), revisionHash)
	if err != nil {
		return nil, err
	}
	generation, err := derivedGeneration(ctx, s.readDB())
	if err != nil {
		return nil, err
	}
	all, err := s.queryRecords(ctx, rev, hash)
	if err != nil {
		return nil, err
	}
	policy := linkPolicy
	index, err := s.derived(ctx, rev, hash, all, QueryRequest{Revision: hash, Scope: scope, Input: map[string]json.RawMessage{}, Index: &policy, derived: generation})
	if err != nil {
		return nil, err
	}
	out := map[string][]Link{}
	for _, id := range ids {
		if _, done := out[id]; done {
			continue
		}
		// Copied: the cached index's slices are shared by every reader.
		links := []Link{}
		for _, list := range [][]Link{index.links[id], index.replacements[id]} {
			for _, link := range list {
				links = append(links, Link{ID: link.ID, Version: link.Version, Type: link.Type, Weight: link.Weight})
			}
		}
		if len(links) > 0 {
			out[id] = links
		}
	}
	return out, nil
}

// Nearby is a record written near another in their session: Offset records
// after it, or before it when negative.
type Nearby struct {
	ID     string
	Offset int
}

// Adjacent returns, for each of ids that has any, the records written up to
// window records before and after it in its session, nearest first and, at
// the same distance, the earlier first (offsets -1, 1, -2, 2, …): among the
// active records visible in scope (original documents and index items left
// out), in store order, those of the same session. A session is a place
// (scope, workspace and session) and, within it, the conversation session
// the writer named (data.session: the memory service writes each Add of a
// conversation as its own document of one session), else the record's
// original document (data.documentID), else the day of the writer's
// data.eventAt, else of the record's date, in the writer's own offset. A
// record with none of them has no session and no neighbors; neighbors never
// cross sessions. window must be at least 1. A revision's sessions in a
// scope are built once and cached: a revision never changes.
func (s *Store) Adjacent(ctx context.Context, revisionHash string, scope Scope, ids []string, window int) (map[string][]Nearby, error) {
	if window < 1 {
		return nil, errors.New("adjacent window must be at least 1")
	}
	rev, hash, err := revision(ctx, s.readDB(), revisionHash)
	if err != nil {
		return nil, err
	}
	index, err := s.sessionIndex(ctx, rev, hash, scope)
	if err != nil {
		return nil, err
	}
	out := map[string][]Nearby{}
	for _, id := range ids {
		session, ok := index.key[id]
		if _, done := out[id]; done || !ok {
			continue
		}
		list, at := index.sessions[session], index.position[id]
		near := []Nearby{}
		for distance := 1; distance <= window; distance++ {
			if at-distance >= 0 {
				near = append(near, Nearby{list[at-distance], -distance})
			}
			if at+distance < len(list) {
				near = append(near, Nearby{list[at+distance], distance})
			}
		}
		if len(near) > 0 {
			out[id] = near
		}
	}
	return out, nil
}

// sessions is a scope's records at one revision grouped into their
// sessions, each in store order.
type sessions struct {
	key      map[string]string   // record id → session
	sessions map[string][]string // session → record ids
	position map[string]int      // record id → position in its session
}

// sessionCache keeps the sessions of the last sessionEntries (revision,
// scope) pairs read; a revision is immutable, so an entry never goes stale.
// loading holds a pair being built: its other first readers wait for it.
type sessionCache struct {
	mu      sync.Mutex
	entries map[string]*sessions
	order   []string
	loading map[string]chan struct{}
}

const sessionEntries = 8

// sessionIndex is the sessions of the records visible in scope at rev
// (hash), from the cache or built and cached. One read builds a pair; its
// concurrent first readers wait for that build (and build it themselves
// if it fails), so a pair is built once.
func (s *Store) sessionIndex(ctx context.Context, rev int64, hash string, scope Scope) (*sessions, error) {
	raw, _ := json.Marshal(scope)
	key := hash + "\x00" + string(raw)
	c := &s.sessionCache
	for {
		c.mu.Lock()
		if index := c.entries[key]; index != nil {
			c.mu.Unlock()
			return index, nil
		}
		if wait := c.loading[key]; wait != nil {
			c.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		done := make(chan struct{})
		if c.loading == nil {
			c.loading = map[string]chan struct{}{}
		}
		c.loading[key] = done
		c.mu.Unlock()
		index, err := s.buildSessions(ctx, rev, hash, scope)
		c.mu.Lock()
		delete(c.loading, key)
		close(done)
		if err == nil {
			if c.entries == nil {
				c.entries = map[string]*sessions{}
			}
			for len(c.order) >= sessionEntries {
				delete(c.entries, c.order[0])
				c.order = c.order[1:]
			}
			c.entries[key] = index
			c.order = append(c.order, key)
		}
		c.mu.Unlock()
		return index, err
	}
}

// buildSessions groups the records visible in scope at rev into sessions,
// reading the three writer fields sessions need.
func (s *Store) buildSessions(ctx context.Context, rev int64, hash string, scope Scope) (*sessions, error) {
	s.sessionBuilds.Add(1)
	all, err := s.queryRecords(ctx, rev, hash)
	if err != nil {
		return nil, err
	}
	rows, err := s.readDB().QueryContext(ctx, `SELECT id,
 CASE json_type(record_json,'$.data.session') WHEN 'text' THEN json_extract(record_json,'$.data.session') ELSE '' END,
 CASE json_type(record_json,'$.data.documentID') WHEN 'text' THEN json_extract(record_json,'$.data.documentID') ELSE '' END,
 CASE json_type(record_json,'$.data.eventAt') WHEN 'text' THEN json_extract(record_json,'$.data.eventAt') ELSE '' END
FROM record_versions WHERE rev_from<=? AND (rev_to IS NULL OR rev_to>?)`, rev, rev)
	if err != nil {
		return nil, err
	}
	written := map[string][3]string{}
	for rows.Next() {
		var id, session, document, eventAt string
		if err = rows.Scan(&id, &session, &document, &eventAt); err != nil {
			rows.Close()
			return nil, err
		}
		written[id] = [3]string{session, document, eventAt}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	index := &sessions{key: map[string]string{}, sessions: map[string][]string{}, position: map[string]int{}}
	for _, r := range all {
		if !visible(r, scope) || r.State != "active" || r.Kind == "memory-document" || r.Kind == "memory-index" {
			continue
		}
		fields := written[r.ID]
		if session, ok := sessionOf(r, fields[0], fields[1], fields[2]); ok {
			index.key[r.ID] = session
			index.position[r.ID] = len(index.sessions[session])
			index.sessions[session] = append(index.sessions[session], r.ID)
		}
	}
	return index, nil
}

// sessionOf is the session key of a record: its place, then its
// conversation session, its document or its day.
func sessionOf(r record, session, document, eventAt string) (string, bool) {
	place, _ := json.Marshal([]any{r.Scope, r.WorkspaceID, r.SessionID})
	if session != "" {
		return string(place) + "\x00session\x00" + session, true
	}
	if document != "" {
		return string(place) + "\x00document\x00" + document, true
	}
	for _, value := range []*string{&eventAt, r.Date} {
		if value == nil || *value == "" {
			continue
		}
		if at, err := parseTime(*value); err == nil {
			return string(place) + "\x00day\x00" + at.Format("2006-01-02"), true
		}
	}
	return "", false
}
