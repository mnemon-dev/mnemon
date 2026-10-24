package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/gofrs/flock"
	"github.com/mnemon-dev/mnemon/internal/aml"
	"github.com/mnemon-dev/mnemon/internal/memoryagent"
	"github.com/spf13/cobra"
)

// aml-watch is the alert watcher of an aml-serve: a one-shot command that a
// systemd timer runs every minute. One run
//
//  1. takes the exclusive lock <state>.lock (see below);
//  2. reads the admin listener's /ready and /admin/status (with the admin
//     token as a bearer token, when --admin-token-env names one) and the
//     public listener's /health, each within 5 seconds;
//  3. applies the rules below to what it read and to the state the last run
//     left (counters, when they last rose, when a condition began);
//  4. posts one text message per alert that fires, per firing alert that is
//     due again, and per alert that clears, to the webhook in AML_ALERT_WEBHOOK
//     (AML_ALERT_KIND: telegram, feishu or wecom), each within 10 seconds, each
//     starting with the tag [mnemon-aml] (watchTag), whose header says which
//     host the watcher runs on, and which instance with --instance; and
//  5. saves the state, atomically (a temporary file in the state's directory,
//     renamed over it; mode 0600), whether or not a post failed.
//
// The admin token is a secret too. It is read once, from the variable
// --admin-token-env names, and sent to the admin listener only: not to the
// public one, and not along a redirect, whose Location is not the operator's.
// It is never printed, logged, put in an error, a message or the state.
//
// The webhook's URL is a secret (its path is a bearer token: a Feishu or WeCom
// hook's key, a Telegram bot's token): it is never printed, logged or put in
// an error. A failed post says the HTTP status, the code the webhook answered
// with, or that the webhook could not be reached (net/http's own error names
// the URL, so it is not used). A Telegram bot's URL is its sendMessage with the
// chat in the query (telegramTarget): the post goes to the URL without the
// query, the chat in its body, and only 2xx with "ok":true is a delivery
// (telegramRejection). A message that
// went out is recorded as sent, and not sent again as a new alert (unless the
// state cannot be saved: then the run exits non-zero and the next run says it
// again); one that did not go out stays unsent in the state, the next run
// sends it, and the run exits non-zero. Delivery is at-least-once, for every
// alert: an alert that never went out and then clears owes its resolution all
// the same, which says "alert not delivered" and is tried every run until the
// webhook takes it, with no cap on the tries and no age after which it is
// given up; the state keeps one entry per alert name. Without a webhook the
// alerts and their resolutions are logged instead (and kept, to be posted once
// there is a webhook) and the run exits zero, so that the timer can run before
// the webhook is configured.
//
// One run at a time: a run takes an exclusive, non-blocking lock on
// <state>.lock (gofrs/flock; the file is left in place). If another run holds
// it, the run logs one line saying so, posts and saves nothing, and exits 0;
// two overlapping runs would post twice and the last to save would win. A lock
// that cannot be opened for another reason does not stop the run, which goes
// on without it, and says so.
//
// Rules, each an alert of the name shown (a name with a colon is per kind of
// model provider, or account):
//
//	degraded-search          a Search was answered by the raw ranking since the last run
//	partial-search           more than 5% of the last 10 minutes' Searches were partially
//	                         degraded, once there were 20 of them
//	breaker-open:<kind>      a provider's circuit breaker is open, or opened since the last
//	                         run (the count of trips only grows, so one that opened and
//	                         recovered between two runs still shows)
//	budget-high:<kind>       an account's spent and held amounts are above 80% of its cap
//	budget-held:<kind>       an account's held reservations are above 2% of its cap
//	backlog-age              the oldest work not yet consolidated has waited more than 15 minutes
//	backlog-lag              the largest user backlog has been above the server's lag bound
//	                         for 15 minutes (the bound is read from the status)
//	consolidation-dropped    a batch was dropped from the consolidation layer
//	link-fallback            a batch was linked without JEV
//	add-outran               an Add waited out its window for consolidation
//	restart                  the server started at another time than at the last run
//	ready                    /ready did not answer 200, or /admin/status could not be read
//	health                   the public /health did not answer 200 (not probed when
//	                         --public-url is empty: every run then logs a warning)
//
// The counters of the status count from the server's start, so a restart
// starts them at zero: after one, whatever they count is new. The first run
// (no state file) takes the counters as they are and says nothing about them.
//
// An alert about something that happens (degraded-search, breaker-open,
// consolidation-dropped, link-fallback, add-outran and restart: the others
// are about how things are) is firing for 10 minutes after the last run that
// saw it happen, so that a stream of events is one alert, repeated every 30
// minutes while it lasts and then resolved, not a pair of messages per event.
// Any firing alert is said again 30 minutes after its last message. When the
// status cannot be read, the alerts that come from it are neither repeated
// nor resolved: the ready alert says what is wrong.

const (
	// watchRepeat is how long after its last message a firing alert is said
	// again.
	watchRepeat = 30 * time.Minute
	// watchHold is how long an alert about a counter keeps firing after the
	// last time the counter was seen to rise.
	watchHold = 10 * time.Minute

	watchBacklogAge = 15 * time.Minute
	watchLagFor     = 15 * time.Minute
	// watchBudgetUsed is the share of a cap spent and held above which an
	// account alerts; watchBudgetHeld, the share held alone.
	watchBudgetUsed = 0.80
	watchBudgetHeld = 0.02
	// watchPartialPercent is the share of the last 10 minutes' Searches that
	// may be partially degraded, in percent, and watchPartialSearches the
	// fewest Searches in that window from which the share counts.
	watchPartialPercent  = 5
	watchPartialSearches = 20

	// watchMessageBytes bounds a message: under 1,000 characters, and under
	// WeCom's 2,048 bytes and Telegram's 4,096 characters whatever the
	// characters.
	watchMessageBytes = 999

	// watchTag starts every message, of every kind. A Feishu bot protected by
	// a keyword takes only the texts that contain it and answers the others
	// 200 with a code, which is a failed delivery; the runbook has the
	// operator register the keyword mnemon-aml, so no text may lack it.
	watchTag = "[mnemon-aml]"

	watchReadTimeout = 5 * time.Second
	watchHookTimeout = 10 * time.Second
	// watchRunLimit bounds a run: what it has not sent by then is left to
	// the next run.
	watchRunLimit = time.Minute

	watchStateVersion = 1
	// watchValueBytes bounds the value an alert keeps in the state.
	watchValueBytes  = 1000
	watchReadLimit   = 1 << 20
	watchAnswerLimit = 64 << 10
)

