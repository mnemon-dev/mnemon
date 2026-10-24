package records

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Readers are the access of a process that must never write a store it
// reads (the AML inspect service), while the store's owner writes it, and
// with no write permission at all (systemd's ReadOnlyPaths=).
//
// SQLite reads a WAL-mode file through its -wal and -shm. A read-only
// (mode=ro) connection opens them when both exist, read-only when it may not
// write them, and reads what every commit added. When either is missing it
// must create it: a connection with no write permission cannot
// (SQLITE_READONLY_DIRECTORY), and one with it does, leaving them behind. So
// a WAL file whose -wal and -shm exist (a writer holds it, or left them) is
// opened mode=ro and read live; any other WAL file is quiescent, no writer
// holds it and its main file is the whole database, and it is opened
// mode=ro&immutable=1, which reads the main file alone and makes no file. An
// immutable connection does not see a writer that opens the file later, and
// that writer's checkpoint would change the main file under it: the file's
// identity (inode, size, modification time) is taken at the open, and
// checked again when the read ends (Unchanged): a change is ErrChanged, and
// the read must be discarded. A rollback-journal file (memory-index.db) is
// opened mode=ro: its readers take read locks and make no file. No reader
// writes, creates or checkpoints anything.

// ErrChanged is the error of a read of a quiescent file that a writer
// changed while it was read (Store.Unchanged): what was read may mix two
// states of the file. Reading it again reads it through its writer.
var ErrChanged = errors.New("the database changed while it was read without its write-ahead log; read it again")

// OpenReaderDB opens the SQLite file at path for reading only, by the
// readers' rules above, with one connection: live (mode=ro) when it is a
// rollback-journal file or a WAL file whose -wal and -shm exist, else
// quiescent (mode=ro&immutable=1). check reports ErrChanged when a quiescent
// file has changed since the open, and nil for a live one. A missing file is
// an error wrapping fs.ErrNotExist, and nothing is created.
func OpenReaderDB(path string) (db *sql.DB, check func() error, err error) {
	for attempt := 0; ; attempt++ {
		db, check, err = openReaderDB(path)
		// A live open whose writer closed the file between the look and the
		// open finds its -wal gone and fails: the file is quiescent now.
		if err == nil || attempt == 1 || errors.Is(err, fs.ErrNotExist) {
			return db, check, err
		}
	}
}

