//go:build !windows

package deploy_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The AML verification scripts (scripts/aml): a drill or a load test that
// fails midway leaves no process behind; an abort of a script is never an
// exit 0; the drill's criteria, the Poisson bounds of its random faults and
// the checks of its scenario table; the knee rule of load.sh; the dirty flag
// of the scripts' provenance; and what the scripts refuse before they start
// anything.

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

// testMnemon is the mnemon the cleanup tests run: AML_DRILL_BINARY when it is
// set, else one built from this tree.
func testMnemon(t *testing.T, repo string) string {
	t.Helper()
	if binary := os.Getenv("AML_DRILL_BINARY"); binary != "" {
		return binary
	}
	binary := filepath.Join(t.TempDir(), "mnemon")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = repo
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build mnemon: %v\n%s", err, out)
	}
	return binary
}

// failsLeavingNothing runs script, which must fail midway on purpose (status
// 2, saying "failing on purpose"), with its environment's TMPDIR at tmp. The
// script leads a process group of its own: every process it starts is in
// it, so one left over is found by the group. Its output goes to a file, not
// a pipe that a survivor would hold open. Then no process of the group may
// survive it for more than 15 s, tmp must be empty, and the n ports from
// base must be free. It returns how long the script ran.
func failsLeavingNothing(t *testing.T, script *exec.Cmd, tmp string, base, n int) time.Duration {
	t.Helper()
	script.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	log, err := os.Create(filepath.Join(t.TempDir(), "script.out"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	script.Stdout, script.Stderr = log, log
	start := time.Now()
	if err := script.Start(); err != nil {
		t.Fatal(err)
	}
	group := script.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(-group, syscall.SIGKILL) })
	err = script.Wait()
	took := time.Since(start)
	out, _ := os.ReadFile(log.Name())
	if code := exitCode(t, err); code != 2 || !strings.Contains(string(out), "failing on purpose") {
		t.Fatalf("the script failing midway exited %d, want 2 with its reason:\n%s", code, out)
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
			t.Fatalf("processes of the script survive it:\n%s\nits output:\n%s", strings.Join(survivors, "\n"), out)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if entries, err := os.ReadDir(tmp); err != nil || len(entries) != 0 {
		t.Errorf("the script left %v in its TMPDIR (%v), want nothing", entries, err)
	}
	for port := base; port < base+n; port++ {
		listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err != nil {
			t.Errorf("port %d is still in use after the script: %v", port, err)
			continue
		}
		listener.Close()
	}
	return took
}

// privateDir is a new directory of mode 0700 in the test's temporary
// directory: a TMPDIR the scripts' cleanup must leave empty.
func privateDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "tmp")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestDrillCleanupLeavesNoProcess fails a scenario midway, its workload and
// sampler running, and checks that the drill's exit leaves no process of its
// own, no work directory and no port in use. AML_DRILL_BINARY names a mnemon
// to run; without it the test builds one from this tree.
func TestDrillCleanupLeavesNoProcess(t *testing.T) {
	repo := scriptsRepo(t, "curl", "od", "pgrep", "go")
	binary := testMnemon(t, repo)
	work, tmp := t.TempDir(), privateDir(t)
	base := freePortBase(t, 6)
	drill := exec.Command("bash", filepath.Join(repo, "scripts", "aml", "drill.sh"), "--only", "baseline", "--seed-adds", "0",
		"--binary", binary, "--out", filepath.Join(work, "out"), "--port-base", strconv.Itoa(base),
		// The drivers sleep 20 s between requests: a cleanup that only
		// signals them through their stop files, then removes the files,
		// leaves them running on.
		"--search-rate", "0.05", "--add-rate", "0.05")
	drill.Env = append(os.Environ(), "AML_DRILL_FAIL_AT=fault", "TMPDIR="+tmp)
	took := failsLeavingNothing(t, drill, tmp, base, 6)
	t.Logf("the drill ran %v and failed as asked; no process, directory or port survived", took.Round(time.Second))
}

// TestLoadCleanupLeavesNoProcess fails load.sh --local midway, twice, and
// checks each time that its exit leaves no process of its own, no work
// directory and no port in use:
//   - while its preparation's Adds are in flight: a curl that hangs on /add
//     keeps them there, so a preparation the cleanup does not stop outlives
//     the run;
//   - while a step's attack and sampler run: a vegeta (VEGETA) whose attack
//     lasts two minutes.
//
// AML_DRILL_BINARY names a mnemon to run; without it the test builds one.
func TestLoadCleanupLeavesNoProcess(t *testing.T) {
	repo := scriptsRepo(t, "curl", "od", "pgrep", "sort", "go")
	binary := testMnemon(t, repo)
	curl, err := exec.LookPath("curl")
	if err != nil {
		t.Fatal(err)
	}
	shims := t.TempDir()
	writeFile(t, filepath.Join(shims, "curl"), "#!/bin/bash\n# An Add hangs; every other request is curl's.\n"+
		"for arg in \"$@\"; do case $arg in */add) exec sleep 120 ;; esac; done\nexec "+curl+" \"$@\"\n", 0o755)
	vegeta := filepath.Join(t.TempDir(), "vegeta")
	writeFile(t, vegeta, "#!/bin/bash\n# An attack that lasts two minutes, and no report.\n[ \"$1\" = attack ] && exec sleep 120\nexit 1\n", 0o755)
	for _, c := range []struct {
		at   string
		env  []string
		args []string
	}{
		{"prepare", []string{"PATH=" + shims + string(os.PathListSeparator) + os.Getenv("PATH")}, []string{"--prepare", "3"}},
		{"step", []string{"VEGETA=" + vegeta}, []string{"--prepare", "0"}},
	} {
		t.Run(c.at, func(t *testing.T) {
			work, tmp := t.TempDir(), privateDir(t)
			base := freePortBase(t, 3)
			load := exec.Command("bash", append([]string{filepath.Join(repo, "scripts", "aml", "load.sh"), "--local", "--binary", binary,
				"--mode", "add", "--rates", "1", "--users", "2", "--settle", "30", "--port-base", strconv.Itoa(base),
				"--out", filepath.Join(work, "out")}, c.args...)...)
			load.Env = append(append(os.Environ(), "AML_LOAD_FAIL_AT="+c.at, "TMPDIR="+tmp), c.env...)
			took := failsLeavingNothing(t, load, tmp, base, 3)
			t.Logf("load.sh ran %v and failed at its %s as asked; no process, directory or port survived", took.Round(time.Second), c.at)
		})
	}
}

