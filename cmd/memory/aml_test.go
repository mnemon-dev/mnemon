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
		"max-concurrent-adds": "0", "max-concurrent-searches": "0", "log-format": "text",
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

// The admin endpoints have no authentication: their listener must be a
// loopback address, given as an IP address and a port.
func TestAdminListenMustBeLoopback(t *testing.T) {
	for _, addr := range []string{"", "127.0.0.1:8091", "127.0.0.1:0", "127.0.0.2:8091", "[::1]:8091"} {
		if options, err := amlFlags(t, "--admin-listen", addr); err != nil || options.adminListen != addr {
			t.Fatal(addr, options.adminListen, err)
		}
	}
	for _, addr := range []string{":8091", "0.0.0.0:8091", "[::]:8091", "192.168.1.10:8091", "10.0.0.1:0", "[fe80::1]:8091", "localhost:8091", "example.com:8091", "127.0.0.1", "127.0.0.1:"} {
		_, err := amlFlags(t, "--admin-listen", addr)
		if err == nil || !strings.Contains(err.Error(), "--admin-listen must be a loopback IP address and port") {
			t.Fatal(addr, err)
		}
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

// The systemd unit and the Caddyfile must name flags, paths and a port the
// command really has.
func TestDeployKitMatchesTheCommand(t *testing.T) {
	unit, err := os.ReadFile(deployFile(t, "aml-serve.service"))
	if err != nil {
		t.Fatal(err)
	}
	var execStart string
	for _, line := range strings.Split(string(unit), "\n") {
		if value, ok := strings.CutPrefix(line, "ExecStart="); ok {
			execStart = value
		}
	}
	fields := strings.Fields(execStart)
	if len(fields) < 4 || fields[0] != "/usr/local/bin/mnemon" || fields[1] != "memory-agent" || fields[2] != "aml-serve" {
		t.Fatal("ExecStart:", execStart)
	}
	command := newAMLServeCommand()
	if err := command.ParseFlags(fields[3:]); err != nil {
		t.Fatal(err)
	}
	for flag, value := range map[string]string{
		"listen":             "127.0.0.1:8090",
		"data-root":          "/var/lib/mnemon-aml/data",
		"memory-config":      "/etc/mnemon-aml/memory-config.json",
		"consolidate-on-add": "true",
		"max-open-stores":    "16",
		"auth-key-env":       "AML_API_KEY",
	} {
		if got := command.Flags().Lookup(flag).Value.String(); got != value {
			t.Fatalf("--%s = %q, want %q", flag, got, value)
		}
	}
	for _, line := range []string{"EnvironmentFile=/etc/mnemon-aml/env", "User=mnemon", "Restart=on-failure", "LimitNOFILE=65536"} {
		if !strings.Contains("\n"+string(unit), "\n"+line+"\n") {
			t.Fatalf("service lacks %q", line)
		}
	}

	caddy, err := os.ReadFile(deployFile(t, "Caddyfile"))
	if err != nil {
		t.Fatal(err)
	}
	// Compare with the column alignment collapsed.
	normalized := strings.Join(strings.Fields(string(caddy)), " ")
	// Upstream keep-alive stays below aml-serve's 2-minute idle timeout.
	for _, want := range []string{"reverse_proxy 127.0.0.1:8090 {", "max_size 30MiB", "read_timeout 35m", "write_timeout 35m", "keepalive 60s"} {
		if !strings.Contains(normalized, want) {
			t.Fatalf("Caddyfile lacks %q", want)
		}
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

// post sends body to the public listener with the API key.
func (r *amlServeRun) post(t *testing.T, path string, body any) (int, string) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, r.address(t, false)+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
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
// both listeners serve; a shutdown with nothing in flight tells it
// STOPPING=1, closes everything and exits 0, giving the lock back.
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
	go func() {
		code, body := run.post(t, "/search", searchRequest("u1"))
		searched <- answer{code, body}
	}()
	// The Search waits on its planner.
	waitFor(t, "the planner's request", func() bool { return endpoint.requests.Load() > 0 })
	start := time.Now()
	err := run.stop(t)
	if err == nil || !strings.Contains(err.Error(), "canceled at the end of the 200ms grace: requests still running") {
		t.Fatal(err, run.log.String())
	}
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond || elapsed > 5*time.Second {
		t.Fatal("stopped after", elapsed)
	}
	if got := <-searched; got.code != http.StatusServiceUnavailable || !strings.Contains(got.body, "request canceled") {
		t.Fatal(got)
	}
	if !strings.Contains(run.log.String(), "the 200ms grace has ended with requests still running; canceling them") {
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
	go func() {
		code, _ := run.post(t, "/add", noteAdd("r1", "u1"))
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

// Closing the models waits for their requests in flight, abandoned ones
// included, up to their hard timeout (R14); the shutdown waits no longer than
// its windows, then exits non-zero with their reservations held, and says so.
func TestShutdownBoundsCloseByTheGrace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the abandoned requests keep the budget file open past the test")
	}
	shortGrace(t, 100*time.Millisecond, 400*time.Millisecond)
	notified := recordNotifications(t)
	endpoint := newModelEndpoint(t)
	// The models close the budget file after the command has returned, once
	// the abandoned requests end: its directory goes then.
	budgetDir, err := os.MkdirTemp("", "aml-budget-")
	if err != nil {
		t.Fatal(err)
	}
	// A planner attempt stops waiting after one second and is not retried;
	// its request runs on to the minute's hard timeout.
	config := amlTestConfig(t, endpoint.URL, func(config *memoryagent.Config) {
		config.Planner.TimeoutSeconds, config.Planner.AttemptTimeoutSeconds, config.Planner.MaxAttempts = 60, 1, 1
		config.BudgetFile = filepath.Join(budgetDir, "model-budget.db")
	})
	t.Cleanup(func() {
		endpoint.free()
		waitFor(t, "the budget file to close", func() bool { return os.RemoveAll(budgetDir) == nil })
	})
	run := startAMLServe(t, config)
	waitFor(t, "READY=1", func() bool { return slices.Contains(notified.all(), "READY=1") })
	if code, body := run.post(t, "/add", noteAdd("r1", "u1")); code != http.StatusOK {
		t.Fatal(code, body)
	}
	// The planner fails, so the Search answers the raw ranking; its two
	// planner requests are abandoned, still waiting at the endpoint.
	if code, body := run.post(t, "/search", searchRequest("u1")); code != http.StatusOK {
		t.Fatal(code, body)
	}
	if n := endpoint.requests.Load(); n != 2 {
		t.Fatal("planner requests", n)
	}
	start := time.Now()
	err = run.stop(t)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatal("the shutdown waited for the abandoned requests", elapsed)
	}
	if err == nil || !strings.Contains(err.Error(), "closing the stores and models did not finish") || strings.Contains(err.Error(), "requests still running") {
		t.Fatal(err, run.log.String())
	}
	if !strings.Contains(run.log.String(), "their budget reservations stay held") {
		t.Fatal(run.log.String())
	}
}

// The watchdog pings every half interval while the liveness probe answers,
// skips its pings (and logs why) while it fails, and stops with its context.
func TestWatchdogPingsOnlyWhileAlive(t *testing.T) {
	const interval = 40 * time.Millisecond
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
	done := make(chan struct{})
	go func() {
		amlWatchdog(ctx, interval, probe, func(state string) { pings <- state }, slog.New(slog.NewTextHandler(log, nil)))
		close(done)
	}()
	select {
	case state := <-pings:
		if state != "WATCHDOG=1" {
			t.Fatal(state)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no ping")
	}
	// Every half interval: about twenty pings in ten intervals, where one
	// per interval would give ten.
	time.Sleep(10 * interval)
	if n := len(pings); n < 14 {
		t.Fatal("pings in ten intervals:", n)
	}
	alive.Store(false)
	time.Sleep(interval) // a probe under way may still ping
	for len(pings) > 0 {
		<-pings
	}
	select {
	case <-pings:
		t.Fatal("a ping while the probe fails")
	case <-time.After(3 * interval):
	}
	if !strings.Contains(log.String(), "aml-serve watchdog: no ping: stuck") {
		t.Fatal(log.String())
	}
	alive.Store(true)
	select {
	case <-pings:
	case <-time.After(5 * time.Second):
		t.Fatal("the pings did not resume")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the watchdog did not stop")
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