// newAMLWatchCommand is `mnemon memory-agent aml-watch`: one run of the alert
// watcher (the comment above).
func newAMLWatchCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "aml-watch",
		Short: "Check a running aml-serve once and post its alerts to a Telegram, Feishu or WeCom bot",
		Long: `Check a running aml-serve once: read its admin listener's /ready and /admin/status and its
public /health, apply the alert rules, and post a text message for each alert that fires, each
firing alert that is due again (every 30 minutes) and each alert that clears, to the webhook named
by the environment variable AML_ALERT_WEBHOOK, in the payload of AML_ALERT_KIND (telegram, feishu or
wecom); for telegram the webhook is https://api.telegram.org/bot<token>/sendMessage?chat_id=<chat id>.
Every text starts with [mnemon-aml], the keyword to register on a Feishu bot, and says which host
it is from (and which instance, with --instance). Run it every minute with a systemd timer. The
webhook URL and the admin token are secrets and are never printed. With no webhook the alerts are
logged only. The state between runs is the file --state; one run at a time holds <state>.lock.`,
		Args: cobra.NoArgs,
		RunE: runAMLWatch,
	}
	flags := command.Flags()
	flags.String("admin", "http://127.0.0.1:8091", "URL of aml-serve's admin listener (--admin-listen)")
	flags.String("public-url", "", "URL of the public listener as clients reach it, such as https://memory.example.com, whose /health is checked; empty to skip that check (every run then logs a warning)")
	flags.String("state", "", "Absolute path of the state file kept between runs, such as /var/lib/mnemon-aml/watch-state.json (required)")
	flags.String("admin-token-env", "", "Environment variable holding the token aml-serve's admin listener requires (its --admin-token-env), sent as a bearer token on every admin read; empty for none")
	flags.String("instance", "", "Name of the aml-serve instance this watcher watches, such as beta-1 (2 to 31 lowercase letters, digits and hyphens, starting with a letter): every message then says it is on host/name; empty for the host alone")
	return command
}

// watchSettings are aml-watch's checked settings. webhook is empty to log the
// alerts instead of posting them. adminToken is the bearer token of the admin
// reads, "" for none; instance names the instance in the messages, "" for the
// host alone.
type watchSettings struct {
	admin, public, state string
	webhook, kind        string
	adminToken, instance string
}

// watchInstance is the shape of an instance's name (--instance).
var watchInstance = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)

// amlWatchSettings reads and checks aml-watch's flags and environment; it
// touches nothing. The webhook and its kind are checked when the watcher runs.
func amlWatchSettings(cmd *cobra.Command) (watchSettings, error) {
	flags := cmd.Flags()
	var s watchSettings
	admin, _ := flags.GetString("admin")
	public, _ := flags.GetString("public-url")
	s.state, _ = flags.GetString("state")
	s.instance, _ = flags.GetString("instance")
	tokenEnv, _ := flags.GetString("admin-token-env")
	var err error
	if s.admin, err = watchBaseURL("--admin", admin); err != nil {
		return s, err
	}
	if public != "" {
		if s.public, err = watchBaseURL("--public-url", public); err != nil {
			return s, err
		}
	}
	switch {
	case s.state == "":
		return s, errors.New("--state is required: the file the watcher keeps between runs")
	case !filepath.IsAbs(s.state):
		return s, errors.New("--state must be an absolute path")
	case s.instance != "" && !watchInstance.MatchString(s.instance):
		return s, fmt.Errorf("--instance must be 2 to 31 characters: lowercase letters, digits and hyphens, starting with a letter; not %q", s.instance)
	}
	if tokenEnv != "" {
		// Read once, as it is. aml-serve, which has a rule for the token's
		// length, refuses the one that is too short; here it must be there.
		if s.adminToken = os.Getenv(tokenEnv); s.adminToken == "" {
			return s, fmt.Errorf("--admin-token-env names $%s, which is unset or empty: it must hold the admin token", tokenEnv)
		}
	}
	s.webhook = strings.TrimSpace(os.Getenv("AML_ALERT_WEBHOOK"))
	s.kind = strings.ToLower(strings.TrimSpace(os.Getenv("AML_ALERT_KIND")))
	return s, nil
}

// watchBaseURL is raw as the base of the requests of flag: an http or https
// URL without credentials, query or trailing slash.
func watchBaseURL(flag, raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("%s must be an http or https URL, such as http://127.0.0.1:8091", flag)
	}
	if u.User != nil {
		return "", fmt.Errorf("%s must not contain credentials", flag)
	}
	u.RawQuery, u.Fragment = "", ""
	return strings.TrimRight(u.String(), "/"), nil
}

// runAMLWatch is one run: the process's SIGINT and SIGTERM end the requests
// in flight, and the state is saved all the same.
func runAMLWatch(cmd *cobra.Command, _ []string) error {
	settings, err := amlWatchSettings(cmd)
	if err != nil {
		return err
	}
	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	return newWatcher(settings, amlLogger("text", cmd.ErrOrStderr())).run(ctx)
}

