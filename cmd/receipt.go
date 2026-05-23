package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/mnemon-dev/mnemon/internal/store"
	"github.com/spf13/cobra"
)

var receiptLimit int

type receiptDocument struct {
	Schema      string         `json:"schema"`
	GeneratedAt string         `json:"generated_at"`
	Store       string         `json:"store"`
	Limit       int            `json:"limit"`
	Count       int            `json:"count"`
	Privacy     receiptPrivacy `json:"privacy"`
	Events      []receiptEvent `json:"events"`
}

type receiptPrivacy struct {
	RawDetailIncluded bool   `json:"raw_detail_included"`
	HashAlgorithm     string `json:"hash_algorithm"`
	Note              string `json:"note"`
}

type receiptEvent struct {
	EventName     string `json:"event_name"`
	Operation     string `json:"operation"`
	CreatedAt     string `json:"created_at"`
	InsightIDHash string `json:"insight_id_hash,omitempty"`
	DetailHash    string `json:"detail_hash,omitempty"`
	DetailPresent bool   `json:"detail_present"`
}

var receiptCmd = &cobra.Command{
	Use:   "receipt",
	Short: "Export a privacy-safe memory operation receipt",
	Long: `Export a JSON receipt for recent memory operations without printing raw memory
contents, queries, paths, or operation details. The receipt hashes identifiers and
details so it can be shared for audits of what crossed the memory boundary.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requirePositiveLimit("--limit", receiptLimit); err != nil {
			return err
		}

		db, err := openDB()
		if err != nil {
			return fmt.Errorf("open database: %w", err)
		}
		defer db.Close()

		entries, err := db.GetOplog(receiptLimit)
		if err != nil {
			return fmt.Errorf("get oplog: %w", err)
		}

		doc := buildReceipt(resolveStoreName(), receiptLimit, entries, time.Now().UTC())
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(doc)
	},
}

func init() {
	receiptCmd.Flags().IntVar(&receiptLimit, "limit", 20, "max operations to include")
	rootCmd.AddCommand(receiptCmd)
}

func buildReceipt(storeName string, limit int, entries []store.OplogEntry, generatedAt time.Time) receiptDocument {
	events := make([]receiptEvent, 0, len(entries))
	for _, entry := range entries {
		event := receiptEvent{
			EventName:     "mnemon.memory.operation.observed",
			Operation:     entry.Operation,
			CreatedAt:     entry.CreatedAt,
			InsightIDHash: hashIfPresent(entry.InsightID),
			DetailHash:    hashIfPresent(entry.Detail),
			DetailPresent: entry.Detail != "",
		}
		events = append(events, event)
	}

	return receiptDocument{
		Schema:      "mnemon.memory.receipt.v1",
		GeneratedAt: generatedAt.Format(time.RFC3339),
		Store:       storeName,
		Limit:       limit,
		Count:       len(events),
		Privacy: receiptPrivacy{
			RawDetailIncluded: false,
			HashAlgorithm:     "sha256",
			Note:              "Raw memory contents, recall queries, paths, and operation details are omitted; only hashes and operation metadata are emitted.",
		},
		Events: events,
	}
}

func hashIfPresent(value string) string {
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
