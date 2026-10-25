// Package deploy_test runs the memory cloud's host scripts
// (deploy/aml/instance.sh, installed as mnemon-instance) in a temporary tree,
// with --root and --no-systemd, as the runbook runs them on a host.
package deploy_test

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// secretShape is the shape of an API key and of an admin token: mnm_ and 32
// random bytes in base64url without padding.
var secretShape = regexp.MustCompile(`^mnm_[A-Za-z0-9_-]{43}$`)

// reservedNames are the instance names the script refuses: they collide with
// the kit's paths or read as common words.
var reservedNames = []string{"data", "env", "host", "host-env", "users", "watch", "instances", "aml", "mnemon", "caddy", "test-instance"}

// host is a temporary host: the --root tree after the one-time install steps
// the script needs (host.env, the shipped configuration, the host's
// Caddyfile), and tripwires in place of the tools --no-systemd must never run.
type host struct {
	t        *testing.T
	repo     string
	root     string
	bin      string
	tripwire string
	// secrets are every secret the tree has held, by where it came from, and
	// users the raw user ids given to delete-user.
	secrets map[string]string
	users   []string
	runs    []run
}

// run is one run of the script, kept to check its output for secrets.
type run struct {
	args           []string
	stdout, stderr string
	ok             bool
}

func newHost(t *testing.T, caddyfile string) *host {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the instance script runs on Linux hosts; Windows has no POSIX file modes")
	}
	for _, tool := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is missing; the instance script needs it", tool)
		}
	}
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	h := &host{t: t, repo: repo, root: t.TempDir(), bin: t.TempDir(), secrets: map[string]string{}}
	h.tripwire = filepath.Join(h.bin, "tripwire")
	for _, tool := range []string{"systemctl", "logger", "curl", "caddy", "systemd-analyze"} {
		writeFile(t, filepath.Join(h.bin, tool), "#!/bin/sh\necho \""+tool+" $*\" >> '"+h.tripwire+"'\nexit 97\n", 0o755)
	}
	writeFile(t, h.path("/usr/local/sbin/mnemon-preflight"), "#!/bin/sh\necho \"mnemon-preflight $*\" >> '"+h.tripwire+"'\nexit 97\n", 0o755)

	h.secrets["DEEPSEEK_API_KEY"] = "sk-" + randomHex(t)
	h.secrets["TYPESAFE_API_KEY"] = "ts-" + randomHex(t)
	h.secrets["AML_ALERT_WEBHOOK"] = "https://open.feishu.cn/open-apis/bot/v2/hook/" + randomHex(t)
	h.writeHostEnv(nil)
	shipped, err := os.ReadFile(filepath.Join(repo, "deploy", "aml", "memory-config.industrial.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, h.path("/usr/local/share/mnemon-aml/memory-config.industrial.json"), string(shipped), 0o644)
	if caddyfile != "" {
		raw, err := os.ReadFile(filepath.Join(repo, "deploy", "aml", caddyfile))
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, h.path("/etc/caddy/Caddyfile"), string(raw), 0o644)
	}
	return h
}

// writeHostEnv writes host.env (mode 0600): the host's secrets and
// AML_ALERT_KIND=feishu, with changes; an empty value leaves its name out.
func (h *host) writeHostEnv(changes map[string]string) {
	h.t.Helper()
	values := map[string]string{"AML_ALERT_KIND": "feishu"}
	for _, name := range []string{"DEEPSEEK_API_KEY", "TYPESAFE_API_KEY", "AML_ALERT_WEBHOOK"} {
		values[name] = h.secrets[name]
	}
	maps.Copy(values, changes)
	text := "# The host's secrets: provider keys and the alert bot.\n"
	for _, name := range slices.Sorted(maps.Keys(values)) {
		if values[name] != "" {
			text += name + "=" + values[name] + "\n"
		}
	}
	writeFile(h.t, h.path("/etc/mnemon-aml/host.env"), text, 0o600)
}

func (h *host) path(hostPath string) string {
	return filepath.Join(h.root, filepath.FromSlash(hostPath))
}

// run runs the script with --root and --no-systemd, with stdin as its
// standard input.
func (h *host) run(stdin string, args ...string) run {
	h.t.Helper()
	command := exec.Command("bash", append([]string{filepath.Join(h.repo, "deploy", "aml", "instance.sh"), "--root", h.root, "--no-systemd"}, args...)...)
	command.Stdin = strings.NewReader(stdin)
	// No secret reaches the script but through host.env.
	command.Env = []string{"PATH=" + h.bin + string(os.PathListSeparator) + os.Getenv("PATH"), "HOME=" + h.root, "LC_ALL=C"}
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if _, ok := err.(*exec.ExitError); err != nil && !ok {
		h.t.Fatal(err)
	}
	if trips, _ := os.ReadFile(h.tripwire); len(trips) > 0 {
		h.t.Fatalf("instance.sh --no-systemd %s ran:\n%s", strings.Join(args, " "), trips)
	}
	r := run{args: args, stdout: stdout.String(), stderr: stderr.String(), ok: err == nil}
	h.runs = append(h.runs, r)
	return r
}

// ok runs the script and fails the test unless it succeeds; it returns the
// standard output.
func (h *host) ok(stdin string, args ...string) string {
	h.t.Helper()
	r := h.run(stdin, args...)
	if !r.ok {
		h.t.Fatalf("instance.sh %s failed:\n%s%s", strings.Join(args, " "), r.stdout, r.stderr)
	}
	return r.stdout
}

// refused runs the script and fails the test unless it fails without
// changing a file of the tree; it returns the standard error.
func (h *host) refused(stdin string, args ...string) string {
	h.t.Helper()
	before := h.tree()
	r := h.run(stdin, args...)
	if r.ok {
		h.t.Fatalf("instance.sh %s succeeded:\n%s", strings.Join(args, " "), r.stdout)
	}
	if after := h.tree(); !reflect.DeepEqual(before, after) {
		h.t.Fatalf("the refused instance.sh %s changed the tree:\nbefore %v\nafter  %v", strings.Join(args, " "), before, after)
	}
	if r.stderr == "" {
		h.t.Fatalf("instance.sh %s failed without saying why", strings.Join(args, " "))
	}
	return r.stderr
}

// tree is every file and directory of the root, with its mode and, for a
// file, its contents.
func (h *host) tree() map[string]string {
	h.t.Helper()
	tree := map[string]string{}
	err := filepath.WalkDir(h.root, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		value := info.Mode().String()
		if entry.Type().IsRegular() {
			raw, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			value += " " + string(raw)
		}
		tree[strings.TrimPrefix(file, h.root)] = value
		return nil
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return tree
}

// env reads an environment file of the tree: its NAME=value lines.
func (h *host) env(hostPath string) map[string]string {
	h.t.Helper()
	raw, err := os.ReadFile(h.path(hostPath))
	if err != nil {
		h.t.Fatal(err)
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if name, value, ok := strings.Cut(line, "="); ok && !strings.HasPrefix(line, "#") {
			if _, dup := env[name]; dup {
				h.t.Fatalf("%s sets %s twice", hostPath, name)
			}
			env[name] = value
		}
	}
	return env
}

func (h *host) read(hostPath string) string {
	h.t.Helper()
	raw, err := os.ReadFile(h.path(hostPath))
	if err != nil {
		h.t.Fatal(err)
	}
	return string(raw)
}

// mode is the permission bits of a path of the tree.
func (h *host) mode(hostPath string) fs.FileMode {
	h.t.Helper()
	info, err := os.Stat(h.path(hostPath))
	if err != nil {
		h.t.Fatal(err)
	}
	return info.Mode().Perm()
}

func (h *host) exists(hostPath string) bool {
	_, err := os.Stat(h.path(hostPath))
	return err == nil
}

// instance checks the files new wrote for name: their modes, and that each
// environment file holds exactly what it needs. It returns the engine's and
// the watcher's environment, and records the instance's secrets.
func (h *host) instance(name string) (env, watch map[string]string) {
	h.t.Helper()
	for hostPath, want := range map[string]fs.FileMode{
		"/etc/mnemon-aml/" + name:                                          0o750,
		"/etc/mnemon-aml/" + name + "/env":                                 0o600,
		"/etc/mnemon-aml/" + name + "/watch.env":                           0o600,
		"/etc/mnemon-aml/" + name + "/memory-config.json":                  0o640,
		"/var/lib/mnemon-aml/" + name:                                      0o700,
		"/var/lib/mnemon-aml-watch/" + name:                                0o700,
		"/etc/systemd/system/aml-serve@" + name + ".service.d":             0o755,
		"/etc/systemd/system/aml-serve@" + name + ".service.d/limits.conf": 0o644,
	} {
		if got := h.mode(hostPath); got != want {
			h.t.Errorf("%s has mode %o, want %o", hostPath, got, want)
		}
	}
	env = h.env("/etc/mnemon-aml/" + name + "/env")
	watch = h.env("/etc/mnemon-aml/" + name + "/watch.env")
	for _, secret := range []string{env["AML_API_KEY"], env["MNEMON_ADMIN_TOKEN"]} {
		if !secretShape.MatchString(secret) {
			h.t.Errorf("%s: secret %q is not mnm_ and 43 base64url characters", name, secret)
		}
	}
	if env["AML_API_KEY"] == env["MNEMON_ADMIN_TOKEN"] {
		h.t.Error(name, ": the API key is the admin token")
	}
	// Each value goes into exactly the file that needs it.
	if env["DEEPSEEK_API_KEY"] != h.secrets["DEEPSEEK_API_KEY"] || env["TYPESAFE_API_KEY"] != h.secrets["TYPESAFE_API_KEY"] {
		h.t.Errorf("%s: env does not carry the host's provider keys", name)
	}
	for _, other := range []string{"AML_ALERT_WEBHOOK", "AML_ALERT_KIND", "OPENAI_API_KEY"} {
		if _, ok := env[other]; ok {
			h.t.Errorf("%s: env holds %s", name, other)
		}
	}
	if watch["AML_ALERT_WEBHOOK"] != h.secrets["AML_ALERT_WEBHOOK"] || watch["AML_ALERT_KIND"] != h.env("/etc/mnemon-aml/host.env")["AML_ALERT_KIND"] {
		h.t.Errorf("%s: watch.env does not carry the host's alert bot", name)
	}
	if watch["MNEMON_ADMIN_TOKEN"] != env["MNEMON_ADMIN_TOKEN"] || watch["AML_ADMIN_LISTEN"] != env["AML_ADMIN_LISTEN"] {
		h.t.Errorf("%s: watch.env's admin listener or token differs from the engine's", name)
	}
	if got := slices.Sorted(maps.Keys(watch)); !slices.Equal(got, []string{"AML_ADMIN_LISTEN", "AML_ALERT_KIND", "AML_ALERT_WEBHOOK", "AML_PUBLIC_URL", "MNEMON_ADMIN_TOKEN"}) {
		h.t.Errorf("%s: watch.env holds %q", name, got)
	}
	h.secrets[name+" API key"] = env["AML_API_KEY"]
	h.secrets[name+" admin token"] = env["MNEMON_ADMIN_TOKEN"]
	return env, watch
}

// checkConfig checks that the instance's configuration is the shipped one
// with the instance's caps and budget file.
func (h *host) checkConfig(name string, deepseek, jev float64) {
	h.t.Helper()
	var shipped, got map[string]any
	if err := json.Unmarshal([]byte(h.read("/usr/local/share/mnemon-aml/memory-config.industrial.json")), &shipped); err != nil {
		h.t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(h.read("/etc/mnemon-aml/"+name+"/memory-config.json")), &got); err != nil {
		h.t.Fatal(err)
	}
	shipped["budgetFile"] = "/var/lib/mnemon-aml/" + name + "/model-budget.db"
	shipped["budgets"] = map[string]any{"deepseek": deepseek, "jev": jev}
	if !reflect.DeepEqual(got, shipped) {
		h.t.Errorf("%s: memory-config.json\n got %v\nwant %v", name, got, shipped)
	}
}

// checkLimits checks the instance's limits drop-in.
func (h *host) checkLimits(name, want string) {
	h.t.Helper()
	var settings []string
	raw := h.read("/etc/systemd/system/aml-serve@" + name + ".service.d/limits.conf")
	// The note on a host's file points at the runbook, not at a step of the
	// plan the kit was built by.
	if strings.Contains(raw, "Task ") || strings.Contains(raw, "serve-hardening") {
		h.t.Errorf("%s: limits.conf names the build plan, want the runbook:\n%s", name, raw)
	}
	for _, line := range strings.Split(raw, "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			settings = append(settings, line)
		}
	}
	if got := strings.Join(settings, "\n"); got != want {
		h.t.Errorf("%s: limits.conf\n got %q\nwant %q", name, got, want)
	}
}

