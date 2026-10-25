package memoryagent

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"sync"
	"testing"
)

// The caps reservations see change at once; a reservation already held
// keeps its own amount, and a cap below what is spent stops the kind.
func TestBudgetSetCaps(t *testing.T) {
	budget, err := OpenBudgetWithCaps(filepath.Join(t.TempDir(), "budget.db"), map[string]float64{"deepseek": 1})
	if err != nil {
		t.Fatal(err)
	}
	defer budget.Close()
	ctx := context.Background()
	if caps := budget.Caps(); !mapsEqual(caps, map[string]float64{"deepseek": 1, "jev": 10, "openai": 5}) {
		t.Fatal(caps)
	}
	held, err := budget.reserveAmount(ctx, "deepseek", "h1", 0.25)
	if err != nil {
		t.Fatal(err)
	}
	if err := budget.SetCaps(map[string]float64{"deepseek": 0.1}); err != nil {
		t.Fatal(err)
	}
	accounts, err := budget.Accounts(ctx)
	if err != nil || len(accounts) != 1 || accounts[0].Cap != 0.1 || accounts[0].Held != 0.25 {
		t.Fatal(accounts, err)
	}
	if attempts := budgetAttempts(t, budget); len(attempts) != 1 || attempts[0].reserved != 0.25 || attempts[0].actual != nil {
		t.Fatal("the held reservation changed", attempts)
	}
	if _, err := budget.reserveAmount(ctx, "deepseek", "h2", 0); !errors.Is(err, ErrBudgetCap) {
		t.Fatal("a reservation passed a cap below what is held:", err)
	}
	// The held one settles as it would have.
	if err := budget.settle(ctx, held, Usage{InputTokens: 100, CNY: 0.2}); err != nil {
		t.Fatal(err)
	}
	if h, spent := accountHeld(t, budget, "deepseek"); h != 0 || !near(spent, 0.2) {
		t.Fatal(h, spent)
	}
	// Spent above the cap: the kind stays stopped; JEV is untouched.
	if _, err := budget.reserveAmount(ctx, "deepseek", "h3", 0); !errors.Is(err, ErrBudgetCap) {
		t.Fatal(err)
	}
	if _, err := budget.reserveAmount(ctx, "jev", "h4", 0); err != nil {
		t.Fatal(err)
	}
	if err := budget.SetCaps(map[string]float64{"deepseek": 5}); err != nil {
		t.Fatal(err)
	}
	if _, err := budget.reserveAmount(ctx, "deepseek", "h5", 0); err != nil {
		t.Fatal("a raised cap still refuses:", err)
	}
	for _, bad := range []map[string]float64{{"bogus": 1}, {"deepseek": -1}, {"jev": math.NaN()}, {"jev": math.Inf(-1)}, {"deepseek": 2, "local": 1}} {
		if err := budget.SetCaps(bad); err == nil {
			t.Fatalf("SetCaps(%v) was accepted", bad)
		}
	}
	if caps := budget.Caps(); !mapsEqual(caps, map[string]float64{"deepseek": 5, "jev": 10, "openai": 5}) {
		t.Fatal("a refused SetCaps changed the caps:", caps)
	}
	budget.Caps()["jev"] = 99
	if budget.Caps()["jev"] != 10 {
		t.Fatal("Caps is not a copy")
	}
	// Concurrent changes of two kinds both land.
	var wg sync.WaitGroup
	for _, kind := range []string{"jev", "openai"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 1; i <= 100; i++ {
				budget.SetCaps(map[string]float64{kind: float64(i)})
			}
		}()
	}
	wg.Wait()
	if caps := budget.Caps(); caps["jev"] != 100 || caps["openai"] != 100 || caps["deepseek"] != 5 {
		t.Fatal(caps)
	}
}

// An unlimited cap (+Inf, a settings file's null): no reservation of the kind
// is refused for a cap, a Background one included, and spend is still
// metered; the account shows "cap": null and "unlimited": true until a cap
// is set again.
func TestBudgetUnlimitedCap(t *testing.T) {
	budget, err := OpenBudgetWithCaps(filepath.Join(t.TempDir(), "budget.db"), map[string]float64{"deepseek": 0.3})
	if err != nil {
		t.Fatal(err)
	}
	defer budget.Close()
	ctx := context.Background()
	if err := budget.SetCaps(map[string]float64{"deepseek": math.Inf(1)}); err != nil {
		t.Fatal(err)
	}
	for i := range 100 {
		share := 1.0
		if i%2 == 1 {
			share = backgroundShare
		}
		id, err := budget.reserveWithin(ctx, "deepseek", "h", 0.25, share, Usage{})
		if err != nil {
			t.Fatalf("reservation %d was refused while the cap is unlimited: %v", i, err)
		}
		if i%4 == 0 {
			continue // held
		}
		if err := budget.settle(ctx, id, Usage{InputTokens: 10, CNY: 0.2}); err != nil {
			t.Fatal(err)
		}
	}
	accounts, err := budget.Accounts(ctx)
	if err != nil || len(accounts) != 1 {
		t.Fatal(accounts, err)
	}
	account := accounts[0]
	if !account.Unlimited || !math.IsInf(account.Cap, 1) || !near(account.Spent, 75*0.2) || !near(account.Held, 25*0.25) {
		t.Fatalf("%+v, want the spend metered and the cap unlimited", account)
	}
	// The budget file never holds an infinity, which other readers of the
	// file (SQLite's tools, a script) may not take: the largest finite cap.
	var stored float64
	if err := budget.db.QueryRow(`SELECT cap FROM accounts WHERE kind='deepseek'`).Scan(&stored); err != nil || stored != math.MaxFloat64 {
		t.Fatalf("the budget file records the unlimited cap as %v (%v), want the largest finite float", stored, err)
	}
	raw, err := json.Marshal(account)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil || fields["cap"] != nil || fields["unlimited"] != true {
		t.Fatalf("an unlimited account is %s", raw)
	}
	if _, ok := fields["cap"]; !ok {
		t.Fatalf("an unlimited account has no cap field: %s", raw)
	}
	// A cap again: what is spent is above it, so the kind stops.
	if err := budget.SetCaps(map[string]float64{"deepseek": 0.3}); err != nil {
		t.Fatal(err)
	}
	if _, err := budget.reserveAmount(ctx, "deepseek", "h", 0); !errors.Is(err, ErrBudgetCap) {
		t.Fatal(err)
	}
	accounts, _ = budget.Accounts(ctx)
	raw, _ = json.Marshal(accounts[0])
	fields = nil
	if err := json.Unmarshal(raw, &fields); err != nil || accounts[0].Unlimited || fields["cap"] != 0.3 || fields["unlimited"] != false || len(fields) != 5 {
		t.Fatalf("a capped account is %s", raw)
	}
	// The watcher decodes an unlimited account as no cap at all.
	var decoded BudgetAccount
	if err := json.Unmarshal([]byte(`{"kind":"jev","cap":null,"spent":5,"held":1,"unlimited":true}`), &decoded); err != nil || !decoded.Unlimited || decoded.Cap != 0 {
		t.Fatal(decoded, err)
	}
}