// TestDrillRefusesBeforeStarting checks the arguments the drill refuses
// before it starts anything. The built-in table passes its checks: the
// refusals of --only come from the selection.
func TestDrillRefusesBeforeStarting(t *testing.T) {
	repo := scriptsRepo(t)
	for _, args := range [][]string{
		{"--only", "no-such-scenario"},
		{"--repeat", "0"},
		{"--error-status", "418"},
		{"--built-from", "abc"},
	} {
		out, err := exec.Command("bash", append([]string{filepath.Join(repo, "scripts", "aml", "drill.sh")}, args...)...).CombinedOutput()
		if code := exitCode(t, err); code != 2 || strings.Contains(string(out), "scenario table") {
			t.Errorf("drill.sh %v exited %d, want 2 and no complaint about the built-in table:\n%s", args, code, out)
		}
	}
}

// TestScriptsRefuseAnOptionWithoutItsValue checks that an option given
// without its value is a usage error, status 2, in each script, and never an
// unbound $2 under set -u, whose status 1 would read as a failed criterion.
// A failure before the cleanup is set, such as the drill's table holding
// only comments, maps to 2 as well.
func TestScriptsRefuseAnOptionWithoutItsValue(t *testing.T) {
	repo := scriptsRepo(t)
	script := func(name string) string { return filepath.Join(repo, "scripts", "aml", name) }
	for name, options := range map[string][]string{
		"drill.sh": {"--out", "--only", "--repeat", "--binary", "--built-from", "--search-rate", "--add-rate", "--users", "--seed-adds",
			"--error-status", "--port-base"},
		"load.sh": {"--base-url", "--key-file", "--mode", "--rates", "--duration", "--users", "--messages", "--prepare", "--admin-url",
			"--admin-token-file", "--timeout", "--settle", "--min-baseline", "--out", "--binary", "--built-from", "--deepseek-latency",
			"--jev-latency", "--limits", "--port-base"},
		"final.sh": {"--summary", "--rev", "--out", "--compare", "--repeat"},
	} {
		for _, option := range options {
			out, err := exec.Command("bash", script(name), option).CombinedOutput()
			if code := exitCode(t, err); code != 2 || strings.Contains(string(out), "unbound variable") {
				t.Errorf("%s %s exited %d, want 2 (a usage error):\n%s", name, option, code, out)
			}
		}
	}
	table := filepath.Join(t.TempDir(), "table")
	writeFile(t, table, "# nothing but a comment\n\n   \n", 0o600)
	drill := exec.Command("bash", script("drill.sh"))
	drill.Env = append(os.Environ(), "AML_DRILL_SCENARIOS="+table)
	out, err := drill.CombinedOutput()
	if code := exitCode(t, err); code != 2 || !strings.Contains(string(out), "holds no scenario") {
		t.Errorf("drill.sh with a table of comments exited %d, want 2 saying it holds no scenario:\n%s", code, out)
	}
}

// shellsUnderTest are the bashes the abort tests run under: /bin/bash (3.2 on
// macOS, which loses an abort's status in an EXIT trap) and the bash on PATH
// when that is another one. It logs their versions.
func shellsUnderTest(t *testing.T) []string {
	t.Helper()
	var shells []string
	system, err := os.Stat("/bin/bash")
	if err == nil {
		shells = append(shells, "/bin/bash")
	}
	if path, err := exec.LookPath("bash"); err == nil {
		if found, err := os.Stat(path); err == nil && (system == nil || !os.SameFile(system, found)) {
			shells = append(shells, path)
		}
	}
	if len(shells) == 0 {
		t.Skip("bash is missing; the AML scripts need it")
	}
	for _, shell := range shells {
		version, _ := exec.Command(shell, "--version").Output()
		t.Logf("%s: %s", shell, strings.SplitN(string(version), "\n", 2)[0])
	}
	return shells
}

// TestScriptsExitNonZeroOnAnAbort forces an unbound variable in each script
// (the test hook AML_TEST_UNBOUND), once before the script's own cleanup is
// set (early) and once after (late), and checks that the script exits 2,
// never 0 (ruling R85). Under macOS bash 3.2 an EXIT trap reads the status of
// such an abort, and of every fatal error of the shell's own, as 0, whatever
// the trap captures first: a broken drill would read as a pass. The scripts
// therefore exit 0 only when they say they ended on purpose. A late abort
// must still run the cleanup: the script's temporary directory is gone.
func TestScriptsExitNonZeroOnAnAbort(t *testing.T) {
	repo := scriptsRepo(t, "curl", "od", "pgrep", "sort", "go")
	for _, shell := range shellsUnderTest(t) {
		for _, c := range []struct {
			script string
			args   func(out string, base int) []string
		}{
			{"drill.sh", func(out string, base int) []string {
				return []string{"--only", "baseline", "--seed-adds", "0", "--out", out, "--port-base", strconv.Itoa(base)}
			}},
			{"load.sh", func(out string, _ int) []string { return []string{"--mode", "add", "--rates", "1", "--out", out} }},
			{"final.sh", func(out string, _ int) []string { return []string{"--out", out} }},
		} {
			for _, at := range []string{"early", "late"} {
				t.Run(strings.ReplaceAll(shell, "/", "_")+"/"+c.script+"/"+at, func(t *testing.T) {
					tmp := privateDir(t)
					args := c.args(filepath.Join(t.TempDir(), "out"), freePortBase(t, 6))
					// A script that did not stop at the hook would run on for minutes: end it.
					ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, shell, append([]string{filepath.Join(repo, "scripts", "aml", c.script)}, args...)...)
					cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
					cmd.WaitDelay = 30 * time.Second
					cmd.Env = append(os.Environ(), "AML_TEST_UNBOUND="+at, "TMPDIR="+tmp)
					out, err := cmd.CombinedOutput()
					if code := exitCode(t, err); code != 2 || !strings.Contains(string(out), "unbound variable") {
						t.Errorf("%s with an unbound variable %s exited %d, want 2 after the abort:\n%s", c.script, at, code, out)
					}
					if entries, _ := os.ReadDir(tmp); at == "late" && len(entries) != 0 {
						t.Errorf("%s aborted %s left %v in its TMPDIR, want its cleanup to have run", c.script, at, entries)
					}
				})
			}
		}
	}
}

