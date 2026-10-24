package memory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-systemd/v22/daemon"
	"github.com/gofrs/flock"
	"github.com/mnemon-dev/mnemon/internal/aml"
	"golang.org/x/sync/errgroup"
)

// The aml-serve process: its long-lived components run under one supervisor,
// which starts them in order and stops them in phases.
//
// Startup, once the flags are checked, the data root locked and the server
// built (runAMLServe): the public listener is bound, and the admin listener
// too when its address can be (see below); the server's background work
// (aml.Server.Run) starts, and is ready once its startup scan has queued
// every user and its heartbeat ticks; then the admin server, the public
// server and systemd's watchdog start, each once the one before is ready;
// then systemd is told READY=1. The data plane never depends on the admin
// bind (ruling R91): an admin listener that cannot bind (wg0 not up yet) is
// logged once, and the admin server binds it again every amlAdminRetry in the
// background, while the public listener serves; READY=1 does not wait for it.
//
// Shutdown, on SIGINT or SIGTERM or when a component fails: the watchdog
// stops, then systemd is told STOPPING=1; the public listener closes and the
// consolidation scheduler drains (no job starts, no Add waits for it). The
// requests in flight and the running jobs have amlShutdownGrace to finish.
// Then the requests still running are canceled (they answer 503, which the
// platform retries) and the jobs still running are cut (their saved batches
// stay, and they resume after a restart); the canceled requests have
// amlCancelGrace more to end before their connections are closed. Then the
// admin server, kept open so the drain can be watched, closes; every
// component is waited for; and the server closes its stores and models
// within what is left of both windows (ruling R90): closing the models waits
// for their requests in flight, abandoned ones included, until the windows
// end, then cuts those still running, each of which settles its budget
// reservation at its input estimate (or releases it, unsent) within
// amlSettleGrace more. Should even that not finish, the process exits without
// them, their reservations held, and the next start settles them (the lock
// is then given back while the close goes on, as the process exits). A
// shutdown that had to cancel or cut anything fails the command (exit status
// 1) and says what.

const (
	// amlLockName is the data root's single-instance lock file.
	amlLockName = ".aml-serve.lock"
	// amlMinWatchdog is the shortest watchdog interval systemd may give: the
	// liveness probe needs the heartbeat, which ticks every second, to have
	// ticked within the interval, and pings every half interval.
	amlMinWatchdog = 2 * time.Second
)

var (
	// amlShutdownGrace is how long the requests in flight and the running
	// consolidation jobs have to finish once the shutdown starts;
	// amlCancelGrace, how long the requests canceled then have to end. Tests
	// shorten them.
	amlShutdownGrace = 30 * time.Second
	amlCancelGrace   = 5 * time.Second
	// amlSettleGrace is how long the model requests cut at the end of both
	// windows have to settle their reservations before the process exits.
	// 30 s + 5 s + 5 s stays within the unit's TimeoutStopSec=60.
	amlSettleGrace = 5 * time.Second
	// amlAdminRetry is how often an admin listener that could not bind is
	// bound again. Tests shorten it.
	amlAdminRetry = 5 * time.Second
	// amlWatchdogTicker makes the watchdog's ticker, of period d: its ticks
	// and its stop. Tests tick it themselves.
	amlWatchdogTicker = func(d time.Duration) (<-chan time.Time, func()) {
		ticker := time.NewTicker(d)
		return ticker.C, ticker.Stop
	}
	// sdNotify tells systemd the service's state (sd_notify): nothing without
	// NOTIFY_SOCKET, which stays set for the notifications to come. It sends
	// one datagram and returns: the components call it on their goroutines
	// (the watchdog in its loop) and it must not block them. Tests record the
	// states instead.
	sdNotify = func(state string) error {
		_, err := daemon.SdNotify(false, state)
		return err
	}
)

