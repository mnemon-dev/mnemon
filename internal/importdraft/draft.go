// Package importdraft defines the versioned public import schema for Mnemon
// memory draft files. The schema intentionally decouples from internal DB
// structures so both can evolve independently.
package importdraft

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/mnemon-dev/mnemon/internal/model"
)

// CurrentSchemaVersion is the only schema version this build can import.
const CurrentSchemaVersion = "1"

// MemoryDraft is the top-level structure of a memory draft file.
type MemoryDraft struct {
	// SchemaVersion must equal "1". Checked before any other field.
	SchemaVersion string `json:"schema_version"`

	// Source labels where this draft came from (e.g. "chat-export", "manual").
	// Stored on every imported insight unless overridden per-insight.
	Source string `json:"source,omitempty"`

	// Insights is the list of memory nodes to import.
	Insights []DraftInsight `json:"insights"`

	// Edges is an optional list of explicit relationships between insights.
	// Mnemon also auto-creates edges via its graph engine; these supplement
	// or override auto-detected relationships.
	Edges []DraftEdge `json:"edges,omitempty"`
}

// DraftInsight represents one memory node in the import file.
type DraftInsight struct {
	// Content is the text of the memory. Required; max 8000 characters.
	Content string `json:"content"`

	// Category classifies the type of knowledge.
	// One of: preference, decision, fact, insight, context, general.
	// Defaults to "general" when omitted.
	Category string `json:"category,omitempty"`

	// Importance is a 1–5 signal of how significant this memory is.
	// Defaults to 3 when omitted or 0.
	Importance int `json:"importance,omitempty"`

	// Tags are free-form labels (max 20, each max 100 chars).
	Tags []string `json:"tags,omitempty"`

	// Entities are named subjects in the memory (people, projects, tools).
	// Mnemon merges these with its own auto-extraction.
	Entities []string `json:"entities,omitempty"`

	// Source overrides the top-level source field for this specific insight.
	Source string `json:"source,omitempty"`

	// CreatedAt sets the original creation timestamp (RFC 3339).
	// Defaults to import time when omitted.
	CreatedAt string `json:"created_at,omitempty"`
}

// DraftEdge declares an explicit relationship between two insights.
// Both source_index and target_index are zero-based indices into the insights array.
type DraftEdge struct {
	// SourceIndex is the zero-based index into the insights array.
	SourceIndex int `json:"source_index"`

	// TargetIndex is the zero-based index into the insights array.
	TargetIndex int `json:"target_index"`

	// EdgeType is the kind of relationship.
	// One of: temporal, semantic, causal, entity.
	EdgeType string `json:"edge_type"`

	// Weight is the edge strength in [0.0, 1.0]. Defaults to 0.5.
	Weight float64 `json:"weight,omitempty"`

	// Reason is an optional human/LLM explanation of why the edge exists.
	// Stored as edge metadata.
	Reason string `json:"reason,omitempty"`
}

// Load reads and JSON-decodes a memory draft file.
func Load(path string) (*MemoryDraft, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	var draft MemoryDraft
	if err := json.Unmarshal(data, &draft); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}
	return &draft, nil
}

// Validate checks that the draft is well-formed before any DB writes.
func (d *MemoryDraft) Validate() error {
	if d.SchemaVersion != CurrentSchemaVersion {
		return fmt.Errorf("unsupported schema_version %q (expected %q)", d.SchemaVersion, CurrentSchemaVersion)
	}
	if len(d.Insights) == 0 {
		return fmt.Errorf("insights array is empty; nothing to import")
	}

	for i, ins := range d.Insights {
		if ins.Content == "" {
			return fmt.Errorf("insights[%d]: content is required", i)
		}
		if len(ins.Content) > 8000 {
			return fmt.Errorf("insights[%d]: content too long (%d chars, max 8000)", i, len(ins.Content))
		}

		cat := ins.Category
		if cat == "" {
			cat = string(model.CategoryGeneral)
		}
		if !model.ValidCategories[model.Category(cat)] {
			return fmt.Errorf("insights[%d]: invalid category %q", i, cat)
		}

		imp := ins.Importance
		if imp == 0 {
			imp = 3
		}
		if imp < 1 || imp > 5 {
			return fmt.Errorf("insights[%d]: importance must be 1–5, got %d", i, ins.Importance)
		}

		if len(ins.Tags) > 20 {
			return fmt.Errorf("insights[%d]: too many tags (%d, max 20)", i, len(ins.Tags))
		}
		for j, t := range ins.Tags {
			if len(t) > 100 {
				return fmt.Errorf("insights[%d]: tag[%d] too long (%d chars, max 100)", i, j, len(t))
			}
		}

		if len(ins.Entities) > 50 {
			return fmt.Errorf("insights[%d]: too many entities (%d, max 50)", i, len(ins.Entities))
		}
		for j, e := range ins.Entities {
			if len(e) > 200 {
				return fmt.Errorf("insights[%d]: entity[%d] too long (%d chars, max 200)", i, j, len(e))
			}
		}

		if ins.CreatedAt != "" {
			if _, err := time.Parse(time.RFC3339, ins.CreatedAt); err != nil {
				return fmt.Errorf("insights[%d]: created_at %q is not RFC 3339 (e.g. 2024-01-15T09:30:00Z)", i, ins.CreatedAt)
			}
		}
	}

	n := len(d.Insights)
	for i, edge := range d.Edges {
		if edge.SourceIndex < 0 || edge.SourceIndex >= n {
			return fmt.Errorf("edges[%d]: source_index %d out of range [0,%d)", i, edge.SourceIndex, n)
		}
		if edge.TargetIndex < 0 || edge.TargetIndex >= n {
			return fmt.Errorf("edges[%d]: target_index %d out of range [0,%d)", i, edge.TargetIndex, n)
		}
		if edge.SourceIndex == edge.TargetIndex {
			return fmt.Errorf("edges[%d]: source_index and target_index must differ", i)
		}
		if !model.ValidEdgeTypes[model.EdgeType(edge.EdgeType)] {
			return fmt.Errorf("edges[%d]: invalid edge_type %q (valid: temporal, semantic, causal, entity)", i, edge.EdgeType)
		}
		if edge.Weight < 0 || edge.Weight > 1.0 {
			return fmt.Errorf("edges[%d]: weight %g out of range [0.0, 1.0]", i, edge.Weight)
		}
	}

	return nil
}

// ResolvedSource returns the source to use for a given insight index, falling
// back to the top-level source, then to "import".
func (d *MemoryDraft) ResolvedSource(idx int) string {
	if d.Insights[idx].Source != "" {
		return d.Insights[idx].Source
	}
	if d.Source != "" {
		return d.Source
	}
	return "import"
}
