package memory

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var showCmd = &cobra.Command{
	Use:   "show [id]",
	Short: "Show one full insight by ID",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		db, err := openDB()
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer db.Close()

		insight, err := db.GetInsightByID(args[0])
		if err != nil {
			return err
		}
		_ = db.IncrementAccessCount(insight.ID)
		db.LogOp("show", insight.ID, "full insight")

		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(insight)
	},
}

func init() {
	rootCmd.AddCommand(showCmd)
}
