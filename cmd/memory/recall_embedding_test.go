package memory

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/memory/embed"
	"github.com/mnemon-dev/mnemon/internal/memory/search"
	"github.com/mnemon-dev/mnemon/internal/memory/store"
)

func TestRecallReportsEmbeddingFailure(t *testing.T) {
	for _, tc := range []struct {
		name, body  string
		status      int
		wantWarning bool
	}{
		{"missing model", `{"error":"synthetic-secret and private-provider-echo"}`, 404, true},
		{"invalid vector", `{"embeddings":[[]]}`, 200, true},
		{"healthy model", `{"embeddings":[[1,0]]}`, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldDir, oldStore, oldReadOnly, oldModel := dataDir, storeName, readOnly, embedModel
			oldBasic, oldBrief, oldVerbose, oldLimit := recBasic, recBrief, recVerbose, recLimit
			oldIntent, oldExcerpt := recIntent, recExcerpt
			oldErr := recallCmd.ErrOrStderr()
			t.Cleanup(func() {
				dataDir, storeName, readOnly, embedModel = oldDir, oldStore, oldReadOnly, oldModel
				recBasic, recBrief, recVerbose, recLimit = oldBasic, oldBrief, oldVerbose, oldLimit
				recIntent, recExcerpt = oldIntent, oldExcerpt
				recallCmd.SetErr(oldErr)
			})
			dataDir, storeName, readOnly, embedModel = t.TempDir(), "embedding-recall", false, "selected-model"
			recBasic, recLimit, recIntent, recExcerpt = false, 5, "GENERAL", 240
			t.Setenv("MNEMON_EMBED_PROTOCOL", "ollama")
			t.Setenv("MNEMON_EMBED_DIMENSIONS", "")
			t.Setenv("MNEMON_EMBED_API_KEY", "synthetic-secret")
			var embeds atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/tags" {
					_, _ = w.Write([]byte(`{"models":[]}`))
					return
				}
				embeds.Add(1)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			t.Setenv("MNEMON_EMBED_ENDPOINT", srv.URL)
			db, err := store.Open(store.StoreDir(dataDir, storeName))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			insertTestInsight(t, db, "embedding-seed", "SQLite snapshot memory", "test", "2026-01-01T00:00:00Z")
			if err := db.UpdateEmbedding("embedding-seed", embed.SerializeVector([]float64{1, 0})); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"compact", "brief", "verbose"} {
				t.Run(mode, func(t *testing.T) {
					recBrief, recVerbose = mode == "brief", mode == "verbose"
					var stderr bytes.Buffer
					recallCmd.SetErr(&stderr)
					var runErr error
					out := captureStdout(t, func() { runErr = recallCmd.RunE(recallCmd, []string{"SQLite"}) })
					if runErr != nil {
						t.Fatalf("recall must fall back successfully: %v", runErr)
					}
					var response struct{ Results []json.RawMessage }
					if err := json.Unmarshal([]byte(out), &response); err != nil || len(response.Results) != 1 || !strings.Contains(string(response.Results[0]), "embedding-seed") {
						t.Fatalf("recall lost its JSON result: %v\n%s", err, out)
					}
					warning := stderr.String()
					if tc.wantWarning {
						if strings.Count(warning, "\n") != 1 || !strings.Contains(warning, "without vector similarity") || !strings.Contains(warning, "selected-model") || !strings.Contains(warning, "--embed-model") {
							t.Errorf("missing actionable one-line fallback warning: %q", warning)
						}
					} else if warning != "" {
						t.Errorf("healthy embedding should be quiet: %q", warning)
					}
					for _, secret := range []string{"synthetic-secret", "private-provider-echo"} {
						if strings.Contains(warning+out, secret) {
							t.Errorf("provider content leaked: %q", secret)
						}
					}
					if recVerbose {
						var verbose search.RecallResponse
						if err := json.Unmarshal([]byte(out), &verbose); err != nil {
							t.Fatal(err)
						}
						similarity := verbose.Results[0].Signals.Similarity
						if (tc.wantWarning && similarity != 0) || (!tc.wantWarning && similarity < 0.99) {
							t.Errorf("unexpected vector similarity: %v", similarity)
						}
					}
				})
			}
			if got := embeds.Load(); got != 3 {
				t.Errorf("recall made %d embedding requests, want one per query", got)
			}
		})
	}
}
