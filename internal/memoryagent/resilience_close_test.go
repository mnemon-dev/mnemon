package memoryagent

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// These tests hold the providers and the budget to ruling R90: a restart
// holds nothing for good. A provider's close has a deadline, at which it cuts
// what still runs, and each cut request settles or releases its reservation
// before the close returns; and the holds an earlier process left in a
// budget file, its requests cut by a crash, are settled at the input
// estimates their attempts recorded.

// R90, and the parked T4-M4: until its deadline the close lets the requests
// in flight run on; at it, it cuts those still running, an abandoned one
// included, and the attempts still waiting for a bulkhead permit, cause
// errProviderClosed. The abandoned request, which was sent, settles at its
// input estimate, the waiting call fails without a request, and the close
// returns once they have ended, saying what it cut: nothing stays held.
func TestCloseWithinCutsWhatStillRuns(t *testing.T) {
	var sent atomic.Int64 // the body length of the request the endpoint read
	e := newEndpointWith(t, func(_ int, _ http.ResponseWriter, r *http.Request, body []byte) {
		sent.Store(int64(len(body)))
		<-r.Context().Done() // it never answers
	})
	// An Interactive attempt stops waiting after a second and is not
	// retried; its request runs on toward the minute's hard timeout.
	p, budget := deepseekOn(t, e, ModelConfig{TimeoutSeconds: 60, AttemptTimeoutSeconds: 1, MaxAttempts: 1}, nil)
	ctx := context.Background()
	if _, _, err := p.Complete(ctx, "s", "p"); !errors.Is(err, ErrAttemptTimeout) {
		t.Fatal(err)
	}
	// A Background call finds its bulkhead full: it waits for a permit for
	// up to its attempt timeout, the minute's hard timeout.
	background := p.policies[Background].calls
	for range backgroundCalls {
		background.TryAcquirePermit()
	}
	defer func() {
		for range backgroundCalls {
			background.ReleasePermit()
		}
	}()
	waiting := make(chan error, 1)
	go func() {
		_, _, err := p.Complete(WithCallClass(ctx, Background), "s", "p")
		waiting <- err
	}()
	time.Sleep(100 * time.Millisecond) // it is waiting at the bulkhead
	closing, cancel := context.WithTimeout(ctx, 400*time.Millisecond)
	defer cancel()
	start := time.Now()
	closed := make(chan error, 1)
	go func() { closed <- p.CloseWithin(closing) }()
	var err error
	select {
	case err = <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the close did not return by its deadline")
	}
	if elapsed := time.Since(start); elapsed < 400*time.Millisecond {
		t.Fatal("the close cut before its deadline", elapsed)
	}
	if err == nil || !strings.Contains(err.Error(), "cut 1 model request still in flight") || strings.Contains(err.Error(), "held") {
		t.Fatal(err)
	}
	if got := <-waiting; !errors.Is(got, errProviderClosed) {
		t.Fatal("the call waiting at the bulkhead:", got)
	}
	held, spent := accountHeld(t, budget, "deepseek")
	attempts := budgetAttempts(t, budget)
	estimate := float64(sent.Load()) * 2 / 1e6 // DeepSeek: a byte at most a token, CNY 2 per 1M
	if !near(held, 0) || !near(spent, estimate) || len(attempts) != 1 || attempts[0].actual == nil || !near(*attempts[0].actual, estimate) {
		t.Fatal("held", held, "spent", spent, "want", estimate, "attempts", attempts)
	}
	if _, _, err := p.Complete(ctx, "s", "p"); !errors.Is(err, errProviderClosed) || e.requests() != 1 {
		t.Fatal("a call after the close:", err, e.requests())
	}
}

// R90: a request that ends before the close's deadline is not cut: the
// close waits for it and returns nil, its reply settled at its usage.
func TestCloseWithinWaitsForWhatEndsInTime(t *testing.T) {
	answered := make(chan struct{})
	e := newEndpoint(t, func(_ int, w http.ResponseWriter) {
		<-answered
		io.WriteString(w, deepseekReply)
	})
	p, budget := deepseekOn(t, e, ModelConfig{}, nil)
	called := make(chan error, 1)
	go func() {
		_, _, err := p.Complete(context.Background(), "s", "p")
		called <- err
	}()
	for e.requests() == 0 {
		time.Sleep(time.Millisecond)
	}
	time.AfterFunc(100*time.Millisecond, func() { close(answered) })
	closing, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.CloseWithin(closing); err != nil {
		t.Fatal(err)
	}
	if err := <-called; err != nil {
		t.Fatal(err)
	}
	if held, spent := accountHeld(t, budget, "deepseek"); !near(held, 0) || !near(spent, deepseekReplyCNY) {
		t.Fatal("held", held, "spent", spent)
	}
}

