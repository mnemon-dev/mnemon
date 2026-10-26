package memoryagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// consolidatedStore is a store of five notes whose consolidated memory is
// traceMemory, with the third note read but held back (pending) and the last
// two written after the watermark. The store is closed: it is quiescent.
func consolidatedStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	s, err := records.Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	seedNotes(t, s, "note", 5)
	seedNote(t, s, "orbit", "the mission codename is ORBIT")
	_, heads, err := s.Heads(context.Background(), "", records.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	overlay, err := OpenOverlay(filepath.Join(dir, "memory-index.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	state := emptyTimelineState()
	state.Memory = traceMemory()
	state.Memory.Facts["pet · name"] = []factValue{{Value: "Rex", Date: "2024/01/01"}, {Value: "Rex", Date: "2024/02/01"}, {Value: "Max", Date: "2024/03/01"}}
	state.Memory.FactOrder = append(state.Memory.FactOrder, "pet · name")
	state.Memory.Events[1].Entities = []string{"Tuesday"}
	state.Watermark = heads[2].Version
	state.Pending = []pendingRecord{{ID: heads[2].ID, Header: "2024/01/01", Text: "note 2"}}
	if _, err := overlay.saveTimelines(context.Background(), records.Scope{}, "", state); err != nil {
		t.Fatal(err)
	}
	overlay.Close()
	s.Close()
	return dir
}

func readerConfig() Config {
	return Config{Version: 1, Strategy: "simple", View: &ViewConfig{MaxItems: 16, MaxCharacters: 12000}, Consolidation: &ConsolidationConfig{Mode: "timelines"}}
}

// readerAgent opens the store in dir as the inspect service does: a records
// reader and an agent over it.
func readerAgent(t *testing.T, dir string, config Config) (*records.Store, *Agent) {
	t.Helper()
	store, err := records.OpenReader(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	a, err := OpenReader(store, dir, config, Models{Planner: replies("note", "NEED: the codename", ""), Judge: decisionFunc(fixtureJudge)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return store, a
}

// The consolidated memory as the inspect service pages it: its items with
// ids, the facts' conflicts and supersedes, and the records not folded yet,
// counted as Backlog counts them and named in the order a job reads them.
func TestConsolidated(t *testing.T) {
	dir := consolidatedStore(t)
	store, a := readerAgent(t, dir, readerConfig())
	ctx := context.Background()
	_, heads, err := store.Heads(ctx, "", records.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	c, ok, err := a.Consolidated(ctx, records.Scope{}, 3)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if c.Watermark != heads[2].Version || c.Pending != 4 || !slices.Equal(c.PendingIDs, []string{heads[2].ID, heads[3].ID, heads[4].ID}) {
		t.Fatal(c.Watermark, c.Pending, c.PendingIDs)
	}
	backlog, err := a.Backlog(ctx, records.Scope{})
	if err != nil || backlog.Records != c.Pending {
		t.Fatal(backlog, err)
	}
	if want := []MemoryThread{{ID: "T1", Title: "Mission", Events: 1, First: "2024/01/01", Last: "2024/01/01"}, {ID: "T2", Title: "Launch", Events: 1, First: "2024/01/02", Last: "2024/01/02"}}; !reflect.DeepEqual(c.Threads, want) {
		t.Fatalf("%+v", c.Threads)
	}
	if want := []MemoryEvent{
		{ID: "e0", Thread: "T1", Date: "2024/01/01", Text: "The mission was named ORBIT.", Records: []string{"orbit"}},
		{ID: "e1", Thread: "T2", Date: "2024/01/02", Text: "Lift-off is planned for Tuesday.", Records: []string{"liftoff"}, Entities: []string{"Tuesday"}},
	}; !reflect.DeepEqual(c.Events, want) {
		t.Fatalf("%+v", c.Events)
	}
	if want := []MemoryFact{
		{Key: "mission · codename", Values: []MemoryValue{
			{Value: "ORBIT", Date: "2024/01/01", Records: []string{"orbit"}, Supersedes: -1},
			{Value: "ORBIT-2", Date: "2024/01/05", Records: []string{"mission-001"}, Conflict: true, Supersedes: 0},
		}},
		{Key: "pet · name", Values: []MemoryValue{
			{Value: "Rex", Date: "2024/01/01", Records: []string{}, Supersedes: -1},
			{Value: "Rex", Date: "2024/02/01", Records: []string{}, Supersedes: -1},
			{Value: "Max", Date: "2024/03/01", Records: []string{}, Supersedes: 1},
		}},
	}; !reflect.DeepEqual(c.Facts, want) {
		t.Fatalf("%+v", c.Facts)
	}
	if want := []MemoryDirective{{ID: "d0", Date: "2024/01/01", Text: "Keep it short.", Records: []string{"pref"}}}; !reflect.DeepEqual(c.Directives, want) {
		t.Fatalf("%+v", c.Directives)
	}

	// Without timelines consolidation there is nothing to show.
	config := readerConfig()
	config.Consolidation = nil
	_, plain := readerAgent(t, dir, config)
	if _, ok, err := plain.Consolidated(ctx, records.Scope{}, 3); ok || err != nil {
		t.Fatal(ok, err)
	}
}

// spoilMemory rewrites the scope's stored state in the index at path with a
// memory that does not decode, its digest kept right.
func spoilMemory(t *testing.T, path string) {
	t.Helper()
	writer, err := OpenOverlay(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	var raw string
	if err := writer.db.QueryRow(`SELECT payload FROM timelines WHERE scope=?`, scopeKey(records.Scope{})).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}
	payload["memory"] = json.RawMessage(`{"threads":7}`)
	spoiled, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.db.Exec(`UPDATE timelines SET payload=?,token=? WHERE scope=?`, string(spoiled), reference("timelines", string(spoiled)).Hash, scopeKey(records.Scope{})); err != nil {
		t.Fatal(err)
	}
}

// Progress is what a store row shows: the records Page lists, and the
// watermark and pending count Consolidated reports, read without decoding
// the consolidated memory: a memory that does not decode leaves it whole.
// The state's digest and format are checked all the same.
func TestProgress(t *testing.T) {
	dir := consolidatedStore(t)
	store, a := readerAgent(t, dir, readerConfig())
	ctx := context.Background()
	c, _, err := a.Consolidated(ctx, records.Scope{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	page, _, err := store.Page(ctx, records.Scope{}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	want := Progress{Records: len(page), Consolidates: true, Watermark: c.Watermark, Pending: c.Pending}
	if p, err := a.Progress(ctx, records.Scope{}); err != nil || p != want || want.Pending != 4 || want.Watermark == 0 {
		t.Fatalf("%+v, want %+v: %v", p, want, err)
	}

	index := filepath.Join(dir, "memory-index.db")
	spoilMemory(t, index)
	if _, _, err := a.Consolidated(ctx, records.Scope{}, 0); err == nil {
		t.Fatal("the spoiled memory decodes")
	}
	if p, err := a.Progress(ctx, records.Scope{}); err != nil || p != want {
		t.Fatalf("%+v, want %+v: %v", p, want, err)
	}
	writer, err := OpenOverlay(index, false)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := writer.db.Exec(`UPDATE timelines SET token='0'`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Progress(ctx, records.Scope{}); err == nil {
		t.Fatal("a state whose digest changed")
	}

	// Without consolidation, the records alone.
	config := readerConfig()
	config.Consolidation = nil
	_, plain := readerAgent(t, dir, config)
	if p, err := plain.Progress(ctx, records.Scope{}); err != nil || p != (Progress{Records: len(page)}) {
		t.Fatalf("%+v %v", p, err)
	}
}

// A store not consolidated yet (no memory-index.db): every record is
// pending.
func TestConsolidatedBeforeTheFirstJob(t *testing.T) {
	dir := t.TempDir()
	s, err := records.Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	seedNotes(t, s, "note", 3)
	s.Close()
	_, a := readerAgent(t, dir, readerConfig())
	c, ok, err := a.Consolidated(context.Background(), records.Scope{}, 100)
	if err != nil || !ok || c.Watermark != 0 || c.Pending != 3 || len(c.PendingIDs) != 3 || len(c.Events) != 0 {
		t.Fatalf("%+v %v %v", c, ok, err)
	}
	if p, err := a.Progress(context.Background(), records.Scope{}); err != nil || p != (Progress{Records: 3, Consolidates: true, Pending: 3}) {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "memory-index.db")); !os.IsNotExist(err) {
		t.Fatal("the reader made the derived index", err)
	}
}

// fileSums are the contents of every file of dir.
func fileSums(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		info, _ := entry.Info()
		sum := sha256.Sum256(raw)
		out[entry.Name()] = hex.EncodeToString(sum[:]) + " " + info.ModTime().String()
	}
	return out
}

// An agent opened as a reader reads and composes over a store it may not
// write, and leaves every file as it was.
func TestOpenReaderAgentNeverWrites(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("POSIX permissions, which root ignores")
	}
	dir := consolidatedStore(t)
	before := fileSums(t, dir)
	for _, name := range []string{"records.db", "memory-index.db"} {
		os.Chmod(filepath.Join(dir, name), 0o444)
	}
	os.Chmod(dir, 0o555)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	_, a := readerAgent(t, dir, readerConfig())
	ctx := context.Background()
	view, err := a.Read(ctx, ReadRequest{Question: "What is the mission codename?"})
	if err != nil || len(view.Records) == 0 || len(view.Index) == 0 {
		t.Fatal(view, err)
	}
	if _, _, err := a.Consolidated(ctx, records.Scope{}, 10); err != nil {
		t.Fatal(err)
	}
	if err := a.Unchanged(); err != nil {
		t.Fatal(err)
	}
	a.Close()
	if after := fileSums(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatalf("the reader changed the store:\n%v\n%v", before, after)
	}
}

// The derived index is a rollback-journal file, read live with read locks:
// a reader agent sees what a writer commits at its next read, and has nothing
// to report from Unchanged.
func TestOpenReaderAgentReadsTheIndexLive(t *testing.T) {
	dir := consolidatedStore(t)
	_, a := readerAgent(t, dir, readerConfig())
	ctx := context.Background()
	before, _, err := a.Consolidated(ctx, records.Scope{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := OpenOverlay(filepath.Join(dir, "memory-index.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	state, token, err := writer.loadTimelines(ctx, records.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	state.Memory.Directives = append(state.Memory.Directives, directive{Text: "Answer in French.", Date: "2024/04/01"})
	if _, err := writer.saveTimelines(ctx, records.Scope{}, token, state); err != nil {
		t.Fatal(err)
	}
	after, _, err := a.Consolidated(ctx, records.Scope{}, 10)
	if err != nil || len(after.Directives) != len(before.Directives)+1 || after.Directives[1].ID != "d1" {
		t.Fatalf("%+v %v", after.Directives, err)
	}
	if err := a.Unchanged(); err != nil {
		t.Fatal(err)
	}
}
