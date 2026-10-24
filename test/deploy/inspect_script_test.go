package deploy_test

import (
	"encoding/json"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
)

// The inspect service of an instance (aml-inspect@<n>, the console's 检索
// tab) is opt-in: new never turns it on; inspect enable writes inspect.env
// (0600) with a new token, the listener at the admin port + 1, the debug
// caps and the provider keys, and prints the URL, the token and the
// console's JSON once; inspect disable removes it all.

// inspectOutput checks inspect enable's one-time output: the URL, the token
// on its line and in the console's JSON, nowhere else.
func inspectOutput(t *testing.T, out, url, token string) {
	t.Helper()
	fragment := `{"inspectUrl":"` + url + `","inspectToken":"` + token + `"}`
	for _, want := range []string{"Inspect URL:    " + url + "\n", "Inspect token:  " + token + "\n", fragment + "\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("inspect enable's output lacks %q:\n%s", want, out)
		}
	}
	if n := strings.Count(out, token); n != 2 {
		t.Errorf("inspect enable printed the token %d times, want on its line and in the JSON", n)
	}
	var parsed map[string]string
	if err := json.Unmarshal([]byte(fragment), &parsed); err != nil || parsed["inspectUrl"] != url || parsed["inspectToken"] != token {
		t.Errorf("the console's JSON does not parse: %v", err)
	}
}

// inspectEnv checks name's inspect.env and state and returns the env; it
// records the token as "<name> inspect token".
func (h *host) inspectEnv(name, listen, caps string) map[string]string {
	h.t.Helper()
	if got := h.mode("/etc/mnemon-aml/" + name + "/inspect.env"); got != 0o600 {
		h.t.Errorf("inspect.env has mode %o", got)
	}
	if got := h.mode("/var/lib/mnemon-aml-inspect/" + name); got != 0o700 {
		h.t.Errorf("the inspect state has mode %o", got)
	}
	env := h.env("/etc/mnemon-aml/" + name + "/inspect.env")
	if got := slices.Sorted(maps.Keys(env)); !slices.Equal(got, []string{"AML_INSPECT_DEBUG_CAPS", "AML_INSPECT_LISTEN", "DEEPSEEK_API_KEY", "GOMEMLIMIT", "MNEMON_INSPECT_TOKEN", "TYPESAFE_API_KEY"}) {
		h.t.Errorf("inspect.env holds %q", got)
	}
	engine := h.env("/etc/mnemon-aml/" + name + "/env")
	token := env["MNEMON_INSPECT_TOKEN"]
	switch {
	case env["AML_INSPECT_LISTEN"] != listen:
		h.t.Errorf("AML_INSPECT_LISTEN=%s, want %s", env["AML_INSPECT_LISTEN"], listen)
	case env["AML_INSPECT_DEBUG_CAPS"] != caps:
		h.t.Errorf("AML_INSPECT_DEBUG_CAPS=%s, want %s", env["AML_INSPECT_DEBUG_CAPS"], caps)
	case env["GOMEMLIMIT"] != "819MiB":
		h.t.Errorf("GOMEMLIMIT=%s, want 80%% of MemoryMax=1G", env["GOMEMLIMIT"])
	case !secretShape.MatchString(token) || token == engine["MNEMON_ADMIN_TOKEN"] || token == engine["AML_API_KEY"]:
		h.t.Errorf("the inspect token is not a new mnm_ secret")
	case env["DEEPSEEK_API_KEY"] != h.secrets["DEEPSEEK_API_KEY"] || env["TYPESAFE_API_KEY"] != h.secrets["TYPESAFE_API_KEY"]:
		h.t.Errorf("inspect.env does not carry the host's provider keys")
	}
	h.secrets[name+" inspect token"] = token
	return env
}