// checkNoSecrets checks every run's output and the journal: no run prints a
// secret on its standard error; on its standard output only new prints the
// instance's API key and admin token, rotate-key its new key and inspect
// enable its inspect token (labels "<n> API key", "<n> admin token", "<n>
// rotated API key" and "<n> inspect token"). Neither the journal nor any
// output carries a raw user id.
func (h *host) checkNoSecrets() {
	h.t.Helper()
	journal, err := os.ReadFile(h.path("/var/log/mnemon-instance.log"))
	if err != nil && !os.IsNotExist(err) {
		h.t.Fatal(err)
	}
	for label, secret := range h.secrets {
		if strings.Contains(string(journal), secret) {
			h.t.Errorf("the journal holds the %s", label)
		}
	}
	for _, r := range h.runs {
		allowed := map[string]bool{}
		switch {
		case r.args[0] == "new" && r.ok:
			allowed[r.args[1]+" API key"], allowed[r.args[1]+" admin token"] = true, true
		case r.args[0] == "rotate-key" && r.ok:
			allowed[r.args[1]+" rotated API key"] = true
		case len(r.args) >= 3 && r.args[0] == "inspect" && r.args[1] == "enable":
			// Printed once written, even when the start then fails.
			allowed[r.args[2]+" inspect token"] = true
		}
		for label, secret := range h.secrets {
			if strings.Contains(r.stderr, secret) || strings.Contains(r.stdout, secret) && !allowed[label] {
				h.t.Errorf("instance.sh %s printed the %s", strings.Join(r.args, " "), label)
			}
		}
		for _, user := range h.users {
			if strings.Contains(r.stdout+r.stderr, user) {
				h.t.Errorf("instance.sh %s printed the user id %q", strings.Join(r.args, " "), user)
			}
		}
	}
	for _, user := range h.users {
		if strings.Contains(string(journal), user) {
			h.t.Errorf("the journal holds the user id %q", user)
		}
	}
}

// journal is the journal's lines without their time stamps.
func (h *host) journal() []string {
	h.t.Helper()
	var lines []string
	for _, line := range strings.Split(strings.TrimSuffix(h.read("/var/log/mnemon-instance.log"), "\n"), "\n") {
		_, message, ok := strings.Cut(line, " mnemon-instance: ")
		if !ok {
			h.t.Fatalf("journal line %q is not <time> mnemon-instance: <message>", line)
		}
		lines = append(lines, message)
	}
	return lines
}

func fingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:12]
}

// storeKey is the directory name of a user's store: the first 32 hex digits
// of the SHA-256 of the user id.
func storeKey(user string) string {
	sum := sha256.Sum256([]byte(user))
	return hex.EncodeToString(sum[:])[:32]
}

func randomHex(t *testing.T) string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(raw)
}

func writeFile(t *testing.T, file, text string, mode fs.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(text), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, mode); err != nil {
		t.Fatal(err)
	}
}

// collapse is a Caddy file without its comments, its whitespace collapsed.
func collapse(text string) string {
	var kept []string
	for _, line := range strings.Split(text, "\n") {
		if before, _, found := strings.Cut(line, "#"); found {
			line = before
		}
		kept = append(kept, line)
	}
	return strings.Join(strings.Fields(strings.Join(kept, "\n")), " ")
}

// registration is the registration JSON new prints, exactly as the memory
// cloud design shows it.
func registration(id, kind, hostName, ip, publicURL, adminURL, token, key string) string {
	return `{"id":"` + id + `","kind":"` + kind + `",` + "\n" +
		` "host":{"name":"` + hostName + `","vpcIp":"` + ip + `"},` + "\n" +
		` "publicUrl":"` + publicURL + `",` + "\n" +
		` "adminUrl":"` + adminURL + `",` + "\n" +
		` "adminToken":"` + token + `","apiKeyFingerprint":"` + fingerprint(key) + `"}` + "\n"
}

// checkOutput checks new's one-time output: the base URL, the API key once,
// the admin URL, the admin token and the registration JSON, which parses and
// never holds the API key.
func checkOutput(t *testing.T, out, publicURL, adminURL, key, reg string) {
	t.Helper()
	for _, want := range []string{publicURL, adminURL, reg} {
		if !strings.Contains(out, want) {
			t.Errorf("new's output lacks %q:\n%s", want, out)
		}
	}
	if n := strings.Count(out, key); n != 1 {
		t.Errorf("new printed the API key %d times, want once", n)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(reg), &parsed); err != nil || strings.Contains(reg, key) {
		t.Errorf("the registration JSON does not parse (%v) or holds the API key", err)
	}
}