// watcher is one aml-watch run's world: where to look, where to post, the
// clock and the logger.
type watcher struct {
	watchSettings
	host string
	now  func() time.Time
	log  *slog.Logger
	// reads makes the requests to the admin and public listeners, hooks the
	// post to the webhook, which is not followed to another address.
	reads, hooks *http.Client
}

func newWatcher(settings watchSettings, log *slog.Logger) *watcher {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	return &watcher{
		watchSettings: settings, host: host, now: time.Now, log: log,
		reads: &http.Client{Timeout: watchReadTimeout, CheckRedirect: watchReadRedirect},
		hooks: &http.Client{
			Timeout:       watchHookTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// watchReadRedirect is the redirect policy of the reads: net/http's own (ten
// redirects at most), except that a request that carries the admin token goes
// nowhere else, as the Location of an answer is not the operator's.
func watchReadRedirect(req *http.Request, via []*http.Request) error {
	if req.Header.Get("Authorization") != "" {
		return http.ErrUseLastResponse
	}
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	return nil
}

// origin is what the header of a message says it is on: the host, and with
// --instance, host/name.
func (w *watcher) origin() string {
	if w.instance == "" {
		return w.host
	}
	return w.host + "/" + w.instance
}

// run reads, evaluates, posts and saves, once. Its error says that a message
// could not be delivered, that the state could not be saved or read, or that
// the run was interrupted; an alert, a server that does not answer included,
// is not an error.
func (w *watcher) run(ctx context.Context) error {
	if err := w.checkDelivery(); err != nil {
		return err
	}
	unlock, otherRun := w.lockRun()
	if otherRun {
		return nil
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(ctx, watchRunLimit)
	defer cancel()
	now := w.now().UTC()
	prev, err := loadWatchState(w.state, w.log)
	if err != nil {
		return err
	}
	obs := w.observe(ctx)
	if err := ctx.Err(); err != nil {
		// An interrupted run saw nothing, whatever its probes made of that: it
		// says nothing and keeps the state as it was.
		return fmt.Errorf("aml-watch was interrupted before it had read the server: %w", err)
	}
	next, values, frozen := evaluate(obs, prev, now)
	var messages []watchMessage
	next.Firing, messages = planAlerts(now, w.origin(), values, frozen, prev.Firing)
	outcomes, deliveryErr := w.deliver(ctx, messages)
	settleAlerts(next.Firing, messages, outcomes, now)
	return errors.Join(deliveryErr, saveWatchState(w.state, next))
}

// lockRun takes the exclusive, non-blocking lock on <state>.lock for the run,
// so that two runs (the timer's and an operator's, say) do not overlap: they
// would post the same messages twice, and the last to save would win. If
// another run holds it, otherRun is true: the run has said so and does
// nothing (no post, no save) and exits 0. A lock that cannot be opened at all
// (a read-only directory) does not stop the run: delivery matters more than
// the lock, so the run goes on without it, and says so. unlock gives the lock
// back, closing its file.
func (w *watcher) lockRun() (unlock func(), otherRun bool) {
	path := w.state + ".lock"
	noLock := func() {}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		w.log.Warn("aml-watch: could not take the run lock; going on without it", "lock", path, "error", err)
		return noLock, false
	}
	lock := flock.New(path)
	locked, err := lock.TryLock()
	switch {
	case err != nil:
		w.log.Warn("aml-watch: could not take the run lock; going on without it", "lock", path, "error", err)
		return noLock, false
	case !locked:
		w.log.Warn("aml-watch: another aml-watch run holds the lock; this run does nothing", "lock", path)
		return noLock, true
	}
	return func() {
		if err := lock.Unlock(); err != nil {
			w.log.Warn("aml-watch: could not give the run lock back", "lock", path, "error", err)
		}
	}, false
}

// checkDelivery checks the webhook and its kind; without a webhook there is
// nothing to check.
func (w *watcher) checkDelivery() error {
	if w.webhook == "" {
		return nil
	}
	if w.kind != "feishu" && w.kind != "wecom" && w.kind != "telegram" {
		return errors.New("AML_ALERT_KIND must be feishu or wecom or telegram")
	}
	// The errors never repeat the value: its path is a secret.
	u, err := url.Parse(w.webhook)
	if err != nil || u.Host == "" || !(u.Scheme == "https" || u.Scheme == "http" && watchLoopbackHost(u.Hostname())) {
		return errors.New("AML_ALERT_WEBHOOK must be an https URL (http only to localhost or a loopback address)")
	}
	if w.kind == "telegram" {
		_, _, err = telegramTarget(u)
	}
	return err
}

// watchTelegramChat is the shape of a Telegram chat id: a chat's number (a
// group's or a channel's is negative), or a public channel's @username.
var watchTelegramChat = regexp.MustCompile(`^(-?[0-9]{1,20}|@[A-Za-z][A-Za-z0-9_]{4,31})$`)

// telegramTarget is where a Telegram bot's messages go, from the webhook u,
// which must be the bot's sendMessage with the chat in the query and nothing
// else there: https://api.telegram.org/bot<token>/sendMessage?chat_id=<chat id>,
// on any host (a self-hosted Bot API server answers the same path). target is
// u without its query, and chat the chat_id as given, which the body carries.
// The errors say the rule, never the value: the path holds the bot's token.
func telegramTarget(u *url.URL) (target, chat string, err error) {
	if !strings.HasSuffix(u.Path, "/sendMessage") {
		return "", "", errors.New("AML_ALERT_WEBHOOK for telegram must be the bot's sendMessage URL: https://api.telegram.org/bot<token>/sendMessage?chat_id=<chat id>")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if chats := query["chat_id"]; err != nil || len(query) != 1 || len(chats) != 1 || !watchTelegramChat.MatchString(chats[0]) {
		return "", "", errors.New("AML_ALERT_WEBHOOK for telegram must end in ?chat_id=<chat id>, its only parameter: a chat's number (negative for a group or a channel) or a channel's @username")
	}
	bare := *u
	bare.RawQuery, bare.ForceQuery, bare.Fragment, bare.RawFragment = "", false, "", ""
	return bare.String(), query.Get("chat_id"), nil
}

// watchLoopbackHost reports whether host is this machine: localhost, in any
// case, or a loopback IP address.
func watchLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ---- what the run reads ----

// watchObservation is what one run read. status is nil when /admin/status
// could not be read; each other field is "" when its probe was fine, else
// says what was wrong.
type watchObservation struct {
	status    *aml.AdminStatus
	statusErr string
	ready     string
	health    string
}

// observe reads /ready, /admin/status and, with a public URL, /health.
func (w *watcher) observe(ctx context.Context) watchObservation {
	var obs watchObservation
	obs.ready = w.probeReady(ctx)
	obs.status, obs.statusErr = w.readStatus(ctx)
	if w.public != "" {
		obs.health = w.probeHealth(ctx)
	} else {
		// The probe through the public URL is the only check of the path
		// clients take (DNS, TLS, the proxy): a forgotten flag leaves a trace.
		w.log.Warn("aml-watch: the public health probe is off because --public-url is empty")
	}
	return obs
}

// fetch GETs base+path and returns the status and the start of the body. A
// token, when not empty, is sent as the request's bearer token: the admin
// reads pass the admin token, the public one none.
func (w *watcher) fetch(ctx context.Context, base, path, token string) (int, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+path, nil)
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Accept", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := w.reads.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, watchReadLimit))
	return response.StatusCode, body, err
}

// watchRequestError is err without the URL net/http puts in front of it.
func watchRequestError(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	return err.Error()
}

func (w *watcher) probeReady(ctx context.Context) string {
	code, body, err := w.fetch(ctx, w.admin, "/ready", w.adminToken)
	switch {
	case err != nil:
		return "/ready did not answer: " + watchRequestError(err)
	case code == http.StatusOK:
		return ""
	}
	var ready struct{ Reasons []string }
	if code == http.StatusServiceUnavailable && json.Unmarshal(body, &ready) == nil && len(ready.Reasons) > 0 {
		return "/ready answered 503: " + strings.Join(ready.Reasons, "; ")
	}
	return fmt.Sprintf("/ready answered %d", code)
}

func (w *watcher) readStatus(ctx context.Context) (*aml.AdminStatus, string) {
	code, body, err := w.fetch(ctx, w.admin, "/admin/status", w.adminToken)
	if err != nil {
		return nil, "/admin/status did not answer: " + watchRequestError(err)
	}
	if code != http.StatusOK {
		return nil, fmt.Sprintf("/admin/status answered %d", code)
	}
	var status aml.AdminStatus
	if json.Unmarshal(body, &status) != nil || status.StartedAt.IsZero() {
		return nil, "/admin/status did not answer with an aml-serve status"
	}
	return &status, ""
}

func (w *watcher) probeHealth(ctx context.Context) string {
	target := strings.TrimRight(w.public, "/") + "/health"
	code, _, err := w.fetch(ctx, w.public, "/health", "")
	switch {
	case err != nil:
		return fmt.Sprintf("GET %s did not answer: %s", target, watchRequestError(err))
	case code != http.StatusOK:
		return fmt.Sprintf("GET %s answered %d", target, code)
	}
	return ""
}

// ---- the state between runs ----

// watchState is the state file: JSON, rewritten whole by every run.
//
//	startedAt  the start of the server whose counters these are (zero before
//	           any status was read)
//	counters   the status's counters as of the last run that read it, by name
//	           (the status's own for the Search and consolidation counters,
//	           "trips:<kind>" for a kind's breaker trips)
//	rises      when each alert about a counter was last seen to be new
//	since      when each condition that must last (backlog-lag) was first seen
//	firing     the alerts that fired and have not been resolved
type watchState struct {
	Version   int                   `json:"version"`
	CheckedAt time.Time             `json:"checkedAt"`
	StartedAt time.Time             `json:"startedAt"`
	Counters  map[string]int64      `json:"counters"`
	Rises     map[string]time.Time  `json:"rises"`
	Since     map[string]time.Time  `json:"since"`
	Firing    map[string]watchAlert `json:"firing"`
}

// watchAlert is an alert in the state: since when it fires, when its message
// last went out (zero if none did: it is sent by the next run), and the value
// that fired it last. An alert that stopped firing stays in the state until
// its resolution is delivered.
type watchAlert struct {
	Since    time.Time `json:"since"`
	LastSent time.Time `json:"lastSent"`
	Value    string    `json:"value"`
}

func newWatchState() watchState {
	return watchState{Version: watchStateVersion, Counters: map[string]int64{}, Rises: map[string]time.Time{}, Since: map[string]time.Time{}, Firing: map[string]watchAlert{}}
}

// loadWatchState reads the state file. A missing file is a first run, and a
// file that is not a state of this version is one too (said in the log): the
// run goes on and writes a good one. A file that cannot be read is an error.
func loadWatchState(path string, log *slog.Logger) (watchState, error) {
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return newWatchState(), nil
	}
	if err != nil {
		return watchState{}, fmt.Errorf("read the watch state: %w", err)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, watchReadLimit))
	if err != nil {
		return watchState{}, fmt.Errorf("read the watch state: %w", err)
	}
	var state watchState
	if err := json.Unmarshal(raw, &state); err != nil || state.Version != watchStateVersion {
		log.Warn("aml-watch: the state file is unreadable or of another version; starting afresh", "path", path)
		return newWatchState(), nil
	}
	state.normalize()
	return state, nil
}

// normalize makes the maps of a state read from a file (null, or missing)
// maps again.
func (s *watchState) normalize() {
	if s.Counters == nil {
		s.Counters = map[string]int64{}
	}
	if s.Rises == nil {
		s.Rises = map[string]time.Time{}
	}
	if s.Since == nil {
		s.Since = map[string]time.Time{}
	}
	if s.Firing == nil {
		s.Firing = map[string]watchAlert{}
	}
}

// saveWatchState writes state to path atomically: to a temporary file in the
// same directory (os.CreateTemp makes it mode 0600; it is flushed), renamed
// over the old state. The directory is made if it is missing.
func saveWatchState(path string, state watchState) error {
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("save the watch state: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("save the watch state: %w", err)
	}
	temp, err := os.CreateTemp(dir, ".watch-state-*.tmp")
	if err != nil {
		return fmt.Errorf("save the watch state: %w", err)
	}
	_, writeErr := temp.Write(append(raw, '\n'))
	syncErr := temp.Sync()
	closeErr := temp.Close()
	err = errors.Join(writeErr, syncErr, closeErr)
	if err == nil {
		err = os.Rename(temp.Name(), path)
	}
	if err != nil {
		os.Remove(temp.Name())
		return fmt.Errorf("save the watch state: %w", err)
	}
	return nil
}

// ---- the rules ----

// evaluation is one application of the rules: the state the next run starts
// from (next), and the value of each alert that fires now (firing).
type evaluation struct {
	now  time.Time
	prev watchState
	next watchState
	// fresh: no status was read before. restarted: the server is another
	// process than the last status's.
	fresh, restarted bool
	// since: the conditions that have lasted, as of the last run.
	since  map[string]time.Time
	firing map[string]string
}

// evaluate applies the rules to obs and the last run's state. It returns the
// state to save (its firing alerts are planned next: planAlerts), the value of
// each alert that fires now, and the alerts whose rules could not be applied
// because the status could not be read: they are left as they were.
func evaluate(obs watchObservation, prev watchState, now time.Time) (watchState, map[string]string, map[string]bool) {
	e := &evaluation{now: now, prev: prev, firing: map[string]string{}, since: prev.Since}
	e.next = watchState{
		Version: watchStateVersion, CheckedAt: now, StartedAt: prev.StartedAt,
		Counters: prev.Counters, Rises: map[string]time.Time{}, Since: prev.Since,
	}
	if obs.ready != "" || obs.statusErr != "" {
		e.firing["ready"] = strings.Join(slices.DeleteFunc([]string{obs.ready, obs.statusErr}, func(s string) bool { return s == "" }), "; ")
	}
	if obs.health != "" {
		e.firing["health"] = obs.health
	}
	frozen := map[string]bool{}
	if obs.status == nil {
		// Whatever the status would have said is unknown: the rises are kept
		// to be measured against later.
		maps.Copy(e.next.Rises, prev.Rises)
		for name := range prev.Firing {
			frozen[name] = name != "ready" && name != "health"
		}
		return e.next, e.firing, frozen
	}
	e.status(obs.status)
	return e.next, e.firing, frozen
}

// status applies the rules that read the status.
func (e *evaluation) status(st *aml.AdminStatus) {
	e.fresh = e.prev.StartedAt.IsZero()
	e.restarted = !e.fresh && !st.StartedAt.Equal(e.prev.StartedAt)
	e.next.StartedAt = st.StartedAt
	e.next.Counters = map[string]int64{}
	e.next.Since = map[string]time.Time{}
	if e.restarted {
		// The backlog is the new process's.
		e.since = map[string]time.Time{}
	}
	e.event("restart", e.restarted,
		fmt.Sprintf("aml-serve restarted: it started at %s; the last run saw the one that started at %s", watchTime(st.StartedAt), watchTime(e.prev.StartedAt)),
		fmt.Sprintf("aml-serve restarted and has run since %s", watchTime(st.StartedAt)))
	e.counter("degraded-search", "degraded", watchNoun{"degraded Search", "degraded Searches"}, st.Degraded)
	e.counter("consolidation-dropped", "dropped", watchNoun{"drop from the consolidation layer", "drops from the consolidation layer"}, st.Dropped)
	e.counter("link-fallback", "linkFallbacks", watchNoun{"batch linked without JEV", "batches linked without JEV"}, st.LinkFallbacks)
	e.counter("add-outran", "addOutran", watchNoun{"Add that waited out its window for consolidation", "Adds that waited out their window for consolidation"}, st.AddOutran)
	e.breakers(st)
	e.partial(st)
	e.budgets(st.Budgets)
	e.backlog(st)
}

// base is the count the last run left for the counter key, which is what
// "new" is measured from: the current count on the first run (the history is
// not new), zero after a restart (the counters count from the start).
func (e *evaluation) base(key string, current int64) int64 {
	if e.fresh {
		return current
	}
	if e.restarted {
		return 0
	}
	if last, ok := e.prev.Counters[key]; ok {
		return last
	}
	return current
}

// watchNoun is a noun in the singular and the plural.
type watchNoun struct{ one, many string }

// of is the noun for n of them.
func (noun watchNoun) of(n int64) string {
	if n == 1 {
		return noun.one
	}
	return noun.many
}

// count is n of them: "1 trip", "2 trips".
func (noun watchNoun) count(n int64) string { return fmt.Sprintf("%d %s", n, noun.of(n)) }

// counter is the alert about a counter that grows when something happens.
func (e *evaluation) counter(alert, key string, noun watchNoun, current int64) {
	base := e.base(key, current)
	e.next.Counters[key] = current
	e.event(alert, current > base,
		fmt.Sprintf("%d new %s (%d since the process started)", current-base, noun.of(current-base), current),
		fmt.Sprintf("%s since the process started", noun.count(current)))
}

// event is an alert about something that happens: it fires when rose, and
// goes on firing for watchHold after the last run that saw it rise. risen is
// its value in a run that saw it, held in one that did not.
func (e *evaluation) event(alert string, rose bool, risen, held string) {
	if rose {
		e.next.Rises[alert] = e.now
		e.firing[alert] = risen
		return
	}
	last, ok := e.prev.Rises[alert]
	if !ok || e.now.Sub(last) >= watchHold {
		return
	}
	e.next.Rises[alert] = last
	e.firing[alert] = fmt.Sprintf("%s (last seen %s ago)", held, e.now.Sub(last).Round(time.Second))
}

// breakers: a provider kind's breaker is open now, or opened since the last
// run.
func (e *evaluation) breakers(st *aml.AdminStatus) {
	kinds := map[string]bool{}
	for kind := range st.Breakers {
		kinds[kind] = true
	}
	for kind := range st.BreakerTrips {
		kinds[kind] = true
	}
	times, trip := watchNoun{"time", "times"}, watchNoun{"trip", "trips"}
	for _, kind := range slices.Sorted(maps.Keys(kinds)) {
		trips := st.BreakerTrips[kind]
		base := e.base("trips:"+kind, trips)
		e.next.Counters["trips:"+kind] = trips
		open := st.Breakers[kind] == memoryagent.BreakerOpen
		risen := fmt.Sprintf("%s circuit breaker is open (%s since the process started)", kind, trip.count(trips))
		if !open {
			risen = fmt.Sprintf("%s circuit breaker opened %s since the last run and is closed now (%s since the process started)", kind, times.count(trips-base), trip.count(trips))
		}
		e.event("breaker-open:"+kind, open || trips > base, risen,
			fmt.Sprintf("%s circuit breaker has opened %s since the process started", kind, times.count(trips)))
	}
}

// partial: more than watchPartialPercent of the last ten minutes' Searches
// were partially degraded, with at least watchPartialSearches of them so that
// a handful cannot page.
func (e *evaluation) partial(st *aml.AdminStatus) {
	if st.SearchesLast10m >= watchPartialSearches && st.PartialLast10m*100 > st.SearchesLast10m*watchPartialPercent {
		e.firing["partial-search"] = fmt.Sprintf("%d of %d Searches in the last 10 minutes (%.1f%%) were partially degraded, above %d%%",
			st.PartialLast10m, st.SearchesLast10m, 100*float64(st.PartialLast10m)/float64(st.SearchesLast10m), watchPartialPercent)
	}
}

// budgets: an account above watchBudgetUsed of its cap, spent and held
// together (as /ready counts), or holding more than watchBudgetHeld of it in
// reservations. A cap of zero has no share.
func (e *evaluation) budgets(accounts []memoryagent.BudgetAccount) {
	number := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	for _, a := range accounts {
		if a.Cap <= 0 {
			continue
		}
		if used := (a.Spent + a.Held) / a.Cap; used > watchBudgetUsed {
			e.firing["budget-high:"+a.Kind] = fmt.Sprintf("%s is at %.1f%% of its cap (spent %s, held %s, cap %s)", a.Kind, 100*used, number(a.Spent), number(a.Held), number(a.Cap))
		}
		if held := a.Held / a.Cap; held > watchBudgetHeld {
			e.firing["budget-held:"+a.Kind] = fmt.Sprintf("%s holds %.1f%% of its cap in reservations (held %s of %s)", a.Kind, 100*held, number(a.Held), number(a.Cap))
		}
	}
}

// backlog: the oldest work not yet consolidated is older than
// watchBacklogAge, or the largest backlog has been above the server's lag
// bound for watchLagFor of runs. Without a bound (consolidation is not
// background) there is nothing to be above.
func (e *evaluation) backlog(st *aml.AdminStatus) {
	if age := st.Queue.OldestQueued; age > watchBacklogAge {
		e.firing["backlog-age"] = fmt.Sprintf("the oldest work not yet consolidated has waited %s, above %s", age.Round(time.Second), watchBacklogAge)
	}
	if st.LagBound <= 0 || st.Queue.MaxBacklog <= st.LagBound {
		return
	}
	began, ok := e.since["backlog-lag"]
	if !ok {
		began = e.now
	}
	e.next.Since["backlog-lag"] = began
	if lasted := e.now.Sub(began); lasted >= watchLagFor {
		e.firing["backlog-lag"] = fmt.Sprintf("the largest backlog is %d characters, above the lag bound of %d, for %s", st.Queue.MaxBacklog, st.LagBound, lasted.Round(time.Second))
	}
}

func watchTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// ---- what to say ----

// The kinds of message: a new alert (or one whose first message did not go
// out), a firing alert said again, an alert that cleared.
type watchEvent string

const (
	watchNew      watchEvent = "alert"
	watchAgain    watchEvent = "repeat"
	watchResolved watchEvent = "resolved"
)

// watchMessage is a message to post, and what it is about.
type watchMessage struct {
	alert string
	event watchEvent
	text  string
}

// planAlerts decides, from the alerts that fire now (with their values), the
// ones the status could not speak for (frozen) and the alerts of the last
// run, which messages to post and what the alerts' state is if they are all
// delivered. The messages are in the order of the alerts' names. An alert that
// fires and was not firing is a new alert; one that fires and whose message
// has not gone out (the state has no time it was sent) is new again; one that
// went out is said again 30 minutes after its last message; one that stopped
// owes a resolution, until the webhook takes it.
func planAlerts(now time.Time, host string, firing map[string]string, frozen map[string]bool, prev map[string]watchAlert) (map[string]watchAlert, []watchMessage) {
	names := map[string]bool{}
	for name := range firing {
		names[name] = true
	}
	for name := range prev {
		names[name] = true
	}
	next, messages := map[string]watchAlert{}, []watchMessage{}
	say := func(name string, event watchEvent, alert watchAlert, value string) {
		messages = append(messages, watchMessage{name, event, watchText(event, name, host, watchNote(event, alert, now), value, now)})
	}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		old, wasFiring := prev[name]
		value, isFiring := firing[name]
		switch {
		case frozen[name]:
			next[name] = old
		case isFiring && !wasFiring:
			next[name] = watchAlert{Since: now, Value: watchTruncate(value, watchValueBytes)}
			say(name, watchNew, next[name], value)
		case isFiring:
			alert := watchAlert{Since: old.Since, LastSent: old.LastSent, Value: watchTruncate(value, watchValueBytes)}
			next[name] = alert
			if old.LastSent.IsZero() {
				say(name, watchNew, alert, value)
			} else if now.Sub(old.LastSent) >= watchRepeat {
				say(name, watchAgain, alert, value)
			}
		default:
			// It stopped firing. Its resolution is owed, also when the alert
			// itself never reached the webhook (the message then says so), and
			// the alert stays in the state, one entry, until the webhook has
			// taken the resolution: delivery is at-least-once, with no cap on
			// the tries and no age after which a message is given up.
			next[name] = old
			say(name, watchResolved, old, "was: "+old.Value)
		}
	}
	return next, messages
}

