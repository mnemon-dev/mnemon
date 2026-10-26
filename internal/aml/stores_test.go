package aml

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
)

// The store overview (GET /admin/v1/stores) and the gauges that share its scan
// of the data root. The fixtures are real directories in a temporary data
// root, with files of known sizes and times, and the scan's clock is a
// fakeClock the test moves.

// fixtureBase is the first of the fixtures' times.
var fixtureBase = time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC)

// storeFile is a file of a fixture store.
type storeFile struct {
	name  string
	size  int
	mtime time.Time
}

// makeStore makes the directory of the store with key under root, holding
// files.
func makeStore(t *testing.T, root, key string, files ...storeFile) {
	t.Helper()
	dir := filepath.Join(root, "users", key)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		path := filepath.Join(dir, file.name)
		if err := os.WriteFile(path, bytes.Repeat([]byte("x"), file.size), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, file.mtime, file.mtime); err != nil {
			t.Fatal(err)
		}
	}
}

// fixtureStore is a store of the fixture and what the overview must say of it.
type fixtureStore struct {
	user, key string
	files     []storeFile
	bytes     int64
	modified  time.Time
}

// fixtureStores makes three stores in root, and what the scan must pass over,
// and returns the stores sorted by key. A store's bytes are those of its
// regular files, and its time the newest of theirs.
func fixtureStores(t *testing.T, root string) []fixtureStore {
	t.Helper()
	stores := []fixtureStore{
		{user: "alice@example.test", files: []storeFile{
			{"records.db", 1000, fixtureBase}, {"records.db-wal", 200, fixtureBase.Add(90 * time.Second)}, {"records.db-shm", 32, fixtureBase.Add(30 * time.Second)}}},
		{user: "bob@example.test", files: []storeFile{
			{"records.db", 5000, fixtureBase.Add(10 * time.Minute)}, {"memory-index.db", 700, fixtureBase.Add(5 * time.Minute)}}},
		{user: "carol@example.test", files: []storeFile{{"records.db", 300, fixtureBase.Add(time.Hour)}}},
	}
	for i := range stores {
		s := &stores[i]
		s.key = userKey(s.user)
		for _, file := range s.files {
			s.bytes += int64(file.size)
			if file.mtime.After(s.modified) {
				s.modified = file.mtime
			}
		}
		makeStore(t, root, s.key, s.files...)
	}
	slices.SortFunc(stores, func(a, b fixtureStore) int { return strings.Compare(a.key, b.key) })

	// What the scan passes over: a directory that is no key, a key in capitals,
	// a file named like a key, and in a store what is not one of its files (a
	// directory, a link).
	makeStore(t, root, "not-a-store", storeFile{"records.db", 9999, fixtureBase})
	makeStore(t, root, "ABCDEF00000000000000000000000000", storeFile{"records.db", 9999, fixtureBase})
	if err := os.WriteFile(filepath.Join(root, "users", "0123456789abcdef0123456789abcdef"), bytes.Repeat([]byte("x"), 9999), 0o600); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(root, "users", stores[0].key)
	if err := os.MkdirAll(filepath.Join(inside, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inside, "sub", "big"), bytes.Repeat([]byte("x"), 9999), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Join(root, "users", "not-a-store", "records.db"), filepath.Join(inside, "linked.db")); err != nil {
			t.Fatal(err)
		}
	}
	return stores
}

// storesBody is what /admin/v1/stores answers.
type storesBody struct {
	Total     int         `json:"total"`
	Bytes     int64       `json:"bytes"`
	ScannedAt string      `json:"scannedAt"`
	Stores    []storeBody `json:"stores"`
	Next      *string     `json:"next"`
}

type storeBody struct {
	Store    string `json:"store"`
	Bytes    int64  `json:"bytes"`
	Modified string `json:"modified"`
	Open     bool   `json:"open"`
	Backlog  *int64 `json:"backlog"`
}

// stores asks the overview for query, such as "limit=2&after=…": the status,
// the raw body, and the body decoded when the status is 200.
func (h *harness) stores(t *testing.T, query string) (int, string, storesBody) {
	t.Helper()
	path := "/admin/v1/stores"
	if query != "" {
		path += "?" + query
	}
	response := h.adminCall("GET", path)
	var body storesBody
	if response.Code == http.StatusOK {
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err, response.Body.String())
		}
	}
	return response.Code, response.Body.String(), body
}

