package retrieval

import (
	"strings"
	"unicode"
)

// stopwords contains common English words that are filtered from token sets
// to improve similarity precision (MAGMA compliance: P7).
var stopwords = map[string]bool{
	"a": true, "an": true, "the": true, "is": true, "are": true, "was": true,
	"were": true, "be": true, "been": true, "being": true, "have": true,
	"has": true, "had": true, "do": true, "does": true, "did": true,
	"will": true, "would": true, "could": true, "should": true, "may": true,
	"might": true, "shall": true, "can": true, "to": true, "of": true,
	"in": true, "for": true, "on": true, "with": true, "at": true, "by": true,
	"from": true, "as": true, "into": true, "about": true, "that": true,
	"this": true, "it": true, "its": true, "or": true, "and": true, "but": true,
	"if": true, "not": true, "no": true, "so": true, "up": true, "out": true,
	"than": true, "then": true, "too": true, "very": true, "just": true,
	"also": true, "more": true, "some": true, "any": true, "all": true,
	"each": true, "i": true, "me": true, "my": true, "we": true, "you": true,
	"your": true, "he": true, "she": true, "they": true, "them": true,
	"his": true, "her": true, "our": true, "their": true, "what": true,
	"which": true, "who": true, "how": true, "when": true, "where": true,
}

// Tokenize splits text into lowercase tokens with stopword filtering.
// English words split on whitespace/punctuation; CJK characters generate
// character bigrams. Common English stopwords are excluded.
func Tokenize(text string) map[string]bool {
	tokens := make(map[string]bool)
	AddTokens(tokens, text)
	return tokens
}

// addTokens merges one text into an existing token set. Each field is processed
// separately so CJK bigrams never span content/tag/entity boundaries.
func AddTokens(tokens map[string]bool, text string) {
	text = strings.ToLower(text)

	var word strings.Builder
	var cjkBuf []rune

	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			if word.Len() > 0 {
				w := word.String()
				if !stopwords[w] {
					tokens[w] = true
				}
				word.Reset()
			}
			cjkBuf = append(cjkBuf, r)
		} else {
			if len(cjkBuf) > 0 {
				flushCJK(cjkBuf, tokens)
				cjkBuf = cjkBuf[:0]
			}
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				word.WriteRune(r)
			} else {
				if word.Len() > 0 {
					w := word.String()
					if !stopwords[w] {
						tokens[w] = true
					}
					word.Reset()
				}
			}
		}
	}
	if word.Len() > 0 {
		w := word.String()
		if !stopwords[w] {
			tokens[w] = true
		}
	}
	if len(cjkBuf) > 0 {
		flushCJK(cjkBuf, tokens)
	}
}

func flushCJK(buf []rune, tokens map[string]bool) {
	for j := 0; j < len(buf)-1; j++ {
		tokens[string(buf[j:j+2])] = true
	}
	if len(buf) == 1 {
		tokens[string(buf)] = true
	}
}
