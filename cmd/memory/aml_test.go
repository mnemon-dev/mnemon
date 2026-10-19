package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAMLServeIsAMemoryAgentSubcommand(t *testing.T) {
	command, _, err := rootCmd.Find([]string{"memory-agent", "aml-serve"})
	if err != nil || command.Name() != "aml-serve" || command.Parent().Name() != "memory-agent" {
		t.Fatal(command, err)
	}
	for flag, value := range map[string]string{"listen": "127.0.0.1:8090", "data-root": "", "memory-config": "", "auth-key-env": "AML_API_KEY", "consolidate-on-add": "false", "max-open-stores": "64"} {
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
	} {
		command := newAMLServeCommand()
		command.SetArgs(c.args)
		command.SilenceUsage, command.SilenceErrors = true, true
		if err := command.Execute(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatal(c.args, err)
		}
	}
}
