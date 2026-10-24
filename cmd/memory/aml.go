package memory

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/coreos/go-systemd/v22/daemon"
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
	flags.String("data-root", "", "Absolute directory holding one records store per user (required); one aml-serve at a time locks it")
	flags.String("memory-config", "", "Absolute path of the memory-strategy JSON configuration (required)")
	flags.String("auth-key-env", "AML_API_KEY", "Environment variable holding the API key clients must send")
	flags.String("consolidate", "off", "When to consolidate: off; on-add (during requests: full batches before an Add returns, the rest before the user's next Search); or background (in supervised workers, consolidation mode timelines or layered: an Add only writes, and a Search first folds the rest)")
	flags.Bool("consolidate-on-add", false, "Older spelling of --consolidate on-add")
	flags.Int("consolidation-workers", 4, "Users --consolidate background folds at once")
	flags.String("admin-listen", "", "Loopback IP address and port of the admin listener (/metrics, /ready, /admin/status, /debug/pprof/), which has no authentication; empty for none")
	flags.Int("max-concurrent-adds", 0, "Most Adds served at once, 0 for no limit; past it an Add waits up to 30s for a slot, then answers 503 with Retry-After")
	flags.Int("max-concurrent-searches", 0, "Most Searches served at once, 0 for no limit; past it a Search waits up to 30s for a slot, then answers 503 with Retry-After")
	flags.String("log-format", "text", "Log format: text or json")
	flags.Int("max-open-stores", 64, "Most user stores kept open at once")
	flags.Duration("search-timeout", 10*time.Minute, "Longest memory read of a Search before it answers the raw ranking (1m to 18m)")
	return command
}

// amlServeOptions are aml-serve's checked flags.
type amlServeOptions struct {
	listen, adminListen, dataRoot, memoryConfig, keyEnv, logFormat string
	consolidation                                                  aml.ConsolidationMode
	workers, maxOpenStores, maxAdds, maxSearches                   int
	searchTimeout                                                  time.Duration
}

// amlServeFlags reads and checks aml-serve's flags; it touches nothing.
func amlServeFlags(cmd *cobra.Command) (amlServeOptions, error) {
	flags := cmd.Flags()
	var o amlServeOptions
	o.listen, _ = flags.GetString("listen")
	o.adminListen, _ = flags.GetString("admin-listen")
	o.dataRoot, _ = flags.GetString("data-root")
	o.memoryConfig, _ = flags.GetString("memory-config")
	o.keyEnv, _ = flags.GetString("auth-key-env")
	o.logFormat, _ = flags.GetString("log-format")
	o.workers, _ = flags.GetInt("consolidation-workers")
	o.maxOpenStores, _ = flags.GetInt("max-open-stores")
	o.maxAdds, _ = flags.GetInt("max-concurrent-adds")
	o.maxSearches, _ = flags.GetInt("max-concurrent-searches")
	o.searchTimeout, _ = flags.GetDuration("search-timeout")
	var err error
	if o.consolidation, err = amlConsolidation(cmd); err != nil {
		return o, err
	}
	switch {
	case !filepath.IsAbs(o.dataRoot):
		return o, errors.New("aml-serve requires an absolute --data-root")
	case o.maxOpenStores < 1:
		return o, errors.New("--max-open-stores must be at least 1")
	case o.searchTimeout < time.Minute || o.searchTimeout > 18*time.Minute:
		// A Search may first flush for up to 10 minutes; both must end within
		// the platform's 30-minute request timeout.
		return o, errors.New("--search-timeout must be between 1m and 18m")
	case o.keyEnv == "":
		return o, errors.New("--auth-key-env must name an environment variable")
	case o.workers < 1:
		return o, errors.New("--consolidation-workers must be at least 1")
	case o.maxAdds < 0:
		return o, errors.New("--max-concurrent-adds must not be negative")
	case o.maxSearches < 0:
		return o, errors.New("--max-concurrent-searches must not be negative")
	case o.logFormat != "text" && o.logFormat != "json":
		return o, fmt.Errorf("--log-format must be text or json, not %q", o.logFormat)
	}
	if o.adminListen != "" && !loopbackAddress(o.adminListen) {
		// Anyone who reaches the admin listener can profile the process and
		// read its state.
		return o, fmt.Errorf("--admin-listen must be a loopback IP address and port, such as 127.0.0.1:8091, as the admin endpoints have no authentication; not %q", o.adminListen)
	}
	return o, nil
}

