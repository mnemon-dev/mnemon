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
	"unicode/utf8"
)

// hookOutput is the Codex hook contract shape.
type hookOutput struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

const missingWarn = "[mnemon] Warning: mnemon not found in PATH.\n"

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
// inherits the parent's. A non-zero exit fails the test.
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

// isolatedBin returns a bin dir containing symlinks to python3 and the coreutils
// prime.sh invokes (sed/head/cat) and nothing else. A PATH built from it alone
// exposes exactly the hook's real dependencies while guaranteeing `mnemon` is
// absent, so the missing-binary branch is deterministic no matter what the host
// has installed (no /usr/bin:/bin leak).
func isolatedBin(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	for _, tool := range []string{"python3", "sed", "head", "cat"} {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s not found; Codex hooks require it", tool)
		}
		if err := os.Symlink(p, filepath.Join(bin, tool)); err != nil {
			t.Fatalf("symlink %s: %v", tool, err)
		}
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
			t.Parallel()
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
	isoPATH := isolatedBin(t)

	// assertExact checks the whole additionalContext byte-for-byte — stronger than
	// Contains, so injected or duplicated text cannot slip through.
	assertExact := func(t *testing.T, out []byte, want string) hookOutput {
		t.Helper()
		o := mustDecodeOne(t, out, "SessionStart")
		if o.HookSpecificOutput.AdditionalContext != want {
			t.Errorf("additionalContext =\n  %q\nwant\n  %q", o.HookSpecificOutput.AdditionalContext, want)
		}
		return o
	}

	t.Run("missing mnemon: warning + guide, exact", func(t *testing.T) {
		t.Parallel()
		const guide = "first \"quoted\" line\nback\\slash\ttab\nunicode: café ✓\n"
		home := newPromptHome(t)
		writeGuide(t, filepath.Join(home, ".mnemon", "prompt"), guide)
		out := runHook(t, script, "{}", []string{"HOME=" + home, "PATH=" + isoPATH})
		assertExact(t, out, missingWarn+guide)
	})

	t.Run("no guide: warning only, exact", func(t *testing.T) {
		t.Parallel()
		home := newPromptHome(t)
		out := runHook(t, script, "{}", []string{"HOME=" + home, "PATH=" + isoPATH})
		assertExact(t, out, missingWarn)
	})

	t.Run("mnemon present, stats: count line + guide, exact", func(t *testing.T) {
		t.Parallel()
		home := newPromptHome(t)
		writeGuide(t, filepath.Join(home, ".mnemon", "prompt"), "STATGUIDE\n")
		path := fakeMnemon(t, `{"total_insights": 42, "edge_count": 99}`) + ":" + isoPATH
		out := runHook(t, script, "{}", []string{"HOME=" + home, "PATH=" + path})
		assertExact(t, out, "[mnemon] Memory active (42 insights, 99 edges).\nSTATGUIDE\n")
	})

	t.Run("mnemon present, empty status: fallback line + guide, exact", func(t *testing.T) {
		t.Parallel()
		home := newPromptHome(t)
		writeGuide(t, filepath.Join(home, ".mnemon", "prompt"), "EMPTYGUIDE\n")
		path := fakeMnemon(t, "") + ":" + isoPATH
		out := runHook(t, script, "{}", []string{"HOME=" + home, "PATH=" + path})
		assertExact(t, out, "[mnemon] Memory active.\nEMPTYGUIDE\n")
	})

	t.Run("MNEMON_DATA_DIR override selects that prompt dir, exact", func(t *testing.T) {
		t.Parallel()
		home := newPromptHome(t) // legacy dir exists but has no guide
		dataDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dataDir, "prompt"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeGuide(t, filepath.Join(dataDir, "prompt"), "DATADIR-GUIDE\n")
		out := runHook(t, script, "{}", []string{"HOME=" + home, "MNEMON_DATA_DIR=" + dataDir, "PATH=" + isoPATH})
		assertExact(t, out, missingWarn+"DATADIR-GUIDE\n")
	})

	t.Run("legacy fallback when data dir has no guide, exact", func(t *testing.T) {
		t.Parallel()
		home := newPromptHome(t)
		writeGuide(t, filepath.Join(home, ".mnemon", "prompt"), "LEGACY-GUIDE\n")
		dataDir := t.TempDir() // set, but no prompt/guide.md inside
		out := runHook(t, script, "{}", []string{"HOME=" + home, "MNEMON_DATA_DIR=" + dataDir, "PATH=" + isoPATH})
		assertExact(t, out, missingWarn+"LEGACY-GUIDE\n")
	})

	// Invalid UTF-8 in the guide must never break the hook (SPEC US-4). The old
	// encoder either crashed (UTF-8 locale) or emitted lone surrogates (C locale)
	// that real Codex/serde rejects. Go's json.Unmarshal is lenient to lone
	// surrogates (sanitizes to U+FFFD), so "parses" is not enough — assert exit 0
	// AND that the raw stdout carries no lone-surrogate escape. The fixture has no
	// legitimate non-BMP characters, so any "\ud..." escape here is the bug.
	t.Run("invalid UTF-8 guide: exit 0, no lone surrogates, valid UTF-8", func(t *testing.T) {
		t.Parallel()
		home := newPromptHome(t)
		writeGuide(t, filepath.Join(home, ".mnemon", "prompt"), "good\xff\xfebad\n")
		out := runHook(t, script, "{}", []string{"HOME=" + home, "PATH=" + isoPATH})
		if strings.Contains(string(out), `\ud`) {
			t.Errorf("raw stdout contains a lone-surrogate escape (real Codex/serde rejects):\n%s", out)
		}
		o := assertExact(t, out, missingWarn+"good��bad\n")
		if !utf8.ValidString(o.HookSpecificOutput.AdditionalContext) {
			t.Errorf("additionalContext is not valid UTF-8: %q", o.HookSpecificOutput.AdditionalContext)
		}
	})
}
