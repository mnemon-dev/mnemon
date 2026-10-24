package aml

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
	"github.com/mnemon-dev/mnemon/internal/records"
)

// Every AML user has their own records store, the boundary that keeps one
// user's records out of another's Search:
// <data-root>/users/<hex(sha256(user_id))[:32]>. Inside it the default
// (global) scope holds everything. A store is created by the user's first Add
// only. Open stores and their agents are cached, least recently used first
// out, and a store is closed only once no request holds it.

var errClosing = errors.New("server is shutting down")

func userKey(userID string) string {
	sum := sha256.Sum256([]byte(userID))
	return hex.EncodeToString(sum[:])[:32]
}

// isUserKey reports whether name is a user key: 32 lowercase hex digits.
func isUserKey(name string) bool {
	if len(name) != 32 {
		return false
	}
	for _, c := range name {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// userEntry is one user's open store and agent. A request holds it between
// acquire and release; refs counts the holders, and an entry is closed only
// when it is out of the cache and refs is 0.
type userEntry struct {
	key   string
	ready chan struct{} // closed when the store and agent are open (or err)
	err   error
	store *records.Store
	agent *memoryagent.Agent
	stop  func() // stops the store's query runtime
	// writes serializes this user's Adds: a write reads the store's revision
	// and fails if it moved, and an Add then consolidates alone. waiting
	// counts the Adds waiting for it: an Add with another queued behind it
	// leaves consolidation to that one.
	writes  sync.Mutex
	waiting atomic.Int32

	// Guarded by registry.mu.
	refs    int
	elem    *list.Element
	evicted bool // out of the cache: close at refs 0
	closed  bool
}

type registry struct {
	root  string
	limit int
	// openUser opens a user's store, its query runtime and agent in their
	// directory.
	openUser func(dir string) (*records.Store, *memoryagent.Agent, func(), error)

	mu      sync.Mutex
	entries map[string]*userEntry
	lru     *list.List // front = most recently used
	closing bool
}

func newRegistry(root string, limit int, openUser func(string) (*records.Store, *memoryagent.Agent, func(), error)) *registry {
	return &registry{root: root, limit: limit, openUser: openUser, entries: map[string]*userEntry{}, lru: list.New()}
}

func (g *registry) dir(key string) string { return filepath.Join(g.root, "users", key) }

// acquire returns the user's open store and agent and holds them until
// release. With create false, a user who has no store yet gives nil and
// nothing is created.
func (g *registry) acquire(ctx context.Context, userID string, create bool) (*userEntry, error) {
	return g.acquireKey(ctx, userKey(userID), create)
}

// acquireKey is acquire for the user whose key (userKey) is key.
func (g *registry) acquireKey(ctx context.Context, key string, create bool) (*userEntry, error) {
	if !create && !g.cached(key) {
		// Look for the store without holding the lock: stores are never
		// removed, and one created meanwhile is simply not seen yet.
		if _, err := os.Stat(filepath.Join(g.dir(key), "records.db")); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, nil
			}
			return nil, err
		}
	}
	g.mu.Lock()
	if g.closing {
		g.mu.Unlock()
		return nil, errClosing
	}
	if entry := g.entries[key]; entry != nil {
		entry.refs++
		g.lru.MoveToFront(entry.elem)
		g.mu.Unlock()
		select {
		case <-entry.ready:
		case <-ctx.Done():
			g.release(entry)
			return nil, ctx.Err()
		}
		if entry.err != nil {
			g.release(entry)
			return nil, entry.err
		}
		return entry, nil
	}
	entry := &userEntry{key: key, ready: make(chan struct{}), refs: 1}
	entry.elem = g.lru.PushFront(entry)
	g.entries[key] = entry
	victims := g.evictLocked()
	g.mu.Unlock()
	closeEntries(victims)

	entry.store, entry.agent, entry.stop, entry.err = g.openUser(g.dir(key))
	if entry.err != nil {
		g.mu.Lock()
		g.removeLocked(entry) // the next request tries again
		g.mu.Unlock()
	}
	close(entry.ready)
	if entry.err != nil {
		g.release(entry)
		return nil, entry.err
	}
	return entry, nil
}

// release ends a hold. It closes the entry when it was evicted meanwhile,
// and evicts idle entries while the cache is over its limit.
func (g *registry) release(entry *userEntry) {
	g.mu.Lock()
	entry.refs--
	victims := g.evictLocked()
	if entry.evicted && entry.refs == 0 && !entry.closed {
		entry.closed = true
		victims = append(victims, entry)
	}
	g.mu.Unlock()
	closeEntries(victims)
}

// evictLocked takes idle entries out of the cache, least recently used
// first, while it holds more than its limit; entries in use stay, so the
// cache can exceed its limit while every entry is busy. It returns the
// entries to close once mu is released.
func (g *registry) evictLocked() []*userEntry {
	var victims []*userEntry
	for element := g.lru.Back(); element != nil && len(g.entries) > g.limit; {
		entry := element.Value.(*userEntry)
		element = element.Prev()
		if entry.refs == 0 {
			g.removeLocked(entry)
			entry.closed = true
			victims = append(victims, entry)
		}
	}
	return victims
}

func (g *registry) removeLocked(entry *userEntry) {
	if entry.evicted {
		return
	}
	entry.evicted = true
	delete(g.entries, entry.key)
	g.lru.Remove(entry.elem)
}

// cached reports whether key has a cache entry.
func (g *registry) cached(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.entries[key] != nil
}

// open is the number of cached entries.
func (g *registry) open() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.entries)
}

// close refuses new holds and closes every entry, the held ones at release.
func (g *registry) close() {
	g.mu.Lock()
	g.closing = true
	var victims []*userEntry
	for _, entry := range g.entries {
		g.removeLocked(entry)
		if entry.refs == 0 && !entry.closed {
			entry.closed = true
			victims = append(victims, entry)
		}
	}
	g.mu.Unlock()
	closeEntries(victims)
}

func closeEntries(entries []*userEntry) {
	for _, entry := range entries {
		if entry.stop != nil {
			entry.stop()
		}
		if entry.agent != nil {
			entry.agent.Close()
		}
		if entry.store != nil {
			entry.store.Close()
		}
	}
}