// lockDataRoot creates the data root if needed and takes its single-instance
// lock, an exclusive lock on <data-root>/.aml-serve.lock: a file of its own,
// never a database file, whose SQLite locks are dropped when any descriptor
// on it closes. The kernel releases the lock when the process ends, however
// it ends; while another process holds it the command refuses to run.
func lockDataRoot(root string) (*flock.Flock, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	lock := flock.New(filepath.Join(root, amlLockName))
	locked, err := lock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("lock %s: %w", lock.Path(), err)
	}
	if !locked {
		return nil, fmt.Errorf("another aml-serve holds %s", lock.Path())
	}
	return lock, nil
}

// amlAdminServer is the HTTP server of the admin listener (aml.Server's
// AdminHandler, served on loopback, or on a private address with an admin
// token: checkAdminListen). Its write timeout outlasts the profiles it serves:
// net/http/pprof refuses a profile as long as the write timeout, and one of up
// to 4 minutes finishes within it.
func amlAdminServer(handler http.Handler, log *slog.Logger) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelError),
	}
}

// amlProcess is one aml-serve process: the server and its listeners.
type amlProcess struct {
	server *aml.Server
	log    *slog.Logger
	public *http.Server
	// admin is nil without --admin-listen, whose address is adminAddr; its
	// listener is nil while that address has not bound (bindAdmin).
	admin                         *http.Server
	adminAddr                     string
	publicListener, adminListener net.Listener
	// watchdog is systemd's watchdog interval, 0 without one; watchdogDone
	// closes once the watchdog has returned (nil while it has not started).
	watchdog     time.Duration
	watchdogDone chan struct{}
}

// newAMLProcess binds the process's listeners for server. A public listener
// that cannot bind fails it; an admin listener that cannot bind is logged,
// and bound again later (bindAdmin).
func newAMLProcess(server *aml.Server, options amlServeOptions, log *slog.Logger, watchdog time.Duration) (*amlProcess, error) {
	p := &amlProcess{server: server, log: log, public: server.HTTPServer(options.listen), watchdog: watchdog}
	var err error
	if p.publicListener, err = net.Listen("tcp", options.listen); err != nil {
		return nil, err
	}
	p.logf(slog.LevelInfo, "aml-serve listening on %s", p.publicListener.Addr())
	if options.adminListen != "" {
		p.admin, p.adminAddr = amlAdminServer(server.AdminHandler(), log), options.adminListen
		if p.adminListener, err = net.Listen("tcp", options.adminListen); err != nil {
			p.logf(slog.LevelWarn, "aml-serve: --admin-listen: %v; serving without the admin listener and binding it again every %s", err, amlAdminRetry)
		} else {
			p.logf(slog.LevelInfo, "aml-serve admin listening on %s", p.adminListener.Addr())
		}
	}
	return p, nil
}

func (p *amlProcess) logf(level slog.Level, format string, args ...any) {
	p.log.Log(context.Background(), level, fmt.Sprintf(format, args...))
}

// notify tells systemd state (sdNotify, which does not block); a failure is
// logged.
func (p *amlProcess) notify(state string) {
	if err := sdNotify(state); err != nil {
		p.logf(slog.LevelWarn, "aml-serve: telling systemd %s failed: %v", state, err)
	}
}

// serve runs the process until ctx ends (SIGINT or SIGTERM) or a component
// fails, then shuts it down. Once the shutdown starts, stopSignals gives the
// signals their default action back, so a second one ends the process at
// once. It returns the component's failure and what the shutdown had to cut.
func (p *amlProcess) serve(ctx context.Context, stopSignals func()) error {
	// Serve closes a listener it served; these close the others.
	defer p.publicListener.Close()
	if p.adminListener != nil {
		defer p.adminListener.Close()
	}
	sup := newSupervisor()
	work, stopWork := context.WithCancel(context.Background())
	defer stopWork()
	watch, stopWatch := context.WithCancel(context.Background())
	defer stopWatch()
	// The admin server's retries of its bind end as it closes.
	admin, stopAdmin := context.WithCancel(context.Background())
	defer stopAdmin()
	if p.start(sup, work, watch, admin) == nil {
		p.notify(daemon.SdNotifyReady)
		select {
		case <-ctx.Done():
			p.logf(slog.LevelInfo, "aml-serve: shutting down")
		case <-sup.failed.Done():
			p.logf(slog.LevelError, "aml-serve: %v; shutting down", sup.err())
		}
	}
	stopSignals()
	cut := p.shutdown(sup, stopWork, stopWatch, stopAdmin)
	return errors.Join(sup.err(), cut)
}

