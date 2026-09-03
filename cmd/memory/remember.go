package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mnemon-dev/mnemon/internal/memory/graph"
	"github.com/mnemon-dev/mnemon/internal/memory/model"
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
		if err := validateRememberCLIOptions(); err != nil {
			return err
		}
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

// validateRememberCLIOptions preserves the CLI's strict contract for values
// whose zero value means "omitted" to protocol callers of the shared service.
// Cobra has already supplied non-zero defaults when these flags are omitted,
// so a zero value here is an explicit invalid CLI value rather than absence.
func validateRememberCLIOptions() error {
	category := model.Category(remCategory)
	if !model.ValidCategories[category] {
		return fmt.Errorf(
			"invalid category %q; valid: preference, decision, fact, insight, context, general",
			remCategory)
	}
	if remImportance < 1 || remImportance > 5 {
		return fmt.Errorf("importance must be 1-5, got %d", remImportance)
	}
	entityMode := graph.EntityMode(remEntityMode)
	if !graph.ValidEntityMode(entityMode) {
		return fmt.Errorf(
			"invalid entity mode %q; valid: merge, provided, auto", remEntityMode)
	}
	return nil
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
