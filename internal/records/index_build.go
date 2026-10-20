package records

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/mnemon-dev/mnemon/internal/retrieval"
)

// One builder and at most 200k additional indexed document references across
// four profiles. Cold I/O does not hold the cache mutex. Historical and scoped
// indexes have different keys and never mutate the baseline snapshot.
func (s *Store) derived(ctx context.Context, rev int64, hash string, all []record, req QueryRequest) (*derivedIndex, error) {
	key := searchKey(hash, req)
	c := s.cache
	if c == nil {
		return s.buildDerived(ctx, rev, all, req)
	}
	for {
		c.mu.Lock()
		if index := c.derived[key]; index != nil {
			c.mu.Unlock()
			return index, nil
		}
		if pending := c.derivedLoading; pending != nil {
			c.mu.Unlock()
			select {
			case <-pending:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		pending := make(chan struct{})
		c.derivedLoading = pending
		c.mu.Unlock()
		defer func() {
			c.mu.Lock()
			c.derivedLoading = nil
			close(pending)
			c.mu.Unlock()
		}()
		break
	}
	index, err := s.buildDerived(ctx, rev, all, req)
	if err != nil || len(index.docs) > 200000 {
		return index, err
	}
	// A replace between reading the generation and building: keep the index
	// under the generation it was built from.
	req.derived = index.generation
	key = searchKey(hash, req)
	c.mu.Lock()
	defer c.mu.Unlock()
	// Another build from that generation may have published it meanwhile.
	if existing := c.derived[key]; existing != nil {
		return existing, nil
	}
	if c.derived == nil {
		c.derived = map[string]*derivedIndex{}
	}
	for len(c.derivedOrder) >= 4 || c.derivedDocs+len(index.docs) > 200000 {
		old := c.derivedOrder[0]
		c.derivedOrder = c.derivedOrder[1:]
		c.derivedDocs -= len(c.derived[old].docs)
		delete(c.derived, old)
	}
	c.derived[key] = index
	c.derivedOrder = append(c.derivedOrder, key)
	c.derivedDocs += len(index.docs)
	return index, nil
}

func (s *Store) buildDerived(ctx context.Context, rev int64, all []record, req QueryRequest) (*derivedIndex, error) {
	input := req
	input.Input = copyInput(req.Input)
	if req.Index.Temporal {
		delete(input.Input, "since")
		delete(input.Input, "until")
	}
	docs := candidates(all, input, "", true)
	index := &derivedIndex{meta: map[string]indexDocument{}, entities: map[string][]string{}, known: map[string]bool{}, words: map[string][]string{}, links: map[string][]Link{}, replacements: map[string][]Link{}}
	metadata, generation, err := s.indexMetadata(ctx, rev, docs, req.derived)
	if err != nil {
		return nil, err
	}
	index.generation = generation
	since, until, err := timeBounds(input.Input)
	if req.Index.Temporal {
		since, until, err = timeBounds(req.Input)
	}
	if err != nil && req.Index.Temporal {
		return nil, err
	}
	for i, r := range docs {
		if i%512 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		meta := metadata[r.ID]
		at := indexTime(r, meta)
		if req.Index.Temporal && (!since.IsZero() && at.Before(since) || !until.IsZero() && at.After(until)) {
			continue
		}
		index.docs = append(index.docs, r)
		index.meta[r.ID] = indexDocument{meta: meta, at: at}
		if req.Index.Entities {
			entities := retrieval.MergeEntities(meta.Entities, meta.Aliases)
			entities = retrieval.ResolveEntities(r.SearchText, entities, retrieval.EntityModeMerge)
			if len(meta.derivedEntities) > 0 || len(meta.derivedAliases) > 0 {
				entities = retrieval.MergeEntities(entities, append(slices.Clone(meta.derivedEntities), meta.derivedAliases...))
			}
			for _, entity := range entities[:min(64, len(entities))] {
				if len(entity) <= 200 {
					index.entities[entity] = append(index.entities[entity], r.ID)
					index.known[entity] = true
				}
			}
		}
		if req.Index.Lexical {
			for word := range retrieval.Tokenize(r.SearchText) {
				index.words[word] = append(index.words[word], r.ID)
			}
		}
	}
	if req.Index.Relations || req.Index.Supersedes {
		index.buildLinks()
	}
	if req.Index.Recency {
		index.recent = index.newest(recallAnchors)
	}
	return index, nil
}

// indexMetadata is the index metadata of docs at rev: the record writer's,
// merged with the derived-metadata rows that apply (derived.go) unless
// generation is "" (no overlay). It returns the overlay generation it read.
func (s *Store) indexMetadata(ctx context.Context, rev int64, docs []record, generation string) (map[string]indexMetadata, string, error) {
	wanted := make(map[string]bool, len(docs))
	for _, r := range docs {
		wanted[r.ID] = true
	}
	// Project only derived-index inputs. In particular no content/history/raw
	// JSON is retained alongside the original lean snapshot.
	rows, err := s.readDB().QueryContext(ctx, `SELECT id,json_object(
'version',json_extract(record_json,'$.version'),
'entities',json_extract(record_json,'$.data.entities'),
'aliases',json_extract(record_json,'$.data.aliases'),
'links',json_extract(record_json,'$.data.links'),
'supersedes',json_extract(record_json,'$.data.mnemonSupersedes'),
'eventAt',json_extract(record_json,'$.data.eventAt'))
FROM record_versions WHERE rev_from<=? AND (rev_to IS NULL OR rev_to>?) ORDER BY seq`, rev, rev)
	if err != nil {
		return nil, "", err
	}
	out := make(map[string]indexMetadata, len(docs))
	for rows.Next() {
		var id, raw string
		if err = rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return nil, "", err
		}
		if wanted[id] {
			var meta indexMetadata
			// Malformed optional metadata cannot gain retrieval authority.
			if json.Unmarshal([]byte(raw), &meta) == nil {
				out[id] = meta
			}
		}
	}
	err = rows.Err()
	rows.Close() // before the next statement: without a runtime, one connection
	if err != nil || generation == "" {
		return out, "", err
	}
	generation, overlay, err := s.derivedRows(ctx, wanted)
	if err != nil {
		return nil, "", err
	}
	for id, list := range overlay {
		meta, ok := out[id]
		if !ok {
			continue // its version is unknown
		}
		for _, row := range list {
			if row.Version >= 1 && row.Version == meta.Version {
				meta.merge(row)
			}
		}
		out[id] = meta
	}
	return out, generation, nil
}

func (index *derivedIndex) buildLinks() {
	docs := make(map[string]record, len(index.docs))
	for _, r := range index.docs {
		docs[r.ID] = r
	}
	for _, source := range index.docs {
		meta := index.meta[source.ID].meta
		links := append([]Link(nil), meta.Links...)
		if meta.Supersedes != nil {
			// Only the existing reserved revision channel supplies authority.
			// General link proposals remain retrieval hints, even if they say
			// "supersedes". The Source SDK protects reserved revision fields.
			links = append([]Link{{ID: meta.Supersedes.ID, Version: meta.Supersedes.Version, Type: "supersedes", Weight: 1, authoritative: true}}, links...)
		}
		for _, link := range links[:min(50, len(links))] {
			target, ok := docs[link.ID]
			if !ok || link.ID == source.ID || link.Version < 1 || link.Version != index.meta[link.ID].meta.Version || link.Weight <= 0 || link.Weight > 1 {
				continue
			}
			if source.Scope != target.Scope || !same(source.WorkspaceID, target.WorkspaceID) || !same(source.SessionID, target.SessionID) {
				continue
			}
			if validLinkType(link.Type) {
				index.links[source.ID] = append(index.links[source.ID], link)
				reverse := Link{ID: source.ID, Version: index.meta[source.ID].meta.Version, Type: link.Type, Weight: link.Weight}
				if link.Type == "supersedes" {
					index.replacements[link.ID] = append(index.replacements[link.ID], reverse)
				}
				if link.Type == "entity" || link.Type == "semantic" {
					index.links[link.ID] = append(index.links[link.ID], reverse)
				}
			}
		}
	}
}

func validLinkType(kind string) bool {
	switch kind {
	case "entity", "semantic", "temporal", "causal", "supersedes":
		return true
	default:
		return false
	}
}
