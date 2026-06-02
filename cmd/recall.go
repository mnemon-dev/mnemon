package cmd

import (
	"encoding/json"
	"fmt"
	"math"
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
	recVerbose  bool
)

// compactResult is the LLM-friendly projection of a recall result.
// It drops signals, timestamps, traversal metadata, and other debug fields
// that add noise for agent consumption.
type compactResult struct {
	ID         string  `json:"id"`
	Content    string  `json:"content"`
	Category   string  `json:"category,omitempty"`
	Importance int     `json:"importance,omitempty"`
	Intent     string  `json:"intent"`
	MatchedVia string  `json:"matched_via,omitempty"`
	Confidence string  `json:"confidence"`
	Score      float64 `json:"score"`
}

// compactResponse wraps compact results with an optional hint.
type compactResponse struct {
	Results []compactResult `json:"results"`
	Hint    string          `json:"hint,omitempty"`
}

// confidenceLowMax / confidenceMediumMax bucket the recall score into
// low / medium / high labels for agent consumption. The score is the
// weighted sum of normalized signals (keyword + entity + similarity +
// graph), so it is not a calibrated probability. The current cutoffs
// are chosen empirically and may need tuning once we have a larger
// sample of real recall traces; until then the raw score is also
// exposed for callers that need finer control.
const (
	confidenceLowMax    = 0.25
	confidenceMediumMax = 0.6
)

// confidenceLabel maps a numeric score to a discrete confidence bucket.
func confidenceLabel(score float64) string {
	switch {
	case score < confidenceLowMax:
		return "low"
	case score < confidenceMediumMax:
		return "medium"
	default:
		return "high"
	}
}

// roundScore rounds a float to 3 decimal places (half-away-from-zero).
func roundScore(s float64) float64 {
	return math.Round(s*1000) / 1000
}

// toCompact projects a full RecallResponse into the compact LLM-friendly shape.
func toCompact(resp search.RecallResponse) compactResponse {
	results := make([]compactResult, 0, len(resp.Results))
	for _, r := range resp.Results {
		rounded := roundScore(r.Score)
		cr := compactResult{
			ID:         r.Insight.ID,
			Content:    r.Insight.Content,
			Category:   string(r.Insight.Category),
			Importance: r.Insight.Importance,
			Intent:     string(r.Intent),
			MatchedVia: r.Via,
			Confidence: confidenceLabel(rounded),
			Score:      rounded,
		}
		results = append(results, cr)
	}
	return compactResponse{
		Results: results,
		Hint:    resp.Meta.Hint,
	}
}

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
			// Legacy SQL LIKE recall (not affected by format flags)
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
		ec := embed.NewClientWithModel(resolveEmbedModel())
		if ec.Available() {
			queryVec, _ = ec.Embed(keyword)
		}

		// Extract query entities at cmd layer (avoid graph->search circular dep).
		// Load the known-entity set so the indexed extractor's fourth path can
		// admit user vocabulary (single-segment CamelCase, lowercase project
		// names) that techDictionary does not cover. The lookup is read-only;
		// on error we fall through to the default regex+dictionary extractor.
		knownEntities, _ := db.LoadKnownEntities()
		queryEntities := graph.ExtractEntitiesIndexed(keyword, knownEntities)

		resp, err := search.IntentAwareRecall(db, keyword, queryVec, queryEntities, recLimit, intentOverride)
		if err != nil {
			return fmt.Errorf("recall: %w", err)
		}
		for _, r := range resp.Results {
			_ = db.IncrementAccessCount(r.Insight.ID)
		}
		db.LogOp("recall", "", fmt.Sprintf("q=%s hits=%d", keyword, len(resp.Results)))

		if recVerbose {
			return enc.Encode(resp)
		}
		return enc.Encode(toCompact(resp))
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
	recallCmd.Flags().BoolVar(&recVerbose, "verbose", false, "output full recall response (signals, meta, timestamps)")
	rootCmd.AddCommand(recallCmd)
}