// watchNote is what a header says in brackets: nothing for a new alert; for
// one said again, how long it has fired; for a resolution, how long it fired
// and, if the alert itself never reached the webhook, that it did not.
func watchNote(event watchEvent, alert watchAlert, now time.Time) string {
	fired := now.Sub(alert.Since).Round(time.Second)
	switch event {
	case watchAgain:
		return fmt.Sprintf("still firing after %s", fired)
	case watchResolved:
		if alert.LastSent.IsZero() {
			return fmt.Sprintf("after %s, alert not delivered", fired)
		}
		return fmt.Sprintf("after %s", fired)
	}
	return ""
}

// watchText is the text of a message: the tag, the alert, the host and a note
// (watchNote: how long it has fired); the value that fired it; and the time in
// UTC. The value is cut to keep the message under watchMessageBytes.
func watchText(event watchEvent, alert, host, note, value string, now time.Time) string {
	verb := "ALERT"
	if event == watchResolved {
		verb = "RESOLVED"
	}
	header := fmt.Sprintf("%s %s %s on %s", watchTag, verb, alert, host)
	if note != "" {
		header += " (" + note + ")"
	}
	footer := now.UTC().Format("2006-01-02 15:04:05") + " UTC"
	value = strings.Join(strings.Fields(value), " ")
	room := watchMessageBytes - len(header) - len(footer) - 2
	return watchTruncate(header+"\n"+watchTruncate(value, max(room, 0))+"\n"+footer, watchMessageBytes)
}

