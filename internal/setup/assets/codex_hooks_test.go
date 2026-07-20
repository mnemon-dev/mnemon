package assets

// These tests execute the actual embedded Codex hook byte slices (the exact bytes
// `mnemon setup` installs) as real subprocesses and assert only their external
// contract: Codex requires each hook's stdout to be exactly one
// hookSpecificOutput JSON object. They deliberately do not inspect the scripts'
// internal shell logic. Both tests fail against the pre-patch plain-text hooks
// and pass once the hooks emit structured JSON.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// hookOutput is the Codex hook contract shape.
type hookOutput struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// writeExecutable writes body to dir/name as an executable and returns the path.
func writeExecutable(t *testing.T, dir, name string, body []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, body, 0o755); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

// runHook runs the embedded asset the way Codex does: the script reads stdin and
// writes the hook object to stdout. env fully replaces the environment; nil
// inherits the parent's.
func runHook(t *testing.T, script, stdin string, env []string) []byte {
	t.Helper()
	cmd := exec.Command("bash", script)
	cmd.Stdin = strings.NewReader(stdin)
	if env != nil {
		cmd.Env = env
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("hook exited non-zero: %v\nstderr: %s", err, stderr.String())
	}
	return out
}

// mustDecodeOne asserts out is exactly one JSON object (encoding/json rejects any
// non-whitespace before or after the value) carrying wantEvent, and returns it.
func mustDecodeOne(t *testing.T, out []byte, wantEvent string) hookOutput {
	t.Helper()
	var o hookOutput
	if err := json.Unmarshal(out, &o); err != nil {
		t.Fatalf("stdout is not a single JSON object: %v\nraw: %q", err, out)
	}
	if o.HookSpecificOutput.HookEventName != wantEvent {
		t.Fatalf("hookEventName = %q, want %q", o.HookSpecificOutput.HookEventName, wantEvent)
	}
	return o
}

// shSingleQuote wraps s for safe embedding in a single-quoted shell word.
func shSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// isolatedPythonBin returns a bin dir containing only a python3 symlink. A PATH
// built from it exposes python3 (the hook's sole hard dependency, already shared
// with stop.sh) while keeping the real `mnemon` off PATH, so the missing-binary
// branch is deterministic regardless of the host's install.
func isolatedPythonBin(t *testing.T) string {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found; Codex hooks require it (same dependency as stop.sh)")
	}
	bin := t.TempDir()
	if err := os.Symlink(py, filepath.Join(bin, "python3")); err != nil {
		t.Fatalf("symlink python3: %v", err)
	}
	return bin
}

// fakeMnemon writes a stub `mnemon` whose `status` prints statusStdout (empty =>
// prints nothing) and returns the bin dir holding it.
func fakeMnemon(t *testing.T, statusStdout string) string {
	t.Helper()
	bin := t.TempDir()
	body := "#!/bin/bash\n"
	if statusStdout != "" {
		body += "printf '%s' " + shSingleQuote(statusStdout) + "\n"
	}
	if err := os.WriteFile(filepath.Join(bin, "mnemon"), []byte(body), 0o755); err != nil {
		t.Fatalf("write fake mnemon: %v", err)
	}
	return bin
}

func newPromptHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".mnemon", "prompt"), 0o755); err != nil {
		t.Fatal(err)
	}
	return home
}

