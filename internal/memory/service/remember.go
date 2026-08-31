package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mnemon-dev/mnemon/internal/memory/embed"
	"github.com/mnemon-dev/mnemon/internal/memory/graph"
	"github.com/mnemon-dev/mnemon/internal/memory/model"
	"github.com/mnemon-dev/mnemon/internal/memory/search"
	"github.com/mnemon-dev/mnemon/internal/memory/store"
)

// RememberRequest describes a new insight and its graph behavior.
type RememberRequest struct {
	Content    string
	Category   string
	Importance int
	Tags       []string
	Source     string
	Entities   []string
	EntityMode string
	NoDiff     bool
}

// RememberResult reports the stored insight and any graph or diff effects.
// Effect-only fields are omitted when a duplicate is skipped.
type RememberResult struct {
	ID                  string                     `json:"id"`
	Content             string                     `json:"content"`
	Category            model.Category             `json:"category,omitempty"`
	Importance          int                        `json:"importance,omitempty"`
	Tags                *[]string                  `json:"tags,omitempty"`
	Entities            *[]string                  `json:"entities,omitempty"`
	Action              string                     `json:"action"`
	DiffSuggestion      search.DiffSuggestion      `json:"diff_suggestion"`
	ReplacedID          string                     `json:"replaced_id,omitempty"`
	CreatedAt           string                     `json:"created_at,omitempty"`
	EdgesCreated        *graph.EdgeStats           `json:"edges_created,omitempty"`
	SemanticCandidates  *[]graph.SemanticCandidate `json:"semantic_candidates,omitempty"`
	CausalCandidates    *[]graph.CausalCandidate   `json:"causal_candidates,omitempty"`
	Embedded            *bool                      `json:"embedded,omitempty"`
	EffectiveImportance *float64                   `json:"effective_importance,omitempty"`
	AutoPruned          *int                       `json:"auto_pruned,omitempty"`
	AutoPrunedIDs       *[]string                  `json:"auto_pruned_ids,omitempty"`
}

type normalizedRemember struct {
	content    string
	category   model.Category
	importance int
	tags       []string
	source     string
	entities   []string
	entityMode graph.EntityMode
	noDiff     bool
}

type embeddingState struct {
	vector []float64
	blob   []byte
	cache  graph.EmbedCache
}

type diffDecision struct {
	action     string
	suggestion search.DiffSuggestion
	replacedID string
}

type writeEffects struct {
	edges      graph.EdgeStats
	embedded   bool
	importance float64
	prunedIDs  []string
}

