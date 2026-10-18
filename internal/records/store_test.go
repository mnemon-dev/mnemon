package records

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func sample(id, content string) Upsert {
	raw := `{"id":"` + id + `","kind":"progress","title":"Title","content":"` + content + `","scope":"global","state":"active","data":{"status":null},"createdAt":"2026-10-18T00:00:00.000Z"}`
	return Upsert{raw, "Title\n" + content + "\n{\"status\":null}", content}
}

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestVersionsCASAndOrder(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a, b := sample("a", "one"), sample("b", "two")
	first, err := s.Put(ctx, PutRequest{Upsert: []Upsert{a, b}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Put(ctx, PutRequest{ExpectedRevision: &first, Upsert: []Upsert{sample("a", "updated")}, Delete: []string{"b"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Put(ctx, PutRequest{ExpectedRevision: &first, Delete: []string{"a"}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale CAS: %v", err)
	}
	old, err := s.Get(ctx, GetRequest{Revision: first})
	if err != nil || !reflect.DeepEqual(old.Records, []string{a.RecordJSON, b.RecordJSON}) {
		t.Fatalf("old snapshot: %+v %v", old, err)
	}
	if _, err = s.Put(ctx, PutRequest{ExpectedRevision: &second, Upsert: []Upsert{b, sample("c", "three")}}); err != nil {
		t.Fatal(err)
	}
	current, err := s.Get(ctx, GetRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, raw := range current.Records {
		var r record
		json.Unmarshal([]byte(raw), &r)
		ids = append(ids, r.ID)
	}
	if !reflect.DeepEqual(ids, []string{"a", "b", "c"}) {
		t.Fatal(ids)
	}
	if current.Revision != textHash("["+sample("a", "updated").RecordJSON+","+b.RecordJSON+","+sample("c", "three").RecordJSON+"]") {
		t.Fatal("digest differs from raw TS array")
	}
}

func TestRollbackAndReadOnly(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first, err := s.Put(ctx, PutRequest{Upsert: []Upsert{sample("a", "one")}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Put(ctx, PutRequest{Delete: []string{"a"}, Upsert: []Upsert{{RecordJSON: "invalid"}}})
	if err == nil {
		t.Fatal("malformed write succeeded")
	}
	s.Close()
	s, err = Open(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Get(ctx, GetRequest{})
	if err != nil || got.Revision != first || len(got.Records) != 1 {
		t.Fatal(got, err)
	}
	if _, err = s.Put(ctx, PutRequest{}); err == nil {
		t.Fatal("readonly write succeeded")
	}
	if filepath.Base(dir) == ".mnemon" {
		t.Fatal("invalid test directory")
	}
}
