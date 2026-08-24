package model

import (
	"encoding/json"
	"sort"
	"time"
)

// EdgeType represents the type of relationship between insights.
type EdgeType string

const (
	EdgeTemporal EdgeType = "temporal"
	EdgeSemantic EdgeType = "semantic"
	EdgeCausal   EdgeType = "causal"
	EdgeEntity   EdgeType = "entity"
	// EdgeSupersedes records that the source insight replaces the target.
	// Unlike the other types it is not a similarity or co-occurrence signal
	// but an authority claim: the target is retained for lineage and is
	// demoted in recall so a corrected fact stops outranking its correction.
	EdgeSupersedes EdgeType = "supersedes"
)

var ValidEdgeTypes = map[EdgeType]bool{
	EdgeTemporal:   true,
	EdgeSemantic:   true,
	EdgeCausal:     true,
	EdgeEntity:     true,
	EdgeSupersedes: true,
}

// IsDirected reports whether the relation holds only from source to target.
// Similarity and co-occurrence are mutual, so callers record them both ways.
// Supersession is not mutual: it is a claim that one insight replaces the
// other, and the reverse edge would assert that a correction is itself
// superseded, demoting it alongside what it corrects.
func (t EdgeType) IsDirected() bool { return t == EdgeSupersedes }

// EdgeTypeNames returns the valid type names in a stable order, so help and
// error text cannot drift from the set actually accepted.
func EdgeTypeNames() []string {
	names := make([]string, 0, len(ValidEdgeTypes))
	for t := range ValidEdgeTypes {
		names = append(names, string(t))
	}
	sort.Strings(names)
	return names
}

// Edge represents a directed relationship between two insights.
type Edge struct {
	SourceID  string            `json:"source_id"`
	TargetID  string            `json:"target_id"`
	EdgeType  EdgeType          `json:"edge_type"`
	Weight    float64           `json:"weight"`
	Metadata  map[string]string `json:"metadata"`
	CreatedAt time.Time         `json:"created_at"`
}

// MetadataJSON returns metadata as a JSON string for storage.
func (e *Edge) MetadataJSON() string {
	b, _ := json.Marshal(e.Metadata)
	return string(b)
}

// ParseMetadata parses a JSON string into the Metadata field.
func (e *Edge) ParseMetadata(s string) {
	_ = json.Unmarshal([]byte(s), &e.Metadata)
	if e.Metadata == nil {
		e.Metadata = map[string]string{}
	}
}