// TestExitStatusMapping checks the status a script exits with (lib.sh's
// aml_exit_status, run in its EXIT trap) for the status the trap saw and
// whether the script said it ended on purpose (AML_EXIT_OURS): 2 always
// stands; 0 and 1 only when the script chose them; anything else is a 2.
func TestExitStatusMapping(t *testing.T) {
	repo := scriptsRepo(t)
	lib := filepath.Join(repo, "scripts", "aml", "lib.sh")
	for _, c := range []struct{ status, ours, want string }{
		{"0", "", "2"}, // an abort that the shell reads as 0
		{"0", "1", "0"},
		{"1", "", "2"},
		{"1", "1", "1"},
		{"2", "", "2"},
		{"2", "1", "2"},
		{"3", "1", "2"},
		{"127", "", "2"},
		{"143", "1", "2"},
	} {
		out, err := exec.Command("/bin/bash", "-c", `. "$1"; AML_EXIT_OURS=$3; aml_exit_status "$2" 2>/dev/null`, "_", lib, c.status, c.ours).Output()
		if got := strings.TrimSpace(string(out)); err != nil || got != c.want {
			t.Errorf("status %s with AML_EXIT_OURS=%q maps to %q (%v), want %s", c.status, c.ours, got, err, c.want)
		}
	}
}

// TestFinalSummaryExitsZero checks that a script which ends on purpose still
// exits 0: final.sh --summary of a made-up run. (The drill's and the load
// test's own ends are checked by reading them, below.)
func TestFinalSummaryExitsZero(t *testing.T) {
	repo := scriptsRepo(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "drill", "report.json"), `{"pass":true,"matrix":[]}`, 0o600)
	writeFile(t, filepath.Join(dir, "drill", "matrix.md"), "| scenario |\n|---|\n", 0o600)
	for _, shell := range shellsUnderTest(t) {
		out, err := exec.Command(shell, filepath.Join(repo, "scripts", "aml", "final.sh"), "--summary", dir,
			"--compare", filepath.Join(dir, "none.json")).CombinedOutput()
		if code := exitCode(t, err); code != 0 || !strings.Contains(string(out), "# Final run of") || !strings.Contains(string(out), "No earlier run") {
			t.Errorf("%s final.sh --summary exited %d, want 0 and the summary:\n%s", shell, code, out)
		}
	}
}

// TestFinalRunExitStatus runs final.sh's whole flow on a repository of its
// own, whose drill.sh and load.sh are stubs that exit as asked: the exit status
// of the run is the drill's (0 or 1) when both steps ran, and 2 when a step
// could not run (a drill that exits 2 or more, a load test that fails). The
// real final.sh and lib.sh are copied into it; it extracts that repository's
// HEAD, builds a trivial program and runs the stubs from the extraction.
func TestFinalRunExitStatus(t *testing.T) {
	repo := scriptsRepo(t, "git", "tar", "go")
	tree, env, git := ownGitTree(t)
	for _, name := range []string{"final.sh", "lib.sh"} {
		source, err := os.ReadFile(filepath.Join(repo, "scripts", "aml", name))
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(tree, "scripts", "aml", name), string(source), 0o755)
	}
	writeFile(t, filepath.Join(tree, "scripts", "aml", "drill.sh"), `#!/bin/bash
# A drill that writes a report and exits as STUB_DRILL says.
while [ $# -gt 0 ]; do
	case $1 in --out) out=$2 ;; esac
	shift
done
mkdir -p "$out"
echo '{"pass":true,"matrix":[]}' >"$out/report.json"
printf '| scenario |\n|---|\n' >"$out/matrix.md"
exit "${STUB_DRILL:-0}"
`, 0o755)
	writeFile(t, filepath.Join(tree, "scripts", "aml", "load.sh"), "#!/bin/bash\nexit \"${STUB_LOAD:-0}\"\n", 0o755)
	writeFile(t, filepath.Join(tree, "go.mod"), "module example.invalid/final\n\ngo 1.21\n", 0o644)
	writeFile(t, filepath.Join(tree, "main.go"), "package main\n\nfunc main() {}\n", 0o644)
	git("add", "go.mod", "main.go", "scripts/aml/final.sh", "scripts/aml/lib.sh", "scripts/aml/drill.sh", "scripts/aml/load.sh")
	git("-c", "user.name=test", "-c", "user.email=test@example.invalid", "commit", "-q", "-m", "a tree with stub steps")
	for _, c := range []struct {
		name         string
		drill, load  string
		want         int
		wantInOutput string
	}{
		{"both steps ran and the drill passed", "0", "0", 0, "final run:"},
		{"both steps ran and a hard criterion failed", "1", "0", 1, "final run:"},
		{"the drill could not run", "2", "0", 2, "the drill could not run"},
		{"the drill was stopped by a command that failed", "7", "0", 2, "the drill could not run"},
		{"the load test could not run", "0", "3", 2, "load test could not run"},
	} {
		out := filepath.Join(t.TempDir(), "out")
		cmd := exec.Command("/bin/bash", filepath.Join(tree, "scripts", "aml", "final.sh"), "--out", out)
		cmd.Env = append(append([]string{}, env...), "STUB_DRILL="+c.drill, "STUB_LOAD="+c.load, "TMPDIR="+privateDir(t))
		got, err := cmd.CombinedOutput()
		if code := exitCode(t, err); code != c.want || !strings.Contains(string(got), c.wantInOutput) {
			t.Errorf("%s: final.sh exited %d, want %d with %q in:\n%s", c.name, code, c.want, c.wantInOutput, got)
		}
		if c.want <= 1 {
			if summary, err := os.ReadFile(filepath.Join(out, "summary.md")); err != nil || !strings.Contains(string(summary), "# Final run of") {
				t.Errorf("%s: no summary.md (%v)", c.name, err)
			}
		}
	}
}

// TestScriptsEndOnPurposeOnly reads the scripts: none ends with a plain exit 0
// or 1, which lib.sh's exit rule would turn into a 2, and each one's last
// statement is an aml_exit. The drill's and the load test's own ends are too
// long to run in a test (the real runs, and the final run, show them).
func TestScriptsEndOnPurposeOnly(t *testing.T) {
	repo := scriptsRepo(t)
	plainExit := regexp.MustCompile(`^\s*exit\s+(0|1|"\$[a-z_]+")\b`)
	for name, calls := range map[string][]string{
		"drill.sh": {"aml_exit 0", "aml_exit 1"},
		"load.sh":  {"aml_exit 0", "aml_exit 1"},
		"final.sh": {"aml_exit 0", `aml_exit "$status"`},
	} {
		source, err := os.ReadFile(filepath.Join(repo, "scripts", "aml", name))
		if err != nil {
			t.Fatal(err)
		}
		last := ""
		for number, line := range strings.Split(string(source), "\n") {
			if plainExit.MatchString(line) {
				t.Errorf("%s:%d ends with a plain exit, want aml_exit: %s", name, number+1, line)
			}
			if trimmed := strings.TrimSpace(line); trimmed != "" && !strings.HasPrefix(trimmed, "#") {
				last = trimmed
			}
		}
		if !strings.HasPrefix(last, "aml_exit ") {
			t.Errorf("%s ends with %q, want an aml_exit", name, last)
		}
		for _, call := range calls {
			if !strings.Contains(string(source), call) {
				t.Errorf("%s never calls %q", name, call)
			}
		}
	}
}

