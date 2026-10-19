package memoryagent

import (
	"context"
	"math"
	"slices"
	"strings"

	"github.com/mnemon-dev/mnemon/internal/records"
	"github.com/mnemon-dev/mnemon/internal/retrieval"
)

type relationSnapshot struct {
	revision  string
	docs      []records.RelationDocument
	byID      map[string]int
	groups    map[string][]int
	positions []int
	words     []map[string]bool
	frequency map[string]int
	vectors   [][]float32
	missing   int
}

func (a *Agent) relationSnapshot(ctx context.Context, revision string, scope records.Scope, building bool) (*relationSnapshot, error) {
	hash, docs, err := a.store.RelationDocuments(ctx, revision, scope, building)
	if err != nil {
		return nil, err
	}
	s := &relationSnapshot{revision: hash, docs: docs, byID: map[string]int{}, groups: map[string][]int{}, frequency: map[string]int{}}
	for i, d := range docs {
		s.byID[d.ID] = i
		s.positions = append(s.positions, -1)
		if building {
			s.words = append(s.words, retrieval.Tokenize(d.Text))
			for _, entity := range d.Entities {
				s.frequency[entity]++
			}
		}
		if d.Group != "" {
			s.groups[d.Group] = append(s.groups[d.Group], i)
		}
	}
	s.orderGroups()
	if building && a.config.Relations.Semantic && a.config.Embedding != nil {
		for start := 0; start < len(docs); start += 512 {
			texts := []string{}
			for _, d := range docs[start:min(start+512, len(docs))] {
				texts = append(texts, d.VectorText)
			}
			values, err := a.store.DerivedVectors(ctx, texts, *a.config.Embedding, false)
			if err != nil {
				return nil, err
			}
			for _, v := range values {
				if len(v) == 0 {
					s.missing++
				}
			}
			s.vectors = append(s.vectors, values...)
		}
	}
	return s, nil
}

func (s *relationSnapshot) ref(at int) relationRef {
	d := s.docs[at]
	return relationRef{ID: d.ID, Version: d.Version}
}

func (s *relationSnapshot) adjacent(at int) []int {
	group := s.groups[s.docs[at].Group]
	pos := s.positions[at]
	if pos < 0 {
		return nil
	}
	out := []int{}
	for delta := 1; delta <= 2; delta++ {
		if pos-delta >= 0 {
			out = append(out, group[pos-delta])
		}
		if pos+delta < len(group) {
			out = append(out, group[pos+delta])
		}
	}
	return out
}

// Native primitives propose a bounded short list. Only the decision model can
// admit semantic/entity/causal edges; co-occurrence is not a factual assertion.
func (s *relationSnapshot) candidates(at int, cfg *RelationConfig) []int {
	if !cfg.scored() {
		return s.adjacent(at)
	}
	type hit struct {
		at    int
		score float64
	}
	source := s.docs[at]
	hits := []hit{}
	near := s.adjacent(at)
	for i, other := range s.docs {
		// As on native writes/Jev-Mem, propose links to already written versions.
		// Backfill does not judge the same unordered pair twice. This is not the
		// conversation ordering used by structural adjacency.
		if i == at || other.Version >= source.Version || source.Boundary != other.Boundary {
			continue
		}
		score := s.candidateScore(at, i, cfg, near)
		if score > 0 {
			hits = append(hits, hit{i, score})
		}
	}
	slices.SortFunc(hits, func(a, b hit) int {
		if order := probabilityOrder(a.score, b.score); order != 0 {
			return order
		}
		return strings.Compare(s.docs[a.at].ID, s.docs[b.at].ID)
	})
	out := []int{}
	for _, hit := range hits[:min(8, len(hits))] {
		out = append(out, hit.at)
	}
	return out
}

func (s *relationSnapshot) direction(root, context string) string {
	i, ok := s.byID[root]
	j, found := s.byID[context]
	if !ok || !found || s.docs[i].Group == "" || s.docs[i].Group != s.docs[j].Group {
		return "unrelated"
	}
	if s.positions[j] < s.positions[i] {
		return "before"
	}
	return "after"
}

func (s *relationSnapshot) orderGroups() {
	docs := s.docs
	for group, indices := range s.groups {
		slices.SortFunc(indices, func(i, j int) int {
			a, b := docs[i], docs[j]
			if a.Position != b.Position {
				if a.Position < b.Position {
					return -1
				}
				return 1
			}
			if a.Part != b.Part {
				if a.Part < b.Part {
					return -1
				}
				return 1
			}
			return strings.Compare(a.ID, b.ID)
		})
		// Ambiguous positions cannot acquire structural adjacency authority.
		for i := 1; i < len(indices); i++ {
			a, b := docs[indices[i-1]], docs[indices[i]]
			if a.Position == b.Position && a.Part == b.Part {
				delete(s.groups, group)
				break
			}
		}
		if s.groups[group] != nil {
			for position, at := range indices {
				s.positions[at] = position
			}
		}
	}
}

func (s *relationSnapshot) candidateScore(at, i int, cfg *RelationConfig, near []int) float64 {
	source, other := s.docs[at], s.docs[i]
	score := retrieval.TokenOverlap(s.words[at], s.words[i])
	if cfg.Adjacent && slices.Contains(near, i) {
		score += 4
	}
	if cfg.Entity {
		for _, e := range source.Entities {
			if s.frequency[e] <= max(20, len(s.docs)/100) && slices.Contains(other.Entities, e) {
				score += 2
			}
		}
	}
	if cfg.Causal && (retrieval.HasCausalSignal(source.Text) || retrieval.HasCausalSignal(other.Text)) && score >= 0.15 {
		score += 1
	}
	if cfg.Semantic && len(s.vectors) > i && len(s.vectors[at]) != 0 && len(s.vectors[at]) == len(s.vectors[i]) {
		value := records.VectorDot(s.vectors[at], s.vectors[i])
		if !math.IsNaN(value) && value >= 0.4 {
			score += value * 3
		}
	}
	return score
}
