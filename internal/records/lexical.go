package records

import (
	"cmp"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"
)

var latin = regexp.MustCompile(`[a-z0-9]+(?:[-_:][a-z0-9]+)*`)
var digits = regexp.MustCompile(`^\d+$`)
var stop = func() map[string]bool {
	words := `about above after again against all also and any are aren't because been before being below between both but can
could did didn't does doesn't doing don't down during each few for from further had has have having her here hers herself him himself his
how into isn't it's its itself just like more most myself not now off once only other our ours ourselves out over own same she should some
such than that that's the their theirs them themselves then there these they this those through too under until very was wasn't were
weren't what what's when where which while who whom why will with would you your yours yourself yourselves really`
	out := map[string]bool{}
	for _, w := range strings.Fields(words) {
		out[w] = true
	}
	return out
}()

func Tokens(text string) []string {
	out := latin.FindAllString(lower(text), -1)
	if out == nil {
		out = []string{}
	}
	runes := []rune(text)
	for start := 0; start < len(runes); {
		if !unicode.Is(unicode.Han, runes[start]) {
			start++
			continue
		}
		end := start + 1
		for end < len(runes) && unicode.Is(unicode.Han, runes[end]) {
			end++
		}
		if end-start == 1 {
			out = append(out, string(runes[start]))
		}
		for i := start; i+1 < end; i++ {
			out = append(out, string(runes[i:i+2]))
		}
		start = end
	}
	return out
}

type termCount struct {
	token string
	count int
}
type lexicalDoc struct {
	id     string
	terms  []termCount
	tf     map[string]int
	length int
}
type lexicalIndex struct {
	docs     []lexicalDoc
	df       map[string]int
	total    int
	n        int
	postings map[string][]posting
	parts    []*lexicalIndex
}
type posting struct{ doc, count uint32 }
type scored struct {
	id    string
	score float64
}

func newLexical(records []record) *lexicalIndex {
	index := &lexicalIndex{df: map[string]int{}, n: len(records)}
	for _, r := range records {
		d := lexicalDoc{id: r.ID, tf: map[string]int{}}
		tokens := Tokens(r.SearchText)
		d.length = len(tokens)
		index.total += len(tokens)
		order := []string{}
		for _, token := range tokens {
			if d.tf[token] == 0 {
				order = append(order, token)
			}
			d.tf[token]++
		}
		for _, token := range order {
			d.terms = append(d.terms, termCount{token, d.tf[token]})
			index.df[token]++
		}
		index.docs = append(index.docs, d)
	}
	return index
}

func (index *lexicalIndex) search(query string) []string {
	terms := []string{}
	seen := map[string]bool{}
	for _, t := range Tokens(query) {
		if !seen[t] {
			terms = append(terms, t)
			seen[t] = true
		}
	}
	n := float64(index.n)
	if len(terms) == 0 {
		return []string{}
	}
	average := 1.0
	if n > 0 {
		average = float64(index.total) / n
	}
	if index.postings != nil {
		return rank(index.appendPostingScores(nil, terms, n, average, index.df), len(index.docs))
	}
	if index.parts != nil {
		hits := []scored{}
		for _, part := range index.parts {
			hits = part.appendPostingScores(hits, terms, n, average, index.df)
		}
		return rank(hits, len(hits))
	}
	idfs := make([]float64, len(terms))
	for i, token := range terms {
		df := float64(index.df[token])
		idfs[i] = jsLog(1 + (n-df+0.5)/(df+0.5))
	}
	hits := []scored{}
	for _, doc := range index.docs {
		score := 0.0
		for i, token := range terms {
			tf := float64(doc.tf[token])
			if tf == 0 {
				continue
			}
			idf := idfs[i]
			scaled := float64(1.2 * (0.25 + 0.75*float64(doc.length)/average))
			score += 1 * idf * (tf * 2.2) / (tf + scaled)
		}
		if score > 0 {
			hits = append(hits, scored{doc.id, score})
		}
	}
	return rank(hits, len(hits))
}

func (index *lexicalIndex) appendPostingScores(hits []scored, terms []string, n, average float64, dfCounts map[string]int) []scored {
	potential := 0
	for _, token := range terms {
		potential += len(index.postings[token])
	}
	if potential == 0 {
		return hits
	}
	if potential < len(index.docs)/8 {
		return index.appendSparseScores(hits, terms, n, average, dfCounts, potential)
	}
	scores := make([]float64, len(index.docs))
	scaled := make([]float64, len(index.docs))
	for i, doc := range index.docs {
		scaled[i] = float64(1.2 * (0.25 + 0.75*float64(doc.length)/average))
	}
	// Query order is the original TS Map insertion order. Only documents with
	// a nonzero tf are visited; each document's additions retain that order.
	for _, token := range terms {
		df := float64(dfCounts[token])
		idf := jsLog(1 + (n-df+0.5)/(df+0.5))
		for _, p := range index.postings[token] {
			tf := float64(p.count)
			scores[p.doc] += 1 * idf * (tf * 2.2) / (tf + scaled[p.doc])
		}
	}
	for i, score := range scores {
		if score > 0 {
			hits = append(hits, scored{index.docs[i].id, score})
		}
	}
	return hits
}

