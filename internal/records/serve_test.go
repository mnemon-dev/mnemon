package records

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestServeCASRevisionAndCancellation(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "records-serve-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "s")
	s := testStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, path) }()
	var conn net.Conn
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err = net.Dial("unix", path)
		if err == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("socket permissions: %v %v", info, err)
	}
	enc, dec := json.NewEncoder(conn), json.NewDecoder(bufio.NewReader(conn))
	call := func(req any) map[string]json.RawMessage {
		t.Helper()
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		if err := enc.Encode(req); err != nil {
			t.Fatal(err)
		}
		var result map[string]json.RawMessage
		if err := dec.Decode(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	put := call(map[string]any{"operation": "put", "upsert": []Upsert{sample("a", "dog"), sample("b", "lake")}})
	var first string
	json.Unmarshal(put["revision"], &first)
	if first == "" {
		t.Fatal(put)
	}
	deadline = time.Now().Add(3 * time.Second)
	for {
		var indexed int
		if err := s.db.QueryRow("SELECT count(*) FROM lexical_documents").Scan(&indexed); err != nil {
			t.Fatal(err)
		}
		if indexed == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("serve did not finish the background lexical build after put")
		}
		time.Sleep(time.Millisecond)
	}
	q := func(rev string) map[string]any {
		return map[string]any{"operation": "query", "revision": rev, "input": map[string]any{"query": "dog", "match": "words", "expand": true}}
	}
	got := call(q(first))
	var ids []string
	json.Unmarshal(got["ids"], &ids)
	if !reflect.DeepEqual(ids, []string{"a"}) {
		t.Fatal(got)
	}
	// An independent writer changes the revision; a warm index must not leak.
	external, err := Open(filepath.Dir(sPath(t, s)), false)
	if err != nil {
		t.Fatal(err)
	}
	defer external.Close()
	second, err := external.Put(ctx, PutRequest{ExpectedRevision: &first, Upsert: []Upsert{sample("b", "dog dog")}, Delete: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	got = call(q(second))
	json.Unmarshal(got["ids"], &ids)
	if !reflect.DeepEqual(ids, []string{"b"}) {
		t.Fatal(got)
	}
	got = call(q(first))
	json.Unmarshal(got["ids"], &ids)
	if !reflect.DeepEqual(ids, []string{"a"}) {
		t.Fatal("lost historical snapshot", got)
	}
	got = call(map[string]any{"operation": "put", "expectedRevision": first, "delete": []string{"b"}})
	if stringValue(got["error"]) != ErrConflict.Error() {
		t.Fatal(got)
	}
	if stringValue(call(map[string]any{"operation": "unknown"})["error"]) == "" {
		t.Fatal("missing protocol error")
	}
	if _, err := conn.Write([]byte("{\n")); err != nil {
		t.Fatal(err)
	}
	var malformed map[string]json.RawMessage
	if err := dec.Decode(&malformed); err != nil || stringValue(malformed["error"]) != "Invalid records request JSON" {
		t.Fatalf("malformed frame: %s %v", malformed, err)
	}
	if stringValue(call(map[string]any{"operation": "get", "ids": []string{}})["revision"]) != second {
		t.Fatal("malformed frame changed the collection")
	}
	// A reserved writer must not monopolize the read path. The read-only pool
	// observes committed state while the single writer owns its immediate tx.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	got = call(q(second))
	tx.Rollback()
	json.Unmarshal(got["ids"], &ids)
	if !reflect.DeepEqual(ids, []string{"b"}) {
		t.Fatal("reader blocked or changed under reserved writer", got)
	}
	checkConcurrentServeCAS(t, path, second)
	// Fill idle connections, then cancellation must close them and join workers.
	idle := []net.Conn{}
	for i := 0; i < 15; i++ {
		c, e := net.Dial("unix", path)
		if e != nil {
			t.Fatal(e)
		}
		idle = append(idle, c)
	}
	defer func() {
		for _, c := range idle {
			c.Close()
		}
	}()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serve did not join its workers")
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("owned socket not removed", err)
	}
}

func checkConcurrentServeCAS(t *testing.T, path, hash string) {
	t.Helper()
	frame := func(request any) (map[string]json.RawMessage, error) {
		conn, err := net.Dial("unix", path)
		if err != nil {
			return nil, err
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		if err = json.NewEncoder(conn).Encode(request); err != nil {
			return nil, err
		}
		var result map[string]json.RawMessage
		err = json.NewDecoder(conn).Decode(&result)
		return result, err
	}
	var wg sync.WaitGroup
	results := make(chan map[string]json.RawMessage, 8)
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				r, err := frame(map[string]any{"operation": "query", "revision": hash, "input": map[string]any{"query": "dog", "match": "words", "expand": true}})
				var ids []string
				json.Unmarshal(r["ids"], &ids)
				if err != nil || !reflect.DeepEqual(ids, []string{"b"}) {
					t.Errorf("concurrent pinned read: %s %v", r, err)
					return
				}
			}
		}()
		go func(i int) {
			defer wg.Done()
			r, err := frame(map[string]any{"operation": "put", "expectedRevision": hash, "upsert": []Upsert{sample(fmt.Sprintf("writer-%d", i), "dog pine")}})
			if err != nil {
				t.Error(err)
				return
			}
			results <- r
		}(i)
	}
	wg.Wait()
	close(results)
	winners, conflicts := 0, 0
	for r := range results {
		if stringValue(r["revision"]) != "" {
			winners++
			continue
		}
		if stringValue(r["error"]) == ErrConflict.Error() {
			conflicts++
			continue
		}
		t.Errorf("concurrent CAS: %s", r)
	}
	if winners != 1 || conflicts != 7 {
		t.Fatalf("CAS winners=%d conflicts=%d", winners, conflicts)
	}
}

func sPath(t *testing.T, s *Store) string {
	t.Helper()
	var seq int
	var name, path string
	if err := s.db.QueryRow("PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestServeDoesNotRemoveExistingPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing")
	if err := os.WriteFile(path, []byte("owner"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := testStore(t).Serve(context.Background(), path); err == nil {
		t.Fatal("existing path accepted")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "owner" {
		t.Fatal("existing path was changed")
	}
}
