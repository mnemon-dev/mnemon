package deploy_test

import (
	"bytes"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeInstance stands in for an instance's admin listener and its public
// route: /ready and /admin/status answer only with the admin token, and
// /i/<n>/health answers 200 over TLS.
type fakeInstance struct {
	admin, public *httptest.Server
	token         string

	mu     sync.Mutex
	ready  int
	status string
	seen   []string // each admin request: its URL and whether it carried the token
}

func newFakeInstance(t *testing.T, name, token string) *fakeInstance {
	f := &fakeInstance{token: token, ready: http.StatusOK}
	f.admin = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		authorized := r.Header.Get("Authorization") == "Bearer "+f.token
		f.seen = append(f.seen, r.URL.String()+" token="+map[bool]string{true: "yes", false: "no"}[authorized])
		w.Header().Set("Content-Type", "application/json")
		switch {
		case !authorized:
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"missing or invalid admin token"}`))
		case r.URL.Path == "/ready" && f.ready == http.StatusOK:
			w.Write([]byte(`{"ready":true}`))
		case r.URL.Path == "/ready":
			w.WriteHeader(f.ready)
			w.Write([]byte(`{"ready":false,"reasons":["breaker open: deepseek"]}`))
		case r.URL.Path == "/admin/status":
			w.Write([]byte(f.status))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	f.public = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/i/"+name+"/health" || r.Header.Get("Authorization") != "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(`{"status":"ok"}`))
	}))
	t.Cleanup(f.admin.Close)
	t.Cleanup(f.public.Close)
	return f
}

func (f *fakeInstance) set(ready int, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ready, f.status = ready, status
}

// preflight runs deploy/aml/preflight.sh with --root and --no-systemd, with
// curl behind a spy that records its arguments in record.
func preflight(t *testing.T, h *host, caBundle, record string, args ...string) (string, bool) {
	t.Helper()
	command := exec.Command("bash", append([]string{filepath.Join(h.repo, "deploy", "aml", "preflight.sh"), "--root", h.root, "--no-systemd"}, args...)...)
	command.Env = []string{"PATH=" + h.bin + string(os.PathListSeparator) + os.Getenv("PATH"), "HOME=" + h.root, "LC_ALL=C", "CURL_CA_BUNDLE=" + caBundle}
	var out bytes.Buffer
	command.Stdout, command.Stderr = &out, &out
	err := command.Run()
	if _, ok := err.(*exec.ExitError); err != nil && !ok {
		t.Fatal(err)
	}
	return out.String(), err == nil
}

// The preflight of an instance: every check runs, each failure is one FAIL
// line, the numbers of the status are printed, and no secret is printed or
// passed to curl as an argument.
func TestPreflightScript(t *testing.T) {
	h := newHost(t, "Caddyfile.shared")
	h.ok("", "new", "beta-alice", "--layout", "shared", "--public-url", "https://beta.example.com/i/beta-alice", "--admin-ip", "10.77.0.3", "--host-name", "beta-1")
	env, _ := h.instance("beta-alice")
	fake := newFakeInstance(t, "beta-alice", env["MNEMON_ADMIN_TOKEN"])

	// Point the instance at the fakes, as if they were its listeners.
	admin := strings.TrimPrefix(fake.admin.URL, "http://")
	rewrite := func(hostPath string, values map[string]string) {
		var lines []string
		for _, line := range strings.Split(strings.TrimSuffix(h.read(hostPath), "\n"), "\n") {
			name, _, _ := strings.Cut(line, "=")
			if value, ok := values[name]; ok {
				if value == "" {
					continue
				}
				line = name + "=" + value
			}
			lines = append(lines, line)
		}
		writeFile(t, h.path(hostPath), strings.Join(lines, "\n")+"\n", 0o600)
	}
	rewrite("/etc/mnemon-aml/beta-alice/env", map[string]string{"AML_ADMIN_LISTEN": admin})
	rewrite("/etc/mnemon-aml/beta-alice/watch.env", map[string]string{"AML_ADMIN_LISTEN": admin, "AML_PUBLIC_URL": fake.public.URL + "/i/beta-alice"})
	caBundle := filepath.Join(t.TempDir(), "ca.pem")
	writeFile(t, caBundle, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: fake.public.Certificate().Raw})), 0o644)
	record := filepath.Join(h.bin, "curl-arguments")
	real, err := exec.LookPath("curl")
	if err != nil {
		t.Skip("curl is missing; preflight needs it")
	}
	writeFile(t, filepath.Join(h.bin, "curl"), "#!/bin/sh\nprintf '%s\\n' \"curl $*\" >> '"+record+"'\nexec '"+real+"' \"$@\"\n", 0o755)

	healthy := `{"startedAt":"2026-10-24T08:00:00Z","lagBound":160000,"version":"aml-2",` +
		`"budgets":[{"kind":"deepseek","cap":30,"spent":1.5,"held":0.25},{"kind":"jev","cap":3,"spent":0.1,"held":0}]}`
	fake.set(http.StatusOK, healthy)
	out, ok := preflight(t, h, caBundle, record, "beta-alice")
	if !ok || strings.Contains(out, "FAIL") {
		t.Fatalf("the preflight of a ready instance failed:\n%s", out)
	}
	for _, want := range []string{
		"version aml-2", "lagBound 160000 characters",
		"budget deepseek: spent 1.5 + held 0.25 of cap 30 (5.8%)", "budget jev: spent 0.1 + held 0 of cap 3 (3.3%)",
		"mnemon-preflight beta-alice: all 8 checks passed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the preflight did not print %q:\n%s", want, out)
		}
	}
	fake.mu.Lock()
	seen := strings.Join(fake.seen, "\n")
	fake.mu.Unlock()
	if want := "/ready token=yes\n/admin/status token=yes"; seen != want {
		t.Errorf("the admin listener saw\n%s\nwant\n%s", seen, want)
	}

	// Everything wrong at once: each is one FAIL line.
	fake.set(http.StatusServiceUnavailable, `{"startedAt":"2026-10-24T08:00:00Z","lagBound":0,"budgets":[{"kind":"jev","cap":3,"spent":2.5,"held":0.05}]}`)
	writeFile(t, h.path("/var/lib/mnemon-aml/beta-alice/data/users/"+storeKey("smoke-user")+"/records.db"), "records", 0o600)
	if err := os.Chmod(h.path("/etc/mnemon-aml/beta-alice/env"), 0o644); err != nil {
		t.Fatal(err)
	}
	rewrite("/etc/mnemon-aml/beta-alice/watch.env", map[string]string{"AML_ALERT_KIND": ""})
	rewrite("/etc/mnemon-aml/beta-alice/env", map[string]string{"TYPESAFE_API_KEY": ""})
	if err := os.Chmod(h.path("/etc/mnemon-aml/beta-alice/env"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, ok = preflight(t, h, caBundle, record, "beta-alice")
	if ok {
		t.Fatalf("the preflight passed:\n%s", out)
	}
	var fails []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "FAIL ") {
			fails = append(fails, line)
		}
	}
	for i, want := range []string{
		"/etc/mnemon-aml/beta-alice/env has mode 644, not 0600",
		"/etc/mnemon-aml/beta-alice/env lacks TYPESAFE_API_KEY",
		"/etc/mnemon-aml/beta-alice/watch.env lacks AML_ALERT_KIND",
		"/ready on the admin listener answered 503: breaker open: deepseek",
		"lagBound is 0",
		"budget jev: spent and held are 85% of the cap, not below 80%",
		"the data root holds the store of smoke-user",
	} {
		if i >= len(fails) || !strings.Contains(fails[i], want) {
			t.Errorf("FAIL line %d is not about %q:\n%s", i+1, want, strings.Join(fails, "\n"))
		}
	}
	if len(fails) != 7 || !strings.Contains(out, "mnemon-preflight beta-alice: 7 failures in 8 checks") {
		t.Errorf("want 7 failures in 8 checks:\n%s", out)
	}

	// A wrong token is refused by the listener, and said so; the token
	// itself is never printed nor an argument of curl.
	fake.set(http.StatusOK, healthy)
	fake.mu.Lock()
	fake.token = "mnm_" + strings.Repeat("x", 43)
	fake.mu.Unlock()
	out, _ = preflight(t, h, caBundle, record, "beta-alice")
	if !strings.Contains(out, "FAIL /ready on the admin listener answered 401") {
		t.Errorf("a refused token is not reported:\n%s", out)
	}
	arguments, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(arguments), "--config -") {
		t.Fatalf("curl was not given its config on standard input:\n%s", arguments)
	}
	for label, secret := range h.secrets {
		if strings.Contains(out, secret) || strings.Contains(string(arguments), secret) {
			t.Errorf("the preflight printed the %s or passed it to curl", label)
		}
	}
}