func TestInstanceScriptNew(t *testing.T) {
	t.Run("dedicated", func(t *testing.T) {
		h := newHost(t, "Caddyfile.dedicated")
		args := []string{"new", "eval", "--layout", "dedicated", "--public-url", "https://eval.example.com",
			"--admin-ip", "10.77.0.2", "--host-name", "eval-1", "--deepseek-cap", "600", "--jev-cap", "60"}
		out := h.ok("", args...)
		env, watch := h.instance("eval")
		for name, want := range map[string]string{
			"AML_LAYOUT": "dedicated", "AML_SLOT": "0", "AML_STATUS": "active",
			"AML_LISTEN": "127.0.0.1:8090", "AML_ADMIN_LISTEN": "10.77.0.2:8091",
			"AML_MAX_ADDS": "16", "AML_MAX_SEARCHES": "16", "AML_MAX_OPEN_STORES": "16", "AML_WORKERS": "4",
			// 80% of MemoryMax=40G, in whole MiB.
			"GOMEMLIMIT": "32768MiB",
		} {
			if env[name] != want {
				t.Errorf("env %s=%s, want %s", name, env[name], want)
			}
		}
		if watch["AML_PUBLIC_URL"] != "https://eval.example.com" {
			t.Errorf("watch.env AML_PUBLIC_URL=%s", watch["AML_PUBLIC_URL"])
		}
		h.checkConfig("eval", 600, 60)
		h.checkLimits("eval", "[Service]\nMemoryMax=40G")
		// A dedicated host's route is its Caddyfile.dedicated.
		if h.exists("/etc/caddy/mnemon-instances") {
			t.Error("new --layout dedicated wrote a Caddy snippet")
		}
		key, token := env["AML_API_KEY"], env["MNEMON_ADMIN_TOKEN"]
		checkOutput(t, out, "https://eval.example.com", "http://10.77.0.2:8091", key,
			registration("eval", "dedicated", "eval-1", "10.77.0.2", "https://eval.example.com", "http://10.77.0.2:8091", token, key))

		// The same name again is refused, and a dedicated host holds one
		// instance.
		h.refused("", args...)
		other := []string{"new", "other", "--admin-ip", "10.77.0.2", "--host-name", "eval-1"}
		if stderr := h.refused("", append(other, "--layout", "dedicated", "--public-url", "https://other.example.com")...); !strings.Contains(stderr, "eval") {
			t.Errorf("the refusal of a second instance on a dedicated host does not name eval: %s", stderr)
		}
		h.refused("", append(other, "--layout", "shared", "--public-url", "https://eval.example.com/i/other")...)
		if got := h.journal(); len(got) != 1 || !strings.HasPrefix(got[0], "new eval: ") {
			t.Errorf("journal %q, want one new line", got)
		}
		h.checkNoSecrets()
	})

	t.Run("shared", func(t *testing.T) {
		h := newHost(t, "Caddyfile.shared")
		out := h.ok("", "new", "beta-alice", "--layout", "shared", "--public-url", "https://beta.example.com/i/beta-alice",
			"--admin-ip", "10.77.0.3", "--host-name", "beta-1")
		alice, aliceWatch := h.instance("beta-alice")
		for name, want := range map[string]string{
			"AML_LAYOUT": "shared", "AML_SLOT": "0", "AML_STATUS": "active",
			"AML_LISTEN": "127.0.0.1:8100", "AML_ADMIN_LISTEN": "10.77.0.3:8101",
			"AML_MAX_ADDS": "4", "AML_MAX_SEARCHES": "4", "AML_MAX_OPEN_STORES": "2", "AML_WORKERS": "1",
			// 80% of MemoryMax=1G is 819.2 MiB.
			"GOMEMLIMIT": "819MiB",
		} {
			if alice[name] != want {
				t.Errorf("beta-alice env %s=%s, want %s", name, alice[name], want)
			}
		}
		if aliceWatch["AML_PUBLIC_URL"] != "https://beta.example.com/i/beta-alice" {
			t.Errorf("watch.env AML_PUBLIC_URL=%s", aliceWatch["AML_PUBLIC_URL"])
		}
		h.checkConfig("beta-alice", 30, 3)
		h.checkLimits("beta-alice", "[Service]\nMemoryMax=1G\nCPUQuota=100%")
		checkOutput(t, out, "https://beta.example.com/i/beta-alice", "http://10.77.0.3:8101", alice["AML_API_KEY"],
			registration("beta-alice", "shared", "beta-1", "10.77.0.3", "https://beta.example.com/i/beta-alice", "http://10.77.0.3:8101", alice["MNEMON_ADMIN_TOKEN"], alice["AML_API_KEY"]))

		// The route: the instance's prefix, stripped, to its port, holding
		// requests through a restart.
		const snippet = "/etc/caddy/mnemon-instances/beta-alice.caddy"
		if got := h.mode(snippet); got != 0o644 {
			t.Errorf("%s has mode %o, want 644 (Caddy reads it)", snippet, got)
		}
		route := collapse(h.read(snippet))
		for _, want := range []string{
			"handle_path /i/beta-alice/* { reverse_proxy 127.0.0.1:8100 {", "lb_try_duration 30s", "lb_try_interval 250ms",
			"keepalive 60s", "dial_timeout 5s", "response_header_timeout 35m", "read_timeout 35m", "write_timeout 35m",
		} {
			if !strings.Contains(route, want) {
				t.Errorf("%s lacks %q:\n%s", snippet, want, route)
			}
		}

		// The second instance takes the next slot, and every default can be
		// overridden.
		out = h.ok("", "new", "beta-bob", "--layout=shared", "--public-url", "https://beta.example.com/i/beta-bob",
			"--admin-ip", "10.77.0.3", "--host-name", "beta-1", "--deepseek-cap", "600", "--jev-cap", "60",
			"--memory-max", "2G", "--cpu-quota", "150%", "--max-concurrent-adds", "8", "--max-concurrent-searches", "6",
			"--max-open-stores", "3", "--consolidation-workers", "2")
		bob, _ := h.instance("beta-bob")
		for name, want := range map[string]string{
			"AML_SLOT": "1", "AML_LISTEN": "127.0.0.1:8110", "AML_ADMIN_LISTEN": "10.77.0.3:8111",
			"AML_MAX_ADDS": "8", "AML_MAX_SEARCHES": "6", "AML_MAX_OPEN_STORES": "3", "AML_WORKERS": "2",
			// --memory-max 2G: 1638.4 MiB.
			"GOMEMLIMIT": "1638MiB",
		} {
			if bob[name] != want {
				t.Errorf("beta-bob env %s=%s, want %s", name, bob[name], want)
			}
		}
		h.checkConfig("beta-bob", 600, 60)
		h.checkLimits("beta-bob", "[Service]\nMemoryMax=2G\nCPUQuota=150%")
		if route := collapse(h.read("/etc/caddy/mnemon-instances/beta-bob.caddy")); !strings.Contains(route, "handle_path /i/beta-bob/* { reverse_proxy 127.0.0.1:8110 {") {
			t.Errorf("beta-bob's snippet: %s", route)
		}
		checkOutput(t, out, "https://beta.example.com/i/beta-bob", "http://10.77.0.3:8111", bob["AML_API_KEY"],
			registration("beta-bob", "shared", "beta-1", "10.77.0.3", "https://beta.example.com/i/beta-bob", "http://10.77.0.3:8111", bob["MNEMON_ADMIN_TOKEN"], bob["AML_API_KEY"]))
		if bob["AML_API_KEY"] == alice["AML_API_KEY"] || bob["MNEMON_ADMIN_TOKEN"] == alice["MNEMON_ADMIN_TOKEN"] {
			t.Error("two instances share a secret")
		}

		// The same name again is refused, and so is a dedicated instance on
		// a shared host.
		h.refused("", "new", "beta-alice", "--layout", "shared", "--public-url", "https://beta.example.com/i/beta-alice", "--admin-ip", "10.77.0.3")
		h.refused("", "new", "eval", "--layout", "dedicated", "--public-url", "https://eval.example.com", "--admin-ip", "10.77.0.3")
		h.checkNoSecrets()
	})

	t.Run("refusals", func(t *testing.T) {
		h := newHost(t, "Caddyfile.shared")
		if got := strings.TrimSpace(h.ok("", "list")); len(strings.Split(got, "\n")) != 1 {
			t.Errorf("list on a host without instances printed\n%s", got)
		}
		shared := func(name string, extra ...string) []string {
			return append([]string{"new", name, "--layout", "shared", "--public-url", "https://beta.example.com/i/" + name,
				"--admin-ip", "10.77.0.3", "--host-name", "beta-1"}, extra...)
		}
		// Names match ^[a-z][a-z0-9-]{1,30}$.
		for _, name := range []string{"", "a", "Beta", "9beta", "-beta", "beta_1", "beta.1", "beta/x", "..", "beta alice", "b" + strings.Repeat("x", 31)} {
			h.refused("", shared(name)...)
		}
		h.ok("", shared("b"+strings.Repeat("x", 30))...)
		// Names that collide with the kit's paths or read as common words are
		// reserved.
		for _, name := range reservedNames {
			if stderr := h.refused("", shared(name)...); !strings.Contains(stderr, "reserved") {
				t.Errorf("new %s was not refused as a reserved name: %s", name, stderr)
			}
		}
		for _, extra := range [][]string{
			{"--layout", "cluster"},
			{"--public-url", "http://beta.example.com/i/beta-x"},
			{"--public-url", "https://beta.example.com"},
			{"--public-url", "https://beta.example.com/i/beta-y"},
			{"--public-url", "https://beta.example.com/i/beta-x?key=1"},
			{"--admin-ip", "8.8.8.8"},
			{"--admin-ip", "127.0.0.1"},
			{"--admin-ip", "10.0.0.256"},
			{"--admin-ip", "10.0.0"},
			{"--admin-ip", "010.0.0.1"},
			{"--admin-ip", "172.32.0.1"},
			{"--admin-ip", "fd00::1"},
			{"--host-name", "beta 1"},
			{"--deepseek-cap", "0"},
			{"--jev-cap", "-1"},
			{"--jev-cap", "lots"},
			{"--memory-max", "1g"},
			// Under 1 MiB of GOMEMLIMIT, and a size whose arithmetic would
			// overflow.
			{"--memory-max", "1M"},
			{"--memory-max", "1279K"},
			{"--memory-max", "12345678901G"},
			{"--cpu-quota", "100"},
			{"--max-open-stores", "0"},
			{"--consolidation-workers", "0"},
			{"--max-concurrent-adds", "four"},
			{"--frobnicate", "1"},
			{"--admin-ip"},
		} {
			h.refused("", shared("beta-x", extra...)...)
		}
		h.refused("", "new", "beta-x", "--layout", "shared", "--public-url", "https://beta.example.com/i/beta-x")

		// host.env must be root's alone, and hold every name the shipped
		// configuration and the watcher need; a refusal names the variable,
		// never a value.
		for _, missing := range []string{"DEEPSEEK_API_KEY", "TYPESAFE_API_KEY", "AML_ALERT_WEBHOOK", "AML_ALERT_KIND"} {
			h.writeHostEnv(map[string]string{missing: ""})
			if stderr := h.refused("", shared("beta-x")...); !strings.Contains(stderr, missing) {
				t.Errorf("the refusal for a host.env without %s does not name it: %s", missing, stderr)
			}
		}
		h.secrets["plain-http webhook"] = "http://open.feishu.cn/open-apis/bot/v2/hook/" + randomHex(t)
		for _, change := range []map[string]string{
			{"AML_ALERT_KIND": "slack"},
			{"AML_ALERT_WEBHOOK": h.secrets["plain-http webhook"]},
		} {
			h.writeHostEnv(change)
			h.refused("", shared("beta-x")...)
		}
		h.writeHostEnv(nil)
		if err := os.Chmod(h.path("/etc/mnemon-aml/host.env"), 0o644); err != nil {
			t.Fatal(err)
		}
		if stderr := h.refused("", shared("beta-x")...); !strings.Contains(stderr, "0600") {
			t.Errorf("the refusal for a host.env of mode 0644 does not say so: %s", stderr)
		}
		if err := os.Remove(h.path("/etc/mnemon-aml/host.env")); err != nil {
			t.Fatal(err)
		}
		h.refused("", shared("beta-x")...)
		h.writeHostEnv(nil)

		// Whatever is left of an instance blocks a new one of that name, and
		// stays as it was: a data root above all.
		for _, leftover := range []string{"/var/lib/mnemon-aml/beta-old/data/users/0123/records.db", "/etc/caddy/mnemon-instances/beta-old.caddy",
			"/var/lib/mnemon-aml-watch/beta-old/state.json", "/etc/systemd/system/aml-serve@beta-old.service.d/limits.conf"} {
			writeFile(t, h.path(leftover), "left over", 0o600)
			if stderr := h.refused("", shared("beta-old")...); !strings.Contains(stderr, "exists already") {
				t.Errorf("new over a leftover %s: %s", leftover, stderr)
			}
			if err := os.RemoveAll(h.path(leftover)); err != nil {
				t.Fatal(err)
			}
		}

		// A new that fails half-way, here at the limits drop-in, removes what
		// it had written. (Root writes into a read-only directory.)
		if os.Geteuid() != 0 {
			units := h.path("/etc/systemd/system")
			if err := os.MkdirAll(units, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(units, 0o555); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(units, 0o755) })
			if stderr := h.refused("", shared("beta-y")...); !strings.Contains(stderr, "removed what it had written") {
				t.Errorf("a new that failed half-way said: %s", stderr)
			}
			if err := os.Chmod(units, 0o755); err != nil {
				t.Fatal(err)
			}
		}

		// A shared instance needs the host's Caddyfile to import the snippets.
		dedicated, err := os.ReadFile(filepath.Join(h.repo, "deploy", "aml", "Caddyfile.dedicated"))
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, h.path("/etc/caddy/Caddyfile"), string(dedicated), 0o644)
		h.refused("", shared("beta-x")...)

		// A relative --root, an unknown command and a missing name.
		for _, args := range [][]string{{"bogus"}, {"suspend"}, {"suspend", "nobody"}, {"rotate-key", "nobody"}, {"list", "extra"}} {
			h.refused("", args...)
		}
		// Every subcommand checks the name before it builds a path from it.
		for _, command := range []string{"suspend", "resume", "rotate-key", "delete", "delete-user"} {
			for _, name := range []string{"../beta-bxxx", "bxxx/..", "/etc", "b" + strings.Repeat("x", 30) + "/../.."} {
				if stderr := h.refused(name+"\n", command, name); !strings.Contains(stderr, "instance names are") {
					t.Errorf("%s %s: %s", command, name, stderr)
				}
			}
		}
		// A reserved name is refused by every subcommand: what an older layout
		// left at such a path (/etc/mnemon-aml/env, /var/lib/mnemon-aml/data)
		// is never taken for an instance.
		writeFile(t, h.path("/etc/mnemon-aml/env"), "AML_LAYOUT=dedicated\nAML_SLOT=0\n", 0o600)
		writeFile(t, h.path("/var/lib/mnemon-aml/data/users/0123/records.db"), "an older layout's data", 0o600)
		for _, name := range []string{"env", "data"} {
			for _, command := range []string{"suspend", "resume", "rotate-key", "delete", "delete-user"} {
				if stderr := h.refused(name+"\n", command, name); !strings.Contains(stderr, "reserved") {
					t.Errorf("%s %s was not refused as a reserved name: %s", command, name, stderr)
				}
			}
		}
		for _, leftover := range []string{"/etc/mnemon-aml/env", "/var/lib/mnemon-aml/data"} {
			if err := os.RemoveAll(h.path(leftover)); err != nil {
				t.Fatal(err)
			}
		}
		r := h.run("", "--root", "relative/root", "list")
		if r.ok {
			t.Error("a relative --root is accepted")
		}
		h.checkNoSecrets()
	})
}