func TestInstanceScriptInspect(t *testing.T) {
	h := newHost(t, "Caddyfile.shared")
	for _, name := range []string{"beta-alice", "beta-bob"} {
		h.ok("", "new", name, "--layout", "shared", "--public-url", "https://beta.example.com/i/"+name, "--admin-ip", "10.77.0.3", "--host-name", "beta-1")
		h.instance(name)
		if h.exists("/etc/mnemon-aml/"+name+"/inspect.env") || h.exists("/var/lib/mnemon-aml-inspect/"+name) {
			t.Fatal("new turned inspect on")
		}
	}
	inspectColumn := func() map[string]string {
		t.Helper()
		lines := strings.Split(strings.TrimSuffix(h.ok("", "list"), "\n"), "\n")
		out := map[string]string{}
		for _, line := range lines[1:] {
			fields := strings.Fields(line)
			out[fields[0]] = fields[6]
		}
		return out
	}

	// Slot 1: admin 8111, inspect 8112; the default caps.
	out := h.ok("", "inspect", "enable", "beta-bob")
	env := h.inspectEnv("beta-bob", "10.77.0.3:8112", "deepseek=5,jev=1")
	inspectOutput(t, out, "http://10.77.0.3:8112", env["MNEMON_INSPECT_TOKEN"])
	if got := inspectColumn(); !maps.Equal(got, map[string]string{"beta-alice": "off", "beta-bob": "on"}) {
		t.Errorf("list's INSPECT column: %v", got)
	}
	// It refuses to overwrite what is on.
	if stderr := h.refused("", "inspect", "enable", "beta-bob"); !strings.Contains(stderr, "already on") {
		t.Error(stderr)
	}

	// Slot 0: admin 8101, inspect 8102; caps of the operator's.
	out = h.ok("", "inspect", "enable", "beta-alice", "--debug-deepseek-cap", "2.5", "--debug-jev-cap=0.5")
	env = h.inspectEnv("beta-alice", "10.77.0.3:8102", "deepseek=2.5,jev=0.5")
	inspectOutput(t, out, "http://10.77.0.3:8102", env["MNEMON_INSPECT_TOKEN"])

	for _, args := range [][]string{
		{"inspect"}, {"inspect", "on", "beta-bob"}, {"inspect", "enable"}, {"inspect", "enable", "nobody"},
		{"inspect", "enable", "beta-bob", "--debug-jev-cap", "0"}, {"inspect", "enable", "beta-bob", "--debug-deepseek-cap", "-1"},
		{"inspect", "enable", "beta-bob", "--debug-deepseek-cap", "abc"}, {"inspect", "enable", "beta-bob", "--debug-deepseek-cap"},
		{"inspect", "enable", "beta-bob", "--bogus", "1"}, {"inspect", "disable"}, {"inspect", "disable", "nobody"},
	} {
		h.refused("", args...)
	}

	// disable removes the file and the state, and says what it removed.
	out = h.ok("", "inspect", "disable", "beta-bob")
	for _, want := range []string{"removed /etc/mnemon-aml/beta-bob/inspect.env\n", "removed /var/lib/mnemon-aml-inspect/beta-bob\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("disable did not say %q:\n%s", want, out)
		}
	}
	if h.exists("/etc/mnemon-aml/beta-bob/inspect.env") || h.exists("/var/lib/mnemon-aml-inspect/beta-bob") || !h.exists("/etc/mnemon-aml/beta-bob/env") {
		t.Fatal("disable left inspect's files, or took the instance's")
	}
	if stderr := h.refused("", "inspect", "disable", "beta-bob"); !strings.Contains(stderr, "not on") {
		t.Error(stderr)
	}
	if got := inspectColumn(); !maps.Equal(got, map[string]string{"beta-alice": "on", "beta-bob": "off"}) {
		t.Errorf("list's INSPECT column: %v", got)
	}

	// delete takes the inspect state with the instance.
	out = h.ok("beta-alice\n", "delete", "beta-alice")
	if !strings.Contains(out, "removed /var/lib/mnemon-aml-inspect/beta-alice\n") || h.exists("/var/lib/mnemon-aml-inspect/beta-alice") {
		t.Errorf("delete left the inspect state:\n%s", out)
	}

	journal := strings.Join(h.journal(), "\n")
	for _, want := range []string{
		"inspect enable beta-bob: listen 10.77.0.3:8112, debug caps deepseek 5 CNY, jev 1 USD",
		"inspect enable beta-alice: listen 10.77.0.3:8102, debug caps deepseek 2.5 CNY, jev 0.5 USD",
		"inspect disable beta-bob",
	} {
		if !strings.Contains(journal, want) {
			t.Errorf("the journal lacks %q:\n%s", want, journal)
		}
	}
	h.checkNoSecrets()
}