// watchTruncate is s cut to at most limit bytes, whole characters, with "..."
// where it was cut.
func watchTruncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	if limit <= 3 {
		return "..."[:limit]
	}
	cut := limit - 3
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

// ---- delivery ----

// How a message ended up: delivered, or unsent (kept for the next run).
type watchOutcome int

const (
	watchDelivered watchOutcome = iota
	watchUnsent
)

// deliver posts the messages in order, or logs them when there is no webhook.
// A message that is rejected does not hold up the others, but a webhook that
// does not answer is given up on for the run, so that the run ends in
// bounded time. The error says how many messages were not delivered, and why.
func (w *watcher) deliver(ctx context.Context, messages []watchMessage) ([]watchOutcome, error) {
	if w.webhook == "" {
		return w.logUnposted(messages), nil
	}
	outcomes := make([]watchOutcome, len(messages))
	var reasons []string
	for i, m := range messages {
		err := w.post(ctx, m.text)
		if err == nil {
			w.log.Info("aml-watch: posted", "alert", m.alert, "event", m.event)
			continue
		}
		w.log.Error("aml-watch: not delivered", "alert", m.alert, "event", m.event, "reason", err.Error())
		outcomes[i] = watchUnsent
		if !slices.Contains(reasons, err.Error()) {
			reasons = append(reasons, err.Error())
		}
		var failure *webhookFailure
		if errors.As(err, &failure) && failure.unreachable {
			for j := i + 1; j < len(messages); j++ {
				outcomes[j] = watchUnsent
			}
			break
		}
	}
	if len(reasons) == 0 {
		return outcomes, nil
	}
	return outcomes, fmt.Errorf("alert delivery failed: %d of %d messages not delivered (%s); the next run sends them again",
		countOutcome(outcomes, watchUnsent), len(messages), strings.Join(reasons, "; "))
}

