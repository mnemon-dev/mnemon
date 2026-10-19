package retrieval

import "regexp"

var causalPattern = regexp.MustCompile(
	`(?i)\b(because|therefore|due to|caused by|as a result|decided to|` +
		`chosen because|so that|in order to|leads to|results in)\b|` +
		`(因为|所以|由于|导致|因此|决定|为了|以便)`)

// CausalSignal preserves native Memory's candidate heuristic. It is a search
// hint, not proof of causality; raw-record builders ask a decision model next.
func CausalSignal(text string) string  { return causalPattern.FindString(text) }
func HasCausalSignal(text string) bool { return causalPattern.MatchString(text) }

// TokenOverlap is the native |intersection| / max(|a|, |b|) candidate signal.
func TokenOverlap(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	small, big := a, b
	if len(a) > len(b) {
		small, big = b, a
	}
	count := 0
	for word := range small {
		if big[word] {
			count++
		}
	}
	return float64(count) / float64(max(len(a), len(b)))
}
