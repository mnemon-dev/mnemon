package records

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type Scope struct {
	WorkspaceID *string `json:"workspaceId,omitempty"`
	SessionID   *string `json:"sessionId,omitempty"`
}
type QueryRequest struct {
	Revision  string                     `json:"revision"`
	Input     map[string]json.RawMessage `json:"input"`
	Scope     Scope                      `json:"scope"`
	Embedding *EmbeddingConfig           `json:"embedding,omitempty"`
}
type QueryResult struct {
	IDs       []string  `json:"ids"`
	Expansion *[]string `json:"expansion,omitempty"`
}

func stringValue(value json.RawMessage) string { var v string; _ = json.Unmarshal(value, &v); return v }
func trueValue(value json.RawMessage) bool     { return string(value) == "true" }
func equalString(filter json.RawMessage, value string) bool {
	if filter == nil {
		return true
	}
	var v any
	if json.Unmarshal(filter, &v) != nil {
		return false
	}
	return v == value
}
func same(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
func visible(r record, scope Scope) bool {
	if r.Scope == "project" {
		return scope.WorkspaceID != nil && *scope.WorkspaceID != "" && same(r.WorkspaceID, scope.WorkspaceID)
	}
	if r.Scope == "session" {
		return scope.SessionID != nil && *scope.SessionID != "" && same(r.SessionID, scope.SessionID) && same(r.WorkspaceID, scope.WorkspaceID)
	}
	return true
}
func jsString(value json.RawMessage) string {
	var v any
	if json.Unmarshal(value, &v) != nil {
		return "undefined"
	}
	if v == nil {
		return "null"
	}
	if text, ok := v.(string); ok {
		return text
	}
	return fmt.Sprint(v)
}

func candidates(all []record, req QueryRequest, term string, words bool) []record {
	in := req.Input
	out := []record{}
	var values []json.RawMessage
	idsEnabled := json.Unmarshal(in["ids"], &values) == nil && string(in["ids"]) != "null"
	ids := map[string]bool{}
	if idsEnabled {
		for _, v := range values {
			var id string
			if json.Unmarshal(v, &id) == nil {
				ids[id] = true
			}
		}
	}
	accept := func(r record) bool {
		if !visible(r, req.Scope) || !(r.State == "active" || trueValue(in["archived"]) && r.State == "archived") {
			return false
		}
		if !equalString(in["id"], r.ID) || idsEnabled && !ids[r.ID] || !equalString(in["kind"], r.Kind) {
			return false
		}
		if in["date"] != nil && (r.Date == nil || !equalString(in["date"], *r.Date)) {
			return false
		}
		if in["status"] != nil && !scalarEqual(r.Data["status"], in["status"]) {
			return false
		}
		day := r.CreatedAt
		if len(day) > 10 {
			day = day[:10]
		}
		if r.Date != nil {
			day = *r.Date
		}
		if v := in["since"]; v != nil {
			bound := jsString(v)
			actual := day
			if strings.Contains(bound, "T") {
				actual = r.CreatedAt
			}
			if actual < bound {
				return false
			}
		}
		if v := in["until"]; v != nil {
			bound := jsString(v)
			actual := day
			if strings.Contains(bound, "T") {
				actual = r.CreatedAt
			}
			if actual > bound {
				return false
			}
		}
		if term != "" && !words && !strings.Contains(lower(r.SearchText), term) {
			return false
		}
		return true
	}
	var filtered bool
	for i, r := range all {
		if !accept(r) {
			if !filtered {
				out = append(out, all[:i]...)
				filtered = true
			}
			continue
		}
		if filtered {
			out = append(out, r)
		}
	}
	if !filtered {
		return all // Immutable borrowed view; callers must not sort this slice.
	}
	return out
}

func (s *Store) Query(ctx context.Context, req QueryRequest) (QueryResult, error) {
	rev, hash, err := revision(ctx, s.db, req.Revision)
	if err != nil {
		return QueryResult{}, err
	}
	all, err := s.queryRecords(ctx, rev, hash)
	if err != nil {
		return QueryResult{}, err
	}
	query := stringValue(req.Input["query"])
	term := lower(query)
	match := stringValue(req.Input["match"])
	words := (match == "words" || match == "hybrid") && term != ""
	selection := s.selectRecords(all, hash, req)
	records := selection.docs
	if !words && term != "" {
		records = candidates(records, req, term, false)
	}
	result := QueryResult{IDs: []string{}}
	if words {
		index, precomputed, err := s.queryLexical(ctx, hash, req, selection, term)
		if err == nil && !precomputed {
			index, err = s.lexical(ctx, hash, req, records)
		}
		if err != nil {
			return QueryResult{}, err
		}
		result.IDs = index.search(term)
		if match == "hybrid" && req.Embedding != nil {
			semantic, err := s.selectionVectorRank(ctx, query, selection, *req.Embedding)
			if err != nil {
				return QueryResult{}, err
			}
			result.IDs = fuse(result.IDs, semantic)
		}
		if trueValue(req.Input["expand"]) && req.Input["cursor"] == nil {
			ids := result.IDs
			if len(ids) > 10 {
				ids = ids[:10]
			}
			if precomputed {
				index, err = s.selectionFeedback(ctx, selection, ids)
				if err != nil {
					return QueryResult{}, err
				}
			}
			expansion := index.feedback(ids, term)
			result.Expansion = &expansion
		}
	} else {
		if string(req.Input["recent"]) != "false" {
			records = append([]record(nil), records...)
			sort.SliceStable(records, func(i, j int) bool {
				if records[i].CreatedAt == records[j].CreatedAt {
					return records[i].ID < records[j].ID
				}
				return records[i].CreatedAt > records[j].CreatedAt
			})
		}
		for _, r := range records {
			result.IDs = append(result.IDs, r.ID)
		}
	}
	return result, nil
}