// countScans counts, from now on, the scans of the data root the server makes.
func countScans(h *harness) *atomic.Int64 {
	var scans atomic.Int64
	read := h.server.stores.read
	h.server.stores.read = func(ctx context.Context, root string) ([]storeStat, error) {
		scans.Add(1)
		return read(ctx, root)
	}
	return &scans
}

// failScans makes the scans of the data root fail while the returned switch
// is set.
func failScans(h *harness) *atomic.Bool {
	var failing atomic.Bool
	read := h.server.stores.read
	h.server.stores.read = func(ctx context.Context, root string) ([]storeStat, error) {
		if failing.Load() {
			return nil, errors.New("the disk is on fire")
		}
		return read(ctx, root)
	}
	return &failing
}

func TestStoresOverview(t *testing.T) {
	h := newHarness(t, nil)
	clock := newFakeClock()
	h.server.stores.now = clock.now
	fixture := fixtureStores(t, h.root)
	var all int64
	for _, s := range fixture {
		all += s.bytes
	}
	var responses []string
	ask := func(query string) (int, storesBody) {
		t.Helper()
		code, raw, body := h.stores(t, query)
		responses = append(responses, raw)
		return code, body
	}
	wantStore := func(s fixtureStore) storeBody {
		return storeBody{Store: s.key[:8], Bytes: s.bytes, Modified: s.modified.UTC().Format(time.RFC3339)}
	}

	// A page of two: the first two stores by key, their prefix, the sum of
	// their regular files and the newest time among them; the cursor is the
	// last one's whole key. The totals are those of the data root.
	code, page := ask("limit=2")
	if code != http.StatusOK || page.Total != 3 || page.Bytes != all || page.ScannedAt != clock.now().UTC().Format(time.RFC3339) ||
		len(page.Stores) != 2 || page.Next == nil || *page.Next != fixture[1].key {
		t.Fatalf("%d %+v", code, page)
	}
	for i, got := range page.Stores {
		if got != wantStore(fixture[i]) {
			t.Errorf("store %d: %+v, want %+v", i, got, wantStore(fixture[i]))
		}
	}
	if raw := responses[0]; !strings.Contains(raw, `"scannedAt":"2026-10-24T08:00:00Z"`) || !strings.Contains(raw, `"backlog":null`) {
		t.Error(raw)
	}
	if response := h.adminCall("GET", "/admin/v1/stores?limit=1"); response.Header().Get("Content-Type") != "application/json" || response.Header().Get("Cache-Control") != "no-store" {
		t.Error(response.Header())
	}
	// The cursor is exclusive: the last store, and no cursor after it.
	code, page = ask("limit=2&after=" + *page.Next)
	if code != http.StatusOK || page.Total != 3 || page.Bytes != all || len(page.Stores) != 1 || page.Stores[0] != wantStore(fixture[2]) || page.Next != nil {
		t.Fatalf("%d %+v", code, page)
	}
	if raw := responses[len(responses)-1]; !strings.Contains(raw, `"next":null`) {
		t.Error(raw)
	}
	// Without a limit, 100 at most; 1 and 500 are the bounds.
	for query, want := range map[string]int{"": 3, "limit=500": 3, "limit=1": 1} {
		if code, page := ask(query); code != http.StatusOK || len(page.Stores) != want {
			t.Errorf("%q: %d %d stores", query, code, len(page.Stores))
		}
	}
	// A cursor that is no store (one that was removed since, say) is where the
	// order resumes; past the last there is nothing, and that is an empty list.
	between := fixture[0].key[:31] + "f"
	if !(fixture[0].key < between && between < fixture[1].key) {
		t.Fatal("the fixture's keys leave no room for a cursor between the first two:", fixture[0].key, fixture[1].key)
	}
	if code, page := ask("after=" + between); code != http.StatusOK || len(page.Stores) != 2 || page.Stores[0] != wantStore(fixture[1]) || page.Stores[1] != wantStore(fixture[2]) || page.Next != nil {
		t.Errorf("after %s: %d %+v", between, code, page)
	}
	code, page = ask("after=ffffffffffffffffffffffffffffffff")
	if code != http.StatusOK || page.Stores == nil || len(page.Stores) != 0 || page.Next != nil || page.Total != 3 {
		t.Errorf("past the last: %d %+v", code, page)
	}
	if raw := responses[len(responses)-1]; !strings.Contains(raw, `"stores":[]`) || !strings.Contains(raw, `"next":null`) {
		t.Error(raw)
	}

	// A malformed cursor or limit is the client's error, and so is a query
	// whose percent-encoding is malformed: it is not read as a query without
	// that pair (parked T14-b).
	for _, query := range []string{
		"after=xyz", "after=" + fixture[0].key[:31], "after=" + fixture[0].key + "0", "after=ABCDEF00000000000000000000000000",
		"after=" + strings.Repeat("g", 32), "after=", "limit=0", "limit=501", "limit=a", "limit=-1", "limit=", "limit=1.5", "limit=2&after=bad",
		"after=%zz", "limit=%G1", "limit=1&after=%",
	} {
		response := h.adminCall("GET", "/admin/v1/stores?"+query)
		responses = append(responses, response.Body.String())
		var body struct{ Error string }
		if response.Code != http.StatusBadRequest || response.Header().Get("Content-Type") != "application/json" || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Error == "" {
			t.Errorf("%q: %d %q", query, response.Code, response.Body.String())
		}
	}

	// It only reads: a POST is not served.
	if response := h.adminCall("POST", "/admin/v1/stores"); response.Code != http.StatusNotFound {
		t.Errorf("POST: %d", response.Code)
	}

	// No user id, and nothing of what the scan passed over, is in any answer:
	// the id is not even stored.
	for _, raw := range responses {
		for _, secret := range []string{"alice", "bob", "carol", "example.test", "not-a-store", "ABCDEF", "sub", "linked"} {
			if strings.Contains(raw, secret) {
				t.Errorf("an answer shows %q: %s", secret, raw)
			}
		}
	}
}

