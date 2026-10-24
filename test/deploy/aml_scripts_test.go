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

// The AML verification scripts (scripts/aml): a drill or a load test that
// fails midway leaves no process behind; the drill's criteria and the
// checks of its scenario table; the knee rule of load.sh; the dirty flag of
// the scripts' provenance; and what the scripts refuse before they start
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
	out, err := exec.Command("jq", append(args, "-f", filepath.Join(repo, "scripts", "aml", "scenario.jq"))...).CombinedOutput()
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
//   - ruling R82's bounds on degraded and on partial Searches: for a fault
//     random per attempt at rate p, which the policy gives a attempts, at most
//     max(1, ⌈N × min(1, 2·10·p^a)⌉) of the N Searches; for a fault over a
//     window, at most the Searches that started in it or within the given
//     seconds after it; or a number, 0 or >0, as before;
//   - the breaker's cycle, whose close must come within the 180 s after the
//     fault, and steady, which needs samples to show it.
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
	}{
		{"a random fault: 0.3^4 of 14 Searches allows 3", []func(*drillRun){bounds("0.3^4", "0.3^4"), answered(14, 3, 3)},
			map[string]string{"degraded": "pass", "partial": "pass"}, map[string]float64{"degraded": 3, "partial": 3}},
		{"a random fault: 0.3^4 of 14 Searches fails 4", []func(*drillRun){bounds("0.3^4", "0.3^4"), answered(14, 4, 0)},
			map[string]string{"degraded": "fail", "partial": "pass"}, map[string]float64{"degraded": 3}},
		{"a random fault: 0.05^2 of 20 Searches allows 1, not 2", []func(*drillRun){bounds("0.05^2", "0.05^2"), answered(20, 2, 1)},
			map[string]string{"degraded": "fail", "partial": "pass"}, map[string]float64{"degraded": 1, "partial": 1}},
		{"a random fault: never less than 1", []func(*drillRun){bounds("0.05^4", "0.05^4"), answered(22, 1, 1)},
			map[string]string{"degraded": "pass", "partial": "pass"}, map[string]float64{"degraded": 1, "partial": 1}},
		{"a random fault: at most every Search", []func(*drillRun){bounds("0.5^1", "-"), answered(14, 14, 0)},
			map[string]string{"degraded": "pass", "partial": ""}, map[string]float64{"degraded": 14}},
		{"a window: the Searches started in it and 45 s after", []func(*drillRun){bounds("window+45", "window+45"),
			func(r *drillRun) { r.values["searchStarts"] = append(r.values["searchStarts"].([]int), 1076, 1080) }, answered(16, 11, 0)},
			map[string]string{"degraded": "pass", "partial": "pass"}, map[string]float64{"degraded": 11, "partial": 11}},
		{"a window: one more fails", []func(*drillRun){bounds("window+45", "window+45"), answered(14, 12, 12)},
			map[string]string{"degraded": "fail", "partial": "fail"}, map[string]float64{"degraded": 11}},
		{"a number", []func(*drillRun){bounds("2", "2"), answered(14, 2, 3)},
			map[string]string{"degraded": "pass", "partial": "fail"}, map[string]float64{"degraded": 2, "partial": 2}},
		{"none may", []func(*drillRun){bounds("0", "0"), answered(14, 0, 1)},
			map[string]string{"degraded": "pass", "partial": "fail"}, map[string]float64{"degraded": 0}},
		{"some must", []func(*drillRun){bounds(">0", "-"), answered(14, 0, 0)},
			map[string]string{"degraded": "fail", "partial": ""}, nil},
		{"a cycle that closes within 180 s", []func(*drillRun){breaker("cycle",
			breakerSamples(995, "closed", 1010, "open", 1030, "half-open", 1040, "open", 1100, "closed", 1110, "closed"))},
			map[string]string{"breaker": "pass"}, nil},
		{"a cycle that closes 250 s after the fault", []func(*drillRun){breaker("cycle",
			breakerSamples(995, "closed", 1010, "open", 1040, "open", 1200, "half-open", 1280, "closed"))},
			map[string]string{"breaker": "fail"}, nil},
		{"a cycle that never closes", []func(*drillRun){breaker("cycle", breakerSamples(995, "closed", 1010, "open", 1100, "half-open", 1200, "open"))},
			map[string]string{"breaker": "fail"}, nil},
		{"steady", []func(*drillRun){breaker("steady", breakerSamples(990, "closed", 1030, "closed"))},
			map[string]string{"breaker": "pass"}, nil},
		{"steady without a sample", []func(*drillRun){breaker("steady", []map[string]any{})},
			map[string]string{"breaker": "fail"}, nil},
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

// TestAMLProvenanceCountsUntrackedFiles checks the dirty flag the scripts
// record (lib.sh's aml_provenance) in a repository of its own: go build
// compiles an untracked .go file, so an untracked file makes the tree dirty,
// and so does a changed one; an ignored file does not.
func TestAMLProvenanceCountsUntrackedFiles(t *testing.T) {
	repo := scriptsRepo(t, "git")
	tree := t.TempDir()
	if out, err := exec.Command("git", "-C", tree, "rev-parse", "--is-inside-work-tree").Output(); err == nil && strings.TrimSpace(string(out)) == "true" {
		t.Skip("the temporary directory is inside a git work tree")
	}
	// Never a configuration of this machine's, and only this tree's
	// repository, whatever git would find above it.
	env := append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CEILING_DIRECTORIES="+filepath.Dir(tree))
	git := func(args ...string) {
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