func countOutcome(outcomes []watchOutcome, want watchOutcome) int {
	n := 0
	for _, outcome := range outcomes {
		if outcome == want {
			n++
		}
	}
	return n
}

// logUnposted logs the messages there is no webhook to post, and keeps them
// all unsent, resolutions too: once there is a webhook they are posted (a
// resolution of an alert that never went out says so).
func (w *watcher) logUnposted(messages []watchMessage) []watchOutcome {
	outcomes := make([]watchOutcome, len(messages))
	for i, m := range messages {
		w.log.Warn("aml-watch: AML_ALERT_WEBHOOK is empty: not posting", "alert", m.alert, "event", m.event, "message", m.text)
		outcomes[i] = watchUnsent
	}
	return outcomes
}

// settleAlerts updates the planned state of the alerts with how their
// messages ended up: a delivered one was sent now, a delivered resolution
// ends the alert, and what could not be posted stays as planned.
func settleAlerts(alerts map[string]watchAlert, messages []watchMessage, outcomes []watchOutcome, now time.Time) {
	for i, m := range messages {
		if outcomes[i] != watchDelivered {
			continue
		}
		if m.event == watchResolved {
			delete(alerts, m.alert)
			continue
		}
		alert := alerts[m.alert]
		alert.LastSent = now
		alerts[m.alert] = alert
	}
}