func openReaderDB(path string) (*sql.DB, func() error, error) {
	wal, err := walFile(path)
	if err != nil {
		return nil, nil, err
	}
	var stamp os.FileInfo
	if wal && !(exists(path+"-wal") && exists(path+"-shm")) {
		if stamp, err = os.Stat(path); err != nil {
			return nil, nil, err
		}
	}
	db, err := sql.Open("sqlite", readerDSN(path, stamp != nil))
	if err != nil {
		return nil, nil, err
	}
	db.SetMaxOpenConns(1)
	// The first read opens the file's journal: a live WAL file whose -wal is
	// gone fails here, not on the caller's first query.
	var tables int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_schema`).Scan(&tables); err != nil {
		db.Close()
		return nil, nil, err
	}
	if stamp == nil {
		return db, func() error { return nil }, nil
	}
	return db, func() error { return unchanged(path, stamp) }, nil
}

// readerDSN is a reader's DSN for path: mode=ro, and immutable=1 for a
// quiescent file. It takes no write lock (no _txlock).
func readerDSN(path string, immutable bool) string {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("_pragma", "busy_timeout(10000)")
	q.Set("mode", "ro")
	if immutable {
		q.Set("immutable", "1")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// walFile reports whether the SQLite file at path is in WAL mode: the file
// format versions of its header (bytes 18 and 19) are 2. A file too short for
// a header is not.
func walFile(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	header := make([]byte, 20)
	if _, err := io.ReadFull(f, header); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return false, nil
		}
		return false, err
	}
	return header[18] == 2 && header[19] == 2, nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// unchanged is ErrChanged unless the file at path is still the one stamp
// describes: the same file, size and modification time.
func unchanged(path string, stamp os.FileInfo) error {
	now, err := os.Stat(path)
	if err != nil || !os.SameFile(stamp, now) || now.Size() != stamp.Size() || !now.ModTime().Equal(stamp.ModTime()) {
		return ErrChanged
	}
	return nil
}

// Retryable reports whether err, from a reader, says the file could not be
// read consistently now but may be soon: it changed under a quiescent read
// (ErrChanged), SQLite was busy or locked, a read-only connection met a
// recovery, an uninitialized -shm or a hot rollback journal (a writer's
// crash left one) that the file's writer finishes or rolls back, or a
// writer closed the file between the reader's look and its open.
func Retryable(err error) bool {
	if errors.Is(err, ErrChanged) {
		return true
	}
	var e *sqlite.Error
	if !errors.As(err, &e) {
		return false
	}
	switch code := e.Code(); {
	case code&0xff == sqlite3.SQLITE_BUSY, code&0xff == sqlite3.SQLITE_LOCKED:
		return true
	default:
		return code == sqlite3.SQLITE_READONLY_RECOVERY || code == sqlite3.SQLITE_READONLY_CANTINIT || code == sqlite3.SQLITE_READONLY_DIRECTORY ||
			code == sqlite3.SQLITE_READONLY_ROLLBACK
	}
}

// OpenReader opens the store in directory as a reader (OpenReaderDB): every
// method that only reads works, Put and the derived writes refuse as on a
// readonly store, and it runs no query runtime. Check Unchanged when a read
// ends. A directory without records.db is an error wrapping fs.ErrNotExist.
func OpenReader(directory string) (*Store, error) {
	path, err := filepath.Abs(filepath.Join(directory, "records.db"))
	if err != nil {
		return nil, err
	}
	db, check, err := OpenReaderDB(path)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, path: path, readonly: true, unchanged: check}
	var tables int
	if err = db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE name IN ('lexical_documents','lexical_terms')").Scan(&tables); err != nil {
		db.Close()
		return nil, err
	}
	s.fts = tables == 2
	return s, nil
}

// Unchanged is nil unless the store is a reader of a quiescent file that a
// writer has changed since it was opened (ErrChanged): then what it read
// may be inconsistent, and must be read again. Other stores are never
// changed under their reads.
func (s *Store) Unchanged() error {
	if s.unchanged == nil {
		return nil
	}
	return s.unchanged()
}

// isHead reports whether r is one of scope's heads, the records Heads lists:
// visible in scope, active, and neither a document nor an index item.
func isHead(r record, scope Scope) bool {
	return visible(r, scope) && r.State == "active" && r.Kind != "memory-document" && r.Kind != "memory-index"
}

// PageRecord is a record of a page (Page): its write position (seq), its
// version (Head.Version) and its JSON, verbatim.
type PageRecord struct {
	Seq, Version int64
	JSON         string
}

// pageBatch is how many current versions one statement of Page reads.
const pageBatch = 256

// Page returns the current heads of scope (as Heads lists them) whose seq is
// after after, in write order, limit at most, and whether more follow. Each
// statement reads one bounded batch: no read outlasts its statement.
func (s *Store) Page(ctx context.Context, scope Scope, after int64, limit int) ([]PageRecord, bool, error) {
	if limit < 1 {
		return nil, false, errors.New("a records page needs a positive limit")
	}
	out := []PageRecord{}
	for {
		batch, err := s.versionsAfter(ctx, after)
		if err != nil {
			return nil, false, err
		}
		for _, v := range batch {
			after = v.seq
			if !isHead(v.record, scope) {
				continue
			}
			if len(out) == limit {
				return out, true, nil
			}
			out = append(out, PageRecord{Seq: v.seq, Version: v.version, JSON: v.raw})
		}
		if len(batch) < pageBatch {
			return out, false, nil
		}
	}
}

// Record returns the current head of scope with id as Page lists it, and
// false when Page lists none with that id: no record has it, or the record
// is a document, an index item, not active or not in scope. One statement
// reads it, by the index of current versions.
func (s *Store) Record(ctx context.Context, scope Scope, id string) (PageRecord, bool, error) {
	var v pageVersion
	err := s.readDB().QueryRowContext(ctx, `SELECT seq,rowid,record_json,kind,scope,workspace_id,session_id,state FROM record_versions
WHERE id=? AND rev_to IS NULL`, id).Scan(&v.seq, &v.version, &v.raw, &v.Kind, &v.Scope, &v.WorkspaceID, &v.SessionID, &v.State)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return PageRecord{}, false, nil
	case err != nil:
		return PageRecord{}, false, err
	case !isHead(v.record, scope):
		return PageRecord{}, false, nil
	}
	return PageRecord{Seq: v.seq, Version: v.version, JSON: v.raw}, true, nil
}

// pageVersion is a current version as Page reads it.
type pageVersion struct {
	record
	seq, version int64
	raw          string
}

// versionsAfter reads the next batch of current versions after seq, in seq
// order.
func (s *Store) versionsAfter(ctx context.Context, seq int64) ([]pageVersion, error) {
	rows, err := s.readDB().QueryContext(ctx, `SELECT seq,rowid,record_json,kind,scope,workspace_id,session_id,state FROM record_versions
WHERE rev_to IS NULL AND seq>? ORDER BY seq LIMIT ?`, seq, pageBatch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []pageVersion{}
	for rows.Next() {
		var v pageVersion
		if err := rows.Scan(&v.seq, &v.version, &v.raw, &v.Kind, &v.Scope, &v.WorkspaceID, &v.SessionID, &v.State); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Tail counts the current heads of scope (as Heads lists them) whose
// version is above version, the records written after it, and returns the
// ids of the first limit of them in version order: what consolidation reads
// after its watermark, in the order it reads them. One statement reads them.
func (s *Store) Tail(ctx context.Context, scope Scope, version int64, limit int) (int, []string, error) {
	rows, err := s.readDB().QueryContext(ctx, `SELECT id,kind,scope,workspace_id,session_id,state FROM record_versions
WHERE rev_to IS NULL AND rowid>? ORDER BY rowid`, version)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	count, ids := 0, []string{}
	for rows.Next() {
		var r record
		if err := rows.Scan(&r.ID, &r.Kind, &r.Scope, &r.WorkspaceID, &r.SessionID, &r.State); err != nil {
			return 0, nil, err
		}
		if !isHead(r, scope) {
			continue
		}
		count++
		if len(ids) < limit {
			ids = append(ids, r.ID)
		}
	}
	return count, ids, rows.Err()
}

// Count counts the current heads of scope (as Heads lists them, the records
// Page lists) and, of them, those whose version is above version (as Tail
// counts them): a store's records, and those written after a
// consolidation's watermark. One statement reads them.
func (s *Store) Count(ctx context.Context, scope Scope, version int64) (heads, after int, err error) {
	rows, err := s.readDB().QueryContext(ctx, `SELECT rowid,kind,scope,workspace_id,session_id,state FROM record_versions WHERE rev_to IS NULL`)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var r record
		var v int64
		if err := rows.Scan(&v, &r.Kind, &r.Scope, &r.WorkspaceID, &r.SessionID, &r.State); err != nil {
			return 0, 0, err
		}
		if !isHead(r, scope) {
			continue
		}
		heads++
		if v > version {
			after++
		}
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	return heads, after, nil
}

// LexicalScores returns, for each query, the words score (BM25) of each of
// its ids over scope's searchable records at revision ("" for the current
// one): the score a words search of that query ranks the id by (Query with
// match "words" and no index policy), 0 for an id it does not rank. The
// words index is built once for all the queries; nothing is cached.
func (s *Store) LexicalScores(ctx context.Context, revisionHash string, scope Scope, queries []string, ids [][]string) ([][]float64, error) {
	if len(ids) != len(queries) {
		return nil, fmt.Errorf("lexical scores take one id list per query: %d lists for %d queries", len(ids), len(queries))
	}
	rev, hash, err := revision(ctx, s.readDB(), revisionHash)
	if err != nil {
		return nil, err
	}
	all, err := s.queryRecords(ctx, rev, hash)
	if err != nil {
		return nil, err
	}
	index := newLexical(candidates(all, QueryRequest{Scope: scope}, "", true))
	out := make([][]float64, len(queries))
	for i, query := range queries {
		scores := map[string]float64{}
		for _, hit := range index.hits(lower(query)) {
			scores[hit.id] = hit.score
		}
		out[i] = make([]float64, len(ids[i]))
		for j, id := range ids[i] {
			out[i][j] = scores[id]
		}
	}
	return out, nil
}
