package records

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// A store opened by a long-lived process can run the query runtime Serve
// runs (query cache, reader pool, lexical worker) without a socket.
func TestStartRuntimeCachesQueriesAndBuildsLexical(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	stop, err := s.StartRuntime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.cache == nil || s.readers == nil {
		t.Fatal("runtime not started")
	}
	revision, err := s.Put(ctx, PutRequest{Upsert: []Upsert{sample("a", "dog"), sample("b", "lake")}})
	if err != nil {
		t.Fatal(err)
	}
	// A put wakes the lexical worker; nothing has to query first.
	deadline := time.Now().Add(3 * time.Second)
	for {
		var indexed int
		if err := s.db.QueryRow("SELECT count(*) FROM lexical_documents").Scan(&indexed); err != nil {
			t.Fatal(err)
		}
		if indexed == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the runtime did not build the lexical index after put")
		}
		time.Sleep(time.Millisecond)
	}
	query := QueryRequest{Revision: revision, Input: map[string]json.RawMessage{"query": json.RawMessage(`"dog"`), "match": json.RawMessage(`"words"`)}}
	first, err := s.Query(ctx, query)
	if err != nil || !slices.Equal(first.IDs, []string{"a"}) {
		t.Fatal(first, err)
	}
	// The second query is served from the cache of this revision.
	s.cache.mu.Lock()
	cached := s.cache.snapshotHash == revision && s.cache.snapshot != nil
	s.cache.mu.Unlock()
	if !cached {
		t.Fatal("query snapshot not cached")
	}
	second, err := s.Query(ctx, query)
	if err != nil || !slices.Equal(second.IDs, first.IDs) {
		t.Fatal(second, err)
	}
	stop()
	stop() // idempotent
	if s.cache != nil || s.readers != nil {
		t.Fatal("runtime not stopped")
	}
	// Without the runtime the store still answers.
	third, err := s.Query(ctx, query)
	if err != nil || !slices.Equal(third.IDs, first.IDs) {
		t.Fatal(third, err)
	}
}

func TestServeUsingStillRunsTheRuntime(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "records-runtime-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "s")
	s := testStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	running := make(chan bool, 1)
	handler := func(ctx context.Context, raw []byte) (any, error) {
		running <- s.cache != nil && s.readers != nil
		return s.Dispatch(ctx, raw)
	}
	done := make(chan error, 1)
	go func() { done <- s.ServeUsing(ctx, path, handler) }()
	var conn net.Conn
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if conn, err = net.Dial("unix", path); err == nil {
			break
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if err := json.NewEncoder(conn).Encode(map[string]any{"operation": "put", "upsert": []Upsert{sample("a", "dog")}}); err != nil {
		t.Fatal(err)
	}
	var reply map[string]json.RawMessage
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&reply); err != nil || reply["revision"] == nil {
		t.Fatal(reply, err)
	}
	if !<-running {
		t.Fatal("ServeUsing handled a request without its runtime")
	}
	cancel()
	if err := <-done; err != nil && err != context.Canceled {
		t.Fatal(err)
	}
	if s.cache != nil || s.readers != nil {
		t.Fatal("ServeUsing left its runtime running")
	}
}
