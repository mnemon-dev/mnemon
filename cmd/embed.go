package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/mnemon-dev/mnemon/internal/embed"
	"github.com/spf13/cobra"
)

var (
	embedAll    bool
	embedStatus bool
)

var embedCmd = &cobra.Command{
	Use:   "embed [id]",
	Short: "Generate embeddings for insights via Ollama",
	Long: `Generate embedding vectors for insights using a local Ollama model.

Modes:
  mnemon embed --status            Show embedding coverage statistics
  mnemon embed --all               Backfill embeddings for all un-embedded insights
  mnemon embed <id>                Generate embedding for a specific insight`,
	RunE: func(cmd *cobra.Command, args []string) error {
		db, err := openDB()
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer db.Close()

		ec := embed.NewClientWithModel(resolveEmbedModel())
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")

		// Status mode
		if embedStatus {
			total, embedded, err := db.EmbeddingStats()
			if err != nil {
				return fmt.Errorf("embedding stats: %w", err)
			}
			output := map[string]interface{}{
				"total_insights":   total,
				"embedded":         embedded,
				"coverage":         fmt.Sprintf("%.0f%%", float64(embedded)/float64(max(total, 1))*100),
				"ollama_available": ec.Available(),
				"model":            ec.Model(),
			}
			return enc.Encode(output)
		}

		// Check Ollama availability
		if !ec.Available() {
			return fmt.Errorf("Ollama not available at %s — install with: brew install ollama && ollama pull %s", ec.Endpoint(), ec.Model())
		}

		// Single insight mode
		if len(args) > 0 {
			id := args[0]
			ins, err := db.GetInsightByID(id)
			if err != nil || ins == nil {
				return fmt.Errorf("insight %s not found", id)
			}

			vec, err := ec.Embed(ins.Content)
			if err != nil {
				return fmt.Errorf("embed: %w", err)
			}
			blob := embed.SerializeVector(vec)
			if err := db.UpdateEmbedding(id, blob); err != nil {
				return fmt.Errorf("store embedding: %w", err)
			}

			db.LogOp("embed", id, fmt.Sprintf("dim=%d model=%s", len(vec), ec.Model()))
			output := map[string]interface{}{
				"status":    "embedded",
				"id":        id,
				"dimension": len(vec),
				"model":     ec.Model(),
			}
			return enc.Encode(output)
		}

		// Backfill mode (--all)
		if !embedAll {
			return fmt.Errorf("specify --all to backfill, --status to check coverage, or provide an insight ID")
		}

		missing, err := db.GetInsightsWithoutEmbedding(0)
		if err != nil {
			return fmt.Errorf("query insights: %w", err)
		}

		if len(missing) == 0 {
			output := map[string]interface{}{
				"status":  "complete",
				"message": "all insights already have embeddings",
			}
			return enc.Encode(output)
		}

		succeeded := 0
		failed := 0
		for _, ins := range missing {
			vec, err := ec.Embed(ins.Content)
			if err != nil {
				failed++
				continue
			}
			blob := embed.SerializeVector(vec)
			if err := db.UpdateEmbedding(ins.ID, blob); err != nil {
				failed++
				continue
			}
			succeeded++
		}

		db.LogOp("embed:backfill", "", fmt.Sprintf("succeeded=%d failed=%d model=%s", succeeded, failed, ec.Model()))
		output := map[string]interface{}{
			"status":    "backfill_complete",
			"succeeded": succeeded,
			"failed":    failed,
			"model":     ec.Model(),
		}
		return enc.Encode(output)
	},
}

func init() {
	embedCmd.Flags().BoolVar(&embedAll, "all", false, "backfill embeddings for all un-embedded insights")
	embedCmd.Flags().BoolVar(&embedStatus, "status", false, "show embedding coverage statistics")
	rootCmd.AddCommand(embedCmd)
}
