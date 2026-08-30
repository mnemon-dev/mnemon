package memory

import (
	"fmt"
	"os"

	"github.com/mnemon-dev/mnemon/internal/memory/embed"
	"github.com/mnemon-dev/mnemon/internal/memory/store"
	"github.com/spf13/cobra"
)

var version = "dev"

var (
	dataDir    string
	storeName  string
	readOnly   bool
	embedModel string
)

var rootCmd = &cobra.Command{
	Use:   "mnemon",
	Short: "Memory daemon for LLM agents",
	Long:  "Mnemon is a standalone memory daemon based on MAGMA's four-graph architecture.",
}

// New returns Mnemon's Memory command tree for composition by the product
// root. Command execution and process exit remain the root command's concern.
func New(buildVersion string) *cobra.Command {
	version = buildVersion
	rootCmd.Version = buildVersion
	return rootCmd
}

func init() {
	defaultDataDir := store.DefaultDataDir()
	if env := os.Getenv("MNEMON_DATA_DIR"); env != "" {
		defaultDataDir = env
	}
	rootCmd.PersistentFlags().StringVar(&dataDir, "data-dir", defaultDataDir, "base data directory (env: MNEMON_DATA_DIR)")
	rootCmd.PersistentFlags().StringVar(&storeName, "store", "", "named memory store (overrides MNEMON_STORE and active file)")
	rootCmd.PersistentFlags().BoolVar(&readOnly, "readonly", false, "open an immutable database snapshot (reject writes; create no WAL files)")
	rootCmd.PersistentFlags().StringVar(&embedModel, "embed-model", "",
		fmt.Sprintf("embedding model (env: MNEMON_EMBED_MODEL; default: %s)", embed.DefaultModel))
}

// resolveEmbedModel returns the embedding model selector that should be
// passed to embed.NewClientWithModel.
//
// Resolution chain (delegated to NewClientWithModel):
//
//	non-empty --embed-model flag > MNEMON_EMBED_MODEL env var > embed.DefaultModel
//
// An explicitly empty --embed-model is treated as "unset" and falls through
// to the env var / built-in default; this matches how the existing --data-dir
// flag behaves and avoids surprises when a user clears the flag via shell
// scripting. Env-var resolution happens inside NewClientWithModel at command
// execution time (not at cmd/init time), so test setups using t.Setenv after
// package init still work as expected.
func resolveEmbedModel() string {
	return embedModel
}

// resolveStoreName returns the effective store name.
// Priority: --store flag > MNEMON_STORE env > active file > "default".
func resolveStoreName() string {
	if storeName != "" {
		return storeName
	}
	if env := os.Getenv("MNEMON_STORE"); env != "" {
		return env
	}
	return store.ReadActive(dataDir)
}

// truncID safely truncates an ID to 8 characters for display.
func truncID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// openDB is a helper used by subcommands.
func openDB() (*store.DB, error) {
	name := resolveStoreName()
	if !store.ValidStoreName(name) {
		return nil, fmt.Errorf("invalid store name %q", name)
	}
	dir := store.StoreDir(dataDir, name)

	if readOnly {
		return store.OpenReadOnly(dir)
	}

	if err := store.MigrateIfNeeded(dataDir); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return store.Open(dir)
}

// openWritableDB opens the selected store and rejects mutating command paths
// when --readonly is active. The store layer still enforces SQLite mode=ro as
// a defense in depth; this guard gives callers a stable, actionable CLI error
// before embeddings or any other command work begins.
func openWritableDB(action string) (*store.DB, error) {
	db, err := openDB()
	if err != nil {
		return nil, err
	}
	if err := requireWritableDB(db, action); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func requireWritableDB(db *store.DB, action string) error {
	if db.IsReadOnly() {
		return readOnlyWriteError(action)
	}
	return nil
}

func requireWritableMode(action string) error {
	if readOnly {
		return readOnlyWriteError(action)
	}
	return nil
}

func readOnlyWriteError(action string) error {
	return fmt.Errorf("%s is unavailable with --readonly: database writes are disabled", action)
}
