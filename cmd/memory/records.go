package memory

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/mnemon-dev/mnemon/internal/memory/store"
	"github.com/mnemon-dev/mnemon/internal/records"
	"github.com/spf13/cobra"
)

func init() {
	group := &cobra.Command{Use: "records", Short: "Store and retrieve raw records in an independent records.db"}
	for _, operation := range []string{"get", "put", "query", "embed"} {
		operation := operation
		command := &cobra.Command{Use: operation, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
			if readOnly && operation == "put" {
				return readOnlyWriteError("records put")
			}
			db, err := openRecordsStore(cmd)
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
		if operation == "query" {
			command.Short = "Rank raw records; feedback expansion is optional"
			command.Long = "Read a JSON query from stdin. input.expand defaults to false; set it to true on the first words/hybrid page to return feedback terms for a later search. Ranked IDs are unchanged by this switch."
		}
		if operation == "embed" {
			command.Flags().Bool("status", false, "Report vector coverage without embedding")
		}
		group.AddCommand(command)
	}
	serve := &cobra.Command{Use: "serve", Args: cobra.NoArgs, Short: "Serve records NDJSON on a local Unix socket", RunE: func(cmd *cobra.Command, args []string) error {
		db, err := openRecordsStore(cmd)
		if err != nil {
			return err
		}
		defer db.Close()
		path, _ := cmd.Flags().GetString("socket")
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return db.Serve(ctx, path)
	}}
	serve.Flags().String("socket", "", "Unix socket path (required)")
	group.AddCommand(serve)
	rootCmd.AddCommand(group)
}

func openRecordsStore(cmd *cobra.Command) (*records.Store, error) {
	if !cmd.Flags().Changed("data-dir") && !cmd.InheritedFlags().Changed("data-dir") {
		return nil, fmt.Errorf("records requires an explicit --data-dir")
	}
	name := storeName
	if name == "" {
		name = "default"
	}
	if !store.ValidStoreName(name) {
		return nil, fmt.Errorf("invalid store name %q", name)
	}
	return records.Open(store.StoreDir(dataDir, name), readOnly)
}
