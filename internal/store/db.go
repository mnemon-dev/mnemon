package store

import (
	"database/sql"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mnemon-dev/mnemon/internal/embed"
	_ "modernc.org/sqlite"
)

// DefaultStoreName is the fallback store when none is specified.
const DefaultStoreName = "default"

const embeddingFloat32UserVersion = 1

var validStoreNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// dbExecer abstracts sql.DB and sql.Tx so store methods work in both contexts.
type dbExecer interface {
	Exec(string, ...any) (sql.Result, error)
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

// DB wraps the SQLite database connection.
type DB struct {
	conn     *sql.DB
	tx       *sql.Tx // current active transaction (nil = no transaction)
	path     string
	readOnly bool
}

// IsReadOnly returns true if the database was opened in read-only mode.
func (db *DB) IsReadOnly() bool { return db.readOnly }

// execer returns the active transaction if set, otherwise the raw connection.
func (db *DB) execer() dbExecer {
	if db.tx != nil {
		return db.tx
	}
	return db.conn
}

// InTransaction runs fn inside a single SQL transaction.
// All store methods called within fn will use the transaction automatically.
func (db *DB) InTransaction(fn func() error) error {
	if db.tx != nil {
		return fmt.Errorf("nested transactions not supported")
	}
	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	db.tx = tx
	defer func() { db.tx = nil }()
	if err := fn(); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// DefaultDataDir returns ~/.mnemon.
func DefaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		home = "/tmp"
	}
	return filepath.Join(home, ".mnemon")
}

// ValidStoreName returns true if name matches [a-zA-Z0-9][a-zA-Z0-9_-]*.
func ValidStoreName(name string) bool {
	return validStoreNameRe.MatchString(name)
}

// StoreDir returns <baseDir>/data/<name>.
func StoreDir(baseDir, name string) string {
	return filepath.Join(baseDir, "data", name)
}

// ActiveFile returns the path to <baseDir>/active.
func ActiveFile(baseDir string) string {
	return filepath.Join(baseDir, "active")
}

// ReadActive reads the active store name from <baseDir>/active.
// Returns DefaultStoreName if the file doesn't exist or is empty.
func ReadActive(baseDir string) string {
	data, err := os.ReadFile(ActiveFile(baseDir))
	if err != nil {
		return DefaultStoreName
	}
	name := strings.TrimSpace(string(data))
	if name == "" {
		return DefaultStoreName
	}
	if !ValidStoreName(name) {
		return DefaultStoreName
	}
	return name
}

// WriteActive writes the active store name to <baseDir>/active.
func WriteActive(baseDir, name string) error {
	if !ValidStoreName(name) {
		return fmt.Errorf("invalid store name %q", name)
	}
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(ActiveFile(baseDir), []byte(name+"\n"), 0o644)
}

