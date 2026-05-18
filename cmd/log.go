package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var logLimit int

var logCmd = &cobra.Command{
	Use:   "log",
	Short: "Show recent operations",
	Long:  "Display the operation log showing what mnemon has been doing (remember, recall, forget, etc).",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requirePositiveLimit("--limit", logLimit); err != nil {
			return err
		}

		db, err := openDB()
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer db.Close()

		entries, err := db.GetOplog(logLimit)
		if err != nil {
			return fmt.Errorf("get oplog: %w", err)
		}

		if len(entries) == 0 {
			fmt.Println("No operations recorded yet.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintf(w, "TIME\tOP\tINSIGHT\tDETAIL\n")
		fmt.Fprintf(w, "----\t--\t-------\t------\n")
		for _, e := range entries {
			insightID := e.InsightID
			if len(insightID) > 8 {
				insightID = insightID[:8]
			}
			detail := e.Detail
			if len(detail) > 60 {
				detail = detail[:57] + "..."
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", e.CreatedAt, e.Operation, insightID, detail)
		}
		w.Flush()
		return nil
	},
}

func init() {
	logCmd.Flags().IntVar(&logLimit, "limit", 20, "max entries to show")
	rootCmd.AddCommand(logCmd)
}
