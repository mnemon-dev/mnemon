package memory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gofrs/flock"
	"github.com/mnemon-dev/mnemon/internal/aml"
	"github.com/mnemon-dev/mnemon/internal/memoryagent"
	"github.com/spf13/cobra"
)

// newAMLInspectCommand is `mnemon memory-agent aml-inspect`: the inspect
// service of one engine instance, the memory cloud console's read-only
// retrieval backend (docs/inspect-api.md). It runs beside aml-serve, never
// in its process, and reads its data root without writing anything there.
func newAMLInspectCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "aml-inspect",
		Short: "Serve side-effect-free retrieval over an aml-serve data root, for the console",
		Args:  cobra.NoArgs,
		RunE:  runAMLInspect,
	}
	flags := command.Flags()
	flags.String("data-root", "", "Absolute aml-serve data root to read (required); nothing in it is ever written")
	flags.String("memory-config", "", "Absolute path of the instance's memory-strategy JSON configuration (required): the read's settings and models")
	flags.String("listen", "127.0.0.1:8092", "IP address and port to listen on: a loopback address, or a private one (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, fc00::/7) that needs --token-env")
	flags.String("token-env", "", "Environment variable holding the token (32 bytes at least) that every request must send as an Authorization: Bearer header; required for a private --listen; empty for no token")
	flags.String("debug-budget-file", "", "Absolute budget file full searches reserve and settle in, never the configuration's budgetFile; empty for no full searches")
	flags.String("debug-caps", "", "The debug budget's caps by kind, such as deepseek=5,jev=1 (DeepSeek in CNY, JEV in USD); full searches are refused while none is set")
	flags.Int("max-concurrent", 2, "Most requests reading stores at once; past it a request answers 503 with Retry-After")
	flags.String("log-format", "text", "Log format: text or json")
	return command
}

// amlInspectOptions are aml-inspect's checked flags. token is what the
// variable --token-env names holds, "" without the flag.
type amlInspectOptions struct {
	dataRoot, memoryConfig, listen, logFormat, debugBudget, token string
	caps                                                          map[string]float64
	maxConcurrent                                                 int
}

// amlInspectFlags reads and checks aml-inspect's flags, and the token's
// variable; it touches nothing else. --listen and --token-env follow the
// admin listener's rules (checkListen, listenToken).
func amlInspectFlags(cmd *cobra.Command) (amlInspectOptions, error) {
	flags := cmd.Flags()
	var o amlInspectOptions
	o.dataRoot, _ = flags.GetString("data-root")
	o.memoryConfig, _ = flags.GetString("memory-config")
	o.listen, _ = flags.GetString("listen")
	o.logFormat, _ = flags.GetString("log-format")
	o.debugBudget, _ = flags.GetString("debug-budget-file")
	o.maxConcurrent, _ = flags.GetInt("max-concurrent")
	caps, _ := flags.GetString("debug-caps")
	switch {
	case !filepath.IsAbs(o.dataRoot):
		return o, errors.New("aml-inspect requires an absolute --data-root")
	case o.maxConcurrent < 1:
		return o, errors.New("--max-concurrent must be at least 1")
	case o.logFormat != "text" && o.logFormat != "json":
		return o, fmt.Errorf("--log-format must be text or json, not %q", o.logFormat)
	case o.debugBudget != "" && !filepath.IsAbs(o.debugBudget):
		return o, errors.New("--debug-budget-file must be an absolute path")
	case caps != "" && o.debugBudget == "":
		return o, errors.New("--debug-caps needs --debug-budget-file")
	}
	var err error
	if o.caps, err = debugCaps(caps); err != nil {
		return o, err
	}
	tokenEnv, _ := flags.GetString("token-env")
	if o.token, err = listenToken(inspectListener, tokenEnv); err != nil {
		return o, err
	}
	if o.listen == "" {
		// Unlike the admin listener, the service always listens.
		return o, errors.New("--listen must be an IP address and a port, such as 127.0.0.1:8092 or 10.0.0.5:8092: aml-inspect always listens")
	}
	return o, checkListen(inspectListener, o.listen, o.token != "")
}

// debugCaps reads --debug-caps: kind=amount pairs separated by commas, each
// kind once; "" is none.
func debugCaps(value string) (map[string]float64, error) {
	caps := map[string]float64{}
	if value == "" {
		return caps, nil
	}
	for _, pair := range strings.Split(value, ",") {
		kind, amount, ok := strings.Cut(pair, "=")
		cap, err := strconv.ParseFloat(amount, 64)
		if !ok || err != nil || math.IsNaN(cap) || math.IsInf(cap, 0) || cap < 0 {
			return nil, fmt.Errorf("--debug-caps must be kind=amount pairs, such as deepseek=5,jev=1, not %q", pair)
		}
		if _, paid := memoryagent.PaidKinds()[kind]; !paid {
			return nil, fmt.Errorf("--debug-caps kind %q: the kinds are %s", kind, aml.PaidKindNames())
		}
		if _, dup := caps[kind]; dup {
			return nil, fmt.Errorf("--debug-caps names %s twice", kind)
		}
		caps[kind] = cap
	}
	return caps, nil
}

// inspectShutdownGrace is how long the requests in flight have to finish
// once the inspect service stops; inspectCancelGrace, how long the requests
// canceled then have to end; inspectSettleGrace, how long the model requests
// that the models' close cuts at the end of both windows have to settle
// (R90). 30 s + 5 s + 5 s stays within the unit's TimeoutStopSec=45. Tests
// shorten the first two.
var (
	inspectShutdownGrace = 30 * time.Second
	inspectCancelGrace   = 5 * time.Second
	inspectSettleGrace   = 5 * time.Second
)