// Two stores may share a prefix: they are ordered, and paged, by their whole
// keys.
func TestStoresSharingAPrefix(t *testing.T) {
	h := newHarness(t, nil)
	keys := []string{"58e645e0000000000000000000000003", "58e645e0000000000000000000000001", "58e645e0ffffffffffffffffffffffff", "58e645e0000000000000000000000002"}
	for _, key := range keys {
		makeStore(t, h.root, key, storeFile{"records.db", 10, fixtureBase})
	}
	slices.Sort(keys)
	after := ""
	for i, key := range keys {
		query := "limit=1"
		if after != "" {
			query += "&after=" + after
		}
		code, _, page := h.stores(t, query)
		if code != http.StatusOK || page.Total != 4 || len(page.Stores) != 1 || page.Stores[0].Store != "58e645e0" {
			t.Fatalf("page %d: %d %+v", i, code, page)
		}
		if last := i == len(keys)-1; last != (page.Next == nil) || !last && *page.Next != key {
			t.Fatalf("page %d: the cursor is %v, want %s", i, page.Next, key)
		}
		if page.Next != nil {
			after = *page.Next
		}
	}
}

// A query string the overview cannot read is a 400 that says why: a bad
// percent-escape and a ';' between pairs are different faults, and the second
// is not a percent-encoding error (the re-review of the final fix's N7).
func TestStoresOverviewSaysWhyAQueryStringIsMalformed(t *testing.T) {
	h := newHarness(t, nil)
	for _, c := range []struct{ query, mentions, not string }{
		{"after=%zz", "percent-encoding", "';'"},
		{"limit=%G1", "percent-encoding", "';'"},
		{"limit=1&after=%", "percent-encoding", "';'"},
		{"limit=1;after=" + strings.Repeat("0", 32), "';'", "percent-encoding"},
		{"limit=2;limit=3", "';'", "percent-encoding"},
		{"after=abc;", "';'", "percent-encoding"},
		{";", "';'", "percent-encoding"},
	} {
		response := h.adminCall("GET", "/admin/v1/stores?"+c.query)
		var body struct{ Error string }
		if response.Code != http.StatusBadRequest || json.Unmarshal(response.Body.Bytes(), &body) != nil || !strings.HasPrefix(body.Error, "the query string is malformed: ") {
			t.Errorf("%q: %d %q", c.query, response.Code, response.Body.String())
			continue
		}
		if !strings.Contains(body.Error, c.mentions) || strings.Contains(body.Error, c.not) {
			t.Errorf("%q: %q does not name the cause (%s), not %s", c.query, body.Error, c.mentions, c.not)
		}
	}
}

