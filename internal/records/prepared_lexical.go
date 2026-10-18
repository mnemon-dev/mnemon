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
	return &preparedLexical{owner: v, index: index, remainder: newSelection(tail)}, positions
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
			prepared.index.postings[token] = stat.postings
		}
	}
	if prepared != nil {
		prepared.index.df = df
	}
	return df, prepared, nil
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
