package memory

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/memory/embed"
	"github.com/mnemon-dev/mnemon/internal/memory/store"
)

func TestEmbedStatusChecksConfiguredModel(t *testing.T) {
	for _, tc := range []struct {
		name, protocol, body         string
		discoveryStatus, embedStatus int
		wantProvider, wantEmbedding  bool
		wantProbes                   int32
	}{
		{"ollama ready", "ollama", `{"embeddings":[[1,0]]}`, 200, 200, true, true, 1},
		{"ollama missing model", "ollama", `{"error":"model is missing"}`, 200, 404, true, false, 1},
		{"ollama empty vector", "ollama", `{"embeddings":[[]]}`, 200, 200, true, false, 1},
		{"ollama unavailable", "ollama", "", 503, 503, false, false, 0},
		{"openai ready", "openai", `{"data":[{"embedding":[1,0]}]}`, 200, 200, true, true, 1},
		{"openai missing model", "openai", `{"error":"model is missing"}`, 200, 404, true, false, 1},
		{"openai malformed vector", "openai", `{"data":[{"embedding":"invalid"}]}`, 200, 200, true, false, 1},
		{"openai without discovery", "openai", `{"data":[{"embedding":[1,0]}]}`, 404, 200, true, true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldDir, oldStore, oldReadOnly := dataDir, storeName, readOnly
			oldStatus, oldModel := embedStatus, embedModel
			t.Cleanup(func() {
				dataDir, storeName, readOnly = oldDir, oldStore, oldReadOnly
				embedStatus, embedModel = oldStatus, oldModel
			})
			dataDir, storeName, readOnly = t.TempDir(), "embedding-status", false
			embedStatus, embedModel = true, "selected-model"
			t.Setenv("MNEMON_EMBED_PROTOCOL", tc.protocol)
			t.Setenv("MNEMON_EMBED_DIMENSIONS", "")
			t.Setenv("MNEMON_EMBED_API_KEY", "synthetic-key")
			t.Setenv("MNEMON_EMBED_MODEL", "ignored-env-model")
			var probes atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					w.WriteHeader(tc.discoveryStatus)
					_, _ = w.Write([]byte(`{"models":[{"name":"different-model:latest"}]}`))
					return
				}
				probes.Add(1)
				var request struct{ Model, Input string }
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Errorf("decode embedding probe: %v", err)
				}
				if request.Model != "selected-model" || request.Input != "availability probe" {
					t.Errorf("probe must use configured model and synthetic input: %+v", request)
				}
				w.WriteHeader(tc.embedStatus)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			t.Setenv("MNEMON_EMBED_ENDPOINT", srv.URL)

			db, err := store.Open(store.StoreDir(dataDir, storeName))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			insertTestInsight(t, db, "status-seed", "stored memory must not be sent by status", "test", "2026-01-01T00:00:00Z")
			if err := db.UpdateEmbedding("status-seed", embed.SerializeVector([]float64{1, 0})); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			var runErr error
			out := captureStdout(t, func() { runErr = embedCmd.RunE(embedCmd, nil) })
			if runErr != nil {
				t.Fatal(runErr)
			}
			var result struct {
				EmbeddingAvailable bool   `json:"embedding_available"`
				OllamaAvailable    bool   `json:"ollama_available"`
				Model              string `json:"model"`
				Coverage           string `json:"coverage"`
			}
			if err := json.Unmarshal([]byte(out), &result); err != nil {
				t.Fatalf("decode status: %v\n%s", err, out)
			}
			if result.EmbeddingAvailable != tc.wantEmbedding || result.OllamaAvailable != tc.wantProvider || result.Model != "selected-model" {
				t.Errorf("unexpected embedding status: %s", out)
			}
			if result.Coverage != "100%" {
				t.Errorf("model availability must not alter stored embedding coverage: %s", out)
			}
			if got := probes.Load(); got != tc.wantProbes {
				t.Errorf("embedding probes = %d, want %d", got, tc.wantProbes)
			}
		})
	}
}
