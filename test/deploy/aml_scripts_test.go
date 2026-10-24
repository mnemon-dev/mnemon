//go:build !windows

package deploy_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The AML verification scripts (scripts/aml): a drill that fails midway
// leaves no process behind, the knee rule of load.sh, and what the scripts
// refuse before they start anything.

// scriptsRepo is the repository root, and skips the test without the tools
// the scripts need.
func scriptsRepo(t *testing.T, tools ...string) string {
	t.Helper()
	for _, tool := range append([]string{"bash", "jq", "awk"}, tools...) {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is missing; the AML scripts need it", tool)
		}
	}
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

// exitCode is the exit status of a finished command, failing the test when it
// did not run.
func exitCode(t *testing.T, err error) int {
	t.Helper()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		return exit.ExitCode()
	}
	t.Fatalf("the script did not run: %v", err)
	return -1
}

// freePortBase is the first of n consecutive free loopback ports.
func freePortBase(t *testing.T, n int) int {
	t.Helper()
	for try := 0; try < 50; try++ {
		base := 20000 + rand.IntN(30000)
		free := true
		for port := base; port < base+n && free; port++ {
			listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
			if err != nil {
				free = false
				continue
			}
			listener.Close()
		}
		if free {
			return base
		}
	}
	t.Fatal("no free port range")
	return 0
}