// ListStores returns sorted names of all stores under <baseDir>/data/.
func ListStores(baseDir string) ([]string, error) {
	dataDir := filepath.Join(baseDir, "data")
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && ValidStoreName(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// StoreExists checks whether the named store directory exists.
func StoreExists(baseDir, name string) bool {
	if !ValidStoreName(name) {
		return false
	}
	fi, err := os.Stat(StoreDir(baseDir, name))
	return err == nil && fi.IsDir()
}

// MigrateIfNeeded moves a legacy ~/.mnemon/mnemon.db into the new
// data/default/ layout. It is safe to call multiple times.
func MigrateIfNeeded(baseDir string) error {
	oldDB := filepath.Join(baseDir, "mnemon.db")
	newDir := StoreDir(baseDir, DefaultStoreName)
	newDB := filepath.Join(newDir, "mnemon.db")

	// Also check for WAL/SHM files
	oldWAL := oldDB + "-wal"
	oldSHM := oldDB + "-shm"

	// Already migrated or fresh install
	if _, err := os.Stat(oldDB); os.IsNotExist(err) {
		return nil
	}
	// If new layout already exists, don't overwrite
	if _, err := os.Stat(newDB); err == nil {
		return nil
	}

	if err := os.MkdirAll(newDir, 0o755); err != nil {
		return fmt.Errorf("create default store dir: %w", err)
	}
	if err := os.Rename(oldDB, newDB); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}

	// Move WAL and SHM if they exist
	if err := renameIfExists(oldWAL, newDB+"-wal"); err != nil {
		return fmt.Errorf("migrate WAL: %w", err)
	}
	if err := renameIfExists(oldSHM, newDB+"-shm"); err != nil {
		return fmt.Errorf("migrate SHM: %w", err)
	}

	fmt.Fprintf(os.Stderr, "mnemon: migrated database to %s\n", newDB)
	return nil
}

func renameIfExists(oldPath, newPath string) error {
	if _, err := os.Stat(oldPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.Rename(oldPath, newPath)
}

// OpenReadOnly opens the SQLite database in read-only mode.
// Safe for read-only filesystem mounts: uses journal_mode=OFF to avoid
// writing WAL/SHM sidecar files.
func OpenReadOnly(dataDir string) (*DB, error) {
	dbPath := filepath.Join(dataDir, "mnemon.db")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("database not found: %s", dbPath)
	}

	conn, err := sql.Open("sqlite", dbPath+"?mode=ro&_pragma=journal_mode(OFF)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("open readonly database: %w", err)
	}
	conn.SetMaxOpenConns(1)
	return &DB{conn: conn, path: dbPath, readOnly: true}, nil
}

// Open opens (or creates) the SQLite database at the given directory.
func Open(dataDir string) (*DB, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	dbPath := filepath.Join(dataDir, "mnemon.db")
	conn, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	conn.SetMaxOpenConns(1) // SQLite single-writer

	db := &DB{conn: conn, path: dbPath}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

// Close closes the database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}

// Path returns the database file path.
func (db *DB) Path() string {
	return db.path
}

// Conn returns the underlying sql.DB for advanced queries.
func (db *DB) Conn() *sql.DB {
	return db.conn
}

func (db *DB) migrate() error {
	schema := `
CREATE TABLE IF NOT EXISTS insights (
    id          TEXT PRIMARY KEY,
    content     TEXT NOT NULL,
    category    TEXT DEFAULT 'general',
    importance  INTEGER DEFAULT 3,
    tags        TEXT DEFAULT '[]',
    entities    TEXT DEFAULT '[]',
    source      TEXT DEFAULT 'user',
    access_count INTEGER DEFAULT 0,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    deleted_at  TEXT
);

CREATE TABLE IF NOT EXISTS edges (
    source_id   TEXT NOT NULL,
    target_id   TEXT NOT NULL,
    edge_type   TEXT NOT NULL CHECK(edge_type IN ('temporal','semantic','causal','entity')),
    weight      REAL DEFAULT 1.0,
    metadata    TEXT DEFAULT '{}',
    created_at  TEXT NOT NULL,
    PRIMARY KEY (source_id, target_id, edge_type),
    FOREIGN KEY (source_id) REFERENCES insights(id) ON DELETE CASCADE,
    FOREIGN KEY (target_id) REFERENCES insights(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_insights_category ON insights(category);
CREATE INDEX IF NOT EXISTS idx_insights_importance ON insights(importance);
CREATE INDEX IF NOT EXISTS idx_insights_created ON insights(created_at);
CREATE INDEX IF NOT EXISTS idx_insights_deleted ON insights(deleted_at);
CREATE INDEX IF NOT EXISTS idx_insights_source ON insights(source);
CREATE INDEX IF NOT EXISTS idx_edges_source ON edges(source_id);
CREATE INDEX IF NOT EXISTS idx_edges_target ON edges(target_id);
CREATE INDEX IF NOT EXISTS idx_edges_type ON edges(edge_type);
CREATE INDEX IF NOT EXISTS idx_edges_source_type ON edges(source_id, edge_type);
CREATE INDEX IF NOT EXISTS idx_edges_target_type ON edges(target_id, edge_type);

CREATE TABLE IF NOT EXISTS oplog (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    operation   TEXT NOT NULL,
    insight_id  TEXT,
    detail      TEXT DEFAULT '',
    created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_oplog_created ON oplog(created_at);
`
	_, err := db.conn.Exec(schema)
	if err != nil {
		return err
	}

	// Phase 2 migration: add last_accessed_at column
	if err := addColumnIfNotExists(db.conn, `ALTER TABLE insights ADD COLUMN last_accessed_at TEXT`); err != nil {
		return fmt.Errorf("add last_accessed_at: %w", err)
	}

	// Phase 3 migration: add embedding column
	if err := addColumnIfNotExists(db.conn, `ALTER TABLE insights ADD COLUMN embedding BLOB`); err != nil {
		return fmt.Errorf("add embedding: %w", err)
	}
	if err := db.migrateEmbeddingsToFloat32(); err != nil {
		return fmt.Errorf("migrate embeddings to float32: %w", err)
	}

	// Lifecycle migration: add effective_importance column
	if err := addColumnIfNotExists(db.conn, `ALTER TABLE insights ADD COLUMN effective_importance REAL DEFAULT 0.5`); err != nil {
		return fmt.Errorf("add effective_importance: %w", err)
	}
	if _, err := db.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_insights_effective_imp ON insights(effective_importance)`); err != nil {
		return fmt.Errorf("create effective_imp index: %w", err)
	}
	if _, err := db.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_prune_candidates ON insights(deleted_at, importance, access_count, effective_importance)`); err != nil {
		return fmt.Errorf("create prune_candidates index: %w", err)
	}

	// Migration: remove narrative edge type from existing databases
	if err := db.migrateRemoveNarrativeEdges(); err != nil {
		return fmt.Errorf("remove narrative edges: %w", err)
	}

	// One-time cleanup: soft-delete narrative category insights from legacy databases.
	// Only runs the UPDATE when narrative insights actually exist (avoids needless writes).
	var narrativeCount int
	_ = db.conn.QueryRow(`SELECT COUNT(*) FROM insights WHERE category = 'narrative' AND deleted_at IS NULL`).Scan(&narrativeCount)
	if narrativeCount > 0 {
		if _, err := db.conn.Exec(`UPDATE insights SET deleted_at = datetime('now') WHERE category = 'narrative' AND deleted_at IS NULL`); err != nil {
			return fmt.Errorf("clean narrative insights: %w", err)
		}
	}

	return nil
}

// addColumnIfNotExists runs an ALTER TABLE ADD COLUMN statement,
// ignoring "duplicate column" errors (column already exists).
func addColumnIfNotExists(conn *sql.DB, stmt string) error {
	_, err := conn.Exec(stmt)
	if err != nil && strings.Contains(err.Error(), "duplicate column") {
		return nil
	}
	return err
}

func (db *DB) migrateEmbeddingsToFloat32() error {
	var userVersion int
	if err := db.conn.QueryRow(`PRAGMA user_version`).Scan(&userVersion); err != nil {
		return err
	}
	if userVersion >= embeddingFloat32UserVersion {
		return nil
	}

	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.Query(`SELECT id, embedding FROM insights WHERE embedding IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("select embeddings: %w", err)
	}
	type migratedEmbedding struct {
		id   string
		blob []byte
	}
	var migrated []migratedEmbedding
	for rows.Next() {
		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			rows.Close()
			return fmt.Errorf("scan embedding: %w", err)
		}
		vec := embed.DeserializeLegacyVector(blob)
		if vec == nil || !looksLikeLegacyVector(vec) {
			// Not a parseable legacy float64 blob (empty, malformed, or
			// already in float32 form) — leave it untouched. Aborting the
			// whole migration over one bad row would permanently block the
			// database from opening, and re-encoding non-legacy data here
			// would silently corrupt it.
			fmt.Fprintf(os.Stderr, "mnemon: skipping float32 migration for insight %s: embedding is not a legacy float64 vector (%d bytes)\n", id, len(blob))
			continue
		}
		migrated = append(migrated, migratedEmbedding{id: id, blob: embed.SerializeVector(vec)})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("scan embeddings: %w", err)
	}
	rows.Close()

	for _, item := range migrated {
		if _, err := tx.Exec(`UPDATE insights SET embedding = ? WHERE id = ?`, item.blob, item.id); err != nil {
			return fmt.Errorf("update embedding %s: %w", item.id, err)
		}
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, embeddingFloat32UserVersion)); err != nil {
		return fmt.Errorf("set user_version: %w", err)
	}

	return tx.Commit()
}

// looksLikeLegacyVector reports whether v's values are plausible for an
// embedding (finite and of reasonable magnitude). It guards against
// misreading an already-migrated float32 blob as legacy float64: reinterpreting
// arbitrary float32 bit patterns as float64 yields NaN, Inf, or extreme
// magnitudes with overwhelming probability, so a vector that looks "normal"
// is almost certainly genuine legacy data.
func looksLikeLegacyVector(v []float64) bool {
	for _, f := range v {
		if math.IsNaN(f) || math.IsInf(f, 0) || math.Abs(f) > 1e6 {
			return false
		}
	}
	return true
}

// migrateRemoveNarrativeEdges recreates the edges table without the 'narrative' type
// if the old CHECK constraint still allows it.
func (db *DB) migrateRemoveNarrativeEdges() error {
	// Probe whether the old schema allows 'narrative'
	_, testErr := db.conn.Exec(`INSERT INTO edges VALUES ('__test','__test','narrative',0,'{}',datetime('now'))`)
	if testErr != nil {
		return nil // current schema already rejects 'narrative', nothing to do
	}

	// Old schema — migrate within a transaction
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	steps := []string{
		`DELETE FROM edges WHERE source_id = '__test'`,
		`DELETE FROM edges WHERE edge_type = 'narrative'`,
		`ALTER TABLE edges RENAME TO edges_old`,
		`CREATE TABLE edges (
			source_id   TEXT NOT NULL,
			target_id   TEXT NOT NULL,
			edge_type   TEXT NOT NULL CHECK(edge_type IN ('temporal','semantic','causal','entity')),
			weight      REAL DEFAULT 1.0,
			metadata    TEXT DEFAULT '{}',
			created_at  TEXT NOT NULL,
			PRIMARY KEY (source_id, target_id, edge_type),
			FOREIGN KEY (source_id) REFERENCES insights(id) ON DELETE CASCADE,
			FOREIGN KEY (target_id) REFERENCES insights(id) ON DELETE CASCADE
		)`,
		`INSERT INTO edges SELECT * FROM edges_old`,
		`DROP TABLE edges_old`,
		`CREATE INDEX IF NOT EXISTS idx_edges_source ON edges(source_id)`,
		`CREATE INDEX IF NOT EXISTS idx_edges_target ON edges(target_id)`,
		`CREATE INDEX IF NOT EXISTS idx_edges_type ON edges(edge_type)`,
	}
	for _, s := range steps {
		if _, err := tx.Exec(s); err != nil {
			return fmt.Errorf("step %q: %w", s[:min(len(s), 40)], err)
		}
	}
	return tx.Commit()
}
