package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/mnemon-dev/mnemon/internal/aml"
	"github.com/mnemon-dev/mnemon/internal/memoryagent"
)

func TestAMLServeIsAMemoryAgentSubcommand(t *testing.T) {
	command, _, err := rootCmd.Find([]string{"memory-agent", "aml-serve"})
	if err != nil || command.Name() != "aml-serve" || command.Parent().Name() != "memory-agent" {
		t.Fatal(command, err)
	}
	for flag, value := range map[string]string{
		"listen": "127.0.0.1:8090", "data-root": "", "memory-config": "", "auth-key-env": "AML_API_KEY", "consolidate-on-add": "false",
		"max-open-stores": "64", "search-timeout": "10m0s", "consolidate": "off", "consolidation-workers": "4", "admin-listen": "",
		"admin-token-env": "", "max-concurrent-adds": "0", "max-concurrent-searches": "0", "log-format": "text",
	} {
		if f := command.Flags().Lookup(flag); f == nil || f.DefValue != value {
			t.Fatal(flag, f)
		}
	}
}

// amlFlags parses args, after an absolute --data-root, as aml-serve's flags.
func amlFlags(t *testing.T, args ...string) (amlServeOptions, error) {
	t.Helper()
	command := newAMLServeCommand()
	if err := command.ParseFlags(append([]string{"--data-root", t.TempDir()}, args...)); err != nil {
		t.Fatal(err)
	}
	return amlServeFlags(command)
}

// executeAMLServe runs aml-serve with args to its end.
func executeAMLServe(args ...string) error {
	command := newAMLServeCommand()
	command.SetArgs(args)
	command.SilenceUsage, command.SilenceErrors = true, true
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	return command.Execute()
}

func TestConsolidateFlagValues(t *testing.T) {
	for args, want := range map[string]aml.ConsolidationMode{
		"":                           aml.ConsolidationOff,
		"--consolidate off":          aml.ConsolidationOff,
		"--consolidate on-add":       aml.ConsolidationOnAdd,
		"--consolidate background":   aml.ConsolidationBackground,
		"--consolidate-on-add":       aml.ConsolidationOnAdd, // the older spelling
		"--consolidate-on-add=true":  aml.ConsolidationOnAdd,
		"--consolidate-on-add=false": aml.ConsolidationOff,
	} {
		options, err := amlFlags(t, strings.Fields(args)...)
		if err != nil || options.consolidation != want {
			t.Fatalf("%q: %q %v", args, options.consolidation, err)
		}
	}
	for args, want := range map[string]string{
		"--consolidate bogus": `--consolidate must be off, on-add or background, not "bogus"`,
		"--consolidate=":      `--consolidate must be off, on-add or background, not ""`,
		"--consolidate-on-add --consolidate background": "--consolidate-on-add is the older spelling of --consolidate on-add: give one of them",
		"--consolidate on-add --consolidate-on-add":     "--consolidate-on-add is the older spelling of --consolidate on-add: give one of them",
		"--consolidate-on-add=false --consolidate off":  "--consolidate-on-add is the older spelling of --consolidate on-add: give one of them",
		"--consolidation-workers 0":                     "--consolidation-workers must be at least 1",
		"--max-concurrent-adds -1":                      "--max-concurrent-adds must not be negative",
		"--max-concurrent-searches -1":                  "--max-concurrent-searches must not be negative",
		"--log-format yaml":                             `--log-format must be text or json, not "yaml"`,
	} {
		if _, err := amlFlags(t, strings.Fields(args)...); err == nil || err.Error() != want {
			t.Fatalf("%q: %v", args, err)
		}
	}
	// The command refuses them before it takes the data root's lock.
	dir := t.TempDir()
	t.Setenv("AML_TEST_KEY", "k")
	if err := executeAMLServe("--data-root", dir, "--memory-config", filepath.Join(dir, "memory.json"), "--auth-key-env", "AML_TEST_KEY", "--consolidate", "bogus"); err == nil || !strings.Contains(err.Error(), "--consolidate must be") {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatal("the refused command touched the data root", entries, err)
	}
}

// The E1 cases of a listener's address, which aml-serve's --admin-listen
// (TestAdminListenRules) and aml-inspect's --listen (TestInspectAuthAndListen)
// both follow: loopback addresses need no token, private ones need it, and
// every other is refused, token or not.
var (
	loopbackListens = []string{"127.0.0.1:8091", "127.0.0.1:0", "127.0.0.2:8091", "[::1]:8091", "[::ffff:127.0.0.1]:8091"}
	privateListens  = []string{"10.0.0.5:8091", "172.16.0.1:8091", "172.31.255.255:8091", "192.168.1.2:8091", "[fd00::1]:8091", "[::ffff:10.0.0.5]:8091"}
	// Unspecified, link-local, shared (100.64/10), public and documentation
	// addresses, the neighbours of the private ranges, host names, and what
	// is not an address and a port.
	refusedListens = []string{
		"0.0.0.0:8091", "[::]:8091", ":8091", "[::ffff:0.0.0.0]:8091",
		"169.254.1.1:8091", "[fe80::1]:8091", "[fe80::1%eth0]:8091", "[fd00::1%eth0]:8091",
		"100.64.0.1:8091", "203.0.113.5:8091", "[::ffff:203.0.113.5]:8091", "[2001:db8::1]:8091", "172.32.0.1:8091", "172.15.255.255:8091", "11.0.0.1:8091", "192.169.0.1:8091",
		"localhost:8091", "example.com:8091", "10.0.0.5", "10.0.0.5:", "10.0.0.5:99999", "10.0.0.5:http", "[10.0.0.5]", "http://10.0.0.5:8091",
	}
)

// The admin endpoints are served on a loopback address, where an admin token
// is optional, or on a private one (10/8, 172.16/12, 192.168/16, fc00::/7),
// where it is required. Every other address is refused, with a token or
// without, as is a host name or an address with no port. A flag that names a
// variable needs it to hold 32 bytes at least.
func TestAdminListenRules(t *testing.T) {
	const variable = "AML_TEST_ADMIN_TOKEN"
	token := strings.Repeat("t", 32)
	wantError := func(t *testing.T, err error, parts ...string) {
		t.Helper()
		if err == nil {
			t.Fatal("no error")
		}
		for _, part := range parts {
			if !strings.Contains(err.Error(), part) {
				t.Fatalf("%q lacks %q", err, part)
			}
		}
	}

	// Loopback addresses, and none at all, need no token. An IPv4 address
	// spelled as an IPv6 one is the IPv4 address.
	for _, addr := range append([]string{""}, loopbackListens...) {
		if options, err := amlFlags(t, "--admin-listen", addr); err != nil || options.adminListen != addr || options.adminToken != "" {
			t.Fatal(addr, options.adminListen, err)
		}
		// A token is optional there, not forbidden.
		t.Setenv(variable, token)
		if options, err := amlFlags(t, "--admin-listen", addr, "--admin-token-env", variable); err != nil || options.adminToken != token {
			t.Fatal(addr, err)
		}
	}

	// Private addresses are refused without a token, and accepted with one.
	for _, addr := range privateListens {
		_, err := amlFlags(t, "--admin-listen", addr)
		wantError(t, err, "--admin-listen", "private", "--admin-token-env")
		t.Setenv(variable, token)
		if options, err := amlFlags(t, "--admin-listen", addr, "--admin-token-env", variable); err != nil || options.adminListen != addr || options.adminToken != token {
			t.Fatal(addr, options.adminListen, err)
		}
	}

	// Everything else is refused, whatever the token.
	for _, addr := range refusedListens {
		_, err := amlFlags(t, "--admin-listen", addr)
		wantError(t, err, "--admin-listen must be")
		t.Setenv(variable, token)
		_, err = amlFlags(t, "--admin-listen", addr, "--admin-token-env", variable)
		wantError(t, err, "--admin-listen must be")
	}

	// The token: the variable the flag names must hold 32 bytes at
	// least, on any address; what the error says never repeats a value.
	const missing = "AML_TEST_NO_SUCH_ADMIN_TOKEN"
	for _, addr := range []string{"", "127.0.0.1:8091", "10.0.0.5:8091"} {
		t.Setenv(variable, "")
		_, err := amlFlags(t, "--admin-listen", addr, "--admin-token-env", variable)
		wantError(t, err, "--admin-token-env", variable, "unset or empty")
		os.Unsetenv(missing)
		_, err = amlFlags(t, "--admin-listen", addr, "--admin-token-env", missing)
		wantError(t, err, "--admin-token-env", missing, "unset or empty")
		short := strings.Repeat("s", 31)
		t.Setenv(variable, short)
		_, err = amlFlags(t, "--admin-listen", addr, "--admin-token-env", variable)
		wantError(t, err, "--admin-token-env", variable, "32 bytes")
		if strings.Contains(err.Error(), short) {
			t.Fatal("the error shows the token:", err)
		}
	}
	// Even with no admin listener, a flag that names a variable is checked: an
	// operator who set it means to have a token.
	t.Setenv(variable, "")
	_, err := amlFlags(t, "--admin-token-env", variable)
	wantError(t, err, "--admin-token-env", variable)

	// The command refuses them before it takes the data root's lock.
	dir := t.TempDir()
	t.Setenv("AML_TEST_KEY", "k")
	err = executeAMLServe("--data-root", dir, "--memory-config", filepath.Join(dir, "memory.json"), "--auth-key-env", "AML_TEST_KEY", "--admin-listen", "10.0.0.5:8091")
	wantError(t, err, "--admin-listen", "private")
	err = executeAMLServe("--data-root", dir, "--memory-config", filepath.Join(dir, "memory.json"), "--auth-key-env", "AML_TEST_KEY", "--admin-listen", "127.0.0.1:0", "--admin-token-env", missing)
	wantError(t, err, "--admin-token-env", missing)
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatal("a refused command touched the data root", entries, err)
	}
}

