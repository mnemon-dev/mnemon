package aml

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/pprof"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
)

// The admin handler is the operator's side of the server: metrics, readiness,
// a status the alert watcher reads, the store overview the console reads and
// the profiler. Without an admin token (ServerConfig.AdminToken) it has no
// authentication, so the command serves it on a loopback listener only; with
// one, every request must carry the token and the command may serve it on a
// private address (AdminHandler).

const (
	// minFreeDisk is the free space the data root's disk must have to be
	// ready: 1 GiB.
	minFreeDisk = 1 << 30
	// readyBudgetCeiling is the share of its cap a paid account may have spent
	// and held before the server is no longer ready: the last 5% is headroom.
	readyBudgetCeiling = 0.95
	// adminReadWait bounds the reads /ready and /admin/status make of the
	// budget file.
	adminReadWait = 5 * time.Second
)

// AdminStatus is what /admin/status answers, as JSON. The alert watcher
// (aml-watch) reads it, so its field names are a contract: fields may be
// added, never renamed or repurposed. Counters count from the process start
// (StartedAt); users appear only as counts.
//
//	startedAt        when the server was made (RFC 3339): differs after every restart
//	adds, searches   Adds and Searches answered 200
//	degraded         Searches answered by the raw ranking because the memory read failed
//	partial          Searches answered by a read in which a stage fell back
//	dropped          drops of records from the consolidation layer (a batch, or half of one,
//	                 that failed again after being split); the "consolidation dropped" lines
//	linkFallbacks    batches linked without JEV because JEV failed
//	addOutran        Adds that waited out their window for consolidation and answered anyway
//	partialLast10m   partial Searches in the last ten one-minute buckets (the current minute
//	                 and the nine before it)
//	searchesLast10m  Searches answered in those buckets
//	queue            the consolidation scheduler (zero unless consolidation is background):
//	                 queued, running, backingOff and held users; oldestQueued, in nanoseconds,
//	                 the age of the oldest work not yet consolidated; maxBacklog, the largest
//	                 user backlog in characters
//	lagBound         the backlog, in characters, above which an Add waits for consolidation: the
//	                 unit of queue.maxBacklog (zero unless consolidation is background)
//	breakers         the state of the circuit breaker of each provider kind: "closed", "open"
//	                 (refusing calls) or "half-open" (probing); the worst of a kind's breakers
//	breakerTrips     by provider kind, how many times a circuit breaker of the kind has opened
//	                 (a failed probe of a half-open one included): unlike breakers it only grows,
//	                 so a breaker that opened and closed between two readings still moves it
//	rejectedByKind   by provider kind, the requests the provider rejected with 401, 402 or 403 (a
//	                 wrong or revoked key, an empty balance): it only grows, and has a key, 0 at
//	                 first, for every kind the models have
//	budgets          each paid account that has been used: kind, cap (null when unlimited), spent,
//	                 held, source ("config", or "settings" for a cap the settings file sets) and
//	                 unlimited; always an array, empty when none has been used and when the budget
//	                 file cannot be read (/ready says so)
//	openStores       user stores open now
//	version          the product version of the build (ServerConfig.Version)
//	providerKeys     by key kind the configuration uses (deepseek, jev): where its key comes from,
//	                 "account" (the key file), "host" (the environment) or "none", and the key's
//	                 fingerprint, the first 12 hex digits of its SHA-256 ("" with none); never a key
type AdminStatus struct {
	StartedAt     time.Time `json:"startedAt"`
	Adds          int64     `json:"adds"`
	Searches      int64     `json:"searches"`
	Degraded      int64     `json:"degraded"`
	Partial       int64     `json:"partial"`
	Dropped       int64     `json:"dropped"`
	LinkFallbacks int64     `json:"linkFallbacks"`
	AddOutran     int64     `json:"addOutran"`
	// The one-shot links: records asked, windows JEV refused, jobs whose step
	// stopped on an error; and the records view.loopLinks pooled.
	OneShotAsked    int64                            `json:"oneShotAsked"`
	OneShotRefused  int64                            `json:"oneShotRefused"`
	OneShotErrors   int64                            `json:"oneShotErrors"`
	LoopLinked      int64                            `json:"loopLinked"`
	PartialLast10m  int64                            `json:"partialLast10m"`
	SearchesLast10m int64                            `json:"searchesLast10m"`
	Queue           SchedulerStatus                  `json:"queue"`
	LagBound        int64                            `json:"lagBound"`
	Breakers        map[string]string                `json:"breakers"`
	BreakerTrips    map[string]int64                 `json:"breakerTrips"`
	RejectedByKind  map[string]int64                 `json:"rejectedByKind"`
	Budgets         []memoryagent.BudgetAccount      `json:"budgets"`
	OpenStores      int                              `json:"openStores"`
	Version         string                           `json:"version"`
	ProviderKeys    map[string]memoryagent.KeyStatus `json:"providerKeys"`
}

