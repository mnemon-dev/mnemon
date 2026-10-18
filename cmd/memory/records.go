package memory

import (
	"encoding/json"
	"fmt"
	"github.com/mnemon-dev/mnemon/internal/memory/store"
	"github.com/mnemon-dev/mnemon/internal/records"
	"github.com/spf13/cobra"
	"io"
)

func init() {
	group := &cobra.Command{Use: "records", Short: "Store and retrieve raw records in an independent records.db"}
	for _, operation := range []string{"get", "put", "query", "embed"} {
		operation := operation
		command := &cobra.Command{Use: operation, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("data-dir") && !cmd.InheritedFlags().Changed("data-dir") {
				return fmt.Errorf("records requires an explicit --data-dir")
			}
			name := storeName
			if name == "" {
				name = "default"
			}
			if !store.ValidStoreName(name) {
				return fmt.Errorf("invalid store name %q", name)
			}
			if readOnly && operation == "put" {
				return readOnlyWriteError("records put")
			}
			db, err := records.Open(store.StoreDir(dataDir, name), readOnly)
			if err != nil {
				return err
			}
			defer db.Close()
			decoder := json.NewDecoder(io.LimitReader(cmd.InOrStdin(), 2<<30))
			var result any
			if operation == "get" {
				var req records.GetRequest
				if err = decoder.Decode(&req); err == io.EOF {
					err = nil
				}
				if err == nil {
					result, err = db.Get(cmd.Context(), req)
				}
			} else if operation == "query" {
				var req records.QueryRequest
				if err = decoder.Decode(&req); err == nil {
					result, err = db.Query(cmd.Context(), req)
				}
			} else if operation == "embed" {
				var req records.EmbedRequest
				if err = decoder.Decode(&req); err == nil {
					status, _ := cmd.Flags().GetBool("status")
					result, err = db.Embed(cmd.Context(), req, status)
				}
			} else {
				var req records.PutRequest
				if err = decoder.Decode(&req); err == nil {
					var rev string
					rev, err = db.Put(cmd.Context(), req)
					result = map[string]string{"revision": rev}
				}
			}
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
		}}
		if operation == "embed" {
			command.Flags().Bool("status", false, "Report vector coverage without embedding")
		}
		group.AddCommand(command)
	}
	rootCmd.AddCommand(group)
}
