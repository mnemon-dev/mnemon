package setup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadJSONFileToleratesLineCommentsAndTrailingCommas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	input := `{
  // keep this comment outside strings
  "hooks": {
    "SessionStart": [
      {"command": "echo // not a comment"},
    ],
  },
}
`
	if err := os.WriteFile(path, []byte(input), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got, err := ReadJSONFile(path)
	if err != nil {
		t.Fatalf("read json file: %v", err)
	}
	hooks := got["hooks"].(map[string]any)
	sessionStart := hooks["SessionStart"].([]any)
	entry := sessionStart[0].(map[string]any)
	if entry["command"] != "echo // not a comment" {
		t.Fatalf("string content was corrupted: %#v", entry["command"])
	}
}

func TestAddClaudeHooksSelectiveReplacesExistingMnemonHooks(t *testing.T) {
	data := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{"hooks": []any{map[string]any{"command": "/old/mnemon/prime.sh"}}},
				map[string]any{"hooks": []any{map[string]any{"command": "/keep/custom.sh"}}},
			},
			"Stop": []any{
				map[string]any{"hooks": []any{map[string]any{"command": "/old/mnemon/stop.sh"}}},
			},
		},
	}

	addClaudeHooksSelective(data, "/new/hooks", HookSelection{Remind: true, Nudge: false, Compact: true})

	hooks := data["hooks"].(map[string]any)
	if len(hooks["SessionStart"].([]any)) != 2 {
		t.Fatalf("expected kept custom hook plus new prime hook: %#v", hooks["SessionStart"])
	}
	if _, ok := hooks["Stop"]; ok {
		t.Fatalf("disabled nudge should remove mnemon Stop hooks: %#v", hooks["Stop"])
	}
	if _, ok := hooks["UserPromptSubmit"]; !ok {
		t.Fatal("enabled remind hook should be registered")
	}
	if _, ok := hooks["PreCompact"]; !ok {
		t.Fatal("enabled compact hook should be registered")
	}
}

func TestAddCodexHooksReplacesExistingMnemonHooks(t *testing.T) {
	data := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{"hooks": []any{map[string]any{"command": "/old/mnemon/prime.sh"}}},
				map[string]any{"hooks": []any{map[string]any{"command": "/keep/custom.sh"}}},
			},
			"UserPromptSubmit": []any{
				map[string]any{"hooks": []any{map[string]any{"command": "/old/mnemon/user_prompt.sh"}}},
			},
			"Stop": []any{
				map[string]any{"hooks": []any{map[string]any{"command": "/old/mnemon/stop.sh"}}},
			},
		},
	}

	addCodexHooks(data, "/new/hooks")

	hooks := data["hooks"].(map[string]any)
	sessionStart := hooks["SessionStart"].([]any)
	if len(sessionStart) != 2 {
		t.Fatalf("expected kept custom hook plus new prime hook: %#v", sessionStart)
	}
	userPrompt := hooks["UserPromptSubmit"].([]any)
	if len(userPrompt) != 1 {
		t.Fatalf("expected one new remind hook: %#v", userPrompt)
	}
	stop := hooks["Stop"].([]any)
	if len(stop) != 1 {
		t.Fatalf("expected one new stop hook: %#v", stop)
	}
}
