package memory

import (
	"strings"
	"unicode"

	"github.com/mnemon-dev/mnemon/internal/memory/search"
)

const (
	maxBriefQueryBytes = 4096
	maxBriefQueryTerms = 64
	maxBriefMatchWidth = 4096
)

func makeBriefExcerpt(content string, maxChars int) string {
	return makeQueryBriefExcerpt(content, "", maxChars)
}

// makeQueryBriefExcerpt changes only the discovery projection. It selects one
// continuous passage, retaining nearby context rather than synthesizing facts.
func makeQueryBriefExcerpt(content, query string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}
	runes := []rune(strings.Join(strings.Fields(content), " "))
	if len(runes) <= maxChars {
		return string(runes)
	}
	if maxChars <= 2 || len(query) > maxBriefQueryBytes {
		return briefPrefix(runes, maxChars)
	}
	terms := search.Tokenize(query)
	if len(terms) == 0 || len(terms) > maxBriefQueryTerms {
		return briefPrefix(runes, maxChars)
	}
	window := briefMatchWindow{terms: terms, width: min(maxChars-2, maxBriefMatchWidth), counts: make(map[string]int)}
	window.scan(runes)
	if window.bestCount == 0 {
		return briefPrefix(runes, maxChars)
	}
	return renderBriefMatch(runes, window.best, maxChars)
}

func briefPrefix(runes []rune, maxChars int) string {
	return strings.TrimSpace(string(runes[:maxChars-1])) + "…"
}

type briefMatch struct {
	start, end int
	term       string
}

type briefMatchWindow struct {
	terms     map[string]bool
	width     int
	matches   []briefMatch
	counts    map[string]int
	best      briefMatch
	bestCount int
}

// scan recognizes the same word and Han-bigram units used by query Tokenize.
// The active window holds at most width matches, independent of repeated hits.
func (w *briefMatchWindow) scan(runes []rune) {
	for i := 0; i < len(runes); {
		if unicode.Is(unicode.Han, runes[i]) {
			if i+1 < len(runes) && unicode.Is(unicode.Han, runes[i+1]) {
				w.observe(runes, i, i+2)
			} else if i == 0 || !unicode.Is(unicode.Han, runes[i-1]) {
				w.observe(runes, i, i+1)
			}
			i++
			continue
		}
		if !unicode.IsLetter(runes[i]) && !unicode.IsDigit(runes[i]) {
			i++
			continue
		}
		start := i
		for i < len(runes) && !unicode.Is(unicode.Han, runes[i]) && (unicode.IsLetter(runes[i]) || unicode.IsDigit(runes[i])) {
			i++
		}
		w.observe(runes, start, i)
	}
}

func (w *briefMatchWindow) observe(runes []rune, start, end int) {
	if end-start > w.width {
		return
	}
	term := strings.ToLower(string(runes[start:end]))
	if !w.terms[term] {
		return
	}
	w.matches = append(w.matches, briefMatch{start: start, end: end, term: term})
	w.counts[term]++
	for end-w.matches[0].start > w.width || w.counts[w.matches[0].term] > 1 {
		first := w.matches[0].term
		w.counts[first]--
		if w.counts[first] == 0 {
			delete(w.counts, first)
		}
		w.matches = w.matches[1:]
	}
	span := briefMatch{start: w.matches[0].start, end: end}
	if count := len(w.counts); count > w.bestCount || (count == w.bestCount && span.end-span.start < w.best.end-w.best.start) {
		w.best, w.bestCount = span, count
	}
}

func renderBriefMatch(runes []rune, match briefMatch, maxChars int) string {
	// Reserve both ellipses; spend most spare space after the matches so nearby
	// values, dates and qualifications stay visible. Keep whole adjacent words.
	budget := maxChars - 2
	start := max(0, match.start-(budget-(match.end-match.start))/3)
	minStart := max(0, match.end-budget)
	aligned := start
	for aligned > minStart && !briefWordBoundary(runes, aligned) {
		aligned--
	}
	if briefWordBoundary(runes, aligned) {
		start = aligned
	} else {
		for start < match.start && !briefWordBoundary(runes, start) {
			start++
		}
	}
	end := min(len(runes), start+budget)
	for end > match.end && !briefWordBoundary(runes, end) {
		end--
	}
	excerpt := strings.TrimSpace(string(runes[start:end]))
	if start > 0 {
		excerpt = "…" + excerpt
	}
	if end < len(runes) {
		excerpt += "…"
	}
	return excerpt
}

func briefWordBoundary(runes []rune, index int) bool {
	return index == 0 || index == len(runes) ||
		unicode.IsSpace(runes[index-1]) || unicode.IsSpace(runes[index]) ||
		unicode.Is(unicode.Han, runes[index-1]) || unicode.Is(unicode.Han, runes[index])
}
