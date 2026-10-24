package memory

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
)

// These tests hold aml-serve and aml-inspect to ruling R90: a restart holds
// nothing for good. A stop cuts the model requests still in flight at the end
// of its windows, which settle before the process exits (aml_test.go's
// TestShutdownCutsAbandonedModelRequests), and a start settles what a crash
// left held in the instance's own budget file, under the instance's lock.

// leftHold is a reservation a process that ended left held: its kind, what
// it holds, and the input estimate its attempt recorded (cost and tokens).
type leftHold struct {
	kind      string
	reserved  float64
	estimate  float64
	estimated int
}

// leaveHolds writes holds into the budget file at path as a process that
// crashed with requests in flight leaves them.
func leaveHolds(t *testing.T, path string, holds ...leftHold) {
	t.Helper()
	budget, err := memoryagent.OpenBudget(path)
	if err != nil {
		t.Fatal(err)
	}
	budget.Close()
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: path}).String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i, h := range holds {
		for _, statement := range []struct {
			query string
			args  []any
		}{
			{`INSERT INTO accounts(kind,cap) VALUES(?,100) ON CONFLICT(kind) DO NOTHING`, []any{h.kind}},
			{`INSERT INTO attempts(id,kind,request_hash,reserved,estimate,estimate_tokens) VALUES(?,?,?,?,?,?)`, []any{fmt.Sprintf("left-%d", i), h.kind, "h", h.reserved, h.estimate, h.estimated}},
			{`UPDATE accounts SET held=held+? WHERE kind=?`, []any{h.reserved, h.kind}},
		} {
			if _, err := db.Exec(statement.query, statement.args...); err != nil {
				t.Fatal(statement.query, err)
			}
		}
	}
}

// budgetFileAccount is what the budget file at path says of kind's account:
// held and spent.
func budgetFileAccount(t *testing.T, path, kind string) (held, spent float64) {
	t.Helper()
	budget, err := memoryagent.OpenBudget(path)
	if err != nil {
		t.Fatal(err)
	}
	defer budget.Close()
	accounts, err := budget.Accounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, account := range accounts {
		if account.Kind == kind {
			return account.Held, account.Spent
		}
	}
	t.Fatalf("no %s account in %v", kind, accounts)
	return 0, 0
}

// budgetAccount is budgetFileAccount for the budget file of the memory
// configuration at configPath.
func budgetAccount(t *testing.T, configPath, kind string) (held, spent float64) {
	t.Helper()
	config, err := readMemoryConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	return budgetFileAccount(t, config.BudgetFile, kind)
}

// near reports whether two amounts agree to 1e-9.
func near(a, b float64) bool { return a-b < 1e-9 && b-a < 1e-9 }

// R90: the holds a previous process left in the instance's budget file, its
// requests cut by a crash, are settled at the input estimates their attempts
// recorded when aml-serve starts, under the data root's lock and before it
// listens, and one line says what it settled, by kind. A start with nothing
// held says nothing of it.
func TestAMLServeSettlesTheHoldsAPreviousProcessLeft(t *testing.T) {
	notified := recordNotifications(t)
	endpoint := newModelEndpoint(t)
	configPath := amlTestConfig(t, endpoint.URL, nil)
	config, err := readMemoryConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	leaveHolds(t, config.BudgetFile,
		leftHold{"deepseek", 0.25, 0.0021, 1050},
		leftHold{"deepseek", 0.25, 0.0009, 450},
		leftHold{"jev", 0.01, 0.000042, 1000})
	run := startAMLServe(t, configPath)
	waitFor(t, "READY=1", func() bool { return slices.Contains(notified.all(), "READY=1") })
	if err := run.stop(t); err != nil {
		t.Fatal(err, run.log.String())
	}
	log := run.log.String()
	settled := strings.Index(log, "settled 3 budget reservations a previous process left held, at their input estimates: deepseek 2 (held 0.5000, settled 0.0030), jev 1 (held 0.0100, settled 0.0000)")
	if listening := strings.Index(log, "aml-serve listening on"); settled < 0 || listening < settled || strings.Count(log, "budget reservations") != 1 {
		t.Fatal(log)
	}
	if held, spent := budgetFileAccount(t, config.BudgetFile, "deepseek"); held != 0 || !near(spent, 0.003) {
		t.Fatal("deepseek held", held, "spent", spent)
	}
	if held, spent := budgetFileAccount(t, config.BudgetFile, "jev"); held != 0 || !near(spent, 0.000042) {
		t.Fatal("jev held", held, "spent", spent)
	}

	// Nothing is held now: the next start says nothing of it.
	again := startAMLServe(t, configPath)
	waitFor(t, "READY=1 again", func() bool {
		ready := 0
		for _, state := range notified.all() {
			if state == "READY=1" {
				ready++
			}
		}
		return ready == 2
	})
	if err := again.stop(t); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(again.log.String(), "budget reservations") {
		t.Fatal(again.log.String())
	}
}

// shortInspectGrace makes aml-inspect's shutdown windows short until the test
// ends.
func shortInspectGrace(t *testing.T, grace, cancel time.Duration) {
	t.Helper()
	previousGrace, previousCancel := inspectShutdownGrace, inspectCancelGrace
	inspectShutdownGrace, inspectCancelGrace = grace, cancel
	t.Cleanup(func() { inspectShutdownGrace, inspectCancelGrace = previousGrace, previousCancel })
}

// amlInspectRun is an aml-inspect command the test runs until it stops it.
type amlInspectRun struct {
	log  *syncLog
	stop context.CancelFunc
	done chan error
	addr string
}

