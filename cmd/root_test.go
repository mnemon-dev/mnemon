package cmd

import (
	"strings"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/embed"
)

func TestOpenDBRejectsInvalidStoreNameFromEnv(t *testing.T) {
	t.Setenv("MNEMON_STORE", "../outside")

	oldDataDir, oldStoreName, oldReadOnly := dataDir, storeName, readOnly
	t.Cleanup(func() {
		dataDir, storeName, readOnly = oldDataDir, oldStoreName, oldReadOnly
	})
	dataDir = t.TempDir()
	storeName = ""
	readOnly = false

	db, err := openDB()
	if err == nil {
		if db != nil {
			db.Close()
		}
		t.Fatal("expected invalid store name error")
	}
	if !strings.Contains(err.Error(), "invalid store name") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOpenDBRejectsInvalidStoreNameFromFlag(t *testing.T) {
	oldDataDir, oldStoreName, oldReadOnly := dataDir, storeName, readOnly
	t.Cleanup(func() {
		dataDir, storeName, readOnly = oldDataDir, oldStoreName, oldReadOnly
	})
	dataDir = t.TempDir()
	storeName = "../outside"
	readOnly = false

	db, err := openDB()
	if err == nil {
		if db != nil {
			db.Close()
		}
		t.Fatal("expected invalid store name error")
	}
	if !strings.Contains(err.Error(), "invalid store name") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestResolveEmbedModelChain exercises the full cmd → embed pipeline for the
// --embed-model flag and MNEMON_EMBED_MODEL env var, mirroring how cobra
// will hand the value off at runtime. The test runs against
// embed.NewClientWithModel directly so it does not require a live Ollama.
func TestResolveEmbedModelChain(t *testing.T) {
	oldEmbedModel := embedModel
	t.Cleanup(func() { embedModel = oldEmbedModel })

	cases := []struct {
		name      string
		flagValue string
		envValue  string
		want      string
	}{
		{
			name:      "flag wins over env",
			flagValue: "flag-model",
			envValue:  "env-model",
			want:      "flag-model",
		},
		{
			name:      "empty flag falls through to env",
			flagValue: "",
			envValue:  "env-model",
			want:      "env-model",
		},
		{
			name:      "empty flag and empty env falls through to built-in default",
			flagValue: "",
			envValue:  "",
			want:      embed.DefaultModel,
		},
		{
			name:      "flag value passes through verbatim",
			flagValue: "nomic-embed-text-v2-moe:latest",
			envValue:  "",
			want:      "nomic-embed-text-v2-moe:latest",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MNEMON_EMBED_MODEL", tc.envValue)
			embedModel = tc.flagValue
			client := embed.NewClientWithModel(resolveEmbedModel())
			if got := client.Model(); got != tc.want {
				t.Errorf("model resolution: want %q, got %q", tc.want, got)
			}
		})
	}
}
