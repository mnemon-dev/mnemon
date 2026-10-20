package memoryagent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	byteorder "encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// ModelCache records model replies and replays them to identical requests
// (Config.ModelCache). It is an evaluation tool, never for production: the
// arms of an ablation that share one cache get the base run's answers to the
// requests they share, so they differ only where they send different
// requests. A replayed answer is not a fresh one, and the cache keeps model
// replies, which quote the evidence they were given.
//
// A reply is keyed by the request exactly as it would be sent (replayKey).
// Only replies the call returned are recorded: never errors, refusals,
// unreadable or cut-off replies. Several processes share one file (WAL, with
// a busy timeout): two that miss the same request at once both call the
// model and both store, and the last write wins.
type ModelCache struct {
	db *sql.DB
}

// modelCacheFormat names the replay key's layout; a change of what a key
// covers changes it, so old entries stop matching instead of misleading.
const modelCacheFormat = "mnemon-model-cache/v1"

// OpenModelCache opens or creates the cache file at path, which must be
// absolute and private, like the budget file.
func OpenModelCache(path string) (*ModelCache, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("model cache must be an absolute path")
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
		return nil, errors.New("model cache must be a private regular file")
	}
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("_pragma", "busy_timeout(10000)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err = walMode(db); err == nil {
		_, err = db.Exec(`CREATE TABLE IF NOT EXISTS replies(key TEXT PRIMARY KEY,body BLOB NOT NULL)`)
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return &ModelCache{db: db}, nil
}

// walMode puts the file in write-ahead-log mode. Processes that open a new
// file together race to switch it, and SQLite refuses a losing switch at
// once instead of waiting out the busy timeout (waiting could deadlock), so
// a refused switch is retried until the busy timeout has passed.
func walMode(db *sql.DB) error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		var mode string
		err := db.QueryRow(`PRAGMA journal_mode=WAL`).Scan(&mode)
		switch {
		case err == nil && mode == "wal":
			return nil
		case err == nil:
			return fmt.Errorf("model cache journal mode is %q, not wal", mode)
		case !sqliteBusy(err) || time.Now().After(deadline):
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// sqliteBusy reports whether err is SQLite's SQLITE_BUSY, extended codes
// included.
func sqliteBusy(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff == sqlite3.SQLITE_BUSY
}

// Close closes the file, after the last call of the models that share it.
func (c *ModelCache) Close() error { return c.db.Close() }

// lookup returns the reply recorded for key. A cache that cannot be read
// finds nothing: the call goes to the model as without a cache.
func (c *ModelCache) lookup(ctx context.Context, key string) ([]byte, bool) {
	var body []byte
	if err := c.db.QueryRowContext(ctx, `SELECT body FROM replies WHERE key=?`, key).Scan(&body); err != nil {
		return nil, false
	}
	return body, true
}

// store records body as the reply to key, replacing an earlier one. It runs
// even when the caller's context has ended: the reply was paid for.
func (c *ModelCache) store(ctx context.Context, key string, body []byte) error {
	_, err := c.db.ExecContext(context.WithoutCancel(ctx), `INSERT INTO replies(key,body) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET body=excluded.body`, key, body)
	return err
}

// replayKey is a request's cache key: sha256 over the key format, the
// provider kind, the endpoint, the model and the exact body sent, each
// length-prefixed so that no two requests share a key.
func replayKey(kind, endpoint, model string, body []byte) string {
	h := sha256.New()
	for _, part := range [][]byte{[]byte(modelCacheFormat), []byte(kind), []byte(endpoint), []byte(model), body} {
		h.Write(byteorder.BigEndian.AppendUint64(nil, uint64(len(part))))
		h.Write(part)
	}
	return hex.EncodeToString(h.Sum(nil))
}
