package records

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// fileState is what a reader must leave as it was: a file's size, time,
// contents and mode.
type fileState struct {
	size  int64
	mtime time.Time
	sum   string
	mode  fs.FileMode
}

// dirState is every file of dir, by name.
func dirState(t *testing.T, dir string) map[string]fileState {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]fileState{}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		out[entry.Name()] = fileState{info.Size(), info.ModTime(), hex.EncodeToString(sum[:]), info.Mode()}
	}
	return out
}

// readOnlyDir takes every write permission away from dir and its files, as
// systemd's ReadOnlyPaths= does for the inspect service, until the test ends.
// A process that already holds a file open for writing keeps writing it.
func readOnlyDir(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes whatever the permissions say")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if err := os.Chmod(filepath.Join(dir, entry.Name()), 0o444); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.Chmod(dir, 0o755)
		entries, _ := os.ReadDir(dir)
		for _, entry := range entries {
			os.Chmod(filepath.Join(dir, entry.Name()), 0o644)
		}
	})
}

// kindRecord is a record of kind in the global scope, active or archived.
func kindRecord(id, kind, state, content string) Upsert {
	raw := `{"id":"` + id + `","kind":"` + kind + `","title":"Title","content":"` + content + `","scope":"global","state":"` + state + `","data":{"session":"s1"},"createdAt":"2026-10-18T00:00:00.000Z"}`
	return Upsert{raw, content, content}
}

// storeWith writes a document and its chunks, then more chunks, and closes
// the store: no writer holds it, and it has no -wal or -shm.
func storeWith(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	mustPut(t, s, PutRequest{Upsert: []Upsert{
		kindRecord("doc-1", "memory-document", "archived", "Caroline went to the support group. The group met on Tuesday."),
		kindRecord("doc-1-00000", "memory-record", "active", "Caroline went to the support group"),
		kindRecord("doc-1-00001", "memory-record", "active", "The group met on Tuesday"),
	}})
	mustPut(t, s, PutRequest{Upsert: []Upsert{
		kindRecord("doc-2", "memory-document", "archived", "Melanie painted a sunrise over the lake."),
		kindRecord("doc-2-00000", "memory-record", "active", "Melanie painted a sunrise over the lake"),
		kindRecord("idx-1", "memory-index", "active", "an index item"),
		kindRecord("old-1", "memory-record", "archived", "an archived record"),
	}})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(filepath.Join(dir, "records.db"+suffix)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("records.db%s outlived the store's close: %v", suffix, err)
		}
	}
	return dir
}

func pageIDs(t *testing.T, records []PageRecord) []string {
	t.Helper()
	ids := []string{}
	for _, record := range records {
		var r struct{ ID string }
		if err := json.Unmarshal([]byte(record.JSON), &r); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.ID)
	}
	return ids
}

