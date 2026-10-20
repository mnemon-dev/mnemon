package aml

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
)

// timelinesHarness consolidates on Add in timelines mode with batches of
// batch units. Each addRequest stores one record the job reads as 171 units.
func timelinesHarness(t *testing.T, batch int) *harness {
	t.Helper()
	return newHarness(t, func(config *ServerConfig) {
		config.MemoryConfig.Consolidation = &memoryagent.ConsolidationConfig{Mode: "timelines", BatchCharacters: batch}
		config.ConsolidateOnAdd = true
	})
}

// eventually waits up to five seconds for done.
func eventually(t *testing.T, done func() bool) {
	t.Helper()
	for start := time.Now(); !done(); time.Sleep(5 * time.Millisecond) {
		if time.Since(start) > 5*time.Second {
			t.Fatal("timed out")
		}
	}
}

const question = "When did Caroline go to the LGBTQ support group?"

func TestAddFoldsFullBatchesSearchFlushesTail(t *testing.T) {
	// Two records fill a batch of 300.
	h := timelinesHarness(t, 300)
	h.mustAdd(t, addRequest("r1", "u1"))
	if n := h.consolidator.calls.Load(); n != 0 {
		t.Fatal("an Add folded its short tail", n)
	}
	h.mustAdd(t, addRequest("r2", "u1"))
	if n := h.consolidator.calls.Load(); n != 1 {
		t.Fatal("an Add did not fold a full batch", n)
	}
	h.mustAdd(t, addRequest("r3", "u1"))
	if n := h.consolidator.calls.Load(); n != 1 {
		t.Fatal(n)
	}
	// The next Search folds the tail, then reads it.
	items := h.search(t, SearchRequest{Query: question, UserID: "u1", TopK: 100})
	if n := h.consolidator.calls.Load(); n != 2 || len(items) == 0 || !strings.HasPrefix(items[0].ID, "index:") {
		t.Fatal(n, items)
	}
	// With nothing pending, a Search folds nothing.
	h.search(t, SearchRequest{Query: question, UserID: "u1", TopK: 100})
	if n := h.consolidator.calls.Load(); n != 2 || strings.Contains(h.log.String(), "failed") {
		t.Fatal(n, h.log.String())
	}
}

