package records

import (
	"context"
	"encoding/json"

	"github.com/mnemon-dev/mnemon/internal/retrieval"
)

// One builder and at most 150k additional indexed document references across
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
	if err != nil || len(index.docs) > 150000 {
		return index, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.derived == nil {
		c.derived = map[string]*derivedIndex{}
	}
	for len(c.derivedOrder) >= 4 || c.derivedDocs+len(index.docs) > 150000 {
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
	metadata, err := s.indexMetadata(ctx, rev, docs)
	if err != nil {
		return nil, err
	}
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
	return index, nil
}

func (s *Store) indexMetadata(ctx context.Context, rev int64, docs []record) (map[string]indexMetadata, error) {
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
'eventAt',json_extract(record_json,'$.data.eventAt'))
FROM record_versions WHERE rev_from<=? AND (rev_to IS NULL OR rev_to>?) ORDER BY seq`, rev, rev)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]indexMetadata, len(docs))
	for rows.Next() {
		var id, raw string
		if err = rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		if wanted[id] {
			var meta indexMetadata
			// Malformed optional metadata cannot gain retrieval authority.
			if json.Unmarshal([]byte(raw), &meta) == nil {
				out[id] = meta
			}
		}
	}
	return out, rows.Err()
}

func (index *derivedIndex) buildLinks() {
	docs := make(map[string]record, len(index.docs))
	for _, r := range index.docs {
		docs[r.ID] = r
	}
	for _, source := range index.docs {
		links := index.meta[source.ID].meta.Links
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
