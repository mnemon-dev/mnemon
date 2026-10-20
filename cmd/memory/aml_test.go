package memory

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/aml"
	"github.com/mnemon-dev/mnemon/internal/memoryagent"
	"github.com/mnemon-dev/mnemon/internal/records"
)

func TestAMLServeIsAMemoryAgentSubcommand(t *testing.T) {
	command, _, err := rootCmd.Find([]string{"memory-agent", "aml-serve"})
	if err != nil || command.Name() != "aml-serve" || command.Parent().Name() != "memory-agent" {
		t.Fatal(command, err)
	}
	for flag, value := range map[string]string{"listen": "127.0.0.1:8090", "data-root": "", "memory-config": "", "auth-key-env": "AML_API_KEY", "consolidate-on-add": "false", "max-open-stores": "64", "search-timeout": "10m0s"} {
		if f := command.Flags().Lookup(flag); f == nil || f.DefValue != value {
			t.Fatal(flag, f)
		}
	}
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
		// The reference system delivers its View in time order.
		View:          &memoryagent.ViewConfig{MaxItems: 100, MaxCharacters: 240000, Fusion: "rrf", Order: "time"},
		Consolidation: &memoryagent.ConsolidationConfig{Mode: "timelines"},
		// DeepSeek without thinking: a thinking planner can return empty plans
		// under the 200/400-token planning caps.
		Planner: &memoryagent.ModelConfig{Kind: "deepseek", Model: "deepseek-flash", KeyEnv: "DEEPSEEK_API_KEY"},
		Judge:   &memoryagent.ModelConfig{Kind: "jev", Model: "jev-1.13.0", KeyEnv: "TYPESAFE_API_KEY"},
		// A fold of a full batch may take minutes; the default 90 s would
		// cut it off.
		Consolidator: &memoryagent.ModelConfig{Kind: "deepseek", Model: "deepseek-flash", KeyEnv: "DEEPSEEK_API_KEY", TimeoutSeconds: 600},
		Embedding: &records.EmbeddingConfig{
			URL: "http://127.0.0.1:11434", Model: "nomic-embed-text",
			QueryPrefix: "search_query: ", DocumentPrefix: "search_document: ",
		},
		BudgetFile: "/var/lib/mnemon-aml/model-budget.db",
		Budgets:    map[string]float64{"jev": 20, "deepseek": 200},
	}
	if !reflect.DeepEqual(config, want) {
		t.Fatalf("industrial config\n got %+v\nwant %+v", config, want)
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
