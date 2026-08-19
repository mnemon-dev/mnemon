package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mnemon-dev/mnemon/internal/memory/graph"
	"github.com/mnemon-dev/mnemon/internal/memory/model"
	"github.com/mnemon-dev/mnemon/internal/memory/store"
	"github.com/spf13/cobra"
)

var (
	relEdgeType string
	relDepth    int
)

var relatedCmd = &cobra.Command{
	Use:   "related [id]",
	Short: "Find related insights via graph traversal",
	Long:  "BFS traversal from a given insight, optionally filtered by edge type.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		startID := args[0]

		db, err := openDB()
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer db.Close()

		// Verify start node exists
		start, err := db.GetInsightByID(startID)
		if err != nil {
			return fmt.Errorf("insight not found: %w", err)
		}

		var edgeFilter model.EdgeType
		if relEdgeType != "" {
			et := model.EdgeType(relEdgeType)
			if !model.ValidEdgeTypes[et] {
				return fmt.Errorf("invalid edge type %q; valid: %s",
					relEdgeType, strings.Join(model.EdgeTypeNames(), ", "))
			}
			edgeFilter = et
		}

		// BFS traversal
		related := bfsTraverse(db, start.ID, edgeFilter, relDepth)

		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(related)
	},
}

type relatedResult struct {
	ID         string `json:"id"`
	Content    string `json:"content"`
	Category   string `json:"category"`
	Importance int    `json:"importance"`
	Depth      int    `json:"depth"`
	EdgeType   string `json:"via_edge_type,omitempty"`
}

func bfsTraverse(db *store.DB, startID string, edgeFilter model.EdgeType, maxDepth int) []relatedResult {
	nodes := graph.BFS(db, startID, graph.BFSOptions{
		MaxDepth:   maxDepth,
		EdgeFilter: edgeFilter,
	})
	results := make([]relatedResult, 0, len(nodes))
	for _, n := range nodes {
		results = append(results, relatedResult{
			ID:         n.Insight.ID,
			Content:    n.Insight.Content,
			Category:   string(n.Insight.Category),
			Importance: n.Insight.Importance,
			Depth:      n.Hop,
			EdgeType:   string(n.ViaEdge.EdgeType),
		})
	}
	return results
}

func init() {
	relatedCmd.Flags().StringVar(&relEdgeType, "edge", "",
		fmt.Sprintf("filter by edge type (%s)", strings.Join(model.EdgeTypeNames(), "|")))
	relatedCmd.Flags().IntVar(&relDepth, "depth", 2, "max traversal depth")
	rootCmd.AddCommand(relatedCmd)
}
