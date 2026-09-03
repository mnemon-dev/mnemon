package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memory/graph"
	"github.com/mnemon-dev/mnemon/internal/memory/model"
)

// RelatedRequest describes a bounded graph traversal from one insight.
type RelatedRequest struct {
	ID       string
	EdgeType string
	Depth    int
	Limit    int
}

// RelatedResult is one insight discovered by graph traversal.
type RelatedResult struct {
	ID         string `json:"id"`
	Content    string `json:"content"`
	Category   string `json:"category"`
	Importance int    `json:"importance"`
	Depth      int    `json:"depth"`
	EdgeType   string `json:"via_edge_type,omitempty"`
}

// Related returns insights reachable from the requested insight.
func (s *Service) Related(ctx context.Context, request RelatedRequest) ([]RelatedResult, error) {
	ctx = normalizeContext(ctx)
	release, err := s.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	if strings.TrimSpace(request.ID) == "" {
		return nil, fmt.Errorf("id must not be empty")
	}
	if request.Depth <= 0 {
		return nil, fmt.Errorf("depth must be greater than 0")
	}
	if request.Limit < 0 {
		return nil, fmt.Errorf("limit must be greater than 0")
	}
	var edgeFilter model.EdgeType
	if request.EdgeType != "" {
		edgeFilter = model.EdgeType(request.EdgeType)
		if !model.ValidEdgeTypes[edgeFilter] {
			return nil, fmt.Errorf(
				"invalid edge type %q; valid: temporal, semantic, causal, entity", request.EdgeType)
		}
	}
	db, err := s.openDB()
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	start, err := db.GetInsightByID(request.ID)
	if err != nil {
		return nil, fmt.Errorf("insight not found: %w", err)
	}

	nodes := graph.BFS(db, start.ID, graph.BFSOptions{
		MaxDepth: request.Depth, MaxNodes: request.Limit, EdgeFilter: edgeFilter,
	})
	results := make([]RelatedResult, 0, len(nodes))
	for _, node := range nodes {
		results = append(results, RelatedResult{
			ID: node.Insight.ID, Content: node.Insight.Content,
			Category: string(node.Insight.Category), Importance: node.Insight.Importance,
			Depth: node.Hop, EdgeType: string(node.ViaEdge.EdgeType),
		})
	}
	return results, nil
}

// LinkRequest describes a bidirectional typed edge between two insights.
type LinkRequest struct {
	SourceID  string
	TargetID  string
	EdgeType  string
	Weight    float64
	Metadata  map[string]string
	CreatedBy string
}

// LinkResult reports the durable edge pair.
type LinkResult struct {
	Status   string            `json:"status"`
	SourceID string            `json:"source_id"`
	TargetID string            `json:"target_id"`
	EdgeType string            `json:"edge_type"`
	Weight   float64           `json:"weight"`
	Metadata map[string]string `json:"metadata"`
}

// Link creates or replaces both directions of one typed relationship.
func (s *Service) Link(ctx context.Context, request LinkRequest) (LinkResult, error) {
	ctx = normalizeContext(ctx)
	release, err := s.acquire(ctx)
	if err != nil {
		return LinkResult{}, err
	}
	defer release()

	if strings.TrimSpace(request.SourceID) == "" || strings.TrimSpace(request.TargetID) == "" {
		return LinkResult{}, fmt.Errorf("source_id and target_id must not be empty")
	}
	if request.EdgeType == "" {
		request.EdgeType = string(model.EdgeSemantic)
	}
	edgeType := model.EdgeType(request.EdgeType)
	if !model.ValidEdgeTypes[edgeType] {
		return LinkResult{}, fmt.Errorf(
			"invalid edge type %q; valid: temporal, semantic, causal, entity", request.EdgeType)
	}
	if request.Weight < 0 || request.Weight > 1 {
		return LinkResult{}, fmt.Errorf("weight must be between 0.0 and 1.0, got %.2f", request.Weight)
	}
	db, err := s.openWritableDB("link")
	if err != nil {
		return LinkResult{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	if err := validateLinkEndpoints(db, request.SourceID, request.TargetID); err != nil {
		return LinkResult{}, err
	}
	metadata := copyMetadata(request.Metadata)
	if request.CreatedBy == "" {
		request.CreatedBy = "agent"
	}
	metadata["created_by"] = request.CreatedBy

	now := time.Now().UTC()
	err = db.InTransactionContext(ctx, func() error {
		for _, endpoints := range [][2]string{
			{request.SourceID, request.TargetID}, {request.TargetID, request.SourceID},
		} {
			if err := db.InsertEdge(&model.Edge{
				SourceID: endpoints[0], TargetID: endpoints[1], EdgeType: edgeType,
				Weight: request.Weight, Metadata: metadata, CreatedAt: now,
			}); err != nil {
				return fmt.Errorf("create edge %s→%s: %w", endpoints[0], endpoints[1], err)
			}
		}
		db.LogOp("link", request.SourceID, fmt.Sprintf("%s→%s type=%s weight=%.2f",
			truncateID(request.SourceID), truncateID(request.TargetID), request.EdgeType, request.Weight))
		return nil
	})
	if err != nil {
		return LinkResult{}, err
	}
	return LinkResult{
		Status: "linked", SourceID: request.SourceID, TargetID: request.TargetID,
		EdgeType: request.EdgeType, Weight: request.Weight, Metadata: metadata,
	}, nil
}

type insightLookup interface {
	GetInsightByID(string) (*model.Insight, error)
}

func validateLinkEndpoints(db insightLookup, sourceID, targetID string) error {
	if source, err := db.GetInsightByID(sourceID); err != nil || source == nil {
		return fmt.Errorf("source insight %s not found", sourceID)
	}
	if target, err := db.GetInsightByID(targetID); err != nil || target == nil {
		return fmt.Errorf("target insight %s not found", targetID)
	}
	return nil
}

func copyMetadata(metadata map[string]string) map[string]string {
	result := make(map[string]string, len(metadata)+1)
	for key, value := range metadata {
		result[key] = value
	}
	return result
}

func truncateID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