func TestInstanceScriptLifecycle(t *testing.T) {
	h := newHost(t, "Caddyfile.shared")
	newShared := func(name string) {
		h.ok("", "new", name, "--layout", "shared", "--public-url", "https://beta.example.com/i/"+name, "--admin-ip", "10.77.0.3", "--host-name", "beta-1")
		h.instance(name)
	}
	newShared("beta-alice")
	newShared("beta-bob")
	const snippet = "/etc/caddy/mnemon-instances/beta-alice.caddy"
	active := h.read(snippet)
	envFile := "/etc/mnemon-aml/beta-alice/env"
	key, token := h.env(envFile)["AML_API_KEY"], h.env(envFile)["MNEMON_ADMIN_TOKEN"]
	watch := h.read("/etc/mnemon-aml/beta-alice/watch.env")

	listed := func() map[string][]string {
		t.Helper()
		rows := map[string][]string{}
		lines := strings.Split(strings.TrimSuffix(h.ok("", "list"), "\n"), "\n")
		if header := strings.Fields(lines[0]); !slices.Equal(header, []string{"NAME", "LAYOUT", "SLOT", "DATA", "ADMIN", "STATUS", "INSPECT", "KEYS", "PUBLIC", "URL"}) {
			t.Fatalf("list header %q", header)
		}
		for _, line := range lines[1:] {
			fields := strings.Fields(line)
			rows[fields[0]] = fields[1:]
		}
		return rows
	}
	if got, want := listed(), map[string][]string{
		"beta-alice": {"shared", "0", "127.0.0.1:8100", "10.77.0.3:8101", "active", "off", "host", "https://beta.example.com/i/beta-alice"},
		"beta-bob":   {"shared", "1", "127.0.0.1:8110", "10.77.0.3:8111", "active", "off", "host", "https://beta.example.com/i/beta-bob"},
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("list\n got %q\nwant %q", got, want)
	}

	// suspend: the route answers 403 in the engine's error shape; it can be
	// run again, which changes nothing.
	for range 2 {
		h.ok("", "suspend", "beta-alice")
		route := collapse(h.read(snippet))
		if want := "handle_path /i/beta-alice/* { header Content-Type application/json respond `{\"error\":\"account suspended\"}` 403 }"; route != want {
			t.Errorf("the suspended snippet is\n%s\nwant\n%s", route, want)
		}
		if got := h.env(envFile)["AML_STATUS"]; got != "suspended" {
			t.Errorf("AML_STATUS=%s after suspend", got)
		}
	}
	if got := listed()["beta-alice"]; got[4] != "suspended" {
		t.Errorf("list shows beta-alice as %q", got)
	}
	h.ok("", "resume", "beta-alice")
	if got := h.read(snippet); got != active {
		t.Errorf("resume left the snippet\n%s\nwant\n%s", got, active)
	}
	if got := h.env(envFile)["AML_STATUS"]; got != "active" {
		t.Errorf("AML_STATUS=%s after resume", got)
	}

	// rotate-key: a new key, printed once with its fingerprint; nothing
	// else changes.
	out := h.ok("", "rotate-key", "beta-alice")
	rotated := h.env(envFile)
	newKey := rotated["AML_API_KEY"]
	h.secrets["beta-alice rotated API key"] = newKey
	if !secretShape.MatchString(newKey) || newKey == key {
		t.Fatalf("rotate-key wrote the key %q", newKey)
	}
	if strings.Count(out, newKey) != 1 || strings.Contains(out, key) || !strings.Contains(out, fingerprint(newKey)) || strings.Contains(out, fingerprint(key)) {
		t.Errorf("rotate-key printed\n%s\nwant the new key once and its fingerprint %s", out, fingerprint(newKey))
	}
	if rotated["MNEMON_ADMIN_TOKEN"] != token || h.read("/etc/mnemon-aml/beta-alice/watch.env") != watch || h.mode(envFile) != 0o600 {
		t.Error("rotate-key changed more than the API key")
	}

	// delete-user: the user id comes on standard input, one line whose one
	// trailing newline is not part of it.
	users := map[string]string{"smoke-user": "smoke-user\n", "other-user": "other-user\n\nignored\n", " spaced id\t": " spaced id\t", "kept-user": ""}
	for user := range users {
		h.users = append(h.users, user)
		writeFile(t, h.path("/var/lib/mnemon-aml/beta-alice/data/users/"+storeKey(user)+"/records.db"), "records", 0o600)
	}
	h.refused("nobody\n", "delete-user", "beta-alice")
	h.refused("", "delete-user", "beta-alice")
	h.refused("smoke-user\n", "delete-user", "beta-bob")
	for user, stdin := range users {
		if stdin == "" {
			continue
		}
		out := h.ok(stdin, "delete-user", "beta-alice")
		if h.exists("/var/lib/mnemon-aml/beta-alice/data/users/" + storeKey(user)) {
			t.Errorf("delete-user left the store of %q", user)
		}
		if !strings.Contains(out, storeKey(user)[:8]) {
			t.Errorf("delete-user did not name the store %s: %s", storeKey(user)[:8], out)
		}
	}
	if !h.exists("/var/lib/mnemon-aml/beta-alice/data/users/" + storeKey("kept-user") + "/records.db") {
		t.Error("delete-user removed another user's store")
	}

	// delete: only with the name typed, and then everything.
	for _, typed := range []string{"", "beta-bob\n", "beta-alic\n"} {
		h.refused(typed, "delete", "beta-alice")
	}
	out = h.ok("beta-alice\n", "delete", "beta-alice")
	for _, removed := range []string{
		"/etc/mnemon-aml/beta-alice", "/var/lib/mnemon-aml/beta-alice", "/var/lib/mnemon-aml-watch/beta-alice",
		"/etc/systemd/system/aml-serve@beta-alice.service.d", snippet,
	} {
		if h.exists(removed) {
			t.Errorf("delete left %s", removed)
		}
		if !strings.Contains(out, "removed "+removed+"\n") {
			t.Errorf("delete did not say it removed %s:\n%s", removed, out)
		}
	}
	h.refused("beta-alice\n", "delete", "beta-alice")
	if _, ok := listed()["beta-alice"]; ok || !h.exists("/etc/caddy/mnemon-instances/beta-bob.caddy") {
		t.Error("delete removed the wrong instance")
	}
	// delete also removes what is left of an instance new did not finish.
	writeFile(t, h.path("/var/lib/mnemon-aml-watch/ghost/state.json"), "{}", 0o600)
	if out := h.ok("ghost\n", "delete", "ghost"); out != "removed /var/lib/mnemon-aml-watch/ghost\nMark ghost deleted in the console.\n" || h.exists("/var/lib/mnemon-aml-watch/ghost") {
		t.Errorf("delete of a leftover printed\n%s", out)
	}
	// The freed slot is the lowest free one again.
	newShared("beta-carol")
	if got := h.env("/etc/mnemon-aml/beta-carol/env"); got["AML_SLOT"] != "0" || got["AML_LISTEN"] != "127.0.0.1:8100" {
		t.Errorf("beta-carol took slot %s (%s), want the freed slot 0", got["AML_SLOT"], got["AML_LISTEN"])
	}

	// One journal line per action, naming the action and the instance.
	want := []string{"new beta-alice", "new beta-bob", "suspend beta-alice", "suspend beta-alice", "resume beta-alice",
		"rotate-key beta-alice", "delete-user beta-alice", "delete-user beta-alice", "delete-user beta-alice",
		"delete beta-alice", "delete ghost", "new beta-carol"}
	got := h.journal()
	if len(got) != len(want) {
		t.Fatalf("journal\n%s\nwant %d lines", strings.Join(got, "\n"), len(want))
	}
	for i, line := range got {
		if line != want[i] && !strings.HasPrefix(line, want[i]+":") {
			t.Errorf("journal line %d is %q, want %q", i, line, want[i])
		}
	}
	h.checkNoSecrets()
}

// listColumn is the column named header of every row of list, by instance.
func (h *host) listColumn(header string) map[string]string {
	h.t.Helper()
	lines := strings.Split(strings.TrimSuffix(h.ok("", "list"), "\n"), "\n")
	column := slices.Index(strings.Fields(lines[0]), header)
	if column < 0 {
		h.t.Fatalf("list has no %s column: %q", header, lines[0])
	}
	values := map[string]string{}
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		values[fields[0]] = fields[column]
	}
	return values
}

// list's KEYS column names the kinds of an instance's own provider keys
// (ruling R98), read from the key file's names, never a key; delete removes
// the file with the instance's state.
func TestInstanceScriptListShowsKeyKindsOnly(t *testing.T) {
	h := newHost(t, "Caddyfile.shared")
	for _, name := range []string{"beta-alice", "beta-bob", "beta-carol", "beta-dave"} {
		h.ok("", "new", name, "--layout", "shared", "--public-url", "https://beta.example.com/i/"+name, "--admin-ip", "10.77.0.3", "--host-name", "beta-1")
	}
	deepseek, jev := "sk-account-"+randomHex(t), "ts-account-"+randomHex(t)
	h.secrets["planted DeepSeek account key"], h.secrets["planted JEV account key"] = deepseek, jev
	keyFile := func(name, content string) {
		writeFile(t, h.path("/var/lib/mnemon-aml/"+name+"/provider-keys.json"), content, 0o600)
	}
	keyFile("beta-alice", `{"version":1,"keys":{"jev":"`+jev+`","deepseek":"`+deepseek+`"},"updatedAt":"2026-10-25T08:00:00Z"}`)
	keyFile("beta-bob", `{"version":1,"keys":{"jev":"`+jev+`"},"updatedAt":"2026-10-25T08:00:00Z"}`)
	keyFile("beta-carol", `{"version":1,"keys":{"deepseek":"`+deepseek)
	want := map[string]string{"beta-alice": "account:deepseek,jev", "beta-bob": "account:jev", "beta-carol": "invalid", "beta-dave": "host"}
	if got := h.listColumn("KEYS"); !reflect.DeepEqual(got, want) {
		t.Errorf("KEYS %v, want %v", got, want)
	}
	out := h.ok("beta-alice\n", "delete", "beta-alice")
	if h.exists("/var/lib/mnemon-aml/beta-alice/provider-keys.json") || !strings.Contains(out, "removed /var/lib/mnemon-aml/beta-alice\n") {
		t.Errorf("delete left the key file:\n%s", out)
	}
	h.checkNoSecrets()
}

