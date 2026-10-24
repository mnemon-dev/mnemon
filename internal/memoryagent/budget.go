package memoryagent

import (
	"context"
	"database/sql"
	"errors"
	"maps"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/google/uuid"
	"github.com/mnemon-dev/mnemon/internal/records"
	_ "modernc.org/sqlite"
)

// Budget is a durable prepare/call/settle boundary, shared across processes.
// Every attempt of a call reserves on its own (the provider retries through
// it, resilience.go), recording the input estimate its request settles at
// if no metering comes back. Attempts that provably cost nothing (never
// sent, or refused by the endpoint) release their reservation; the others
// settle it when their request ends, at the reply's metering or, when none
// came back, at the request's input estimate. Only a request still in flight
// holds one, or one whose process ended before it did, which SettleHolds
// settles at the estimate it recorded. It stores metering and request
// hashes, never prompts or credentials.
type Budget struct {
	db   *sql.DB
	caps map[string]float64
}

// ErrBudgetCap is the error of a reservation the account's cap refuses.
var ErrBudgetCap = errors.New("model spending cap reached, including unresolved reservations")

// errInvalidMetering is the error of a settlement whose metering the budget
// refuses: no input tokens, negative amounts, or a cost above the
// reservation of a kind whose calls it bounds. The reservation is left as it
// was.
var errInvalidMetering = errors.New("invalid model metering")

// Paid accounts by provider kind. Caps are USD for jev and openai, CNY for
// deepseek (currencies). A reservation is at least the flat amount held for
// one bounded request; larger completions hold more (Provider.reservation).
var (
	defaultCaps  = map[string]float64{"jev": 10, "deepseek": 50, "openai": 5}
	reservations = map[string]float64{"jev": 0.01, "deepseek": 0.25, "openai": 0.01}
	currencies   = map[string]string{"jev": "USD", "deepseek": "CNY", "openai": "USD"}
)

// posixModes says whether a file's permission bits say who may read it, as
// POSIX systems' do. On Windows they do not: Go reports 0666 for any writable
// file and 0444 for a read-only one, whoever its ACL lets in, so the budget
// file and the model cache are checked to be private only elsewhere (ruling
// R89). Tests set it.
var posixModes = runtime.GOOS != "windows"

// privateFile reports whether info is of a regular file that, where POSIX
// modes mean something, neither its group nor others may read or write.
func privateFile(info os.FileInfo) bool {
	return info.Mode().IsRegular() && (!posixModes || info.Mode().Perm()&0o077 == 0)
}

// PaidKinds are the provider kinds a budget accounts (its caps' kinds), each
// with the currency its caps, spending and costs count in. The map is the
// caller's.
func PaidKinds() map[string]string { return maps.Clone(currencies) }

func OpenBudget(path string) (*Budget, error) { return OpenBudgetWithCaps(path, nil) }

