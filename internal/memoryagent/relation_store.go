package memoryagent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// RelationConfig composes native candidate signals with bounded decision-model
// judgments. Adjacency is structural; the other edges are fallible hints only.
type RelationConfig struct {
	Adjacent bool `json:"adjacent,omitempty"`
	Entity   bool `json:"entity,omitempty"`
	Semantic bool `json:"semantic,omitempty"`
	Causal   bool `json:"causal,omitempty"`
	OnWrite  bool `json:"onWrite,omitempty"`
}

func (c *RelationConfig) enabled() bool {
	return c != nil && (c.Adjacent || c.Entity || c.Semantic || c.Causal)
}

func (c *RelationConfig) scored() bool { return c != nil && (c.Entity || c.Semantic || c.Causal) }

type relationRef struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
}

type RelationEdge struct {
	Source relationRef `json:"source"`
	Target relationRef `json:"target"`
	Type   string      `json:"type"`
	Score  float64     `json:"score"`
}

type relationStore struct {
	db       *sql.DB
	readonly bool
}

func openRelations(path string, readonly bool) (*relationStore, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("relations path must be absolute")
	}
	if !readonly {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			f.Close()
		} else if !os.IsExist(err) {
			return nil, err
		}
	}
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("_pragma", "busy_timeout(10000)")
	q.Set("_txlock", "immediate")
	if readonly {
		q.Set("mode", "ro")
	}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if readonly {
		err = db.Ping()
	} else {
		_, err = db.Exec(`CREATE TABLE IF NOT EXISTS relation_nodes(profile TEXT,id TEXT,version INTEGER,PRIMARY KEY(profile,id));
CREATE TABLE IF NOT EXISTS relation_edges(profile TEXT,source TEXT,source_version INTEGER,target TEXT,target_version INTEGER,kind TEXT,score REAL,PRIMARY KEY(profile,source,source_version,target,target_version,kind));
CREATE INDEX IF NOT EXISTS relation_targets ON relation_edges(profile,target);
CREATE TABLE IF NOT EXISTS relation_status(profile TEXT PRIMARY KEY,revision TEXT NOT NULL,remaining INTEGER NOT NULL);`)
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return &relationStore{db: db, readonly: readonly}, nil
}

func (s *relationStore) progress(ctx context.Context, profile string) (string, int, error) {
	var revision string
	var remaining int
	err := s.db.QueryRowContext(ctx, `SELECT revision,remaining FROM relation_status WHERE profile=?`, profile).Scan(&revision, &remaining)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, nil
	}
	return revision, remaining, err
}

func (s *relationStore) saveProgress(ctx context.Context, profile, revision string, remaining int) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO relation_status VALUES(?,?,?) ON CONFLICT(profile) DO UPDATE SET revision=excluded.revision,remaining=excluded.remaining`, profile, revision, remaining)
	return err
}

func (s *relationStore) versions(ctx context.Context, profile string) (map[string]int64, error) {
	out := map[string]int64{}
	rows, err := s.db.QueryContext(ctx, `SELECT id,version FROM relation_nodes WHERE profile=?`, profile)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var version int64
		if err := rows.Scan(&id, &version); err != nil {
			return nil, err
		}
		out[id] = version
		if len(out) > 200000 {
			return nil, errors.New("relation node limit exceeded")
		}
	}
	return out, rows.Err()
}

// publish fences each processed source after external model work. Concurrent
// builders may replay an identical source version, but cannot overwrite a newer
// one. Obsolete incident edges are disposable; historical raw reads remain valid.
func (s *relationStore) publish(ctx context.Context, profile string, previous map[string]int64, nodes []relationRef, edges []RelationEdge) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, node := range nodes {
		var have int64
		err := tx.QueryRowContext(ctx, `SELECT version FROM relation_nodes WHERE profile=? AND id=?`, profile, node.ID).Scan(&have)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if have != previous[node.ID] && have != node.Version {
			return errors.New("relation index changed concurrently; rerun the bounded job")
		}
		if have == node.Version {
			return tx.Commit() // A concurrent successful builder owns this version.
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO relation_nodes VALUES(?,?,?) ON CONFLICT(profile,id) DO UPDATE SET version=excluded.version`, profile, node.ID, node.Version); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM relation_edges WHERE profile=? AND ((source=? AND source_version!=?) OR (target=? AND target_version!=?))`, profile, node.ID, node.Version, node.ID, node.Version); err != nil {
			return err
		}
	}
	for _, edge := range edges {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO relation_edges VALUES(?,?,?,?,?,?,?)`, profile, edge.Source.ID, edge.Source.Version, edge.Target.ID, edge.Target.Version, edge.Type, edge.Score); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Both directions are retrieval routes, including causal edges: a question
// about an effect must be able to find its cause. The stored direction remains
// explicit and never supplies supersession or deletion authority.
func (s *relationStore) neighbors(ctx context.Context, profile string, ids []string) ([]RelationEdge, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > 48 {
		return nil, errors.New("relation frontier exceeds 48")
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := []any{profile}
	for range 2 {
		for _, id := range ids {
			args = append(args, id)
		}
	}
	query := fmt.Sprintf(`SELECT source,source_version,target,target_version,kind,score FROM relation_edges WHERE profile=? AND (source IN (%s) OR target IN (%s)) ORDER BY score DESC,source,target,kind LIMIT 513`, marks, marks)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RelationEdge{}
	for rows.Next() {
		var e RelationEdge
		if err := rows.Scan(&e.Source.ID, &e.Source.Version, &e.Target.ID, &e.Target.Version, &e.Type, &e.Score); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
