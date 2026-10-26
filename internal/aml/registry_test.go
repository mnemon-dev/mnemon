package aml

import "testing"

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