// A data root with no store yet has an overview, and it is empty.
func TestStoresOverviewOfAnEmptyDataRoot(t *testing.T) {
	for name, prepare := range map[string]func(root string){
		"no users directory":    func(string) {},
		"an empty users folder": func(root string) { os.MkdirAll(filepath.Join(root, "users"), 0o700) },
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, nil)
			prepare(h.root)
			code, raw, page := h.stores(t, "")
			if code != http.StatusOK || page.Total != 0 || page.Bytes != 0 || page.Next != nil || !strings.Contains(raw, `"stores":[]`) {
				t.Fatalf("%d %s", code, raw)
			}
		})
	}
}

// A scan serves 60 seconds: a store made after it is not seen sooner. Paging
// reads the same scan.
func TestStoresScanCached(t *testing.T) {
	h := newHarness(t, nil)
	clock := newFakeClock()
	h.server.stores.now = clock.now
	scans := countScans(h)
	makeStore(t, h.root, userKey("first"), storeFile{"records.db", 100, fixtureBase})
	_, _, page := h.stores(t, "")
	if page.Total != 1 || page.Bytes != 100 || scans.Load() != 1 {
		t.Fatalf("%+v after %d scans", page, scans.Load())
	}
	scannedAt := page.ScannedAt

	makeStore(t, h.root, userKey("second"), storeFile{"records.db", 50, fixtureBase})
	clock.advance(59 * time.Second)
	_, _, page = h.stores(t, "")
	if page.Total != 1 || page.Bytes != 100 || page.ScannedAt != scannedAt || scans.Load() != 1 {
		t.Fatalf("at 59 s: %+v after %d scans", page, scans.Load())
	}
	clock.advance(time.Second)
	_, _, page = h.stores(t, "limit=1")
	if page.Total != 2 || page.Bytes != 150 || page.ScannedAt == scannedAt || scans.Load() != 2 || page.Next == nil {
		t.Fatalf("at 60 s: %+v after %d scans", page, scans.Load())
	}
	h.stores(t, "limit=1&after="+*page.Next)
	if scans.Load() != 2 {
		t.Fatal("the second page made a scan:", scans.Load())
	}
}

// Requests that arrive while a scan runs wait for it: one scan answers them
// all.
func TestStoresOneScanForConcurrentRequests(t *testing.T) {
	h := newHarness(t, nil)
	makeStore(t, h.root, userKey("a"), storeFile{"records.db", 10, fixtureBase})
	var scans atomic.Int64
	reading, release := make(chan struct{}, 32), make(chan struct{})
	read := h.server.stores.read
	h.server.stores.read = func(ctx context.Context, root string) ([]storeStat, error) {
		scans.Add(1)
		reading <- struct{}{}
		<-release
		return read(ctx, root)
	}
	const requests = 8
	answers := make([]string, requests)
	var wg sync.WaitGroup
	for i := range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response := h.adminCall("GET", "/admin/v1/stores")
			answers[i] = fmt.Sprint(response.Code, " ", response.Body.String())
		}()
	}
	<-reading                          // a scan is under way…
	time.Sleep(100 * time.Millisecond) // …while the others arrive behind it
	close(release)
	wg.Wait()
	if scans.Load() != 1 {
		t.Fatal("scans:", scans.Load())
	}
	for _, answer := range answers {
		if !strings.HasPrefix(answer, "200 ") || answer != answers[0] {
			t.Fatalf("%q, and %q", answer, answers[0])
		}
	}
}

