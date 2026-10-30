package search

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Keep this list small and explicit: question forms, not translated bags of
// generic words such as "time" or "about". No language identification is needed;
// all matching forms contribute, and contradictory intents fail to GENERAL.
var multilingualQuestionPatterns = []struct {
	intent  Intent
	pattern *regexp.Regexp
}{
	// Hindi (Devanagari). Bare क्या also starts yes/no questions, so require a copula.
	{IntentWhy, intentWords(`क्यों|क्यूँ|किसलिए`)},
	{IntentWhen, intentWords(`कब`)},
	{IntentEntity, intentWords(`(?:क्या|कौन) (?:है|हैं)`)},
	// Spanish. Accept both precomposed and decomposed accents, without removing them.
	{IntentWhy, intentWords(`por qu(?:é|e\x{0301})`)},
	{IntentWhen, intentWords(`cu(?:á|a\x{0301})ndo`)},
	{IntentEntity, intentWords(`qu(?:é|e\x{0301}) es|qui(?:é|e\x{0301})n es`)},
	// Modern Standard Arabic. Common vowel marks and tatweel are removed below.
	{IntentWhy, intentWords(`لماذا`)},
	{IntentWhen, intentWords(`متى`)},
	{IntentEntity, intentWords(`(?:ما|من) (?:هو|هي)`)},
	// French. Apostrophes within words are not quotation delimiters.
	{IntentWhy, intentWords(`pourquoi`)},
	{IntentWhen, intentWords(`quand`)},
	{IntentEntity, intentWords(`qu['’]est(?:-| )ce que|qui est|c['’]est quoi`)},
	// Bengali. A final কী/কি/কে asks for an entity; mid-sentence কি can be yes/no.
	{IntentWhy, intentWords(`কেন`)},
	{IntentWhen, intentWords(`কখন|কবে`)},
	{IntentEntity, intentWords(`(?:কী|কি|কে)[\s\p{Z}]*[?？]?$`)},
	// Portuguese (European and Brazilian shared forms).
	{IntentWhy, intentWords(`por qu(?:e|ê|e\x{0302})`)},
	{IntentWhen, intentWords(`quando`)},
	{IntentEntity, intentWords(`(?:o que|quem) (?:é|e\x{0301})`)},
	// Indonesian (Latin script).
	{IntentWhy, intentWords(`mengapa|kenapa`)},
	{IntentWhen, intentWords(`kapan`)},
	{IntentEntity, intentWords(`(?:apa|siapa) itu`)},
	// Russian (Cyrillic).
	{IntentWhy, intentWords(`почему|зачем`)},
	{IntentWhen, intentWords(`когда`)},
	{IntentEntity, intentWords(`что такое|кто (?:такой|такая|такие)`)},
	// German (Latin script).
	{IntentWhy, intentWords(`warum|wieso|weshalb`)},
	{IntentWhen, intentWords(`wann`)},
	{IntentEntity, intentWords(`(?:was|wer) (?:ist|sind)`)},
}

// Go's \b only understands ASCII. Marks and joiners must stay attached to words
// in Indic/Arabic text; Unicode letters and numbers also prevent cross-script
// substring matches such as "почемуx", "ékapan", or "why中文".
const intentWordChars = `\p{L}\p{M}\p{N}_\x{200c}\x{200d}`

func intentWords(pattern string) *regexp.Regexp {
	pattern = strings.ReplaceAll(pattern, " ", `[\s\p{Z}]+`)
	return regexp.MustCompile(`(?i)(^|[^` + intentWordChars + `])(?:` + pattern + `)($|[^` + intentWordChars + `])`)
}

// Ignore paired quoted/code spans in the new cues. A single quote preceded by a
// letter is an apostrophe (qu'est-ce), not an opening quote. Legacy-only queries
// retain their historical handling of quotes and keyword counts.
var quotedIntentText = regexp.MustCompile(`(?s)"[^"]*"|“[^”]*”|«[^»]*»|` + "`[^`]*`" + `|(^|[^` + intentWordChars + `])'[^']*'`)

func multilingualQuestionIntent(query string) (Intent, bool) {
	query = strings.TrimSpace(quotedIntentText.ReplaceAllString(query, " "))
	query = strings.Map(func(r rune) rune {
		if r == '\u0640' || (r >= '\u064b' && r <= '\u0652') || r == '\u0670' {
			return -1 // Arabic tatweel, harakat, and superscript alif.
		}
		return r
	}, query)
	intent := IntentGeneral
	for _, cue := range multilingualQuestionPatterns {
		if !cue.pattern.MatchString(query) {
			continue
		}
		if intent != IntentGeneral && intent != cue.intent {
			return IntentGeneral, true
		}
		intent = cue.intent
	}
	return intent, false
}

// Preserve legacy scoring, while checking Unicode boundaries for the English
// alternatives. Chinese cues intentionally match without spaces. Checking the
// surrounding runes does not consume separators between repeated keywords.
func legacyIntentScore(pattern *regexp.Regexp, query string) int {
	score := 0
	for offset := 0; offset < len(query); {
		span := pattern.FindStringIndex(query[offset:])
		if span == nil {
			break
		}
		start, end := offset+span[0], offset+span[1]
		first, size := utf8.DecodeRuneInString(query[start:end])
		offset = end
		if unicode.Is(unicode.Han, first) {
			score++
			continue
		}
		before, _ := utf8.DecodeLastRuneInString(query[:start])
		after, _ := utf8.DecodeRuneInString(query[end:])
		if !intentWordRune(before) && !intentWordRune(after) {
			score++
			continue
		}
		// An invalid compound can contain a later valid cue: in "retell me
		// about", rejecting "tell me about" must not consume the word "about".
		offset = start + size
	}
	return score
}

func intentWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsNumber(r) ||
		r == '_' || r == '\u200c' || r == '\u200d'
}