// start starts the components in order, each once the one before is ready:
// the background work (Run: its users queued and its heartbeat ticking), the
// admin server, the public server and systemd's watchdog. Run ends with work,
// the watchdog with watch, and an admin server still binding with admin. It
// returns the error of a component that failed to start.
func (p *amlProcess) start(sup *supervisor, work, watch, admin context.Context) error {
	err := sup.start("background work", func(ready func()) error {
		if err := p.server.Run(work, ready); err != nil {
			return err
		}
		if work.Err() == nil {
			return errors.New("it stopped before the shutdown")
		}
		return nil
	})
	switch {
	case err != nil || p.admin == nil:
	case p.adminListener != nil:
		err = sup.start("admin server", serveHTTP(p.admin, p.adminListener))
	default:
		err = sup.start("admin server", p.bindAdmin(admin))
	}
	if err == nil {
		err = sup.start("public server", serveHTTP(p.public, p.publicListener))
	}
	if err == nil && p.watchdog > 0 {
		done := make(chan struct{})
		p.watchdogDone = done
		err = sup.start("watchdog", func(ready func()) error {
			defer close(done)
			ready()
			live := func(ctx context.Context) error { return amlLive(ctx, p.server, p.watchdog) }
			amlWatchdog(watch, p.watchdog, live, p.notify, p.log)
			return nil
		})
	}
	return err
}

// serveHTTP is the component that serves server's requests from listener
// until Shutdown or Close.
func serveHTTP(server *http.Server, listener net.Listener) func(ready func()) error {
	return func(ready func()) error {
		ready()
		if err := server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

// bindAdmin is the admin server's component while its address has not
// bound (R91): it is ready at once, so that the public server starts and
// the data plane serves without it, and it binds the address again every
// amlAdminRetry, quietly, until it can, then serves it as serveHTTP does
// and says so; or until ctx ends, as the admin server closes. A listener
// bound as the server closes is served and closed at once.
func (p *amlProcess) bindAdmin(ctx context.Context) func(ready func()) error {
	return func(ready func()) error {
		ready()
		retry := time.NewTicker(amlAdminRetry)
		defer retry.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-retry.C:
			}
			listener, err := net.Listen("tcp", p.adminAddr)
			if err != nil {
				continue
			}
			p.logf(slog.LevelInfo, "aml-serve admin listening on %s", listener.Addr())
			return serveHTTP(p.admin, listener)(func() {})
		}
	}
}

