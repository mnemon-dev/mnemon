package graph

import (
	"github.com/mnemon-dev/mnemon/internal/model"
	"github.com/mnemon-dev/mnemon/internal/store"
)

// EdgeStats tracks how many edges of each type were created.
type EdgeStats struct {
	Temporal int `json:"temporal"`
	Entity   int `json:"entity"`
	Causal   int `json:"causal"`
	Semantic int `json:"semantic"`
}

// Engine orchestrates automatic edge creation when insights are stored.
type Engine struct {
	db         *store.DB
	embedCache EmbedCache
	entityMode EntityMode
}

// NewEngine creates a new graph edge engine.
// If embedCache is non-nil, it is reused for semantic operations instead of querying the database.
func NewEngine(db *store.DB, embedCache EmbedCache) *Engine {
	return NewEngineWithEntityMode(db, embedCache, EntityModeMerge)
}

// NewEngineWithEntityMode creates a graph engine with configurable entity handling.
func NewEngineWithEntityMode(db *store.DB, embedCache EmbedCache, entityMode EntityMode) *Engine {
	return &Engine{db: db, embedCache: embedCache, entityMode: entityMode}
}

// OnInsightCreated runs all edge generators for a newly created insight.
// It merges any pre-provided entities (e.g. from LLM) with regex-extracted ones.
func (e *Engine) OnInsightCreated(insight *model.Insight) EdgeStats {
	var stats EdgeStats

	// 1. Resolve entities from pre-provided values and/or regex+dictionary extraction.
	insight.Entities = ResolveEntities(insight.Content, insight.Entities, e.entityMode)

	// 2. Temporal backbone + proximity edges
	stats.Temporal = CreateTemporalEdge(e.db, insight)

	// 3. Entity co-occurrence edges
	stats.Entity = CreateEntityEdges(e.db, insight)

	// 4. Causal keyword edges
	stats.Causal = CreateCausalEdges(e.db, insight)

	// 5. Auto semantic edges (when embeddings available)
	stats.Semantic = CreateSemanticEdges(e.db, insight, e.embedCache)

	return stats
}