// The scan runs on the goroutine of the request that started it and stops
// when that request does. A request that waited behind it does not fail with
// it: it scans itself.
func TestStoresScanOutlivesTheCallerThatStartedIt(t *testing.T) {
	h := newHarness(t, nil)
	makeStore(t, h.root, userKey("a"), storeFile{"records.db", 10, fixtureBase})
	var scans atomic.Int64
	entered := make(chan struct{})
	read := h.server.stores.read
	h.server.stores.read = func(ctx context.Context, root string) ([]storeStat, error) {
		if scans.Add(1) == 1 {
			close(entered)
			<-ctx.Done() // the first scan runs until its caller has gone
			return nil, ctx.Err()
		}
		return read(ctx, root)
	}
	call := func(ctx context.Context) <-chan *httptest.ResponseRecorder {
		answered := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			recorder := httptest.NewRecorder()
			h.server.AdminHandler().ServeHTTP(recorder, httptest.NewRequest("GET", "/admin/v1/stores", nil).WithContext(ctx))
			answered <- recorder
		}()
		return answered
	}
	ctx, leave := context.WithCancel(context.Background())
	defer leave()
	leader := call(ctx)
	<-entered
	follower := call(context.Background())
	time.Sleep(50 * time.Millisecond) // the follower waits behind the leader
	leave()
	if got := received(t, follower); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"total":1`) {
		t.Fatalf("the follower: %d %s", got.Code, got.Body.String())
	}
	if got := received(t, leader); got.Code != http.StatusServiceUnavailable {
		t.Fatalf("the leader: %d %s", got.Code, got.Body.String())
	}
	if scans.Load() != 2 {
		t.Fatal("scans:", scans.Load())
	}
}

// Parked T14-a (final review): a caller that waits behind another's scan
// leaves at its own deadline, as a scrape must (scrapeWait), however long the
// scan it waits for runs: here the scan of an overview request, which has no
// deadline of its own. The scan goes on for its own caller, who gets it.
func TestAScanFollowerLeavesAtItsDeadline(t *testing.T) {
	h := newHarness(t, nil)
	makeStore(t, h.root, userKey("a"), storeFile{"records.db", 10, fixtureBase})
	entered, release := make(chan struct{}), make(chan struct{})
	read := h.server.stores.read
	h.server.stores.read = func(ctx context.Context, root string) ([]storeStat, error) {
		close(entered)
		<-release
		return read(ctx, root)
	}
	// A scan held back for longer than the follower waits; it is released
	// at the latest when the test would otherwise hang.
	defer time.AfterFunc(3*time.Second, func() { close(release) }).Stop()
	leader := make(chan *httptest.ResponseRecorder, 1)
	go func() { leader <- h.adminCall("GET", "/admin/v1/stores") }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	scan, err := h.server.stores.scan(ctx)
	if waited := time.Since(start); !errors.Is(err, context.DeadlineExceeded) || scan != nil || waited > time.Second {
		t.Fatal("the follower waited", waited, "and got", scan, err)
	}
	close(release)
	if got := received(t, leader); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"total":1`) {
		t.Fatalf("the leader: %d %s", got.Code, got.Body.String())
	}
}

// countdownContext lets Err say nil a few times and then that the context was
// canceled, and counts the questions.
type countdownContext struct {
	context.Context
	left, asked atomic.Int64
}

func (c *countdownContext) Err() error {
	c.asked.Add(1)
	if c.left.Add(-1) < 0 {
		return context.Canceled
	}
	return nil
}

