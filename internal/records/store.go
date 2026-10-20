// Package records owns the raw-record channel. It never opens mnemon.db or
// imports Memory policy; JSON and retrieval texts arrive verbatim from TS.
package records

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"

	_ "modernc.org/sqlite"
)

var ErrConflict = errors.New("Record revision changed; refresh before saving")

const emptyRevision = "4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945"

type Upsert struct {
	RecordJSON string `json:"recordJSON"`
	SearchText string `json:"searchText"`
	VectorText string `json:"vectorText"`
}

type PutRequest struct {
	ExpectedRevision *string  `json:"expectedRevision"`
	Upsert           []Upsert `json:"upsert"`
	Delete           []string `json:"delete"`
}

type GetRequest struct {
	Revision string   `json:"revision,omitempty"`
	IDs      []string `json:"ids,omitempty"`
}

// Records are strings so the Go JSON encoder cannot reinterpret record JSON.
type Snapshot struct {
	Revision string   `json:"revision"`
	Records  []string `json:"records"`
}

type record struct {
	ID                          string                     `json:"id"`
	Kind                        string                     `json:"kind"`
	Scope                       string                     `json:"scope"`
	WorkspaceID                 *string                    `json:"workspaceId"`
	SessionID                   *string                    `json:"sessionId"`
	Date                        *string                    `json:"date"`
	State                       string                     `json:"state"`
	CreatedAt                   string                     `json:"createdAt"`
	Data                        map[string]json.RawMessage `json:"data"`
	Raw, SearchText, VectorText string                     `json:"-"`
	SearchLower                 string                     `json:"-"`
	Seq                         int64                      `json:"-"`
	VersionID                   int64                      `json:"-"`
	TokenCount                  int                        `json:"-"`
}

// Match JS's case-sensitive property lookup. Extra record properties are opaque.
func (r *record) decode(raw string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return err
	}
	for _, field := range []struct {
		name  string
		value any
	}{
		{"id", &r.ID}, {"kind", &r.Kind}, {"scope", &r.Scope},
		{"workspaceId", &r.WorkspaceID}, {"sessionId", &r.SessionID},
		{"date", &r.Date}, {"state", &r.State}, {"createdAt", &r.CreatedAt}, {"data", &r.Data},
	} {
		if value, ok := fields[field.name]; ok {
			if err := json.Unmarshal(value, field.value); err != nil {
				return err
			}
		}
	}
	return nil
}

type Store struct {
	db       *sql.DB
	readers  *sql.DB // Serve-owned read-only pool; writes retain db's single connection
	path     string
	readonly bool
	cache    *queryCache // enabled only for the lifetime of Serve
	fts      bool
	// running is set while a query runtime (StartRuntime) runs.
	running atomic.Bool
	// sessionCache keeps the sessions Adjacent reads (neighbors.go);
	// sessionBuilds counts how many it built.
	sessionCache  sessionCache
	sessionBuilds atomic.Int64
}

func (s *Store) ReadOnly() bool { return s.readonly }

func Open(directory string, readonly bool) (*Store, error) {
	if !readonly {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, err
		}
	}
	path, err := filepath.Abs(filepath.Join(directory, "records.db"))
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteDSN(path, readonly))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, path: path, readonly: readonly}
	if err = db.Ping(); err == nil && !readonly {
		err = s.initialize()
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	var tables int
	if err = db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name IN ('lexical_documents','lexical_terms')").Scan(&tables); err != nil {
		db.Close()
		return nil, err
	}
	s.fts = tables == 2
	return s, nil
}

func sqliteDSN(path string, readonly bool) string {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("_pragma", "busy_timeout(10000)")
	q.Set("_txlock", "immediate")
	if readonly {
		q.Set("mode", "ro")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (s *Store) readDB() *sql.DB {
	if s.readers != nil {
		return s.readers
	}
	return s.db
}

func (s *Store) initialize() error {
	_, err := s.db.Exec(`PRAGMA journal_mode=WAL;
CREATE TABLE IF NOT EXISTS meta(key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS record_versions(
 id TEXT NOT NULL, seq INTEGER NOT NULL, rev_from INTEGER NOT NULL, rev_to INTEGER,
 record_json TEXT NOT NULL, search_text TEXT NOT NULL, vector_text TEXT NOT NULL,
 kind TEXT NOT NULL, scope TEXT NOT NULL, workspace_id TEXT, session_id TEXT,
 date TEXT, state TEXT NOT NULL, status_json TEXT, created_at TEXT NOT NULL,
 PRIMARY KEY(id,rev_from));
CREATE INDEX IF NOT EXISTS record_versions_revision ON record_versions(rev_from,rev_to,seq);
CREATE UNIQUE INDEX IF NOT EXISTS record_versions_current ON record_versions(id) WHERE rev_to IS NULL;
CREATE TABLE IF NOT EXISTS embeddings(text_hash TEXT NOT NULL, model TEXT NOT NULL, prefix TEXT NOT NULL,
 vector BLOB NOT NULL, PRIMARY KEY(text_hash,model,prefix));
CREATE TABLE IF NOT EXISTS lexical_documents(version_id INTEGER PRIMARY KEY, tokens TEXT NOT NULL, token_count INTEGER NOT NULL);
CREATE VIRTUAL TABLE IF NOT EXISTS lexical_fts USING fts5(tokens, content='lexical_documents', content_rowid='version_id',
 tokenize="unicode61 remove_diacritics 0 categories 'L* N* M* S* Co Cn' tokenchars '-_:'");
CREATE VIRTUAL TABLE IF NOT EXISTS lexical_terms USING fts5vocab(lexical_fts, 'instance');
` + derivedSchema + `
INSERT OR IGNORE INTO meta VALUES('schema','1'),('tokenizer','ts-v1'),('revision','0'),('seq','0'),
 ('digest','` + emptyRevision + `'),('revision/` + emptyRevision + `','0');`)
	return err
}

func (s *Store) Close() error { return s.db.Close() }

type reader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func revision(ctx context.Context, db reader, requested string) (int64, string, error) {
	var number string
	if requested == "" {
		if err := db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key='digest'").Scan(&requested); err != nil {
			return 0, "", err
		}
	}
	err := db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key=?", "revision/"+requested).Scan(&number)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", errors.New("Unknown record revision")
	}
	if err != nil {
		return 0, "", err
	}
	n, err := strconv.ParseInt(number, 10, 64)
	return n, requested, err
}

