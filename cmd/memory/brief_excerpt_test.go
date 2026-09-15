package memory

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBriefDiscoveryShowsMatchingFactAndQualifiers(t *testing.T) {
	fact := "The maintenance cartridge decision dated 2025-04-12 says do not use RT-42; use QZ-73 instead."
	for _, tc := range []struct {
		name, content, query string
		want                 []string
	}{
		{"long-header", strings.Repeat("[archive_ref=opaque-772; actor=clerk; cartridge=inventory] ", 12) + fact,
			"maintenance cartridge", []string{"2025-04-12", "do not use RT-42", "use QZ-73 instead"}},
		{"plain-prose", strings.Repeat("The meeting covered routine floor plans and furniture placement. ", 9) + fact,
			"maintenance cartridge", []string{"2025-04-12", "do not use RT-42", "use QZ-73 instead"}},
		{"match-near-old-cutoff", strings.Repeat("context ", 27) + strings.TrimPrefix(fact, "The "),
			"maintenance cartridge", []string{"2025-04-12", "do not use RT-42", "use QZ-73 instead"}},
		{"cjk", strings.Repeat("[归档批次=常规记录; 编辑者=实验员] 🧪 ", 18) + "实验台的冷却泵方案于2025年4月12日调整；不再使用RT-42，改用QZ-73。",
			"冷却泵", []string{"2025年4月12日", "不再使用RT-42", "改用QZ-73"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := scopedRecallStore(t)
			oldLimit, oldBrief, oldExcerpt := searchLimit, searchBrief, searchExcerpt
			t.Cleanup(func() { searchLimit, searchBrief, searchExcerpt = oldLimit, oldBrief, oldExcerpt })
			insertTestInsight(t, db, "entry", tc.content, "prod", "2025-04-12T00:00:00Z")
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			recLimit, recExcerpt, searchLimit, searchExcerpt = 1, 240, 1, 240
			for _, surface := range []string{"recall", "basic", "search"} {
				t.Run(surface, func(t *testing.T) {
					full, _ := runBriefDiscovery(t, surface, tc.query, false)
					brief, detail := runBriefDiscovery(t, surface, tc.query, true)
					if len(full) != 1 || len(brief) != 1 || full[0]["id"] != brief[0]["id"] || full[0]["score"] != brief[0]["score"] {
						t.Fatalf("brief changed discovery selection or scoring: full=%v brief=%v", full, brief)
					}
					if full[0]["content"] != tc.content || detail != "mnemon show <id>" {
						t.Fatal("full content or the detail command changed")
					}
					excerpt, ok := brief[0]["excerpt"].(string)
					if !ok || !utf8.ValidString(excerpt) || utf8.RuneCountInString(excerpt) > 240 {
						t.Fatalf("invalid or oversized excerpt: %q", excerpt)
					}
					for _, want := range tc.want {
						if !strings.Contains(excerpt, want) {
							t.Errorf("brief hid the fact's adjacent qualifier %q: %q", want, excerpt)
						}
					}
				})
			}
		})
	}
}

func TestQueryBriefExcerptFallbackAndBounds(t *testing.T) {
	content := strings.Repeat("neutral archive context 🧪 世界 ", 40) + "MATCHING TERM dated 2025-04-12 is not approved."
	for _, query := range []string{"", "unmatched", "the and of", strings.Repeat("x", maxBriefQueryBytes+1)} {
		if got, want := makeQueryBriefExcerpt(content, query, 80), makeBriefExcerpt(content, 80); got != want {
			t.Errorf("fallback changed for query %q: got %q, want %q", query, got, want)
		}
	}
	terms := make([]string, maxBriefQueryTerms+1)
	for i := range terms {
		terms[i] = fmt.Sprintf("word%d", i)
	}
	if got, want := makeQueryBriefExcerpt(content, strings.Join(terms, " "), 80), makeBriefExcerpt(content, 80); got != want {
		t.Errorf("excessive query terms did not fall back to the prefix: %q", got)
	}
	for _, limit := range []int{0, 1, 2, 3, 8, 40, 80, 240, 5000} {
		got := makeQueryBriefExcerpt(content, "matching term", limit)
		if !utf8.ValidString(got) || utf8.RuneCountInString(got) > limit || strings.ContainsAny(got, "\n\t") {
			t.Errorf("limit %d produced invalid excerpt %q", limit, got)
		}
		if limit >= utf8.RuneCountInString(content) && got != content {
			t.Error("short content was changed")
		}
	}
}

