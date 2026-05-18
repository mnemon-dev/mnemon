package cmd

import (
	"fmt"
	"os"

	"github.com/mnemon-dev/mnemon/internal/store"
	"github.com/spf13/cobra"
)

var storeCmd = &cobra.Command{
	Use:   "store",
	Short: "Manage memory stores",
	Long:  "Create, list, switch, and remove isolated memory stores.",
}

// ── list ─────────────────────────────────────────────────────────────

var storeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all memory stores",
	RunE: func(cmd *cobra.Command, args []string) error {
		names, err := store.ListStores(dataDir)
		if err != nil {
			return err
		}
		if len(names) == 0 {
			fmt.Println("  (no stores yet — run 'mnemon store create <name>' or any command to create default)")
			return nil
		}
		active := resolveStoreName()
		for _, n := range names {
			marker := "  "
			if n == active {
				marker = "* "
			}
			fmt.Printf("%s%s\n", marker, n)
		}
		return nil
	},
}

// ── create ───────────────────────────────────────────────────────────

var storeCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a new memory store",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if !store.ValidStoreName(name) {
			return fmt.Errorf("invalid store name %q: must match [a-zA-Z0-9][a-zA-Z0-9_-]*", name)
		}
		if store.StoreExists(dataDir, name) {
			return fmt.Errorf("store %q already exists", name)
		}
		dir := store.StoreDir(dataDir, name)
		db, err := store.Open(dir)
		if err != nil {
			return fmt.Errorf("create store: %w", err)
		}
		db.Close()
		fmt.Printf("Created store %q\n", name)
		return nil
	},
}

// ── set ──────────────────────────────────────────────────────────────

var storeSetCmd = &cobra.Command{
	Use:   "set <name>",
	Short: "Set the default active store",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if !store.ValidStoreName(name) {
			return fmt.Errorf("invalid store name %q: must match [a-zA-Z0-9][a-zA-Z0-9_-]*", name)
		}
		if !store.StoreExists(dataDir, name) {
			return fmt.Errorf("store %q does not exist (use 'mnemon store create %s' first)", name, name)
		}
		if err := store.WriteActive(dataDir, name); err != nil {
			return fmt.Errorf("write active file: %w", err)
		}
		fmt.Printf("Active store set to %q\n", name)
		return nil
	},
}

// ── remove ───────────────────────────────────────────────────────────

var storeRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a memory store and all its data",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if !store.ValidStoreName(name) {
			return fmt.Errorf("invalid store name %q: must match [a-zA-Z0-9][a-zA-Z0-9_-]*", name)
		}
		if !store.StoreExists(dataDir, name) {
			return fmt.Errorf("store %q does not exist", name)
		}
		active := resolveStoreName()
		if name == active {
			return fmt.Errorf("cannot remove the active store %q (switch first with 'mnemon store set <other>')", name)
		}
		dir := store.StoreDir(dataDir, name)
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("remove store: %w", err)
		}
		fmt.Printf("Removed store %q\n", name)
		return nil
	},
}

func init() {
	storeCmd.AddCommand(storeListCmd, storeCreateCmd, storeSetCmd, storeRemoveCmd)
	rootCmd.AddCommand(storeCmd)
}
