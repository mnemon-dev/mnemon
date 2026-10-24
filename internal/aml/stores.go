package aml

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

// The store overview: what the console reads of the data root. A scan lists
// the user stores under <data-root>/users (the directories named by a user key,
// isUserKey) with the bytes of their files and the newest time among them. The
// scan is kept for storesCacheFor and shared by whoever asks meanwhile: the
// overview (GET /admin/v1/stores) and the store gauges of a scrape. It names a
// user by the first 8 digits of the key only, as the logs do, and shows no
// content: the user id is not stored anywhere to be shown.

const (
	// storesCacheFor is how long a scan answers.
	storesCacheFor = 60 * time.Second
	// The overview's page size: the default, and the most a request may ask.
	defaultStoresPage = 100
	maxStoresPage     = 500
	// storeScanTries bounds how many scans a request starts or joins while the
	// requests that started them leave before they end.
	storeScanTries = 3
)

// storeStat is what a scan found of one user store.
type storeStat struct {
	key      string    // the store's key (userKey): the name of its directory
	bytes    int64     // the sizes of the regular files in the directory
	modified time.Time // the newest of their modification times
}

// storeScan is a scan of the data root: its stores in key order, the sum of
// their bytes and when it ended. It is not changed once made.
type storeScan struct {
	at     time.Time
	stores []storeStat
	bytes  int64
}

// storeScanner keeps the latest scan of the data root and makes the next one
// when it is older than storesCacheFor. Callers that come while a scan runs
// wait for it (singleflight). A scan runs on the goroutine of the caller that
// starts it, ends with that caller's context, and holds no lock while it
// reads the disk.
type storeScanner struct {
	root string
	now  func() time.Time
	// read lists the stores under root (readStores); tests wrap it.
	read func(ctx context.Context, root string) ([]storeStat, error)
	// logf tells of a scan that failed; it must not block.
	logf func(format string, args ...any)

	flight singleflight.Group
	// failing says the last scan failed: another failure is no news.
	failing atomic.Bool

	mu   sync.Mutex
	last *storeScan // guarded by mu
}

func newStoreScanner(root string, now func() time.Time, logf func(format string, args ...any)) *storeScanner {
	return &storeScanner{root: root, now: now, read: readStores, logf: logf}
}

// scan is the latest scan while it is younger than storesCacheFor, else a new
// one. A caller that waited for another's scan, and finds that it ended
// because that caller left, scans again (at most storeScanTries times in all)
// rather than fail with a context that is not its own.
func (c *storeScanner) scan(ctx context.Context) (*storeScan, error) {
	var err error
	for range storeScanTries {
		if scan := c.cached(); scan != nil {
			return scan, nil
		}
		var result any
		var shared bool
		result, err, shared = c.flight.Do("scan", func() (any, error) {
			// A scan may have ended while this call waited its turn.
			if scan := c.cached(); scan != nil {
				return scan, nil
			}
			return c.run(ctx)
		})
		if err == nil {
			return result.(*storeScan), nil
		}
		if !shared || ctx.Err() != nil || !endedWithItsContext(err) {
			return nil, err
		}
	}
	return nil, err
}

// endedWithItsContext reports whether err is the end of a context.
func endedWithItsContext(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// cached is the latest scan while it is younger than storesCacheFor, else nil.
func (c *storeScanner) cached() *storeScan {
	now := c.now() // before the lock: now is a callback
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last != nil && now.Sub(c.last.at) < storesCacheFor {
		return c.last
	}
	return nil
}

// run scans the data root and keeps the result. A failure is told once per
// streak of failures, a caller that left is no failure; none is kept.
func (c *storeScanner) run(ctx context.Context) (*storeScan, error) {
	stores, err := c.read(ctx, c.root)
	if err != nil {
		if !errors.Is(err, context.Canceled) && !c.failing.Swap(true) {
			c.logf("store scan failed: %v", err)
		}
		return nil, err
	}
	c.failing.Store(false)
	scan := &storeScan{at: c.now(), stores: stores}
	for _, store := range stores {
		scan.bytes += store.bytes
	}
	c.mu.Lock()
	c.last = scan
	c.mu.Unlock()
	return scan, nil
}

// readStores lists the user stores under <root>/users in key order, with the
// size of each. It asks ctx between the entries of the directory, so a scan of
// a large data root ends soon after its caller does. An error names a store by
// its prefix only, never by the path that holds its whole key.
func readStores(ctx context.Context, root string) ([]storeStat, error) {
	users := filepath.Join(root, "users")
	// ReadDir sorts by name, and the names are keys: the overview's order.
	entries, err := os.ReadDir(users)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil // nobody has added anything yet
	}
	if err != nil {
		return nil, err
	}
	stores := make([]storeStat, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() || !isUserKey(entry.Name()) {
			continue
		}
		store, err := statStore(filepath.Join(users, entry.Name()), entry)
		if errors.Is(err, fs.ErrNotExist) {
			continue // removed since the listing
		}
		if err != nil {
			return nil, fmt.Errorf("store %s: %w", short(entry.Name()), withoutPath(err))
		}
		stores = append(stores, store)
	}
	return stores, nil
}