// A store no writer holds is read from its main file alone: with no write
// permission anywhere, every read works, and the reader creates, changes and
// checkpoints nothing.
func TestOpenReaderQuiescentStoreNeverWrites(t *testing.T) {
	dir := storeWith(t)
	readOnlyDir(t, dir)
	before := dirState(t, dir)
	ctx := context.Background()

	reader, err := OpenReader(dir)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := reader.Get(ctx, GetRequest{IDs: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := reader.Query(ctx, QueryRequest{Revision: snapshot.Revision, Input: map[string]json.RawMessage{"query": json.RawMessage(`"support group"`), "match": json.RawMessage(`"words"`)}})
	if err != nil || !slices.Equal(result.IDs, []string{"doc-1-00000", "doc-1-00001"}) {
		t.Fatal(result.IDs, err)
	}
	page, more, err := reader.Page(ctx, Scope{}, 0, 10)
	if err != nil || more || !slices.Equal(pageIDs(t, page), []string{"doc-1-00000", "doc-1-00001", "doc-2-00000"}) {
		t.Fatal(pageIDs(t, page), more, err)
	}
	count, ids, err := reader.Tail(ctx, Scope{}, 0, 10)
	if err != nil || count != 3 || len(ids) != 3 {
		t.Fatal(count, ids, err)
	}
	if _, err := reader.LexicalScores(ctx, snapshot.Revision, Scope{}, []string{"support group"}, [][]string{result.IDs}); err != nil {
		t.Fatal(err)
	}
	if err := reader.Unchanged(); err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if after := dirState(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatalf("the reader changed the store's directory:\nbefore %v\nafter  %v", before, after)
	}
}

// With write permission, a mode=ro connection to a WAL database with no -wal
// creates the -wal and -shm and leaves them behind; a reader does not.
func TestOpenReaderCreatesNoFilesEvenWhenItCould(t *testing.T) {
	dir := storeWith(t)
	before := dirState(t, dir)
	reader, err := OpenReader(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Get(context.Background(), GetRequest{}); err != nil {
		t.Fatal(err)
	}
	reader.Close()
	if after := dirState(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatalf("the reader changed the store's directory:\nbefore %v\nafter  %v", before, after)
	}
}

// A store a writer holds open (its -wal and -shm exist) is read live through
// them, even when the reader may write none of the files: it sees every
// commit, and creates nothing.
func TestOpenReaderReadsALiveWriter(t *testing.T) {
	dir := storeWith(t)
	writer, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	ctx := context.Background()
	mustPut(t, writer, PutRequest{Upsert: []Upsert{kindRecord("doc-3-00000", "memory-record", "active", "Caroline adopted a dog")}})
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(filepath.Join(dir, "records.db"+suffix)); err != nil {
			t.Fatalf("an open writer has no records.db%s: %v", suffix, err)
		}
	}
	readOnlyDir(t, dir) // the writer keeps its open descriptors
	names := func() []string {
		entries, _ := os.ReadDir(dir)
		out := []string{}
		for _, entry := range entries {
			out = append(out, entry.Name())
		}
		return out
	}
	files := names()
	reader, err := OpenReader(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for i := range 20 {
		id := fmt.Sprintf("doc-4-%05d", i)
		mustPut(t, writer, PutRequest{Upsert: []Upsert{kindRecord(id, "memory-record", "active", "a walk on the beach")}})
		count, _, err := reader.Tail(ctx, Scope{}, 0, 0)
		if err != nil || count != 4+i+1 {
			t.Fatalf("after commit %d the reader counts %d: %v", i, count, err)
		}
	}
	if err := reader.Unchanged(); err != nil {
		t.Fatal(err)
	}
	if got := names(); !slices.Equal(got, files) {
		t.Fatalf("the reader made files: %v, then %v", files, got)
	}
}

// A store read from its main file alone, which a writer then changes (here
// its close checkpoints into the main file), fails the read's check.
func TestOpenReaderNoticesAQuiescentStoreChanging(t *testing.T) {
	dir := storeWith(t)
	reader, err := OpenReader(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := reader.Get(context.Background(), GetRequest{}); err != nil {
		t.Fatal(err)
	}
	if err := reader.Unchanged(); err != nil {
		t.Fatal(err)
	}
	writer, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	mustPut(t, writer, PutRequest{Upsert: []Upsert{kindRecord("doc-3-00000", "memory-record", "active", "Caroline adopted a dog")}})
	writer.Close()
	if err := reader.Unchanged(); !errors.Is(err, ErrChanged) {
		t.Fatalf("Unchanged after a checkpoint: %v", err)
	}
}

func TestOpenReaderOfNoStore(t *testing.T) {
	dir := t.TempDir()
	if _, err := OpenReader(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatal("OpenReader made files", entries)
	}
}

// OpenReaderDB reads a rollback-journal database (memory-index.db) with
// ordinary read locks, which make no file; a quiescent WAL one by its main
// file alone.
func TestOpenReaderDBModes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plain.db")
	plain, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plain.Exec(`CREATE TABLE t(x); INSERT INTO t VALUES(1)`); err != nil {
		t.Fatal(err)
	}
	plain.Close()
	readOnlyDir(t, dir)
	before := dirState(t, dir)
	db, check, err := OpenReaderDB(path)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM t`).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if err := check(); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if after := dirState(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatalf("the reader changed the directory:\nbefore %v\nafter  %v", before, after)
	}
	if _, _, err := OpenReaderDB(filepath.Join(dir, "missing.db")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
}

// Page lists the heads Heads lists (active, neither documents nor index
// items) in write order, a page at a time.
func TestPage(t *testing.T) {
	dir := storeWith(t)
	reader, err := OpenReader(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	ctx := context.Background()
	first, more, err := reader.Page(ctx, Scope{}, 0, 2)
	if err != nil || !more || !slices.Equal(pageIDs(t, first), []string{"doc-1-00000", "doc-1-00001"}) {
		t.Fatal(pageIDs(t, first), more, err)
	}
	if first[0].Seq >= first[1].Seq || first[0].Version == 0 {
		t.Fatal(first)
	}
	second, more, err := reader.Page(ctx, Scope{}, first[1].Seq, 2)
	if err != nil || more || !slices.Equal(pageIDs(t, second), []string{"doc-2-00000"}) {
		t.Fatal(pageIDs(t, second), more, err)
	}
	if last, more, err := reader.Page(ctx, Scope{}, second[0].Seq, 2); err != nil || more || len(last) != 0 {
		t.Fatal(last, more, err)
	}
	_, heads, err := reader.Heads(ctx, "", Scope{})
	if err != nil {
		t.Fatal(err)
	}
	all, _, err := reader.Page(ctx, Scope{}, 0, 100)
	if err != nil || len(all) != len(heads) {
		t.Fatal(len(all), len(heads), err)
	}
	for i, head := range heads {
		if all[i].Version != head.Version || !strings.Contains(all[i].JSON, `"id":"`+head.ID+`"`) {
			t.Fatal(i, all[i], head)
		}
	}
}

// Tail counts the heads written after a version and names the first ones, in
// version order: what consolidation reads after its watermark.
func TestTail(t *testing.T) {
	dir := storeWith(t)
	reader, err := OpenReader(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	ctx := context.Background()
	_, heads, err := reader.Heads(ctx, "", Scope{})
	if err != nil {
		t.Fatal(err)
	}
	count, ids, err := reader.Tail(ctx, Scope{}, heads[0].Version, 1)
	if err != nil || count != 2 || !slices.Equal(ids, []string{heads[1].ID}) {
		t.Fatal(count, ids, err)
	}
	if count, ids, err := reader.Tail(ctx, Scope{}, heads[2].Version, 10); err != nil || count != 0 || len(ids) != 0 {
		t.Fatal(count, ids, err)
	}
}

// LexicalScores are the words scores the ranking sorts by: the query's
// ranked ids have positive, non-increasing scores, and an id it did not rank
// scores 0.
func TestLexicalScoresFollowTheRanking(t *testing.T) {
	dir := storeWith(t)
	reader, err := OpenReader(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	ctx := context.Background()
	snapshot, err := reader.Get(ctx, GetRequest{IDs: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	queries := []string{"Caroline support group Tuesday", "sunrise lake"}
	var ranked [][]string
	for _, query := range queries {
		text, _ := json.Marshal(query)
		result, err := reader.Query(ctx, QueryRequest{Revision: snapshot.Revision, Input: map[string]json.RawMessage{"query": text, "match": json.RawMessage(`"words"`)}})
		if err != nil || len(result.IDs) == 0 {
			t.Fatal(result.IDs, err)
		}
		ranked = append(ranked, append(slices.Clone(result.IDs), "doc-404"))
	}
	scores, err := reader.LexicalScores(ctx, snapshot.Revision, Scope{}, queries, ranked)
	if err != nil {
		t.Fatal(err)
	}
	for q := range queries {
		got := scores[q]
		if len(got) != len(ranked[q]) || got[len(got)-1] != 0 {
			t.Fatal(q, got)
		}
		for i := 0; i < len(got)-1; i++ {
			if got[i] <= 0 || i > 0 && got[i] > got[i-1] {
				t.Fatalf("query %q: scores %v do not follow the ranking %v", queries[q], got, ranked[q])
			}
		}
	}
	// The same scores with the store's query runtime: its prepared and cached
	// indexes change nothing.
	writer, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	stop, err := writer.StartRuntime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	again, err := writer.LexicalScores(ctx, snapshot.Revision, Scope{}, queries, ranked)
	if err != nil || !reflect.DeepEqual(again, scores) {
		t.Fatal(again, scores, err)
	}
}

// A reader store runs no query runtime: its reader pool would not follow the
// reader's rules.
func TestReaderRunsNoRuntime(t *testing.T) {
	reader, err := OpenReader(storeWith(t))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := reader.StartRuntime(context.Background()); err == nil {
		t.Fatal("a reader started a query runtime")
	}
}
