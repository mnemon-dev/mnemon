package memoryagent

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net/url"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// Budget is a durable prepare/call/settle boundary, shared across processes.
// Ambiguous calls retain their reservation; calls that provably cost nothing
// (never sent, or refused by the endpoint) release it; no automatic retry
// occurs. It stores metering and request hashes, never prompts or credentials.
type Budget struct {
	db   *sql.DB
	caps map[string]float64
}

// ErrBudgetCap is the error of a reservation the account's cap refuses.
var ErrBudgetCap = errors.New("model spending cap reached, including unresolved reservations")

// Paid accounts by provider kind. Caps are USD for jev and openai, CNY for
// deepseek. A reservation is at least the flat amount held for one bounded
// request; larger completions hold more (Provider.reservation).
var (
	defaultCaps  = map[string]float64{"jev": 10, "deepseek": 50, "openai": 5}
	reservations = map[string]float64{"jev": 0.01, "deepseek": 0.25, "openai": 0.01}
)

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
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("budget file must be a private regular file")
	}
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("_pragma", "busy_timeout(10000)")
	// Reserve the writer before reading an attempt. Deferred transactions can
	// deadlock when two processes both upgrade settlement reads to writes.
	q.Set("_txlock", "immediate")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS accounts(kind TEXT PRIMARY KEY,cap REAL NOT NULL,spent REAL NOT NULL DEFAULT 0,held REAL NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS attempts(id TEXT PRIMARY KEY,kind TEXT NOT NULL,request_hash TEXT NOT NULL,reserved REAL NOT NULL,actual REAL,input_tokens INTEGER,output_tokens INTEGER);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Budget{db: db, caps: resolved}, nil
}

func (b *Budget) Close() error { return b.db.Close() }

func (b *Budget) reserve(ctx context.Context, kind, hash string) (string, error) {
	return b.reserveAmount(ctx, kind, hash, 0)
}

// reserveAmount holds amount for one call, at least the kind's flat
// reservation, and returns the attempt's id; "" for a local model.
func (b *Budget) reserveAmount(ctx context.Context, kind, hash string, amount float64) (string, error) {
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
	result, err := tx.ExecContext(ctx, `UPDATE accounts SET held=held+? WHERE kind=? AND spent+held+?<=cap`, reservation, kind, reservation)
	if err != nil {
		return "", err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return "", ErrBudgetCap
	}
	id := uuid.NewString()
	if _, err = tx.ExecContext(ctx, `INSERT INTO attempts(id,kind,request_hash,reserved) VALUES(?,?,?,?)`, id, kind, hash, reservation); err != nil {
		return "", err
	}
	return id, tx.Commit()
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
		return errors.New("invalid model metering; reservation retained")
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
