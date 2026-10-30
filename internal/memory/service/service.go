// Package service adapts the existing native Memory algorithms to the shared
// local service. It does not project native data into a new storage format.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/mnemon-dev/mnemon/internal/memory/graph"
	"github.com/mnemon-dev/mnemon/internal/memory/search"
	"github.com/mnemon-dev/mnemon/internal/memory/store"
)

type Embedder interface {
	Available() bool
	Embed(string) ([]float64, error)
}

type Request struct {
	Operation string `json:"operation"`
	Query     string `json:"query"`
	ID        string `json:"id"`
	Limit     int    `json:"limit"`
	Intent    string `json:"intent,omitempty"`
	Basic     bool   `json:"basic,omitempty"`
	Category  string `json:"category,omitempty"`
	Source    string `json:"source,omitempty"`
}

type Service struct {
	db    *store.DB
	embed Embedder
	// The legacy native DB owns transaction-local state. This adapter serializes
	// native calls only; records readers and external CLI writers remain live.
	mu sync.Mutex
}

func New(db *store.DB, embed Embedder) *Service { return &Service{db: db, embed: embed} }

func (s *Service) Dispatch(ctx context.Context, raw []byte) (any, error) {
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, errors.New("invalid native request JSON")
	}
	if req.Limit == 0 {
		req.Limit = 10
	}
	if req.Limit < 1 || req.Limit > 1000 || len(req.Query) > 8000 || len(req.ID) > 100 {
		return nil, errors.New("native request exceeds query, ID or limit bounds")
	}
	var intent *search.Intent
	if req.Intent != "" {
		parsed, err := search.IntentFromString(req.Intent)
		if err != nil {
			return nil, err
		}
		intent = &parsed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var vector []float64
	if req.Operation == "native.recall" && !req.Basic && s.embed != nil && s.embed.Available() {
		vector, _ = s.embed.Embed(req.Query)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch req.Operation {
	case "native.show":
		if req.ID == "" {
			return nil, errors.New("native.show requires id")
		}
		insight, err := s.db.GetInsightByID(req.ID)
		if err == nil {
			_ = s.db.IncrementAccessCount(insight.ID)
			s.db.LogOp("show", insight.ID, "full insight")
		}
		return insight, err
	case "native.search", "native.recall":
		if req.Query == "" {
			return nil, errors.New("native search requires query")
		}
		return s.query(req, vector, intent)
	default:
		return nil, errors.New("unknown native operation")
	}
}

func (s *Service) query(req Request, vector []float64, intent *search.Intent) (any, error) {
	if req.Operation == "native.recall" && req.Basic {
		results, err := s.db.QueryInsights(store.QueryFilter{Keyword: req.Query, Category: req.Category, Source: req.Source, Limit: req.Limit})
		if err == nil {
			for _, result := range results {
				_ = s.db.IncrementAccessCount(result.ID)
			}
			s.db.LogOp("recall:basic", "", fmt.Sprintf("q=%s hits=%d", req.Query, len(results)))
		}
		return results, err
	}
	all, err := s.db.GetAllActiveInsights()
	if err != nil {
		return nil, err
	}
	if req.Operation == "native.search" {
		results := search.KeywordSearch(all, req.Query, req.Limit)
		for _, result := range results {
			_ = s.db.IncrementAccessCount(result.Insight.ID)
		}
		s.db.LogOp("search", "", fmt.Sprintf("q=%s hits=%d", req.Query, len(results)))
		return results, nil
	}
	entities := graph.ExtractEntitiesIndexed(req.Query, store.KnownEntities(all))
	response, err := search.IntentAwareRecallFrom(s.db, all, req.Query, vector, entities, req.Limit, intent)
	if err == nil {
		for _, result := range response.Results {
			_ = s.db.IncrementAccessCount(result.Insight.ID)
		}
		s.db.LogOp("recall", "", fmt.Sprintf("q=%s hits=%d", req.Query, len(response.Results)))
	}
	return response, err
}
