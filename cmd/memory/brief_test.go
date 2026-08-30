package memory

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestMakeBriefExcerptNormalizesAndTruncatesByRune(t *testing.T) {
	got := makeBriefExcerpt("  first\n\tsecond  世界再见  ", 15)
	if got != "first second 世…" {
		t.Fatalf("excerpt = %q", got)
	}
	if utf8.RuneCountInString(got) > 15 {
		t.Fatalf("excerpt has %d runes, want at most 15", utf8.RuneCountInString(got))
	}
	if strings.ContainsAny(got, "\n\t") {
		t.Fatalf("excerpt retained control whitespace: %q", got)
	}
}

func TestMakeBriefExcerptLeavesShortContentWhole(t *testing.T) {
	if got := makeBriefExcerpt("short memory", 20); got != "short memory" {
		t.Fatalf("excerpt = %q", got)
	}
	if got := makeBriefExcerpt("long", 1); got != "…" {
		t.Fatalf("single-character excerpt = %q", got)
	}
}

func TestBriefResponseIsCompactAndPointsToFullResult(t *testing.T) {
	score := 0.842
	response := newBriefResponse([]briefResult{{
		ID: "memory-id", Excerpt: "short", Category: "decision", Score: &score,
	}}, "")
	var out bytes.Buffer
	if err := encodeBrief(&out, response); err != nil {
		t.Fatalf("encode brief response: %v", err)
	}
	if strings.Contains(out.String(), "\n  ") {
		t.Fatalf("brief JSON was indented: %q", out.String())
	}
	if !strings.Contains(out.String(), "mnemon show <id>") {
		t.Fatalf("brief JSON escaped its command hint: %q", out.String())
	}
	var decoded briefResponse
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("decode brief response: %v", err)
	}
	if decoded.DetailCommand != "mnemon show <id>" {
		t.Fatalf("detail command = %q", decoded.DetailCommand)
	}
}

func TestBriefResponseKeepsEmptyResultsAsArray(t *testing.T) {
	response := newBriefResponse(nil, "sparse_results")
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"results":[]`) {
		t.Fatalf("empty results are not an array: %s", data)
	}
	if response.DetailCommand != "" {
		t.Fatalf("empty response has detail command %q", response.DetailCommand)
	}
}

func TestValidateBriefExcerptChars(t *testing.T) {
	if err := validateBriefExcerptChars(false, 0); err != nil {
		t.Fatalf("disabled brief mode rejected unused limit: %v", err)
	}
	if err := validateBriefExcerptChars(true, 0); err == nil {
		t.Fatal("brief mode accepted zero excerpt length")
	}
}
