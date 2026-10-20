package records

import (
	"context"
	"encoding/json"
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
		links := []Link{}
		for _, link := range append(index.links[id], index.replacements[id]...) {
			links = append(links, Link{ID: link.ID, Version: link.Version, Type: link.Type, Weight: link.Weight})
		}
		if len(links) > 0 {
			out[id] = links
		}
	}
	return out, nil
}

// Adjacent returns, for each of ids that has any, the records written just
// before and just after it in its session, in that order: among the active
// records visible in scope (original documents and index items left out),
// in store order, the previous and the next one of the same session. A
// session is a place (scope, workspace and session) and, within it, the
// record's original document when the writer named one (data.documentID),
// else the day of the writer's data.eventAt, else of the record's date, in
// the writer's own offset. A record with neither has no session and no
// neighbors; neighbors never cross sessions.
func (s *Store) Adjacent(ctx context.Context, revisionHash string, scope Scope, ids []string) (map[string][]string, error) {
	rev, hash, err := revision(ctx, s.readDB(), revisionHash)
	if err != nil {
		return nil, err
	}
	all, err := s.queryRecords(ctx, rev, hash)
	if err != nil {
		return nil, err
	}
	// Only the two writer fields sessions need are projected.
	rows, err := s.readDB().QueryContext(ctx, `SELECT id,
 CASE json_type(record_json,'$.data.documentID') WHEN 'text' THEN json_extract(record_json,'$.data.documentID') ELSE '' END,
 CASE json_type(record_json,'$.data.eventAt') WHEN 'text' THEN json_extract(record_json,'$.data.eventAt') ELSE '' END
FROM record_versions WHERE rev_from<=? AND (rev_to IS NULL OR rev_to>?)`, rev, rev)
	if err != nil {
		return nil, err
	}
	written := map[string][2]string{}
	for rows.Next() {
		var id, document, eventAt string
		if err = rows.Scan(&id, &document, &eventAt); err != nil {
			rows.Close()
			return nil, err
		}
		written[id] = [2]string{document, eventAt}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	sessions, key := map[string][]string{}, map[string]string{}
	for _, r := range all {
		if !visible(r, scope) || r.State != "active" || r.Kind == "memory-document" || r.Kind == "memory-index" {
			continue
		}
		if session, ok := sessionOf(r, written[r.ID][0], written[r.ID][1]); ok {
			key[r.ID] = session
			sessions[session] = append(sessions[session], r.ID)
		}
	}
	position := map[string]int{}
	for _, list := range sessions {
		for i, id := range list {
			position[id] = i
		}
	}
	out := map[string][]string{}
	for _, id := range ids {
		session, ok := key[id]
		if _, done := out[id]; done || !ok {
			continue
		}
		list, at := sessions[session], position[id]
		near := []string{}
		if at > 0 {
			near = append(near, list[at-1])
		}
		if at+1 < len(list) {
			near = append(near, list[at+1])
		}
		if len(near) > 0 {
			out[id] = near
		}
	}
	return out, nil
}

// sessionOf is the session key of a record: its place, then its document or
// its day.
func sessionOf(r record, document, eventAt string) (string, bool) {
	place, _ := json.Marshal([]any{r.Scope, r.WorkspaceID, r.SessionID})
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