func writeGuide(t *testing.T, promptDir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(promptDir, "guide.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCodexUserPromptHook(t *testing.T) {
	t.Parallel()
	const wantContext = "[mnemon] Evaluate: recall needed? After responding, evaluate: remember needed?"
	script := writeExecutable(t, t.TempDir(), "user_prompt.sh", CodexUserPromptHook)

	// Codex may deliver an empty body or a JSON prompt payload; the hook must
	// consume stdin and emit the same static object either way.
	cases := []struct{ name, stdin string }{
		{"empty stdin", ""},
		{"json stdin", `{"prompt":"hello"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := mustDecodeOne(t, runHook(t, script, tc.stdin, nil), "UserPromptSubmit")
			if o.HookSpecificOutput.AdditionalContext != wantContext {
				t.Errorf("additionalContext = %q, want %q", o.HookSpecificOutput.AdditionalContext, wantContext)
			}
		})
	}
}

func TestCodexPrimeHook(t *testing.T) {
	t.Parallel()
	script := writeExecutable(t, t.TempDir(), "prime.sh", CodexPrimeHook)
	basePATH := isolatedPythonBin(t) + ":/usr/bin:/bin"

	// A guide fixture exercising every escaping hazard: quotes, backslash, tab,
	// newline, and non-ASCII. It must survive round-trip byte-for-byte.
	const guideFixture = "first \"quoted\" line\nback\\slash\ttab\nunicode: café ✓\n"

	t.Run("missing mnemon: warning + guide preserved byte-exact", func(t *testing.T) {
		home := newPromptHome(t)
		writeGuide(t, filepath.Join(home, ".mnemon", "prompt"), guideFixture)
		o := mustDecodeOne(t, runHook(t, script, "{}", []string{"HOME=" + home, "PATH=" + basePATH}), "SessionStart")
		ac := o.HookSpecificOutput.AdditionalContext
		if !strings.Contains(ac, "[mnemon] Warning: mnemon not found in PATH.") {
			t.Errorf("missing-binary warning absent: %q", ac)
		}
		if !strings.HasSuffix(ac, guideFixture) {
			t.Errorf("guide not preserved byte-exact.\n got: %q\nwant suffix: %q", ac, guideFixture)
		}
	})

	t.Run("no guide: still one valid object", func(t *testing.T) {
		home := newPromptHome(t) // prompt dir exists, no guide.md
		o := mustDecodeOne(t, runHook(t, script, "{}", []string{"HOME=" + home, "PATH=" + basePATH}), "SessionStart")
		if !strings.Contains(o.HookSpecificOutput.AdditionalContext, "mnemon not found in PATH") {
			t.Errorf("expected warning, got %q", o.HookSpecificOutput.AdditionalContext)
		}
	})

	t.Run("mnemon present, stats -> count line", func(t *testing.T) {
		home := newPromptHome(t)
		path := fakeMnemon(t, `{"total_insights": 42, "edge_count": 99}`) + ":" + basePATH
		o := mustDecodeOne(t, runHook(t, script, "{}", []string{"HOME=" + home, "PATH=" + path}), "SessionStart")
		const want = "[mnemon] Memory active (42 insights, 99 edges)."
		if !strings.Contains(o.HookSpecificOutput.AdditionalContext, want) {
			t.Errorf("stats line = %q, want contains %q", o.HookSpecificOutput.AdditionalContext, want)
		}
	})

	t.Run("mnemon present, empty status -> plain fallback", func(t *testing.T) {
		home := newPromptHome(t)
		path := fakeMnemon(t, "") + ":" + basePATH
		o := mustDecodeOne(t, runHook(t, script, "{}", []string{"HOME=" + home, "PATH=" + path}), "SessionStart")
		if !strings.Contains(o.HookSpecificOutput.AdditionalContext, "[mnemon] Memory active.") {
			t.Errorf("fallback line absent: %q", o.HookSpecificOutput.AdditionalContext)
		}
	})

	t.Run("MNEMON_DATA_DIR override selects that prompt dir", func(t *testing.T) {
		home := newPromptHome(t) // legacy dir exists but has no guide
		dataDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dataDir, "prompt"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeGuide(t, filepath.Join(dataDir, "prompt"), "DATADIR-GUIDE\n")
		o := mustDecodeOne(t, runHook(t, script, "{}", []string{"HOME=" + home, "MNEMON_DATA_DIR=" + dataDir, "PATH=" + basePATH}), "SessionStart")
		if !strings.Contains(o.HookSpecificOutput.AdditionalContext, "DATADIR-GUIDE") {
			t.Errorf("MNEMON_DATA_DIR guide not used: %q", o.HookSpecificOutput.AdditionalContext)
		}
	})

	t.Run("legacy fallback when data dir has no guide", func(t *testing.T) {
		home := newPromptHome(t)
		writeGuide(t, filepath.Join(home, ".mnemon", "prompt"), "LEGACY-GUIDE\n")
		dataDir := t.TempDir() // set, but no prompt/guide.md inside
		o := mustDecodeOne(t, runHook(t, script, "{}", []string{"HOME=" + home, "MNEMON_DATA_DIR=" + dataDir, "PATH=" + basePATH}), "SessionStart")
		if !strings.Contains(o.HookSpecificOutput.AdditionalContext, "LEGACY-GUIDE") {
			t.Errorf("legacy fallback guide not used: %q", o.HookSpecificOutput.AdditionalContext)
		}
	})
}