// Remember validates and stores one insight using the CLI's diff and graph
// semantics.
func (s *Service) Remember(ctx context.Context, request RememberRequest) (RememberResult, error) {
	ctx = normalizeContext(ctx)
	release, err := s.acquire(ctx)
	if err != nil {
		return RememberResult{}, err
	}
	defer release()

	normalized, err := normalizeRememberRequest(request)
	if err != nil {
		return RememberResult{}, err
	}
	db, err := s.openWritableDB("remember")
	if err != nil {
		return RememberResult{}, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	insight := newInsight(normalized)
	embedding := s.prepareEmbedding(ctx, db, normalized.content)
	decision, err := decideRememberDiff(db, normalized, embedding)
	if err != nil {
		return RememberResult{}, err
	}
	if decision.action == "skipped" {
		db.LogOp("diff-skip", insight.ID, fmt.Sprintf("duplicate of %s", decision.replacedID))
		return skippedRememberResult(insight, decision), nil
	}

	effects, err := s.persistInsight(db, insight, normalized.entityMode, embedding, decision)
	if err != nil {
		return RememberResult{}, err
	}
	return completeRememberResult(db, insight, embedding.cache, decision, effects), nil
}

func normalizeRememberRequest(request RememberRequest) (normalizedRemember, error) {
	if strings.TrimSpace(request.Content) == "" {
		return normalizedRemember{}, fmt.Errorf("content must not be empty")
	}
	if len(request.Content) > 8000 {
		return normalizedRemember{}, fmt.Errorf(
			"content too long (%d chars, max 8000); consider chunking into multiple remember calls",
			len(request.Content))
	}
	if request.Category == "" {
		request.Category = string(model.CategoryGeneral)
	}
	category := model.Category(request.Category)
	if !model.ValidCategories[category] {
		return normalizedRemember{}, fmt.Errorf(
			"invalid category %q; valid: preference, decision, fact, insight, context, general",
			request.Category)
	}
	if request.Importance == 0 {
		request.Importance = 3
	}
	if request.Importance < 1 || request.Importance > 5 {
		return normalizedRemember{}, fmt.Errorf("importance must be 1-5, got %d", request.Importance)
	}
	if request.Source == "" {
		request.Source = "user"
	}
	if request.EntityMode == "" {
		request.EntityMode = string(graph.EntityModeMerge)
	}
	entityMode := graph.EntityMode(request.EntityMode)
	if !graph.ValidEntityMode(entityMode) {
		return normalizedRemember{}, fmt.Errorf(
			"invalid entity mode %q; valid: merge, provided, auto", request.EntityMode)
	}
	tags, err := normalizeValues("tag", "tags", request.Tags, 20, 100)
	if err != nil {
		return normalizedRemember{}, err
	}
	entities, err := normalizeValues("entity", "entities", request.Entities, 50, 200)
	if err != nil {
		return normalizedRemember{}, err
	}
	return normalizedRemember{
		content: request.Content, category: category, importance: request.Importance,
		tags: tags, source: request.Source, entities: entities,
		entityMode: entityMode, noDiff: request.NoDiff,
	}, nil
}

func normalizeValues(label, plural string, values []string, maxCount, maxLength int) ([]string, error) {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if len(value) > maxLength {
			return nil, fmt.Errorf("%s too long (%d chars, max %d): %s",
				label, len(value), maxLength, value[:50])
		}
		normalized = append(normalized, value)
	}
	if len(normalized) > maxCount {
		return nil, fmt.Errorf("too many %s (%d, max %d)", plural, len(normalized), maxCount)
	}
	if normalized == nil {
		normalized = []string{}
	}
	return normalized, nil
}

func newInsight(request normalizedRemember) *model.Insight {
	now := time.Now().UTC()
	return &model.Insight{
		ID: uuid.New().String(), Content: request.content, Category: request.category,
		Importance: request.importance, Tags: request.tags, Entities: request.entities,
		Source: request.source, CreatedAt: now, UpdatedAt: now,
	}
}

func (s *Service) prepareEmbedding(ctx context.Context, db *store.DB, content string) embeddingState {
	client := embed.NewClientWithModel(s.config.EmbedModel)
	if !client.Available() || ctx.Err() != nil {
		return embeddingState{}
	}
	state := embeddingState{}
	if vector, err := client.Embed(content); err == nil {
		state.vector = vector
		state.blob = embed.SerializeVector(vector)
	}
	dbEmbeddings, err := db.GetAllEmbeddings()
	if err != nil {
		return state
	}
	state.cache = make(graph.EmbedCache, len(dbEmbeddings))
	for _, item := range dbEmbeddings {
		if vector := embed.DeserializeVector(item.Embedding); vector != nil {
			state.cache[item.ID] = vector
		}
	}
	return state
}