// lockDebugBudget takes the debug budget's lock, an exclusive lock on
// <debug-budget-file>.lock, a file of its own beside it: one aml-inspect per
// debug budget, so that what the service finds held there when it starts is
// a previous run's, which it settles (R90). The kernel releases the lock
// when the process ends, however it ends; while another process holds it,
// the command refuses to run.
func lockDebugBudget(path string) (*flock.Flock, error) {
	lock := flock.New(path + ".lock")
	locked, err := lock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("lock %s: %w", lock.Path(), err)
	}
	if !locked {
		return nil, fmt.Errorf("another aml-inspect holds %s", lock.Path())
	}
	return lock, nil
}

// runAMLInspect checks the flags (the token among them) and the memory
// configuration, builds the inspect service (which checks the data root and
// the debug budget file, and opens the latter), takes the debug budget's
// lock and settles what a previous run left held there, binds the listener
// and serves until SIGINT or SIGTERM. It never takes the data root's lock or
// opens a file in the data root but the stores it reads for a request.
func runAMLInspect(cmd *cobra.Command, _ []string) error {
	options, err := amlInspectFlags(cmd)
	if err != nil {
		return err
	}
	config, err := readMemoryConfig(options.memoryConfig)
	if err != nil {
		return err
	}
	log := amlLogger(options.logFormat, cmd.ErrOrStderr())
	ctx, stopSignals := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	inspector, err := aml.NewInspector(aml.InspectConfig{
		DataRoot: options.dataRoot, MemoryConfig: config, Token: options.token,
		DebugBudgetFile: options.debugBudget, DebugCaps: options.caps, MaxConcurrent: options.maxConcurrent,
		Logger: log, Version: version,
	})
	if err != nil {
		return err
	}
	if options.debugBudget != "" {
		lock, err := lockDebugBudget(filepath.Clean(options.debugBudget))
		if err != nil {
			inspector.Close()
			return err
		}
		defer lock.Unlock()
		if err := inspector.SettleHolds(ctx); err != nil {
			log.Error(fmt.Sprintf("aml-inspect: %v; it stays held until a start settles it", err))
		}
	}
	listener, err := net.Listen("tcp", options.listen)
	if err != nil {
		inspector.Close()
		return err
	}
	log.Info(fmt.Sprintf("aml-inspect listening on %s", listener.Addr()))
	return serveInspect(ctx, stopSignals, inspector, listener, log)
}

// serveInspect serves the inspect service on listener until ctx ends or the
// server fails, then shuts down: the requests in flight have
// inspectShutdownGrace to finish, those still running are canceled and have
// inspectCancelGrace more, then the connections close; and the models and
// the debug budget close within what is left of both windows, as aml-serve's
// do (the final review's Minor 2, R90): the models' requests in flight,
// abandoned ones included, are cut at the windows' end and settle in the
// debug budget within inspectSettleGrace more, after which the command
// returns all the same. The serving goroutine is this function's: it is
// waited for before it returns.
func serveInspect(ctx context.Context, stopSignals func(), inspector *aml.Inspector, listener net.Listener, log *slog.Logger) error {
	server := inspector.HTTPServer()
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	var failed error
	select {
	case <-ctx.Done():
		log.Info("aml-inspect: shutting down")
	case failed = <-served:
		log.Error(fmt.Sprintf("aml-inspect: %v; shutting down", failed))
	}
	stopSignals()
	graceEnd := time.Now().Add(inspectShutdownGrace)
	end := graceEnd.Add(inspectCancelGrace)
	grace, endGrace := context.WithDeadline(context.Background(), graceEnd)
	defer endGrace()
	var cut []error
	if err := server.Shutdown(grace); errors.Is(err, context.DeadlineExceeded) {
		log.Warn(fmt.Sprintf("aml-inspect: the %s grace has ended with requests still running; canceling them", inspectShutdownGrace))
		inspector.CancelRequests()
		final, endFinal := context.WithDeadline(context.Background(), end)
		if server.Shutdown(final) != nil {
			server.Close()
		}
		endFinal()
		cut = append(cut, fmt.Errorf("canceled at the end of the %s grace: requests still running", inspectShutdownGrace))
	}
	if failed == nil {
		if err := <-served; !errors.Is(err, http.ErrServerClosed) {
			failed = err
		}
	}
	closing, endClosing := context.WithDeadline(context.Background(), end)
	defer endClosing()
	var closeErr error
	switch {
	case !closeBy(func() error { closeErr = inspector.CloseWithin(closing); return closeErr }, end.Add(inspectSettleGrace)):
		log.Error(fmt.Sprintf("aml-inspect: closing the models did not finish %s after the end of the shutdown; exiting while model requests settle: their debug budget reservations stay held until the next start settles them", inspectSettleGrace))
		cut = append(cut, fmt.Errorf("closing the models did not finish %s after the end of the shutdown: the model requests still settling keep their debug budget reservations held until the next start settles them", inspectSettleGrace))
	case closeErr != nil:
		log.Warn(fmt.Sprintf("aml-inspect: %v", closeErr))
		cut = append(cut, closeErr)
	}
	if errors.Is(failed, http.ErrServerClosed) {
		failed = nil
	}
	return errors.Join(append([]error{failed}, cut...)...)
}
