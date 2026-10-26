package assets

// These tests guard the encoding contract of the PowerShell variant of the
// ZCode prime hook. Windows PowerShell 5.1 reads BOM-less files with the
// system ANSI code page (GBK on Chinese systems), so the Get-Content call that
// loads guide.md must pin -Encoding UTF8; without it the injected guide text
// is corrupted for every non-ASCII character. The static check runs on every
// platform; the behavioral check executes the embedded script on Windows,
// where a stock powershell.exe with a non-UTF-8 code page reproduces the bug.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestZCodePrimeHookPowerShellDeclaresUTF8 requires the Get-Content call that
// loads guide.md to pin -Encoding UTF8.
func TestZCodePrimeHookPowerShellDeclaresUTF8(t *testing.T) {
	call := regexp.MustCompile(`Get-Content\s+-LiteralPath\s+\$guidePath[^)]*`).
		FindString(string(ZCodePrimeHookPowerShell))
	if call == "" {
		t.Fatal("prime.ps1 no longer loads guidePath via Get-Content; update this guard")
	}
	if !regexp.MustCompile(`-Encoding\s+UTF8`).MatchString(call) {
		t.Fatalf("Get-Content for guide.md must pin -Encoding UTF8, got: %s", call)
	}
}

// TestZCodePrimeHookPowerShellReadsBOMlessCJKGuide runs the embedded hook with
// a BOM-less UTF-8 CJK guide and requires the injected context to keep the
// text intact. On the pre-fix script the guide is decoded with the ANSI code
// page and this test fails on any Windows system whose code page is not UTF-8.
func TestZCodePrimeHookPowerShellReadsBOMlessCJKGuide(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell only")
	}
	ps, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skip("powershell.exe not found:", err)
	}
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		t.Skip("SystemRoot not set")
	}

	guide := "### Mnemon 指南 — 回忆再回答\n默认每条新消息先 recall。"
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "prompt"), 0o755); err != nil {
		t.Fatalf("mkdir prompt: %v", err)
	}
	// os.WriteFile writes the literal bytes: UTF-8 without a BOM.
	if err := os.WriteFile(filepath.Join(dataDir, "prompt", "guide.md"), []byte(guide), 0o644); err != nil {
		t.Fatalf("write guide.md: %v", err)
	}

	script := filepath.Join(t.TempDir(), "prime.ps1")
	if err := os.WriteFile(script, ZCodePrimeHookPowerShell, 0o644); err != nil {
		t.Fatalf("write prime.ps1: %v", err)
	}

	// where.exe stands in for the mnemon binary: `where status` finds nothing
	// on a System32-only PATH, exits non-zero, and the hook falls back to
	// "[mnemon] Memory active." without depending on a real installation.
	//
	// The console starts pinned to code page 936 instead of inheriting the
	// host's default: on a machine whose stdout already defaults to UTF-8 the
	// script's own OutputEncoding fix would be unobservable, and this test
	// must fail whenever the script stops forcing UTF-8 output.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	quoted := "'" + strings.ReplaceAll(script, "'", "''") + "'"
	cmd := exec.CommandContext(ctx, ps, "-NoProfile", "-NonInteractive",
		"-ExecutionPolicy", "Bypass", "-Command",
		"[Console]::OutputEncoding = [System.Text.Encoding]::GetEncoding(936); & "+quoted)
	cmd.Stdin = strings.NewReader("{}")
	cmd.Env = []string{
		"MNEMON_BIN=" + filepath.Join(systemRoot, "System32", "where.exe"),
		"MNEMON_DATA_DIR=" + dataDir,
		"SystemRoot=" + systemRoot,
		"PATH=" + filepath.Join(systemRoot, "System32"),
		"TEMP=" + os.TempDir(),
		"TMP=" + os.TempDir(),
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run prime.ps1: %v\nstderr:\n%s", err, stderr.String())
	}

	var payload hookOutput
	if err := json.Unmarshal(bytes.TrimSpace(out), &payload); err != nil {
		t.Fatalf("parse hook output %q: %v", string(out), err)
	}
	injected := payload.HookSpecificOutput.AdditionalContext
	if !strings.Contains(injected, guide) {
		t.Fatalf("injected context lost the CJK guide (encoding regression?)\nwant substring: %q\ngot: %q",
			guide, injected)
	}
}