// startAMLInspect runs aml-inspect on a loopback port of its own, without a
// token, over root with configPath, its full searches billed to debug; it
// returns once the command listens, or with the command's error.
func startAMLInspect(t *testing.T, root, configPath, debug string) (*amlInspectRun, error) {
	t.Helper()
	run := &amlInspectRun{log: &syncLog{}, done: make(chan error, 1)}
	command := newAMLInspectCommand()
	command.SetArgs([]string{"--listen", "127.0.0.1:0", "--data-root", root, "--memory-config", configPath,
		"--debug-budget-file", debug, "--debug-caps", "deepseek=5,jev=1", "--log-format", "json"})
	command.SilenceUsage, command.SilenceErrors = true, true
	command.SetOut(io.Discard)
	command.SetErr(run.log)
	ctx, stop := context.WithCancel(context.Background())
	run.stop = stop
	go func() { run.done <- command.ExecuteContext(ctx) }()
	t.Cleanup(func() {
		stop()
		select {
		case <-run.done:
		case <-time.After(30 * time.Second):
		}
	})
	for start := time.Now(); time.Since(start) < 10*time.Second; time.Sleep(5 * time.Millisecond) {
		if match := inspectListening.FindStringSubmatch(run.log.String()); match != nil {
			run.addr = match[1]
			return run, nil
		}
		select {
		case err := <-run.done:
			run.done <- err
			return run, err
		default:
		}
	}
	t.Fatal("aml-inspect did not listen:", run.log.String())
	return nil, nil
}

// end stops the command as SIGTERM does and returns its error, and how long
// it took to return.
func (r *amlInspectRun) end(t *testing.T) (error, time.Duration) {
	t.Helper()
	start := time.Now()
	r.stop()
	select {
	case err := <-r.done:
		r.done <- err
		return err, time.Since(start)
	case <-time.After(30 * time.Second):
		t.Fatal("aml-inspect did not stop")
		return nil, 0
	}
}

// R90 for the inspect service: it takes its debug budget's lock, settles the
// holds a previous run left there at their recorded estimates before it
// listens, and says so in one line; a second aml-inspect on the same debug
// budget refuses to start while the first runs.
func TestAMLInspectSettlesTheHoldsAPreviousRunLeft(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "users"), 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := amlTestConfig(t, "http://127.0.0.1:1", nil)
	debug := filepath.Join(t.TempDir(), "debug-budget.db")
	leaveHolds(t, debug, leftHold{"deepseek", 0.25, 0.004, 2000})
	first, err := startAMLInspect(t, root, configPath, debug)
	if err != nil {
		t.Fatal(err)
	}
	log := first.log.String()
	settled := strings.Index(log, "settled 1 budget reservation a previous process left held, at their input estimates: deepseek 1 (held 0.2500, settled 0.0040)")
	if listening := strings.Index(log, "aml-inspect listening on"); settled < 0 || listening < settled {
		t.Fatal(log)
	}
	if _, err := startAMLInspect(t, root, configPath, debug); err == nil || err.Error() != "another aml-inspect holds "+debug+".lock" {
		t.Fatal("a second aml-inspect on the same debug budget:", err)
	}
	if err, _ := first.end(t); err != nil {
		t.Fatal(err, first.log.String())
	}
	if held, spent := budgetFileAccount(t, debug, "deepseek"); held != 0 || !near(spent, 0.004) {
		t.Fatal("held", held, "spent", spent)
	}
}

// Minor 2 and R90: aml-inspect's shutdown is bounded as aml-serve's is.
// Closing its models waits for their requests in flight, those of a full
// search whose planner attempts were abandoned included, until the end of
// its windows, then cuts them: they settle in the debug budget, and the
// command returns within a few seconds of the windows (TimeoutStopSec=45
// leaves it more), saying what it cut.
func TestAMLInspectShutdownCutsAbandonedModelRequests(t *testing.T) {
	shortInspectGrace(t, 100*time.Millisecond, 400*time.Millisecond)
	notified := recordNotifications(t)
	endpoint := newModelEndpoint(t)
	// A planner attempt stops waiting after one second and is not retried;
	// its request runs on toward the minute's hard timeout.
	configPath := amlTestConfig(t, endpoint.URL, func(config *memoryagent.Config) {
		config.Planner.TimeoutSeconds, config.Planner.AttemptTimeoutSeconds, config.Planner.MaxAttempts = 60, 1, 1
	})
	// An aml-serve run gives the data root a user's store.
	serve := startAMLServe(t, configPath)
	waitFor(t, "READY=1", func() bool { return slices.Contains(notified.all(), "READY=1") })
	if code, body := serve.post(t, "/add", noteAdd("r1", "u1")); code != http.StatusOK {
		t.Fatal(code, body)
	}
	if err := serve.stop(t); err != nil {
		t.Fatal(err)
	}
	debug := filepath.Join(t.TempDir(), "debug-budget.db")
	run, err := startAMLInspect(t, serve.root, configPath, debug)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"user_id": "u1", "query": "When did Caroline go to the LGBTQ support group?", "mode": "full"})
	response, err := (&http.Client{Timeout: 30 * time.Second}).Post("http://"+run.addr+"/v1/search", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if n := endpoint.requests.Load(); n < 1 {
		t.Fatal("planner requests", n)
	}
	err, took := run.end(t)
	if took > 5*time.Second {
		t.Fatal("the shutdown waited for the abandoned requests", took)
	}
	if err == nil || !strings.Contains(err.Error(), "deepseek: cut ") || !strings.Contains(err.Error(), "still in flight at the close's deadline: their reservations settled at their input estimates") {
		t.Fatal(err, run.log.String())
	}
	if held, spent := budgetFileAccount(t, debug, "deepseek"); held != 0 || spent <= 0 {
		t.Fatal("debug budget deepseek held", held, "spent", spent)
	}
}
