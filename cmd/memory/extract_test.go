package memory

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/memory/importdraft"
)

const extractContent = "Jane Rivera briefed the CEO of Acme Foundation; notes in docs/plan.md"

// startExtractServer fakes a Workers AI extraction endpoint that answers every
// request with items, or fails with status. It returns the request counter.
func startExtractServer(t *testing.T, status int, items []string) *atomic.Int32 {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if status != http.StatusOK {
			http.Error(w, `{"success":false,"errors":[{"message":"model unavailable"}]}`, status)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{
			"answers": map[string]any{"entities": map[string]any{"type": "list", "value": items}},
		}})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("MNEMON_EXTRACT_MODEL", "test-model")
	t.Setenv("MNEMON_EXTRACT_ENDPOINT", srv.URL)
	t.Setenv("MNEMON_EXTRACT_API_KEY", "")
	t.Setenv("MNEMON_EXTRACT_TIMEOUT", "")
	return &hits
}

// stderrOf runs fn and returns what it wrote to stderr.
func stderrOf(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stderr: %v", err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()
	fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close stderr writer: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	return string(data)
}

func entitySet(entities []string) map[string]bool {
	set := make(map[string]bool, len(entities))
	for _, e := range entities {
		set[e] = true
	}
	return set
}

func rememberEntities(t *testing.T) (map[string]bool, string) {
	t.Helper()
	var out string
	var runErr error
	stderr := stderrOf(t, func() {
		out = captureStdout(t, func() { runErr = rememberCmd.RunE(rememberCmd, []string{extractContent}) })
	})
	if runErr != nil {
		t.Fatalf("remember: %v", runErr)
	}
	var result struct {
		Entities []string `json:"entities"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode remember output: %v\n%s", err, out)
	}
	return entitySet(result.Entities), stderr
}

func TestRememberUsesModelEntitiesWhenNoneProvided(t *testing.T) {
	// Given an extraction model that finds two real names and invents one
	configureRememberTest(t)
	hits := startExtractServer(t, http.StatusOK, []string{"Jane Rivera", "Acme Foundation", "Invented Inc"})

	// When a memory is remembered without entities
	entities, stderr := rememberEntities(t)

	// Then the real names replace the acronym guess, exact paths stay, and the invented name is dropped
	for _, want := range []string{"Jane Rivera", "Acme Foundation", "docs/plan.md"} {
		if !entities[want] {
			t.Errorf("want entity %q, got %v", want, entities)
		}
	}
	if entities["CEO"] || entities["Invented Inc"] {
		t.Errorf("entities = %v; want no acronym guess and no invented name", entities)
	}
	if hits.Load() != 1 || stderr != "" {
		t.Fatalf("requests = %d, stderr = %q", hits.Load(), stderr)
	}
}

func TestRememberSkipsModelWhenEntitiesProvided(t *testing.T) {
	// Given a configured model and caller-provided entities
	configureRememberTest(t)
	hits := startExtractServer(t, http.StatusOK, []string{"Jane Rivera"})
	remEntities = "zebra"

	// When the memory is remembered
	entities, _ := rememberEntities(t)

	// Then the model is never asked and heuristic extraction is unchanged
	if hits.Load() != 0 || !entities["zebra"] || !entities["CEO"] || entities["Jane Rivera"] {
		t.Fatalf("requests = %d, entities = %v", hits.Load(), entities)
	}
}

func TestRememberFallsBackToHeuristicsWhenModelFails(t *testing.T) {
	// Given an extraction model that errors
	configureRememberTest(t)
	startExtractServer(t, http.StatusInternalServerError, nil)

	// When a memory is remembered without entities
	entities, stderr := rememberEntities(t)

	// Then heuristic extraction applies and one warning is printed
	if !entities["CEO"] || entities["Jane Rivera"] {
		t.Fatalf("entities = %v; want heuristic extraction", entities)
	}
	if !strings.HasPrefix(stderr, "warning: entity extraction unavailable") || strings.Count(stderr, "\n") != 1 {
		t.Fatalf("stderr = %q, want a single warning line", stderr)
	}
}

func TestImportUsesModelOnlyForDraftsWithoutEntities(t *testing.T) {
	// Given a model and two drafts, one already carrying entities
	db := setupImportDiffTest(t, true)
	hits := startExtractServer(t, http.StatusOK, []string{"Acme Foundation"})
	draft := importdraft.MemoryDraft{SchemaVersion: "1", Insights: []importdraft.DraftInsight{
		{Content: "Jane Rivera joined the Acme Foundation"},
		{Content: "the Acme Foundation funds Project Lark", Entities: []string{"Project Lark"}},
	}}

	// When the draft is imported
	runImportDiffDraft(t, draft)

	// Then only the first draft asked the model, and its entities came from it
	if hits.Load() != 1 {
		t.Fatalf("requests = %d, want 1", hits.Load())
	}
	insights, err := db.GetAllActiveInsights()
	if err != nil {
		t.Fatal(err)
	}
	for _, ins := range insights {
		got := entitySet(ins.Entities)
		if strings.HasPrefix(ins.Content, "Jane") && !got["Acme Foundation"] {
			t.Errorf("model entities missing: %v", ins.Entities)
		}
		if strings.HasPrefix(ins.Content, "the Acme") && !got["Project Lark"] {
			t.Errorf("provided entities missing: %v", ins.Entities)
		}
	}
}

func TestImportStopsAskingAfterModelFailure(t *testing.T) {
	// Given a failing model and two drafts without entities
	setupImportDiffTest(t, true)
	hits := startExtractServer(t, http.StatusInternalServerError, nil)
	draft := importdraft.MemoryDraft{SchemaVersion: "1", Insights: []importdraft.DraftInsight{
		{Content: "Jane Rivera joined the Acme Foundation"},
		{Content: "Zebra deploys to staging-east"},
	}}

	// When the draft is imported
	stderr := stderrOf(t, func() { runImportDiffDraft(t, draft) })

	// Then the model was asked once and one warning was printed
	if hits.Load() != 1 || strings.Count(stderr, "warning: entity extraction unavailable") != 1 {
		t.Fatalf("requests = %d, stderr = %q", hits.Load(), stderr)
	}
}
