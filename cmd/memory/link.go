package memory

import (
	"encoding/json"
	"fmt"
	"os"

	memoryservice "github.com/mnemon-dev/mnemon/internal/memory/service"
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
		metadata := map[string]string{}
		if linkMeta != "" {
			if err := json.Unmarshal([]byte(linkMeta), &metadata); err != nil {
				return fmt.Errorf("invalid metadata JSON: %w", err)
			}
		}
		result, err := newRuntimeService(os.Stderr).Link(cmd.Context(), memoryservice.LinkRequest{
			SourceID: args[0], TargetID: args[1], EdgeType: linkType,
			Weight: linkWeight, Metadata: metadata, CreatedBy: "claude",
		})
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	},
}

func init() {
	linkCmd.Flags().StringVar(&linkType, "type", "semantic", "edge type (temporal|semantic|causal|entity)")
	linkCmd.Flags().Float64Var(&linkWeight, "weight", 0.5, "edge weight (0.0-1.0)")
	linkCmd.Flags().StringVar(&linkMeta, "meta", "", `optional metadata JSON (e.g. '{"reason":"similar topic"}')`)
	rootCmd.AddCommand(linkCmd)
}
