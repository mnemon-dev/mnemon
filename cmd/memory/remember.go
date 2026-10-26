package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mnemon-dev/mnemon/internal/memory/embed"
	"github.com/mnemon-dev/mnemon/internal/memory/graph"
	"github.com/mnemon-dev/mnemon/internal/memory/model"
	"github.com/mnemon-dev/mnemon/internal/memory/search"
	"github.com/mnemon-dev/mnemon/internal/memory/store"
	"github.com/spf13/cobra"
)

var (
	remCategory   string
	remImportance int
	remTags       string
	remSource     string
	remEntities   string
	remEntityMode string
	remNoDiff     bool
)

var rememberCmd = &cobra.Command{
	Use:   "remember [content]",
	Short: "Store a new insight",
	Long:  "Store a new insight into the memory graph with optional category, importance, and tags.",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		content := strings.Join(args, " ")
		if len(content) > 8000 {
			return fmt.Errorf("content too long (%d chars, max 8000); consider chunking into multiple remember calls", len(content))
		}

		cat := model.Category(remCategory)
		if !model.ValidCategories[cat] {
			return fmt.Errorf("invalid category %q; valid: preference, decision, fact, insight, context, general", remCategory)
		}
		if remImportance < 1 || remImportance > 5 {
			return fmt.Errorf("importance must be 1-5, got %d", remImportance)
		}
		entityMode := graph.EntityMode(remEntityMode)
		if !graph.ValidEntityMode(entityMode) {
			return fmt.Errorf("invalid entity mode %q; valid: merge, provided, auto", remEntityMode)
		}

		var tags []string
		if remTags != "" {
			for _, t := range strings.Split(remTags, ",") {
				t = strings.TrimSpace(t)
				if t != "" {
					if len(t) > 100 {
						return fmt.Errorf("tag too long (%d chars, max 100): %s", len(t), truncateRunes(t, 50))
					}
					tags = append(tags, t)
				}
			}
			if len(tags) > 20 {
				return fmt.Errorf("too many tags (%d, max 20)", len(tags))
			}
		}
		if tags == nil {
			tags = []string{}
		}

		var entities []string
		if remEntities != "" {
			for _, e := range strings.Split(remEntities, ",") {
				e = strings.TrimSpace(e)
				if e != "" {
					if len(e) > 200 {
						return fmt.Errorf("entity too long (%d chars, max 200): %s", len(e), truncateRunes(e, 50))
					}
					entities = append(entities, e)
				}
			}
			if len(entities) > 50 {
				return fmt.Errorf("too many entities (%d, max 50)", len(entities))
			}
		}
		if entities == nil {
			entities = []string{}
		}

		now := time.Now().UTC()
		insight := &model.Insight{
			ID:         uuid.New().String(),
			Content:    content,
			Category:   cat,
			Importance: remImportance,
			Tags:       tags,
			Entities:   entities,
			Source:     remSource,
			CreatedAt:  now,
			UpdatedAt:  now,
		}

		db, err := openWritableDB("remember")
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer db.Close()

		// 1. Compute embedding BEFORE the transaction (HTTP call should not hold a DB lock)
		var embeddingBlob []byte
		var embeddingVec []float64
		ec := embed.NewClientWithModel(resolveEmbedModel())
		if ec.Available() {
			if vec, err := ec.Embed(content); err == nil {
				embeddingVec = vec
				embeddingBlob = embed.SerializeVector(vec)
			}
		}

		// 2. Built-in diff: check for duplicates/conflicts (read-only, before transaction)
		diffAction := "added"
		diffSuggestion := search.DiffAdd
		var duplicateID string

		// Build embed cache once — reused by diff, engine, and semantic candidates.
		var embedCache graph.EmbedCache
		if ec.Available() {
			dbEmbeds, err := db.GetAllEmbeddings()
			if err == nil {
				embedCache = make(graph.EmbedCache, len(dbEmbeds))
				for _, e := range dbEmbeds {
					if v := embed.DeserializeVector(e.Embedding); v != nil {
						embedCache[e.ID] = v
					}
				}
			}
		}

		if !remNoDiff {
			allInsights, err := db.GetAllActiveInsights()
			if err != nil {
				return fmt.Errorf("load insights for diff: %w", err)
			}

			opts := search.DiffOptions{Limit: 5, NewEmbedding: embeddingVec}
			if embedCache != nil {
				opts.ExistingEmbed = make([]search.EmbeddedItem, 0, len(embedCache))
				for id, v := range embedCache {
					opts.ExistingEmbed = append(opts.ExistingEmbed, search.EmbeddedItem{
						ID:        id,
						Embedding: v,
					})
				}
			}

			result := search.Diff(allInsights, content, opts)
			diffSuggestion = result.Suggestion

			// Similarity cannot establish whether two facts have the same
			// subject, value, or relationship. Keep diff suggestions advisory;
			// only byte-identical content permits skipping a write.
			duplicateID = search.FindExactDuplicateID(allInsights, content)
			if duplicateID != "" {
				diffAction = "skipped"
				diffSuggestion = search.DiffDuplicate
			}
		}

		// If duplicate, skip insert entirely
		if diffAction == "skipped" {
			db.LogOp("diff-skip", insight.ID, fmt.Sprintf("duplicate of %s", duplicateID))
			output := map[string]interface{}{
				"id":              insight.ID,
				"content":         content,
				"action":          "skipped",
				"diff_suggestion": string(diffSuggestion),
				"replaced_id":     duplicateID,
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(output)
		}

		// 3. All DB writes in a single atomic transaction
		var (
			edgeStats graph.EdgeStats
			ei        float64
			prunedIDs []string
			embedded  bool
		)
		err = db.InTransaction(func() error {
			if err := db.InsertInsight(insight); err != nil {
				return fmt.Errorf("insert insight: %w", err)
			}

			if embeddingBlob != nil {
				if err := db.UpdateEmbedding(insight.ID, embeddingBlob); err != nil {
					return fmt.Errorf("update embedding: %w", err)
				}
				embedded = true
				// Add the new insight's embedding to the cache so the engine sees it.
				if embedCache != nil {
					embedCache[insight.ID] = embeddingVec
				}
			}

			// Run graph edge engine (includes auto semantic edges when embedded)
			engine := graph.NewEngineWithEntityMode(db, embedCache, entityMode)
			edgeStats = engine.OnInsightCreated(insight)

			// Update entities extracted by the engine
			if len(insight.Entities) > 0 {
				if err := db.UpdateEntities(insight.ID, insight.Entities); err != nil {
					fmt.Fprintf(os.Stderr, "warning: update entities: %v\n", err)
				}
			}

			// Compute and store effective_importance (after edges are created)
			var eiErr error
			ei, eiErr = db.RefreshEffectiveImportance(insight.ID)
			if eiErr != nil {
				fmt.Fprintf(os.Stderr, "warning: refresh EI: %v\n", eiErr)
			}

			// Auto-prune if over capacity (excludeID protects the just-created insight)
			var pruneErr error
			prunedIDs, pruneErr = db.AutoPruneWithResult(store.MaxInsightsLimit(), []string{insight.ID}, insight.ID)
			if pruneErr != nil {
				return fmt.Errorf("auto-prune: %w", pruneErr)
			}

			db.LogOp("remember", insight.ID, insight.Content)
			return nil
		})
		if err != nil {
			// Cache was mutated inside the transaction closure (added entries).
			// On rollback those mutations don't match DB state, so discard the cache
			// to prevent any future code from accidentally using stale data.
			embedCache = nil
			return err
		}

		// 4. Read-only operations outside the transaction (data already committed)
		// Note: embedCache may still contain entries for insights pruned by AutoPrune.
		// findCandidatesByEmbedding safely filters them via GetInsightByID (deleted_at check).
		semanticCandidates := graph.FindSemanticCandidates(db, insight, embedCache)
		if semanticCandidates == nil {
			semanticCandidates = []graph.SemanticCandidate{}
		}

		causalCandidates := graph.FindCausalCandidates(db, insight)
		if causalCandidates == nil {
			causalCandidates = []graph.CausalCandidate{}
		}

		output := map[string]interface{}{
			"id":                   insight.ID,
			"content":              insight.Content,
			"category":             insight.Category,
			"importance":           insight.Importance,
			"tags":                 insight.Tags,
			"entities":             insight.Entities,
			"action":               diffAction,
			"diff_suggestion":      string(diffSuggestion),
			"created_at":           insight.CreatedAt.Format(time.RFC3339),
			"edges_created":        edgeStats,
			"semantic_candidates":  semanticCandidates,
			"causal_candidates":    causalCandidates,
			"embedded":             embedded,
			"effective_importance": ei,
			"auto_pruned":          len(prunedIDs),
			"auto_pruned_ids":      prunedIDs,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(output)
	},
}

func init() {
	rememberCmd.Flags().StringVar(&remCategory, "cat", "general", "category (preference|decision|fact|insight|context|general)")
	rememberCmd.Flags().IntVar(&remImportance, "imp", 3, "importance (1-5)")
	rememberCmd.Flags().StringVar(&remTags, "tags", "", "comma-separated tags")
	rememberCmd.Flags().StringVar(&remSource, "source", "user", "source (user|agent|external)")
	rememberCmd.Flags().StringVar(&remEntities, "entities", "", "comma-separated entities (LLM-extracted, merged with auto-extraction)")
	rememberCmd.Flags().StringVar(&remEntityMode, "entity-mode", string(graph.EntityModeMerge), "entity handling mode (merge|provided|auto)")
	rememberCmd.Flags().BoolVar(&remNoDiff, "no-diff", false, "skip duplicate/conflict detection")
	rootCmd.AddCommand(rememberCmd)
}
