package deploy_test

import (
	"bytes"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	p := newPreflightRig(t)
	h, fake, caBundle, record, rewrite := p.h, p.fake, p.caBundle, p.record, p.rewrite
	healthy := healthyStatus
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

// The account's provider key file, once the console has set a key: the
// preflight checks its mode (and owner) when it exists, and prints nothing
// it holds.
func TestPreflightChecksTheKeyFile(t *testing.T) {
	p := newPreflightRig(t)
	h := p.h
	const keys = "/var/lib/mnemon-aml/beta-alice/provider-keys.json"
	h.secrets["planted account key"] = "sk-account-" + randomHex(t)
	writeFile(t, h.path(keys), `{"version":1,"keys":{"deepseek":"`+h.secrets["planted account key"]+`"},"updatedAt":"2026-10-25T08:00:00Z"}`, 0o600)
	out, ok := preflight(t, h, p.caBundle, p.record, "beta-alice")
	if !ok || !strings.Contains(out, "mnemon-preflight beta-alice: all 9 checks passed") {
		t.Fatalf("the preflight of a private key file:\n%s", out)
	}
	if err := os.Chmod(h.path(keys), 0o644); err != nil {
		t.Fatal(err)
	}
	out, ok = preflight(t, h, p.caBundle, p.record, "beta-alice")
	if ok || !slices.Equal(fails(out), []string{"FAIL " + keys + " has mode 644, not 0600"}) {
		t.Fatalf("the preflight of a key file others may read:\n%s", out)
	}
	for label, secret := range h.secrets {
		if strings.Contains(out, secret) {
			t.Errorf("the preflight printed the %s", label)
		}
	}
}

// The settings file of the caps set at run time (R99): the preflight checks
// its mode (and owner) when it exists; and an unlimited cap (R100) is shown
// as such, with no share of a cap measured, never an alert.
func TestPreflightChecksTheSettingsFile(t *testing.T) {
	p := newPreflightRig(t)
	h := p.h
	const settings = "/var/lib/mnemon-aml/beta-alice/settings.json"
	writeFile(t, h.path(settings), `{"version":1,"budgets":{"jev":null},"updatedAt":"2026-10-25T08:00:00Z"}`, 0o600)
	p.fake.set(http.StatusOK, `{"startedAt":"2026-10-24T08:00:00Z","lagBound":160000,"version":"aml-2",`+
		`"budgets":[{"kind":"deepseek","cap":30,"spent":1.5,"held":0.25,"source":"config","unlimited":false},{"kind":"jev","cap":null,"spent":900,"held":5,"source":"settings","unlimited":true}]}`)
	out, ok := preflight(t, h, p.caBundle, p.record, "beta-alice")
	if !ok || !strings.Contains(out, "mnemon-preflight beta-alice: all 9 checks passed") ||
		!strings.Contains(out, "budget jev: spent 900 + held 5, no cap (unlimited)") || !strings.Contains(out, "budget deepseek: spent 1.5 + held 0.25 of cap 30 (5.8%)") {
		t.Fatalf("the preflight of a private settings file and an unlimited cap:\n%s", out)
	}
	if err := os.Chmod(h.path(settings), 0o640); err != nil {
		t.Fatal(err)
	}
	out, ok = preflight(t, h, p.caBundle, p.record, "beta-alice")
	if ok || !slices.Equal(fails(out), []string{"FAIL " + settings + " has mode 640, not 0600"}) {
		t.Fatalf("the preflight of a settings file its group may read:\n%s", out)
	}
	// Both files: one check each.
	if err := os.Chmod(h.path(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	writeFile(t, h.path("/var/lib/mnemon-aml/beta-alice/provider-keys.json"), `{"version":1,"keys":{}}`, 0o600)
	if out, ok := preflight(t, h, p.caBundle, p.record, "beta-alice"); !ok || !strings.Contains(out, "all 10 checks passed") {
		t.Fatalf("the preflight of both files:\n%s", out)
	}
}

// preflightRig is an instance whose admin listener and public route are
// fakes, ready for mnemon-preflight: its env files point at the fakes, the
// fakes' certificate is curl's CA bundle, and curl runs behind a spy that
// records its arguments.
type preflightRig struct {
	h                *host
	fake             *fakeInstance
	caBundle, record string
}

// healthyStatus is the /admin/status of an instance that is ready for a job.
const healthyStatus = `{"startedAt":"2026-10-24T08:00:00Z","lagBound":160000,"version":"aml-2",` +
	`"budgets":[{"kind":"deepseek","cap":30,"spent":1.5,"held":0.25},{"kind":"jev","cap":3,"spent":0.1,"held":0}]}`

func newPreflightRig(t *testing.T) *preflightRig {
	t.Helper()
	h := newHost(t, "Caddyfile.shared")
	h.ok("", "new", "beta-alice", "--layout", "shared", "--public-url", "https://beta.example.com/i/beta-alice", "--admin-ip", "10.77.0.3", "--host-name", "beta-1")
	env, _ := h.instance("beta-alice")
	p := &preflightRig{h: h, fake: newFakeInstance(t, "beta-alice", env["MNEMON_ADMIN_TOKEN"])}
	admin := strings.TrimPrefix(p.fake.admin.URL, "http://")
	p.rewrite("/etc/mnemon-aml/beta-alice/env", map[string]string{"AML_ADMIN_LISTEN": admin})
	p.rewrite("/etc/mnemon-aml/beta-alice/watch.env", map[string]string{"AML_ADMIN_LISTEN": admin, "AML_PUBLIC_URL": p.fake.public.URL + "/i/beta-alice"})
	p.caBundle = filepath.Join(t.TempDir(), "ca.pem")
	writeFile(t, p.caBundle, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: p.fake.public.Certificate().Raw})), 0o644)
	p.record = filepath.Join(h.bin, "curl-arguments")
	real, err := exec.LookPath("curl")
	if err != nil {
		t.Skip("curl is missing; preflight needs it")
	}
	writeFile(t, filepath.Join(h.bin, "curl"), "#!/bin/sh\nprintf '%s\\n' \"curl $*\" >> '"+p.record+"'\nexec '"+real+"' \"$@\"\n", 0o755)
	p.fake.set(http.StatusOK, healthyStatus)
	return p
}