// TestDrillCleanupLeavesNoProcess fails a scenario midway, its workload and
// sampler running, and checks that the drill's exit leaves no process of its
// own, no work directory and no port in use. AML_DRILL_BINARY names a mnemon
// to run; without it the test builds one from this tree.
func TestDrillCleanupLeavesNoProcess(t *testing.T) {
	repo := scriptsRepo(t, "curl", "od", "pgrep", "go")
	binary := os.Getenv("AML_DRILL_BINARY")
	if binary == "" {
		binary = filepath.Join(t.TempDir(), "mnemon")
		build := exec.Command("go", "build", "-o", binary, ".")
		build.Dir = repo
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build mnemon: %v\n%s", err, out)
		}
	}
	work := t.TempDir()
	tmp := filepath.Join(work, "tmp")
	if err := os.Mkdir(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	base := freePortBase(t, 6)
	drill := exec.Command("bash", filepath.Join(repo, "scripts", "aml", "drill.sh"), "--only", "baseline", "--seed-adds", "0",
		"--binary", binary, "--out", filepath.Join(work, "out"), "--port-base", strconv.Itoa(base),
		// The drivers sleep 20 s between requests: a cleanup that only
		// signals them through their stop files, then removes the files,
		// leaves them running on.
		"--search-rate", "0.05", "--add-rate", "0.05")
	drill.Env = append(os.Environ(), "AML_DRILL_FAIL_AT=fault", "TMPDIR="+tmp)
	// The drill leads a process group of its own: every process it starts
	// is in it, so one left over is found by the group. Its output goes to
	// a file, not a pipe that a survivor would hold open.
	drill.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	log, err := os.Create(filepath.Join(work, "drill.out"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	drill.Stdout, drill.Stderr = log, log
	start := time.Now()
	if err := drill.Start(); err != nil {
		t.Fatal(err)
	}
	group := drill.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(-group, syscall.SIGKILL) })
	err = drill.Wait()
	out, _ := os.ReadFile(log.Name())
	if code := exitCode(t, err); code != 2 || !strings.Contains(string(out), "failing on purpose") {
		t.Fatalf("the drill failing midway exited %d, want 2 with its reason:\n%s", code, out)
	}
	deadline := time.Now().Add(15 * time.Second)
	for syscall.Kill(-group, 0) == nil {
		if time.Now().After(deadline) {
			listing, _ := exec.Command("ps", "-A", "-o", "pid=,pgid=,command=").Output()
			survivors := []string{}
			for _, line := range strings.Split(string(listing), "\n") {
				if fields := strings.Fields(line); len(fields) > 2 && fields[1] == strconv.Itoa(group) {
					survivors = append(survivors, line)
				}
			}
			t.Fatalf("processes of the drill survive it:\n%s\ndrill output:\n%s", strings.Join(survivors, "\n"), out)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if entries, err := os.ReadDir(tmp); err != nil || len(entries) != 0 {
		t.Errorf("the drill left %v in its TMPDIR (%v), want nothing", entries, err)
	}
	for port := base; port < base+6; port++ {
		listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			t.Errorf("port %d is still in use after the drill: %v", port, err)
			continue
		}
		listener.Close()
	}
	t.Logf("the drill ran %v and failed as asked; no process, directory or port survived", time.Since(start).Round(time.Second))
}

// TestDrillRefusesBeforeStarting checks the arguments the drill refuses
// before it starts anything.
func TestDrillRefusesBeforeStarting(t *testing.T) {
	repo := scriptsRepo(t)
	for _, args := range [][]string{
		{"--only", "no-such-scenario"},
		{"--repeat", "0"},
		{"--error-status", "418"},
		{"--built-from", "abc"},
	} {
		out, err := exec.Command("bash", append([]string{filepath.Join(repo, "scripts", "aml", "drill.sh")}, args...)...).CombinedOutput()
		if code := exitCode(t, err); code != 2 {
			t.Errorf("drill.sh %v exited %d, want 2:\n%s", args, code, out)
		}
	}
}

// TestLoadRefusesUserinfoAndReadsEnvLines checks that load.sh refuses a base
// URL holding a user or password, and that a key file may be an env file's
// line.
func TestLoadRefusesUserinfoAndReadsEnvLines(t *testing.T) {
	repo := scriptsRepo(t)
	dir := t.TempDir()
	key := filepath.Join(dir, "key")
	writeFile(t, key, "AML_API_KEY=mnm_test\n", 0o600)
	out, err := exec.Command("bash", filepath.Join(repo, "scripts", "aml", "load.sh"), "--base-url", "http://user:secret@127.0.0.1:9",
		"--key-file", key, "--mode", "add", "--rates", "1").CombinedOutput()
	if code := exitCode(t, err); code != 2 || !strings.Contains(string(out), "no user or password") {
		t.Errorf("load.sh with userinfo in --base-url exited %d, want 2 naming it:\n%s", code, out)
	}
	if strings.Contains(string(out), "secret") {
		t.Errorf("load.sh repeated the URL's password:\n%s", out)
	}

	for name, file := range map[string]string{
		"an env line":         "OTHER=1\nAML_API_KEY=mnm_test\n",
		"a quoted env line":   "AML_API_KEY=\"mnm_test\"\n",
		"the bare key":        "mnm_test\n",
		"no trailing newline": "mnm_test",
	} {
		source, copied := filepath.Join(dir, "source"), filepath.Join(dir, "copied")
		writeFile(t, source, file, 0o600)
		os.Remove(copied)
		run := exec.Command("bash", "-c", `. "$1"; aml_private_copy "$2" AML_API_KEY "$3"`, "_", filepath.Join(repo, "scripts", "aml", "lib.sh"), source, copied)
		if out, err := run.CombinedOutput(); err != nil {
			t.Errorf("%s: aml_private_copy: %v\n%s", name, err, out)
			continue
		}
		raw, err := os.ReadFile(copied)
		info, statErr := os.Stat(copied)
		if err != nil || statErr != nil || string(raw) != "mnm_test\n" || info.Mode().Perm() != 0o600 {
			t.Errorf("%s: copied %q (%v, %v), want the key alone in a 0600 file", name, raw, err, statErr)
		}
	}
}

// TestLoadKneeRule checks the knee rule (scripts/aml/knee.jq) on made-up
// steps: the p95 and the p50 within twice the baseline's, no non-2xx answer,
// every lower rate holding, and the cases with no knee.
func TestLoadKneeRule(t *testing.T) {
	repo := scriptsRepo(t)
	step := func(rate float64, requests, non2xx int, p50, p95 float64) map[string]any {
		return map[string]any{"rate": rate, "requests": requests, "non2xx": non2xx, "p50": p50, "p95": p95}
	}
	cases := []struct {
		name      string
		route     string
		validate  bool
		settled   bool
		steps     []map[string]any
		kneeRate  float64 // 0: no knee
		reasonHas string
	}{
		{"every step holds", "search", false, true, []map[string]any{step(1, 60, 0, 1, 2), step(2, 120, 0, 1.5, 3), step(4, 240, 0, 2, 4)}, 4, ""},
		{"a p50 past twice", "search", false, true, []map[string]any{step(1, 60, 0, 1, 2), step(2, 120, 0, 1.5, 3.9), step(4, 240, 0, 2.1, 3), step(8, 480, 0, 1, 1)}, 2, ""},
		{"a p95 past twice", "search", false, true, []map[string]any{step(1, 60, 0, 1, 2), step(2, 120, 0, 1, 4.1)}, 1, ""},
		{"a non-2xx answer", "add", false, true, []map[string]any{step(1, 60, 0, 1, 2), step(2, 120, 1, 1, 2)}, 1, ""},
		{"a baseline too small", "search", false, true, []map[string]any{step(0.25, 40, 0, 1, 2), step(1, 300, 0, 1, 2)}, 0, "fewer than 50"},
		{"a validation run", "search", true, true, []map[string]any{step(1, 600, 0, 1, 2)}, 0, "validation"},
		{"Adds not settled", "add", false, false, []map[string]any{step(1, 600, 0, 1, 2)}, 0, "settled"},
		{"Searches not settled", "search", false, false, []map[string]any{step(1, 600, 0, 1, 2)}, 1, ""},
		{"a failing baseline", "search", false, true, []map[string]any{step(1, 600, 5, 1, 2)}, 0, "baseline step itself"},
	}
	for _, c := range cases {
		steps, err := json.Marshal(c.steps)
		if err != nil {
			t.Fatal(err)
		}
		program := fmt.Sprintf(`include "knee"; %s | knee(%q; %t; 50; %t)`, steps, c.route, c.validate, c.settled)
		out, err := exec.Command("jq", "-c", "-n", "-L", filepath.Join(repo, "scripts", "aml"), program).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: jq: %v\n%s", c.name, err, out)
		}
		var got struct {
			Knee   *struct{ Rate float64 }
			Reason string
		}
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("%s: %v: %s", c.name, err, out)
		}
		switch {
		case c.kneeRate == 0 && (got.Knee != nil || !strings.Contains(got.Reason, c.reasonHas)):
			t.Errorf("%s: %s, want no knee because of %q", c.name, out, c.reasonHas)
		case c.kneeRate != 0 && (got.Knee == nil || !reflect.DeepEqual(got.Knee.Rate, c.kneeRate)):
			t.Errorf("%s: %s, want the knee at %v/s", c.name, out, c.kneeRate)
		}
	}
}
