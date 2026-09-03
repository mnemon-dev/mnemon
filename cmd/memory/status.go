package memory

import (
	"encoding/json"
	"os"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show memory statistics",
	Long:  "Display aggregate statistics about stored insights and graph edges.",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := newRuntimeService(os.Stderr).Status(cmd.Context())
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
