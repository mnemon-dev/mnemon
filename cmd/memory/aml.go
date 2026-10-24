package memory

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
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
	flags.String("admin-listen", "", "IP address and port of the admin listener (/metrics, /ready, /admin/status, /admin/v1/stores, /debug/pprof/): a loopback address, or a private one (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, fc00::/7) that needs --admin-token-env; empty for none")
	flags.String("admin-token-env", "", "Environment variable holding the token (32 characters at least) that every admin request must send as an Authorization: Bearer header; required for a private --admin-listen; empty for no token")
	flags.Int("max-concurrent-adds", 0, "Most Adds served at once, 0 for no limit; past it an Add waits up to 30s for a slot, then answers 503 with Retry-After")
	flags.Int("max-concurrent-searches", 0, "Most Searches served at once, 0 for no limit; past it a Search waits up to 30s for a slot, then answers 503 with Retry-After")
	flags.String("log-format", "text", "Log format: text or json")
	flags.Int("max-open-stores", 64, "Most user stores kept open at once")
	flags.Duration("search-timeout", 10*time.Minute, "Longest memory read of a Search before it answers the raw ranking (1m to 18m)")
	return command
}

// amlServeOptions are aml-serve's checked flags. adminToken is what the
// variable --admin-token-env names holds, "" without the flag.
type amlServeOptions struct {
	listen, adminListen, dataRoot, memoryConfig, keyEnv, logFormat string
	adminToken                                                     string
	consolidation                                                  aml.ConsolidationMode
	workers, maxOpenStores, maxAdds, maxSearches                   int
	searchTimeout                                                  time.Duration
}

// amlServeFlags reads and checks aml-serve's flags, and the admin token's
// variable; it touches nothing else.
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
	tokenEnv, _ := flags.GetString("admin-token-env")
	if o.adminToken, err = amlAdminToken(tokenEnv); err != nil {
		return o, err
	}
	if err := checkAdminListen(o.adminListen, o.adminToken != ""); err != nil {
		return o, err
	}
	return o, nil
}

// tokenListener is a listener that the bind rule (checkListen) and the
// token rule (listenToken) guard, as their errors name it: its address
// flag, an example port, its token's flag, what the token is called, and
// what anyone who reaches the listener without one could do. aml-serve's
// admin listener and aml-inspect's listener follow the same rules.
type tokenListener struct {
	listen, port, tokenEnv, token, exposes string
}

var (
	adminListener   = tokenListener{listen: "--admin-listen", port: "8091", tokenEnv: "--admin-token-env", token: "admin token", exposes: "profile the process and read its state"}
	inspectListener = tokenListener{listen: "--listen", port: "8092", tokenEnv: "--token-env", token: "inspect token", exposes: "read every user's records"}
)

// amlAdminToken is the admin token the environment variable name holds
// (listenToken).
func amlAdminToken(name string) (string, error) { return listenToken(adminListener, name) }

// listenToken is the token the environment variable name holds, "" when
// name is empty (no token). A variable that is unset, empty or shorter than
// aml.MinAdminTokenLength is an error that names the flag and the variable
// and says nothing of what it holds.
func listenToken(l tokenListener, name string) (string, error) {
	if name == "" {
		return "", nil
	}
	token := os.Getenv(name)
	switch {
	case token == "":
		return "", fmt.Errorf("%s names $%s, which is unset or empty: it must hold the %s, %d characters at least", l.tokenEnv, name, l.token, aml.MinAdminTokenLength)
	case len(token) < aml.MinAdminTokenLength:
		return "", fmt.Errorf("%s names $%s, which holds fewer than %d characters: the %s must hold %d at least", l.tokenEnv, name, aml.MinAdminTokenLength, l.token, aml.MinAdminTokenLength)
	}
	return token, nil
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

// checkAdminListen checks the address of the admin listener, "" for none
// (checkListen).
func checkAdminListen(addr string, tokenSet bool) error {
	return checkListen(adminListener, addr, tokenSet)
}

// checkListen checks the address of a listener, "" for none. Anyone who
// reaches the listener can do what l.exposes says, so it is a loopback
// address, which only this machine reaches, or a private one (netip's
// IsPrivate: 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16 and fc00::/7) when
// its token is set, which only a client that holds it passes. Every other
// address is refused, the token or not: unspecified, link-local, shared
// (100.64.0.0/10) and public ones.
func checkListen(l tokenListener, addr string, tokenSet bool) error {
	if addr == "" {
		return nil
	}
	ip, ok := adminIP(addr)
	if !ok {
		return fmt.Errorf("%s must be an IP address and a port, such as 127.0.0.1:%s or 10.0.0.5:%s; a host name is not accepted, as what it resolves to is not the command's to check; not %q", l.listen, l.port, l.port, addr)
	}
	switch {
	case ip.IsLoopback():
		return nil
	case ip.IsPrivate() && tokenSet:
		return nil
	case ip.IsPrivate():
		return fmt.Errorf("%s %q is a private address, which needs %s: without a token anyone who reaches the listener can %s", l.listen, addr, l.tokenEnv, l.exposes)
	}
	return fmt.Errorf("%s must be a loopback IP address, or a private one (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, fc00::/7) with %s, not an unspecified, link-local, shared or public one; not %q", l.listen, l.tokenEnv, addr)
}

// adminIP is the IP address of addr, which must be an address literal and a
// port, such as 127.0.0.1:8091 or [fd00::1]:8091. A host name, a missing
// port and a zone ([fe80::1%eth0]) are not accepted. An IPv4 address spelled
// as an IPv6 one is the IPv4 address.
func adminIP(addr string) (netip.Addr, bool) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return netip.Addr{}, false
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return netip.Addr{}, false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.Zone() != "" {
		return netip.Addr{}, false
	}
	return ip.Unmap(), true
}

// amlLogger is the logger of --log-format, to w.
func amlLogger(format string, w io.Writer) *slog.Logger {
	if format == "json" {
		return slog.New(slog.NewJSONHandler(w, nil))
	}
	return slog.New(slog.NewTextHandler(w, nil))
}

// runAMLServe checks the flags (the admin token among them), the API key, the
// memory configuration and systemd's watchdog setting, takes the data root's
// lock (before anything opens a file in the data root or the budget file),
// builds the server, binds the listeners and runs the process
// (aml_process.go) until SIGINT or SIGTERM. Its error, the exit status 1, also
// reports a shutdown that had to cut requests, consolidation jobs or model
// requests.
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
		Logger: log, AdminToken: options.adminToken, Version: version,
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
