package memory

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mnemon-dev/mnemon/internal/aml"
	"github.com/spf13/cobra"
)

// newAMLServeCommand is `mnemon memory-agent aml-serve`: the Agent Memory
// Leaderboard Add/Search HTTP contract over one records store per user.
func newAMLServeCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "aml-serve",
		Short: "Serve the Agent Memory Leaderboard Add/Search HTTP API over per-user stores",
		Args:  cobra.NoArgs,
		RunE:  runAMLServe,
	}
	flags := command.Flags()
	flags.String("listen", "127.0.0.1:8090", "TCP address to listen on")
	flags.String("data-root", "", "Absolute directory holding one records store per user (required)")
	flags.String("memory-config", "", "Absolute path of the memory-strategy JSON configuration (required)")
	flags.String("auth-key-env", "AML_API_KEY", "Environment variable holding the API key clients must send")
	flags.Bool("consolidate-on-add", false, "Consolidate during requests: full batches before an Add returns, the rest before the user's next Search")
	flags.Int("max-open-stores", 64, "Most user stores kept open at once")
	flags.Duration("search-timeout", 10*time.Minute, "Longest memory read of a Search before it answers the raw ranking (1m to 18m)")
	return command
}

func runAMLServe(cmd *cobra.Command, _ []string) error {
	if readOnly {
		return errors.New("aml-serve writes user stores and cannot run with --readonly")
	}
	flags := cmd.Flags()
	listen, _ := flags.GetString("listen")
	root, _ := flags.GetString("data-root")
	configPath, _ := flags.GetString("memory-config")
	keyEnv, _ := flags.GetString("auth-key-env")
	consolidate, _ := flags.GetBool("consolidate-on-add")
	maxOpen, _ := flags.GetInt("max-open-stores")
	searchTimeout, _ := flags.GetDuration("search-timeout")
	if !filepath.IsAbs(root) {
		return errors.New("aml-serve requires an absolute --data-root")
	}
	if maxOpen < 1 {
		return errors.New("--max-open-stores must be at least 1")
	}
	if searchTimeout < time.Minute || searchTimeout > 18*time.Minute {
		// A Search may first flush for up to 10 minutes; both must end within
		// the platform's 30-minute request timeout.
		return errors.New("--search-timeout must be between 1m and 18m")
	}
	if keyEnv == "" {
		return errors.New("--auth-key-env must name an environment variable")
	}
	key := os.Getenv(keyEnv)
	if key == "" {
		return fmt.Errorf("aml-serve requires a nonempty API key in $%s", keyEnv)
	}
	config, err := readMemoryConfig(configPath)
	if err != nil {
		return err
	}
	server, err := aml.NewServer(aml.ServerConfig{DataRoot: root, MemoryConfig: config, AuthKey: key, ConsolidateOnAdd: consolidate, MaxOpenStores: maxOpen, SearchTimeout: searchTimeout})
	if err != nil {
		return err
	}
	defer server.Close()
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	httpServer := server.HTTPServer(listen)
	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- httpServer.Serve(listener) }()
	// Every 10 minutes: Adds, Searches, degraded Searches and the budget.
	go server.LogStatus(ctx, 10*time.Minute)
	fmt.Fprintf(cmd.ErrOrStderr(), "aml-serve listening on %s\n", listener.Addr())
	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}
	// Requests in flight finish first; held stores close as they end.
	shutdown, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	if err := httpServer.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(cmd.ErrOrStderr(), "aml-serve: requests still running at shutdown: %v\n", err)
	}
	return nil
}