// rewrite sets the NAME=value lines of an env file of the tree (mode 0600);
// an empty value removes the name.
func (p *preflightRig) rewrite(hostPath string, values map[string]string) {
	p.h.t.Helper()
	var lines []string
	for _, line := range strings.Split(strings.TrimSuffix(p.h.read(hostPath), "\n"), "\n") {
		name, _, _ := strings.Cut(line, "=")
		if value, ok := values[name]; ok {
			if value == "" {
				continue
			}
			line = name + "=" + value
		}
		lines = append(lines, line)
	}
	writeFile(p.h.t, p.h.path(hostPath), strings.Join(lines, "\n")+"\n", 0o600)
}

// fails are the FAIL lines of a preflight's output.
func fails(out string) []string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "FAIL ") {
			lines = append(lines, line)
		}
	}
	return lines
}

// Telegram is an alert kind; a telegram webhook must be a bot's sendMessage
// with its chat, and the FAIL line that says it is not repeats nothing of it.
func TestPreflightScriptAlertKinds(t *testing.T) {
	p := newPreflightRig(t)
	token := "7012345678:AA" + randomHex(t)
	p.h.secrets["telegram bot token"] = token
	bot := "https://api.telegram.org/bot" + token
	for _, tc := range []struct{ kind, webhook, fail string }{
		{"telegram", bot + "/sendMessage?chat_id=123456789", ""},
		{"telegram", bot + "/sendMessage?chat_id=-1001234567890", ""},
		{"telegram", bot + "/sendMessage?chat_id=@mnemon_alerts", ""},
		{"telegram", "https://bots.example.com/bot" + token + "/sendMessage?chat_id=42", ""},
		{"feishu", "https://open.feishu.cn/open-apis/bot/v2/hook/" + randomHex(t), ""},
		{"wecom", "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=" + randomHex(t), ""},
		{"telegram", bot + "/sendMessage", "AML_ALERT_WEBHOOK is not a Telegram bot's sendMessage URL with its chat"},
		{"telegram", bot + "/sendMessage?chat_id=abc", "AML_ALERT_WEBHOOK is not a Telegram bot's sendMessage URL with its chat"},
		{"telegram", bot + "/getUpdates?chat_id=42", "AML_ALERT_WEBHOOK is not a Telegram bot's sendMessage URL with its chat"},
		{"telegram", bot + "/sendMessage?chat_id=42&parse_mode=HTML", "AML_ALERT_WEBHOOK is not a Telegram bot's sendMessage URL with its chat"},
		{"telegram", "https://api.telegram.org/bot" + token + "%zz/sendMessage?chat_id=42", "AML_ALERT_WEBHOOK is not a Telegram bot's sendMessage URL with its chat"},
		{"slack", "https://hooks.slack.com/services/" + randomHex(t), `AML_ALERT_KIND is "slack", not telegram, feishu or wecom`},
	} {
		p.rewrite("/etc/mnemon-aml/beta-alice/watch.env", map[string]string{"AML_ALERT_KIND": tc.kind, "AML_ALERT_WEBHOOK": tc.webhook})
		out, ok := preflight(t, p.h, p.caBundle, p.record, "beta-alice")
		got := fails(out)
		switch {
		case tc.fail == "" && (!ok || len(got) != 0):
			t.Errorf("%s %s: the preflight failed:\n%s", tc.kind, tc.webhook, out)
		case tc.fail != "" && (ok || len(got) != 1 || !strings.Contains(got[0], tc.fail)):
			t.Errorf("%s %s: want one FAIL line about %q:\n%s", tc.kind, tc.webhook, tc.fail, out)
		}
		if strings.Contains(out, tc.webhook) || strings.Contains(out, token) {
			t.Errorf("the preflight printed the webhook:\n%s", out)
		}
	}
	// The kind is named in the FAIL line only when it looks like one (a short
	// word of lowercase letters, digits and hyphens): a value pasted into the
	// wrong line may be a secret, and is not echoed (the final review's
	// parked T15-g).
	for _, kind := range []string{
		bot + "/sendMessage?chat_id=42",    // the webhook, in the kind's line
		token,                              // the bot's token alone
		"Telegram",                         // not lowercase
		"telegram bot",                     // a space
		"0123456789abcdef0",                // 17 characters
		strings.ToLower("S3cret-" + token), // a secret that happens to be lowercase, and long
	} {
		p.rewrite("/etc/mnemon-aml/beta-alice/watch.env", map[string]string{"AML_ALERT_KIND": kind, "AML_ALERT_WEBHOOK": bot + "/sendMessage?chat_id=42"})
		out, ok := preflight(t, p.h, p.caBundle, p.record, "beta-alice")
		got := fails(out)
		if ok || len(got) != 1 || !strings.Contains(got[0], "AML_ALERT_KIND is not telegram, feishu or wecom") || strings.Contains(out, kind) || strings.Contains(out, token) {
			t.Errorf("a kind that is no word (%d characters): want one FAIL line that does not repeat it:\n%s", len(kind), out)
		}
	}
	// A short word is named, so that the operator sees what is wrong.
	p.rewrite("/etc/mnemon-aml/beta-alice/watch.env", map[string]string{"AML_ALERT_KIND": "dingtalk", "AML_ALERT_WEBHOOK": bot + "/sendMessage?chat_id=42"})
	if out, ok := preflight(t, p.h, p.caBundle, p.record, "beta-alice"); ok || len(fails(out)) != 1 || !strings.Contains(fails(out)[0], `AML_ALERT_KIND is "dingtalk", not telegram, feishu or wecom`) {
		t.Errorf("a kind that is a word:\n%s", out)
	}
	// The webhook is checked in the shell: no program is given it.
	if arguments, err := os.ReadFile(p.record); err != nil || strings.Contains(string(arguments), token) {
		t.Errorf("curl's arguments held the bot's token (%v)", err)
	}
}

