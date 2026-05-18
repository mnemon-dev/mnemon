package cmd

import (
	"fmt"
	"os"

	"github.com/mnemon-dev/mnemon/internal/store"
	"github.com/spf13/cobra"
)

// version is set at build time via ldflags.
var version = "dev"

var (
	dataDir   string
	storeName string
	readOnly  bool
)

var rootCmd = &cobra.Command{
	Use:     "mnemon",
	Version: version,
	Short:   "Memory daemon for LLM agents",
	Long:    "Mnemon is a standalone memory daemon based on MAGMA's four-graph architecture.",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	defaultDataDir := store.DefaultDataDir()
	if env := os.Getenv("MNEMON_DATA_DIR"); env != "" {
		defaultDataDir = env
	}
	rootCmd.PersistentFlags().StringVar(&dataDir, "data-dir", defaultDataDir, "base data directory (env: MNEMON_DATA_DIR)")
	rootCmd.PersistentFlags().StringVar(&storeName, "store", "", "named memory store (overrides MNEMON_STORE and active file)")
	rootCmd.PersistentFlags().BoolVar(&readOnly, "readonly", false, "open database in read-only mode (no WAL files, safe for read-only mounts)")
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
