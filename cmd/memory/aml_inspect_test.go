package memory

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestAMLInspectIsAMemoryAgentSubcommand(t *testing.T) {
	command, _, err := rootCmd.Find([]string{"memory-agent", "aml-inspect"})
	if err != nil || command.Name() != "aml-inspect" || command.Parent().Name() != "memory-agent" {
		t.Fatal(command, err)
	}
	for flag, value := range map[string]string{
		"data-root": "", "memory-config": "", "listen": "127.0.0.1:8092", "token-env": "", "debug-budget-file": "",
		"debug-caps": "", "max-concurrent": "2", "log-format": "text",
	} {
		if f := command.Flags().Lookup(flag); f == nil || f.DefValue != value {
			t.Fatal(flag, f)
		}
	}
}

// inspectFlags parses args, after an absolute --data-root and
// --memory-config, as aml-inspect's flags.
func inspectFlags(t *testing.T, args ...string) (amlInspectOptions, error) {
	t.Helper()
	command := newAMLInspectCommand()
	dir := t.TempDir()
	if err := command.ParseFlags(append([]string{"--data-root", dir, "--memory-config", filepath.Join(dir, "memory.json")}, args...)); err != nil {
		t.Fatal(err)
	}
	return amlInspectFlags(command)
}

// aml-inspect's --listen and --token-env follow aml-serve's admin listener's
// rules (E1), with the same cases: a loopback address needs no token, a
// private one needs it, every other is refused; the token holds 32
// characters at least, and no error shows it.
func TestInspectAuthAndListen(t *testing.T) {
	const variable = "AML_TEST_INSPECT_TOKEN"
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
		if strings.Contains(err.Error(), "admin") {
			t.Fatalf("%q names the admin listener", err)
		}
	}
	for _, addr := range loopbackListens {
		t.Setenv(variable, "")
		if options, err := inspectFlags(t, "--listen", addr); err != nil || options.listen != addr || options.token != "" {
			t.Fatal(addr, err)
		}
		t.Setenv(variable, token)
		if options, err := inspectFlags(t, "--listen", addr, "--token-env", variable); err != nil || options.token != token {
			t.Fatal(addr, err)
		}
	}
	for _, addr := range privateListens {
		_, err := inspectFlags(t, "--listen", addr)
		wantError(t, err, "--listen", "private", "--token-env", "read every user's records")
		t.Setenv(variable, token)
		if options, err := inspectFlags(t, "--listen", addr, "--token-env", variable); err != nil || options.listen != addr || options.token != token {
			t.Fatal(addr, err)
		}
	}
	for _, addr := range append([]string{""}, refusedListens...) {
		_, err := inspectFlags(t, "--listen", addr)
		wantError(t, err, "--listen must be")
		t.Setenv(variable, token)
		_, err = inspectFlags(t, "--listen", addr, "--token-env", variable)
		wantError(t, err, "--listen must be")
	}
	const missing = "AML_TEST_NO_SUCH_INSPECT_TOKEN"
	for _, addr := range []string{"127.0.0.1:8092", "10.0.0.5:8092"} {
		t.Setenv(variable, "")
		_, err := inspectFlags(t, "--listen", addr, "--token-env", variable)
		wantError(t, err, "--token-env", variable, "unset or empty", "inspect token")
		os.Unsetenv(missing)
		_, err = inspectFlags(t, "--listen", addr, "--token-env", missing)
		wantError(t, err, "--token-env", missing, "unset or empty")
		short := strings.Repeat("s", 31)
		t.Setenv(variable, short)
		_, err = inspectFlags(t, "--listen", addr, "--token-env", variable)
		wantError(t, err, "--token-env", variable, "32 characters")
		if strings.Contains(err.Error(), short) {
			t.Fatal("the error shows the token:", err)
		}
	}
}