// R90: a reservation records the input estimate its request would settle at
// (Provider.inputEstimate), and SettleHolds settles what a process that ended
// left held at it, or at its reservation for an attempt that recorded none
// (one reserved before estimates were recorded): held goes to 0 and spent
// grows by the settlements, by kind. Settled attempts are left alone, and a
// second call finds nothing.
func TestSettleHoldsSettlesWhatAnEarlierProcessLeft(t *testing.T) {
	path := filepath.Join(t.TempDir(), "budget.db")
	ctx := context.Background()
	earlier, err := OpenBudget(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []struct {
		kind     string
		estimate Usage
	}{
		{"deepseek", Usage{InputTokens: 1000, CNY: 0.002}},
		{"deepseek", Usage{InputTokens: 3000, CNY: 0.006}},
		{"jev", Usage{InputTokens: 500, USD: 0.000021}},
	} {
		if _, err := earlier.reserveWithin(ctx, r.kind, "h", 0, 1, r.estimate); err != nil {
			t.Fatal(err)
		}
	}
	done, err := earlier.reserveWithin(ctx, "deepseek", "h", 0, 1, Usage{InputTokens: 10, CNY: 0.00002})
	if err != nil {
		t.Fatal(err)
	}
	if err := earlier.settle(ctx, done, Usage{InputTokens: 100, OutputTokens: 10, CNY: 0.00028}); err != nil {
		t.Fatal(err)
	}
	// An attempt reserved by a version that recorded no estimate.
	if _, err := earlier.db.Exec(`INSERT INTO attempts(id,kind,request_hash,reserved) VALUES('legacy','deepseek','h',0.25)`); err != nil {
		t.Fatal(err)
	}
	if _, err := earlier.db.Exec(`UPDATE accounts SET held=held+0.25 WHERE kind='deepseek'`); err != nil {
		t.Fatal(err)
	}
	earlier.Close() // the process ends with four attempts held

	b, err := OpenBudget(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	settled, err := b.SettleHolds(ctx)
	want := []HeldSettled{
		{Kind: "deepseek", Holds: 3, Held: 0.75, Settled: 0.002 + 0.006 + 0.25},
		{Kind: "jev", Holds: 1, Held: 0.01, Settled: 0.000021},
	}
	if err != nil || !sameSettled(settled, want) {
		t.Fatalf("settled %+v, %v; want %+v", settled, err, want)
	}
	for kind, wantSpent := range map[string]float64{"deepseek": 0.00028 + 0.002 + 0.006 + 0.25, "jev": 0.000021} {
		if held, spent := accountHeld(t, b, kind); held != 0 || !near(spent, wantSpent) {
			t.Fatal(kind, "held", held, "spent", spent, "want", wantSpent)
		}
	}
	var unresolved int
	if err := b.db.QueryRow(`SELECT count(*) FROM attempts WHERE actual IS NULL`).Scan(&unresolved); err != nil || unresolved != 0 {
		t.Fatal("attempts still held", unresolved, err)
	}
	var actual float64
	var tokens int
	if err := b.db.QueryRow(`SELECT actual,input_tokens FROM attempts WHERE id=?`, done).Scan(&actual, &tokens); err != nil || !near(actual, 0.00028) || tokens != 100 {
		t.Fatal("a settled attempt changed", actual, tokens, err)
	}
	if again, err := b.SettleHolds(ctx); err != nil || len(again) != 0 {
		t.Fatal("a second call settled", again, err)
	}
}

// R90: a budget file made before attempts recorded their estimates opens:
// its attempts gain the columns, and what it holds settles at the
// reservations.
func TestSettleHoldsOnABudgetFileOfAnEarlierVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "budget.db")
	if err := writeFile(path); err != nil {
		t.Fatal(err)
	}
	u := url.URL{Scheme: "file", Path: path}
	old, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE accounts(kind TEXT PRIMARY KEY,cap REAL NOT NULL,spent REAL NOT NULL DEFAULT 0,held REAL NOT NULL DEFAULT 0)`,
		`CREATE TABLE attempts(id TEXT PRIMARY KEY,kind TEXT NOT NULL,request_hash TEXT NOT NULL,reserved REAL NOT NULL,actual REAL,input_tokens INTEGER,output_tokens INTEGER)`,
		`INSERT INTO accounts(kind,cap,spent,held) VALUES('jev',10,0.5,0.01)`,
		`INSERT INTO attempts(id,kind,request_hash,reserved) VALUES('old','jev','h',0.01)`,
	} {
		if _, err := old.Exec(statement); err != nil {
			t.Fatal(statement, err)
		}
	}
	old.Close()
	b, err := OpenBudget(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	settled, err := b.SettleHolds(context.Background())
	if err != nil || !sameSettled(settled, []HeldSettled{{Kind: "jev", Holds: 1, Held: 0.01, Settled: 0.01}}) {
		t.Fatalf("%+v %v", settled, err)
	}
	if held, spent := accountHeld(t, b, "jev"); held != 0 || !near(spent, 0.51) {
		t.Fatal("held", held, "spent", spent)
	}
	// It reserves with an estimate from now on.
	if _, err := b.reserveWithin(context.Background(), "jev", "h", 0, 1, Usage{InputTokens: 7, USD: 0.0000003}); err != nil {
		t.Fatal(err)
	}
}

// writeFile makes an empty private file at path.
func writeFile(path string) error { return os.WriteFile(path, nil, 0o600) }

// sameSettled compares settlements by kind with float tolerance.
func sameSettled(got, want []HeldSettled) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		g, w := got[i], want[i]
		if g.Kind != w.Kind || g.Holds != w.Holds || !near(g.Held, w.Held) || !near(g.Settled, w.Settled) {
			return false
		}
	}
	return true
}