// A dedicated instance has no snippet to swap: suspend and resume stop and
// start its units only.
func TestInstanceScriptDedicatedLifecycle(t *testing.T) {
	h := newHost(t, "Caddyfile.dedicated")
	h.ok("", "new", "eval", "--layout", "dedicated", "--public-url", "https://eval.example.com", "--admin-ip", "10.77.0.2", "--host-name", "eval-1")
	h.instance("eval")
	h.checkConfig("eval", 600, 60)
	h.ok("", "suspend", "eval")
	if got := h.env("/etc/mnemon-aml/eval/env")["AML_STATUS"]; got != "suspended" || h.exists("/etc/caddy/mnemon-instances") {
		t.Errorf("suspend: AML_STATUS=%s, or a snippet was written", got)
	}
	h.ok("", "rotate-key", "eval")
	h.secrets["eval rotated API key"] = h.env("/etc/mnemon-aml/eval/env")["AML_API_KEY"]
	h.ok("", "resume", "eval")
	if got := h.env("/etc/mnemon-aml/eval/env")["AML_STATUS"]; got != "active" {
		t.Errorf("resume: AML_STATUS=%s", got)
	}
	writeFile(t, h.path("/var/lib/mnemon-aml/eval/data/users/"+storeKey("smoke-user")+"/records.db"), "records", 0o600)
	h.users = append(h.users, "smoke-user")
	h.ok("smoke-user", "delete-user", "eval")
	out := h.ok("eval\n", "delete", "eval")
	if h.exists("/etc/mnemon-aml/eval") || strings.Contains(out, "mnemon-instances") {
		t.Errorf("delete of a dedicated instance:\n%s", out)
	}
	h.checkNoSecrets()
}

// Shared slots run from 0 to 49 (admin ports 8101 to 8591); new takes the
// lowest free one, and refuses when none is free.
func TestInstanceScriptSlots(t *testing.T) {
	h := newHost(t, "Caddyfile.shared")
	newShared := func(name string) {
		h.ok("", "new", name, "--layout", "shared", "--public-url", "https://beta.example.com/i/"+name, "--admin-ip", "10.77.0.3", "--host-name", "beta-1")
	}
	// The records of 49 instances in slots 0 to 48: the env file is all the
	// allocator reads.
	for slot := range 49 {
		name := fmt.Sprintf("beta-%02d", slot)
		writeFile(t, h.path("/etc/mnemon-aml/"+name+"/env"), fmt.Sprintf("AML_LAYOUT=shared\nAML_SLOT=%d\n", slot), 0o600)
	}
	newShared("beta-last")
	if env := h.env("/etc/mnemon-aml/beta-last/env"); env["AML_SLOT"] != "49" || env["AML_LISTEN"] != "127.0.0.1:8590" || env["AML_ADMIN_LISTEN"] != "10.77.0.3:8591" {
		t.Errorf("the fiftieth instance has slot %s, data %s and admin %s; want 49, 8590 and 8591", env["AML_SLOT"], env["AML_LISTEN"], env["AML_ADMIN_LISTEN"])
	}
	if stderr := h.refused("", "new", "beta-full", "--layout", "shared", "--public-url", "https://beta.example.com/i/beta-full", "--admin-ip", "10.77.0.3", "--host-name", "beta-1"); !strings.Contains(stderr, "50") {
		t.Errorf("the refusal on a full host does not say so: %s", stderr)
	}
	// A freed slot is taken again.
	if err := os.RemoveAll(h.path("/etc/mnemon-aml/beta-07")); err != nil {
		t.Fatal(err)
	}
	newShared("beta-again")
	if env := h.env("/etc/mnemon-aml/beta-again/env"); env["AML_SLOT"] != "7" || env["AML_ADMIN_LISTEN"] != "10.77.0.3:8171" {
		t.Errorf("the freed slot 7 was not taken: slot %s, admin %s", env["AML_SLOT"], env["AML_ADMIN_LISTEN"])
	}
}

// No secret is ever an argument of a program the script runs (R40): with
// every tool it calls replaced by a spy that records its arguments, a whole
// lifecycle leaves no secret in the record. Builtins, printf among them, run
// no program. So with either kind of alert bot: a Telegram webhook, whose URL
// carries the bot's token, is checked in the shell only.
func TestInstanceScriptKeepsSecretsOutOfArguments(t *testing.T) {
	for _, kind := range []string{"feishu", "telegram"} {
		t.Run(kind, func(t *testing.T) { keepsSecretsOutOfArguments(t, kind) })
	}
}

func keepsSecretsOutOfArguments(t *testing.T, kind string) {
	h := newHost(t, "Caddyfile.shared")
	if kind == "telegram" {
		h.secrets["AML_ALERT_WEBHOOK"] = "https://api.telegram.org/bot7012345678:AA" + randomHex(t) + "/sendMessage?chat_id=-1001234567890"
		h.writeHostEnv(map[string]string{"AML_ALERT_KIND": "telegram"})
	}
	record := filepath.Join(h.bin, "arguments")
	spied := 0
	for _, tool := range []string{"grep", "tail", "cut", "head", "tr", "base64", "mktemp", "mkdir", "chmod", "chown", "mv", "rm",
		"rmdir", "date", "uname", "id", "jq", "stat", "cat", "sha256sum", "shasum"} {
		real, err := exec.LookPath(tool)
		if err != nil {
			continue
		}
		writeFile(t, filepath.Join(h.bin, tool), "#!/bin/sh\nprintf '%s\\n' \""+tool+" $*\" >> '"+record+"'\nexec '"+real+"' \"$@\"\n", 0o755)
		spied++
	}
	if spied < 15 {
		t.Fatalf("found only %d of the tools to spy on", spied)
	}
	h.ok("", "new", "beta-alice", "--layout", "shared", "--public-url", "https://beta.example.com/i/beta-alice", "--admin-ip", "10.77.0.3", "--host-name", "beta-1")
	h.instance("beta-alice")
	h.ok("", "suspend", "beta-alice")
	h.ok("", "resume", "beta-alice")
	h.ok("", "rotate-key", "beta-alice")
	h.secrets["beta-alice rotated API key"] = h.env("/etc/mnemon-aml/beta-alice/env")["AML_API_KEY"]
	h.users = append(h.users, "smoke-user")
	writeFile(t, h.path("/var/lib/mnemon-aml/beta-alice/data/users/"+storeKey("smoke-user")+"/records.db"), "records", 0o600)
	h.ok("smoke-user\n", "delete-user", "beta-alice")
	h.ok("", "list")
	h.ok("beta-alice\n", "delete", "beta-alice")
	arguments, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"jq ", "grep ", "mktemp ", "mv "} {
		if !strings.Contains(string(arguments), tool) {
			t.Fatalf("the spies did not see %q run: the script must run it", tool)
		}
	}
	for label, secret := range h.secrets {
		if strings.Contains(string(arguments), secret) {
			t.Errorf("a program's arguments held the %s", label)
		}
	}
	for _, user := range h.users {
		if strings.Contains(string(arguments), user) {
			t.Errorf("a program's arguments held the user id %q", user)
		}
	}
	h.checkNoSecrets()
}

// GOMEMLIMIT, in the engine env, is 80% of the instance's MemoryMax= rounded
// down to a whole MiB (systemd reads K, M, G and T in powers of 1024), from
// the layout's default or from --memory-max.
func TestInstanceScriptGoMemLimit(t *testing.T) {
	h := newHost(t, "Caddyfile.shared")
	for i, tc := range []struct{ memoryMax, want string }{
		{"", "819MiB"}, // the shared default, 1G
		{"40G", "32768MiB"},
		{"1536M", "1228MiB"},
		{"2097152K", "1638MiB"},
		{"1T", "838860MiB"},
		{"2M", "1MiB"},
		{"1280K", "1MiB"},
	} {
		name := fmt.Sprintf("beta-%d", i)
		args := []string{"new", name, "--layout", "shared", "--public-url", "https://beta.example.com/i/" + name, "--admin-ip", "10.77.0.3", "--host-name", "beta-1"}
		if tc.memoryMax != "" {
			args = append(args, "--memory-max", tc.memoryMax)
		}
		h.ok("", args...)
		env, _ := h.instance(name)
		if env["GOMEMLIMIT"] != tc.want {
			t.Errorf("--memory-max %q: GOMEMLIMIT=%s, want %s", tc.memoryMax, env["GOMEMLIMIT"], tc.want)
		}
		limits := "[Service]\nMemoryMax=" + tc.memoryMax + "\nCPUQuota=100%"
		if tc.memoryMax == "" {
			limits = "[Service]\nMemoryMax=1G\nCPUQuota=100%"
		}
		h.checkLimits(name, limits)
	}
	h.checkNoSecrets()
}