// AdminHandler serves the operator's endpoints: GET /metrics (Prometheus),
// GET /ready, GET /admin/status, GET /admin/v1/stores, PUT and DELETE
// /admin/v1/provider-keys (byok.go) and /admin/v1/budgets (budgets.go), and
// /debug/pprof/. With
// an admin token every request, an unknown path included, must carry it as
// `Authorization: Bearer <token>` (requireAdminToken); without one the handler
// has no authentication, and must be served on a loopback address only. Never
// serve it on the public listener, whose Handler serves none of these.
// Profiles taken over several seconds (/debug/pprof/profile,
// /debug/pprof/trace) need a server write timeout longer than they run.
func (s *Server) AdminHandler() http.Handler { return s.admin }

func (s *Server) adminRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", s.metrics.handler(s.log))
	mux.HandleFunc("GET /ready", s.serveReady)
	mux.HandleFunc("GET /admin/status", s.serveStatus)
	mux.HandleFunc("GET /admin/v1/stores", s.serveStores)
	mux.HandleFunc("PUT /admin/v1/provider-keys", s.replaceProviderKeys)
	mux.HandleFunc("DELETE /admin/v1/provider-keys", s.revertProviderKeys)
	mux.HandleFunc("PUT /admin/v1/budgets", s.setBudgetCaps)
	mux.HandleFunc("DELETE /admin/v1/budgets", s.revertBudgetCaps)
	// The profiler's handlers are registered here by name. Importing
	// net/http/pprof also registers them on http.DefaultServeMux, a side
	// effect that is inert: no server of the process serves the default mux.
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
	return s.requireAdminToken(mux)
}

// requireAdminToken answers 401 to every request of next that does not carry
// the admin token as `Authorization: Bearer <token>` (the scheme in any
// case); no other header or scheme stands in for it. A refusal is counted
// (mnemon_aml_admin_auth_failures_total) and not logged: neither the token,
// nor what was sent, nor anything made of them is ever logged or answered.
// The SHA-256 digests of the presented and the configured token are compared
// in constant time, as the data plane compares its key, so neither the token
// nor its length leaks through timing. Without an admin token it is next
// itself.
func (s *Server) requireAdminToken(next http.Handler) http.Handler {
	return requireBearer(s.adminKey, func(w http.ResponseWriter) {
		s.metrics.adminAuthFailures.Inc()
		w.Header().Set("WWW-Authenticate", `Bearer realm="mnemon-admin"`)
		writeError(w, http.StatusUnauthorized, "missing or invalid admin token")
	}, next)
}

// requireBearer is the bearer token rule of the admin listener and of the
// inspect service: every request of next must carry the token whose SHA-256
// digest is key as `Authorization: Bearer <token>` (the scheme in any case),
// and refuse answers one that does not, before next sees it. No other header
// or scheme stands in for it. The digests are compared in constant time, so
// neither the token nor its length leaks through timing; refuse is given
// nothing of what was sent. With a nil key it is next itself.
func requireBearer(key *[sha256.Size]byte, refuse func(http.ResponseWriter), next http.Handler) http.Handler {
	if key == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !bearerAuthorized(r, key) {
			refuse(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bearerAuthorized reports whether r carries the token whose digest is key
// as a Bearer token.
func bearerAuthorized(r *http.Request, key *[sha256.Size]byte) bool {
	scheme, token, found := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return false
	}
	digest := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return subtle.ConstantTimeCompare(digest[:], key[:]) == 1
}

func (s *Server) serveStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), adminReadWait)
	defer cancel()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, s.status(ctx))
}

// status is the server's AdminStatus.
func (s *Server) status(ctx context.Context) AdminStatus {
	m := s.metrics
	searches, partial := m.recent.totals()
	// An unreadable budget file shows as no accounts here (an empty array,
	// not null); /ready says why.
	accounts, err := s.Accounts(ctx)
	if err != nil || accounts == nil {
		accounts = []memoryagent.BudgetAccount{}
	}
	for i := range accounts {
		accounts[i].Source = s.capSource(accounts[i].Kind)
	}
	return AdminStatus{
		StartedAt:       s.started,
		Adds:            m.adds.Load(),
		Searches:        m.searches.Load(),
		Degraded:        m.degraded.Load(),
		Partial:         m.partial.Load(),
		Dropped:         m.batches[batchDropped].Load(),
		LinkFallbacks:   m.linkFallbacks.Load(),
		AddOutran:       m.addOutran.Load(),
		OneShotAsked:    m.oneShotAsked.Load(),
		OneShotRefused:  m.oneShotRefused.Load(),
		OneShotErrors:   m.oneShotErrors.Load(),
		LoopLinked:      m.loopLinked.Load(),
		PartialLast10m:  partial,
		SearchesLast10m: searches,
		Queue:           s.queueStatus(),
		LagBound:        s.consolidationLagBound(),
		Breakers:        s.breakerStates(),
		BreakerTrips:    s.models.BreakerTrips(),
		RejectedByKind:  s.models.Rejections(),
		Budgets:         accounts,
		OpenStores:      s.users.open(),
		Version:         s.config.Version,
		ProviderKeys:    s.keys.Status(),
	}
}