// TestDrillChecksItsTable checks that the drill refuses, with status 2 and
// before it starts anything, a scenario table holding a value it could not
// judge: a typo would otherwise drop a criterion without a word. Each case is
// a valid row with one column changed. A valid table of one's own reaches
// the selection.
func TestDrillChecksItsTable(t *testing.T) {
	repo := scriptsRepo(t)
	columns := []string{"x", "hard", "30", "fake", `all={"errorRate":0.3,"errorStatus":503}`, "8", "retries", "0.3^4", "window+45", "180",
		"steady", "0:jev", "a made-up fault"}
	// row is the valid row with the changes made: column, value, column, …
	row := func(changes ...any) string {
		changed := append([]string{}, columns...)
		for i := 0; i+1 < len(changes); i += 2 {
			changed[changes[i].(int)] = changes[i+1].(string)
		}
		return strings.Join(changed, "|")
	}
	drill := func(table string) (int, string) {
		t.Helper()
		file := filepath.Join(t.TempDir(), "table")
		writeFile(t, file, table, 0o600)
		cmd := exec.Command("bash", filepath.Join(repo, "scripts", "aml", "drill.sh"), "--only", "no-such-scenario")
		cmd.Env = append(os.Environ(), "AML_DRILL_SCENARIOS="+file)
		out, err := cmd.CombinedOutput()
		return exitCode(t, err), string(out)
	}
	valid := []string{
		row(),
		"# a comment\n" + row(7, "0") + "\n\n" + row(0, "y") + "\n",
		row(7, ">0") + "\n" + row(0, "z", 1, "measured", 2, "60", 3, "none", 4, "-", 5, "12", 6, "none", 7, "2", 8, "-", 9, "-", 10, "-", 11, "-"),
		row(6, "rejected:deepseek") + "\n" + row(0, "y", 1, "expected", 2, "120", 3, "down", 4, "both", 5, "21", 6, "breaker-open", 7, ">0",
			8, "-", 9, "-", 10, "cycle", 11, "-"),
		row(6, "fake:all:slow"),
		row(9, "-"),
		row(9, "12.5"),
		row(7, "0.05^2"),
		row(8, "1^1"),
		row(8, "window+0"),
	}
	for _, table := range valid {
		if code, out := drill(table); code != 2 || !strings.Contains(out, "no scenario matched") || strings.Contains(out, "scenario table") {
			t.Errorf("a valid table was refused (exit %d), want the selection's refusal:\n%s\n%s", code, table, out)
		}
	}
	for _, c := range []struct {
		column int
		value  string
	}{
		{0, ""}, {0, "a,b"}, {0, "a b"},
		{1, "hrad"},
		{2, "30s"}, {2, "0"},
		{3, "toxin"},
		{4, ""},
		{5, "eight"},
		{6, "retry"}, {6, "rejected:openai"}, {6, "fake:x:slow"},
		{7, "=0"}, {7, "<=1"}, {7, ">1"}, {7, "1.5"}, {7, "0.3^"}, {7, "^4"}, {7, "1.5^4"}, {7, "0.3^0"}, {7, "0^4"}, {7, "window+"}, {7, "window-45"},
		{8, "=0"}, {8, "w+45"},
		{9, "fast"},
		{10, "stedy"},
		{11, "0:openai"}, {11, "1:jev"},
		{12, ""},
	} {
		table := row(c.column, c.value)
		if code, out := drill(table); code != 2 || !strings.Contains(out, "scenario table") {
			t.Errorf("column %d %q: exit %d, want 2 refusing the table:\n%s", c.column+1, c.value, code, out)
		}
	}
	for name, table := range map[string]string{
		"a column short":     strings.Join(columns[:12], "|"),
		"a column too many":  row() + "|more",
		"a name used twice":  row() + "\n" + row(),
		"a typo on line two": row() + "\n" + row(0, "y", 7, "=0"),
	} {
		if code, out := drill(table); code != 2 || !strings.Contains(out, "scenario table") {
			t.Errorf("%s: exit %d, want 2 refusing the table:\n%s", name, code, out)
		}
	}
}

// jqOutput runs jq with the arguments and returns its output. A jq program
// that does not end fails the test after 30 s instead of hanging it.
func jqOutput(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "jq", args...).CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("jq %v did not end within 30 s", args)
	}
	return out, err
}

// drillRun is one made-up run of a drill scenario, in the arguments drill.sh
// gives scripts/aml/scenario.jq: a hard row whose fault, from t 1000 to 1030,
// took effect (3 JEV retries); 14 Searches started every 4 s from t 990 and
// answered; breakers closed throughout; healthy otherwise.
type drillRun struct {
	text          map[string]string // --arg
	values        map[string]any    // --argjson
	before, after map[string]any    // /admin/status, before and after
}

func newDrillRun() *drillRun {
	status := func() map[string]any {
		return map[string]any{"searches": 0, "degraded": 0, "partial": 0, "adds": 0, "linkFallbacks": 0,
			"rejectedByKind": map[string]int{"deepseek": 0, "jev": 0}, "breakerTrips": map[string]int{"deepseek": 0, "jev": 0},
			"budgets": []map[string]any{{"kind": "deepseek", "held": 0}, {"kind": "jev", "held": 0}}}
	}
	r := &drillRun{
		text: map[string]string{"name": "made-up", "class": "hard", "what": "a made-up fault", "type": "fake", "spec": "-", "effect": "retries",
			"wantDegraded": "-", "wantPartial": "-", "wantP95": "-", "wantBreaker": "-", "wantRetries": "-"},
		before: status(), after: status(),
	}
	starts := []int{}
	for at := 990; at <= 1042; at += 4 {
		starts = append(starts, at)
	}
	r.after["searches"] = len(starts)
	latency := map[string]any{"n": len(starts), "p50": 1, "p95": 2, "p99": 3, "max": 4}
	codes := map[string]any{"total": len(starts), "non2xx": 0, "shed": 0, "codes": map[string]int{"200": len(starts)}}
	r.values = map[string]any{"run": 1, "window": 30, "tstart": 990, "tfault": 1000, "tend": 1030, "tidle": 1100, "residual": 500,
		"heldSeconds": 2, "drainLimit": 600, "residualLimit": 10500, "recoverLimit": 180, "without": 0, "minSearches": 5,
		"searchLatency": latency, "addLatency": latency, "searchCodes": codes, "addCodes": codes,
		"mb": []any{map[string]any{}}, "ma": []any{map[string]any{`mnemon_aml_model_retries_total{kind="jev"}`: 3}},
		"fb": []any{map[string]any{}}, "fa": []any{map[string]any{}}, "reasons": []any{map[string]any{}},
		"samples":      breakerSamples(990, "closed", 1000, "closed", 1030, "closed", 1060, "closed"),
		"searchStarts": starts}
	return r
}

