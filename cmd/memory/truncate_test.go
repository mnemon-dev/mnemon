package memory

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateRunes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"ascii under limit", "hello", 10, "hello"},
		{"ascii at limit", "hello", 5, "hello"},
		{"ascii over limit", "hello", 3, "hel"},
		{"cjk under limit", "飞书课程笔记", 10, "飞书课程笔记"},
		{"cjk over limit", "飞书课程笔记", 3, "飞书课"},
		{"mixed ascii and cjk", "q=飞书 CS336", 6, "q=飞书 C"},
		{"zero limit", "飞书", 0, ""},
		{"negative limit", "飞书", -1, ""},
		{"empty", "", 3, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := truncateRunes(tt.in, tt.n); got != tt.want {
				t.Fatalf("truncateRunes(%q, %d) = %q, want %q", tt.in, tt.n, got, tt.want)
			}
		})
	}
}

// Truncated detail previews must stay valid UTF-8: byte-based slicing used to
// split multi-byte characters and corrupt mnemon log output on CJK text.
func TestTruncateRunesStaysValidUTF8(t *testing.T) {
	detail := "q=" + strings.Repeat("用户（Ming）", 20)
	if !utf8.ValidString(detail) {
		t.Fatal("test input must be valid UTF-8")
	}
	// The ASCII prefix positions byte 57 inside a multi-byte character, so the
	// byte-based slicing that used to build log previews fails this input too.
	if utf8.ValidString(string([]byte(detail)[:57])) {
		t.Fatal("test input must make byte-based slicing split a multi-byte character")
	}
	got := truncateRunes(detail, 57) + "..."
	if !utf8.ValidString(got) {
		t.Fatalf("truncated preview is not valid UTF-8: %q", got)
	}
}