func (index *lexicalIndex) appendSparseScores(hits []scored, terms []string, n, average float64, dfCounts map[string]int, capacity int) []scored {
	scores := make(map[uint32]float64, capacity)
	for _, token := range terms {
		df := float64(dfCounts[token])
		idf := jsLog(1 + (n-df+0.5)/(df+0.5))
		for _, p := range index.postings[token] {
			tf := float64(p.count)
			scaled := float64(1.2 * (0.25 + 0.75*float64(index.docs[p.doc].length)/average))
			scores[p.doc] += 1 * idf * (tf * 2.2) / (tf + scaled)
		}
	}
	// Each score was accumulated in query order. Unique IDs give the final
	// rank a total order, independent of this integer-map enumeration order.
	for pos, score := range scores {
		if score > 0 {
			hits = append(hits, scored{index.docs[pos].id, score})
		}
	}
	return hits
}

func rank(hits []scored, limit int) []string {
	// NaN is not a total order: preserve the original stable-sort behavior for
	// malformed/incompatible vector dimensions rather than changing its ties.
	finiteOrder := true
	for _, hit := range hits {
		if math.IsNaN(hit.score) {
			finiteOrder = false
			break
		}
	}
	if finiteOrder {
		if limit > 0 && len(hits) > limit {
			hits = topScores(hits, limit)
		}
		slices.SortFunc(hits, compareScore)
	} else {
		sort.SliceStable(hits, func(i, j int) bool {
			return hits[i].score == hits[j].score && hits[i].id < hits[j].id || hits[i].score > hits[j].score
		})
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.id
	}
	return out
}

func compareScore(a, b scored) int {
	if a.score == b.score {
		return cmp.Compare(a.id, b.id)
	}
	if a.score > b.score {
		return -1
	}
	return 1
}

// An exact worst-first heap retains only k scores, with the identical score/id
// total order used by the final sort. No approximate vector search or rounding.
func topScores(hits []scored, k int) []scored {
	keep := hits[:k]
	for i := k/2 - 1; i >= 0; i-- {
		scoreDown(keep, i)
	}
	for _, hit := range hits[k:] {
		if compareScore(hit, keep[0]) < 0 {
			keep[0] = hit
			scoreDown(keep, 0)
		}
	}
	return keep
}

func scoreDown(hits []scored, root int) {
	for {
		child := 2*root + 1
		if child >= len(hits) {
			return
		}
		if child+1 < len(hits) && compareScore(hits[child+1], hits[child]) > 0 {
			child++
		}
		if compareScore(hits[root], hits[child]) >= 0 {
			return
		}
		hits[root], hits[child] = hits[child], hits[root]
		root = child
	}
}

func (index *lexicalIndex) feedback(ids []string, query string) []string {
	exclude := map[string]bool{}
	for _, token := range Tokens(query) {
		exclude[token] = true
	}
	docs := map[string]lexicalDoc{}
	for _, d := range index.docs {
		docs[d.id] = d
	}
	scores := map[string]float64{}
	order := []string{}
	for _, id := range ids {
		for _, term := range docs[id].terms {
			token := term.token
			if !feedbackToken(token, exclude) {
				continue
			}
			if _, ok := scores[token]; !ok {
				order = append(order, token)
			}
			scores[token] += float64(float64(term.count) * jsLog(1+float64(index.n)/(float64(index.df[token])+0.5)))
		}
	}
	hits := make([]scored, len(order))
	for i, t := range order {
		hits[i] = scored{t, scores[t]}
	}
	return rank(hits, 10)
}

func feedbackToken(token string, exclude map[string]bool) bool {
	return !exclude[token] && !stop[token] && !digits.MatchString(token) &&
		(strings.ContainsFunc(token, func(r rune) bool { return unicode.Is(unicode.Han, r) }) || len(token) >= 3)
}

func fuse(rankings ...[]string) []string {
	scores := map[string]float64{}
	ids := []string{}
	for _, ranking := range rankings {
		for i, id := range ranking {
			if i >= 200 {
				break
			}
			if _, ok := scores[id]; !ok {
				ids = append(ids, id)
			}
			scores[id] += 1 / (60 + float64(i))
		}
	}
	hits := make([]scored, len(ids))
	for i, id := range ids {
		hits[i] = scored{id, scores[id]}
	}
	return rank(hits, len(hits))
}