// Parked T14-d (final review): the admin token and the API key are two
// secrets: --admin-token-env naming the variable --auth-key-env names would
// make the API key, which every client holds, the admin token. It is
// refused, whatever the variable holds, and the error shows no value.
func TestAdminTokenIsNotTheAPIKey(t *testing.T) {
	const shared = "AML_TEST_SHARED_SECRET"
	secret := strings.Repeat("k", 40)
	t.Setenv(shared, secret)
	_, err := amlFlags(t, "--admin-listen", "127.0.0.1:8091", "--admin-token-env", shared, "--auth-key-env", shared)
	if err == nil || !strings.Contains(err.Error(), "--admin-token-env") || !strings.Contains(err.Error(), "--auth-key-env") || strings.Contains(err.Error(), secret) {
		t.Fatal(err)
	}
	// Two variables, even holding one value, are two flags' business: the
	// kit writes two secrets.
	t.Setenv("AML_TEST_OTHER_SECRET", secret)
	if _, err := amlFlags(t, "--admin-listen", "127.0.0.1:8091", "--admin-token-env", "AML_TEST_OTHER_SECRET", "--auth-key-env", shared); err != nil {
		t.Fatal(err)
	}
}

// One aml-serve per data root: a second one refuses to start while the first
// holds <data-root>/.aml-serve.lock, before it opens the budget file or a
// store; a command that fails after taking the lock gives it back.
func TestAMLServeRefusesSecondInstance(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "memory.json")
	if err := os.WriteFile(config, []byte(`{"version":1,"strategy":"simple"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AML_TEST_KEY", "k")
	args := []string{"--data-root", dir, "--memory-config", config, "--auth-key-env", "AML_TEST_KEY"}
	path := filepath.Join(dir, ".aml-serve.lock")
	held := flock.New(path)
	if locked, err := held.TryLock(); !locked || err != nil {
		t.Fatal(locked, err)
	}
	if err := executeAMLServe(args...); err == nil || err.Error() != "another aml-serve holds "+path {
		t.Fatal(err)
	}
	if err := held.Unlock(); err != nil {
		t.Fatal(err)
	}
	// Free, the lock is taken; the server then refuses the configuration,
	// which names no models, and the lock is given back.
	if err := executeAMLServe(args...); err == nil || !strings.Contains(err.Error(), "requires planner and decision models") {
		t.Fatal(err)
	}
	if locked, err := held.TryLock(); !locked || err != nil {
		t.Fatal("the failed command kept the lock", locked, err)
	}
	held.Unlock()
}

func TestAMLServeRefusesBeforeListening(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "memory.json")
	if err := os.WriteFile(config, []byte(`{"version":1,"strategy":"simple"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AML_TEST_KEY", "")
	t.Setenv("AML_TEST_SET", "k")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--data-root", "relative", "--memory-config", config, "--auth-key-env", "AML_TEST_SET"}, "absolute --data-root"},
		{[]string{"--data-root", dir, "--memory-config", config, "--auth-key-env", "AML_TEST_KEY"}, "nonempty API key in $AML_TEST_KEY"},
		{[]string{"--data-root", dir, "--memory-config", "memory.json", "--auth-key-env", "AML_TEST_SET"}, "absolute file path"},
		// The configuration is checked by making an agent: no models.
		{[]string{"--data-root", dir, "--memory-config", config, "--auth-key-env", "AML_TEST_SET"}, "requires planner and decision models"},
		{[]string{"--data-root", dir, "--memory-config", config, "--auth-key-env", "AML_TEST_SET", "--max-open-stores", "0"}, "--max-open-stores"},
		{[]string{"--data-root", dir, "--memory-config", config, "--auth-key-env", "AML_TEST_SET", "--search-timeout", "59s"}, "--search-timeout must be between 1m and 18m"},
		{[]string{"--data-root", dir, "--memory-config", config, "--auth-key-env", "AML_TEST_SET", "--search-timeout", "19m"}, "--search-timeout must be between 1m and 18m"},
	} {
		command := newAMLServeCommand()
		command.SetArgs(c.args)
		command.SilenceUsage, command.SilenceErrors = true, true
		if err := command.Execute(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatal(c.args, err)
		}
	}
}

// A watchdog interval the liveness probe cannot keep up with, or one systemd
// could not have set, refuses to start before the data root is locked.
func TestAMLServeRefusesAWatchdogItCannotPing(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "memory.json")
	if err := os.WriteFile(config, []byte(`{"version":1,"strategy":"simple"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AML_TEST_KEY", "k")
	for usec, want := range map[string]string{
		"1999999": "systemd's watchdog interval 1.999999s is below 2s: set WatchdogSec to at least 2s",
		"soon":    "systemd watchdog: error converting WATCHDOG_USEC",
	} {
		t.Setenv("WATCHDOG_USEC", usec)
		if err := executeAMLServe("--data-root", dir, "--memory-config", config, "--auth-key-env", "AML_TEST_KEY"); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatal(usec, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".aml-serve.lock")); !os.IsNotExist(err) {
		t.Fatal("the refused command took the lock", err)
	}
}

// deployFile is the absolute path of a file of the deployment kit.
func deployFile(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "deploy", "aml", name))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// The industrial configuration is what the runbook tells the operator to
// install: strict decoding must accept it, it must say what the submission
// says it does, and a server must start on it (no network is used).
func TestIndustrialConfigLoadsAndStarts(t *testing.T) {
	config, err := readMemoryConfig(deployFile(t, "memory-config.industrial.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := memoryagent.Config{
		Version:  1,
		Strategy: "simple",
		// The judged View: no fusion with the plain search ranking, so the
		// View is the records JEV judged (maxItems and maxCharacters only
		// cap it), delivered in time order after the consolidation blocks.
		View: &memoryagent.ViewConfig{MaxItems: 100, MaxCharacters: 240000, Order: "time"},
		// Layered consolidation: DeepSeek extracts each batch alone, JEV
		// links it into the memory. No native edges, no expansion, no index
		// routes: Expand, Index and NativeEdges stay unset.
		Consolidation: &memoryagent.ConsolidationConfig{Mode: "layered"},
		// DeepSeek without thinking: a thinking planner can return empty plans
		// under the 200/400-token planning caps.
		Planner: &memoryagent.ModelConfig{Kind: "deepseek", Model: "deepseek-flash", KeyEnv: "DEEPSEEK_API_KEY"},
		Judge:   &memoryagent.ModelConfig{Kind: "jev", Model: "jev-1.13.0", KeyEnv: "TYPESAFE_API_KEY"},
		// The extraction of a full batch may take minutes; the default 90 s
		// would cut it off.
		Consolidator: &memoryagent.ModelConfig{Kind: "deepseek", Model: "deepseek-flash", KeyEnv: "DEEPSEEK_API_KEY", TimeoutSeconds: 600},
		// BM25 only, no Ollama: without an embedding section the cue searches
		// match words, the consolidation layer and the linking rank by shared
		// words, and an Add embeds nothing.
		Embedding:  nil,
		BudgetFile: "/var/lib/mnemon-aml/model-budget.db",
		// The evaluation caps: JEV in USD, DeepSeek in CNY.
		Budgets: map[string]float64{"jev": 60, "deepseek": 600},
	}
	if !reflect.DeepEqual(config, want) {
		got, _ := json.MarshalIndent(config, "", " ")
		wanted, _ := json.MarshalIndent(want, "", " ")
		t.Fatalf("industrial config\n got %s\nwant %s", got, wanted)
	}

	// The same file with a writable budget path: NewServer builds the real
	// providers and probes the agents, as aml-serve does at startup.
	config.BudgetFile = filepath.Join(t.TempDir(), "model-budget.db")
	serve := func() error {
		server, err := aml.NewServer(aml.ServerConfig{DataRoot: t.TempDir(), MemoryConfig: config, AuthKey: "k", ConsolidateOnAdd: true, MaxOpenStores: 16})
		if err == nil {
			server.Close()
		}
		return err
	}
	t.Setenv("DEEPSEEK_API_KEY", "test-deepseek")
	t.Setenv("TYPESAFE_API_KEY", "test-typesafe")
	if err := serve(); err != nil {
		t.Fatal(err)
	}
	// The runbook's troubleshooting entry: a missing key refuses to start.
	t.Setenv("TYPESAFE_API_KEY", "")
	if err := serve(); err == nil || !strings.Contains(err.Error(), "credential environment variable") {
		t.Fatal(err)
	}
}

// systemdUnit is a unit file's settings: by section, each key's values in
// file order (a key may be given more than once).
type systemdUnit map[string]map[string][]string

// readUnit parses the kit's unit file name. Comments and blank lines are
// skipped. A setting continued on the next line is refused, so that every
// setting, ExecStart above all, is one line, the same for every instance.
func readUnit(t *testing.T, name string) systemdUnit {
	t.Helper()
	raw, err := os.ReadFile(deployFile(t, name))
	if err != nil {
		t.Fatal(err)
	}
	unit, section := systemdUnit{}, ""
	// A Windows checkout may end the lines with \r\n.
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";"):
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
			section = strings.Trim(line, "[]")
			if unit[section] == nil {
				unit[section] = map[string][]string{}
			}
		default:
			key, value, ok := strings.Cut(line, "=")
			if !ok || section == "" || strings.HasSuffix(value, `\`) {
				t.Fatalf("%s: %q is not a one-line setting of a section", name, line)
			}
			key = strings.TrimSpace(key)
			unit[section][key] = append(unit[section][key], strings.TrimSpace(value))
		}
	}
	return unit
}

// get is the one value of key in section; a missing or repeated key fails.
func (u systemdUnit) get(t *testing.T, section, key string) string {
	t.Helper()
	if values := u[section][key]; len(values) == 1 {
		return values[0]
	}
	t.Fatalf("[%s] %s is %q, want one value", section, key, u[section][key])
	return ""
}

// systemdSpan reads a systemd time span of one number and a unit: none
// (seconds), ms, s or min.
func systemdSpan(t *testing.T, span string) time.Duration {
	t.Helper()
	match := regexp.MustCompile(`^([0-9]+)(ms|s|min)?$`).FindStringSubmatch(span)
	if match == nil {
		t.Fatalf("time span %q is not one number and a unit", span)
	}
	n, _ := time.ParseDuration(match[1] + "s")
	switch match[2] {
	case "ms":
		return n / 1000
	case "min":
		return n * 60
	}
	return n
}

// execArgs is the command line systemd runs for execStart in instance, whose
// EnvironmentFile holds env: %i is the instance and ${NAME} is NAME's value.
// The templates use neither %I, which would turn the dashes of a name into
// slashes, nor $NAME, which systemd splits at whitespace.
func execArgs(t *testing.T, execStart, instance string, env map[string]string) []string {
	t.Helper()
	line := strings.ReplaceAll(execStart, "%i", instance)
	var unknown []string
	line = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`).ReplaceAllStringFunc(line, func(ref string) string {
		value, ok := env[ref[2:len(ref)-1]]
		if !ok {
			unknown = append(unknown, ref)
		}
		return value
	})
	if len(unknown) > 0 || strings.ContainsAny(line, "$%") {
		t.Fatalf("ExecStart %q uses %q, which the instance's environment file does not set, or a $ or %% this test does not expand", execStart, unknown)
	}
	return strings.Fields(line)
}

// unitSandbox is the sandboxing aml-serve@.service sets and
// aml-watch@.service repeats.
var unitSandbox = []string{
	"UMask", "NoNewPrivileges", "PrivateTmp", "PrivateDevices", "ProtectSystem", "ProtectHome",
	"ProtectKernelTunables", "ProtectKernelModules", "ProtectControlGroups", "RestrictSUIDSGID",
	"LockPersonality", "RestrictRealtime",
}

// The instance templates must run aml-serve and aml-watch with flags the
// commands have once systemd has filled an instance in, and carry the
// settings the runbook and the memory cloud design rely on.
func TestServiceUnitFlagsParse(t *testing.T) {
	// No file of the kit mentions Ollama: retrieval is BM25 only.
	err := filepath.WalkDir(deployFile(t, ""), func(file string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		raw, err := os.ReadFile(file)
		if err == nil && strings.Contains(strings.ToLower(string(raw)), "ollama") {
			t.Errorf("%s mentions Ollama", file)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	// A dedicated instance's two environment files, as mnemon-instance new
	// writes them, each with only its own names: env, the engine's (the record,
	// the addresses, the flag values, the Go memory limit and the secrets), and
	// watch.env, the watcher's five. A template that reads a variable its file
	// does not set fails here.
	const instance = "eval"
	env := map[string]string{
		"AML_LAYOUT": "dedicated", "AML_SLOT": "0", "AML_STATUS": "active",
		"AML_LISTEN": "127.0.0.1:8090", "AML_ADMIN_LISTEN": "10.0.0.5:8091",
		"AML_MAX_ADDS": "16", "AML_MAX_SEARCHES": "16", "AML_MAX_OPEN_STORES": "16", "AML_WORKERS": "4",
		"GOMEMLIMIT": "32768MiB", "AML_API_KEY": "mnm_api-key", "MNEMON_ADMIN_TOKEN": "mnm_admin-token",
		"DEEPSEEK_API_KEY": "sk-deepseek", "TYPESAFE_API_KEY": "ts-jev",
	}
	watchEnv := map[string]string{
		"AML_ALERT_WEBHOOK": "https://api.telegram.org/bot123:token/sendMessage?chat_id=42", "AML_ALERT_KIND": "telegram",
		"AML_PUBLIC_URL": "https://eval.example.com", "AML_ADMIN_LISTEN": env["AML_ADMIN_LISTEN"], "MNEMON_ADMIN_TOKEN": env["MNEMON_ADMIN_TOKEN"],
	}

	serve := readUnit(t, "aml-serve@.service")
	args := execArgs(t, serve.get(t, "Service", "ExecStart"), instance, env)
	if len(args) < 3 || !slices.Equal(args[:3], []string{"/usr/local/bin/mnemon", "memory-agent", "aml-serve"}) {
		t.Fatal("aml-serve@.service runs", args)
	}
	command := newAMLServeCommand()
	if err := command.ParseFlags(args[3:]); err != nil {
		t.Fatal(err)
	}
	if rest := command.Flags().Args(); len(rest) > 0 {
		t.Fatal("aml-serve@.service passes arguments that are not flags:", rest)
	}
	for flag, want := range map[string]string{
		"listen":                  "127.0.0.1:8090",
		"admin-listen":            "10.0.0.5:8091",
		"admin-token-env":         "MNEMON_ADMIN_TOKEN",
		"auth-key-env":            "AML_API_KEY",
		"data-root":               "/var/lib/mnemon-aml/eval/data",
		"memory-config":           "/etc/mnemon-aml/eval/memory-config.json",
		"consolidate":             "background",
		"consolidation-workers":   "4",
		"max-open-stores":         "16",
		"max-concurrent-adds":     "16",
		"max-concurrent-searches": "16",
		"log-format":              "json",
	} {
		if f := command.Flags().Lookup(flag); !f.Changed || f.Value.String() != want {
			t.Errorf("aml-serve@.service passes --%s %q, want %q", flag, f.Value.String(), want)
		}
	}
	if command.Flags().Changed("consolidate-on-add") {
		t.Error("aml-serve@.service passes --consolidate-on-add")
	}
	// The admin listener binds the host's WireGuard address: the unit starts
	// wg0 with it and after it, but never needs it (R91: a Requires= would
	// stop the evaluation with the overlay, and aml-serve serves its public
	// listener while the admin one waits for wg0).
	for _, key := range []string{"Wants", "After"} {
		if values := strings.Fields(serve.get(t, "Unit", key)); !slices.Contains(values, "wg-quick@wg0.service") {
			t.Errorf("aml-serve@.service %s=%q lacks wg-quick@wg0.service", key, values)
		}
	}
	for _, key := range []string{"Requires", "Requisite", "BindsTo", "PartOf"} {
		if values, ok := serve["Unit"][key]; ok {
			t.Errorf("aml-serve@.service %s=%q: the data plane must not depend on the overlay", key, values)
		}
	}
	// The admin listener binds the host's private overlay address, which
	// aml-serve accepts only with the admin token.
	if err := checkAdminListen(env["AML_ADMIN_LISTEN"], true); err != nil {
		t.Fatal(err)
	}
	if checkAdminListen(env["AML_ADMIN_LISTEN"], false) == nil {
		t.Fatal("a private admin listener is accepted without a token")
	}
	for _, s := range []struct{ section, key, want string }{
		{"Unit", "StartLimitIntervalSec", "0"},
		{"Service", "Type", "notify"},
		{"Service", "NotifyAccess", "main"},
		{"Service", "Restart", "always"},
		{"Service", "RestartSec", "5"},
		{"Service", "TimeoutStopSec", "60"},
		{"Service", "User", "mnemon"},
		{"Service", "Group", "mnemon"},
		{"Service", "EnvironmentFile", "/etc/mnemon-aml/%i/env"},
		// The data root and the budget file are under /var/lib/mnemon-aml/<n>/.
		{"Service", "StateDirectory", "mnemon-aml/%i"},
		{"Service", "StateDirectoryMode", "0700"},
		{"Service", "LimitNOFILE", "65536"},
		{"Install", "WantedBy", "multi-user.target"},
	} {
		if got := serve.get(t, s.section, s.key); got != s.want {
			t.Errorf("aml-serve@.service [%s] %s=%s, want %s", s.section, s.key, got, s.want)
		}
	}
	if watchdog := systemdSpan(t, serve.get(t, "Service", "WatchdogSec")); watchdog < amlMinWatchdog {
		t.Errorf("WatchdogSec %s is below the %s aml-serve accepts", watchdog, amlMinWatchdog)
	}
	// The shutdown (aml_process.go) fits in TimeoutStopSec: the requests in
	// flight and the scheduler's drain share amlShutdownGrace, the requests
	// canceled then have amlCancelGrace more, and the stores and models close
	// within the same window, the model requests cut at its end settling
	// within amlSettleGrace (R90).
	if stop, shutdown := systemdSpan(t, serve.get(t, "Service", "TimeoutStopSec")), amlShutdownGrace+amlCancelGrace+amlSettleGrace; stop <= shutdown {
		t.Errorf("TimeoutStopSec %s does not outlast the shutdown's %s", stop, shutdown)
	}
	// The limits are per instance, in its drop-in, so the template is the
	// same for every instance.
	for _, key := range []string{"MemoryMax", "CPUQuota"} {
		if _, ok := serve["Service"][key]; ok {
			t.Errorf("aml-serve@.service sets %s, which belongs in the instance's limits.conf drop-in", key)
		}
	}

	watch := readUnit(t, "aml-watch@.service")
	args = execArgs(t, watch.get(t, "Service", "ExecStart"), instance, watchEnv)
	if len(args) < 3 || !slices.Equal(args[:3], []string{"/usr/local/bin/mnemon", "memory-agent", "aml-watch"}) {
		t.Fatal("aml-watch@.service runs", args)
	}
	command = newAMLWatchCommand()
	if err := command.ParseFlags(args[3:]); err != nil {
		t.Fatal(err)
	}
	if rest := command.Flags().Args(); len(rest) > 0 {
		t.Fatal("aml-watch@.service passes arguments that are not flags:", rest)
	}
	for flag, want := range map[string]string{
		"admin":           "http://" + watchEnv["AML_ADMIN_LISTEN"],
		"admin-token-env": "MNEMON_ADMIN_TOKEN",
		"public-url":      watchEnv["AML_PUBLIC_URL"],
		"instance":        instance,
		"state":           "/var/lib/mnemon-aml-watch/eval/state.json",
	} {
		if f := command.Flags().Lookup(flag); !f.Changed || f.Value.String() != want {
			t.Errorf("aml-watch@.service passes --%s %q, want %q", flag, f.Value.String(), want)
		}
	}
	state := command.Flags().Lookup("state").Value.String()
	if !path.IsAbs(state) || path.Clean(state) != state || !strings.HasPrefix(state, "/var/lib/mnemon-aml-watch/") {
		t.Errorf("--state %q is not an absolute path under /var/lib/mnemon-aml-watch/", state)
	}
	for flag, value := range map[string]string{"--admin": "http://" + watchEnv["AML_ADMIN_LISTEN"], "--public-url": watchEnv["AML_PUBLIC_URL"]} {
		if _, err := watchBaseURL(flag, value); err != nil {
			t.Error(err)
		}
	}
	if !watchInstance.MatchString(instance) {
		t.Errorf("aml-watch refuses --instance %q", instance)
	}
	for _, s := range []struct{ key, want string }{
		{"Type", "oneshot"},
		{"User", "mnemon"},
		{"Group", "mnemon"},
		{"EnvironmentFile", "/etc/mnemon-aml/%i/watch.env"},
		{"StateDirectory", "mnemon-aml-watch/%i"},
		{"StateDirectoryMode", "0700"},
		// A wedged run is killed, which gives its run lock back.
		{"TimeoutStartSec", "90"},
	} {
		if got := watch.get(t, "Service", s.key); got != s.want {
			t.Errorf("aml-watch@.service %s=%s, want %s", s.key, got, s.want)
		}
	}
	// The timer runs the watcher, which must run while aml-serve is down.
	if _, ok := watch["Install"]; ok {
		t.Error("aml-watch@.service has an [Install] section; its timer starts it")
	}
	if requires, ok := watch["Unit"]["Requires"]; ok {
		t.Errorf("aml-watch@.service requires %q: the watcher must run while aml-serve is down", requires)
	}
	for _, key := range unitSandbox {
		if want := serve["Service"][key]; len(want) == 0 || !slices.Equal(watch["Service"][key], want) {
			t.Errorf("%s is %q in aml-serve@.service and %q in aml-watch@.service, want one value in both", key, want, watch["Service"][key])
		}
	}

	timer := readUnit(t, "aml-watch@.timer")
	for _, s := range []struct{ section, key, want string }{
		{"Timer", "OnBootSec", "2min"},
		{"Timer", "OnUnitActiveSec", "1min"},
		{"Timer", "AccuracySec", "1s"},
		{"Install", "WantedBy", "timers.target"},
	} {
		if got := timer.get(t, s.section, s.key); got != s.want {
			t.Errorf("aml-watch@.timer [%s] %s=%s, want %s", s.section, s.key, got, s.want)
		}
	}

	// The inspect service: inspect.env, as mnemon-instance inspect enable
	// writes it, with only its own names.
	inspectEnv := map[string]string{
		"AML_INSPECT_LISTEN": "10.0.0.5:8092", "AML_INSPECT_DEBUG_CAPS": "deepseek=5,jev=1", "MNEMON_INSPECT_TOKEN": "mnm_inspect-token",
		"GOMEMLIMIT": "819MiB", "DEEPSEEK_API_KEY": "sk-deepseek", "TYPESAFE_API_KEY": "ts-jev",
	}
	inspect := readUnit(t, "aml-inspect@.service")
	args = execArgs(t, inspect.get(t, "Service", "ExecStart"), instance, inspectEnv)
	if len(args) < 3 || !slices.Equal(args[:3], []string{"/usr/local/bin/mnemon", "memory-agent", "aml-inspect"}) {
		t.Fatal("aml-inspect@.service runs", args)
	}
	command = newAMLInspectCommand()
	if err := command.ParseFlags(args[3:]); err != nil {
		t.Fatal(err)
	}
	if rest := command.Flags().Args(); len(rest) > 0 {
		t.Fatal("aml-inspect@.service passes arguments that are not flags:", rest)
	}
	for flag, want := range map[string]string{
		"listen":            "10.0.0.5:8092",
		"token-env":         "MNEMON_INSPECT_TOKEN",
		"data-root":         "/var/lib/mnemon-aml/eval/data",
		"memory-config":     "/etc/mnemon-aml/eval/memory-config.json",
		"debug-budget-file": "/var/lib/mnemon-aml-inspect/eval/debug-budget.db",
		"debug-caps":        "deepseek=5,jev=1",
		"max-concurrent":    "2",
		"log-format":        "json",
	} {
		if f := command.Flags().Lookup(flag); !f.Changed || f.Value.String() != want {
			t.Errorf("aml-inspect@.service passes --%s %q, want %q", flag, f.Value.String(), want)
		}
	}
	// The units run on Linux: on Windows their paths are not absolute (R89),
	// so there the flags are parsed but not checked.
	if runtime.GOOS != "windows" {
		t.Setenv("MNEMON_INSPECT_TOKEN", strings.Repeat("i", 47))
		options, err := amlInspectFlags(command)
		if err != nil || options.caps["deepseek"] != 5 || options.caps["jev"] != 1 {
			t.Fatal("aml-inspect@.service's flags do not parse:", err)
		}
	}
	debugBudget, dataRoot := command.Flags().Lookup("debug-budget-file").Value.String(), command.Flags().Lookup("data-root").Value.String()
	// The debug budget lives in the service's own state, apart from the
	// instance's budget file (memory-config.json's budgetFile).
	if dir := path.Dir(debugBudget); dir != "/var/lib/mnemon-aml-inspect/eval" || debugBudget == "/var/lib/mnemon-aml/eval/model-budget.db" {
		t.Errorf("--debug-budget-file %q is not in the service's state directory", debugBudget)
	}
	for _, s := range []struct{ section, key, want string }{
		{"Service", "Type", "simple"},
		{"Service", "User", "mnemon"},
		{"Service", "Group", "mnemon"},
		{"Service", "Restart", "on-failure"},
		{"Service", "EnvironmentFile", "/etc/mnemon-aml/%i/inspect.env"},
		// The instance's state (data root and budget file) is mounted read only.
		{"Service", "ReadOnlyPaths", "/var/lib/mnemon-aml/%i"},
		{"Service", "StateDirectory", "mnemon-aml-inspect/%i"},
		{"Service", "StateDirectoryMode", "0700"},
		{"Service", "MemoryMax", "1G"},
		{"Service", "CPUQuota", "100%"},
		{"Service", "Nice", "10"},
		// Best-effort at its lowest priority, not idle (R80): the idle
		// class can starve under sustained I/O, and an inspect read
		// holding its SHARED lock on the rollback-journal memory-index.db
		// would then hold up aml-serve's consolidation writes.
		{"Service", "IOSchedulingClass", "best-effort"},
		{"Service", "IOSchedulingPriority", "7"},
		{"Install", "WantedBy", "multi-user.target"},
	} {
		if got := inspect.get(t, s.section, s.key); got != s.want {
			t.Errorf("aml-inspect@.service [%s] %s=%s, want %s", s.section, s.key, got, s.want)
		}
	}
	// aml-inspect's shutdown (aml_inspect.go) fits in its TimeoutStopSec as
	// aml-serve's does (Minor 2, R90).
	if stop, shutdown := systemdSpan(t, inspect.get(t, "Service", "TimeoutStopSec")), inspectShutdownGrace+inspectCancelGrace+inspectSettleGrace; stop <= shutdown {
		t.Errorf("aml-inspect@.service TimeoutStopSec %s does not outlast the shutdown's %s", stop, shutdown)
	}
	if !strings.HasPrefix(dataRoot, strings.ReplaceAll(inspect.get(t, "Service", "ReadOnlyPaths"), "%i", instance)+"/") {
		t.Errorf("--data-root %s is not under ReadOnlyPaths=", dataRoot)
	}
	if requires, ok := inspect["Unit"]["Requires"]; ok {
		t.Errorf("aml-inspect@.service requires %q: it reads stores whether aml-serve runs or not", requires)
	}
	for _, key := range unitSandbox {
		if want := serve["Service"][key]; len(want) == 0 || !slices.Equal(inspect["Service"][key], want) {
			t.Errorf("%s is %q in aml-serve@.service and %q in aml-inspect@.service, want one value in both", key, want, inspect["Service"][key])
		}
	}
	// The inspect port is the admin port + 1, inside the firewall's
	// 8091–8600 overlay rule.
	_, port, _ := strings.Cut(inspectEnv["AML_INSPECT_LISTEN"], ":")
	_, adminPort, _ := strings.Cut(env["AML_ADMIN_LISTEN"], ":")
	if p, a := atoiPort(t, port), atoiPort(t, adminPort); p != a+1 || p < 8091 || p > 8600 {
		t.Errorf("inspect port %d, admin port %d", p, a)
	}
}

func atoiPort(t *testing.T, port string) int {
	t.Helper()
	n := 0
	for _, c := range port {
		if c < '0' || c > '9' {
			t.Fatalf("port %q", port)
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// The Caddyfiles must proxy to the ports the instances listen on, hold
// requests through a restart, and keep the platform's limits. The shared
// host's snippets are the instance script's (test/deploy).
func TestDeployKitMatchesTheCommand(t *testing.T) {
	read := func(name string) string {
		raw, err := os.ReadFile(deployFile(t, name))
		if err != nil {
			t.Fatal(err)
		}
		// Compare without the comments and with the column alignment collapsed.
		var kept []string
		for _, line := range strings.Split(string(raw), "\n") {
			if before, _, found := strings.Cut(line, "#"); found {
				line = before
			}
			kept = append(kept, line)
		}
		return strings.Join(strings.Fields(strings.Join(kept, "\n")), " ")
	}
	// The platform waits up to 31 minutes for an Add or a Search, and
	// aml-serve takes bodies of up to 30 MiB.
	limits := []string{"read_body 35m", "read_header 30s", "write 35m", "idle 5m", "max_size 30MiB"}

	dedicated := read("Caddyfile.dedicated")
	// Slot 0 of a dedicated host: data 127.0.0.1:8090. Caddy dials again
	// through a restart, and its upstream keep-alive stays below aml-serve's
	// 2-minute idle timeout.
	for _, want := range append([]string{
		"reverse_proxy 127.0.0.1:8090 {", "lb_try_duration 30s", "lb_try_interval 250ms",
		"keepalive 60s", "dial_timeout 5s", "response_header_timeout 35m", "read_timeout 35m", "write_timeout 35m",
	}, limits...) {
		if !strings.Contains(dedicated, want) {
			t.Errorf("Caddyfile.dedicated lacks %q", want)
		}
	}

	shared := read("Caddyfile.shared")
	for _, want := range append([]string{
		"import /etc/caddy/mnemon-instances/*.caddy",
		"handle { header Content-Type application/json respond `{\"error\":\"not found\"}` 404 }",
	}, limits...) {
		if !strings.Contains(shared, want) {
			t.Errorf("Caddyfile.shared lacks %q", want)
		}
	}
	// A shared host reaches its instances through their snippets only.
	if strings.Contains(shared, "reverse_proxy") {
		t.Error("Caddyfile.shared proxies outside the instances' snippets")
	}
}

// syncLog is a log destination the command and the test share.
type syncLog struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *syncLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *syncLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// waitFor polls done for up to ten seconds.
func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	for start := time.Now(); !done(); time.Sleep(5 * time.Millisecond) {
		if time.Since(start) > 10*time.Second {
			t.Fatal("timed out waiting for", what)
		}
	}
}

// notifications records, in order, what the command tells systemd.
type notifications struct {
	mu     sync.Mutex
	states []string
}

func (n *notifications) all() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.states)
}

// recordNotifications replaces sd_notify until the test ends.
func recordNotifications(t *testing.T) *notifications {
	t.Helper()
	n := &notifications{}
	previous := sdNotify
	sdNotify = func(state string) error {
		n.mu.Lock()
		n.states = append(n.states, state)
		n.mu.Unlock()
		return nil
	}
	t.Cleanup(func() { sdNotify = previous })
	return n
}

// shortGrace makes the shutdown's windows short until the test ends.
func shortGrace(t *testing.T, grace, cancel time.Duration) {
	t.Helper()
	previousGrace, previousCancel := amlShutdownGrace, amlCancelGrace
	amlShutdownGrace, amlCancelGrace = grace, cancel
	t.Cleanup(func() { amlShutdownGrace, amlCancelGrace = previousGrace, previousCancel })
}

// modelEndpoint stands in for DeepSeek and JEV (their testEndpoint): every
// request it reads waits until its client leaves or the endpoint is released,
// then is answered 503.
type modelEndpoint struct {
	*httptest.Server
	requests atomic.Int64
	release  chan struct{}
	once     sync.Once
}

func newModelEndpoint(t *testing.T) *modelEndpoint {
	t.Helper()
	e := &modelEndpoint{release: make(chan struct{})}
	e.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		e.requests.Add(1)
		select {
		case <-r.Context().Done():
		case <-e.release:
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(func() {
		e.free()
		e.Close()
	})
	return e
}

// free answers the waiting requests and those to come.
func (e *modelEndpoint) free() { e.once.Do(func() { close(e.release) }) }

// amlTestConfig writes a memory configuration whose planner (DeepSeek) and
// judge (JEV), and consolidator when edit adds one, call endpoint; the
// budget file is the test's own.
func amlTestConfig(t *testing.T, endpoint string, edit func(*memoryagent.Config)) string {
	t.Helper()
	t.Setenv("AML_TEST_DEEPSEEK", "test-only-placeholder")
	t.Setenv("AML_TEST_JEV", "test-only-placeholder")
	dir := t.TempDir()
	config := memoryagent.Config{
		Version:    1,
		Strategy:   "simple",
		View:       &memoryagent.ViewConfig{MaxItems: 100, MaxCharacters: 240000, Order: "time"},
		Planner:    &memoryagent.ModelConfig{Kind: "deepseek", Model: "deepseek-flash", KeyEnv: "AML_TEST_DEEPSEEK", TestEndpoint: endpoint},
		Judge:      &memoryagent.ModelConfig{Kind: "jev", Model: "jev-1.13.0", KeyEnv: "AML_TEST_JEV", TestEndpoint: endpoint},
		BudgetFile: filepath.Join(dir, "model-budget.db"),
	}
	if edit != nil {
		edit(&config)
	}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "memory-config.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// amlServeRun is an aml-serve command the test runs until it stops it.
type amlServeRun struct {
	root   string
	log    *syncLog
	cancel context.CancelFunc
	done   chan error
	once   sync.Once
	err    error
}

// startAMLServe runs aml-serve on a loopback port of its own, over a new
// data root, with configPath and args; the test's end stops it.
func startAMLServe(t *testing.T, configPath string, args ...string) *amlServeRun {
	t.Helper()
	run := newAMLServeRun(t)
	run.start(t, configPath, args...)
	return run
}

// newAMLServeRun is an aml-serve run not started yet: its data root and log
// exist.
func newAMLServeRun(t *testing.T) *amlServeRun {
	t.Helper()
	return &amlServeRun{root: t.TempDir(), log: &syncLog{}, done: make(chan error, 1)}
}

// start starts the run's command; the test's end stops it.
func (run *amlServeRun) start(t *testing.T, configPath string, args ...string) {
	t.Helper()
	t.Setenv("AML_TEST_KEY", testAMLKey)
	command := newAMLServeCommand()
	command.SetArgs(append([]string{"--listen", "127.0.0.1:0", "--data-root", run.root, "--memory-config", configPath, "--auth-key-env", "AML_TEST_KEY"}, args...))
	command.SilenceUsage, command.SilenceErrors = true, true
	command.SetOut(io.Discard)
	command.SetErr(run.log)
	ctx, cancel := context.WithCancel(context.Background())
	run.cancel = cancel
	go func() { run.done <- command.ExecuteContext(ctx) }()
	t.Cleanup(func() { run.stop(t) })
}

const testAMLKey = "test-aml-key"

// stop stops the command as SIGTERM does and returns its error.
func (r *amlServeRun) stop(t *testing.T) error {
	t.Helper()
	r.once.Do(func() {
		r.cancel()
		select {
		case r.err = <-r.done:
		case <-time.After(30 * time.Second):
			t.Fatal("aml-serve did not stop")
		}
	})
	return r.err
}

var listeningOn = regexp.MustCompile(`aml-serve (admin )?listening on (127\.0\.0\.1:\d+)`)

// address is the address the command's public (or admin) listener is bound
// to, as it logged it.
func (r *amlServeRun) address(t *testing.T, admin bool) string {
	t.Helper()
	var addr string
	waitFor(t, "the listening line", func() bool {
		for _, match := range listeningOn.FindAllStringSubmatch(r.log.String(), -1) {
			if (match[1] != "") == admin {
				addr = match[2]
			}
		}
		return addr != ""
	})
	return "http://" + addr
}

// post sends body to the public listener with the API key. It waits for the
// listening line, which fails the test when it never comes: call it on the
// test's goroutine only, and postTo on any other (the final review's parked
// T9-g).
func (r *amlServeRun) post(t *testing.T, path string, body any) (int, string) {
	t.Helper()
	return postTo(r.address(t, false), path, body)
}

// postTo sends body to base+path with the API key. It fails no test, so any
// goroutine may call it: a request that cannot be made or sent answers 0,
// with its error as the body.
func postTo(base, path string, body any) (int, string) {
	raw, err := json.Marshal(body)
	if err != nil {
		return 0, err.Error()
	}
	request, err := http.NewRequest(http.MethodPost, base+path, bytes.NewReader(raw))
	if err != nil {
		return 0, err.Error()
	}
	request.Header.Set("Authorization", "Bearer "+testAMLKey)
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		return 0, err.Error()
	}
	defer response.Body.Close()
	answer, _ := io.ReadAll(response.Body)
	return response.StatusCode, string(answer)
}

// get fetches url and returns its status.
func get(t *testing.T, url string) int {
	t.Helper()
	response, err := (&http.Client{Timeout: 10 * time.Second}).Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	io.Copy(io.Discard, response.Body)
	return response.StatusCode
}

func noteAdd(requestID, userID string) aml.AddRequest {
	return aml.AddRequest{RequestID: requestID, UserID: userID, SessionID: "s1", Messages: []aml.Message{
		{Role: "user", Content: "I went to the LGBTQ support group yesterday, Caroline here; everyone shared their stories and it was powerful."},
		{Role: "assistant", Content: "That sounds meaningful. How did it feel to be there with the others?"},
	}}
}

func searchRequest(userID string) aml.SearchRequest {
	return aml.SearchRequest{Query: "When did Caroline go to the LGBTQ support group?", UserID: userID, TopK: 10}
}

// healthAt is the status the public /health answers within a second, at the
// address the log names; 0 when it does not answer.
func healthAt(log string) int {
	for _, match := range listeningOn.FindAllStringSubmatch(log, -1) {
		if match[1] != "" {
			continue
		}
		response, err := (&http.Client{Timeout: time.Second}).Get("http://" + match[2] + "/health")
		if err != nil {
			return 0
		}
		response.Body.Close()
		return response.StatusCode
	}
	return 0
}

// lockFree reports whether the data root's lock is free: a test can take it.
func lockFree(t *testing.T, root string) bool {
	t.Helper()
	lock := flock.New(filepath.Join(root, ".aml-serve.lock"))
	locked, err := lock.TryLock()
	if err != nil {
		t.Fatal(err)
	}
	if locked {
		lock.Unlock()
	}
	return locked
}

// The process starts its components in order and tells systemd READY=1 once
// the public listener serves, and the admin listener too when its address
// bound (R91); a shutdown with nothing in flight tells it STOPPING=1, closes
// everything and exits 0, giving the lock back.
func TestAMLServeServesUntilStopped(t *testing.T) {
	notified := recordNotifications(t)
	// When READY=1 is sent, the public server already answers.
	run := newAMLServeRun(t)
	readyHealth := make(chan int, 1)
	record := sdNotify
	sdNotify = func(state string) error {
		if state == "READY=1" {
			readyHealth <- healthAt(run.log.String())
		}
		return record(state)
	}
	endpoint := newModelEndpoint(t)
	run.start(t, amlTestConfig(t, endpoint.URL, nil), "--admin-listen", "127.0.0.1:0", "--log-format", "json")
	waitFor(t, "READY=1", func() bool { return slices.Contains(notified.all(), "READY=1") })
	if code := <-readyHealth; code != http.StatusOK {
		t.Fatal("public /health when READY=1 was sent:", code)
	}
	public, admin := run.address(t, false), run.address(t, true)
	if public == admin {
		t.Fatal("one listener for both", public)
	}
	if code := get(t, public+"/health"); code != http.StatusOK {
		t.Fatal("public /health", code)
	}
	for _, path := range []string{"/metrics", "/admin/status"} {
		if code := get(t, admin+path); code != http.StatusOK {
			t.Fatal("admin", path, code)
		}
	}
	if code := get(t, public+"/metrics"); code != http.StatusNotFound {
		t.Fatal("the public listener serves /metrics", code)
	}
	if code, body := run.post(t, "/add", noteAdd("r1", "u1")); code != http.StatusOK {
		t.Fatal(code, body)
	}
	if lockFree(t, run.root) {
		t.Fatal("the data root is not locked while serving")
	}
	if err := run.stop(t); err != nil {
		t.Fatal(err, run.log.String())
	}
	if got := notified.all(); !slices.Equal(got, []string{"READY=1", "STOPPING=1"}) {
		t.Fatal(got)
	}
	if !lockFree(t, run.root) {
		t.Fatal("the lock was not given back")
	}
	// --log-format json: every line is a JSON object.
	for _, line := range strings.Split(strings.TrimSpace(run.log.String()), "\n") {
		if !json.Valid([]byte(line)) {
			t.Fatal("not JSON:", line)
		}
	}
	if strings.Contains(run.log.String(), "cancel") || endpoint.requests.Load() != 0 {
		t.Fatal(endpoint.requests.Load(), run.log.String())
	}
}

// getWith fetches url with token as its bearer token (none when empty) and
// returns its status and body.
func getWith(t *testing.T, url, token string) (int, string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return response.StatusCode, string(body)
}

// With --admin-token-env the admin listener answers only the requests that
// hold the token (the profiler and an unknown path included), tells which
// build runs, and the token is nowhere in the command's log. The public
// listener does not notice.
func TestAMLServeAdminToken(t *testing.T) {
	notified := recordNotifications(t)
	endpoint := newModelEndpoint(t)
	const token = "aml-serve-e2e-admin-token-0123456789"
	t.Setenv("AML_TEST_ADMIN_TOKEN", token)
	run := startAMLServe(t, amlTestConfig(t, endpoint.URL, nil), "--admin-listen", "127.0.0.1:0", "--admin-token-env", "AML_TEST_ADMIN_TOKEN")
	waitFor(t, "READY=1", func() bool { return slices.Contains(notified.all(), "READY=1") })
	public, admin := run.address(t, false), run.address(t, true)

	for _, path := range []string{"/metrics", "/ready", "/admin/status", "/admin/v1/stores", "/debug/pprof/", "/no-such-route"} {
		for _, sent := range []string{"", "wrong-" + token, token[:len(token)-1]} {
			if code, body := getWith(t, admin+path, sent); code != http.StatusUnauthorized || !strings.Contains(body, "missing or invalid admin token") {
				t.Errorf("%s with token %q: %d %s", path, sent, code, body)
			}
		}
	}
	for _, path := range []string{"/metrics", "/admin/status", "/admin/v1/stores", "/debug/pprof/"} {
		if code, body := getWith(t, admin+path, token); code != http.StatusOK {
			t.Errorf("%s with the token: %d %.80s", path, code, body)
		}
	}
	// /ready says yes or no by the disk, but never 401.
	if code, _ := getWith(t, admin+"/ready", token); code != http.StatusOK && code != http.StatusServiceUnavailable {
		t.Errorf("/ready with the token: %d", code)
	}
	_, body := getWith(t, admin+"/admin/status", token)
	var status aml.AdminStatus
	if err := json.Unmarshal([]byte(body), &status); err != nil || status.Version != version {
		t.Errorf("/admin/status says version %q, want %q: %v", status.Version, version, err)
	}
	_, body = getWith(t, admin+"/metrics", token)
	if !strings.Contains(body, "mnemon_aml_admin_auth_failures_total 18\n") {
		t.Error("the failures were not counted:", body)
	}

	// The data plane is as it was: no token on the public listener.
	if code := get(t, public+"/health"); code != http.StatusOK {
		t.Error("public /health", code)
	}
	if code, body := run.post(t, "/add", noteAdd("r1", "u1")); code != http.StatusOK {
		t.Error("public /add", code, body)
	}
	if err := run.stop(t); err != nil {
		t.Fatal(err, run.log.String())
	}
	if strings.Contains(run.log.String(), token) {
		t.Error("the log shows the admin token:", run.log.String())
	}
}

// A Search still running when the grace ends is canceled: it answers 503,
// which the platform retries, and the command fails (exit status 1), saying
// what it cut.
func TestShutdownExitsNonZeroWhenRequestsCancelled(t *testing.T) {
	shortGrace(t, 200*time.Millisecond, 5*time.Second)
	notified := recordNotifications(t)
	endpoint := newModelEndpoint(t)
	run := startAMLServe(t, amlTestConfig(t, endpoint.URL, nil))
	waitFor(t, "READY=1", func() bool { return slices.Contains(notified.all(), "READY=1") })
	if code, body := run.post(t, "/add", noteAdd("r1", "u1")); code != http.StatusOK {
		t.Fatal(code, body)
	}
	type answer struct {
		code int
		body string
	}
	searched := make(chan answer, 1)
	public := run.address(t, false) // on the test's goroutine: it may fail the test
	go func() {
		code, body := postTo(public, "/search", searchRequest("u1"))
		searched <- answer{code, body}
	}()
	// The Search waits on its planner.
	waitFor(t, "the planner's request", func() bool { return endpoint.requests.Load() > 0 })
	start := time.Now()
	err := run.stop(t)
	// It says how many it canceled (the final review's parked T9-e).
	if err == nil || !strings.Contains(err.Error(), "canceled at the end of the 200ms grace: 1 request still running") {
		t.Fatal(err, run.log.String())
	}
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond || elapsed > 5*time.Second {
		t.Fatal("stopped after", elapsed)
	}
	if got := <-searched; got.code != http.StatusServiceUnavailable || !strings.Contains(got.body, "request canceled") {
		t.Fatal(got)
	}
	if !strings.Contains(run.log.String(), "the 200ms grace has ended with 1 request still running; canceling them") {
		t.Fatal(run.log.String())
	}
	if got := notified.all(); !slices.Equal(got, []string{"READY=1", "STOPPING=1"}) {
		t.Fatal(got)
	}
	if !lockFree(t, run.root) {
		t.Fatal("the lock was not given back")
	}
}

// A background consolidation job still running when the grace ends is cut,
// and the command fails, saying so; the job's user resumes after a restart.
func TestShutdownCutsConsolidationJobsPastTheGrace(t *testing.T) {
	shortGrace(t, 200*time.Millisecond, 5*time.Second)
	notified := recordNotifications(t)
	endpoint := newModelEndpoint(t)
	config := amlTestConfig(t, endpoint.URL, func(config *memoryagent.Config) {
		config.Consolidation = &memoryagent.ConsolidationConfig{Mode: "timelines", BatchCharacters: 300}
		config.Consolidator = &memoryagent.ModelConfig{Kind: "deepseek", Model: "deepseek-flash", KeyEnv: "AML_TEST_DEEPSEEK", TestEndpoint: endpoint.URL}
	})
	run := startAMLServe(t, config, "--consolidate", "background", "--consolidation-workers", "1")
	waitFor(t, "READY=1", func() bool { return slices.Contains(notified.all(), "READY=1") })
	for i := range 4 {
		if code, body := run.post(t, "/add", noteAdd(fmt.Sprintf("r%d", i), "u1")); code != http.StatusOK {
			t.Fatal(code, body)
		}
	}
	// A worker's job waits on the consolidator.
	waitFor(t, "the consolidator's request", func() bool { return endpoint.requests.Load() > 0 })
	err := run.stop(t)
	if err == nil || !strings.Contains(err.Error(), "cut at the end of the 200ms grace: consolidation jobs still running: 1") || strings.Contains(err.Error(), "requests") {
		t.Fatal(err, run.log.String())
	}
	if !strings.Contains(run.log.String(), "the 200ms grace has ended with consolidation jobs still running: 1; cutting them: their saved batches stay, and they resume after a restart") {
		t.Fatal(run.log.String())
	}
	if !lockFree(t, run.root) {
		t.Fatal("the lock was not given back")
	}
}

// The scheduler drains as the shutdown starts: a background Add waiting at
// the lag bound answers 200 at once (its records are stored), not at the end
// of the grace, and is not counted as canceled; the job is cut later.
func TestShutdownAnswersAddsWaitingAtTheBound(t *testing.T) {
	shortGrace(t, 2*time.Second, 5*time.Second)
	notified := recordNotifications(t)
	endpoint := newModelEndpoint(t)
	// A job's window is one batch of 300 characters, the lag bound.
	config := amlTestConfig(t, endpoint.URL, func(config *memoryagent.Config) {
		config.Consolidation = &memoryagent.ConsolidationConfig{Mode: "timelines", BatchCharacters: 300, MaxBatches: 1}
		config.Consolidator = &memoryagent.ModelConfig{Kind: "deepseek", Model: "deepseek-flash", KeyEnv: "AML_TEST_DEEPSEEK", TestEndpoint: endpoint.URL}
	})
	run := startAMLServe(t, config, "--consolidate", "background")
	waitFor(t, "READY=1", func() bool { return slices.Contains(notified.all(), "READY=1") })
	if code, body := run.post(t, "/add", noteAdd("r0", "u1")); code != http.StatusOK {
		t.Fatal(code, body)
	}
	// Past the bound, the next Add waits for the job, whose fold waits at
	// the endpoint.
	type answer struct {
		code int
		at   time.Time
	}
	added := make(chan answer, 1)
	public := run.address(t, false) // on the test's goroutine: it may fail the test
	go func() {
		code, _ := postTo(public, "/add", noteAdd("r1", "u1"))
		added <- answer{code, time.Now()}
	}()
	waitFor(t, "the consolidator's request", func() bool { return endpoint.requests.Load() > 0 })
	select {
	case got := <-added:
		t.Fatal("the Add did not wait at the bound", got.code)
	case <-time.After(100 * time.Millisecond):
	}
	start := time.Now()
	err := run.stop(t)
	got := <-added
	if got.code != http.StatusOK || got.at.Sub(start) > time.Second {
		t.Fatal("the waiting Add answered", got.code, "after", got.at.Sub(start))
	}
	if err == nil || !strings.Contains(err.Error(), "consolidation jobs still running: 1") || strings.Contains(err.Error(), "requests") {
		t.Fatal(err, run.log.String())
	}
}

// The supervisor starts a component only once the one before is ready, fails
// one that returns before it is ready, ends failed at the first failure, and
// waits for the components until a deadline.
func TestSupervisorStartsInOrder(t *testing.T) {
	var mu sync.Mutex
	order := []string{}
	note := func(event string) {
		mu.Lock()
		order = append(order, event)
		mu.Unlock()
	}
	sup := newSupervisor()
	release := make(chan struct{})
	serving := func(name string, delay time.Duration) func(ready func()) error {
		return func(ready func()) error {
			time.Sleep(delay)
			note(name + " ready")
			ready()
			<-release
			return nil
		}
	}
	if err := sup.start("first", serving("first", 20*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	note("first started")
	if err := sup.start("second", serving("second", 0)); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if !slices.Equal(order, []string{"first ready", "first started", "second ready"}) {
		t.Fatal(order)
	}
	mu.Unlock()
	if sup.failed.Err() != nil || sup.err() != nil {
		t.Fatal("failed before any failure", sup.err())
	}
	if err := sup.start("early", func(func()) error { return nil }); err == nil || err.Error() != "early: it stopped before it was ready" {
		t.Fatal(err)
	}
	if err := sup.start("broken", func(func()) error { return errors.New("address in use") }); err == nil || err.Error() != "broken: address in use" {
		t.Fatal(err)
	}
	select {
	case <-sup.failed.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("a failure did not end failed")
	}
	if err := sup.err(); err == nil || err.Error() != "early: it stopped before it was ready" {
		t.Fatal("the first failure", err)
	}
	if stuck := sup.wait(time.Now().Add(20 * time.Millisecond)); !slices.Equal(stuck, []string{"first", "second"}) {
		t.Fatal(stuck)
	}
	close(release)
	if stuck := sup.wait(time.Now().Add(5 * time.Second)); stuck != nil {
		t.Fatal(stuck)
	}
}

// R90: closing the models waits for their requests in flight, abandoned ones
// included, until the end of the shutdown's windows, then cuts those still
// running. Each settles its reservation at its input estimate before the
// command returns, within a few seconds of the windows (TimeoutStopSec leaves
// it more), non-zero, saying what it cut: nothing stays held, and the budget
// file is closed. How many it cuts, one or two, is not the test's to say
// (the re-review of the final fix's N1): see the comment at the Search.
func TestShutdownCutsAbandonedModelRequests(t *testing.T) {
	shortGrace(t, 100*time.Millisecond, 400*time.Millisecond)
	notified := recordNotifications(t)
	endpoint := newModelEndpoint(t)
	// A planner attempt stops waiting after one second and is not retried;
	// its request runs on toward the minute's hard timeout.
	configPath := amlTestConfig(t, endpoint.URL, func(config *memoryagent.Config) {
		config.Planner.TimeoutSeconds, config.Planner.AttemptTimeoutSeconds, config.Planner.MaxAttempts = 60, 1, 1
	})
	run := startAMLServe(t, configPath)
	waitFor(t, "READY=1", func() bool { return slices.Contains(notified.all(), "READY=1") })
	if code, body := run.post(t, "/add", noteAdd("r1", "u1")); code != http.StatusOK {
		t.Fatal(code, body)
	}
	// The planner fails, so the Search answers the raw ranking. Its two
	// planner calls, the cue plan and the need plan, send their requests at
	// once, and they cancel each other when one fails (simple_plan.go, plan).
	// When the first attempt's second is up, the other call may still be
	// waiting: it is canceled with its plan, cuts its own request and settles
	// it at its estimate before the shutdown, so one request is left
	// abandoned. If its own second is up first, both are, waiting at the
	// endpoint. Which it is, is a race between two timers, so the close cuts
	// one request or two.
	if code, body := run.post(t, "/search", searchRequest("u1")); code != http.StatusOK {
		t.Fatal(code, body)
	}
	if n := endpoint.requests.Load(); n != 2 {
		t.Fatal("planner requests", n)
	}
	start := time.Now()
	err := run.stop(t)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatal("the shutdown waited for the abandoned requests", elapsed)
	}
	const cut = `deepseek: cut [12] model requests? still in flight at the close's deadline: their reservations settled at their input estimates`
	if err == nil || !regexp.MustCompile(cut).MatchString(err.Error()) || strings.Contains(err.Error(), "did not finish") || strings.Contains(err.Error(), "requests still running") {
		t.Fatal(err, run.log.String())
	}
	if !regexp.MustCompile("aml-serve: " + cut).MatchString(run.log.String()) {
		t.Fatal(run.log.String())
	}
	if held, spent := budgetAccount(t, configPath, "deepseek"); held != 0 || spent <= 0 {
		t.Fatal("deepseek held", held, "spent", spent)
	}
}

// manualWatchdogTicker makes the watchdog's ticker one the test ticks
// itself, until the test ends: ticks are taken from the returned channel, a
// send returning once the watchdog has taken the tick; period is what the
// watchdog asked for, and stopped closes when it stops its ticker.
func manualWatchdogTicker(t *testing.T) (ticks chan time.Time, period *atomic.Int64, stopped chan struct{}) {
	t.Helper()
	ticks, period, stopped = make(chan time.Time), &atomic.Int64{}, make(chan struct{})
	previous := amlWatchdogTicker
	amlWatchdogTicker = func(d time.Duration) (<-chan time.Time, func()) {
		period.Store(int64(d))
		return ticks, sync.OnceFunc(func() { close(stopped) })
	}
	t.Cleanup(func() { amlWatchdogTicker = previous })
	return ticks, period, stopped
}

// The watchdog pings at each tick of its ticker, every half interval, while
// the liveness probe answers within a quarter interval; it skips the ping,
// and logs why, while the probe fails; and it stops its ticker and returns
// with its context. The test ticks the ticker itself (the final review's
// parked T9-b): no wall-clock cadence to miss under load.
func TestWatchdogPingsOnlyWhileAlive(t *testing.T) {
	const interval = 40 * time.Millisecond
	ticks, period, stopped := manualWatchdogTicker(t)
	var alive atomic.Bool
	alive.Store(true)
	pings := make(chan string, 100)
	log := &syncLog{}
	probe := func(ctx context.Context) error {
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > interval/4 {
			t.Error("a probe without its quarter-interval deadline")
		}
		if !alive.Load() {
			return errors.New("stuck")
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		amlWatchdog(ctx, interval, probe, func(state string) { pings <- state }, slog.New(slog.NewTextHandler(log, nil)))
		close(done)
	}()
	ping := func(what string) {
		t.Helper()
		select {
		case state := <-pings:
			if state != "WATCHDOG=1" {
				t.Fatal(what, state)
			}
		case <-time.After(5 * time.Second):
			t.Fatal(what, "no ping")
		}
	}
	ticks <- time.Now()
	ping("the first tick:")
	if got := time.Duration(period.Load()); got != interval/2 {
		t.Fatal("the watchdog ticks every", got, "want", interval/2)
	}
	// While the probe fails: no ping. A tick is taken only once the one
	// before was handled, so after two ticks the first is done.
	alive.Store(false)
	ticks <- time.Now()
	ticks <- time.Now()
	if len(pings) != 0 || !strings.Contains(log.String(), "aml-serve watchdog: no ping: stuck") {
		t.Fatal("pings while the probe fails:", len(pings), log.String())
	}
	alive.Store(true)
	ticks <- time.Now()
	ping("once the probe passes again:")
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the watchdog did not stop")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("the watchdog's ticker was not stopped")
	}
}

// Parked T9-a (final review): the watchdog stops, and has stopped, before
// systemd is told STOPPING=1, so no ping can follow it: when STOPPING=1 is
// sent, the watchdog takes no tick any more.
func TestWatchdogStopsBeforeStopping(t *testing.T) {
	ticks, _, _ := manualWatchdogTicker(t)
	t.Setenv("WATCHDOG_USEC", "2000000")
	var mu sync.Mutex
	states := []string{}
	tickedAtStopping := false
	previous := sdNotify
	sdNotify = func(state string) error {
		mu.Lock()
		states = append(states, state)
		mu.Unlock()
		if state == "STOPPING=1" {
			select {
			case ticks <- time.Now(): // the watchdog still runs: its ping follows
				tickedAtStopping = true
				time.Sleep(200 * time.Millisecond)
			case <-time.After(200 * time.Millisecond):
			}
		}
		return nil
	}
	t.Cleanup(func() { sdNotify = previous })
	notified := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(states)
	}
	endpoint := newModelEndpoint(t)
	run := startAMLServe(t, amlTestConfig(t, endpoint.URL, nil))
	waitFor(t, "READY=1", func() bool { return slices.Contains(notified(), "READY=1") })
	ticks <- time.Now()
	waitFor(t, "a ping", func() bool { return slices.Contains(notified(), "WATCHDOG=1") })
	if err := run.stop(t); err != nil {
		t.Fatal(err, run.log.String())
	}
	if got := notified(); tickedAtStopping || !slices.Equal(got, []string{"READY=1", "WATCHDOG=1", "STOPPING=1"}) {
		t.Fatal("the watchdog ran when STOPPING=1 was sent:", tickedAtStopping, got)
	}
}

// The liveness probe passes while the public handler answers, Run's heartbeat
// is recent and the budget file answers; it touches no model.
func TestLivenessProbe(t *testing.T) {
	endpoint := newModelEndpoint(t)
	config, err := readMemoryConfig(amlTestConfig(t, endpoint.URL, nil))
	if err != nil {
		t.Fatal(err)
	}
	server, err := aml.NewServer(aml.ServerConfig{DataRoot: t.TempDir(), MemoryConfig: config, AuthKey: "k", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	ctx := context.Background()
	if err := amlLive(ctx, server, time.Minute); err == nil || !strings.Contains(err.Error(), "the background work has not ticked") {
		t.Fatal(err)
	}
	runCtx, stopRun := context.WithCancel(ctx)
	ready, done := make(chan struct{}), make(chan error, 1)
	go func() { done <- server.Run(runCtx, func() { close(ready) }) }()
	<-ready
	if err := amlLive(ctx, server, time.Minute); err != nil {
		t.Fatal(err)
	}
	// Once Run has stopped, the heartbeat ages past the interval.
	stopRun()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := amlLive(ctx, server, 10*time.Millisecond); err == nil || !strings.Contains(err.Error(), "the background work last ticked") {
		t.Fatal(err)
	}
	// A closed budget file does not answer.
	server.Close()
	if err := amlLive(ctx, server, time.Minute); err == nil || !strings.Contains(err.Error(), "the budget file does not answer") {
		t.Fatal(err)
	}
	if n := endpoint.requests.Load(); n != 0 {
		t.Fatal("the probe called a model", n)
	}
}

// The admin listener's server times out slow clients but outlasts the
// profiles pprof serves.
func TestAdminServerTimeouts(t *testing.T) {
	server := amlAdminServer(http.NotFoundHandler(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if server.ReadHeaderTimeout != 10*time.Second || server.WriteTimeout != 5*time.Minute || server.IdleTimeout != 2*time.Minute {
		t.Fatal(server.ReadHeaderTimeout, server.WriteTimeout, server.IdleTimeout)
	}
}

// With systemd (Type=notify, WatchdogSec), the process tells its socket
// READY=1, then WATCHDOG=1 every half interval, then STOPPING=1: the
// environment stays set for every notification.
func TestAMLServeNotifiesSystemd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("systemd's notify socket is a Unix datagram socket")
	}
	dir, err := os.MkdirTemp("", "sd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "notify")
	socket, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: socketPath, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	states := make(chan string, 100)
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := socket.Read(buf)
			if err != nil {
				return
			}
			states <- string(buf[:n])
		}
	}()
	t.Setenv("NOTIFY_SOCKET", socketPath)
	t.Setenv("WATCHDOG_USEC", "2000000") // the shortest interval it takes: a ping every second
	endpoint := newModelEndpoint(t)
	run := startAMLServe(t, amlTestConfig(t, endpoint.URL, nil))
	next := func() string {
		select {
		case state := <-states:
			return state
		case <-time.After(10 * time.Second):
			t.Fatal("systemd was told nothing", run.log.String())
			return ""
		}
	}
	if state := next(); state != "READY=1" {
		t.Fatal(state)
	}
	for range 2 {
		if state := next(); state != "WATCHDOG=1" {
			t.Fatal(state)
		}
	}
	if err := run.stop(t); err != nil {
		t.Fatal(err)
	}
	for state := next(); state != "STOPPING=1"; state = next() {
		if state != "WATCHDOG=1" {
			t.Fatal(state)
		}
	}
	select {
	case state := <-states:
		t.Fatal("told after STOPPING=1:", state)
	case <-time.After(300 * time.Millisecond):
	}
}

// Parked T9-c (final review): a component that fails after READY=1 (here the
// public server, its listener closed under it) shuts the process down, which
// tells systemd STOPPING=1 and fails the command (exit status 1), naming the
// component.
func TestAMLServeFailsWhenAComponentFailsAfterReady(t *testing.T) {
	notified := recordNotifications(t)
	endpoint := newModelEndpoint(t)
	config, err := readMemoryConfig(amlTestConfig(t, endpoint.URL, nil))
	if err != nil {
		t.Fatal(err)
	}
	log := &syncLog{}
	logger := slog.New(slog.NewTextHandler(log, nil))
	server, err := aml.NewServer(aml.ServerConfig{DataRoot: t.TempDir(), MemoryConfig: config, AuthKey: "k", Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	process, err := newAMLProcess(server, amlServeOptions{listen: "127.0.0.1:0"}, logger, 0)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- process.serve(context.Background(), func() {}) }()
	waitFor(t, "READY=1", func() bool { return slices.Contains(notified.all(), "READY=1") })
	process.publicListener.Close()
	select {
	case err = <-served:
	case <-time.After(30 * time.Second):
		t.Fatal("the process did not stop")
	}
	if err == nil || !strings.Contains(err.Error(), "public server: ") {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "aml-serve: public server: ") || !strings.Contains(log.String(), "; shutting down") {
		t.Fatal(log.String())
	}
	if got := notified.all(); !slices.Equal(got, []string{"READY=1", "STOPPING=1"}) {
		t.Fatal(got)
	}
}

// Parked T9-c (final review): in background mode, a stop with nothing in
// flight drains the scheduler, closes everything and exits 0.
func TestAMLServeBackgroundModeStopsCleanly(t *testing.T) {
	notified := recordNotifications(t)
	endpoint := newModelEndpoint(t)
	config := amlTestConfig(t, endpoint.URL, func(config *memoryagent.Config) {
		config.Consolidation = &memoryagent.ConsolidationConfig{Mode: "timelines", BatchCharacters: 50000}
		config.Consolidator = &memoryagent.ModelConfig{Kind: "deepseek", Model: "deepseek-flash", KeyEnv: "AML_TEST_DEEPSEEK", TestEndpoint: endpoint.URL}
	})
	run := startAMLServe(t, config, "--consolidate", "background")
	waitFor(t, "READY=1", func() bool { return slices.Contains(notified.all(), "READY=1") })
	if code, body := run.post(t, "/add", noteAdd("r1", "u1")); code != http.StatusOK {
		t.Fatal(code, body)
	}
	if err := run.stop(t); err != nil {
		t.Fatal(err, run.log.String())
	}
	if got := notified.all(); !slices.Equal(got, []string{"READY=1", "STOPPING=1"}) {
		t.Fatal(got)
	}
	if !lockFree(t, run.root) || endpoint.requests.Load() != 0 {
		t.Fatal("the lock, or a model call:", lockFree(t, run.root), endpoint.requests.Load())
	}
}
