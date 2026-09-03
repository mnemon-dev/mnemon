package service

import (
	"context"
	"fmt"
	"os"

	"github.com/mnemon-dev/mnemon/internal/memory/store"
)

// StatusResult contains aggregate statistics for the selected Memory store.
type StatusResult struct {
	TotalInsights   int                `json:"total_insights"`
	DeletedInsights int                `json:"deleted_insights"`
	ByCategory      map[string]int     `json:"by_category"`
	EdgeCount       int                `json:"edge_count"`
	TopEntities     []store.EntityStat `json:"top_entities"`
	OplogCount      int                `json:"oplog_count"`
	DBPath          string             `json:"db_path"`
	DBSizeBytes     int64              `json:"db_size_bytes"`
}

// Status returns statistics and storage metadata for the selected store.
func (s *Service) Status(ctx context.Context) (StatusResult, error) {
	ctx = normalizeContext(ctx)
	release, err := s.acquire(ctx)
	if err != nil {
		return StatusResult{}, err
	}
	defer release()

	db, err := s.openDB()
	if err != nil {
		return StatusResult{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	stats, err := db.GetStats()
	if err != nil {
		return StatusResult{}, fmt.Errorf("get stats: %w", err)
	}
	var size int64
	if info, err := os.Stat(db.Path()); err == nil {
		size = info.Size()
	}
	return StatusResult{
		TotalInsights: stats.Total, DeletedInsights: stats.DeletedCount,
		ByCategory: stats.ByCategory, EdgeCount: stats.EdgeCount,
		TopEntities: stats.TopEntities, OplogCount: stats.OplogCount,
		DBPath: db.Path(), DBSizeBytes: size,
	}, nil
}
