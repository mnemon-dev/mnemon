package aml

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
)

// walOpen reports whether a handle on the SQLite file at path is open: the
// write-ahead log exists from the first open to the last close.
func walOpen(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path + "-wal")
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return err == nil
}

func TestServerOwnsTheConfiguredModelCache(t *testing.T) {
	local := &memoryagent.ModelConfig{Kind: "local", Model: "m", BaseURL: "http://127.0.0.1:1/v1"}
	path := filepath.Join(t.TempDir(), "model-cache.db")
	config := simpleConfig()
	config.Planner, config.Judge, config.ModelCache = local, local, path
	server, err := NewServer(ServerConfig{DataRoot: t.TempDir(), MemoryConfig: config, AuthKey: testKey})
	if err != nil {
		t.Fatal(err)
	}
	if server.cache == nil || !walOpen(t, path) {
		t.Fatal("the server did not open the model cache")
	}
	server.Close()
	if walOpen(t, path) {
		t.Fatal("Close left the model cache open")
	}
	// A configuration the agents refuse closes the cache it opened.
	config.Judge = nil
	if server, err = NewServer(ServerConfig{DataRoot: t.TempDir(), MemoryConfig: config, AuthKey: testKey}); err == nil {
		server.Close()
		t.Fatal("simple strategy without a judge accepted")
	}
	if walOpen(t, path) {
		t.Fatal("a refused configuration left the model cache open")
	}
}