// statStore measures the store in dir, the directory entry entry names: the
// sizes of the regular files in it, and the newest of their modification
// times (the directory's own when it holds none). A directory or a link in it
// is no file of the store.
func statStore(dir string, entry fs.DirEntry) (storeStat, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return storeStat{}, err
	}
	store := storeStat{key: entry.Name()}
	for _, file := range files {
		if !file.Type().IsRegular() {
			continue
		}
		info, err := file.Info()
		if errors.Is(err, fs.ErrNotExist) {
			continue // SQLite removes the -wal and -shm files of a database it closes
		}
		if err != nil {
			return storeStat{}, err
		}
		store.bytes += info.Size()
		if info.ModTime().After(store.modified) {
			store.modified = info.ModTime()
		}
	}
	if store.modified.IsZero() {
		info, err := entry.Info()
		if err != nil {
			return storeStat{}, err
		}
		store.modified = info.ModTime()
	}
	return store, nil
}

// withoutPath is err without the *fs.PathError that names the path it failed
// on: the path of a store holds its whole key.
func withoutPath(err error) error {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err
	}
	return err
}

// storesPage is what GET /admin/v1/stores answers: the totals of the whole
// data root, and a page of its stores in key order. Next is the cursor of the
// next page, the last store's whole key, and null on the last page.
type storesPage struct {
	Total     int          `json:"total"`
	Bytes     int64        `json:"bytes"`
	ScannedAt string       `json:"scannedAt"`
	Stores    []storeEntry `json:"stores"`
	Next      *string      `json:"next"`
}

// storeEntry is a store of a page, by the first 8 digits of its key. Open says
// the registry holds it open now; Backlog is the scheduler's estimate of its
// pending characters, null when it has none.
type storeEntry struct {
	Store    string `json:"store"`
	Bytes    int64  `json:"bytes"`
	Modified string `json:"modified"`
	Open     bool   `json:"open"`
	Backlog  *int64 `json:"backlog"`
}

// serveStores answers GET /admin/v1/stores?limit=<1..500, default 100>&after=<cursor>
// from the latest scan of the data root. A malformed limit or cursor is a 400.
func (s *Server) serveStores(w http.ResponseWriter, r *http.Request) {
	limit, after, err := storesQuery(r.URL.Query(), defaultStoresPage)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	scan, err := s.stores.scan(r.Context())
	if err != nil {
		// The scanner has told of it, once per streak.
		if retryable(err) {
			writeError(w, http.StatusServiceUnavailable, "the store scan is temporarily unavailable; retry")
			return
		}
		writeError(w, http.StatusInternalServerError, "the store scan failed")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, s.storesPage(scan, limit, after))
}

// fieldError is the error of a malformed field of a request: its message is
// what the admin listener answers 400 with, field the field the inspect
// service names.
type fieldError struct{ field, message string }

func (e *fieldError) Error() string { return e.message }

// storesQuery reads the limit (fallback when absent) and the cursor of a
// store page request (the overview's, the inspect service's); its error is a
// *fieldError.
func storesQuery(query url.Values, fallback int) (limit int, after string, err error) {
	limit = fallback
	if query.Has("limit") {
		limit, err = strconv.Atoi(query.Get("limit"))
		if err != nil || limit < 1 || limit > maxStoresPage {
			return 0, "", &fieldError{"limit", fmt.Sprintf("limit must be a whole number from 1 to %d", maxStoresPage)}
		}
	}
	if query.Has("after") {
		if after = query.Get("after"); !isUserKey(after) {
			return 0, "", &fieldError{"after", "after must be a cursor the overview gave: 32 lowercase hex digits"}
		}
	}
	return limit, after, nil
}

// storesAfter is the page of stores (in key order) that follows the cursor
// after (exclusive; "" for the first page), limit stores at most, and the
// cursor of the next page: the page's last key, nil on the last page.
func storesAfter(stores []storeStat, after string, limit int) ([]storeStat, *string) {
	start := 0
	if after != "" {
		var found bool
		start, found = slices.BinarySearchFunc(stores, after, func(store storeStat, key string) int { return strings.Compare(store.key, key) })
		if found {
			start++
		}
	}
	end := min(start+limit, len(stores))
	if end < len(stores) {
		next := stores[end-1].key
		return stores[start:end], &next
	}
	return stores[start:end], nil
}

// storesPage is the page of scan that follows the cursor after (exclusive; ""
// for the first page), limit stores at most.
func (s *Server) storesPage(scan *storeScan, limit int, after string) storesPage {
	stores, next := storesAfter(scan.stores, after, limit)
	page := storesPage{
		Total: len(scan.stores), Bytes: scan.bytes, ScannedAt: scan.at.UTC().Format(time.RFC3339),
		Stores: make([]storeEntry, 0, len(stores)), Next: next,
	}
	for _, store := range stores {
		page.Stores = append(page.Stores, storeEntry{
			Store: short(store.key), Bytes: store.bytes, Modified: store.modified.UTC().Format(time.RFC3339),
			Open: s.users.isOpen(store.key), Backlog: s.storeBacklog(store.key),
		})
	}
	return page
}

// storeBacklog is the scheduler's estimate of the characters pending for the
// store with key, nil when it has none: outside background consolidation, and
// until a job has measured the user's backlog.
func (s *Server) storeBacklog(key string) *int64 {
	if s.sched == nil {
		return nil
	}
	if chars, known := s.sched.Backlog(key); known {
		return &chars
	}
	return nil
}
