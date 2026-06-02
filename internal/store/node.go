package store

import (
	"database/sql"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mnemon-dev/mnemon/internal/model"
)

// Lifecycle constants
const (
	// HalfLifeDays controls how fast effective_importance decays.
	// After this many days without access, importance halves.
	HalfLifeDays = 30.0

	// MaxInsights is the default cap before auto-pruning kicks in.
	MaxInsights = 1000

	// PruneBatchSize is how many excess insights to prune at once.
	PruneBatchSize = 10
)

// InsertInsight inserts a new insight into the database.
func (db *DB) InsertInsight(i *model.Insight) error {
	_, err := db.execer().Exec(
		`INSERT INTO insights (id, content, category, importance, tags, entities, source, access_count, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		i.ID, i.Content, string(i.Category), i.Importance,
		i.TagsJSON(), i.EntitiesJSON(), i.Source, i.AccessCount,
		i.CreatedAt.Format(time.RFC3339), i.UpdatedAt.Format(time.RFC3339),
	)
	return err
}

// GetInsightByID returns a single insight by ID (excludes soft-deleted).
func (db *DB) GetInsightByID(id string) (*model.Insight, error) {
	row := db.execer().QueryRow(
		`SELECT id, content, category, importance, tags, entities, source, access_count, created_at, updated_at, deleted_at
		 FROM insights WHERE id = ? AND deleted_at IS NULL`, id)
	return scanInsight(row)
}

// GetInsightByIDIncludeDeleted returns a single insight by ID, including soft-deleted.
func (db *DB) GetInsightByIDIncludeDeleted(id string) (*model.Insight, error) {
	row := db.execer().QueryRow(
		`SELECT id, content, category, importance, tags, entities, source, access_count, created_at, updated_at, deleted_at
		 FROM insights WHERE id = ?`, id)
	return scanInsight(row)
}

// QueryFilter holds optional filters for querying insights.
type QueryFilter struct {
	Keyword       string
	Category      string
	MinImportance int
	Source        string
	Limit         int
}

// QueryInsights returns insights matching the filter, ordered by importance DESC, created_at DESC.
func (db *DB) QueryInsights(f QueryFilter) ([]*model.Insight, error) {
	var conditions []string
	var args []interface{}

	conditions = append(conditions, "deleted_at IS NULL")

	if f.Keyword != "" {
		conditions = append(conditions, "content LIKE ?")
		args = append(args, "%"+f.Keyword+"%")
	}
	if f.Category != "" {
		conditions = append(conditions, "category = ?")
		args = append(args, f.Category)
	}
	if f.MinImportance > 0 {
		conditions = append(conditions, "importance >= ?")
		args = append(args, f.MinImportance)
	}
	if f.Source != "" {
		conditions = append(conditions, "source = ?")
		args = append(args, f.Source)
	}

	limit := f.Limit
	if limit <= 0 {
		limit = 20
	}

	query := fmt.Sprintf(
		`SELECT id, content, category, importance, tags, entities, source, access_count, created_at, updated_at, deleted_at
		 FROM insights WHERE %s ORDER BY importance DESC, created_at DESC LIMIT ?`,
		strings.Join(conditions, " AND "))
	args = append(args, limit)

	rows, err := db.execer().Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanInsights(rows)
}

// SoftDeleteInsight sets deleted_at on an insight and removes all associated edges.
func (db *DB) SoftDeleteInsight(id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := db.execer().Exec(
		`UPDATE insights SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`,
		now, now, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("insight %s not found or already deleted", id)
	}
	if err := db.DeleteEdgesByNode(id); err != nil {
		return fmt.Errorf("delete edges for %s: %w", id, err)
	}
	return nil
}

// UpdateEntities updates the entities field for an insight.
func (db *DB) UpdateEntities(id string, entities []string) error {
	i := &model.Insight{Entities: entities}
	_, err := db.execer().Exec(
		`UPDATE insights SET entities = ?, updated_at = ? WHERE id = ?`,
		i.EntitiesJSON(), time.Now().UTC().Format(time.RFC3339), id)
	return err
}

// LoadKnownEntities returns the set of distinct entities that appear on at
// least one active (non-deleted) insight. It returns an empty (non-nil) map
// when no insights carry entities. The result is intended for use as a
// read-only filter — for example, by an index-aware entity extractor that
// admits wide-cast candidates only when they match a previously stored
// entity.
func (db *DB) LoadKnownEntities() (map[string]bool, error) {
	known := make(map[string]bool)
	rows, err := db.execer().Query(
		`SELECT DISTINCT je.value FROM insights i, json_each(i.entities) je
		 WHERE i.deleted_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("load known entities: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scan known entity: %w", err)
		}
		if v != "" {
			known[v] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate known entities: %w", err)
	}
	return known, nil
}

