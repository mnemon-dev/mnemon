package memory

import (
	"strings"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/memory/embed"
	"github.com/mnemon-dev/mnemon/internal/memory/store"
	"github.com/spf13/cobra"
)

func TestNewReturnsComposableMemoryRoot(t *testing.T) {
	oldVersion, oldRootVersion := version, rootCmd.Version
	t.Cleanup(func() {
		version = oldVersion
		rootCmd.Version = oldRootVersion
	})

	cmd := New("test-version")
	if cmd.Use != "mnemon" {
		t.Fatalf("root use = %q, want mnemon", cmd.Use)
	}
	if cmd.Version != "test-version" {
		t.Fatalf("root version = %q, want test-version", cmd.Version)
	}
	for _, name := range []string{"remember", "recall", "setup", "store"} {
		if child, _, err := cmd.Find([]string{name}); err != nil || child == cmd {
			t.Fatalf("memory command %q is not registered", name)
		}
	}
}

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

func TestOpenWritableDBRejectsReadOnlyBeforeCommandWork(t *testing.T) {
	oldDataDir, oldStoreName, oldReadOnly := dataDir, storeName, readOnly
	t.Cleanup(func() {
		dataDir, storeName, readOnly = oldDataDir, oldStoreName, oldReadOnly
	})
	dataDir = t.TempDir()
	storeName = ""
	readOnly = false

	db, err := openDB()
	if err != nil {
		t.Fatalf("create database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	readOnly = true
	db, err = openWritableDB("remember")
	if db != nil {
		db.Close()
		t.Fatal("read-only writable open returned a database handle")
	}
	if err == nil || !strings.Contains(err.Error(), "remember is unavailable with --readonly") {
		t.Fatalf("unexpected read-only error: %v", err)
	}

	ro, err := openDB()
	if err != nil {
		t.Fatalf("ordinary read-only open must remain available: %v", err)
	}
	defer ro.Close()
	if !ro.IsReadOnly() {
		t.Fatal("ordinary read-only open returned a writable handle")
	}

	if got := store.ReadActive(dataDir); got != store.DefaultStoreName {
		t.Fatalf("active store = %q, want %q", got, store.DefaultStoreName)
	}
}

func TestStoreMutationsRejectReadOnlyMode(t *testing.T) {
	oldDataDir, oldStoreName, oldReadOnly := dataDir, storeName, readOnly
	t.Cleanup(func() {
		dataDir, storeName, readOnly = oldDataDir, oldStoreName, oldReadOnly
	})
	dataDir = t.TempDir()
	storeName = ""
	readOnly = true

	tests := []struct {
		name string
		cmd  *cobra.Command
	}{
		{name: "store create", cmd: storeCreateCmd},
		{name: "store set", cmd: storeSetCmd},
		{name: "store remove", cmd: storeRemoveCmd},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cmd.RunE(nil, []string{"example"})
			if err == nil || !strings.Contains(err.Error(), tt.name+" is unavailable with --readonly") {
				t.Fatalf("unexpected read-only error: %v", err)
			}
		})
	}

	if store.StoreExists(dataDir, "example") {
		t.Fatal("read-only store commands created a store")
	}
}

// TestResolveEmbedModelChain exercises the full cmd → embed pipeline for the
// --embed-model flag and MNEMON_EMBED_MODEL env var, mirroring how cobra
// will hand the value off at runtime. The test runs against
// embed.NewClientWithModel directly so it does not require a live provider.
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
