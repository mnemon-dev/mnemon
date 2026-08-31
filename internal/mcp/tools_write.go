package mcp

import (
	"context"
	"fmt"
	"unicode/utf8"

	memoryservice "github.com/mnemon-dev/mnemon/internal/memory/service"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type rememberInput struct {
	Content    string   `json:"content" jsonschema:"durable insight content, at most 8000 bytes"`
	Category   string   `json:"category,omitempty" jsonschema:"preference, decision, fact, insight, context, or general; defaults to general"`
	Importance *int     `json:"importance,omitempty" jsonschema:"importance from 1 to 5; defaults to 3"`
	Tags       []string `json:"tags,omitempty" jsonschema:"up to 20 tags, each at most 100 characters"`
	Source     string   `json:"source,omitempty" jsonschema:"memory source, at most 100 characters; defaults to user"`
	Entities   []string `json:"entities,omitempty" jsonschema:"up to 50 explicit entities, each at most 200 characters"`
	EntityMode string   `json:"entity_mode,omitempty" jsonschema:"entity handling: merge, provided, or auto; defaults to merge"`
	NoDiff     bool     `json:"no_diff,omitempty" jsonschema:"skip duplicate and update detection"`
}

type linkInput struct {
	SourceID string            `json:"source_id" jsonschema:"source insight ID"`
	TargetID string            `json:"target_id" jsonschema:"target insight ID"`
	EdgeType string            `json:"edge_type,omitempty" jsonschema:"temporal, semantic, causal, or entity; defaults to semantic"`
	Weight   *float64          `json:"weight,omitempty" jsonschema:"edge weight from 0.0 to 1.0; defaults to 0.5"`
	Metadata map[string]string `json:"metadata,omitempty" jsonschema:"up to 20 metadata entries; keys at most 100 and values at most 1000 characters"`
}

func (s *Server) registerWriteTools() {
	sdkmcp.AddTool(s.sdk, &sdkmcp.Tool{
		Name: "remember", Description: "Store one durable insight and update its memory graph; duplicate detection may skip or replace an older insight.",
		InputSchema: rememberInputSchema(), Annotations: toolAnnotations(false, true, false),
	}, s.remember)
	sdkmcp.AddTool(s.sdk, &sdkmcp.Tool{
		Name: "link", Description: "Create or replace a bidirectional typed relationship between two stored insights.",
		InputSchema: linkInputSchema(), Annotations: toolAnnotations(false, true, true),
	}, s.link)
}

func (s *Server) remember(ctx context.Context, _ *sdkmcp.CallToolRequest, input rememberInput) (*sdkmcp.CallToolResult, *memoryservice.RememberResult, error) {
	if input.Category != "" && !validCategory(input.Category) {
		return nil, nil, fmt.Errorf("invalid category %q", input.Category)
	}
	if input.Source != "" && utf8.RuneCountInString(input.Source) > maxSourceChars {
		return nil, nil, fmt.Errorf("source is too long (max %d characters)", maxSourceChars)
	}
	importance := 0
	if input.Importance != nil {
		importance = *input.Importance
	}
	result, err := s.memory.Remember(ctx, memoryservice.RememberRequest{
		Content: input.Content, Category: input.Category, Importance: importance,
		Tags: input.Tags, Source: input.Source, Entities: input.Entities,
		EntityMode: input.EntityMode, NoDiff: input.NoDiff,
	})
	if err != nil {
		return nil, nil, err
	}
	return nil, &result, nil
}

func (s *Server) link(ctx context.Context, _ *sdkmcp.CallToolRequest, input linkInput) (*sdkmcp.CallToolResult, *memoryservice.LinkResult, error) {
	if err := validateText("source_id", input.SourceID, maxIDChars); err != nil {
		return nil, nil, err
	}
	if err := validateText("target_id", input.TargetID, maxIDChars); err != nil {
		return nil, nil, err
	}
	if err := validateMetadata(input.Metadata); err != nil {
		return nil, nil, err
	}
	weight := defaultWeight
	if input.Weight != nil {
		weight = *input.Weight
	}
	result, err := s.memory.Link(ctx, memoryservice.LinkRequest{
		SourceID: input.SourceID, TargetID: input.TargetID, EdgeType: input.EdgeType,
		Weight: weight, Metadata: input.Metadata, CreatedBy: "mcp",
	})
	if err != nil {
		return nil, nil, err
	}
	return nil, &result, nil
}

func validateMetadata(metadata map[string]string) error {
	if len(metadata) > maxMetadata {
		return fmt.Errorf("metadata has too many entries (%d, max %d)", len(metadata), maxMetadata)
	}
	for key, value := range metadata {
		if utf8.RuneCountInString(key) > maxMetadataKey {
			return fmt.Errorf("metadata key is too long (max %d characters)", maxMetadataKey)
		}
		if utf8.RuneCountInString(value) > maxMetadataValue {
			return fmt.Errorf("metadata value for %q is too long (max %d characters)", key, maxMetadataValue)
		}
	}
	return nil
}