// webhookFailure is why a post failed. Its text never contains the URL or the
// webhook's own words. unreachable: no answer came back.
type webhookFailure struct {
	reason      string
	unreachable bool
}

func (f *webhookFailure) Error() string { return f.reason }

// post sends text to the webhook as the payload of its kind: to a Feishu or
// WeCom webhook as it is, to a Telegram bot's sendMessage without the query,
// whose chat goes in the payload.
func (w *watcher) post(ctx context.Context, text string) error {
	target, chat := w.webhook, ""
	if w.kind == "telegram" {
		u, err := url.Parse(w.webhook)
		if err == nil {
			target, chat, err = telegramTarget(u)
		}
		if err != nil {
			return &webhookFailure{reason: "the webhook request could not be made"}
		}
	}
	body, err := watchPayload(w.kind, chat, text)
	if err != nil {
		return &webhookFailure{reason: "the message could not be encoded"}
	}
	// The errors of net/http name the URL, which is the secret: none of them
	// is passed on.
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return &webhookFailure{reason: "the webhook request could not be made"}
	}
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	response, err := w.hooks.Do(request)
	if err != nil {
		return watchTransportFailure(err)
	}
	defer response.Body.Close()
	answer, _ := io.ReadAll(io.LimitReader(response.Body, watchAnswerLimit))
	if w.kind == "telegram" {
		if reason, rejected := telegramRejection(response.StatusCode, answer); rejected {
			return &webhookFailure{reason: reason}
		}
		return nil
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return &webhookFailure{reason: fmt.Sprintf("the webhook answered HTTP %d", response.StatusCode)}
	}
	if code, rejected := watchRejection(answer); rejected {
		return &webhookFailure{reason: fmt.Sprintf("the webhook rejected the message (code %d)", code)}
	}
	return nil
}