// hostStubs stand in for the host's programs that the systemd side of the
// preflight reads, each answering with what dir holds: systemctl is-active
// (active-<unit>, else active), systemd-analyze verify (verify-fails),
// caddy validate (validate-fails), ip -o link and ip -o -4 addr on wg0
// (ip-link, ip-addr; no file: no wg0) and ufw status verbose (ufw-status).
func hostStubs(t *testing.T, dir string) {
	t.Helper()
	for tool, script := range map[string]string{
		"systemctl": `[ "$1" = is-active ] && [ $# -eq 2 ] || { echo "systemctl $*: not canned" >&2; exit 2; }
state=active
[ ! -f "DIR/active-$2" ] || state=$(cat "DIR/active-$2")
echo "$state"
[ "$state" = active ]`,
		"systemd-analyze": `[ "$1" = verify ] || exit 2
[ ! -f DIR/verify-fails ] || { cat DIR/verify-fails >&2; exit 1; }`,
		"caddy": `[ "$1" = validate ] || exit 2
[ ! -f DIR/validate-fails ]`,
		"ip": `case "$*" in
"-o link show dev wg0") file=DIR/ip-link ;;
"-o -4 addr show dev wg0") file=DIR/ip-addr ;;
*) echo "ip $*: not canned" >&2; exit 2 ;;
esac
[ -f "$file" ] || { echo 'Device "wg0" does not exist.' >&2; exit 1; }
cat "$file"`,
		"ufw": `[ "$*" = "status verbose" ] || exit 2
cat DIR/ufw-status`,
	} {
		writeFile(t, filepath.Join(dir, tool), "#!/bin/sh\n"+strings.ReplaceAll(script, "DIR", dir)+"\n", 0o755)
	}
}

