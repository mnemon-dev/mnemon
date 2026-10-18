package memoryagent

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// Budget is a durable prepare/call/settle boundary, shared across processes.
// Ambiguous or failed calls retain their reservation; no automatic retry occurs.
// It stores metering and request hashes, never prompts or credentials.
type Budget struct{ db *sql.DB }

func OpenBudget(path string) (*Budget, error) {
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
	return &Budget{db: db}, nil
}

func (b *Budget) Close() error { return b.db.Close() }

func (b *Budget) reserve(ctx context.Context, kind, hash string) (string, error) {
	if kind == "local" {
		return "", nil
	}
	if b == nil {
		return "", errors.New("paid model requires a durable budget file")
	}
	cap, reservation := 10.0, 0.01 // USD; body <=48k bytes, JEV output is free.
	if kind == "deepseek" {
		cap, reservation = 50, 0.25 // CNY; <=48k body bytes and 2048 output tokens.
	}
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO accounts(kind,cap) VALUES(?,?) ON CONFLICT(kind) DO UPDATE SET cap=MIN(cap,excluded.cap)`, kind, cap)
	if err != nil {
		return "", err
	}
	result, err := tx.ExecContext(ctx, `UPDATE accounts SET held=held+? WHERE kind=? AND spent+held+?<=cap`, reservation, kind, reservation)
	if err != nil {
		return "", err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return "", errors.New("model spending cap reached, including unresolved reservations")
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
	if cost < 0 || cost > held || usage.InputTokens < 1 || usage.OutputTokens < 0 {
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
