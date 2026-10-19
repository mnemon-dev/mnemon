package records

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/mnemon-dev/mnemon/internal/retrieval"
)

// RelationDocument is a lean, immutable input to optional relation builders.
// Version is the store's immutable version identity, not caller-provided JSON.
// Text is a retrieval hint; builders must reread raw evidence before judging it.
type RelationDocument struct {
	ID, Text, VectorText, Boundary, Group string
	Version, Position, Part               int64
	Entities                              []string
}

// RelationDocuments never creates an index or changes original records. An old
// database and historical revisions support this projection without migration.
func (s *Store) RelationDocuments(ctx context.Context, hash string, scope Scope, entities bool) (string, []RelationDocument, error) {
	rev, hash, err := revision(ctx, s.readDB(), hash)
	if err != nil {
		return "", nil, err
	}
	all, err := s.queryRecords(ctx, rev, hash)
	if err != nil {
		return "", nil, err
	}
	docs := []RelationDocument{}
	wanted := map[string]int{}
	for _, r := range all {
		if !visible(r, scope) || r.State != "active" || r.Kind == "memory-document" || r.Kind == "memory-index" {
			continue
		}
		boundary, _ := json.Marshal([]any{r.Scope, r.WorkspaceID, r.SessionID})
		wanted[r.ID] = len(docs)
		docs = append(docs, RelationDocument{ID: r.ID, Version: r.VersionID, Text: r.SearchText, VectorText: r.VectorText, Boundary: string(boundary)})
	}
	if len(docs) > 200000 {
		return "", nil, errors.New("relation index supports at most 200000 visible records")
	}
	rows, err := s.readDB().QueryContext(ctx, `SELECT id,json_object(
'entities',json_extract(record_json,'$.data.entities'),
'aliases',json_extract(record_json,'$.data.aliases'),
'thread',json_extract(record_json,'$.data.thread'),
'position',json_extract(record_json,'$.data.position'),
'document',json_extract(record_json,'$.data.documentID'),
'start',json_extract(record_json,'$.data.start'))
FROM record_versions WHERE rev_from<=? AND (rev_to IS NULL OR rev_to>?) ORDER BY seq`, rev, rev)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return "", nil, err
		}
		at, ok := wanted[id]
		if !ok {
			continue
		}
		applyRelationMetadata(&docs[at], raw, entities)
	}
	return hash, docs, rows.Err()
}

func applyRelationMetadata(d *RelationDocument, raw string, entities bool) {
	var meta struct {
		Entities, Aliases []string
		Thread, Document  string
		Position, Start   *int64
	}
	if json.Unmarshal([]byte(raw), &meta) != nil {
		return // Malformed optional hints cannot create a relation.
	}
	if entities {
		d.Entities = retrieval.ResolveEntities(d.Text, retrieval.MergeEntities(meta.Entities, meta.Aliases), retrieval.EntityModeMerge)
	}
	if meta.Thread != "" && len(meta.Thread) <= 256 && meta.Position != nil && *meta.Position >= 0 && *meta.Position <= 1<<53-1 {
		d.Group, d.Position = "thread:"+meta.Thread, *meta.Position
	} else if meta.Document != "" && len(meta.Document) <= 256 && meta.Start != nil && *meta.Start >= 0 {
		d.Group = "document:" + meta.Document
	}
	if meta.Start != nil && *meta.Start >= 0 {
		d.Part = *meta.Start
	}
	if d.Group != "" {
		d.Group = d.Boundary + "\x00" + d.Group
	}
}
