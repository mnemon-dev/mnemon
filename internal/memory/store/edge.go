package store

import (
	"fmt"
	"strings"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memory/model"
)

// InsertEdge inserts or replaces an edge.
func (db *DB) InsertEdge(e *model.Edge) error {
	_, err := db.execer().Exec(
		`INSERT OR REPLACE INTO edges (source_id, target_id, edge_type, weight, metadata, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		e.SourceID, e.TargetID, string(e.EdgeType), e.Weight,
		e.MetadataJSON(), e.CreatedAt.Format(time.RFC3339),
	)
	return err
}

// GetEdgesByNode returns all edges where the given node is source or target.
// Uses UNION ALL to allow SQLite to use separate indexes on source_id and target_id.
func (db *DB) GetEdgesByNode(nodeID string) ([]*model.Edge, error) {
	rows, err := db.execer().Query(
		`SELECT source_id, target_id, edge_type, weight, metadata, created_at
		 FROM edges WHERE source_id = ?
		 UNION ALL
		 SELECT source_id, target_id, edge_type, weight, metadata, created_at
		 FROM edges WHERE target_id = ? AND source_id != ?`,
		nodeID, nodeID, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEdges(rows)
}

// supersededLookupChunk bounds how many ids go into one IN clause. SQLite's
// host-parameter ceiling is 32766 on current builds and 999 on older ones;
// 500 stays inside both. Recall's candidate set is normally far smaller, so
// the loop below runs once.
const supersededLookupChunk = 500

// GetSupersededIDs returns which of the given ids are the target of at least
// one 'supersedes' edge, i.e. which of them some other insight claims to
// replace. Recall uses this to demote stale content; the rows are kept so the
// lineage stays inspectable.
//
// The lookup is scoped to the ids the caller holds. Reading every supersedes
// edge in the store would cost time proportional to its whole supersession
// history on a path that only needs a verdict for the current candidates,
// and idx_edges_target_type answers the scoped form from the index.
func (db *DB) GetSupersededIDs(ids []string) (map[string]bool, error) {
	superseded := make(map[string]bool)
	ex := db.execer()
	for start := 0; start < len(ids); start += supersededLookupChunk {
		end := min(start+supersededLookupChunk, len(ids))
		if err := collectSupersededIDs(ex, ids[start:end], superseded); err != nil {
			return nil, err
		}
	}
	return superseded, nil
}

// collectSupersededIDs adds the superseded ids in one batch to into. The rows
// are closed before returning: the pool holds a single connection, so an open
// cursor would block the next batch.
func collectSupersededIDs(ex dbExecer, chunk []string, into map[string]bool) error {
	args := make([]any, 0, len(chunk)+1)
	args = append(args, string(model.EdgeSupersedes))
	placeholders := make([]string, len(chunk))
	for i, id := range chunk {
		placeholders[i] = "?"
		args = append(args, id)
	}

	rows, err := ex.Query(fmt.Sprintf(
		`SELECT DISTINCT target_id FROM edges WHERE edge_type = ? AND target_id IN (%s)`,
		strings.Join(placeholders, ",")), args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		into[id] = true
	}
	return rows.Err()
}

// GetEdgesByNodeAndType returns edges for a node filtered by edge type.
// Uses UNION ALL to allow SQLite to use composite indexes.
func (db *DB) GetEdgesByNodeAndType(nodeID string, edgeType model.EdgeType) ([]*model.Edge, error) {
	rows, err := db.execer().Query(
		`SELECT source_id, target_id, edge_type, weight, metadata, created_at
		 FROM edges WHERE source_id = ? AND edge_type = ?
		 UNION ALL
		 SELECT source_id, target_id, edge_type, weight, metadata, created_at
		 FROM edges WHERE target_id = ? AND edge_type = ? AND source_id != ?`,
		nodeID, string(edgeType), nodeID, string(edgeType), nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEdges(rows)
}

// GetEdgesBySourceAndType returns edges where the given node is source, filtered by type.
func (db *DB) GetEdgesBySourceAndType(sourceID string, edgeType model.EdgeType) ([]*model.Edge, error) {
	rows, err := db.execer().Query(
		`SELECT source_id, target_id, edge_type, weight, metadata, created_at
		 FROM edges WHERE source_id = ? AND edge_type = ?`, sourceID, string(edgeType))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEdges(rows)
}

// FindInsightsWithEntity returns insight IDs that have the given entity in their entities JSON array.
func (db *DB) FindInsightsWithEntity(entity string, excludeID string, limit int) ([]string, error) {
	rows, err := db.execer().Query(
		`SELECT DISTINCT i.id FROM insights i, json_each(i.entities) je
		 WHERE i.deleted_at IS NULL AND i.id != ? AND je.value = ?
		 ORDER BY i.created_at DESC LIMIT ?`,
		excludeID, entity, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// GetAllEdges returns all edges in the graph.
func (db *DB) GetAllEdges() ([]*model.Edge, error) {
	rows, err := db.execer().Query(
		`SELECT source_id, target_id, edge_type, weight, metadata, created_at FROM edges`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEdges(rows)
}

// DeleteEdge removes one typed edge between two nodes.
func (db *DB) DeleteEdge(sourceID string, targetID string, edgeType model.EdgeType) error {
	_, err := db.execer().Exec(
		`DELETE FROM edges WHERE source_id = ? AND target_id = ? AND edge_type = ?`,
		sourceID, targetID, string(edgeType))
	return err
}

// DeleteEdgesByNode removes all edges referencing a node.
func (db *DB) DeleteEdgesByNode(nodeID string) error {
	_, err := db.execer().Exec(
		`DELETE FROM edges WHERE source_id = ? OR target_id = ?`, nodeID, nodeID)
	return err
}

func scanEdges(rows interface {
	Next() bool
	Scan(...interface{}) error
}) ([]*model.Edge, error) {
	var results []*model.Edge
	for rows.Next() {
		var e model.Edge
		var edgeType, metadata, createdAt string
		err := rows.Scan(&e.SourceID, &e.TargetID, &edgeType, &e.Weight, &metadata, &createdAt)
		if err != nil {
			return nil, err
		}
		e.EdgeType = model.EdgeType(edgeType)
		e.ParseMetadata(metadata)
		if e.CreatedAt, err = time.Parse(time.RFC3339, createdAt); err != nil {
			return nil, fmt.Errorf("parse edge created_at (%s→%s): %w", e.SourceID, e.TargetID, err)
		}
		results = append(results, &e)
	}
	return results, nil
}