// shutdown stops the process in the phases the comment at the top of this
// file describes and returns what it had to cut, nil when nothing.
func (p *amlProcess) shutdown(sup *supervisor, stopWork, stopWatch, stopAdmin context.CancelFunc) error {
	graceEnd := time.Now().Add(amlShutdownGrace)
	end := graceEnd.Add(amlCancelGrace)
	// The watchdog stops first, and has stopped, before systemd is told
	// STOPPING=1, so that no ping follows it (the final review's parked
	// T9-a). Its probe ends with it; that is quick (it calls no model).
	stopWatch()
	if p.watchdogDone != nil {
		<-p.watchdogDone
	}
	p.notify(daemon.SdNotifyStopping)
	p.server.Drain()
	grace, endGrace := context.WithDeadline(context.Background(), graceEnd)
	defer endGrace()
	var cut []error
	requestsLeft := false
	if err := p.public.Shutdown(grace); errors.Is(err, context.DeadlineExceeded) {
		requestsLeft = true
	} else if err != nil {
		p.logf(slog.LevelWarn, "aml-serve: closing the public listener: %v", err)
	}
	if err := p.server.WaitDrained(grace); err != nil {
		p.logf(slog.LevelWarn, "aml-serve: the %s grace has ended with %v; cutting them: their saved batches stay, and they resume after a restart", amlShutdownGrace, err)
		cut = append(cut, fmt.Errorf("cut at the end of the %s grace: %w", amlShutdownGrace, err))
	}
	// Run's end cuts the jobs still running, and stops the status line and
	// the heartbeat.
	stopWork()
	if requestsLeft {
		// How many it cancels (the final review's parked T9-e).
		running := requestCount(p.server.RequestsInFlight())
		p.logf(slog.LevelWarn, "aml-serve: the %s grace has ended with %s still running; canceling them", amlShutdownGrace, running)
		p.server.CancelRequests()
		final, endFinal := context.WithDeadline(context.Background(), end)
		if p.public.Shutdown(final) != nil {
			p.logf(slog.LevelError, "aml-serve: requests still running %s after they were canceled; closing their connections", amlCancelGrace)
			p.public.Close()
		}
		endFinal()
		cut = append(cut, fmt.Errorf("canceled at the end of the %s grace: %s still running", amlShutdownGrace, running))
	}
	if p.admin != nil {
		stopAdmin()
		p.admin.Close()
	}
	if stuck := sup.wait(end); len(stuck) > 0 {
		p.logf(slog.LevelError, "aml-serve: %s did not stop by the end of the shutdown", strings.Join(stuck, ", "))
		cut = append(cut, fmt.Errorf("did not stop: %s", strings.Join(stuck, ", ")))
	}
	// The models' close cuts at the windows' end what still runs (R90).
	closing, endClosing := context.WithDeadline(context.Background(), end)
	defer endClosing()
	var closeErr error
	switch {
	case !closeBy(func() error { closeErr = p.server.CloseWithin(closing); return closeErr }, end.Add(amlSettleGrace)):
		p.logf(slog.LevelError, "aml-serve: closing the stores and models did not finish %s after the end of the shutdown; exiting while model requests settle: their budget reservations stay held until the next start settles them", amlSettleGrace)
		cut = append(cut, fmt.Errorf("closing the stores and models did not finish %s after the end of the shutdown: the model requests still settling keep their budget reservations held until the next start settles them", amlSettleGrace))
	case closeErr != nil:
		p.logf(slog.LevelWarn, "aml-serve: %v", closeErr)
		cut = append(cut, closeErr)
	}
	return errors.Join(cut...)
}

// requestCount is n requests, in words: "1 request", "3 requests".
func requestCount(n int64) string {
	if n == 1 {
		return "1 request"
	}
	return fmt.Sprintf("%d requests", n)
}

// closeBy runs closer and reports whether it returned by the deadline. Past
// it closer goes on alone, until it returns or the process exits; what it
// writes is the caller's to read only once closeBy has reported true.
func closeBy(closer func() error, deadline time.Time) bool {
	closed := make(chan struct{})
	go func() {
		closer()
		close(closed)
	}()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-closed:
		return true
	case <-timer.C:
		return false
	}
}

// supervisor owns the process's long-lived components (the Go engineering
// standard, §4.1 and §6): it starts them one at a time, each once the one
// before is ready; the first to fail ends failed, which starts the
// shutdown; and wait waits for them all. A component is never restarted
// alone: the process exits and systemd restarts it.
type supervisor struct {
	group  *errgroup.Group
	failed context.Context

	mu      sync.Mutex
	running []string // the components that have not returned, in start order
	first   error    // the first component's failure
}

func newSupervisor() *supervisor {
	group, failed := errgroup.WithContext(context.Background())
	return &supervisor{group: group, failed: failed}
}