// delete-user removes a store only by a key of 32 hex digits: a SHA-256 tool
// that prints nothing, or anything else, never makes it remove the users
// directory, or any directory but the user's store.
func TestInstanceScriptDeleteUserChecksTheKey(t *testing.T) {
	h := newHost(t, "Caddyfile.shared")
	h.ok("", "new", "beta-alice", "--layout", "shared", "--public-url", "https://beta.example.com/i/beta-alice", "--admin-ip", "10.77.0.3", "--host-name", "beta-1")
	h.instance("beta-alice")
	h.users = append(h.users, "smoke-user")
	for _, user := range []string{"smoke-user", "kept-user"} {
		writeFile(t, h.path("/var/lib/mnemon-aml/beta-alice/data/users/"+storeKey(user)+"/records.db"), "records", 0o600)
	}
	key := storeKey("smoke-user")
	digest := filepath.Join(t.TempDir(), "digest")
	for _, tool := range []string{"sha256sum", "shasum"} {
		writeFile(t, filepath.Join(h.bin, tool), "#!/bin/sh\ncat >/dev/null\ncat '"+digest+"'\n", 0o755)
	}
	for name, output := range map[string]string{
		"nothing":       "",
		"an empty line": "\n",
		"spaces":        "    \n",
		"upper case":    strings.ToUpper(key) + "  -\n",
		"too short":     key[:31] + "  -\n",
		"not hex":       "zz" + key[2:] + "  -\n",
	} {
		writeFile(t, digest, output, 0o644)
		if stderr := h.refused("smoke-user\n", "delete-user", "beta-alice"); !strings.Contains(stderr, "nothing was deleted") {
			t.Errorf("a SHA-256 tool that prints %s: %s", name, stderr)
		}
	}
	// With the real tools it removes the store, and only it.
	for _, tool := range []string{"sha256sum", "shasum"} {
		if err := os.Remove(filepath.Join(h.bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	h.ok("smoke-user\n", "delete-user", "beta-alice")
	if h.exists("/var/lib/mnemon-aml/beta-alice/data/users/"+key) || !h.exists("/var/lib/mnemon-aml/beta-alice/data/users/"+storeKey("kept-user")+"/records.db") {
		t.Error("delete-user removed the wrong store")
	}
	h.checkNoSecrets()
}

// A journal that cannot be written never costs the one-time output: new and
// rotate-key say so, and print the keys all the same.
func TestInstanceScriptJournalFailureKeepsTheKeys(t *testing.T) {
	h := newHost(t, "Caddyfile.shared")
	// The journal's file is a directory, so no line can be appended to it.
	journal := h.path("/var/log/mnemon-instance.log")
	if err := os.MkdirAll(journal, 0o755); err != nil {
		t.Fatal(err)
	}
	r := h.run("", "new", "beta-alice", "--layout", "shared", "--public-url", "https://beta.example.com/i/beta-alice", "--admin-ip", "10.77.0.3", "--host-name", "beta-1")
	if !r.ok || !strings.Contains(r.stderr, "could not write the journal line") {
		t.Fatalf("new with a journal that fails:\n%s%s", r.stdout, r.stderr)
	}
	env, _ := h.instance("beta-alice")
	checkOutput(t, r.stdout, "https://beta.example.com/i/beta-alice", "http://10.77.0.3:8101", env["AML_API_KEY"],
		registration("beta-alice", "shared", "beta-1", "10.77.0.3", "https://beta.example.com/i/beta-alice", "http://10.77.0.3:8101", env["MNEMON_ADMIN_TOKEN"], env["AML_API_KEY"]))
	r = h.run("", "rotate-key", "beta-alice")
	rotated := h.env("/etc/mnemon-aml/beta-alice/env")["AML_API_KEY"]
	h.secrets["beta-alice rotated API key"] = rotated
	if !r.ok || !strings.Contains(r.stderr, "could not write the journal line") || strings.Count(r.stdout, rotated) != 1 {
		t.Fatalf("rotate-key with a journal that fails:\n%s%s", r.stdout, r.stderr)
	}
	if err := os.Remove(journal); err != nil {
		t.Fatal(err)
	}
	h.checkNoSecrets()
}

// strictTr is a tr that parses its options as strictly as a modern parser
// may (the Rust coreutils' on Ubuntu 26.04, say): an operand that starts with
// "-" is an unknown option unless "--" came before it.
func strictTr(real string) string {
	return `#!/bin/sh
for arg in "$@"; do
	case $arg in
	--) break ;;
	-c | -C | -d | -s | -cd | -ds | -cs) ;;
	-*)
		printf 'tr: unexpected argument %s\n' "$arg" >&2
		exit 1
		;;
	esac
done
exec '` + real + `' "$@"
`
}

// The script's tr operands that start with "-" (the base64url alphabet's)
// come after "--", so that a strict option parser takes them as operands.
func TestInstanceScriptTrOperandsComeAfterDoubleDash(t *testing.T) {
	h := newHost(t, "Caddyfile.shared")
	real, err := exec.LookPath("tr")
	if err != nil {
		t.Skip("tr is missing")
	}
	writeFile(t, filepath.Join(h.bin, "tr"), strictTr(real), 0o755)
	h.ok("", "new", "beta-alice", "--layout", "shared", "--public-url", "https://beta.example.com/i/beta-alice", "--admin-ip", "10.77.0.3", "--host-name", "beta-1")
	h.instance("beta-alice")
	h.ok("", "rotate-key", "beta-alice")
	if key := h.env("/etc/mnemon-aml/beta-alice/env")["AML_API_KEY"]; !secretShape.MatchString(key) {
		t.Errorf("rotate-key wrote the key %q", key)
	} else {
		h.secrets["beta-alice rotated API key"] = key
	}
	h.checkNoSecrets()
}

// Telegram is an alert kind: new copies the bot's sendMessage URL, chat
// included, and the kind into watch.env. A telegram URL that is not a bot's
// sendMessage with its chat (?chat_id=, its only parameter) is refused, the
// refusal naming the variable, never the value.
func TestInstanceScriptTelegramAlerts(t *testing.T) {
	h := newHost(t, "Caddyfile.shared")
	token := "7012345678:AA" + randomHex(t)
	h.secrets["telegram bot token"] = token
	bot := "https://api.telegram.org/bot" + token
	shared := func(name string) []string {
		return []string{"new", name, "--layout", "shared", "--public-url", "https://beta.example.com/i/" + name, "--admin-ip", "10.77.0.3", "--host-name", "beta-1"}
	}
	for i, webhook := range []string{
		bot + "/sendMessage?chat_id=123456789",
		bot + "/sendMessage?chat_id=-1001234567890",
		bot + "/sendMessage?chat_id=@mnemon_alerts",
		bot + "/sendMessage?chat_id=%40mnemon_alerts",
		"https://bots.example.com/bot" + token + "/sendMessage?chat_id=42",
	} {
		h.secrets["AML_ALERT_WEBHOOK"] = webhook
		h.writeHostEnv(map[string]string{"AML_ALERT_KIND": "telegram"})
		name := fmt.Sprintf("beta-%d", i)
		h.ok("", shared(name)...)
		if _, watch := h.instance(name); watch["AML_ALERT_KIND"] != "telegram" || watch["AML_ALERT_WEBHOOK"] != webhook {
			t.Errorf("%s: watch.env carries %s and another webhook", name, watch["AML_ALERT_KIND"])
		}
	}
	for _, webhook := range []string{
		bot + "/sendMessage",
		bot + "/sendMessage?",
		bot + "/sendMessage?chat_id=",
		bot + "/sendMessage?chat_id=abc",
		bot + "/sendMessage?chat_id=@abcd",
		bot + "/sendMessage?chat_id=42&parse_mode=HTML",
		bot + "/sendMessage?chat_id=42&chat_id=43",
		bot + "/getUpdates?chat_id=42",
		bot + "/sendMessage/?chat_id=42",
		bot + "?chat_id=42",
		"http://api.telegram.org/bot" + token + "/sendMessage?chat_id=42",
		// What the watcher's URL parser refuses, the shell refuses too, and a
		// fragment, which the watcher drops, it refuses as well
		// (TestTelegramRuleNeverLooserThanTheWatchers has the whole table).
		"https://api.telegram.org/bot" + token + "%zz/sendMessage?chat_id=42",
		"https://api telegram.org/bot" + token + "/sendMessage?chat_id=42",
		"https://api.telegram.org/bot" + token + "/sendMessage?chat_id=42#x",
	} {
		h.writeHostEnv(map[string]string{"AML_ALERT_KIND": "telegram", "AML_ALERT_WEBHOOK": webhook})
		if stderr := h.refused("", shared("beta-x")...); !strings.Contains(stderr, "AML_ALERT_WEBHOOK") {
			t.Errorf("the refusal of a telegram webhook does not name AML_ALERT_WEBHOOK: %s", stderr)
		}
	}
	// A kind the watcher does not know is refused, telegram's spelling too.
	for _, kind := range []string{"Telegram", "telegram-bot"} {
		h.writeHostEnv(map[string]string{"AML_ALERT_KIND": kind, "AML_ALERT_WEBHOOK": bot + "/sendMessage?chat_id=42"})
		if stderr := h.refused("", shared("beta-x")...); !strings.Contains(stderr, "AML_ALERT_KIND") {
			t.Errorf("kind %s: %s", kind, stderr)
		}
	}
	h.checkNoSecrets()
}

// ---- the systemd side, sourced ----

// The tests above run the script with --no-systemd, which skips what it does
// with systemd, Caddy and the admin listener. These source the script instead
// (main runs only when it is run), set ROOT to the host's tree and SYSTEMD=1,
// and call its functions with the programs of that side stubbed as shell
// functions: systemctl, logger and caddy record their arguments in calls,
// curl answers /ready, and sleep moves bash's clock (SECONDS) on instead of
// waiting. The tripwires stay on PATH: a program a stub misses fails the test.

// systemdStubs are the stubs: CADDY_STATUS is caddy validate's exit status,
// READY_AFTER the try at which /ready answers 200 (none when 0), and
// SLEEP_STEP the seconds one try and its sleep take. Each systemctl call
// records the instance's AML_STATUS at the time.
const systemdStubs = `
systemctl() { printf 'systemctl %s (status %s)\n' "$*" "$(env_get "$ROOT$ETC/$INSTANCE/env" AML_STATUS)" >>"$CALLS"; }
logger() { printf 'logger %s\n' "$*" >>"$CALLS"; }
caddy() { printf 'caddy %s\n' "$*" >>"$CALLS"; return "${CADDY_STATUS:-0}"; }
curl() {
	cat >/dev/null
	printf 'curl %s\n' "$*" >>"$CALLS"
	local tries
	tries=$(grep -c '^curl ' "$CALLS")
	if [ "${READY_AFTER:-1}" -gt 0 ] && [ "$tries" -ge "${READY_AFTER:-1}" ]; then printf 200; else printf 000; fi
}
sleep() { SECONDS=$((SECONDS + ${SLEEP_STEP:-1})); }
`

// sourced runs code in bash after sourcing instance.sh with the stubs, for
// instance; it returns the run and the calls the stubs recorded.
func (h *host) sourced(instance, code string) (run, []string) {
	h.t.Helper()
	calls := filepath.Join(h.t.TempDir(), "calls")
	writeFile(h.t, calls, "", 0o600)
	script := "source '" + filepath.Join(h.repo, "deploy", "aml", "instance.sh") + "'\n" +
		"ROOT='" + h.root + "' SYSTEMD=1 CALLS='" + calls + "' INSTANCE='" + instance + "'\n" + systemdStubs + code
	command := exec.Command("bash", "-c", script)
	command.Stdin = strings.NewReader("")
	command.Env = []string{"PATH=" + h.bin + string(os.PathListSeparator) + os.Getenv("PATH"), "HOME=" + h.root, "LC_ALL=C"}
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if _, ok := err.(*exec.ExitError); err != nil && !ok {
		h.t.Fatal(err)
	}
	if trips, _ := os.ReadFile(h.tripwire); len(trips) > 0 {
		h.t.Fatalf("a stub was missed; the sourced script ran:\n%s", trips)
	}
	raw, readErr := os.ReadFile(calls)
	if readErr != nil {
		h.t.Fatal(readErr)
	}
	// No secret is an argument of the programs the stubs stand for.
	for label, secret := range h.secrets {
		if strings.Contains(string(raw), secret) {
			h.t.Errorf("a program's arguments held the %s", label)
		}
	}
	r := run{args: []string{"sourced", instance}, stdout: stdout.String(), stderr: stderr.String(), ok: err == nil}
	h.runs = append(h.runs, r)
	return r, strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
}

// systemctlCalls are the systemctl calls of calls, without the status.
func systemctlCalls(calls []string) []string {
	var out []string
	for _, call := range calls {
		if rest, ok := strings.CutPrefix(call, "systemctl "); ok {
			out = append(out, rest[:strings.LastIndex(rest, " (status ")])
		}
	}
	return out
}

// newSharedAlice is a shared host with the instance beta-alice, made with
// --no-systemd; it returns the host and beta-alice's active snippet.
func newSharedAlice(t *testing.T) (*host, string) {
	t.Helper()
	h := newHost(t, "Caddyfile.shared")
	h.ok("", "new", "beta-alice", "--layout", "shared", "--public-url", "https://beta.example.com/i/beta-alice", "--admin-ip", "10.77.0.3", "--host-name", "beta-1")
	h.instance("beta-alice")
	return h, h.read("/etc/caddy/mnemon-instances/beta-alice.caddy")
}

// The wait for /ready has a deadline of READY_WAIT (120) seconds, however long
// each try takes: an admin address that does not answer makes curl wait its
// 5 s each time, which a count of 120 tries made 12 minutes.
func TestInstanceScriptWaitReadyHasASecondsDeadline(t *testing.T) {
	h, _ := newSharedAlice(t)
	r, calls := h.sourced("beta-alice", `
SLEEP_STEP=6 READY_AFTER=0
start=$SECONDS
if wait_ready beta-alice; then echo ready; else echo "not ready after $((SECONDS - start)) s"; fi
`)
	tries := 0
	for _, call := range calls {
		if strings.HasPrefix(call, "curl ") {
			tries++
		}
	}
	var waited int
	if _, err := fmt.Sscanf(strings.TrimSpace(r.stdout), "not ready after %d s", &waited); err != nil || waited < 120 || waited > 126 || tries > 22 {
		t.Errorf("wait_ready gave up after %q, %d tries; want within 120 s plus one try", r.stdout, tries)
	}
	if !strings.Contains(r.stderr, "beta-alice: /ready did not answer 200 within 120s") {
		t.Errorf("wait_ready said: %s", r.stderr)
	}
	// An instance that becomes ready ends the wait at once.
	r, calls = h.sourced("beta-alice", `READY_AFTER=3; wait_ready beta-alice && echo ready`)
	if strings.TrimSpace(r.stdout) != "ready" || len(calls) != 3 {
		t.Errorf("%q %q", r.stdout, calls)
	}
}

// A route Caddy refuses is put back as it was: the old snippet, or none when
// there was none, so that the host's Caddyfile stays valid. Here through
// suspend, which then stops nothing.
func TestInstanceScriptRefusedRouteLeavesNoSnippet(t *testing.T) {
	h, active := newSharedAlice(t)
	const snippet = "/etc/caddy/mnemon-instances/beta-alice.caddy"
	r, calls := h.sourced("beta-alice", `CADDY_STATUS=1; cmd_suspend beta-alice`)
	if r.ok || !strings.Contains(r.stderr, "Caddy did not take the suspended route of beta-alice") || h.read(snippet) != active {
		t.Fatalf("a refused route with a snippet before: %s\n%s", r.stderr, h.read(snippet))
	}
	if got := systemctlCalls(calls); len(got) != 0 {
		t.Errorf("suspend went on after Caddy refused its route: %q", got)
	}
	if got := h.env("/etc/mnemon-aml/beta-alice/env")["AML_STATUS"]; got != "active" {
		t.Errorf("AML_STATUS=%s", got)
	}

	// With no snippet before, the refused one is removed.
	if err := os.Remove(h.path(snippet)); err != nil {
		t.Fatal(err)
	}
	r, calls = h.sourced("beta-alice", `CADDY_STATUS=1; cmd_suspend beta-alice`)
	if r.ok || !strings.Contains(r.stderr, "Caddy did not take the suspended route of beta-alice") || h.exists(snippet) {
		t.Fatalf("a refused route without a snippet before: %s (snippet left: %v)", r.stderr, h.exists(snippet))
	}
	if got := systemctlCalls(calls); len(got) != 0 {
		t.Errorf("suspend went on after Caddy refused its route: %q", got)
	}
	h.checkNoSecrets()
}

// resume starts the instance and waits for /ready first; only then does it
// restore the route, record AML_STATUS=active and enable the watcher's timer.
// A resume that fails on the way stops and disables aml-serve again, and the
// instance stays fully suspended.
func TestInstanceScriptResume(t *testing.T) {
	h, active := newSharedAlice(t)
	const snippet = "/etc/caddy/mnemon-instances/beta-alice.caddy"
	h.ok("", "suspend", "beta-alice")
	suspended := h.read(snippet)
	stillSuspended := func(when string) {
		t.Helper()
		if h.read(snippet) != suspended || h.env("/etc/mnemon-aml/beta-alice/env")["AML_STATUS"] != "suspended" {
			t.Errorf("%s: the route or the status is not suspended any more:\n%s", when, h.read(snippet))
		}
	}
	stopped := []string{"enable --now aml-serve@beta-alice.service", "disable --now aml-serve@beta-alice.service"}

	// Not ready within 120 s: the unit is stopped again.
	r, calls := h.sourced("beta-alice", `READY_AFTER=0 SLEEP_STEP=6; cmd_resume beta-alice`)
	if r.ok || !strings.Contains(r.stderr, "stays suspended") {
		t.Errorf("a resume that never got ready: %s", r.stderr)
	}
	if got := systemctlCalls(calls); !slices.Equal(got, stopped) {
		t.Errorf("systemctl %q, want %q", got, stopped)
	}
	for _, call := range calls {
		if strings.HasPrefix(call, "caddy ") {
			t.Error("resume touched the route of an instance that is not ready:", call)
		}
	}
	stillSuspended("not ready")

	// Ready, but Caddy refuses the route: the unit is stopped again, and the
	// suspended route stays.
	r, calls = h.sourced("beta-alice", `CADDY_STATUS=1; cmd_resume beta-alice`)
	if r.ok || !strings.Contains(r.stderr, "Caddy did not take the active route of beta-alice") || !strings.Contains(r.stderr, "stays suspended") {
		t.Errorf("a resume whose route was refused: %s", r.stderr)
	}
	if got := systemctlCalls(calls); !slices.Equal(got, stopped) {
		t.Errorf("systemctl %q, want %q", got, stopped)
	}
	stillSuspended("route refused")

	// Ready and routed: the status is active before the timer starts.
	r, calls = h.sourced("beta-alice", `cmd_resume beta-alice`)
	if !r.ok || strings.TrimSpace(r.stdout) != "beta-alice is running again." {
		t.Fatalf("resume: %s%s", r.stdout, r.stderr)
	}
	want := []string{
		"systemctl enable --now aml-serve@beta-alice.service (status suspended)",
		"curl --disable --config - --silent --output /dev/null --max-time 5 --write-out %{http_code} http://10.77.0.3:8101/ready",
		"caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile",
		"systemctl reload caddy (status suspended)",
		"systemctl enable --now aml-watch@beta-alice.timer (status active)",
		"logger -t mnemon-instance -- resume beta-alice",
	}
	if !slices.Equal(calls, want) {
		t.Errorf("resume ran\n%s\nwant\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
	}
	if h.read(snippet) != active || h.env("/etc/mnemon-aml/beta-alice/env")["AML_STATUS"] != "active" {
		t.Error("resume did not restore the route and the status")
	}

	// An instance that is active is never stopped by a resume that fails: a
	// /ready that answers 503 (a breaker open, say) is not a reason to take a
	// serving instance down.
	r, calls = h.sourced("beta-alice", `READY_AFTER=0 SLEEP_STEP=6; cmd_resume beta-alice`)
	if r.ok || strings.Contains(r.stderr, "stays suspended") {
		t.Errorf("a resume of an active instance that is not ready: %s", r.stderr)
	}
	if got := systemctlCalls(calls); !slices.Equal(got, stopped[:1]) {
		t.Errorf("systemctl %q, want %q", got, stopped[:1])
	}
	if h.read(snippet) != active || h.env("/etc/mnemon-aml/beta-alice/env")["AML_STATUS"] != "active" {
		t.Error("a failed resume of an active instance changed its route or status")
	}
	h.checkNoSecrets()
}

// Once resume has restored the route the instance is back, and the two steps
// that remain (recording AML_STATUS=active, enabling the watcher's timer) must
// not leave it half done without a word: a failure there says so, and what to
// run to finish it. The instance is not stopped again: it serves (the final
// review's parked T15-d).
func TestInstanceScriptResumeFailsLoudlyAfterTheRoute(t *testing.T) {
	h, active := newSharedAlice(t)
	const snippet = "/etc/caddy/mnemon-instances/beta-alice.caddy"
	statusOf := func() string { return h.env("/etc/mnemon-aml/beta-alice/env")["AML_STATUS"] }
	for _, c := range []struct {
		name, code, status string
	}{
		{"the timer does not start", `systemctl() { printf 'systemctl %s (status %s)\n' "$*" "$(env_get "$ROOT$ETC/$INSTANCE/env" AML_STATUS)" >>"$CALLS"; case "$*" in "enable --now aml-watch@"*) return 1 ;; esac; }
cmd_resume beta-alice`, "active"},
		{"the status cannot be recorded", `env_set() { return 1; }
cmd_resume beta-alice`, "suspended"},
	} {
		h.ok("", "suspend", "beta-alice")
		r, calls := h.sourced("beta-alice", c.code)
		if r.ok {
			t.Errorf("%s: resume succeeded", c.name)
		}
		for _, want := range []string{"resume beta-alice stopped after the instance was back", "aml-serve@beta-alice runs", "run mnemon-instance resume beta-alice again"} {
			if !strings.Contains(r.stderr, want) {
				t.Errorf("%s: resume did not say %q:\n%s", c.name, want, r.stderr)
			}
		}
		if strings.Contains(r.stderr, "stays suspended") {
			t.Errorf("%s: resume says the instance is suspended, but its route is back:\n%s", c.name, r.stderr)
		}
		for _, call := range systemctlCalls(calls) {
			if strings.HasPrefix(call, "disable --now aml-serve@") {
				t.Errorf("%s: resume stopped an instance whose route it had restored: %q", c.name, systemctlCalls(calls))
			}
		}
		if h.read(snippet) != active || statusOf() != c.status {
			t.Errorf("%s: the route is not active (%v) or AML_STATUS is %q, want %q", c.name, h.read(snippet) == active, statusOf(), c.status)
		}
		// Run again, it finishes.
		r, _ = h.sourced("beta-alice", `cmd_resume beta-alice`)
		if !r.ok || statusOf() != "active" {
			t.Errorf("%s: resume again: %s%s (AML_STATUS %q)", c.name, r.stdout, r.stderr, statusOf())
		}
	}
	h.checkNoSecrets()
}

// new on a shared host whose Caddy refuses the new instance's route removes
// the snippet it wrote (there was none before), so that the host's Caddyfile
// stays valid; it prints the keys all the same, and says what went wrong.
func TestInstanceScriptNewWithARefusedRoute(t *testing.T) {
	h := newHost(t, "Caddyfile.shared")
	// What the one-time host install puts in place, which new checks for.
	writeFile(t, h.path("/usr/local/bin/mnemon"), "#!/bin/sh\nexit 97\n", 0o755)
	for _, unit := range []string{"aml-serve@.service", "aml-watch@.service", "aml-watch@.timer"} {
		writeFile(t, h.path("/etc/systemd/system/"+unit), "[Unit]\n", 0o644)
	}
	r, calls := h.sourced("beta-alice", `CADDY_STATUS=1
id() { if [ "$*" = mnemon ]; then return 0; fi; command id "$@"; }
cmd_new beta-alice --layout shared --public-url https://beta.example.com/i/beta-alice --admin-ip 10.77.0.3 --host-name beta-1
`)
	if r.ok || !strings.Contains(r.stderr, "Caddy did not take the route of beta-alice; its snippet is removed") || h.exists("/etc/caddy/mnemon-instances/beta-alice.caddy") {
		t.Fatalf("new with a refused route:\n%s%s", r.stdout, r.stderr)
	}
	// It says how to put the route back once the Caddyfile is fixed: resume
	// writes the snippet of an instance that is running (the final review's
	// parked T15-c).
	if !strings.Contains(r.stderr, "fix the Caddyfile, then run mnemon-instance resume beta-alice") {
		t.Errorf("new did not say how to put the route back:\n%s", r.stderr)
	}
	want := []string{"daemon-reload", "enable --now aml-serve@beta-alice.service", "enable --now aml-watch@beta-alice.timer"}
	if got := systemctlCalls(calls); !slices.Equal(got, want) {
		t.Errorf("systemctl %q, want %q", got, want)
	}
	// The instance stays, and its keys are printed once, on standard output
	// only; nothing else secret is printed.
	env, _ := h.instance("beta-alice")
	for _, secret := range []string{env["AML_API_KEY"], env["MNEMON_ADMIN_TOKEN"]} {
		if !strings.Contains(r.stdout, secret) || strings.Contains(r.stderr, secret) {
			t.Errorf("the one-time output is not on standard output alone:\n%s%s", r.stdout, r.stderr)
		}
	}
	for _, label := range []string{"DEEPSEEK_API_KEY", "TYPESAFE_API_KEY", "AML_ALERT_WEBHOOK"} {
		if strings.Contains(r.stdout+r.stderr, h.secrets[label]) {
			t.Errorf("new printed the %s", label)
		}
	}
}

// telegramURLRule is the body of the telegram_url function of a script of
// deploy/aml.
func telegramURLRule(t *testing.T, script string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "aml", script))
	if err != nil {
		t.Fatal(err)
	}
	_, body, found := strings.Cut(string(raw), "\ntelegram_url() {\n")
	body, _, closed := strings.Cut(body, "\n}\n")
	if !found || !closed || !strings.Contains(body, "sendMessage") {
		t.Fatalf("%s has no telegram_url function", script)
	}
	return body
}

// Both scripts check a Telegram webhook by the same rule, which is never
// looser than the watcher's: new refuses what preflight would fail, and the
// other way round.
func TestScriptsShareTheTelegramRule(t *testing.T) {
	if instance, preflight := telegramURLRule(t, "instance.sh"), telegramURLRule(t, "preflight.sh"); instance != preflight {
		t.Errorf("the scripts' telegram_url differ:\ninstance.sh:\n%s\npreflight.sh:\n%s", instance, preflight)
	}
}

// telegramCase is a Telegram webhook with what the watcher answers (its
// checkDelivery) and what the scripts' telegram_url must.
type telegramCase struct {
	url              string
	watcher, scripts bool
	why              string
}

// telegramCases are the cases of TestWatchTelegramNeedsChatID
// (cmd/memory/aml_watch_test.go), with others the scripts' shell had to get
// right. The watcher's answers were taken from checkDelivery when the table
// was written.
func telegramCases() []telegramCase {
	const token = "7012345678:AAabcdef"
	bot := "https://api.telegram.org/bot" + token
	return []telegramCase{
		{bot + "/sendMessage?chat_id=123456789", true, true, "a private chat"},
		{bot + "/sendMessage?chat_id=-1001234567890", true, true, "a supergroup"},
		{bot + "/sendMessage?chat_id=@mnemon_alerts", true, true, "a channel"},
		{bot + "/sendMessage?chat_id=%40mnemon_alerts", true, true, "a channel, @ escaped"},
		{bot + "/sendMessage?chat_id=" + strings.Repeat("9", 20), true, true, "the longest number"},
		{bot + "/sendMessage?chat_id=@a" + strings.Repeat("b", 31), true, true, "the longest channel name"},
		{"https://bots.example.com/bot" + token + "/sendMessage?chat_id=42", true, true, "another host"},
		{"https://api.telegram.org:8443/bot" + token + "/sendMessage?chat_id=42", true, true, "a port"},
		{"https://api.telegram.org/sendMessage?chat_id=42", true, true, "a Bot API server with no bot in its path"},
		{"https://api.telegram.org/a/b/c/sendMessage?chat_id=42", true, true, "a deeper path"},
		{"https://api.telegram.org/bot" + token + "é/sendMessage?chat_id=42", true, true, "a non-ASCII character in the path"},

		// What the watcher takes and the scripts are stricter about.
		{"http://127.0.0.1:8081/bot" + token + "/sendMessage?chat_id=42", true, false, "http to this machine: the scripts take https only"},
		{"http://localhost:8081/bot" + token + "/sendMessage?chat_id=42", true, false, "http to localhost"},
		{"https://[2001:db8::1]:8081/bot" + token + "/sendMessage?chat_id=42", true, false, "an IPv6 host"},
		{"https://user:pw@bots.example.com/bot" + token + "/sendMessage?chat_id=42", true, false, "user:password@"},
		{bot + "/sendMessage?chat_id=42#x", true, false, "a fragment, which the watcher drops"},
		{"https://api.telegram.org/bot" + token + " /sendMessage?chat_id=42", true, false, "a space in the path, which the watcher escapes"},

		// What both refuse.
		{bot + "/sendMessage", false, false, "no query"},
		{bot + "/sendMessage?", false, false, "an empty query"},
		{bot + "/sendMessage?chat_id=", false, false, "an empty chat"},
		{bot + "/sendMessage?chat_id=abc", false, false, "a word"},
		{bot + "/sendMessage?chat_id=12.5", false, false, "a fraction"},
		{bot + "/sendMessage?chat_id=%2B42", false, false, "a plus sign"},
		{bot + "/sendMessage?chat_id=--42", false, false, "two minus signs"},
		{bot + "/sendMessage?chat_id=" + strings.Repeat("9", 21), false, false, "a number too long"},
		{bot + "/sendMessage?chat_id=@abcd", false, false, "a channel name too short"},
		{bot + "/sendMessage?chat_id=@1channel", false, false, "a channel name that starts with a digit"},
		{bot + "/sendMessage?chat_id=@a" + strings.Repeat("b", 32), false, false, "a channel name too long"},
		{bot + "/sendMessage?chat_id=42%0A", false, false, "a newline escaped in the chat"},
		{bot + "/sendMessage?chat_id=42&chat_id=43", false, false, "two chats"},
		{bot + "/sendMessage?chat_id=42&parse_mode=HTML", false, false, "another parameter"},
		{bot + "/sendMessage?chat_id=42;x=1", false, false, "a semicolon"},
		{bot + "/sendMessage?chat=42", false, false, "another parameter name"},
		{bot + "/getUpdates?chat_id=42", false, false, "another method"},
		{bot + "/sendMessage/?chat_id=42", false, false, "a trailing slash"},
		{bot + "/sendmessage?chat_id=42", false, false, "another case"},
		{bot + "?chat_id=42", false, false, "no method"},
		{"http://api.telegram.org/bot" + token + "/sendMessage?chat_id=42", false, false, "http to another host"},
		{"https:///bot" + token + "/sendMessage?chat_id=42", false, false, "no host"},
		{"api.telegram.org/bot" + token + "/sendMessage?chat_id=42", false, false, "no scheme"},
		{"https://api.telegram.org/bot" + token + "/sendMessage?chat_id=4%zz2", false, false, "a bad escape in the chat"},
		{"https://api.telegram.org/bot" + token + "%zz/sendMessage?chat_id=42", false, false, "a bad escape in the path"},
		{"https://api telegram.org/bot" + token + "/sendMessage?chat_id=42", false, false, "a space in the host"},
		{"https://api.telegram.org/bot" + token + "\t/sendMessage?chat_id=42", false, false, "a tab in the path"},
		{"https://api.telegram.org/bot" + token + "\n/sendMessage?chat_id=42", false, false, "a newline in the path"},
		{"https://api.telegram.org/bot" + token + "\x01/sendMessage?chat_id=42", false, false, "a control character in the path"},
		{"https://api.telegram.org/bot" + token + "\x1f/sendMessage?chat_id=42", false, false, "the last control character below the space"},
		{"https://api.telegram.org/bot" + token + "\x7f/sendMessage?chat_id=42", false, false, "DEL in the path"},
		{"https://api.telegram.org/bot" + token + "/send\x01Message?chat_id=42", false, false, "a control character in the method"},
		{"https://api.telegram.org\x01/bot" + token + "/sendMessage?chat_id=42", false, false, "a control character in the host"},
		{bot + "/sendMessage?chat_id=4\x012", false, false, "a control character in the chat"},
	}
}

// The scripts' Telegram rule never takes a webhook the watcher refuses, and
// takes the ones the table says it does. The scripts may be stricter than the
// watcher (they take https only, no IPv6 host, no user:password@, no fragment,
// no space), and are never looser. Control characters were the gap (the final
// review's parked T15-a): the watcher's URL parser refuses them in the path,
// and the shell's character class let all but the white-space ones through.
func TestTelegramRuleNeverLooserThanTheWatchers(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is missing")
	}
	cases := telegramCases()
	for _, c := range cases {
		if c.scripts && !c.watcher {
			t.Fatalf("%q (%s): the table has the scripts take what the watcher refuses", c.url, c.why)
		}
	}
	for _, script := range []string{"instance.sh", "preflight.sh"} {
		urls := make([]string, len(cases))
		for i, c := range cases {
			urls[i] = c.url
		}
		code := "LC_ALL=C\ntelegram_url() {\n" + telegramURLRule(t, script) + "\n}\nfor url; do if telegram_url \"$url\"; then echo yes; else echo no; fi; done\n"
		out, err := exec.Command("bash", append([]string{"-c", code, "_"}, urls...)...).Output()
		if err != nil {
			t.Fatalf("%s: %v", script, err)
		}
		answers := strings.Fields(string(out))
		if len(answers) != len(cases) {
			t.Fatalf("%s answered %d of %d cases", script, len(answers), len(cases))
		}
		for i, c := range cases {
			if got := answers[i] == "yes"; got != c.scripts {
				t.Errorf("%s takes %q (%s): %v, want %v (the watcher: %v)", script, c.url, c.why, got, c.scripts, c.watcher)
			}
		}
	}
}
