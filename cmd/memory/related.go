package memory

import (
	"encoding/json"
	"os"

	memoryservice "github.com/mnemon-dev/mnemon/internal/memory/service"
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
		results, err := newRuntimeService(os.Stderr).Related(cmd.Context(), memoryservice.RelatedRequest{
			ID: args[0], EdgeType: relEdgeType, Depth: relDepth,
		})
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(results)
	},
}

func init() {
	relatedCmd.Flags().StringVar(&relEdgeType, "edge", "", "filter by edge type (temporal|semantic|causal|entity)")
	relatedCmd.Flags().IntVar(&relDepth, "depth", 2, "max traversal depth")
	rootCmd.AddCommand(relatedCmd)
}