// start runs the component named name, which calls ready once it serves,
// and returns once it is ready, or with its failure once it has returned
// without being ready. A component that returns before it is ready fails.
// ready never blocks, and a second call does nothing.
func (s *supervisor) start(name string, run func(ready func()) error) error {
	ready, exited := make(chan struct{}), make(chan error, 1)
	var once sync.Once
	s.mu.Lock()
	s.running = append(s.running, name)
	s.mu.Unlock()
	s.group.Go(func() error {
		err := run(func() { once.Do(func() { close(ready) }) })
		select {
		case <-ready:
		default:
			if err == nil {
				err = errors.New("it stopped before it was ready")
			}
		}
		if err != nil {
			err = fmt.Errorf("%s: %w", name, err)
		}
		s.exited(name, err)
		exited <- err
		return err
	})
	select {
	case <-ready:
		return nil
	case err := <-exited:
		return err
	}
}

// exited records that the component name returned with err.
func (s *supervisor) exited(name string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = slices.DeleteFunc(s.running, func(running string) bool { return running == name })
	if s.first == nil {
		s.first = err
	}
}

// err is the first component's failure, nil while none failed.
func (s *supervisor) err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.first
}

// wait waits for every component to return, until the deadline at most, and
// returns the names of those still running then. Past the deadline its
// waiting goroutine is left to the process's exit.
func (s *supervisor) wait(deadline time.Time) []string {
	done := make(chan struct{})
	go func() {
		s.group.Wait()
		close(done)
	}()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		s.mu.Lock()
		defer s.mu.Unlock()
		return slices.Clone(s.running)
	}
}

// amlWatchdog pings systemd's watchdog (WATCHDOG=1) every half interval,
// each time live answers within a quarter interval, until ctx ends. A failed
// probe skips its ping and is logged: systemd restarts a service whose pings
// stop for an interval.
func amlWatchdog(ctx context.Context, interval time.Duration, live func(context.Context) error, notify func(state string), log *slog.Logger) {
	ticks, stop := amlWatchdogTicker(interval / 2)
	defer stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
		probe, cancel := context.WithTimeout(ctx, interval/4)
		err := live(probe)
		cancel()
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			log.Warn(fmt.Sprintf("aml-serve watchdog: no ping: %v", err))
		default:
			notify(daemon.SdNotifyWatchdog)
		}
	}
}

// amlLive is the watchdog's liveness probe of server: its public /health
// handler, served in process, answers 200; its background work's heartbeat
// (aml.Server.Heartbeat: Run runs and the scheduler's and the store
// registry's locks are free) ticked within interval; and its budget file
// answers. It opens no store and calls no model, so a slow or failing
// provider never makes the service look dead: /ready reports those.
func amlLive(ctx context.Context, server *aml.Server, interval time.Duration) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "/health", nil)
	if err != nil {
		return err
	}
	health := &probeResponse{header: http.Header{}}
	server.Handler().ServeHTTP(health, request)
	if health.status != http.StatusOK {
		return fmt.Errorf("/health answered %d", health.status)
	}
	beat := server.Heartbeat()
	if beat.IsZero() {
		return errors.New("the background work has not ticked")
	}
	if age := time.Since(beat); age > interval {
		return fmt.Errorf("the background work last ticked %s ago", age.Round(time.Millisecond))
	}
	if _, err := server.Accounts(ctx); err != nil {
		return fmt.Errorf("the budget file does not answer: %w", err)
	}
	return nil
}

// probeResponse keeps the status a handler answers the liveness probe with;
// the body is dropped.
type probeResponse struct {
	header http.Header
	status int
}

func (r *probeResponse) Header() http.Header { return r.header }

func (r *probeResponse) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}

func (r *probeResponse) Write(body []byte) (int, error) {
	r.WriteHeader(http.StatusOK)
	return len(body), nil
}
