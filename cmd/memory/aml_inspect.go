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

	"github.com/mnemon-dev/mnemon/internal/aml"
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
	flags.String("token-env", "", "Environment variable holding the token (32 characters at least) that every request must send as an Authorization: Bearer header; required for a private --listen; empty for no token")
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
		switch kind {
		case "deepseek", "jev", "openai":
		default:
			return nil, fmt.Errorf("--debug-caps kind %q: the kinds are deepseek, jev and openai", kind)
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
// canceled then have to end. Tests shorten them.
var (
	inspectShutdownGrace = 30 * time.Second
	inspectCancelGrace   = 5 * time.Second
)

// runAMLInspect checks the flags (the token among them) and the memory
// configuration, builds the inspect service (which checks the data root and
// the debug budget file, and opens the latter), binds the listener and
// serves until SIGINT or SIGTERM. It never takes the data root's lock or
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
// inspectCancelGrace more, then the connections close and the models and the
// debug budget are closed (after their requests in flight). The serving
// goroutine is this function's: it is waited for before it returns.
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
	grace, endGrace := context.WithTimeout(context.Background(), inspectShutdownGrace)
	defer endGrace()
	var cut error
	if err := server.Shutdown(grace); errors.Is(err, context.DeadlineExceeded) {
		log.Warn(fmt.Sprintf("aml-inspect: the %s grace has ended with requests still running; canceling them", inspectShutdownGrace))
		inspector.CancelRequests()
		final, endFinal := context.WithTimeout(context.Background(), inspectCancelGrace)
		if server.Shutdown(final) != nil {
			server.Close()
		}
		endFinal()
		cut = fmt.Errorf("canceled at the end of the %s grace: requests still running", inspectShutdownGrace)
	}
	if failed == nil {
		if err := <-served; !errors.Is(err, http.ErrServerClosed) {
			failed = err
		}
	}
	inspector.Close()
	if errors.Is(failed, http.ErrServerClosed) {
		failed = nil
	}
	return errors.Join(failed, cut)
}
