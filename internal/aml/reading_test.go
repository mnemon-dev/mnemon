package aml

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
)

// layeredBudgets is a newBudgets server with layered consolidation, which
// reads by a role reading.
func layeredBudgets(t *testing.T) *byok {
	return newBudgets(t, func(config *ServerConfig) {
		config.MemoryConfig.Consolidation = &memoryagent.ConsolidationConfig{Mode: "layered"}
	})
}

// settingsJSON is the settings file as JSON fields, nil when it does not
// exist.
func settingsJSON(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	var file map[string]json.RawMessage
	if err != nil || json.Unmarshal(raw, &file) != nil {
		t.Fatalf("the settings file %s: %v", raw, err)
	}
	return file
}

func readAnswer(t *testing.T, body string) ReadStatus {
	t.Helper()
	var status ReadStatus
	if err := json.Unmarshal([]byte(body), &status); err != nil {
		t.Fatal(body, err)
	}
	return status
}

// TestRoleReadingSetAndRevert: a role reading set through the admin listener
// goes into the settings file next to the budget caps, neither change
// dropping the other, and every agent reads by it at once; a revert goes
// back to the configuration's, the file staying version 1 while it holds
// only caps and going once it holds nothing; a start reads it back.
func TestRoleReadingSetAndRevert(t *testing.T) {
	b := layeredBudgets(t)
	defaults := readingOf(memoryagent.DefaultRoleReading())
	if read := b.adminStatus(t).Read; read == nil || read.Source != "config" || !reflect.DeepEqual(read.Policy, defaults) || !reflect.DeepEqual(read.Config, defaults) {
		t.Fatalf("status read %+v", read)
	}
	okAnswer(t, b.admin("PUT", "/admin/v1/budgets", `{"deepseek":800}`))
	set := readAnswer(t, okAnswer(t, b.admin("PUT", "/admin/v1/read", `{"default":"full","roles":{" Tool ":"ignore"}}`)))
	want := Reading{Default: "full", Excerpt: 240, Roles: map[string]string{"tool": "ignore"}}
	if set.Source != "settings" || !reflect.DeepEqual(set.Policy, want) || !reflect.DeepEqual(set.Config, defaults) {
		t.Fatalf("set answered %+v", set)
	}
	if got := b.server.config.MemoryConfig.ReadOverride(); got == nil || !reflect.DeepEqual(readingOf(*got), want) {
		t.Fatal("the agents do not read by the reading set:", got)
	}
	file := settingsJSON(t, b.settingsFile())
	if string(file["version"]) != "2" || string(file["budgets"]) != `{"deepseek":800}` || !strings.Contains(string(file["read"]), `"tool":"ignore"`) {
		t.Fatalf("the settings file %v", file)
	}
	// A cap change keeps the reading, and the reading the caps.
	okAnswer(t, b.admin("PUT", "/admin/v1/budgets", `{"jev":70}`))
	if file := settingsJSON(t, b.settingsFile()); file["read"] == nil || string(file["budgets"]) != `{"deepseek":800,"jev":70}` {
		t.Fatalf("a cap change dropped the reading: %v", file)
	}
	if body := okAnswer(t, b.admin("PUT", "/admin/v1/read", `{"default":"full"}`)); !strings.Contains(body, `"policy":{"default":"full","excerpt":240,"roles":{}}`) {
		t.Fatal("an answer without roles must spell them {}:", body)
	}
	reverted := readAnswer(t, okAnswer(t, b.admin("DELETE", "/admin/v1/read", "")))
	if reverted.Source != "config" || !reflect.DeepEqual(reverted.Policy, defaults) || b.server.config.MemoryConfig.ReadOverride() != nil {
		t.Fatalf("revert answered %+v", reverted)
	}
	if file := settingsJSON(t, b.settingsFile()); string(file["version"]) != "1" || file["read"] != nil || string(file["budgets"]) != `{"deepseek":800,"jev":70}` {
		t.Fatalf("after the revert the settings file is %v", file)
	}
	okAnswer(t, b.admin("DELETE", "/admin/v1/budgets?kind=all", ""))
	if settingsJSON(t, b.settingsFile()) != nil {
		t.Fatal("an empty settings file stayed")
	}
	// A start reads the settings file's reading back.
	okAnswer(t, b.admin("PUT", "/admin/v1/read", `{"default":"excerpt","excerpt":300}`))
	config := b.config
	config.DataRoot = t.TempDir()
	config.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	again, err := NewServer(config)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if read := again.readStatus(); read.Source != "settings" || read.Policy.Default != "excerpt" || read.Policy.Excerpt != 300 || len(read.Policy.Roles) != 0 {
		t.Fatalf("a start read %+v", read)
	}
}

