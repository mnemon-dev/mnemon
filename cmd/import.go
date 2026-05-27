package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/mnemon-dev/mnemon/internal/embed"
	"github.com/mnemon-dev/mnemon/internal/graph"
	"github.com/mnemon-dev/mnemon/internal/importdraft"
	"github.com/mnemon-dev/mnemon/internal/model"
	"github.com/mnemon-dev/mnemon/internal/search"
	"github.com/mnemon-dev/mnemon/internal/store"
	"github.com/spf13/cobra"
)

var (
	importNoDiff bool
	importDryRun bool
)

var importCmd = &cobra.Command{
	Use:   "import <file>",
	Short: "Import a memory draft file",
	Long: `Import insights from a memory draft JSON file (schema_version: "1").

Each insight passes through Mnemon's normal write path: deduplication,
graph edge construction, embeddings, and lifecycle scoring are all applied
automatically.

The draft format and a reference LLM prompt for generating it from chat
exports are documented in docs/IMPORT.md.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		draft, err := importdraft.Load(args[0])
		if err != nil {
			return err
		}
		if err := draft.Validate(); err != nil {
			return fmt.Errorf("invalid draft: %w", err)
		}

		if importDryRun {
			fmt.Printf("Dry run: %d insights, %d explicit edges — validation passed.\n",
				len(draft.Insights), len(draft.Edges))
			return nil
		}

		db, err := openDB()
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer db.Close()

		ec := embed.NewClientWithModel(resolveEmbedModel())

		// Build embed cache once for all diff and graph operations.
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

		// imported maps draft index → assigned insight ID (for explicit edge resolution).
		imported := make(map[int]string, len(draft.Insights))
		importedIDs := make(map[string]bool, len(draft.Insights))
		importedSources := make(map[string]bool)
		refreshIDs := make(map[string]bool)
		results := make([]importResult, 0, len(draft.Insights))

		for idx, di := range draft.Insights {
			cat := model.Category(di.Category)
			if cat == "" {
				cat = model.CategoryGeneral
			}
			imp := di.Importance
			if imp == 0 {
				imp = 3
			}
			tags := di.Tags
			if tags == nil {
				tags = []string{}
			}
			entities := di.Entities
			if entities == nil {
				entities = []string{}
			}

			var createdAt time.Time
			if di.CreatedAt != "" {
				if t, err := time.Parse(time.RFC3339, di.CreatedAt); err == nil {
					createdAt = t.UTC()
				}
			}
			if createdAt.IsZero() {
				createdAt = time.Now().UTC()
			}

			insight := &model.Insight{
				ID:         uuid.New().String(),
				Content:    di.Content,
				Category:   cat,
				Importance: imp,
				Tags:       tags,
				Entities:   entities,
				Source:     draft.ResolvedSource(idx),
				CreatedAt:  createdAt,
				UpdatedAt:  createdAt,
			}

			// Compute embedding before acquiring the DB lock.
			var embeddingBlob []byte
			var embeddingVec []float64
			if ec.Available() {
				if vec, err := ec.Embed(insight.Content); err == nil {
					embeddingVec = vec
					embeddingBlob = embed.SerializeVector(vec)
				}
			}

			var action string
			var replacedID string

			if importNoDiff {
				action = "added"
			} else {
				allInsights, err := db.GetAllActiveInsights()
				if err != nil {
					results = append(results, importResult{Index: idx, ID: insight.ID, Content: insight.Content, Error: err.Error()})
					continue
				}
				opts := search.DiffOptions{Limit: 5, NewEmbedding: embeddingVec}
				if embedCache != nil {
					opts.ExistingEmbed = make([]search.EmbeddedItem, 0, len(embedCache))
					for id, v := range embedCache {
						opts.ExistingEmbed = append(opts.ExistingEmbed, search.EmbeddedItem{ID: id, Embedding: v})
					}
				}
				result := search.Diff(allInsights, insight.Content, opts)
				switch result.Suggestion {
				case search.DiffDuplicate:
					action = "skipped"
					if len(result.Matches) > 0 {
						replacedID = result.Matches[0].ID
					}
				case search.DiffConflict, search.DiffUpdate:
					action = "updated"
					if len(result.Matches) > 0 {
						replacedID = result.Matches[0].ID
					}
				default:
					action = "added"
				}
			}

			if action == "skipped" {
				db.LogOp("import-skip", insight.ID, fmt.Sprintf("duplicate of %s", replacedID))
				if replacedID != "" {
					imported[idx] = replacedID
				} else {
					imported[idx] = insight.ID
				}
				results = append(results, importResult{Index: idx, ID: imported[idx], Content: insight.Content, Action: action})
				continue
			}

			var writeErr error
			err = db.InTransaction(func() error {
				if action == "updated" && replacedID != "" {
					if err := db.SoftDeleteInsight(replacedID); err != nil {
						fmt.Fprintf(os.Stderr, "warning: soft-delete %s: %v\n", replacedID, err)
					} else {
						db.LogOp("import-replace", replacedID, fmt.Sprintf("replaced by %s", insight.ID))
						delete(embedCache, replacedID)
					}
				}
				if err := db.InsertInsight(insight); err != nil {
					return fmt.Errorf("insert insight: %w", err)
				}
				if embeddingBlob != nil {
					if err := db.UpdateEmbedding(insight.ID, embeddingBlob); err != nil {
						return fmt.Errorf("update embedding: %w", err)
					}
					if embedCache != nil {
						embedCache[insight.ID] = embeddingVec
					}
				}
				engine := graph.NewEngineWithOptions(db, embedCache, graph.EngineOptions{
					EntityMode:   graph.EntityModeMerge,
					TemporalMode: graph.TemporalDisabled,
				})
				engine.OnInsightCreated(insight)
				if len(insight.Entities) > 0 {
					_ = db.UpdateEntities(insight.ID, insight.Entities)
				}
				if _, err := db.RefreshEffectiveImportance(insight.ID); err != nil {
					fmt.Fprintf(os.Stderr, "warning: refresh EI for %s: %v\n", insight.ID, err)
				}
				db.LogOp("import", insight.ID, insight.Content)
				return nil
			})
			if err != nil {
				writeErr = err
				embedCache = nil
			}

			if writeErr != nil {
				results = append(results, importResult{Index: idx, ID: insight.ID, Content: insight.Content, Error: writeErr.Error()})
				continue
			}

			imported[idx] = insight.ID
			importedIDs[insight.ID] = true
			importedSources[insight.Source] = true
			refreshIDs[insight.ID] = true
			results = append(results, importResult{Index: idx, ID: insight.ID, Content: insight.Content, Action: action})
		}

		edgesInserted := 0
		temporalEdgesRepaired := 0
		pruned := 0
		if err := db.InTransaction(func() error {
			// Insert explicit edges for successfully imported insights.
			for _, de := range draft.Edges {
				srcID, srcOK := imported[de.SourceIndex]
				tgtID, tgtOK := imported[de.TargetIndex]
				if !srcOK || !tgtOK {
					continue
				}
				w := de.Weight
				if w == 0 {
					w = 0.5
				}
				meta := map[string]string{}
				if de.Reason != "" {
					meta["reason"] = de.Reason
				}
				edge := &model.Edge{
					SourceID:  srcID,
					TargetID:  tgtID,
					EdgeType:  model.EdgeType(de.EdgeType),
					Weight:    w,
					Metadata:  meta,
					CreatedAt: time.Now().UTC(),
				}
				if err := db.InsertEdge(edge); err != nil {
					fmt.Fprintf(os.Stderr, "warning: insert explicit edge %d→%d: %v\n", de.SourceIndex, de.TargetIndex, err)
					continue
				}
				edgesInserted++
				refreshIDs[srcID] = true
				refreshIDs[tgtID] = true
			}

			repaired, touched, err := repairImportedTemporalEdges(db, importedSources, importedIDs)
			if err != nil {
				return err
			}
			temporalEdgesRepaired = repaired
			for id := range touched {
				refreshIDs[id] = true
			}

			for id := range refreshIDs {
				if _, err := db.RefreshEffectiveImportance(id); err != nil {
					fmt.Fprintf(os.Stderr, "warning: refresh EI for %s: %v\n", id, err)
				}
			}

			var pruneErr error
			pruned, pruneErr = db.AutoPrune(store.MaxInsights, nil)
			return pruneErr
		}); err != nil {
			return fmt.Errorf("finalize import graph: %w", err)
		}

		_ = temporalEdgesRepaired // computed internally; not surfaced in default output
		summary := map[string]interface{}{
			"imported":       countAction(results, "added"),
			"updated":        countAction(results, "updated"),
			"skipped":        countAction(results, "skipped"),
			"errors":         countErrors(results),
			"edges_inserted": edgesInserted,
			"auto_pruned":    pruned,
			"results":        results,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(summary)
	},
}

func repairImportedTemporalEdges(db *store.DB, sources map[string]bool, importedIDs map[string]bool) (int, map[string]bool, error) {
	touched := make(map[string]bool)
	if len(importedIDs) == 0 {
		return 0, touched, nil
	}

	inserted := 0
	for source := range sources {
		timeline, err := db.GetActiveInsightsBySourceOrdered(source)
		if err != nil {
			return inserted, touched, fmt.Errorf("load temporal timeline for source %q: %w", source, err)
		}
		if len(timeline) == 0 {
			continue
		}

		for idx, insight := range timeline {
			if !importedIDs[insight.ID] {
				continue
			}
			touched[insight.ID] = true

			prevExisting := nearestNonImportedBefore(timeline, importedIDs, idx)
			nextExisting := nearestNonImportedAfter(timeline, importedIDs, idx)
			if prevExisting != nil && nextExisting != nil {
				if err := db.DeleteEdge(prevExisting.ID, nextExisting.ID, model.EdgeTemporal); err != nil {
					return inserted, touched, fmt.Errorf("delete temporal edge %s→%s: %w", prevExisting.ID, nextExisting.ID, err)
				}
				if err := db.DeleteEdge(nextExisting.ID, prevExisting.ID, model.EdgeTemporal); err != nil {
					return inserted, touched, fmt.Errorf("delete temporal edge %s→%s: %w", nextExisting.ID, prevExisting.ID, err)
				}
				touched[prevExisting.ID] = true
				touched[nextExisting.ID] = true
			}
		}

		now := time.Now().UTC()
		for idx := 0; idx < len(timeline)-1; idx++ {
			prev := timeline[idx]
			next := timeline[idx+1]
			if !importedIDs[prev.ID] && !importedIDs[next.ID] {
				continue
			}

			if err := db.InsertEdge(&model.Edge{
				SourceID:  prev.ID,
				TargetID:  next.ID,
				EdgeType:  model.EdgeTemporal,
				Weight:    1.0,
				Metadata:  map[string]string{"sub_type": "backbone", "direction": "precedes"},
				CreatedAt: now,
			}); err != nil {
				return inserted, touched, fmt.Errorf("insert temporal edge %s→%s: %w", prev.ID, next.ID, err)
			}
			inserted++

			if err := db.InsertEdge(&model.Edge{
				SourceID:  next.ID,
				TargetID:  prev.ID,
				EdgeType:  model.EdgeTemporal,
				Weight:    1.0,
				Metadata:  map[string]string{"sub_type": "backbone", "direction": "succeeds"},
				CreatedAt: now,
			}); err != nil {
				return inserted, touched, fmt.Errorf("insert temporal edge %s→%s: %w", next.ID, prev.ID, err)
			}
			inserted++
			touched[prev.ID] = true
			touched[next.ID] = true
		}
	}

	return inserted, touched, nil
}

func nearestNonImportedBefore(timeline []*model.Insight, importedIDs map[string]bool, idx int) *model.Insight {
	for i := idx - 1; i >= 0; i-- {
		if !importedIDs[timeline[i].ID] {
			return timeline[i]
		}
	}
	return nil
}

func nearestNonImportedAfter(timeline []*model.Insight, importedIDs map[string]bool, idx int) *model.Insight {
	for i := idx + 1; i < len(timeline); i++ {
		if !importedIDs[timeline[i].ID] {
			return timeline[i]
		}
	}
	return nil
}

func countAction(results []importResult, action string) int {
	n := 0
	for _, r := range results {
		if r.Action == action {
			n++
		}
	}
	return n
}

func countErrors(results []importResult) int {
	n := 0
	for _, r := range results {
		if r.Error != "" {
			n++
		}
	}
	return n
}

type importResult struct {
	Index   int    `json:"index"`
	ID      string `json:"id"`
	Content string `json:"content"`
	Action  string `json:"action"`
	Error   string `json:"error,omitempty"`
}

func init() {
	importCmd.Flags().BoolVar(&importNoDiff, "no-diff", false, "skip deduplication; insert all insights as new")
	importCmd.Flags().BoolVar(&importDryRun, "dry-run", false, "validate the draft file without writing to the database")
	rootCmd.AddCommand(importCmd)
}