// IncrementAccessCount bumps the access count and refreshes last_accessed_at.
// No-op when the database is read-only.
func (db *DB) IncrementAccessCount(id string) error {
	if db.readOnly {
		return nil
	}
	_, err := db.execer().Exec(
		`UPDATE insights SET access_count = access_count + 1, last_accessed_at = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339), id)
	return err
}

// baseWeight maps importance (1-5) to a base weight for effective_importance.
func baseWeight(importance int) float64 {
	switch importance {
	case 5:
		return 1.0
	case 4:
		return 0.8
	case 3:
		return 0.5
	case 2:
		return 0.3
	default:
		return 0.15
	}
}

// ComputeEffectiveImportance calculates the current effective importance.
// Formula: base_weight(imp) * log(1 + access_count) * 0.5^(days_since_access / half_life) * (1 + 0.1*min(edges,5))
// For newly created insights (0 days, 0 access), the access factor is log(1+0)=0,
// so we use max(1.0, log(1+access)) to ensure a non-zero baseline.
func ComputeEffectiveImportance(importance int, accessCount int, daysSinceAccess float64, edgeCount int) float64 {
	base := baseWeight(importance)
	accessFactor := math.Log(1.0 + float64(accessCount))
	if accessFactor < 1.0 {
		accessFactor = 1.0 // baseline for 0-1 accesses
	}
	decayFactor := math.Pow(0.5, daysSinceAccess/HalfLifeDays)
	edges := edgeCount
	if edges > 5 {
		edges = 5
	}
	edgeFactor := 1.0 + 0.1*float64(edges)

	return base * accessFactor * decayFactor * edgeFactor
}

// IsImmune returns true if an insight should never be auto-pruned.
// Immune if: importance >= 4 OR access_count >= 3.
func IsImmune(importance int, accessCount int) bool {
	return importance >= 4 || accessCount >= 3
}

// RefreshEffectiveImportance recomputes and stores effective_importance for one insight.
func (db *DB) RefreshEffectiveImportance(id string) (float64, error) {
	var importance, accessCount int
	var createdAt string
	var lastAccessedAt sql.NullString
	err := db.execer().QueryRow(
		`SELECT importance, access_count, created_at, last_accessed_at FROM insights WHERE id = ? AND deleted_at IS NULL`, id,
	).Scan(&importance, &accessCount, &createdAt, &lastAccessedAt)
	if err != nil {
		return 0, err
	}

	lastAccess, parseErr := time.Parse(time.RFC3339, createdAt)
	if parseErr != nil {
		return 0, fmt.Errorf("parse created_at for %s: %w", id, parseErr)
	}
	if lastAccessedAt.Valid && lastAccessedAt.String != "" {
		if t, err := time.Parse(time.RFC3339, lastAccessedAt.String); err == nil {
			lastAccess = t
		}
	}
	daysSince := time.Now().UTC().Sub(lastAccess).Hours() / 24.0

	var edgeCount int
	if err := db.execer().QueryRow(
		`SELECT (SELECT COUNT(*) FROM edges WHERE source_id = ?) +
		        (SELECT COUNT(*) FROM edges WHERE target_id = ?)`,
		id, id).Scan(&edgeCount); err != nil {
		return 0, fmt.Errorf("count edges for %s: %w", id, err)
	}

	ei := ComputeEffectiveImportance(importance, accessCount, daysSince, edgeCount)

	_, err = db.execer().Exec(`UPDATE insights SET effective_importance = ? WHERE id = ?`, ei, id)
	return ei, err
}

// RetentionCandidate holds an insight and its effective importance breakdown.
type RetentionCandidate struct {
	Insight             *model.Insight `json:"insight"`
	EffectiveImportance float64        `json:"effective_importance"`
	DaysSinceAccess     float64        `json:"days_since_access"`
	EdgeCount           int            `json:"edge_count"`
	Immune              bool           `json:"immune"`
}

// GetRetentionCandidates returns non-immune insights sorted by effective_importance ascending.
// Uses bulk queries for last_accessed_at and edge counts instead of per-insight queries.
func (db *DB) GetRetentionCandidates(threshold float64, limit int) ([]RetentionCandidate, int, error) {
	// Single query: all active insights including last_accessed_at
	type insightRow struct {
		insight    *model.Insight
		lastAccess time.Time
	}
	rows, err := db.execer().Query(
		`SELECT id, content, category, importance, tags, entities, source, access_count,
		        created_at, updated_at, deleted_at, last_accessed_at
		 FROM insights WHERE deleted_at IS NULL`)
	if err != nil {
		return nil, 0, err
	}
	var insightRows []insightRow
	for rows.Next() {
		var i model.Insight
		var cat, tags, entities, source, createdAt, updatedAt string
		var deletedAt, lastAccessedAt sql.NullString
		err := rows.Scan(&i.ID, &i.Content, &cat, &i.Importance, &tags, &entities,
			&source, &i.AccessCount, &createdAt, &updatedAt, &deletedAt, &lastAccessedAt)
		if err != nil {
			rows.Close()
			return nil, 0, err
		}
		i.Category = model.Category(cat)
		i.Source = source
		i.ParseTags(tags)
		i.ParseEntities(entities)
		if i.CreatedAt, err = time.Parse(time.RFC3339, createdAt); err != nil {
			rows.Close()
			return nil, 0, fmt.Errorf("parse created_at for %s: %w", i.ID, err)
		}
		if i.UpdatedAt, err = time.Parse(time.RFC3339, updatedAt); err != nil {
			rows.Close()
			return nil, 0, fmt.Errorf("parse updated_at for %s: %w", i.ID, err)
		}
		if deletedAt.Valid {
			t, parseErr := time.Parse(time.RFC3339, deletedAt.String)
			if parseErr != nil {
				rows.Close()
				return nil, 0, fmt.Errorf("parse deleted_at for %s: %w", i.ID, parseErr)
			}
			i.DeletedAt = &t
		}
		la := i.CreatedAt
		if lastAccessedAt.Valid && lastAccessedAt.String != "" {
			if t, err := time.Parse(time.RFC3339, lastAccessedAt.String); err == nil {
				la = t
			}
		}
		insightRows = append(insightRows, insightRow{insight: &i, lastAccess: la})
	}
	rows.Close()

	// Single query: edge counts per insight (replaces N individual COUNT queries)
	edgeCounts := make(map[string]int)
	ecRows, err := db.execer().Query(
		`SELECT id, SUM(cnt) FROM (
			SELECT source_id AS id, COUNT(*) AS cnt FROM edges GROUP BY source_id
			UNION ALL
			SELECT target_id AS id, COUNT(*) AS cnt FROM edges GROUP BY target_id
		) GROUP BY id`)
	if err == nil {
		for ecRows.Next() {
			var id string
			var cnt int
			if ecRows.Scan(&id, &cnt) == nil {
				edgeCounts[id] = cnt
			}
		}
		ecRows.Close()
	}

	// Compute EI and collect candidates; batch-update EI in a transaction.
	now := time.Now().UTC()
	type eiUpdate struct {
		id string
		ei float64
	}
	var updates []eiUpdate
	var candidates []RetentionCandidate
	for _, ir := range insightRows {
		ins := ir.insight
		daysSince := now.Sub(ir.lastAccess).Hours() / 24.0
		ec := edgeCounts[ins.ID]
		ei := ComputeEffectiveImportance(ins.Importance, ins.AccessCount, daysSince, ec)
		immune := IsImmune(ins.Importance, ins.AccessCount)

		updates = append(updates, eiUpdate{id: ins.ID, ei: ei})

		if ei < threshold && !immune {
			candidates = append(candidates, RetentionCandidate{
				Insight:             ins,
				EffectiveImportance: ei,
				DaysSinceAccess:     daysSince,
				EdgeCount:           ec,
				Immune:              immune,
			})
		}
	}

	// Batch-update effective_importance in a single transaction (best-effort:
	// failures don't affect returned candidates since EI is already computed in memory).
	if len(updates) > 0 {
		tx, err := db.conn.Begin()
		if err == nil {
			var txErr error
			for _, u := range updates {
				if _, txErr = tx.Exec(`UPDATE insights SET effective_importance = ? WHERE id = ?`, u.ei, u.id); txErr != nil {
					break
				}
			}
			if txErr != nil {
				tx.Rollback()
				fmt.Fprintf(os.Stderr, "warning: batch EI update failed, rolled back: %v\n", txErr)
			} else {
				tx.Commit()
			}
		}
	}

	// Sort by effective_importance ascending (weakest first)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].EffectiveImportance < candidates[j].EffectiveImportance
	})

	total := len(insightRows)
	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates, total, nil
}

// AutoPrune soft-deletes the lowest effective_importance non-immune insights
// when total active count exceeds maxInsights. excludeIDs are protected from pruning
// (typically the just-created insights). Returns number pruned.
// If already inside a transaction (db.tx != nil), executes inline; otherwise wraps in its own transaction.
func (db *DB) AutoPrune(maxInsights int, excludeIDs []string) (int, error) {
	if db.tx != nil {
		return db.autoPrune(maxInsights, excludeIDs)
	}
	var pruned int
	err := db.InTransaction(func() error {
		var innerErr error
		pruned, innerErr = db.autoPrune(maxInsights, excludeIDs)
		return innerErr
	})
	return pruned, err
}

func (db *DB) autoPrune(maxInsights int, excludeIDs []string) (int, error) {
	ex := db.execer()

	var total int
	if err := ex.QueryRow(`SELECT COUNT(*) FROM insights WHERE deleted_at IS NULL`).Scan(&total); err != nil {
		return 0, fmt.Errorf("count insights: %w", err)
	}
	if total <= maxInsights {
		return 0, nil
	}

	excess := total - maxInsights
	if excess > PruneBatchSize {
		excess = PruneBatchSize
	}

	// Build NOT IN clause for excluded IDs
	var excludeClause string
	var args []interface{}
	if len(excludeIDs) > 0 {
		placeholders := make([]string, len(excludeIDs))
		for i, id := range excludeIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		excludeClause = fmt.Sprintf("AND id NOT IN (%s)", strings.Join(placeholders, ","))
	}
	args = append(args, excess)

	// Collect candidate IDs first (close cursor before writing to avoid single-conn deadlock)
	rows, err := ex.Query(
		fmt.Sprintf(`SELECT id FROM insights
		 WHERE deleted_at IS NULL AND importance < 4 AND access_count < 3 %s
		 ORDER BY effective_importance ASC LIMIT ?`, excludeClause), args...)
	if err != nil {
		return 0, fmt.Errorf("query prune candidates: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan prune candidate: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	pruned := 0
	for _, id := range ids {
		res, err := ex.Exec(
			`UPDATE insights SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`,
			now, now, id)
		if err != nil {
			return pruned, fmt.Errorf("prune %s: %w", id, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			if err := db.DeleteEdgesByNode(id); err != nil {
				return pruned, fmt.Errorf("delete edges for pruned %s: %w", id, err)
			}
			pruned++
		}
	}

	return pruned, nil
}

// BoostRetention boosts an insight's retention: access_count +3, refreshes last_accessed_at.
func (db *DB) BoostRetention(id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := db.execer().Exec(
		`UPDATE insights SET access_count = access_count + 3, last_accessed_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`,
		now, now, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("insight %s not found or already deleted", id)
	}
	return nil
}

// GetRecentInsightsInWindow returns non-deleted insights created within the given
// time window (hours), excluding the given ID. Ordered by created_at DESC.
func (db *DB) GetRecentInsightsInWindow(excludeID string, windowHours float64, limit int) ([]*model.Insight, error) {
	cutoff := time.Now().UTC().Add(-time.Duration(windowHours * float64(time.Hour)))
	rows, err := db.execer().Query(
		`SELECT id, content, category, importance, tags, entities, source, access_count, created_at, updated_at, deleted_at
		 FROM insights WHERE id != ? AND deleted_at IS NULL AND created_at >= ?
		 ORDER BY created_at DESC LIMIT ?`,
		excludeID, cutoff.Format(time.RFC3339), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanInsights(rows)
}

// GetLatestInsightBySource returns the most recent non-deleted insight for a given source, excluding the given ID.
func (db *DB) GetLatestInsightBySource(source string, excludeID string) (*model.Insight, error) {
	row := db.execer().QueryRow(
		`SELECT id, content, category, importance, tags, entities, source, access_count, created_at, updated_at, deleted_at
		 FROM insights WHERE source = ? AND id != ? AND deleted_at IS NULL
		 ORDER BY created_at DESC, rowid DESC LIMIT 1`, source, excludeID)
	return scanInsight(row)
}

// GetRecentInsightsBySource returns the N most recent non-deleted insights for a source, excluding the given ID.
func (db *DB) GetRecentInsightsBySource(source string, excludeID string, limit int) ([]*model.Insight, error) {
	rows, err := db.execer().Query(
		`SELECT id, content, category, importance, tags, entities, source, access_count, created_at, updated_at, deleted_at
		 FROM insights WHERE source = ? AND id != ? AND deleted_at IS NULL
		 ORDER BY created_at DESC LIMIT ?`, source, excludeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanInsights(rows)
}

// GetActiveInsightsBySourceOrdered returns active insights for a source in chronological order.
func (db *DB) GetActiveInsightsBySourceOrdered(source string) ([]*model.Insight, error) {
	rows, err := db.execer().Query(
		`SELECT id, content, category, importance, tags, entities, source, access_count, created_at, updated_at, deleted_at
		 FROM insights WHERE source = ? AND deleted_at IS NULL
		 ORDER BY created_at ASC, rowid ASC`, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanInsights(rows)
}

// GetAllActiveInsights returns all non-deleted insights.
func (db *DB) GetAllActiveInsights() ([]*model.Insight, error) {
	rows, err := db.execer().Query(
		`SELECT id, content, category, importance, tags, entities, source, access_count, created_at, updated_at, deleted_at
		 FROM insights WHERE deleted_at IS NULL ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanInsights(rows)
}

// EntityStat holds entity link count info.
type EntityStat struct {
	Entity string `json:"entity"`
	Count  int    `json:"count"`
}

// InsightStats holds aggregate statistics.
type InsightStats struct {
	Total        int            `json:"total"`
	ByCategory   map[string]int `json:"by_category"`
	EdgeCount    int            `json:"edge_count"`
	DeletedCount int            `json:"deleted_count"`
	TopEntities  []EntityStat   `json:"top_entities"`
	OplogCount   int            `json:"oplog_count"`
}

// GetStats returns aggregate statistics.
func (db *DB) GetStats() (*InsightStats, error) {
	stats := &InsightStats{ByCategory: make(map[string]int)}

	// Total active
	if err := db.execer().QueryRow(`SELECT COUNT(*) FROM insights WHERE deleted_at IS NULL`).Scan(&stats.Total); err != nil {
		return nil, fmt.Errorf("count active: %w", err)
	}

	// Deleted
	if err := db.execer().QueryRow(`SELECT COUNT(*) FROM insights WHERE deleted_at IS NOT NULL`).Scan(&stats.DeletedCount); err != nil {
		return nil, fmt.Errorf("count deleted: %w", err)
	}

	// By category
	rows, err := db.execer().Query(`SELECT category, COUNT(*) FROM insights WHERE deleted_at IS NULL GROUP BY category`)
	if err != nil {
		return nil, fmt.Errorf("query categories: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cat string
		var count int
		if err := rows.Scan(&cat, &count); err != nil {
			return nil, err
		}
		stats.ByCategory[cat] = count
	}

	// Edge count
	if err := db.execer().QueryRow(`SELECT COUNT(*) FROM edges`).Scan(&stats.EdgeCount); err != nil {
		return nil, fmt.Errorf("count edges: %w", err)
	}

	// Oplog count
	if err := db.execer().QueryRow(`SELECT COUNT(*) FROM oplog`).Scan(&stats.OplogCount); err != nil {
		return nil, fmt.Errorf("count oplog: %w", err)
	}

	// Top entities by link count (across active insights)
	eRows, err := db.execer().Query(`
		SELECT je.value, COUNT(DISTINCT i.id) as cnt
		FROM insights i, json_each(i.entities) je
		WHERE i.deleted_at IS NULL
		GROUP BY je.value
		ORDER BY cnt DESC
		LIMIT 20`)
	if err == nil {
		defer eRows.Close()
		for eRows.Next() {
			var es EntityStat
			if err := eRows.Scan(&es.Entity, &es.Count); err != nil {
				break
			}
			stats.TopEntities = append(stats.TopEntities, es)
		}
	}
	if stats.TopEntities == nil {
		stats.TopEntities = []EntityStat{}
	}

	return stats, nil
}

// UpdateEmbedding stores an embedding vector for an insight.
func (db *DB) UpdateEmbedding(id string, blob []byte) error {
	_, err := db.execer().Exec(
		`UPDATE insights SET embedding = ?, updated_at = ? WHERE id = ?`,
		blob, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

// GetEmbedding returns the raw embedding blob for an insight.
func (db *DB) GetEmbedding(id string) ([]byte, error) {
	var blob []byte
	err := db.execer().QueryRow(`SELECT embedding FROM insights WHERE id = ? AND deleted_at IS NULL`, id).Scan(&blob)
	if err != nil {
		return nil, err
	}
	return blob, nil
}

// EmbeddedInsight pairs an insight ID, content, and its embedding blob.
type EmbeddedInsight struct {
	ID        string
	Content   string
	Embedding []byte
}

// GetAllEmbeddings returns all active insights that have embeddings.
func (db *DB) GetAllEmbeddings() ([]EmbeddedInsight, error) {
	rows, err := db.execer().Query(
		`SELECT id, content, embedding FROM insights WHERE deleted_at IS NULL AND embedding IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []EmbeddedInsight
	for rows.Next() {
		var e EmbeddedInsight
		if err := rows.Scan(&e.ID, &e.Content, &e.Embedding); err != nil {
			return nil, err
		}
		if len(e.Embedding) > 0 {
			results = append(results, e)
		}
	}
	return results, nil
}

// ScanEmbeddings streams embeddings one at a time via callback, avoiding full-slice allocation.
// The callback returns true to continue scanning, false to stop early.
func (db *DB) ScanEmbeddings(fn func(id string, blob []byte) bool) error {
	rows, err := db.execer().Query(
		`SELECT id, embedding FROM insights WHERE deleted_at IS NULL AND embedding IS NOT NULL`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			return err
		}
		if len(blob) > 0 {
			if !fn(id, blob) {
				break
			}
		}
	}
	return nil
}

// EmbeddingStats returns total insights and how many have embeddings.
func (db *DB) EmbeddingStats() (total int, embedded int, err error) {
	if err := db.execer().QueryRow(`SELECT COUNT(*) FROM insights WHERE deleted_at IS NULL`).Scan(&total); err != nil {
		return 0, 0, fmt.Errorf("count active insights: %w", err)
	}
	if err := db.execer().QueryRow(`SELECT COUNT(*) FROM insights WHERE deleted_at IS NULL AND embedding IS NOT NULL`).Scan(&embedded); err != nil {
		return 0, 0, fmt.Errorf("count embedded insights: %w", err)
	}
	return total, embedded, nil
}

// GetInsightsWithoutEmbedding returns active insights that lack embeddings.
func (db *DB) GetInsightsWithoutEmbedding(limit int) ([]*model.Insight, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.execer().Query(
		`SELECT id, content, category, importance, tags, entities, source, access_count, created_at, updated_at, deleted_at
		 FROM insights WHERE deleted_at IS NULL AND embedding IS NULL
		 ORDER BY importance DESC, created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanInsights(rows)
}

// insightScanner abstracts sql.Row and sql.Rows for shared insight row scanning.
type insightScanner interface {
	Scan(dest ...any) error
}

// scanOneInsight scans a single insight row and parses all fields.
func scanOneInsight(s insightScanner) (*model.Insight, error) {
	var i model.Insight
	var cat, tags, entities, source, createdAt, updatedAt string
	var deletedAt sql.NullString

	err := s.Scan(&i.ID, &i.Content, &cat, &i.Importance, &tags, &entities,
		&source, &i.AccessCount, &createdAt, &updatedAt, &deletedAt)
	if err != nil {
		return nil, err
	}

	i.Category = model.Category(cat)
	i.Source = source
	i.ParseTags(tags)
	i.ParseEntities(entities)
	if i.CreatedAt, err = time.Parse(time.RFC3339, createdAt); err != nil {
		return nil, fmt.Errorf("parse created_at for %s: %w", i.ID, err)
	}
	if i.UpdatedAt, err = time.Parse(time.RFC3339, updatedAt); err != nil {
		return nil, fmt.Errorf("parse updated_at for %s: %w", i.ID, err)
	}
	if deletedAt.Valid {
		t, parseErr := time.Parse(time.RFC3339, deletedAt.String)
		if parseErr != nil {
			return nil, fmt.Errorf("parse deleted_at for %s: %w", i.ID, parseErr)
		}
		i.DeletedAt = &t
	}
	return &i, nil
}

func scanInsight(row *sql.Row) (*model.Insight, error) {
	return scanOneInsight(row)
}

func scanInsights(rows *sql.Rows) ([]*model.Insight, error) {
	results := make([]*model.Insight, 0)
	for rows.Next() {
		ins, err := scanOneInsight(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, ins)
	}
	return results, nil
}