func TestAMLInspectFlags(t *testing.T) {
	options, err := inspectFlags(t, "--debug-budget-file", "/var/lib/mnemon-aml-inspect/eval/debug-budget.db", "--debug-caps", "deepseek=5,jev=1.5", "--max-concurrent", "3", "--log-format", "json")
	if err != nil || options.debugBudget != "/var/lib/mnemon-aml-inspect/eval/debug-budget.db" || options.caps["deepseek"] != 5 || options.caps["jev"] != 1.5 ||
		len(options.caps) != 2 || options.maxConcurrent != 3 || options.logFormat != "json" {
		t.Fatalf("%+v %v", options, err)
	}
	for args, want := range map[string]string{
		"--debug-caps deepseek=5":                                "--debug-caps needs --debug-budget-file",
		"--debug-budget-file debug.db":                           "--debug-budget-file must be an absolute path",
		"--debug-budget-file /d.db --debug-caps deepseek":        `--debug-caps must be kind=amount pairs, such as deepseek=5,jev=1, not "deepseek"`,
		"--debug-budget-file /d.db --debug-caps deepseek=x":      `--debug-caps must be kind=amount pairs, such as deepseek=5,jev=1, not "deepseek=x"`,
		"--debug-budget-file /d.db --debug-caps deepseek=-1":     `--debug-caps must be kind=amount pairs, such as deepseek=5,jev=1, not "deepseek=-1"`,
		"--debug-budget-file /d.db --debug-caps jev=1,jev=2":     `--debug-caps names jev twice`,
		"--debug-budget-file /d.db --debug-caps claude=1":        `--debug-caps kind "claude": the kinds are deepseek, jev and openai`,
		"--max-concurrent 0":                                     "--max-concurrent must be at least 1",
		"--log-format yaml":                                      `--log-format must be text or json, not "yaml"`,
		"--debug-budget-file /d.db --debug-caps deepseek=5,,jev": `--debug-caps must be kind=amount pairs, such as deepseek=5,jev=1, not ""`,
	} {
		if _, err := inspectFlags(t, strings.Fields(args)...); err == nil || err.Error() != want {
			t.Errorf("%q: %v", args, err)
		}
	}
	command := newAMLInspectCommand()
	if err := command.ParseFlags([]string{"--data-root", "data", "--memory-config", "/m.json"}); err != nil {
		t.Fatal(err)
	}
	if _, err := amlInspectFlags(command); err == nil || err.Error() != "aml-inspect requires an absolute --data-root" {
		t.Fatal(err)
	}
}

var inspectListening = regexp.MustCompile(`aml-inspect listening on (127\.0\.0\.1:\d+)`)

// The command serves over a data root it never writes until it is stopped,
// with its token on every route, and exits 0.
func TestAMLInspectServesUntilStopped(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "users"), 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := amlTestConfig(t, "http://127.0.0.1:1", nil)
	token := strings.Repeat("k", 40)
	t.Setenv("AML_TEST_INSPECT_TOKEN", token)
	debug := filepath.Join(t.TempDir(), "debug-budget.db")
	command := newAMLInspectCommand()
	log := &syncLog{}
	command.SetArgs([]string{"--listen", "127.0.0.1:0", "--token-env", "AML_TEST_INSPECT_TOKEN", "--data-root", root, "--memory-config", configPath,
		"--debug-budget-file", debug, "--debug-caps", "deepseek=5,jev=1", "--log-format", "json"})
	command.SilenceUsage, command.SilenceErrors = true, true
	command.SetOut(io.Discard)
	command.SetErr(log)
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- command.ExecuteContext(ctx) }()
	var addr string
	waitFor(t, "the listening line", func() bool {
		if match := inspectListening.FindStringSubmatch(log.String()); match != nil {
			addr = match[1]
		}
		return addr != ""
	})
	get := func(path, auth string) (int, map[string]any) {
		request, _ := http.NewRequest(http.MethodGet, "http://"+addr+path, nil)
		if auth != "" {
			request.Header.Set("Authorization", "Bearer "+auth)
		}
		response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var body map[string]any
		json.NewDecoder(response.Body).Decode(&body)
		return response.StatusCode, body
	}
	if status, _ := get("/healthz", ""); status != http.StatusUnauthorized {
		t.Fatal(status)
	}
	if status, body := get("/healthz", token); status != http.StatusOK || body["ok"] != true {
		t.Fatal(status, body)
	}
	if status, body := get("/v1/info", token); status != http.StatusOK || body["modes"].(map[string]any)["full"] != true || body["version"] != version {
		t.Fatal(status, body)
	}
	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err, log.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("aml-inspect did not stop")
	}
	if strings.Contains(log.String(), token) {
		t.Fatal("the log holds the token")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatal("aml-inspect touched the data root:", entries, err)
	}
	if _, err := os.Stat(debug); err != nil {
		t.Fatal("no debug budget file", err)
	}
}

// The command refuses a debug budget file that is the configuration's own,
// before it listens.
func TestAMLInspectRefusesTheInstanceBudget(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "users"), 0o700)
	configPath := amlTestConfig(t, "http://127.0.0.1:1", nil)
	config, err := readMemoryConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	command := newAMLInspectCommand()
	command.SetArgs([]string{"--listen", "127.0.0.1:0", "--data-root", root, "--memory-config", configPath, "--debug-budget-file", config.BudgetFile, "--debug-caps", "deepseek=5,jev=1"})
	command.SilenceUsage, command.SilenceErrors = true, true
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "must differ from the configuration's budgetFile") {
		t.Fatal(err)
	}
	if _, err := os.Stat(config.BudgetFile); !os.IsNotExist(err) {
		t.Fatal("the refused command made the instance's budget file", err)
	}
}
