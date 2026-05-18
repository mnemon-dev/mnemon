package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mnemon-dev/mnemon/internal/embed"
	"github.com/mnemon-dev/mnemon/internal/graph"
	"github.com/mnemon-dev/mnemon/internal/search"
	"github.com/mnemon-dev/mnemon/internal/store"
	"github.com/spf13/cobra"
)

var (
	recCategory string
	recLimit    int
	recSource   string
	recBasic    bool
	recSmart    bool //nolint:unused // deprecated: smart is now the default; kept for backward compat
	recIntent   string
)

var recallCmd = &cobra.Command{
	Use:   "recall [keyword]",
	Short: "Retrieve insights by keyword",
	Long:  "Search for insights using intent-aware graph-enhanced retrieval. Use --basic for simple SQL LIKE matching.",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		keyword := strings.Join(args, " ")
		if err := requirePositiveLimit("--limit", recLimit); err != nil {
			return err
		}

		db, err := openDB()
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer db.Close()

		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")

		if recBasic {
			// Legacy SQL LIKE recall
			results, err := db.QueryInsights(store.QueryFilter{
				Keyword:  keyword,
				Category: recCategory,
				Source:   recSource,
				Limit:    recLimit,
			})
			if err != nil {
				return fmt.Errorf("query insights: %w", err)
			}

			for _, r := range results {
				_ = db.IncrementAccessCount(r.ID)
			}
			db.LogOp("recall:basic", "", fmt.Sprintf("q=%s hits=%d", keyword, len(results)))
			return enc.Encode(results)
		}

		// Default: intent-aware graph-enhanced recall
		var intentOverride *search.Intent
		if recIntent != "" {
			parsed, err := search.IntentFromString(recIntent)
			if err != nil {
				return err
			}
			intentOverride = &parsed
		}

		// Try to get query embedding for hybrid search
		var queryVec []float64
		ec := embed.NewClient()
		if ec.Available() {
			queryVec, _ = ec.Embed(keyword)
		}

		// Extract query entities at cmd layer (avoid graph→search circular dep)
		queryEntities := graph.ExtractEntities(keyword)

		resp, err := search.IntentAwareRecall(db, keyword, queryVec, queryEntities, recLimit, intentOverride)
		if err != nil {
			return fmt.Errorf("recall: %w", err)
		}
		for _, r := range resp.Results {
			_ = db.IncrementAccessCount(r.Insight.ID)
		}
		db.LogOp("recall", "", fmt.Sprintf("q=%s hits=%d", keyword, len(resp.Results)))
		return enc.Encode(resp)
	},
}

func init() {
	recallCmd.Flags().StringVar(&recCategory, "cat", "", "filter by category")
	recallCmd.Flags().IntVar(&recLimit, "limit", 10, "max results")
	recallCmd.Flags().StringVar(&recSource, "source", "", "filter by source")
	recallCmd.Flags().BoolVar(&recBasic, "basic", false, "use simple SQL LIKE matching instead of smart recall")
	recallCmd.Flags().BoolVar(&recSmart, "smart", false, "deprecated: smart is now the default")
	_ = recallCmd.Flags().MarkHidden("smart")
	recallCmd.Flags().StringVar(&recIntent, "intent", "", "override intent (WHY|WHEN|ENTITY|GENERAL)")
	rootCmd.AddCommand(recallCmd)
}