func TestQueryBriefExcerptKeepsContinuousContextAndStableTies(t *testing.T) {
	content := strings.Repeat("registry entry ", 30) + "On 2025-04-12, do not approve the QuartzHarbor shipment; the date is 2025-04-19. " + strings.Repeat("other context ", 30)
	got := makeQueryBriefExcerpt(content, "QuartzHarbor shipment", 160)
	for _, want := range []string{"2025-04-12, do not approve", "QuartzHarbor shipment", "the date is 2025-04-19"} {
		if !strings.Contains(got, want) {
			t.Errorf("lost adjacent context %q: %q", want, got)
		}
	}
	if !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "…") {
		t.Fatalf("omission is not visible: %q", got)
	}
	if !strings.Contains(content, strings.Trim(got, "…")) {
		t.Fatalf("excerpt synthesized or joined noncontiguous text: %q", got)
	}
	if other := makeQueryBriefExcerpt(content, "shipment QuartzHarbor QuartzHarbor", 160); other != got {
		t.Errorf("query ordering or repetition changed the excerpt: %q vs %q", got, other)
	}
	content = strings.Repeat("note ", 40) + "alpha beta first passage. " + strings.Repeat("padding ", 40) + "alpha beta second passage."
	if got := makeQueryBriefExcerpt(content, "alpha beta", 70); !strings.Contains(got, "first passage") {
		t.Errorf("equal coverage and density did not keep the earlier passage: %q", got)
	}
}

func TestBriefMatchWindowStaysBoundedUnderRepeatedHits(t *testing.T) {
	window := briefMatchWindow{terms: map[string]bool{"alpha": true, "beta": true}, width: 40, counts: make(map[string]int)}
	runes := []rune(strings.Repeat("alpha ", 10000) + "alpha beta dated 2025-04-12")
	window.scan(runes)
	if len(window.matches) > window.width || len(window.counts) > len(window.terms) || window.bestCount != 2 {
		t.Fatalf("repeated hits exceeded the active window or hid distinct terms: %+v", window)
	}
	if got := renderBriefMatch(runes, window.best, 80); !strings.Contains(got, "alpha beta dated 2025-04-12") {
		t.Fatalf("repetition displaced the complete matching passage: %q", got)
	}
}

func FuzzQueryBriefExcerptBounds(f *testing.F) {
	f.Add("prefix 🧪 中文 content not approved on 2025-04-12", "content approved", uint16(32))
	f.Add("\xff long\ntext "+strings.Repeat("context ", 100), "long text", uint16(1))
	f.Fuzz(func(t *testing.T, content, query string, size uint16) {
		limit := int(size%513) + 1
		got := makeQueryBriefExcerpt(content, query, limit)
		if !utf8.ValidString(got) || utf8.RuneCountInString(got) > limit || strings.ContainsAny(got, "\n\r\t") {
			t.Fatalf("invalid bounded excerpt (limit %d): %q", limit, got)
		}
		if _, err := json.Marshal(newBriefResponse([]briefResult{{ID: "entry", Excerpt: got}}, "")); err != nil {
			t.Fatal(err)
		}
	})
}

func runBriefDiscovery(t *testing.T, surface, query string, brief bool) ([]map[string]any, string) {
	t.Helper()
	recBasic, recBrief, searchBrief = surface == "basic", brief, brief
	command := recallCmd
	if surface == "search" {
		command = searchCmd
	}
	var runErr error
	out := captureStdout(t, func() { runErr = command.RunE(command, []string{query}) })
	if runErr != nil {
		t.Fatal(runErr)
	}
	if !brief && surface != "recall" {
		var rows []map[string]any
		if err := json.Unmarshal([]byte(out), &rows); err != nil {
			t.Fatal(err)
		}
		return rows, ""
	}
	var response struct {
		Results       []map[string]any `json:"results"`
		DetailCommand string           `json:"detail_command"`
	}
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		t.Fatal(err)
	}
	return response.Results, response.DetailCommand
}
