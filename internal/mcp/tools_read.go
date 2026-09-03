package mcp

import (
	"context"
	"fmt"

	memoryservice "github.com/mnemon-dev/mnemon/internal/memory/service"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type recallInput struct {
	Query    string `json:"query" jsonschema:"natural-language memory query, at most 2000 characters"`
	Limit    *int   `json:"limit,omitempty" jsonschema:"maximum results, 1-100; defaults to 10"`
	Category string `json:"category,omitempty" jsonschema:"basic-mode category filter"`
	Source   string `json:"source,omitempty" jsonschema:"basic-mode source filter"`
	Basic    bool   `json:"basic,omitempty" jsonschema:"use simple SQL substring matching instead of intent-aware recall"`
	Intent   string `json:"intent,omitempty" jsonschema:"optional smart-recall intent override: WHY, WHEN, ENTITY, or GENERAL"`
	Full     bool   `json:"full,omitempty" jsonschema:"return complete content instead of the default 600-character projection"`
}

type searchInput struct {
	Query string `json:"query" jsonschema:"token search query, at most 2000 characters"`
	Limit *int   `json:"limit,omitempty" jsonschema:"maximum results, 1-100; defaults to 10"`
	Full  bool   `json:"full,omitempty" jsonschema:"return complete content instead of the default 600-character projection"`
}

type relatedInput struct {
	ID       string `json:"id" jsonschema:"starting insight ID"`
	EdgeType string `json:"edge_type,omitempty" jsonschema:"optional edge filter: temporal, semantic, causal, or entity"`
	Depth    *int   `json:"depth,omitempty" jsonschema:"maximum graph depth, 1-10; defaults to 2"`
	Limit    *int   `json:"limit,omitempty" jsonschema:"maximum results, 1-100; defaults to 20"`
	Full     bool   `json:"full,omitempty" jsonschema:"return complete content instead of the default 600-character projection"`
}

func (s *Server) registerReadTools() {
	sdkmcp.AddTool(s.sdk, &sdkmcp.Tool{
		Name: "recall", Description: "Retrieve durable insights using intent-aware graph search, or basic substring matching; records access history.",
		InputSchema: recallInputSchema(), Annotations: toolAnnotations(toolBehavior{
			readOnly: false, destructive: false, idempotent: false, openWorld: true,
		}),
	}, s.recall)
	sdkmcp.AddTool(s.sdk, &sdkmcp.Tool{
		Name: "search", Description: "Search durable insights with token-based relevance scoring; records access history.",
		InputSchema: searchInputSchema(), Annotations: toolAnnotations(toolBehavior{
			readOnly: false, destructive: false, idempotent: false, openWorld: false,
		}),
	}, s.search)
	sdkmcp.AddTool(s.sdk, &sdkmcp.Tool{
		Name: "related", Description: "Traverse typed graph edges from one insight to find related memories.",
		InputSchema: relatedInputSchema(), Annotations: toolAnnotations(toolBehavior{
			readOnly: true, destructive: false, idempotent: true, openWorld: false,
		}),
	}, s.related)
	sdkmcp.AddTool(s.sdk, &sdkmcp.Tool{
		Name: "status", Description: "Show aggregate statistics for the selected Mnemon store.",
		Annotations: toolAnnotations(toolBehavior{
			readOnly: true, destructive: false, idempotent: true, openWorld: false,
		}),
	}, s.status)
}

func (s *Server) recall(ctx context.Context, _ *sdkmcp.CallToolRequest, input recallInput) (*sdkmcp.CallToolResult, *insightListResult, error) {
	if err := validateText("query", input.Query, maxQueryChars); err != nil {
		return nil, nil, err
	}
	if !validCategory(input.Category) {
		return nil, nil, fmt.Errorf("invalid category %q", input.Category)
	}
	limit, err := boundedLimit(input.Limit, defaultLimit)
	if err != nil {
		return nil, nil, err
	}
	response, err := s.memory.Recall(ctx, memoryservice.RecallRequest{
		Query: input.Query, Category: input.Category, Source: input.Source,
		Limit: limit, Basic: input.Basic, Intent: input.Intent,
	})
	if err != nil {
		return nil, nil, err
	}
	output := projectRecall(response, input.Full)
	return nil, &output, nil
}

func (s *Server) search(ctx context.Context, _ *sdkmcp.CallToolRequest, input searchInput) (*sdkmcp.CallToolResult, *insightListResult, error) {
	if err := validateText("query", input.Query, maxQueryChars); err != nil {
		return nil, nil, err
	}
	limit, err := boundedLimit(input.Limit, defaultLimit)
	if err != nil {
		return nil, nil, err
	}
	results, err := s.memory.Search(ctx, memoryservice.SearchRequest{Query: input.Query, Limit: limit})
	if err != nil {
		return nil, nil, err
	}
	output := projectSearch(results, input.Full)
	return nil, &output, nil
}

func (s *Server) related(ctx context.Context, _ *sdkmcp.CallToolRequest, input relatedInput) (*sdkmcp.CallToolResult, *insightListResult, error) {
	if err := validateText("id", input.ID, maxIDChars); err != nil {
		return nil, nil, err
	}
	depth, err := boundedDepth(input.Depth)
	if err != nil {
		return nil, nil, err
	}
	limit, err := boundedLimit(input.Limit, defaultRelatedLimit)
	if err != nil {
		return nil, nil, err
	}
	results, err := s.memory.Related(ctx, memoryservice.RelatedRequest{
		ID: input.ID, EdgeType: input.EdgeType, Depth: depth, Limit: limit,
	})
	if err != nil {
		return nil, nil, err
	}
	output := projectRelated(results, input.Full)
	return nil, &output, nil
}

func (s *Server) status(ctx context.Context, _ *sdkmcp.CallToolRequest, _ struct{}) (*sdkmcp.CallToolResult, *memoryservice.StatusResult, error) {
	result, err := s.memory.Status(ctx)
	if err != nil {
		return nil, nil, err
	}
	return nil, &result, nil
}