// amlConsolidation is the consolidation mode --consolidate names, or its
// older spelling --consolidate-on-add; giving both is an error.
func amlConsolidation(cmd *cobra.Command) (aml.ConsolidationMode, error) {
	flags := cmd.Flags()
	if flags.Changed("consolidate-on-add") {
		if flags.Changed("consolidate") {
			return "", errors.New("--consolidate-on-add is the older spelling of --consolidate on-add: give one of them")
		}
		if onAdd, _ := flags.GetBool("consolidate-on-add"); onAdd {
			return aml.ConsolidationOnAdd, nil
		}
		return aml.ConsolidationOff, nil
	}
	switch value, _ := flags.GetString("consolidate"); value {
	case "off":
		return aml.ConsolidationOff, nil
	case "on-add":
		return aml.ConsolidationOnAdd, nil
	case "background":
		return aml.ConsolidationBackground, nil
	default:
		return "", fmt.Errorf("--consolidate must be off, on-add or background, not %q", value)
	}
}

// loopbackAddress reports whether addr is a loopback IP address and a port,
// such as 127.0.0.1:8091 or [::1]:8091. A host name is refused: what it
// resolves to is not the command's to check.
func loopbackAddress(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// amlLogger is the logger of --log-format, to w.
func amlLogger(format string, w io.Writer) *slog.Logger {
	if format == "json" {
		return slog.New(slog.NewJSONHandler(w, nil))
	}
	return slog.New(slog.NewTextHandler(w, nil))
}

// runAMLServe checks the flags, the API key, the memory configuration and
// systemd's watchdog setting, takes the data root's lock (before anything
// opens a file in the data root or the budget file), builds the server, binds
// the listeners and runs the process (aml_process.go) until SIGINT or
// SIGTERM. Its error, the exit status 1, also reports a shutdown that had to
// cut requests, consolidation jobs or model requests.
func runAMLServe(cmd *cobra.Command, _ []string) error {
	if readOnly {
		return errors.New("aml-serve writes user stores and cannot run with --readonly")
	}
	options, err := amlServeFlags(cmd)
	if err != nil {
		return err
	}
	key := os.Getenv(options.keyEnv)
	if key == "" {
		return fmt.Errorf("aml-serve requires a nonempty API key in $%s", options.keyEnv)
	}
	config, err := readMemoryConfig(options.memoryConfig)
	if err != nil {
		return err
	}
	// With WatchdogSec, systemd sets WATCHDOG_USEC (and WATCHDOG_PID); they
	// stay set, as NOTIFY_SOCKET does, for the notifications to come.
	watchdog, err := daemon.SdWatchdogEnabled(false)
	if err != nil {
		return fmt.Errorf("systemd watchdog: %w", err)
	}
	if watchdog > 0 && watchdog < amlMinWatchdog {
		// The liveness probe reads a heartbeat that ticks every second.
		return fmt.Errorf("systemd's watchdog interval %s is below %s: set WatchdogSec to at least %s", watchdog, amlMinWatchdog, amlMinWatchdog)
	}
	log := amlLogger(options.logFormat, cmd.ErrOrStderr())
	ctx, stopSignals := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	lock, err := lockDataRoot(options.dataRoot)
	if err != nil {
		return err
	}
	defer lock.Unlock()
	server, err := aml.NewServer(aml.ServerConfig{
		DataRoot: options.dataRoot, MemoryConfig: config, AuthKey: key,
		Consolidation: options.consolidation, ConsolidationWorkers: options.workers,
		MaxOpenStores: options.maxOpenStores, SearchTimeout: options.searchTimeout,
		MaxConcurrentAdds: options.maxAdds, MaxConcurrentSearches: options.maxSearches,
		Logger: log,
	})
	if err != nil {
		return err
	}
	process, err := newAMLProcess(server, options, log, watchdog)
	if err != nil {
		// Nothing has run: no request or model call is in flight.
		server.Close()
		return err
	}
	return process.serve(ctx, stopSignals)
}
