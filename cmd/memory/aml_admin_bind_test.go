package memory

import (
	"net"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// shortAdminRetry makes aml-serve bind its admin listener again every
// interval until the test ends.
func shortAdminRetry(t *testing.T, interval time.Duration) {
	t.Helper()
	previous := amlAdminRetry
	amlAdminRetry = interval
	t.Cleanup(func() { amlAdminRetry = previous })
}

// R91: the data plane never depends on the admin bind. An --admin-listen
// address that cannot be bound when aml-serve starts (wg0 not up yet; here,
// a port another listener holds) does not keep the public listener from
// serving: aml-serve tells systemd READY=1, serves Adds, logs the failure
// once, and binds the address again every amlAdminRetry until it can, then
// serves the admin endpoints there and says so. It stops cleanly.
func TestAMLServeServesBeforeItsAdminListenerBinds(t *testing.T) {
	shortAdminRetry(t, 50*time.Millisecond)
	notified := recordNotifications(t)
	endpoint := newModelEndpoint(t)
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	adminAddr := held.Addr().String()
	run := startAMLServe(t, amlTestConfig(t, endpoint.URL, nil), "--admin-listen", adminAddr)
	waitFor(t, "READY=1", func() bool { return slices.Contains(notified.all(), "READY=1") })
	public := run.address(t, false)
	if code := get(t, public+"/health"); code != http.StatusOK {
		t.Fatal("public /health", code)
	}
	if code, body := run.post(t, "/add", noteAdd("r1", "u1")); code != http.StatusOK {
		t.Fatal(code, body)
	}
	// It has tried again several times by now, and said so once.
	time.Sleep(300 * time.Millisecond)
	if log := run.log.String(); strings.Count(log, "--admin-listen") != 1 || !strings.Contains(log, "serving without the admin listener and binding it again every 50ms") || strings.Contains(log, "admin listening on") {
		t.Fatal(log)
	}
	held.Close()
	if admin := run.address(t, true); admin != "http://"+adminAddr {
		t.Fatal("the admin listener bound", admin, "want", adminAddr)
	}
	if code := get(t, "http://"+adminAddr+"/admin/status"); code != http.StatusOK {
		t.Fatal("admin /admin/status", code)
	}
	if err := run.stop(t); err != nil {
		t.Fatal(err, run.log.String())
	}
	if got := notified.all(); !slices.Equal(got, []string{"READY=1", "STOPPING=1"}) {
		t.Fatal(got)
	}
}

// R91: a shutdown while the admin listener is still unbound stops its
// retries with the rest: the command returns, cleanly, and the address is
// never bound.
func TestAMLServeStopsWhileItsAdminListenerIsUnbound(t *testing.T) {
	shortAdminRetry(t, 20*time.Millisecond)
	notified := recordNotifications(t)
	endpoint := newModelEndpoint(t)
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	run := startAMLServe(t, amlTestConfig(t, endpoint.URL, nil), "--admin-listen", held.Addr().String())
	waitFor(t, "READY=1", func() bool { return slices.Contains(notified.all(), "READY=1") })
	if err := run.stop(t); err != nil {
		t.Fatal(err, run.log.String())
	}
	held.Close()
	time.Sleep(100 * time.Millisecond) // a retry left running would bind now
	probe, err := net.Listen("tcp", held.Addr().String())
	if err != nil {
		t.Fatal("the address was bound after the command returned:", err)
	}
	probe.Close()
	if strings.Contains(run.log.String(), "admin listening on") {
		t.Fatal(run.log.String())
	}
}