// OpenBudgetWithCaps opens the shared budget file with operator-configured
// caps by provider kind; kinds missing from caps keep their defaults. The
// configured cap replaces whatever cap an earlier process stored.
func OpenBudgetWithCaps(path string, caps map[string]float64) (*Budget, error) {
	resolved := map[string]float64{}
	for kind, cap := range defaultCaps {
		resolved[kind] = cap
	}
	for kind, cap := range caps {
		if _, ok := defaultCaps[kind]; !ok {
			return nil, errors.New("budget cap kind must be jev, deepseek or openai")
		}
		if math.IsNaN(cap) || math.IsInf(cap, 0) || cap < 0 {
			return nil, errors.New("budget cap must be a finite nonnegative amount")
		}
		resolved[kind] = cap
	}
	if !filepath.IsAbs(path) {
		return nil, errors.New("budget file must be an absolute path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err == nil {
		f.Close()
	} else if !os.IsExist(err) {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil || !privateFile(info) {
		return nil, errors.New("budget file must be a private regular file")
	}
	db, err := sql.Open("sqlite", budgetDSN(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS accounts(kind TEXT PRIMARY KEY,cap REAL NOT NULL,spent REAL NOT NULL DEFAULT 0,held REAL NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS attempts(id TEXT PRIMARY KEY,kind TEXT NOT NULL,request_hash TEXT NOT NULL,reserved REAL NOT NULL,actual REAL,input_tokens INTEGER,output_tokens INTEGER);`)
	if err == nil {
		err = addEstimateColumns(db)
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Budget{db: db, caps: resolved}, nil
}

// budgetDSN is the DSN of the budget file at path (records.SQLiteURI).
func budgetDSN(path string) string {
	q := url.Values{}
	q.Set("_pragma", "busy_timeout(10000)")
	// Reserve the writer before reading an attempt. Deferred transactions can
	// deadlock when two processes both upgrade settlement reads to writes.
	q.Set("_txlock", "immediate")
	return records.SQLiteURI(path, q)
}

// addEstimateColumns gives the attempts of a budget file made before they
// recorded their input estimates the columns that hold one (estimate, its
// cost, and estimate_tokens); an attempt reserved without one holds NULL
// there. Processes that open the file at once may both add a column: the
// one that loses finds it added.
func addEstimateColumns(db *sql.DB) error {
	rows, err := db.Query(`SELECT name FROM pragma_table_info('attempts')`)
	if err != nil {
		return err
	}
	columns := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		columns[name] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, column := range []string{"estimate REAL", "estimate_tokens INTEGER"} {
		name, _, _ := strings.Cut(column, " ")
		if columns[name] {
			continue
		}
		if _, err := db.Exec(`ALTER TABLE attempts ADD COLUMN ` + column); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return err
		}
	}
	return nil
}

func (b *Budget) Close() error { return b.db.Close() }

func (b *Budget) reserve(ctx context.Context, kind, hash string) (string, error) {
	return b.reserveAmount(ctx, kind, hash, 0)
}

// reserveAmount holds amount for one call, at least the kind's flat
// reservation, and returns the attempt's id; "" for a local model.
func (b *Budget) reserveAmount(ctx context.Context, kind, hash string, amount float64) (string, error) {
	return b.reserveWithin(ctx, kind, hash, amount, 1, Usage{})
}

// reserveWithin is reserveAmount for a call that may fill only share of its
// account's cap: a reservation the cap admits but the share does not fails
// with ErrBackgroundReserve. The attempt records estimate, what its request
// settles at when no metering the budget accepts comes back
// (Provider.inputEstimate), for SettleHolds; one with no input tokens is
// none, and the attempt records no estimate.
func (b *Budget) reserveWithin(ctx context.Context, kind, hash string, amount, share float64, estimate Usage) (string, error) {
	if kind == "local" {
		return "", nil
	}
	if b == nil {
		return "", errors.New("paid model requires a durable budget file")
	}
	// Flat reservations cover a bounded request: JEV bodies <=96k units
	// (output is free), DeepSeek <=48k bytes plus its default output limits,
	// OpenAI a typical call (settle records larger metered calls at their
	// real price). Larger completions pass the amount they could cost.
	cap, ok := b.caps[kind]
	if !ok {
		return "", errors.New("unknown paid model kind")
	}
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 {
		return "", errors.New("invalid reservation amount")
	}
	reservation := max(reservations[kind], amount)
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO accounts(kind,cap) VALUES(?,?) ON CONFLICT(kind) DO UPDATE SET cap=excluded.cap`, kind, cap)
	if err != nil {
		return "", err
	}
	result, err := tx.ExecContext(ctx, `UPDATE accounts SET held=held+? WHERE kind=? AND spent+held+?<=cap*?`, reservation, kind, reservation, share)
	if err != nil {
		return "", err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return "", b.refusal(ctx, tx, kind, reservation, share)
	}
	var estimateCost, estimateTokens any // NULL: no estimate
	if estimate.InputTokens >= 1 {
		estimateCost, estimateTokens = min(estimate.USD+estimate.CNY, reservation), estimate.InputTokens
	}
	id := uuid.NewString()
	if _, err = tx.ExecContext(ctx, `INSERT INTO attempts(id,kind,request_hash,reserved,estimate,estimate_tokens) VALUES(?,?,?,?,?,?)`, id, kind, hash, reservation, estimateCost, estimateTokens); err != nil {
		return "", err
	}
	return id, tx.Commit()
}

// refusal is the error of a reservation the account refused within share of
// its cap: ErrBackgroundReserve when the whole cap would have admitted it,
// else ErrBudgetCap.
func (b *Budget) refusal(ctx context.Context, tx *sql.Tx, kind string, reservation, share float64) error {
	if share >= 1 {
		return ErrBudgetCap
	}
	var fits bool
	if err := tx.QueryRowContext(ctx, `SELECT spent+held+?<=cap FROM accounts WHERE kind=?`, reservation, kind).Scan(&fits); err != nil || !fits {
		return ErrBudgetCap
	}
	return ErrBackgroundReserve
}

func (b *Budget) settle(ctx context.Context, id string, usage Usage) error {
	if id == "" {
		return nil
	}
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var kind string
	var held float64
	if err = tx.QueryRowContext(ctx, `SELECT kind,reserved FROM attempts WHERE id=? AND actual IS NULL`, id).Scan(&kind, &held); err != nil {
		return err
	}
	cost := usage.USD + usage.CNY
	// A completed openai call is recorded at its metered price even when it
	// exceeds the flat reservation: the money is spent, and the cap blocks the
	// next reservation. Other kinds are bounded by their reservation.
	if cost < 0 || cost > held && kind != "openai" || usage.InputTokens < 1 || usage.OutputTokens < 0 {
		return errInvalidMetering
	}
	result, err := tx.ExecContext(ctx, `UPDATE attempts SET actual=?,input_tokens=?,output_tokens=? WHERE id=? AND actual IS NULL`, cost, usage.InputTokens, usage.OutputTokens, id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return errors.New("model settlement changed concurrently")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE accounts SET held=held-?,spent=spent+? WHERE kind=?`, held, cost, kind); err != nil {
		return err
	}
	return tx.Commit()
}

// release ends an attempt that cost nothing: the request was never sent, or
// the endpoint refused it. Its reservation is freed and it is recorded at 0.
func (b *Budget) release(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var kind string
	var held float64
	if err = tx.QueryRowContext(ctx, `SELECT kind,reserved FROM attempts WHERE id=? AND actual IS NULL`, id).Scan(&kind, &held); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE attempts SET actual=0,input_tokens=0,output_tokens=0 WHERE id=? AND actual IS NULL`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE accounts SET held=held-? WHERE kind=?`, held, kind); err != nil {
		return err
	}
	return tx.Commit()
}

// HeldSettled is what Budget.SettleHolds settled of one paid account: how
// many reservations were held, what they held, and what they settled at.
type HeldSettled struct {
	Kind          string
	Holds         int
	Held, Settled float64
}

// SettleHolds settles every reservation the budget file still holds: each at
// the input estimate its attempt recorded (at most its reservation), or at
// its reservation when it recorded none, as an attempt reserved before
// estimates were recorded did; and it sets each account's held to what its
// attempts still hold, nothing. They are the holds of requests that a
// process which has ended left in flight (ruling R90): a request that may
// have been processed, never known to have cost nothing, so a settlement,
// not a release. It returns what it settled, by kind in kind order; nothing
// when nothing was held.
//
// It cannot tell the holds of a process that has ended from those of one
// that runs: call it only as the file's one writer, before reserving, as
// aml-serve does under its data root's lock for the instance's own budget
// file. It runs in one transaction.
func (b *Budget) SettleHolds(ctx context.Context) ([]HeldSettled, error) {
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id,kind,reserved,estimate,estimate_tokens FROM attempts WHERE actual IS NULL ORDER BY kind,rowid`)
	if err != nil {
		return nil, err
	}
	type hold struct {
		id, kind string
		reserved float64
		estimate sql.NullFloat64
		tokens   sql.NullInt64
	}
	holds := []hold{}
	for rows.Next() {
		var h hold
		if err := rows.Scan(&h.id, &h.kind, &h.reserved, &h.estimate, &h.tokens); err != nil {
			rows.Close()
			return nil, err
		}
		holds = append(holds, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	settled := []HeldSettled{}
	for _, h := range holds {
		cost, tokens := h.reserved, int64(1)
		if h.estimate.Valid {
			cost = min(max(h.estimate.Float64, 0), h.reserved)
		}
		if h.tokens.Valid {
			tokens = max(h.tokens.Int64, 1)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE attempts SET actual=?,input_tokens=?,output_tokens=0 WHERE id=? AND actual IS NULL`, cost, tokens, h.id); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET spent=spent+? WHERE kind=?`, cost, h.kind); err != nil {
			return nil, err
		}
		if n := len(settled); n == 0 || settled[n-1].Kind != h.kind {
			settled = append(settled, HeldSettled{Kind: h.kind})
		}
		account := &settled[len(settled)-1]
		account.Holds++
		account.Held += h.reserved
		account.Settled += cost
	}
	// What the attempts still hold, nothing: no rounding of the held sum is
	// left behind.
	if _, err := tx.ExecContext(ctx, `UPDATE accounts SET held=(SELECT COALESCE(SUM(reserved),0) FROM attempts WHERE attempts.kind=accounts.kind AND actual IS NULL)`); err != nil {
		return nil, err
	}
	return settled, tx.Commit()
}

// BudgetAccount is one paid account of a budget file: the cap this process
// enforces, what calls spent and what unresolved calls hold.
type BudgetAccount struct {
	Kind  string  `json:"kind"`
	Cap   float64 `json:"cap"`
	Spent float64 `json:"spent"`
	Held  float64 `json:"held"`
}

// Accounts reads the accounts calls have reserved on, by kind; it changes
// nothing.
func (b *Budget) Accounts(ctx context.Context) ([]BudgetAccount, error) {
	rows, err := b.db.QueryContext(ctx, `SELECT kind,spent,held FROM accounts ORDER BY kind`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BudgetAccount{}
	for rows.Next() {
		var account BudgetAccount
		if err := rows.Scan(&account.Kind, &account.Spent, &account.Held); err != nil {
			return nil, err
		}
		account.Cap = b.caps[account.Kind]
		out = append(out, account)
	}
	return out, rows.Err()
}