// A dedicated host's inspect service listens on 8092, the admin port + 1.
func TestInstanceScriptInspectDedicated(t *testing.T) {
	h := newHost(t, "Caddyfile.dedicated")
	h.ok("", "new", "eval", "--layout", "dedicated", "--public-url", "https://eval.example.com", "--admin-ip", "10.77.0.2", "--host-name", "eval-1")
	h.instance("eval")
	out := h.ok("", "inspect", "enable", "eval")
	env := h.inspectEnv("eval", "10.77.0.2:8092", "deepseek=5,jev=1")
	inspectOutput(t, out, "http://10.77.0.2:8092", env["MNEMON_INSPECT_TOKEN"])
	h.checkNoSecrets()
}

// The systemd side, sourced: enable starts aml-inspect@<n> and waits for its
// /healthz, the token reaching curl only on its standard input; disable and
// delete stop and disable it; a failed start leaves the token printed.
func TestInstanceScriptInspectUnits(t *testing.T) {
	h, _ := newSharedAlice(t)
	writeFile(t, h.path("/etc/systemd/system/aml-inspect@.service"), "[Unit]\n", 0o644)
	r, calls := h.sourced("beta-alice", `cmd_inspect enable beta-alice`)
	if !r.ok {
		t.Fatalf("inspect enable: %s%s", r.stdout, r.stderr)
	}
	h.runs[len(h.runs)-1].args = []string{"inspect", "enable", "beta-alice"}
	env := h.inspectEnv("beta-alice", "10.77.0.3:8102", "deepseek=5,jev=1")
	inspectOutput(t, r.stdout, "http://10.77.0.3:8102", env["MNEMON_INSPECT_TOKEN"])
	// The journal line comes first: the token is printed whatever the start does.
	want := []string{
		"logger -t mnemon-instance -- inspect enable beta-alice: listen 10.77.0.3:8102, debug caps deepseek 5 CNY, jev 1 USD",
		"systemctl daemon-reload (status active)",
		"systemctl enable --now aml-inspect@beta-alice.service (status active)",
		"curl --disable --config - --silent --output /dev/null --max-time 5 --write-out %{http_code} http://10.77.0.3:8102/healthz",
	}
	if !slices.Equal(calls, want) {
		t.Errorf("inspect enable ran\n%s\nwant\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
	}

	r, calls = h.sourced("beta-alice", `cmd_inspect disable beta-alice`)
	if !r.ok {
		t.Fatalf("inspect disable: %s%s", r.stdout, r.stderr)
	}
	if got := systemctlCalls(calls); !slices.Equal(got, []string{"disable --now aml-inspect@beta-alice.service", "reset-failed aml-inspect@beta-alice.service"}) {
		t.Errorf("inspect disable ran %q", got)
	}

	// /healthz never answers: the token is printed all the same, and the
	// command fails saying how to remove it.
	r, _ = h.sourced("beta-alice", `READY_AFTER=0 SLEEP_STEP=6; cmd_inspect enable beta-alice`)
	h.runs[len(h.runs)-1].args = []string{"inspect", "enable", "beta-alice"}
	if r.ok || !strings.Contains(r.stderr, "inspect disable beta-alice removes it") || !strings.Contains(r.stdout, "Inspect token:  mnm_") {
		t.Errorf("a start that failed: %s%s", r.stdout, r.stderr)
	}
	h.inspectEnv("beta-alice", "10.77.0.3:8102", "deepseek=5,jev=1")

	// delete stops it too.
	r, calls = h.sourced("beta-alice", `printf 'beta-alice\n' | cmd_delete beta-alice`)
	if !r.ok || !slices.Contains(systemctlCalls(calls), "disable --now aml-inspect@beta-alice.service") {
		t.Errorf("delete: %v %s%s", systemctlCalls(calls), r.stdout, r.stderr)
	}
	// Without inspect, delete leaves its unit alone: a host may lack the template.
	h2, _ := newSharedAlice(t)
	_, calls = h2.sourced("beta-alice", `printf 'beta-alice\n' | cmd_delete beta-alice`)
	for _, call := range systemctlCalls(calls) {
		if strings.Contains(call, "disable --now aml-inspect@") {
			t.Error("delete of an instance without inspect disabled aml-inspect:", call)
		}
	}
	h.checkNoSecrets()
}

// preflight checks the inspect service only while it is on: inspect.env's
// mode and names, and its /healthz with the inspect token.
func TestPreflightScriptInspect(t *testing.T) {
	p := newPreflightRig(t)
	h := p.h
	out, ok := preflight(t, h, p.caBundle, p.record, "beta-alice")
	if !ok || !strings.Contains(out, "all 8 checks passed") {
		t.Fatalf("without inspect:\n%s", out)
	}
	token := "mnm_" + strings.Repeat("i", 43)
	h.secrets["beta-alice inspect token"] = token
	var seen []string
	inspect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path+" "+map[bool]string{true: "token", false: "no token"}[r.Header.Get("Authorization") == "Bearer "+token])
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer inspect.Close()
	env := map[string]string{
		"AML_INSPECT_LISTEN": strings.TrimPrefix(inspect.URL, "http://"), "AML_INSPECT_DEBUG_CAPS": "deepseek=5,jev=1", "GOMEMLIMIT": "819MiB",
		"MNEMON_INSPECT_TOKEN": token, "DEEPSEEK_API_KEY": h.secrets["DEEPSEEK_API_KEY"], "TYPESAFE_API_KEY": h.secrets["TYPESAFE_API_KEY"],
	}
	write := func(env map[string]string, mode fs.FileMode) {
		text := ""
		for _, name := range slices.Sorted(maps.Keys(env)) {
			text += name + "=" + env[name] + "\n"
		}
		writeFile(t, h.path("/etc/mnemon-aml/beta-alice/inspect.env"), text, mode)
	}
	write(env, 0o600)
	out, ok = preflight(t, h, p.caBundle, p.record, "beta-alice")
	if !ok || !strings.Contains(out, "all 10 checks passed") || !slices.Equal(seen, []string{"/healthz token"}) {
		t.Fatalf("with inspect on:\n%s\n%v", out, seen)
	}

	// Wrong mode, a missing name, a refused token: each a FAIL line.
	broken := maps.Clone(env)
	delete(broken, "AML_INSPECT_DEBUG_CAPS")
	broken["MNEMON_INSPECT_TOKEN"] = "mnm_" + strings.Repeat("w", 43)
	write(broken, 0o644)
	out, ok = preflight(t, h, p.caBundle, p.record, "beta-alice")
	got := fails(out)
	want := []string{
		"FAIL /etc/mnemon-aml/beta-alice/inspect.env has mode 644, not 0600",
		"FAIL /etc/mnemon-aml/beta-alice/inspect.env lacks AML_INSPECT_DEBUG_CAPS",
		"FAIL /healthz on the inspect listener answered 401",
	}
	if ok || !slices.Equal(got, want) || !strings.Contains(out, "3 failures in 10 checks") {
		t.Fatalf("FAIL lines\n%s\nwant\n%s\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"), out)
	}
	arguments, err := os.ReadFile(p.record)
	if err != nil {
		t.Fatal(err)
	}
	for label, secret := range h.secrets {
		if strings.Contains(out, secret) || strings.Contains(string(arguments), secret) {
			t.Errorf("the preflight printed the %s or passed it to curl", label)
		}
	}
}
