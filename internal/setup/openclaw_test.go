package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenClawRegisterPluginWritesSelection(t *testing.T) {
	dir := t.TempDir()
	path, err := OpenClawRegisterPlugin(dir, HookSelection{Remind: true, Nudge: false, Compact: true})
	if err != nil {
		t.Fatalf("register plugin: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}

	plugins := cfg["plugins"].(map[string]any)
	entries := plugins["entries"].(map[string]any)
	mnemon := entries["mnemon"].(map[string]any)
	config := mnemon["config"].(map[string]any)

	if mnemon["enabled"] != true {
		t.Fatalf("mnemon should be enabled: %#v", mnemon["enabled"])
	}
	if config["remind"] != true || config["nudge"] != false || config["compact"] != true {
		t.Fatalf("unexpected hook selection: %#v", config)
	}
}

func TestRemoveOpenClawPluginEntryPreservesOtherEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openclaw.json")
	input := `{
  "plugins": {
    "entries": {
      "mnemon": {"enabled": true},
      "other": {"enabled": true}
    }
  },
  "theme": "dark"
}
`
	if err := os.WriteFile(path, []byte(input), 0600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if err := removeOpenClawPluginEntry(path); err != nil {
		t.Fatalf("remove plugin entry: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse result: %v", err)
	}
	entries := cfg["plugins"].(map[string]any)["entries"].(map[string]any)
	if _, ok := entries["mnemon"]; ok {
		t.Fatal("mnemon entry should be removed")
	}
	if _, ok := entries["other"]; !ok {
		t.Fatal("other entry should be preserved")
	}
	if cfg["theme"] != "dark" {
		t.Fatalf("top-level fields should be preserved: %#v", cfg)
	}
}

func TestRemoveOpenClawPluginEntryRemovesEmptyConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openclaw.json")
	if err := os.WriteFile(path, []byte(`{"plugins":{"entries":{"mnemon":{"enabled":true}}}}`), 0600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if err := removeOpenClawPluginEntry(path); err != nil {
		t.Fatalf("remove plugin entry: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected empty config file removal, got err=%v", err)
	}
}
