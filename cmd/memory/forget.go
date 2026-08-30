package memory

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var forgetCmd = &cobra.Command{
	Use:   "forget [id]",
	Short: "Soft-delete an insight",
	Long:  "Mark an insight as deleted (soft delete). The data is preserved but excluded from queries.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]

		db, err := openWritableDB("forget")
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer db.Close()

		if err := db.SoftDeleteInsight(id); err != nil {
			return fmt.Errorf("forget: %w", err)
		}

		db.LogOp("forget", id, "")

		output := map[string]interface{}{
			"id":      id,
			"status":  "deleted",
			"message": "Insight soft-deleted successfully",
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(output)
	},
}

func init() {
	rootCmd.AddCommand(forgetCmd)
}
