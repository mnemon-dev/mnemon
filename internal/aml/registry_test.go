package aml

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
	"github.com/mnemon-dev/mnemon/internal/records"
)

// A panic while a store opens is that open's error: the next acquire opens
// the store again instead of waiting for an open that never ends.
func TestRegistryOpenPanicIsAnError(t *testing.T) {
	calls := 0
	g := newRegistry(t.TempDir(), 4, func(string) (*records.Store, *memoryagent.Agent, func(), error) {
		calls++
		if calls == 1 {
			panic("open bug")
		}
		return nil, nil, nil, nil
	})
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatal("acquire panicked:", r)
			}
		}()
		_, err = g.acquire(context.Background(), "u1", true)
	}()
	if err == nil || !strings.Contains(err.Error(), "open bug") {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	entry, err := g.acquire(ctx, "u1", true)
	if err != nil || calls != 2 {
		t.Fatal(err, calls)
	}
	g.release(entry)
}

// open is whether the registry holds the store, as OpenStores counts them; a
// store it has dropped from its cache is not open.
func TestRegistryIsOpen(t *testing.T) {
	h := newHarness(t, func(config *ServerConfig) { config.MaxOpenStores = 1 })
	a, b := userKey("u1"), userKey("u2")
	if h.server.users.isOpen(a) {
		t.Fatal("a store nobody opened is open")
	}
	h.mustAdd(t, addRequest("r1", "u1"))
	if !h.server.users.isOpen(a) || h.server.users.isOpen(b) {
		t.Fatal("after u1's Add:", h.server.users.isOpen(a), h.server.users.isOpen(b))
	}
	// A cache of one: u2 takes u1's place.
	h.mustAdd(t, addRequest("r2", "u2"))
	if h.server.users.isOpen(a) || !h.server.users.isOpen(b) {
		t.Fatal("after u2's Add:", h.server.users.isOpen(a), h.server.users.isOpen(b))
	}
	if open := h.adminStatus(t).OpenStores; open != 1 {
		t.Fatal(open)
	}
}