// breakerSamples are /admin/status samples at the times given, each followed
// by JEV's breaker state then (DeepSeek's stays closed).
func breakerSamples(timeline ...any) []map[string]any {
	samples := []map[string]any{}
	for i := 0; i+1 < len(timeline); i += 2 {
		samples = append(samples, map[string]any{"t": timeline[i], "breakers": map[string]any{"deepseek": "closed", "jev": timeline[i+1]},
			"degraded": 0, "queue": map[string]int{"maxBacklog": 100}})
	}
	return samples
}

// summary runs scripts/aml/scenario.jq on the run.
func (r *drillRun) summary(t *testing.T, repo string) map[string]any {
	t.Helper()
	args := []string{"-n", "-c"}
	for name, value := range r.text {
		args = append(args, "--arg", name, value)
	}
	values := map[string]any{"sb": []any{r.before}, "sa": []any{r.after}}
	for name, value := range r.values {
		values[name] = value
	}
	for name, value := range values {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		args = append(args, "--argjson", name, string(raw))
	}
	lib := filepath.Join(repo, "scripts", "aml")
	out, err := jqOutput(t, append(args, "-L", lib, "-f", filepath.Join(lib, "scenario.jq"))...)
	if err != nil {
		t.Fatalf("scenario.jq: %v\n%s", err, out)
	}
	var summary map[string]any
	if err := json.Unmarshal(out, &summary); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return summary
}

// TestDrillScenarioCriteria runs the drill's criteria (scripts/aml/scenario.jq)
// on made-up runs:
//   - ruling R84's bounds on degraded and on partial Searches: for a fault
//     random per attempt at rate p, which the policy gives a attempts, with
//     q = 1 − (1 − p^a)^10 and λ = N × q for the N Searches answered, the
//     smallest k with P(X > k) < 1% for X Poisson with mean λ, at least 1 and
//     at most N (the expected values here are from an 80-digit decimal
//     computation of the Poisson tail, made apart from the code);
//   - the bounds of a fault over a window: at most the Searches that started
//     in it or within the given seconds after it; and a number, 0 or >0;
//   - the breaker's cycle, whose close must come within the 180 s after the
//     fault's end, and steady, which needs samples to show it.
func TestDrillScenarioCriteria(t *testing.T) {
	repo := scriptsRepo(t)
	bounds := func(degraded, partial string) func(*drillRun) {
		return func(r *drillRun) { r.text["wantDegraded"], r.text["wantPartial"] = degraded, partial }
	}
	answered := func(searches, degraded, partial int) func(*drillRun) {
		return func(r *drillRun) {
			r.after["searches"], r.after["degraded"], r.after["partial"] = searches, degraded, partial
		}
	}
	breaker := func(want string, samples []map[string]any) func(*drillRun) {
		return func(r *drillRun) { r.text["wantBreaker"], r.values["samples"] = want, samples }
	}
	for _, c := range []struct {
		name   string
		edits  []func(*drillRun)
		want   map[string]string  // criterion: "pass", "fail", or "" for not judged
		atMost map[string]float64 // bounds.<column>.atMost
		lambda float64            // bounds.<column>.lambda, for a bound of the form P^A
	}{
		{"a random fault: 0.3^4 of 14 Searches (λ 1.0935) allows 4", []func(*drillRun){bounds("0.3^4", "0.3^4"), answered(14, 4, 4)},
			map[string]string{"degraded": "pass", "partial": "pass"}, map[string]float64{"degraded": 4, "partial": 4}, 1.093546},
		{"a random fault: 0.3^4 of 14 Searches fails 5", []func(*drillRun){bounds("0.3^4", "0.3^4"), answered(14, 5, 0)},
			map[string]string{"degraded": "fail", "partial": "pass"}, map[string]float64{"degraded": 4}, 1.093546},
		{"a random fault: 0.3^4 of 22 Searches (λ 1.7184) allows 5", []func(*drillRun){bounds("0.3^4", "0.3^4"), answered(22, 5, 5)},
			map[string]string{"degraded": "pass", "partial": "pass"}, map[string]float64{"degraded": 5, "partial": 5}, 1.718429},
		{"a random fault: 0.05^2 of 20 Searches (λ 0.4944) allows 3, not 4", []func(*drillRun){bounds("0.05^2", "0.05^2"), answered(20, 4, 3)},
			map[string]string{"degraded": "fail", "partial": "pass"}, map[string]float64{"degraded": 3, "partial": 3}, 0.494412},
		{"a random fault: 0.2^2 of 18 Searches (λ 6.033) allows 12", []func(*drillRun){bounds("0.2^2", "0.2^2"), answered(18, 12, 12)},
			map[string]string{"degraded": "pass", "partial": "pass"}, map[string]float64{"degraded": 12, "partial": 12}, 6.033013},
		{"a random fault: 0.2^2 of 18 Searches fails 13", []func(*drillRun){bounds("0.2^2", "0.2^2"), answered(18, 13, 13)},
			map[string]string{"degraded": "fail", "partial": "fail"}, map[string]float64{"degraded": 12}, 6.033013},
		{"a random fault: never less than 1", []func(*drillRun){bounds("0.05^4", "0.05^4"), answered(22, 1, 1)},
			map[string]string{"degraded": "pass", "partial": "pass"}, map[string]float64{"degraded": 1, "partial": 1}, 0.001375},
		{"a random fault: 1 is the most when the quantile is 0", []func(*drillRun){bounds("0.05^4", "0.05^4"), answered(22, 2, 0)},
			map[string]string{"degraded": "fail", "partial": "pass"}, map[string]float64{"degraded": 1}, 0.001375},
		{"a random fault: never more than the Searches answered", []func(*drillRun){bounds("0.5^1", "-"), answered(14, 14, 0)},
			map[string]string{"degraded": "pass", "partial": ""}, map[string]float64{"degraded": 14}, 13.986328},
		{"a window: the Searches started in it and 45 s after", []func(*drillRun){bounds("window+45", "window+45"),
			func(r *drillRun) { r.values["searchStarts"] = append(r.values["searchStarts"].([]int), 1076, 1080) }, answered(16, 11, 0)},
			map[string]string{"degraded": "pass", "partial": "pass"}, map[string]float64{"degraded": 11, "partial": 11}, 0},
		{"a window: one more fails", []func(*drillRun){bounds("window+45", "window+45"), answered(14, 12, 12)},
			map[string]string{"degraded": "fail", "partial": "fail"}, map[string]float64{"degraded": 11}, 0},
		{"a number", []func(*drillRun){bounds("2", "2"), answered(14, 2, 3)},
			map[string]string{"degraded": "pass", "partial": "fail"}, map[string]float64{"degraded": 2, "partial": 2}, 0},
		{"none may", []func(*drillRun){bounds("0", "0"), answered(14, 0, 1)},
			map[string]string{"degraded": "pass", "partial": "fail"}, map[string]float64{"degraded": 0}, 0},
		{"some must", []func(*drillRun){bounds(">0", "-"), answered(14, 0, 0)},
			map[string]string{"degraded": "fail", "partial": ""}, nil, 0},
		{"a cycle that closes within 180 s", []func(*drillRun){breaker("cycle",
			breakerSamples(995, "closed", 1010, "open", 1030, "half-open", 1040, "open", 1100, "closed", 1110, "closed"))},
			map[string]string{"breaker": "pass"}, nil, 0},
		{"a cycle that closes 250 s after the fault's end", []func(*drillRun){breaker("cycle",
			breakerSamples(995, "closed", 1010, "open", 1040, "open", 1200, "half-open", 1280, "closed"))},
			map[string]string{"breaker": "fail"}, nil, 0},
		{"a cycle that never closes", []func(*drillRun){breaker("cycle", breakerSamples(995, "closed", 1010, "open", 1100, "half-open", 1200, "open"))},
			map[string]string{"breaker": "fail"}, nil, 0},
		{"steady", []func(*drillRun){breaker("steady", breakerSamples(990, "closed", 1030, "closed"))},
			map[string]string{"breaker": "pass"}, nil, 0},
		{"steady without a sample", []func(*drillRun){breaker("steady", []map[string]any{})},
			map[string]string{"breaker": "fail"}, nil, 0},
	} {
		r := newDrillRun()
		for _, edit := range c.edits {
			edit(r)
		}
		summary := r.summary(t, repo)
		criteria, _ := summary["criteria"].(map[string]any)
		for name, want := range c.want {
			if got, _ := criteria[name].(string); got != want {
				t.Errorf("%s: criterion %s is %q, want %q\n%v", c.name, name, got, want, criteria)
			}
		}
		all, _ := summary["bounds"].(map[string]any)
		for column, want := range c.atMost {
			bound, _ := all[column].(map[string]any)
			if got, ok := bound["atMost"].(float64); !ok || got != want {
				t.Errorf("%s: the %s bound is %v, want at most %v", c.name, column, bound, want)
			}
			if got, _ := bound["lambda"].(float64); c.lambda != 0 && math.Abs(got-c.lambda) > 1e-6 {
				t.Errorf("%s: the %s bound's λ is %v, want %v", c.name, column, bound["lambda"], c.lambda)
			}
		}
	}
}