func TestSearchFlushFailureStillReads(t *testing.T) {
	h := timelinesHarness(t, 300)
	h.mustAdd(t, addRequest("r1", "u1"))
	h.consolidator.answer(func(context.Context, string) (string, error) {
		return "", errors.New("model HTTP 500; reservation released")
	})
	items := h.search(t, SearchRequest{Query: question, UserID: "u1", TopK: 100})
	if len(items) == 0 || strings.HasPrefix(items[0].ID, "index:") || h.server.DegradedSearches() != 0 {
		t.Fatal(items, h.log.String())
	}
	logged := h.log.String()
	if !strings.Contains(logged, "flush failed (user "+userKey("u1")[:8]+"): model HTTP 500") {
		t.Fatal(logged)
	}
	// A flush that runs past its window stops; the Search reads on.
	h.server.flushWindow = 50 * time.Millisecond
	h.consolidator.answer(func(ctx context.Context, _ string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	start := time.Now()
	if items = h.search(t, SearchRequest{Query: question, UserID: "u1", TopK: 100}); len(items) == 0 || time.Since(start) > 5*time.Second {
		t.Fatal(items, time.Since(start))
	}
	if !strings.Contains(h.log.String(), "context deadline exceeded") {
		t.Fatal(h.log.String())
	}
}

func TestAddConsolidationWindowStartsAtArrival(t *testing.T) {
	// Every record fills a batch of 100.
	h := timelinesHarness(t, 100)
	h.server.addWindow = 0
	h.mustAdd(t, addRequest("r1", "u1"))
	if n := h.consolidator.calls.Load(); n != 0 || !strings.Contains(h.log.String(), "consolidation skipped: the Add's window has passed (user "+userKey("u1")[:8]+")") {
		t.Fatal(n, h.log.String())
	}
	// The window counts from the Add's arrival, not from the user's lock.
	h.server.addWindow = 200 * time.Millisecond
	user, err := h.server.users.acquire(context.Background(), "u1", false)
	if err != nil || user == nil {
		t.Fatal(err)
	}
	user.writes.Lock()
	done := make(chan int)
	go func() { done <- h.call("POST", "/add", addRequest("r2", "u1")).Code }()
	eventually(t, func() bool { return user.waiting.Load() == 1 })
	time.Sleep(300 * time.Millisecond)
	user.writes.Unlock()
	h.server.users.release(user)
	if code := <-done; code != http.StatusOK || h.consolidator.calls.Load() != 0 || strings.Count(h.log.String(), "consolidation skipped") != 2 {
		t.Fatal(code, h.consolidator.calls.Load(), h.log.String())
	}
	// The next Add in time folds what the others left.
	h.server.addWindow = 25 * time.Minute
	h.mustAdd(t, addRequest("r3", "u1"))
	if n := h.consolidator.calls.Load(); n != 3 {
		t.Fatal(n)
	}
}

func TestAddLeavesConsolidationToQueuedAdd(t *testing.T) {
	h := timelinesHarness(t, 100)
	h.mustAdd(t, addRequest("r0", "u1"))
	if n := h.consolidator.calls.Load(); n != 1 {
		t.Fatal(n)
	}
	user, err := h.server.users.acquire(context.Background(), "u1", false)
	if err != nil || user == nil {
		t.Fatal(err)
	}
	user.writes.Lock()
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i, id := range []string{"r1", "r2"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = h.call("POST", "/add", addRequest(id, "u1")).Code
		}()
	}
	eventually(t, func() bool { return user.waiting.Load() == 2 })
	user.writes.Unlock()
	h.server.users.release(user)
	wg.Wait()
	// The first to write leaves its record to the one queued behind it,
	// which folds both.
	if codes[0] != http.StatusOK || codes[1] != http.StatusOK || h.consolidator.calls.Load() != 3 {
		t.Fatal(codes, h.consolidator.calls.Load())
	}
	if n := strings.Count(h.log.String(), "consolidation left to a queued Add (user "+userKey("u1")[:8]+")"); n != 1 {
		t.Fatal(h.log.String())
	}
}

func TestConsolidationDropsAndPanicsAreLogged(t *testing.T) {
	h := timelinesHarness(t, 100)
	h.consolidator.answer(func(context.Context, string) (string, error) { return "no JSON here", nil })
	h.mustAdd(t, addRequest("r1", "u1"))
	prefix := userKey("u1")[:8]
	if !strings.Contains(h.log.String(), "consolidation dropped 1 records (user "+prefix+"): the model's reply did not parse twice") {
		t.Fatal(h.log.String())
	}
	// A consolidation that panics fails like one that errs: the Add's
	// records are stored, so the Add succeeds.
	h.consolidator.answer(func(context.Context, string) (string, error) { panic("fold bug") })
	h.mustAdd(t, addRequest("r2", "u1"))
	logged := h.log.String()
	if !strings.Contains(logged, "consolidation panicked (user "+prefix+"): fold bug") || !strings.Contains(logged, "goroutine ") ||
		!strings.Contains(logged, "consolidation failed (user "+prefix+")") || strings.Contains(logged, "Caroline") {
		t.Fatal(logged)
	}
	// The lock the panicking job held is free again.
	h.consolidator.answer(func(_ context.Context, prompt string) (string, error) {
		return `{"events":[{"thread":"new: Group","date":"2023/05/08","text":"Caroline went.","records":["r0"]}]}`, nil
	})
	done := make(chan int, 1)
	go func() { done <- h.call("POST", "/add", addRequest("r3", "u1")).Code }()
	select {
	case code := <-done:
		if code != http.StatusOK {
			t.Fatal(code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the timelines lock stayed held after a panic")
	}
}

func TestLayeredModeFoldsLikeTimelines(t *testing.T) {
	// Two records fill a batch of 300, as in timelines mode.
	h := newHarness(t, func(config *ServerConfig) {
		config.MemoryConfig.Consolidation = &memoryagent.ConsolidationConfig{Mode: "layered", BatchCharacters: 300}
		config.ConsolidateOnAdd = true
	})
	h.mustAdd(t, addRequest("r1", "u1"))
	if n := h.consolidator.calls.Load(); n != 0 {
		t.Fatal("an Add folded its short tail", n)
	}
	h.mustAdd(t, addRequest("r2", "u1"))
	h.mustAdd(t, addRequest("r3", "u1"))
	if n := h.consolidator.calls.Load(); n != 1 {
		t.Fatal("Adds did not fold exactly the full batch", n)
	}
	// The next Search folds the tail, links it and reads the layer.
	items := h.search(t, SearchRequest{Query: question, UserID: "u1", TopK: 100})
	if n := h.consolidator.calls.Load(); n != 2 || len(items) == 0 || !strings.HasPrefix(items[0].ID, "index:") || strings.Contains(h.log.String(), "failed") {
		t.Fatal(n, items, h.log.String())
	}
}

// derivedGeneration is the derived-metadata generation of the one user's
// records, "" before any was written.
func derivedGeneration(t *testing.T, root string) string {
	t.Helper()
	var path string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Name() == "records.db" {
			path = p
		}
		return nil
	})
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value string
	err = db.QueryRow(`SELECT value FROM meta WHERE key='derived'`).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestNativeEdgesRebuiltOncePerConsolidation(t *testing.T) {
	h := newHarness(t, func(config *ServerConfig) {
		config.MemoryConfig.Consolidation = &memoryagent.ConsolidationConfig{Mode: "layered", BatchCharacters: 300, MaxBatches: 1, NativeEdges: true}
		config.ConsolidateOnAdd = true
	})
	// Six long messages: six records, each a full batch, one job each.
	long := addRequest("r1", "u1")
	long.Messages = nil
	for i := range 6 {
		long.Messages = append(long.Messages, Message{Role: "user", Content: fmt.Sprintf("Note %d: ", i) + strings.Repeat("Caroline talked about the support group again. ", 10), Timestamp: at(i)})
	}
	h.mustAdd(t, long)
	if n := h.consolidator.calls.Load(); n < 2 {
		t.Fatal("the Add folded fewer than two jobs", n)
	}
	// The jobs defer their edges; the Add rebuilds them once.
	if g := derivedGeneration(t, h.root); g != "1" {
		t.Fatal("rebuilds", g, h.log.String())
	}
	// An Add that folds nothing rebuilds nothing.
	h.mustAdd(t, addRequest("r2", "u1"))
	if g := derivedGeneration(t, h.root); g != "1" {
		t.Fatal("rebuilds", g)
	}
	// The Search's flush folds the tail and rebuilds once more; with
	// nothing pending, a Search rebuilds nothing.
	h.search(t, SearchRequest{Query: question, UserID: "u1", TopK: 100})
	if g := derivedGeneration(t, h.root); g != "2" {
		t.Fatal("rebuilds", g)
	}
	h.search(t, SearchRequest{Query: question, UserID: "u1", TopK: 100})
	if g := derivedGeneration(t, h.root); g != "2" || strings.Contains(h.log.String(), "failed") {
		t.Fatal("rebuilds", g, h.log.String())
	}
}