// consolidationLagBound is the backlog, in characters, above which an Add
// waits for consolidation; zero unless consolidation is background, where
// no Add waits.
func (s *Server) consolidationLagBound() int64 {
	if s.mode != ConsolidationBackground {
		return 0
	}
	return s.lagBound
}

// queueStatus is the scheduler's status; zero without a scheduler.
func (s *Server) queueStatus() SchedulerStatus {
	if s.sched == nil {
		return SchedulerStatus{}
	}
	return s.sched.Status()
}

// Accounts reads the paid accounts calls have reserved on; none without a
// budget file. Its error says the budget file does not answer, which
// systemd's watchdog (aml-serve) checks.
func (s *Server) Accounts(ctx context.Context) ([]memoryagent.BudgetAccount, error) {
	if s.budget == nil {
		return []memoryagent.BudgetAccount{}, nil
	}
	return s.budget.Accounts(ctx)
}

// breakerStates are the circuit breaker states of the providers by kind (the
// planner and the consolidator can both be DeepSeek): a kind is as bad as the
// worst of its breakers.
func (s *Server) breakerStates() map[string]string {
	states := map[string]string{}
	for _, breaker := range s.models.Breakers() {
		states[breaker.Kind] = worse(states[breaker.Kind], breaker.State)
	}
	return states
}

// worse is the worse of two breaker states, "" being no state at all: open,
// then half-open, then closed.
func worse(a, b string) string {
	rank := func(state string) int {
		switch state {
		case memoryagent.BreakerOpen:
			return 3
		case memoryagent.BreakerHalfOpen:
			return 2
		case memoryagent.BreakerClosed:
			return 1
		}
		return 0
	}
	if rank(b) > rank(a) {
		return b
	}
	return a
}

// readiness is the body of /ready.
type readiness struct {
	Ready   bool     `json:"ready"`
	Reasons []string `json:"reasons,omitempty"`
}

// serveReady answers 200 {"ready":true} when the server can take work, else
// 503 with the reasons it cannot: the data root is not writable, the disk is
// short of free space, a provider's breaker is open or a paid account is
// within 5% of its cap.
func (s *Server) serveReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), adminReadWait)
	defer cancel()
	w.Header().Set("Cache-Control", "no-store")
	reasons := s.notReadyReasons(ctx)
	if len(reasons) == 0 {
		writeJSON(w, http.StatusOK, readiness{Ready: true})
		return
	}
	writeJSON(w, http.StatusServiceUnavailable, readiness{Reasons: reasons})
}

// notReadyReasons lists why the server is not ready, empty if it is: the
// storage, the open breakers (by kind) and the accounts nearly spent, in that
// order.
func (s *Server) notReadyReasons(ctx context.Context) []string {
	reasons := append(s.storageReasons(), breakerReasons(s.breakerStates())...)
	accounts, err := s.Accounts(ctx)
	if err != nil {
		return append(reasons, "budget unreadable: "+err.Error())
	}
	return append(reasons, overBudget(accounts)...)
}

// breakerReasons name, by kind, the providers whose breaker refuses calls. A
// half-open breaker is letting calls probe the provider, so it is not one.
func breakerReasons(states map[string]string) []string {
	reasons := []string{}
	for _, kind := range slices.Sorted(maps.Keys(states)) {
		if states[kind] == memoryagent.BreakerOpen {
			reasons = append(reasons, "breaker open: "+kind)
		}
	}
	return reasons
}

// storageReasons are the reasons the data root cannot take writes: it is not
// writable (a file is created in it and removed) or its disk has less than
// minFreeDisk free.
func (s *Server) storageReasons() []string {
	reasons := []string{}
	if err := probeWritable(s.config.DataRoot); err != nil {
		reasons = append(reasons, "data root is not writable: "+err.Error())
	}
	free, err := s.freeDisk(s.config.DataRoot)
	switch {
	case err != nil:
		reasons = append(reasons, "free disk unreadable: "+err.Error())
	case free < minFreeDisk:
		reasons = append(reasons, fmt.Sprintf("free disk %d MiB is below %d GiB", free>>20, minFreeDisk>>30))
	}
	return reasons
}

// probeWritable creates a file in dir and removes it.
func probeWritable(dir string) error {
	probe, err := os.CreateTemp(dir, ".ready-")
	if err != nil {
		return err
	}
	return errors.Join(probe.Close(), os.Remove(probe.Name()))
}

// overBudget lists the accounts with more than readyBudgetCeiling of their cap
// spent or held. A cap of zero has no share to measure.
func overBudget(accounts []memoryagent.BudgetAccount) []string {
	reasons := []string{}
	for _, account := range accounts {
		// An unlimited cap (R100) has no share to measure either.
		if account.Cap <= 0 || account.Unlimited {
			continue
		}
		if used := (account.Spent + account.Held) / account.Cap; used > readyBudgetCeiling {
			reasons = append(reasons, fmt.Sprintf("budget %s is at %.1f%% of its cap", account.Kind, 100*used))
		}
	}
	return reasons
}