// poissonQuantileOracle is the smallest k with P(X > k) < tail for X Poisson
// with the mean, from the terms exp(−mean + i·ln mean − ln i!) (math.Lgamma)
// summed from 0: not the recurrence in log space that the jq module uses.
func poissonQuantileOracle(mean, tail float64) int {
	if mean <= 0 {
		return 0
	}
	cdf := 0.0
	for k := 0; ; k++ {
		logFactorial, _ := math.Lgamma(float64(k) + 1)
		cdf += math.Exp(-mean + float64(k)*math.Log(mean) - logFactorial)
		if 1-cdf < tail {
			return k
		}
	}
}

// TestPoissonQuantile checks scripts/aml/poisson.jq's poissonQuantile(mean;
// tail), the smallest k with P(X > k) < tail for X Poisson with that mean:
//   - against values from an 80-digit decimal computation of the tail, made
//     apart from the code (for mean 0.5: P(X > 2) = 1.44% and P(X > 3) = 0.18%,
//     so k = 3), among them the edges where the tail passes 1% (0.01 and
//     0.0101) and a mean whose e^-mean is 0 in a double;
//   - against the oracle above over a sweep of means and tails;
//   - a mean or a tail it cannot use, and a tail too small for a double to
//     reach, are errors, never an endless loop.
func TestPoissonQuantile(t *testing.T) {
	repo := scriptsRepo(t)
	jq := func(program string) string {
		t.Helper()
		out, err := jqOutput(t, "-n", "-c", "-L", filepath.Join(repo, "scripts", "aml"), `include "poisson"; `+program)
		if err != nil {
			t.Fatalf("jq %s: %v\n%s", program, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	known := []struct {
		mean, tail float64
		k          int
	}{
		{0, 0.01, 0}, {0.0001, 0.01, 0}, {0.01, 0.01, 0}, {0.0101, 0.01, 1}, {0.1, 0.01, 1}, {0.445, 0.01, 3}, {0.5, 0.01, 3}, {1, 0.01, 4},
		{2, 0.01, 6}, {5, 0.01, 11}, {10, 0.01, 18}, {22, 0.01, 34}, {100, 0.01, 124}, {1000, 0.01, 1074},
		{0.5, 0.05, 2}, {0.5, 0.1, 1}, {0.5, 0.001, 4}, {10, 0.05, 15}, {10, 0.001, 21},
	}
	var means, wants []string
	for _, c := range known {
		means = append(means, fmt.Sprintf("[%v,%v]", c.mean, c.tail))
		wants = append(wants, strconv.Itoa(c.k))
	}
	if got, want := jq("["+strings.Join(means, ",")+"] | map(poissonQuantile(.[0]; .[1]))"), "["+strings.Join(wants, ",")+"]"; got != want {
		t.Errorf("poissonQuantile over the known values is\n%s\nwant\n%s", got, want)
	}

	var sweep, want []string
	for mean := 0.001; mean < 300; mean *= 1.37 {
		for _, tail := range []float64{0.05, 0.01, 0.001} {
			sweep = append(sweep, fmt.Sprintf("[%v,%v]", mean, tail))
			want = append(want, strconv.Itoa(poissonQuantileOracle(mean, tail)))
		}
	}
	if got, want := jq("["+strings.Join(sweep, ",")+"] | map(poissonQuantile(.[0]; .[1]))"), "["+strings.Join(want, ",")+"]"; got != want {
		t.Errorf("poissonQuantile over the sweep is\n%s\nwant the oracle's\n%s", got, want)
	}

	if got, want := jq(`[try poissonQuantile(nan; 0.01) catch "error", try poissonQuantile(infinite; 0.01) catch "error",
		try poissonQuantile(1; 0) catch "error", try poissonQuantile(1; 1) catch "error", try poissonQuantile(1; 1e-300) catch "error"]`),
		`["error","error","error","error","error"]`; got != want {
		t.Errorf("poissonQuantile of a mean or a tail it cannot use is %s, want an error for each", got)
	}
}

// TestDrillTableDerivesItsBounds checks the bounds that the built-in scenario
// table derives for its random faults (ruling R84), against an oracle of this
// test's own. For every row whose degraded or partial column is P^A: the N
// Searches a run answers at 0.25 a second are (10 s before the fault + the
// window + 15 s after) / 4, rounded up; q = 1 − (1 − P^A)^10, λ = N × q and k
// is the Poisson 99% quantile; then
//   - scripts/aml/scenario.jq gives, for N answered Searches, that λ and the
//     bound max(1, min(N, k)); and
//   - the table's comments state it, as "P^A at N n: q …, λ …, k …, at most
//     …", so that what a reader of the table sees is what the drill judges.
func TestDrillTableDerivesItsBounds(t *testing.T) {
	repo := scriptsRepo(t)
	source, err := os.ReadFile(filepath.Join(repo, "scripts", "aml", "drill.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	const open = "\tcat <<EOF\n"
	start := strings.Index(text, open)
	if start < 0 {
		t.Fatal("drill.sh holds no built-in table (cat <<EOF)")
	}
	table := text[start+len(open):]
	table = table[:strings.Index(table, "\nEOF\n")]
	comments := map[string]bool{}
	derivation := regexp.MustCompile(`^#\s+(\S+\^\d+ at N \d+: q \S+, λ \S+, k \d+, at most \d+)$`)
	form := regexp.MustCompile(`^([0-9.]+)\^([0-9]+)$`)
	type derived struct {
		form     string
		searches int
	}
	rows := map[derived][]string{} // the derivation: the rows that have it
	for _, line := range strings.Split(table, "\n") {
		if m := derivation.FindStringSubmatch(line); m != nil {
			comments[m[1]] = true
		}
		columns := strings.Split(line, "|")
		if strings.HasPrefix(line, "#") || len(columns) != 13 {
			continue
		}
		window, err := strconv.Atoi(columns[2])
		if err != nil {
			t.Fatalf("row %s: window %q", columns[0], columns[2])
		}
		searches := (10 + window + 15 + 3) / 4 // rounded up
		for _, bound := range columns[7:9] {
			key := derived{bound, searches}
			if names := rows[key]; form.MatchString(bound) && (len(names) == 0 || names[len(names)-1] != columns[0]) {
				rows[key] = append(names, columns[0])
			}
		}
	}
	if len(rows) < 7 {
		t.Fatalf("the table has %d distinct random-fault derivations, want at least 7 (the rows with a P^A bound): %v", len(rows), rows)
	}
	for key, names := range rows {
		parts := form.FindStringSubmatch(key.form)
		p, _ := strconv.ParseFloat(parts[1], 64)
		attempts, _ := strconv.Atoi(parts[2])
		share := 1 - math.Pow(1-math.Pow(p, float64(attempts)), 10)
		lambda := float64(key.searches) * share
		quantile := poissonQuantileOracle(lambda, 0.01)
		atMost := max(1, min(key.searches, quantile))

		run := newDrillRun()
		run.text["wantDegraded"], run.text["wantPartial"] = key.form, key.form
		run.after["searches"] = key.searches
		bounds, _ := run.summary(t, repo)["bounds"].(map[string]any)
		for _, column := range []string{"degraded", "partial"} {
			bound, _ := bounds[column].(map[string]any)
			if got, _ := bound["atMost"].(float64); got != float64(atMost) {
				t.Errorf("%s at N %d (%v): the drill's %s bound is %v, want at most %d", key.form, key.searches, names, column, bound, atMost)
			}
			if got, _ := bound["lambda"].(float64); math.Abs(got-lambda) > 1e-9 {
				t.Errorf("%s at N %d (%v): the drill's λ is %v, want %v", key.form, key.searches, names, bound["lambda"], lambda)
			}
		}
		stated := fmt.Sprintf("%s at N %d: q %s, λ %s, k %d, at most %d", key.form, key.searches,
			strconv.FormatFloat(share, 'f', 5, 64), strconv.FormatFloat(lambda, 'f', 3, 64), quantile, atMost)
		if !comments[stated] {
			t.Errorf("the table does not state the derivation of %v in a comment line:\n  # %s", names, stated)
		}
	}
}

// TestLoadRefusesUserinfoAndReadsEnvLines checks that load.sh refuses a base
// URL holding a user or password, and that a key file may be an env file's
// line. load.sh needs curl, sort, od and go before it reads its URL.
func TestLoadRefusesUserinfoAndReadsEnvLines(t *testing.T) {
	repo := scriptsRepo(t, "curl", "sort", "od", "go")
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

// ownGitTree is a new git repository in the test's temporary directory, apart
// from every repository above it: the tree, the environment to run git (and
// the scripts that run git) with, and a function that runs git on the tree. It
// skips the test when the temporary directory is inside a git work tree. Never
// a configuration of this machine's, and only this tree's repository,
// whatever git would find above it.
func ownGitTree(t *testing.T) (tree string, env []string, git func(args ...string)) {
	t.Helper()
	tree = t.TempDir()
	if out, err := exec.Command("git", "-C", tree, "rev-parse", "--is-inside-work-tree").Output(); err == nil && strings.TrimSpace(string(out)) == "true" {
		t.Skip("the temporary directory is inside a git work tree")
	}
	env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CEILING_DIRECTORIES="+filepath.Dir(tree))
	git = func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"--git-dir=" + filepath.Join(tree, ".git"), "--work-tree=" + tree}, args...)...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	initialize := exec.Command("git", "init", "-q", tree)
	initialize.Env = env
	if out, err := initialize.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return tree, env, git
}

// TestAMLProvenanceCountsUntrackedFiles checks the dirty flag the scripts
// record (lib.sh's aml_provenance) in a repository of its own: go build
// compiles an untracked .go file, so an untracked file makes the tree dirty,
// and so does a changed one; an ignored file does not.
func TestAMLProvenanceCountsUntrackedFiles(t *testing.T) {
	repo := scriptsRepo(t, "git")
	tree, env, git := ownGitTree(t)
	writeFile(t, filepath.Join(tree, ".gitignore"), "ignored.go\n", 0o644)
	writeFile(t, filepath.Join(tree, "main.go"), "package main\n\nfunc main() {}\n", 0o644)
	git("add", ".gitignore", "main.go")
	git("-c", "user.name=test", "-c", "user.email=test@example.invalid", "commit", "-q", "-m", "a tree")
	dirty := func() any {
		t.Helper()
		cmd := exec.Command("bash", "-c", `. "$1"; AML_REPO=$2; aml_provenance`, "_", filepath.Join(repo, "scripts", "aml", "lib.sh"), tree)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		var provenance struct {
			Commit *string
			Dirty  any
		}
		if err != nil || json.Unmarshal(out, &provenance) != nil || provenance.Commit == nil || len(*provenance.Commit) != 40 {
			t.Fatalf("aml_provenance: %v: %s", err, out)
		}
		return provenance.Dirty
	}
	if got := dirty(); got != false {
		t.Errorf("a clean tree is dirty %v, want false", got)
	}
	writeFile(t, filepath.Join(tree, "ignored.go"), "package main\n", 0o644)
	if got := dirty(); got != false {
		t.Errorf("an ignored file makes the tree dirty %v, want false", got)
	}
	writeFile(t, filepath.Join(tree, "untracked.go"), "package main\n\nfunc init() {}\n", 0o644)
	if got := dirty(); got != true {
		t.Errorf("an untracked .go file leaves the tree dirty %v, want true", got)
	}
	os.Remove(filepath.Join(tree, "untracked.go"))
	writeFile(t, filepath.Join(tree, "main.go"), "package main\n\nfunc main() { println() }\n", 0o644)
	if got := dirty(); got != true {
		t.Errorf("a changed file leaves the tree dirty %v, want true", got)
	}
}

// TestLoadKneeRule checks the knee rule (scripts/aml/knee.jq) on made-up
// steps: the p95 and the p50 within twice the baseline's, no non-2xx answer,
// every lower rate holding, and the cases with no knee. An Add step that did
// not start from a settled scheduler is void, and so is every step after it:
// the knee comes from the steps before it.
func TestLoadKneeRule(t *testing.T) {
	repo := scriptsRepo(t)
	step := func(rate float64, requests, non2xx int, p50, p95 float64) map[string]any {
		return map[string]any{"rate": rate, "requests": requests, "non2xx": non2xx, "p50": p50, "p95": p95, "settled": true}
	}
	settled := func(value any, s map[string]any) map[string]any {
		s["settled"] = value
		return s
	}
	cases := []struct {
		name      string
		route     string
		validate  bool
		steps     []map[string]any
		kneeRate  float64 // 0: no knee
		reasonHas string  // the reason there is none, or the note on the void steps
	}{
		{"every step holds", "search", false, []map[string]any{step(1, 60, 0, 1, 2), step(2, 120, 0, 1.5, 3), step(4, 240, 0, 2, 4)}, 4, ""},
		{"a p50 past twice", "search", false, []map[string]any{step(1, 60, 0, 1, 2), step(2, 120, 0, 1.5, 3.9), step(4, 240, 0, 2.1, 3), step(8, 480, 0, 1, 1)}, 2, ""},
		{"a p95 past twice", "search", false, []map[string]any{step(1, 60, 0, 1, 2), step(2, 120, 0, 1, 4.1)}, 1, ""},
		{"a non-2xx answer", "add", false, []map[string]any{step(1, 60, 0, 1, 2), step(2, 120, 1, 1, 2)}, 1, ""},
		{"a baseline too small", "search", false, []map[string]any{step(0.25, 40, 0, 1, 2), step(1, 300, 0, 1, 2)}, 0, "fewer than 50"},
		{"a validation run", "search", true, []map[string]any{step(1, 600, 0, 1, 2)}, 0, "validation"},
		{"Adds whose first step did not start settled", "add", false, []map[string]any{settled(false, step(1, 600, 0, 1, 2)), step(2, 1200, 0, 1, 2)}, 0, "settled"},
		{"Adds with no settle (no admin access)", "add", false, []map[string]any{settled(nil, step(1, 600, 0, 1, 2))}, 0, "settled"},
		{"Adds unsettled from the third step", "add", false, []map[string]any{step(1, 600, 0, 1, 2), step(2, 1200, 0, 1, 2),
			settled(false, step(4, 2400, 0, 1, 2)), settled(false, step(8, 4800, 0, 1, 2))}, 2, "from 4/s"},
		{"Adds unsettled only after the knee", "add", false, []map[string]any{step(1, 600, 0, 1, 2), step(2, 1200, 0, 1, 2),
			step(4, 2400, 0, 1, 5), settled(false, step(8, 4800, 0, 1, 2))}, 2, "from 8/s"},
		{"Searches not settled", "search", false, []map[string]any{settled(false, step(1, 600, 0, 1, 2))}, 1, ""},
		{"a failing baseline", "search", false, []map[string]any{step(1, 600, 5, 1, 2)}, 0, "baseline step itself"},
	}
	for _, c := range cases {
		steps, err := json.Marshal(c.steps)
		if err != nil {
			t.Fatal(err)
		}
		program := fmt.Sprintf(`include "knee"; %s | knee(%q; %t; 50)`, steps, c.route, c.validate)
		out, err := exec.Command("jq", "-c", "-n", "-L", filepath.Join(repo, "scripts", "aml"), program).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: jq: %v\n%s", c.name, err, out)
		}
		var got struct {
			Knee   *struct{ Rate float64 }
			Reason string
			Void   string
		}
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("%s: %v: %s", c.name, err, out)
		}
		switch {
		case c.kneeRate == 0 && (got.Knee != nil || !strings.Contains(got.Reason, c.reasonHas)):
			t.Errorf("%s: %s, want no knee because of %q", c.name, out, c.reasonHas)
		case c.kneeRate != 0 && (got.Knee == nil || !reflect.DeepEqual(got.Knee.Rate, c.kneeRate)):
			t.Errorf("%s: %s, want the knee at %v/s", c.name, out, c.kneeRate)
		case c.kneeRate != 0 && ((c.reasonHas == "") != (got.Void == "") || !strings.Contains(got.Void, c.reasonHas)):
			t.Errorf("%s: %s, want a note on void steps %q", c.name, out, c.reasonHas)
		}
	}
}
