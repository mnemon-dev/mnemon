package memory

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/mnemon-dev/mnemon/internal/memory/graph"
	memoryservice "github.com/mnemon-dev/mnemon/internal/memory/service"
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
		result, err := newRuntimeService(os.Stderr).Remember(cmd.Context(), memoryservice.RememberRequest{
			Content: strings.Join(args, " "), Category: remCategory,
			Importance: remImportance, Tags: splitMemoryList(remTags), Source: remSource,
			Entities: splitMemoryList(remEntities), EntityMode: remEntityMode, NoDiff: remNoDiff,
		})
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	},
}

func splitMemoryList(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, ",")
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