// ufwStatus is what ufw status verbose prints on a host that keeps the
// runbook's "Firewall", followed by extra rules.
func ufwStatus(extra ...string) string {
	rules := []string{
		"22/tcp                     ALLOW IN    Anywhere",
		"80/tcp                     ALLOW IN    Anywhere",
		"443/tcp                    ALLOW IN    Anywhere",
		"443/udp                    ALLOW IN    Anywhere",
		"51820/udp                  ALLOW IN    203.0.113.10",
		"8091:8600/tcp on wg0       ALLOW IN    10.77.0.1",
		"22/tcp (v6)                ALLOW IN    Anywhere (v6)",
		"80/tcp (v6)                ALLOW IN    Anywhere (v6)",
		"443/tcp (v6)               ALLOW IN    Anywhere (v6)",
		"443/udp (v6)               ALLOW IN    Anywhere (v6)",
	}
	return "Status: active\nLogging: on (low)\nDefault: deny (incoming), allow (outgoing), deny (routed)\nNew profiles: skip\n\n" +
		"To                         Action      From\n--                         ------      ----\n" +
		strings.Join(append(rules, extra...), "\n") + "\n\n"
}

// The preflight on a host, without --no-systemd: the units, systemd-analyze,
// caddy, wg0 and ufw are read from stubs, so that every check runs and each
// can fail.
func TestPreflightScriptOnAHost(t *testing.T) {
	p := newPreflightRig(t)
	stubs := t.TempDir()
	hostStubs(t, stubs)
	canned := func(files map[string]string) {
		t.Helper()
		for _, name := range []string{"active-aml-serve@beta-alice.service", "active-aml-watch@beta-alice.timer", "active-caddy.service", "verify-fails", "validate-fails", "ip-link", "ip-addr", "ufw-status"} {
			os.Remove(filepath.Join(stubs, name))
		}
		for name, text := range files {
			writeFile(t, filepath.Join(stubs, name), text, 0o644)
		}
	}
	// The admin listener is the fake's, on loopback: wg0 carries its address.
	admin := p.h.env("/etc/mnemon-aml/beta-alice/env")["AML_ADMIN_LISTEN"]
	ip := admin[:strings.LastIndex(admin, ":")]
	upLink := "5: wg0: <POINTOPOINT,NOARP,UP,LOWER_UP> mtu 1420 qdisc noqueue state UNKNOWN mode DEFAULT group default qlen 1000\\    link/none \n"
	addr := "5: wg0    inet " + ip + "/24 scope global wg0\\       valid_lft forever preferred_lft forever\n"
	healthy := map[string]string{"ip-link": upLink, "ip-addr": addr, "ufw-status": ufwStatus()}
	run := func() (string, bool) {
		t.Helper()
		command := exec.Command("bash", filepath.Join(p.h.repo, "deploy", "aml", "preflight.sh"), "--root", p.h.root, "beta-alice")
		command.Env = []string{"PATH=" + stubs + string(os.PathListSeparator) + p.h.bin + string(os.PathListSeparator) + os.Getenv("PATH"), "HOME=" + p.h.root, "LC_ALL=C", "CURL_CA_BUNDLE=" + p.caBundle}
		var out bytes.Buffer
		command.Stdout, command.Stderr = &out, &out
		err := command.Run()
		if _, ok := err.(*exec.ExitError); err != nil && !ok {
			t.Fatal(err)
		}
		for label, secret := range p.h.secrets {
			if strings.Contains(out.String(), secret) {
				t.Errorf("the preflight printed the %s", label)
			}
		}
		return out.String(), err == nil
	}

	canned(healthy)
	if out, ok := run(); !ok || len(fails(out)) != 0 || !strings.Contains(out, "mnemon-preflight beta-alice: all 15 checks passed") {
		t.Fatalf("the preflight of a ready host failed:\n%s", out)
	}

	// Everything on the host's side wrong at once: one FAIL line each.
	canned(map[string]string{
		"active-aml-watch@beta-alice.timer": "inactive",
		"active-caddy.service":              "failed",
		"verify-fails":                      "/etc/systemd/system/aml-serve@.service:30: Unknown key name 'Bogus'\n",
		"validate-fails":                    "",
		"ip-link":                           "5: wg0: <POINTOPOINT,NOARP> mtu 1420 qdisc noqueue state DOWN mode DEFAULT group default qlen 1000\\    link/none \n",
		"ip-addr":                           "5: wg0    inet 10.9.9.9/24 scope global wg0\\       valid_lft forever preferred_lft forever\n",
		"ufw-status":                        "Status: inactive\n",
	})
	out, ok := run()
	want := []string{
		"aml-watch@beta-alice.timer is inactive, not active",
		"caddy.service is failed, not active",
		"systemd-analyze verify of the unit templates failed: /etc/systemd/system/aml-serve@.service:30: Unknown key name 'Bogus'",
		"caddy validate --config /etc/caddy/Caddyfile failed",
		"wg0 is down",
		"wg0 does not carry " + ip + ", the address of AML_ADMIN_LISTEN",
		"ufw is not active",
		"ufw does not deny incoming by default",
		"ufw does not allow 22/tcp",
		"ufw does not allow 80/tcp",
		"ufw does not allow 443/tcp",
		"ufw does not allow 443/udp",
		"ufw does not allow 51820/udp from the ops host",
		"ufw does not let the hub reach the admin ports",
	}
	got := fails(out)
	if ok || len(got) != len(want) || !strings.Contains(out, fmt.Sprintf("%d failures in 15 checks", len(want))) {
		t.Errorf("want %d FAIL lines in 15 checks:\n%s", len(want), out)
	}
	for i, w := range want {
		if i >= len(got) || !strings.Contains(got[i], w) {
			t.Errorf("FAIL line %d is not about %q:\n%s", i+1, w, strings.Join(got, "\n"))
		}
	}

	// No wg0 at all.
	canned(map[string]string{"ufw-status": ufwStatus()})
	if out, _ := run(); len(fails(out)) != 1 || !strings.Contains(fails(out)[0], "wg0 does not exist") {
		t.Errorf("a host without wg0:\n%s", out)
	}

	// A rule that lets anyone reach 51820/udp or an admin port (8091 to 8600
	// over tcp), however ufw writes it, fails, naming the rule.
	for _, rule := range []string{
		"51820/udp                  ALLOW IN    Anywhere",
		"51820/udp (v6)             ALLOW IN    Anywhere (v6)",
		"51820                      ALLOW IN    Anywhere",
		"8091:8600/tcp              ALLOW IN    Anywhere",
		"8091:8600/tcp (v6)         ALLOW IN    Anywhere (v6)",
		"8091:8600/tcp on wg0       ALLOW IN    Anywhere",
		"8091:8600/tcp (v6) on wg0  ALLOW IN    Anywhere (v6)",
		"8101/tcp                   ALLOW IN    Anywhere",
		"8600/tcp (v6)              ALLOW IN    Anywhere (v6)",
		"8101                       ALLOW IN    Anywhere",
		"8100,8101/tcp              ALLOW IN    Anywhere",
		"8000:9000/tcp              ALLOW IN    Anywhere",
		"8101/tcp                   LIMIT IN    Anywhere",
		"8101/tcp                   ALLOW IN    Anywhere                   # admin",
		"8101/tcp                   ALLOW IN    Anywhere   ",
		"10.77.0.3 8101/tcp         ALLOW IN    Anywhere",
		"Anywhere on wg0            ALLOW IN    Anywhere",
		"Anywhere (v6) on wg0       ALLOW IN    Anywhere (v6)",
		// A source that names a protocol: ufw writes "Anywhere/tcp" or
		// "Anywhere/udp" for a rule given one (the final review's parked
		// T15-b); a destination that is all of a protocol's ports holds the
		// ports too.
		"8101/tcp                   ALLOW IN    Anywhere/tcp",
		"8101/tcp (v6)              ALLOW IN    Anywhere/tcp (v6)",
		"8091:8600/tcp on wg0       ALLOW IN    Anywhere/tcp",
		"8101/tcp                   LIMIT IN    Anywhere/tcp               # admin",
		"51820/udp                  ALLOW IN    Anywhere/udp",
		"51820/udp (v6)             ALLOW IN    Anywhere/udp (v6)",
		"51820                      ALLOW IN    Anywhere/udp",
		"Anywhere/tcp               ALLOW IN    Anywhere/tcp",
		"Anywhere/udp               ALLOW IN    Anywhere/udp",
		"Anywhere/tcp (v6)          ALLOW IN    Anywhere/tcp (v6)",
		"Anywhere/udp on wg0        ALLOW IN    Anywhere/udp",
	} {
		canned(map[string]string{"ip-link": upLink, "ip-addr": addr, "ufw-status": ufwStatus(rule)})
		out, ok := run()
		got := fails(out)
		squeezed := strings.Join(strings.Fields(rule), " ")
		if ok || len(got) != 1 || !strings.Contains(got[0], "from anywhere") || !strings.Contains(got[0], squeezed) {
			t.Errorf("the rule %q: want one FAIL line naming it:\n%s", squeezed, out)
		}
	}
	// Rules that let no one else in pass.
	for _, rule := range []string{
		"8090/tcp                   ALLOW IN    Anywhere",
		"8601/tcp                   ALLOW IN    Anywhere",
		"8101/udp                   ALLOW IN    Anywhere",
		"51820/tcp                  ALLOW IN    Anywhere",
		"51821/udp                  ALLOW IN    Anywhere",
		"8101/tcp                   DENY IN     Anywhere",
		"8101/tcp                   ALLOW IN    10.77.0.1",
		"8091:8600/tcp (v6) on wg0  ALLOW IN    fd00::1",
		"51820/udp (v6)             ALLOW IN    2001:db8::10",
		"Nginx Full                 ALLOW IN    Anywhere",
		"8101/tcp                   ALLOW OUT   Anywhere",
		"8101/udp                   ALLOW IN    Anywhere/udp",
		"51820/tcp                  ALLOW IN    Anywhere/tcp",
		"8090/tcp                   ALLOW IN    Anywhere/tcp",
		"51821/udp (v6)             ALLOW IN    Anywhere/udp (v6)",
		"8101/tcp                   ALLOW IN    10.77.0.1/tcp",
	} {
		canned(map[string]string{"ip-link": upLink, "ip-addr": addr, "ufw-status": ufwStatus(rule)})
		if out, ok := run(); !ok || len(fails(out)) != 0 {
			t.Errorf("the rule %q fails the preflight:\n%s", rule, out)
		}
	}
}