func load(ctx context.Context, db reader, rev int64) ([]record, error) {
	rows, err := db.QueryContext(ctx, `SELECT record_json,search_text,vector_text,seq FROM record_versions
WHERE rev_from<=? AND (rev_to IS NULL OR rev_to>?) ORDER BY seq`, rev, rev)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []record{}
	for rows.Next() {
		var r record
		if err := rows.Scan(&r.Raw, &r.SearchText, &r.VectorText, &r.Seq); err != nil {
			return nil, err
		}
		if err := r.decode(r.Raw); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) Get(ctx context.Context, req GetRequest) (Snapshot, error) {
	rev, hash, err := revision(ctx, s.readDB(), req.Revision)
	if err != nil {
		return Snapshot{}, err
	}
	return s.readRecords(ctx, rev, hash, req.IDs)
}

func parseUpserts(values []Upsert) ([]record, error) {
	out := make([]record, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		var r record
		if err := r.decode(value.RecordJSON); err != nil {
			return nil, fmt.Errorf("invalid record JSON: %w", err)
		}
		if r.ID == "" || r.Kind == "" || r.Scope == "" || r.State == "" || r.CreatedAt == "" || seen[r.ID] {
			return nil, errors.New("Invalid or duplicate record")
		}
		seen[r.ID] = true
		r.Raw, r.SearchText, r.VectorText = value.RecordJSON, value.SearchText, value.VectorText
		out = append(out, r)
	}
	return out, nil
}

func (s *Store) Put(ctx context.Context, req PutRequest) (string, error) {
	if s.readonly {
		return "", errors.New("records put is unavailable with --readonly")
	}
	values, err := parseUpserts(req.Upsert)
	if err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	rev, hash, err := revision(ctx, tx, "")
	if err != nil {
		return "", err
	}
	if req.ExpectedRevision != nil && *req.ExpectedRevision != hash {
		return "", ErrConflict
	}
	var seq int64
	if err := tx.QueryRowContext(ctx, "SELECT value FROM meta WHERE key='seq'").Scan(&seq); err != nil {
		return "", err
	}
	for _, id := range req.Delete {
		if _, err := tx.ExecContext(ctx, "UPDATE record_versions SET rev_to=? WHERE id=? AND rev_to IS NULL", rev+1, id); err != nil {
			return "", err
		}
	}
	for _, r := range values {
		if err := writeVersion(ctx, tx, r, rev+1, &seq); err != nil {
			return "", err
		}
	}
	all, err := load(ctx, tx, rev+1)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write([]byte("["))
	for i, r := range all {
		if i > 0 {
			h.Write([]byte(","))
		}
		h.Write([]byte(r.Raw))
	}
	h.Write([]byte("]"))
	hash = hex.EncodeToString(h.Sum(nil))
	for _, entry := range [][2]string{{"revision", strconv.FormatInt(rev+1, 10)}, {"seq", strconv.FormatInt(seq, 10)}, {"digest", hash}, {"revision/" + hash, strconv.FormatInt(rev+1, 10)}} {
		if _, err := tx.ExecContext(ctx, "INSERT INTO meta VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", entry[0], entry[1]); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	s.wakeLexical() // a running query runtime indexes the new versions
	return hash, nil
}

func writeVersion(ctx context.Context, tx *sql.Tx, r record, rev int64, next *int64) error {
	var seq int64
	err := tx.QueryRowContext(ctx, "SELECT seq FROM record_versions WHERE id=? AND rev_to IS NULL", r.ID).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		*next++
		seq = *next
	} else if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE record_versions SET rev_to=? WHERE id=? AND rev_to IS NULL", rev, r.ID); err != nil {
		return err
	}
	var status any
	if v, ok := r.Data["status"]; ok {
		status = string(v)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO record_versions VALUES(?,?,?,NULL,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, seq, rev, r.Raw, r.SearchText, r.VectorText, r.Kind, r.Scope, r.WorkspaceID, r.SessionID, r.Date, r.State, status, r.CreatedAt)
	return err
}

func textHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func scalarEqual(a, b json.RawMessage) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	switch av.(type) {
	case map[string]any, []any:
		return false
	}
	switch bv.(type) {
	case map[string]any, []any:
		return false
	}
	return av == bv
}

// JS lowercasing expands U+0130 to i + combining dot; Go's simple mapping does
// not. Keep the separator so ASCII word boundaries match the frozen TS source.
func lower(text string) string { return strings.ToLower(strings.ReplaceAll(text, "İ", "i\u0307")) }
