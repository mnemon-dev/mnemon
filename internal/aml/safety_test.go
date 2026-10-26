package aml

import (
	"strings"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
)

// TestSafetyMaskHidesSensitiveValuesInSearches: with safety.mask a Search
// hands out a stored card number as "[card ending 4242]", and counts it;
// without it the number comes back as stored.
func TestSafetyMaskHidesSensitiveValuesInSearches(t *testing.T) {
	stored := "I paid with my card 4242 4242 4242 4242 at the LGBTQ support group fundraiser."
	for _, on := range []bool{false, true} {
		h := newHarness(t, func(config *ServerConfig) {
			if on {
				config.MemoryConfig.Safety = &memoryagent.SafetyConfig{Mask: true}
			}
		})
		h.mustAdd(t, AddRequest{RequestID: "r1", UserID: "u1", SessionID: "s1", Messages: []Message{{Role: "user", Content: stored, Timestamp: at(0)}}})
		items := h.search(t, SearchRequest{Query: "How did I pay at the LGBTQ support group fundraiser?", UserID: "u1", TopK: 100})
		if len(items) == 0 {
			t.Fatal("no items")
		}
		content := items[0].Content
		if on != strings.Contains(content, "[card ending 4242]") || on == strings.Contains(content, "4242 4242 4242 4242") {
			t.Fatalf("mask %v: %q", on, content)
		}
		if on {
			h.scrape(t).is(t, 1, "mnemon_aml_search_masked_total", "kind", "card")
		} else {
			h.scrape(t).absent(t, "mnemon_aml_search_masked_total", "kind", "card")
		}
	}
}
