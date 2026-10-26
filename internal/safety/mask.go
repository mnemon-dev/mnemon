// Package safety holds the privacy checks a memory service runs on what it
// hands out, as plain engineering: patterns, checksums, no model.
package safety

import (
	"regexp"
	"strings"
)

// Kinds of values Mask hides, as its counts name them.
const (
	KindCard     = "card"     // a payment card number that passes Luhn's check
	KindSSN      = "ssn"      // a US social security number in its dashed form
	KindIBAN     = "iban"     // an IBAN whose check digits hold (ISO 13616)
	KindSecret   = "secret"   // an API key, token or private key of a known form
	KindPassword = "password" // the value after "password", "passcode" or "PIN"
)

var (
	// A card: 13 to 19 digits, in groups split by single spaces or dashes.
	cardPattern = regexp.MustCompile(`\b\d(?:[ -]?\d){12,18}\b`)
	ssnPattern  = regexp.MustCompile(`\b(\d{3})-(\d{2})-(\d{4})\b`)
	ibanPattern = regexp.MustCompile(`\b[A-Z]{2}\d{2}(?: ?[A-Z0-9]{4}){2,7}(?: ?[A-Z0-9]{1,4})?\b`)
	// Secrets of a known form: OpenAI- and DeepSeek-style keys, AWS access
	// key ids, GitHub and Slack tokens, JSON web tokens, and private keys.
	secretPattern = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}|\bAKIA[0-9A-Z]{16}\b|\bgh[pousr]_[A-Za-z0-9]{36,}\b|\bxox[abprs]-\d+-[A-Za-z0-9-]{10,}|\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}|-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`)
	// A password said outright: the word, then "is", ":" or "=", then the
	// value, one token of four or more characters that holds a digit or a
	// symbol other than "_" and opens with no bracket (a word such as
	// "strong", a variable such as hashed_password, or a template's
	// "[your password]", is not a password).
	passwordPattern = regexp.MustCompile(`(?i)\b(password|passwd|passcode|pin(?: code)?)(\s*(?:is|:|=)\s*)([^\s"'.,;\[\]<>{}()]{4,})`)
	passwordValue   = regexp.MustCompile(`[0-9!@#$%^&*+=?~-]`)
)

// Mask returns text with every sensitive value of a kind above hidden, and
// how many of each kind it hid. A hidden value keeps what a reply may need,
// its kind and its last four characters ("[card ending 4242]"), and loses
// what could be misused. Text without such a value is returned as it is.
func Mask(text string) (string, map[string]int) {
	counts := map[string]int{}
	text = secretPattern.ReplaceAllStringFunc(text, func(string) string {
		counts[KindSecret]++
		return "[secret]"
	})
	text = passwordPattern.ReplaceAllStringFunc(text, func(match string) string {
		parts := passwordPattern.FindStringSubmatch(match)
		if !passwordValue.MatchString(parts[3]) {
			return match
		}
		counts[KindPassword]++
		return parts[1] + parts[2] + "[hidden]"
	})
	text = ibanPattern.ReplaceAllStringFunc(text, func(match string) string {
		compact := strings.ReplaceAll(match, " ", "")
		if !ibanValid(compact) {
			return match
		}
		counts[KindIBAN]++
		return "[IBAN ending " + compact[len(compact)-4:] + "]"
	})
	text = ssnPattern.ReplaceAllStringFunc(text, func(match string) string {
		parts := ssnPattern.FindStringSubmatch(match)
		area, group, serial := parts[1], parts[2], parts[3]
		if area == "000" || area == "666" || area[0] == '9' || group == "00" || serial == "0000" {
			return match
		}
		counts[KindSSN]++
		return "[SSN ending " + serial + "]"
	})
	text = cardPattern.ReplaceAllStringFunc(text, func(match string) string {
		digits := strings.NewReplacer(" ", "", "-", "").Replace(match)
		if !luhnValid(digits) || !consistentGroups(match) {
			return match
		}
		counts[KindCard]++
		return "[card ending " + digits[len(digits)-4:] + "]"
	})
	return text, counts
}

// luhnValid reports whether digits pass the Luhn check.
func luhnValid(digits string) bool {
	sum := 0
	for i := len(digits) - 1; i >= 0; i-- {
		d := int(digits[i] - '0')
		if (len(digits)-1-i)%2 == 1 {
			if d *= 2; d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return sum%10 == 0
}

// consistentGroups reports whether a card's digits are split one way: by
// spaces, by dashes, or not at all. A phone number with a dash and a space
// is not a card.
func consistentGroups(match string) bool {
	return !(strings.Contains(match, " ") && strings.Contains(match, "-"))
}

// ibanValid reports whether an IBAN without spaces holds its check digits:
// the country code and check digits moved to the end, letters as numbers
// (A=10 … Z=35), the number modulo 97 is 1.
func ibanValid(iban string) bool {
	if len(iban) < 15 || len(iban) > 34 {
		return false
	}
	rearranged := iban[4:] + iban[:4]
	remainder := 0
	for _, c := range rearranged {
		switch {
		case c >= '0' && c <= '9':
			remainder = (remainder*10 + int(c-'0')) % 97
		case c >= 'A' && c <= 'Z':
			remainder = (remainder*100 + int(c-'A'+10)) % 97
		default:
			return false
		}
	}
	return remainder == 1
}