// The scan asks its context between the entries of the users directory, so a
// scan of a large data root ends when its caller does, not at its end.
func TestReadStoresStopsWhenItsContextEnds(t *testing.T) {
	root := t.TempDir()
	const stores = 12
	for i := range stores {
		makeStore(t, root, userKey(fmt.Sprint("user ", i)), storeFile{"records.db", 10, fixtureBase})
	}
	all, err := readStores(context.Background(), root)
	if err != nil || len(all) != stores || !slices.IsSortedFunc(all, func(a, b storeStat) int { return strings.Compare(a.key, b.key) }) {
		t.Fatalf("%d stores, sorted %v, %v", len(all), slices.IsSortedFunc(all, func(a, b storeStat) int { return strings.Compare(a.key, b.key) }), err)
	}
	ctx := &countdownContext{Context: context.Background()}
	ctx.left.Store(3)
	if _, err := readStores(ctx, root); !errors.Is(err, context.Canceled) || ctx.asked.Load() >= stores {
		t.Fatalf("%v after %d questions", err, ctx.asked.Load())
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readStores(canceled, root); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

// A scan that fails is not kept, and is told once per streak of failures, by
// the store's prefix and never the whole key.
func TestStoreScanFailureIsLoggedOncePerStreak(t *testing.T) {
	h := newHarness(t, nil)
	clock := newFakeClock()
	h.server.stores.now = clock.now
	makeStore(t, h.root, userKey("a"), storeFile{"records.db", 10, fixtureBase})
	failing := failScans(h)
	failing.Store(true)
	told := func() int { return strings.Count(h.log.String(), "store scan failed") }
	for range 3 {
		if code, raw, _ := h.stores(t, ""); code != http.StatusInternalServerError || !strings.Contains(raw, `"error"`) || strings.Contains(raw, "fire") {
			t.Fatalf("%d %s", code, raw)
		}
		h.scrape(t)
	}
	if told() != 1 || !strings.Contains(h.log.String(), "the disk is on fire") {
		t.Fatalf("%d lines: %s", told(), h.log.String())
	}
	// It is not cached: the first scan that can go well does.
	failing.Store(false)
	if code, _, page := h.stores(t, ""); code != http.StatusOK || page.Total != 1 {
		t.Fatalf("after the failure: %d %+v", code, page)
	}
	// A failure after a success is a new streak.
	failing.Store(true)
	clock.advance(time.Minute)
	if code, _, _ := h.stores(t, ""); code != http.StatusInternalServerError || told() != 2 {
		t.Fatalf("%d, %d lines: %s", code, told(), h.log.String())
	}
}

// A store the scan cannot read fails the scan, and the failure names it by its
// prefix only: a log shows a user as nothing more.
func TestStoreScanErrorNamesAStoreByItsPrefix(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not stop this user")
	}
	h := newHarness(t, nil)
	key := userKey("locked")
	makeStore(t, h.root, key, storeFile{"records.db", 10, fixtureBase})
	dir := filepath.Join(h.root, "users", key)
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	code, raw, _ := h.stores(t, "")
	logged := h.log.String()
	if code != http.StatusInternalServerError || !strings.Contains(logged, "store scan failed: store "+key[:8]+": ") || strings.Contains(logged+raw, key) {
		t.Fatalf("%d %s\n%s", code, raw, logged)
	}
}

// open and backlog of the overview: a store the registry holds is open; a user
// the scheduler has measured has its backlog; everything else has none, null
// in the answer, and outside background consolidation nobody does.
func TestStoresOpenAndBacklog(t *testing.T) {
	settings := memoryagent.ConsolidationConfig{Mode: "timelines", BatchCharacters: 300}
	h := newHarness(t, func(config *ServerConfig) {
		config.MemoryConfig.Consolidation = &settings
		config.Consolidation = ConsolidationBackground
	})
	h.consolidator.answer(parityReply)
	busy, onDisk := userKey("u1"), userKey("on disk only")
	makeStore(t, h.root, onDisk, storeFile{"notes.bin", 10, fixtureBase}) // no records.db: nothing opens it
	for i := 1; i <= 3; i++ {
		h.mustAdd(t, addRequest(fmt.Sprintf("r%d", i), "u1"))
	}
	byPrefix := func() map[string]storeBody {
		t.Helper()
		code, raw, page := h.stores(t, "")
		if code != http.StatusOK || len(page.Stores) != 2 {
			t.Fatal(code, raw)
		}
		out := map[string]storeBody{}
		for _, store := range page.Stores {
			out[store.Store] = store
		}
		return out
	}

	// No job has run: the first Add measured the user's backlog, which the
	// scheduler had no measure of (the final review's parked T6-a), and the
	// next two added their characters. The store is open, its backlog known.
	if status := h.server.sched.Status(); status.MaxBacklog == 0 {
		t.Fatal(status)
	}
	stores := byPrefix()
	if got := stores[busy[:8]]; !got.Open || got.Backlog == nil || *got.Backlog <= 0 {
		t.Errorf("before any job: %+v", got)
	}
	if got := stores[onDisk[:8]]; got.Open || got.Backlog != nil {
		t.Errorf("a store nothing opened: %+v", got)
	}

	// The scheduler folds the full batches and measures the tail that is left.
	h.run(t)
	h.settle(t)
	tail := h.backlog(t, "u1").Characters
	if tail <= 0 {
		t.Fatal("the fixture leaves no tail to measure:", tail)
	}
	stores = byPrefix()
	if got := stores[busy[:8]]; !got.Open || got.Backlog == nil || *got.Backlog != tail {
		t.Errorf("after the job (backlog %d): %+v", tail, got)
	}
	if got := stores[onDisk[:8]]; got.Open || got.Backlog != nil {
		t.Errorf("a store with nothing pending: %+v", got)
	}

	// Without background consolidation there is no scheduler to ask.
	plain := newHarness(t, nil)
	plain.mustAdd(t, addRequest("r1", "u1"))
	_, raw, page := plain.stores(t, "")
	if len(page.Stores) != 1 || !page.Stores[0].Open || page.Stores[0].Backlog != nil || !strings.Contains(raw, `"backlog":null`) {
		t.Errorf("%s", raw)
	}
}

// ---- the gauges ----

// The gauges of the stores are those of the overview's total and bytes, with
// no label of any kind; the lag bound is the background Add's bound, 0 when no
// Add waits.
func TestStateGaugesForStoresAndLagBound(t *testing.T) {
	h := newHarness(t, nil)
	fixture := fixtureStores(t, h.root)
	var all int64
	for _, s := range fixture {
		all += s.bytes
	}
	m := h.scrape(t)
	_, _, page := h.stores(t, "")
	m.is(t, float64(page.Total), "mnemon_aml_stores")
	m.is(t, float64(page.Bytes), "mnemon_aml_store_bytes")
	if page.Total != len(fixture) || page.Bytes != all {
		t.Fatalf("%+v, want %d stores of %d bytes", page, len(fixture), all)
	}
	for series := range m {
		for _, name := range []string{"mnemon_aml_stores", "mnemon_aml_store_bytes", "mnemon_aml_consolidation_lag_bound_characters"} {
			if strings.HasPrefix(series, name+"{") {
				t.Errorf("%s has labels", series)
			}
		}
	}
	// A data root that holds no store has zero stores, which is not the same
	// as not knowing.
	empty := newHarness(t, nil).scrape(t)
	empty.is(t, 0, "mnemon_aml_stores")
	empty.is(t, 0, "mnemon_aml_store_bytes")

	bound := func(mode ConsolidationMode) samples {
		return newHarness(t, func(config *ServerConfig) {
			config.MemoryConfig.Consolidation = &memoryagent.ConsolidationConfig{Mode: "layered"}
			config.Consolidation = mode
		}).scrape(t)
	}
	bound(ConsolidationBackground).is(t, 160000, "mnemon_aml_consolidation_lag_bound_characters")
	for _, mode := range []ConsolidationMode{ConsolidationOff, ConsolidationOnAdd} {
		bound(mode).is(t, 0, "mnemon_aml_consolidation_lag_bound_characters")
	}
}

// A scrape never makes a scan the overview's cache could answer.
func TestStoreGaugesUseTheCache(t *testing.T) {
	h := newHarness(t, nil)
	clock := newFakeClock()
	h.server.stores.now = clock.now
	scans := countScans(h)
	makeStore(t, h.root, userKey("a"), storeFile{"records.db", 10, fixtureBase})
	h.scrape(t).is(t, 1, "mnemon_aml_stores")
	clock.advance(59 * time.Second)
	h.scrape(t).is(t, 10, "mnemon_aml_store_bytes")
	if scans.Load() != 1 {
		t.Fatal("two scrapes within 60 s made", scans.Load(), "scans")
	}
	// The overview reads the scan the scrapes made, and they read its.
	if _, _, page := h.stores(t, ""); page.Total != 1 || scans.Load() != 1 {
		t.Fatalf("%+v after %d scans", page, scans.Load())
	}
	makeStore(t, h.root, userKey("b"), storeFile{"records.db", 5, fixtureBase})
	clock.advance(time.Second)
	m := h.scrape(t)
	m.is(t, 2, "mnemon_aml_stores")
	m.is(t, 15, "mnemon_aml_store_bytes")
	if _, _, page := h.stores(t, ""); page.Total != 2 || scans.Load() != 2 {
		t.Fatalf("%+v after %d scans", page, scans.Load())
	}
}

// A scan that fails leaves the gauges of the stores out of the scrape (0 would
// be a claim), and the rest of the scrape goes on.
func TestStoreGaugesLeaveOutAFailedScan(t *testing.T) {
	h := newHarness(t, nil)
	clock := newFakeClock()
	h.server.stores.now = clock.now
	makeStore(t, h.root, userKey("a"), storeFile{"records.db", 10, fixtureBase})
	failing := failScans(h)
	failing.Store(true)
	m := h.scrape(t)
	m.absent(t, "mnemon_aml_stores")
	m.absent(t, "mnemon_aml_store_bytes")
	m.is(t, 0, "mnemon_aml_open_stores")
	m.is(t, 0, "mnemon_aml_consolidation_lag_bound_characters")
	failing.Store(false)
	m = h.scrape(t)
	m.is(t, 1, "mnemon_aml_stores")
	m.is(t, 10, "mnemon_aml_store_bytes")
}