// watchTransportFailure says what kind of failure err is, and nothing else.
func watchTransportFailure(err error) *webhookFailure {
	var timeout interface{ Timeout() bool }
	switch {
	case errors.Is(err, context.Canceled):
		return &webhookFailure{reason: "the post was interrupted", unreachable: true}
	case errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout():
		return &webhookFailure{reason: "the webhook did not answer in time", unreachable: true}
	}
	return &webhookFailure{reason: "the webhook could not be reached", unreachable: true}
}

// watchRejection is the code of an answer, however it is spelled, that says
// the webhook did not take the message though it answered 2xx: Feishu's code
// (or StatusCode) and WeCom's errcode, when not zero. Anything else, an
// answer that is not JSON included, is taken as success.
func watchRejection(answer []byte) (int, bool) {
	var body struct {
		Code       *int `json:"code"`
		StatusCode *int `json:"StatusCode"`
		ErrCode    *int `json:"errcode"`
	}
	if json.Unmarshal(answer, &body) != nil {
		return 0, false
	}
	for _, code := range []*int{body.Code, body.StatusCode, body.ErrCode} {
		if code != nil && *code != 0 {
			return *code, true
		}
	}
	return 0, false
}

// telegramRejection is why a Telegram bot did not take a message, from the
// HTTP status of its answer and the answer: it took it only when the status is
// 2xx and the answer says "ok":true. The reason says the status and, when
// there is one, Telegram's numeric error_code; never its description, which
// can repeat what it was sent. A 429 is a rejection like any other: the next
// run sends the message again, and its retry_after is not waited for.
func telegramRejection(status int, answer []byte) (string, bool) {
	var body struct {
		OK        bool `json:"ok"`
		ErrorCode *int `json:"error_code"`
	}
	parsed := json.Unmarshal(answer, &body) == nil
	success := status >= 200 && status <= 299
	switch {
	case success && parsed && body.OK:
		return "", false
	case parsed && body.ErrorCode != nil:
		return fmt.Sprintf("the webhook answered HTTP %d with error_code %d", status, *body.ErrorCode), true
	case success:
		return fmt.Sprintf(`the webhook answered HTTP %d without "ok":true`, status), true
	}
	return fmt.Sprintf("the webhook answered HTTP %d", status), true
}

// The payloads of the three kinds of webhook: Feishu's custom bot and WeCom's
// group robot, both a plain text message, and a Telegram bot's sendMessage,
// plain text too: without a parse_mode nothing in the text is read as markup,
// and the links it holds get no preview.
type feishuPayload struct {
	MsgType string `json:"msg_type"`
	Content struct {
		Text string `json:"text"`
	} `json:"content"`
}

type wecomPayload struct {
	MsgType string `json:"msgtype"`
	Text    struct {
		Content string `json:"content"`
	} `json:"text"`
}

type telegramPayload struct {
	ChatID                string `json:"chat_id"`
	Text                  string `json:"text"`
	DisableWebPagePreview bool   `json:"disable_web_page_preview"`
}

// watchPayload is the JSON body that posts text to a webhook of kind; chat is
// a Telegram bot's chat (telegramTarget), as it was given.
func watchPayload(kind, chat, text string) ([]byte, error) {
	var payload any
	switch kind {
	case "feishu":
		p := feishuPayload{MsgType: "text"}
		p.Content.Text = text
		payload = p
	case "wecom":
		p := wecomPayload{MsgType: "text"}
		p.Text.Content = text
		payload = p
	case "telegram":
		payload = telegramPayload{ChatID: chat, Text: text, DisableWebPagePreview: true}
	default:
		return nil, fmt.Errorf("unknown kind %q", kind)
	}
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		return nil, err
	}
	return bytes.TrimRight(body.Bytes(), "\n"), nil
}
