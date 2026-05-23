package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/store"
)

func TestBuildReceiptOmitsRawDetails(t *testing.T) {
	generatedAt := time.Date(2026, 5, 23, 16, 0, 0, 0, time.UTC)
	entries := []store.OplogEntry{
		{
			Operation: "remember",
			InsightID: "ins-secret-123",
			Detail:    "customer ACME had incident sk_live_private_demo in /private/path",
			CreatedAt: "2026-05-23T15:59:00Z",
		},
		{
			Operation: "recall",
			Detail:    "q=private roadmap hits=3",
			CreatedAt: "2026-05-23T15:59:30Z",
		},
	}

	doc := buildReceipt("default", 20, entries, generatedAt)
	if doc.Schema != "mnemon.memory.receipt.v1" {
		t.Fatalf("unexpected schema: %s", doc.Schema)
	}
	if doc.GeneratedAt != generatedAt.Format(time.RFC3339) {
		t.Fatalf("unexpected generated_at: %s", doc.GeneratedAt)
	}
	if doc.Count != 2 || len(doc.Events) != 2 {
		t.Fatalf("unexpected event count: count=%d len=%d", doc.Count, len(doc.Events))
	}
	if doc.Privacy.RawDetailIncluded {
		t.Fatal("receipt should not include raw details")
	}

	rendered := doc.Events[0].DetailHash + doc.Events[0].InsightIDHash + doc.Events[1].DetailHash
	for _, forbidden := range []string{"ACME", "sk_live_private_demo", "/private/path", "private roadmap", "ins-secret-123"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("receipt leaked raw value %q in hashes: %s", forbidden, rendered)
		}
	}
	if doc.Events[0].DetailHash == "" || doc.Events[0].InsightIDHash == "" {
		t.Fatal("expected hashes for non-empty detail and insight id")
	}
	if !doc.Events[0].DetailPresent || !doc.Events[1].DetailPresent {
		t.Fatal("expected detail_present for entries with detail")
	}
}
