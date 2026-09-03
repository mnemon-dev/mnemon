package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/mnemon-dev/mnemon/internal/memory/embed"
	"github.com/mnemon-dev/mnemon/internal/memory/graph"
	"github.com/mnemon-dev/mnemon/internal/memory/model"
	"github.com/mnemon-dev/mnemon/internal/memory/search"
	"github.com/mnemon-dev/mnemon/internal/memory/store"
)

// RecallRequest describes intent-aware or basic insight retrieval.
type RecallRequest struct {
	Query    string
	Category string
	Source   string
	Limit    int
	Basic    bool
	Intent   string
}

// RecallResponse contains exactly one of BasicResults or SmartResults.
type RecallResponse struct {
	BasicResults []*model.Insight
	SmartResults *search.RecallResponse
}

// Recall retrieves insights using the same retrieval semantics as the root
// recall command.
func (s *Service) Recall(ctx context.Context, request RecallRequest) (RecallResponse, error) {
	ctx = normalizeContext(ctx)
	release, err := s.acquire(ctx)
	if err != nil {
		return RecallResponse{}, err
	}
	defer release()

	limit, err := normalizeLimit(request.Limit)
	if err != nil {
		return RecallResponse{}, err
	}
	db, err := s.openDB()
	if err != nil {
		return RecallResponse{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	if request.Basic {
		return s.basicRecall(db, request, limit)
	}
	return s.smartRecall(ctx, db, request, limit)
}

func (s *Service) basicRecall(db *store.DB, request RecallRequest, limit int) (RecallResponse, error) {
	results, err := db.QueryInsights(store.QueryFilter{
		Keyword:  request.Query,
		Category: request.Category,
		Source:   request.Source,
		Limit:    limit,
	})
	if err != nil {
		return RecallResponse{}, fmt.Errorf("query insights: %w", err)
	}
	for _, result := range results {
		_ = db.IncrementAccessCount(result.ID)
	}
	detail := fmt.Sprintf("q=%s hits=%d", request.Query, len(results))
	db.LogOp("recall:basic", "", s.auditDetail(detail, fmt.Sprintf("hits=%d", len(results))))
	return RecallResponse{BasicResults: results}, nil
}

func (s *Service) smartRecall(ctx context.Context, db *store.DB, request RecallRequest, limit int) (RecallResponse, error) {
	var intentOverride *search.Intent
	if request.Intent != "" {
		parsed, err := search.IntentFromString(request.Intent)
		if err != nil {
			return RecallResponse{}, err
		}
		intentOverride = &parsed
	}

	var queryVector []float64
	embedder := embed.NewClientWithModel(s.config.EmbedModel)
	if embedder.AvailableContext(ctx) {
		vector, embedErr := embedder.EmbedContext(ctx, request.Query)
		if contextErr := ctx.Err(); contextErr != nil {
			return RecallResponse{}, contextErr
		}
		if embedErr == nil {
			queryVector = vector
		}
	} else if err := ctx.Err(); err != nil {
		return RecallResponse{}, err
	}
	knownEntities, _ := db.LoadKnownEntities()
	queryEntities := graph.ExtractEntitiesIndexed(request.Query, knownEntities)
	response, err := search.IntentAwareRecall(
		db, request.Query, queryVector, queryEntities, limit, intentOverride)
	if err != nil {
		return RecallResponse{}, fmt.Errorf("recall: %w", err)
	}
	for _, result := range response.Results {
		_ = db.IncrementAccessCount(result.Insight.ID)
	}
	detail := fmt.Sprintf("q=%s hits=%d", request.Query, len(response.Results))
	db.LogOp("recall", "", s.auditDetail(detail, fmt.Sprintf("hits=%d", len(response.Results))))
	return RecallResponse{SmartResults: &response}, nil
}

// SearchRequest describes token-ranked insight search.
type SearchRequest struct {
	Query string
	Limit int
}

// Search finds insights using token-based relevance scoring.
func (s *Service) Search(ctx context.Context, request SearchRequest) ([]search.ScoredInsight, error) {
	ctx = normalizeContext(ctx)
	release, err := s.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	if strings.TrimSpace(request.Query) == "" {
		return nil, fmt.Errorf("query must not be empty")
	}
	limit, err := normalizeLimit(request.Limit)
	if err != nil {
		return nil, err
	}
	db, err := s.openDB()
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	all, err := db.GetAllActiveInsights()
	if err != nil {
		return nil, fmt.Errorf("get insights: %w", err)
	}
	results := search.KeywordSearch(all, request.Query, limit)
	for _, result := range results {
		_ = db.IncrementAccessCount(result.Insight.ID)
	}
	detail := fmt.Sprintf("q=%s hits=%d", request.Query, len(results))
	db.LogOp("search", "", s.auditDetail(detail, fmt.Sprintf("hits=%d", len(results))))
	return results, nil
}