// TestRoleReadingRequestRules: a reading that is not one is refused naming
// its field and changes nothing; without a settings file or without
// consolidation there is nothing to set.
func TestRoleReadingRequestRules(t *testing.T) {
	b := layeredBudgets(t)
	for body, field := range map[string]string{
		`{"default":"half"}`:                                       "default",
		`{"default":"full","excerpt":39}`:                          "excerpt",
		`{"default":"full","excerpt":2001}`:                        "excerpt",
		`{"default":"full","roles":{" ":"full"}}`:                  "roles",
		`{"default":"full","roles":{"bot":"half"}}`:                "roles.bot",
		`{"default":"full","roles":{"Bot":"full","bot":"ignore"}}`: "roles.bot",
		`{"default":"full","other":1}`:                             "body",
		`{"default":1}`:                                            "body",
		`[]`:                                                       "body",
	} {
		response := b.admin("PUT", "/admin/v1/read", body)
		if response.Code != http.StatusBadRequest || strings.TrimSpace(response.Body.String()) != `{"error":"invalid","field":"`+field+`"}` {
			t.Errorf("%s: %d %s", body, response.Code, response.Body.String())
		}
	}
	if response := b.admin("PUT", "/admin/v1/read", `{"default":"full","roles":{"`+strings.Repeat("r", 5000)+`":"full"}}`); response.Code != http.StatusRequestEntityTooLarge {
		t.Error("a large body:", response.Code)
	}
	if read := b.adminStatus(t).Read; read.Source != "config" || settingsJSON(t, b.settingsFile()) != nil {
		t.Fatal("a refused reading changed something", read)
	}
	noFile := newBYOK(t, func(config *ServerConfig) {
		config.MemoryConfig.Consolidation = &memoryagent.ConsolidationConfig{Mode: "layered"}
	})
	for _, method := range []string{"PUT", "DELETE"} {
		if response := noFile.admin(method, "/admin/v1/read", `{"default":"full"}`); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "no_settings_file") {
			t.Error(method, "without a settings file:", response.Code, response.Body.String())
		}
	}
	plain := newBudgets(t, nil)
	if response := plain.admin("PUT", "/admin/v1/read", `{"default":"full"}`); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "no_consolidation") {
		t.Error("without consolidation:", response.Code, response.Body.String())
	}
	if plain.adminStatus(t).Read != nil {
		t.Error("a status without consolidation shows a reading")
	}
	// A settings file with a reading that is not one is refused at a start.
	state := t.TempDir()
	config := b.config
	config.DataRoot, config.SettingsFile = t.TempDir(), filepath.Join(state, "settings.json")
	config.ProviderKeysFile = filepath.Join(state, "provider-keys.json")
	config.MemoryConfig.BudgetFile = filepath.Join(state, "model-budget.db")
	config.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, content := range []string{
		`{"version":2,"budgets":{},"read":{"default":"half"}}`,
		`{"version":1,"budgets":{},"read":{"default":"full"}}`,
		`{"version":2,"budgets":{}}`,
	} {
		writeTestFile(t, config.SettingsFile, content, 0o600)
		if server, err := NewServer(config); err == nil {
			server.Close()
			t.Errorf("%s was accepted", content)
		}
	}
	writeTestFile(t, config.SettingsFile, `{"version":2,"budgets":{},"read":{"default":"ignore","roles":{"user":"full"}}}`, 0o600)
	server, err := NewServer(config)
	if err != nil {
		t.Fatal("a reading without caps:", err)
	}
	defer server.Close()
	if read := server.readStatus(); read.Source != "settings" || read.Policy.Default != "ignore" {
		t.Fatalf("%+v", read)
	}
}
