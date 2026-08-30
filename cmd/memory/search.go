package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mnemon-dev/mnemon/internal/memory/search"
	"github.com/spf13/cobra"
)

var (
	searchLimit   int
	searchBrief   bool
	searchExcerpt int
)

var searchCmd = &cobra.Command{
	Use:   "search [query]",
	Short: "Search insights with token-based scoring",
	Long:  "Search insights using tokenized keyword matching. Returns results ranked by relevance score.",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := strings.Join(args, " ")
		if err := requirePositiveLimit("--limit", searchLimit); err != nil {
			return err
		}
		if err := validateBriefExcerptChars(searchBrief, searchExcerpt); err != nil {
			return err
		}

		db, err := openDB()
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer db.Close()

		all, err := db.GetAllActiveInsights()
		if err != nil {
			return fmt.Errorf("get insights: %w", err)
		}

		results := search.KeywordSearch(all, query, searchLimit)

		// Increment access count for returned results
		for _, r := range results {
			_ = db.IncrementAccessCount(r.Insight.ID)
		}

		db.LogOp("search", "", fmt.Sprintf("q=%s hits=%d", query, len(results)))
		if searchBrief {
			brief := make([]briefResult, 0, len(results))
			for _, result := range results {
				score := roundScore(result.Score)
				brief = append(brief, briefResult{
					ID:       result.Insight.ID,
					Excerpt:  makeBriefExcerpt(result.Insight.Content, searchExcerpt),
					Category: string(result.Insight.Category),
					Score:    scorePointer(score),
				})
			}
			return encodeBrief(os.Stdout, newBriefResponse(brief, ""))
		}

		type outputItem struct {
			ID         string   `json:"id"`
			Content    string   `json:"content"`
			Category   string   `json:"category"`
			Importance int      `json:"importance"`
			Tags       []string `json:"tags"`
			Score      float64  `json:"score"`
		}
		output := make([]outputItem, 0)
		for _, r := range results {
			output = append(output, outputItem{
				ID:         r.Insight.ID,
				Content:    r.Insight.Content,
				Category:   string(r.Insight.Category),
				Importance: r.Insight.Importance,
				Tags:       r.Insight.Tags,
				Score:      r.Score,
			})
		}

		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(output)
	},
}

func init() {
	searchCmd.Flags().IntVar(&searchLimit, "limit", 10, "max results")
	searchCmd.Flags().BoolVar(&searchBrief, "brief", false, "output short excerpts for discovery; use 'mnemon show <id>' for full content")
	searchCmd.Flags().IntVar(&searchExcerpt, "excerpt-chars", defaultBriefExcerptChars, "maximum characters per --brief excerpt")
	rootCmd.AddCommand(searchCmd)
}
