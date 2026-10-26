package memory

// truncateRunes shortens s to at most n runes so multi-byte UTF-8
// characters are never split mid-sequence in display output.
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
