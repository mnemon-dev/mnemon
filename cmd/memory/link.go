package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memory/model"
	"github.com/spf13/cobra"
)

var (
	linkType   string
	linkWeight float64
	linkMeta   string
)

var linkCmd = &cobra.Command{
	Use:   "link <source_id> <target_id>",
	Short: "Create or update an edge between two insights",
	Long:  "Create or update a typed edge between two insights. Used by Claude to create semantic edges after evaluating candidates.",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		sourceID := args[0]
		targetID := args[1]

		// Validate edge type
		edgeType := model.EdgeType(linkType)
		if !model.ValidEdgeTypes[edgeType] {
			return fmt.Errorf("invalid edge type %q; valid: %s",
				linkType, strings.Join(model.EdgeTypeNames(), ", "))
		}

		// Validate weight
		if linkWeight < 0.0 || linkWeight > 1.0 {
			return fmt.Errorf("weight must be between 0.0 and 1.0, got %.2f", linkWeight)
		}

		db, err := openDB()
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer db.Close()

		// Validate both insights exist
		src, err := db.GetInsightByID(sourceID)
		if err != nil || src == nil {
			return fmt.Errorf("source insight %s not found", sourceID)
		}
		tgt, err := db.GetInsightByID(targetID)
		if err != nil || tgt == nil {
			return fmt.Errorf("target insight %s not found", targetID)
		}

		// Parse optional metadata
		metadata := map[string]string{"created_by": "claude"}
		if linkMeta != "" {
			if err := json.Unmarshal([]byte(linkMeta), &metadata); err != nil {
				return fmt.Errorf("invalid metadata JSON: %w", err)
			}
			metadata["created_by"] = "claude"
		}

		now := time.Now().UTC()

		// Mutual relations are recorded in both directions.
		err = db.InsertEdge(&model.Edge{
			SourceID:  sourceID,
			TargetID:  targetID,
			EdgeType:  edgeType,
			Weight:    linkWeight,
			Metadata:  metadata,
			CreatedAt: now,
		})
		if err != nil {
			return fmt.Errorf("create edge %s→%s: %w", sourceID, targetID, err)
		}

		// A directed relation is recorded once. Writing the reverse of a
		// supersedes edge would mark the correction as superseded too, so
		// both insights would be demoted and the stale one would keep its
		// lead over the correction -- the exact ordering this type exists
		// to fix.
		if !edgeType.IsDirected() {
			err = db.InsertEdge(&model.Edge{
				SourceID:  targetID,
				TargetID:  sourceID,
				EdgeType:  edgeType,
				Weight:    linkWeight,
				Metadata:  metadata,
				CreatedAt: now,
			})
			if err != nil {
				return fmt.Errorf("create edge %s→%s: %w", targetID, sourceID, err)
			}
		}

		db.LogOp("link", sourceID, fmt.Sprintf("%s→%s type=%s weight=%.2f", truncID(sourceID), truncID(targetID), linkType, linkWeight))

		output := map[string]interface{}{
			"status":    "linked",
			"source_id": sourceID,
			"target_id": targetID,
			"edge_type": linkType,
			"weight":    linkWeight,
			"metadata":  metadata,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(output)
	},
}

func init() {
	linkCmd.Flags().StringVar(&linkType, "type", "semantic",
		fmt.Sprintf("edge type (%s)", strings.Join(model.EdgeTypeNames(), "|")))
	linkCmd.Flags().Float64Var(&linkWeight, "weight", 0.5, "edge weight (0.0-1.0)")
	linkCmd.Flags().StringVar(&linkMeta, "meta", "", `optional metadata JSON (e.g. '{"reason":"similar topic"}')`)
	rootCmd.AddCommand(linkCmd)
}
