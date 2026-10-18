package records

import (
	"cmp"
	"context"
	"slices"
	"strings"
)

// Retain complete terms for at most 150k immutable documents, prioritizing the
// longest documents. The unindexed remainder stays in SQLite; candidate-local
// df and length totals still include every document, including that remainder.
type preparedLexical struct {
	owner     *querySelection
	index     *lexicalIndex
	remainder *querySelection
	positions []int
}

type tokenStatistic struct {
	df       int
	last     int64
	postings []posting
}

func prepareLexicalSubset(v *querySelection) (*preparedLexical, []int) {
	if v.prefixLimit == 0 || !v.ready {
		return nil, nil
	}
	order := make([]int, len(v.docs))
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(i, j int) int {
		if v.docs[i].TokenCount == v.docs[j].TokenCount {
			return cmp.Compare(i, j)
		}
		return cmp.Compare(v.docs[j].TokenCount, v.docs[i].TokenCount)
	})
	positions := make([]int, len(v.docs))
	for i := range positions {
		positions[i] = -1
	}
	index := &lexicalIndex{docs: make([]lexicalDoc, v.prefixLimit), n: len(v.docs), total: v.total, postings: map[string][]posting{}}
	for pos, original := range order[:v.prefixLimit] {
		r := &v.docs[original]
		positions[original] = pos
		index.docs[pos] = lexicalDoc{id: r.ID, length: r.TokenCount}
	}
	tail := make([]record, 0, len(v.docs)-v.prefixLimit)
	for i, r := range v.docs {
		if positions[i] < 0 {
			tail = append(tail, r)
		}
	}
	return &preparedLexical{owner: v, index: index, remainder: newSelection(tail), positions: positions}, positions
}

func (s *Store) buildStatistics(ctx context.Context, v *querySelection) (map[string]int, *preparedLexical, error) {
	prepared, positions := prepareLexicalSubset(v)
	terms := map[string]*tokenStatistic{}
	err := s.selectionTokens(ctx, v, "", func(version int64, text string) {
		pos := -1
		if prepared != nil {
			pos = positions[v.wanted[version]]
		}
		for token := range strings.SplitSeq(text, " ") {
			if token == "" {
				continue
			}
			stat := terms[token]
			if stat == nil {
				stat = &tokenStatistic{}
				// Intern once: later updates mutate the value, so map keys do
				// not retain an entire tokenized document's backing string.
				terms[strings.Clone(token)] = stat
			}
			if stat.last != version {
				stat.df++
				stat.last = version
			}
			if pos >= 0 {
				last := len(stat.postings) - 1
				if last >= 0 && stat.postings[last].doc == uint32(pos) {
					stat.postings[last].count++
				} else {
					stat.postings = append(stat.postings, posting{uint32(pos), 1})
				}
			}
		}
	})
	if err != nil {
		return nil, nil, err
	}
	df := make(map[string]int, len(terms))
	for token, stat := range terms {
		df[token] = stat.df
		if prepared != nil && len(stat.postings) > 0 {
			slices.SortFunc(stat.postings, func(a, b posting) int { return cmp.Compare(a.doc, b.doc) })
			prepared.index.postings[token] = stat.postings
		}
	}
	if prepared != nil {
		prepared.index.df = df
	}
	return df, prepared, nil
}

// Reuse cached terms for filtered candidates too. Membership is by immutable
// version, never just ID; df/n/average below are local to this candidate set.
func (s *Store) filteredPrepared(ctx context.Context, v *querySelection, query string, p *preparedLexical) (*lexicalIndex, bool, error) {
	base := &lexicalIndex{df: map[string]int{}, postings: map[string][]posting{}}
	slots := map[uint32]int{}
	tail := []record{}
	for _, r := range v.docs {
		original, ok := p.owner.wanted[r.VersionID]
		if !ok || p.positions[original] < 0 {
			tail = append(tail, r)
			continue
		}
		cached := p.positions[original]
		slots[uint32(cached)] = len(base.docs)
		base.docs = append(base.docs, p.index.docs[cached])
	}
	if len(slots) == 0 {
		return s.selectionLexical(ctx, v, query, p)
	}
	seen := map[string]bool{}
	for _, token := range Tokens(query) {
		if seen[token] {
			continue
		}
		seen[token] = true
		posts := p.index.postings[token]
		if len(slots) < len(posts)/8 {
			for cached, local := range slots {
				at, ok := slices.BinarySearchFunc(posts, cached, func(a posting, b uint32) int { return cmp.Compare(a.doc, b) })
				if ok {
					base.postings[token] = append(base.postings[token], posting{uint32(local), posts[at].count})
					base.df[token]++
				}
			}
		} else {
			for _, post := range posts {
				if local, ok := slots[post.doc]; ok {
					base.postings[token] = append(base.postings[token], posting{uint32(local), post.count})
					base.df[token]++
				}
			}
		}
	}
	parts := []*lexicalIndex{base}
	if len(tail) > 0 {
		other, ready, err := s.selectionLexical(ctx, newSelection(tail), query, p)
		if err != nil || !ready {
			return other, ready, err
		}
		parts = append(parts, other)
		for token, df := range other.df {
			base.df[token] += df
		}
	}
	return &lexicalIndex{n: len(v.docs), total: v.total, df: base.df, parts: parts}, true, nil
}

func (s *Store) keepPrepared(v *querySelection, prepared *preparedLexical) {
	c := s.cache
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.allSelection != v {
		return
	}
	for c.documents+len(prepared.index.docs) > 150000 && len(c.order) > 0 {
		oldest := c.order[0]
		c.order = c.order[1:]
		c.documents -= len(c.indexes[oldest].docs)
		delete(c.indexes, oldest)
	}
	c.prepared = prepared
}