func decideRememberDiff(db *store.DB, request normalizedRemember, embedding embeddingState) (diffDecision, error) {
	if request.noDiff {
		return diffDecision{action: "added", suggestion: search.DiffAdd}, nil
	}
	insights, err := db.GetAllActiveInsights()
	if err != nil {
		return diffDecision{}, fmt.Errorf("load insights for diff: %w", err)
	}
	options := search.DiffOptions{Limit: 5, NewEmbedding: embedding.vector}
	if embedding.cache != nil {
		ids := make([]string, 0, len(embedding.cache))
		for id := range embedding.cache {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		options.ExistingEmbed = make([]search.EmbeddedItem, 0, len(ids))
		for _, id := range ids {
			options.ExistingEmbed = append(options.ExistingEmbed, search.EmbeddedItem{
				ID: id, Embedding: embedding.cache[id],
			})
		}
	}
	result := search.Diff(insights, request.content, options)
	decision := diffDecision{action: "added", suggestion: result.Suggestion}
	if len(result.Matches) == 0 {
		return decision, nil
	}
	switch result.Suggestion {
	case search.DiffDuplicate:
		decision.action = "skipped"
		decision.replacedID = result.Matches[0].ID
	case search.DiffUpdate:
		if result.Matches[0].TokenSimilarity >= 0.6 {
			decision.action = "updated"
			decision.replacedID = result.Matches[0].ID
		}
	}
	return decision, nil
}

func (s *Service) persistInsight(db *store.DB, insight *model.Insight, entityMode graph.EntityMode,
	embedding embeddingState, decision diffDecision) (writeEffects, error) {
	var effects writeEffects
	err := db.InTransaction(func() error {
		if decision.action == "updated" && decision.replacedID != "" {
			if err := db.SoftDeleteInsight(decision.replacedID); err != nil {
				fmt.Fprintf(s.config.Warnings, "warning: soft-delete %s: %v\n", decision.replacedID, err)
			} else {
				db.LogOp("diff-replace", decision.replacedID, fmt.Sprintf("replaced by %s", insight.ID))
				delete(embedding.cache, decision.replacedID)
			}
		}
		if err := db.InsertInsight(insight); err != nil {
			return fmt.Errorf("insert insight: %w", err)
		}
		if embedding.blob != nil {
			if err := db.UpdateEmbedding(insight.ID, embedding.blob); err != nil {
				return fmt.Errorf("update embedding: %w", err)
			}
			effects.embedded = true
			if embedding.cache != nil {
				embedding.cache[insight.ID] = embedding.vector
			}
		}
		effects.edges = graph.NewEngineWithEntityMode(db, embedding.cache, entityMode).OnInsightCreated(insight)
		s.updateRememberEntities(db, insight)
		effects.importance, _ = s.refreshImportance(db, insight.ID)
		var err error
		effects.prunedIDs, err = db.AutoPruneWithResult(store.MaxInsightsLimit(), []string{insight.ID}, insight.ID)
		if err != nil {
			return fmt.Errorf("auto-prune: %w", err)
		}
		db.LogOp("remember", insight.ID, s.auditDetail(insight.Content, "content redacted"))
		return nil
	})
	return effects, err
}

func (s *Service) updateRememberEntities(db *store.DB, insight *model.Insight) {
	if len(insight.Entities) == 0 {
		return
	}
	if err := db.UpdateEntities(insight.ID, insight.Entities); err != nil {
		fmt.Fprintf(s.config.Warnings, "warning: update entities: %v\n", err)
	}
}

func (s *Service) refreshImportance(db *store.DB, id string) (float64, error) {
	importance, err := db.RefreshEffectiveImportance(id)
	if err != nil {
		fmt.Fprintf(s.config.Warnings, "warning: refresh EI: %v\n", err)
	}
	return importance, err
}

func skippedRememberResult(insight *model.Insight, decision diffDecision) RememberResult {
	return RememberResult{
		ID: insight.ID, Content: insight.Content, Action: decision.action,
		DiffSuggestion: decision.suggestion, ReplacedID: decision.replacedID,
	}
}

func completeRememberResult(db *store.DB, insight *model.Insight, cache graph.EmbedCache,
	decision diffDecision, effects writeEffects) RememberResult {
	semantic := graph.FindSemanticCandidates(db, insight, cache)
	if semantic == nil {
		semantic = []graph.SemanticCandidate{}
	}
	causal := graph.FindCausalCandidates(db, insight)
	if causal == nil {
		causal = []graph.CausalCandidate{}
	}
	pruned := effects.prunedIDs
	if pruned == nil {
		pruned = []string{}
	}
	prunedCount := len(pruned)
	tags := insight.Tags
	if tags == nil {
		tags = []string{}
	}
	entities := insight.Entities
	if entities == nil {
		entities = []string{}
	}
	return RememberResult{
		ID: insight.ID, Content: insight.Content, Category: insight.Category,
		Importance: insight.Importance, Tags: &tags, Entities: &entities,
		Action: decision.action, DiffSuggestion: decision.suggestion,
		ReplacedID: decision.replacedID, CreatedAt: insight.CreatedAt.Format(time.RFC3339),
		EdgesCreated: &effects.edges, SemanticCandidates: &semantic, CausalCandidates: &causal,
		Embedded: &effects.embedded, EffectiveImportance: &effects.importance,
		AutoPruned: &prunedCount, AutoPrunedIDs: &pruned,
	}
}
