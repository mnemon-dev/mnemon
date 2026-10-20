package memoryagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// openTestCache opens a model cache at path, closed when the test ends.
func openTestCache(t *testing.T, path string) *ModelCache {
	t.Helper()
	cache, err := OpenModelCache(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cache.Close() })
	return cache
}

func TestModelCacheFileIsAbsolutePrivateAndWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "eval", "model-cache.db")
	cache := openTestCache(t, path)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatal("cache file is not a private regular file", info, err)
	}
	// Several serve processes share one file: readers never wait for the
	// writer, and writers wait for each other.
	var mode string
	if err = cache.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Fatal("journal mode", mode, err)
	}
	var timeout int
	if err = cache.db.QueryRow(`PRAGMA busy_timeout`).Scan(&timeout); err != nil || timeout != 10000 {
		t.Fatal("busy timeout", timeout, err)
	}
	if _, err = OpenModelCache("relative/model-cache.db"); err == nil {
		t.Fatal("relative cache path accepted")
	}
	shared := filepath.Join(t.TempDir(), "shared.db")
	if err = os.WriteFile(shared, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = OpenModelCache(shared); err == nil {
		t.Fatal("cache file readable by others accepted")
	}
	if _, err = OpenModelCache(t.TempDir()); err == nil {
		t.Fatal("directory accepted as cache file")
	}
}

func TestModelCacheLookupAndLastWriteWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model-cache.db")
	a, b := openTestCache(t, path), openTestCache(t, path)
	ctx := context.Background()
	if body, ok := a.lookup(ctx, "k"); ok || body != nil {
		t.Fatal("empty cache found a reply", body)
	}
	if err := a.store(ctx, "k", []byte(`{"first":1}`)); err != nil {
		t.Fatal(err)
	}
	if body, ok := b.lookup(ctx, "k"); !ok || string(body) != `{"first":1}` {
		t.Fatal("another handle on the file does not see the reply", string(body), ok)
	}
	// Two processes that missed the same request at once both store: the
	// last write wins.
	if err := b.store(ctx, "k", []byte(`{"second":2}`)); err != nil {
		t.Fatal(err)
	}
	if body, ok := a.lookup(ctx, "k"); !ok || string(body) != `{"second":2}` {
		t.Fatal("last write did not win", string(body), ok)
	}
	// A store outlives its caller's cancellation: the reply was paid for.
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := a.store(canceled, "late", []byte(`{}`)); err != nil {
		t.Fatal("store gave up on a canceled caller", err)
	}
	if _, ok := b.lookup(ctx, "late"); !ok {
		t.Fatal("reply stored after cancellation is missing")
	}
}

func TestReplayKeyCoversKindEndpointModelAndBody(t *testing.T) {
	base := replayKey("deepseek", "https://api.example/chat/completions", "m", []byte(`{"a":1}`))
	if len(base) != 64 || base != replayKey("deepseek", "https://api.example/chat/completions", "m", []byte(`{"a":1}`)) {
		t.Fatal("key is not a stable sha256", base)
	}
	for name, other := range map[string]string{
		"kind":     replayKey("openai", "https://api.example/chat/completions", "m", []byte(`{"a":1}`)),
		"endpoint": replayKey("deepseek", "https://api.example/v2/chat/completions", "m", []byte(`{"a":1}`)),
		"model":    replayKey("deepseek", "https://api.example/chat/completions", "n", []byte(`{"a":1}`)),
		"body":     replayKey("deepseek", "https://api.example/chat/completions", "m", []byte(`{"a": 1}`)),
		// Parts are length-prefixed: moving a byte between parts changes the key.
		"boundary": replayKey("deepseekh", "ttps://api.example/chat/completions", "m", []byte(`{"a":1}`)),
	} {
		if other == base {
			t.Error("key ignores the", name)
		}
	}
}
