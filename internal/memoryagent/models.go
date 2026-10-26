package memoryagent

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
)

type Usage struct {
	InputTokens  int     `json:"inputTokens"`
	OutputTokens int     `json:"outputTokens"`
	USD          float64 `json:"usd,omitempty"`
	CNY          float64 `json:"cny,omitempty"`
	// Cached counts the calls the model cache answered (Config.ModelCache):
	// their tokens and prices are the recorded reply's, reported as the live
	// call reported them, but nothing was sent, reserved or spent.
	Cached int `json:"cached,omitempty"`
}

type Question struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria,omitempty"`
}

type Completer interface {
	Complete(context.Context, string, string) (string, Usage, error)
}

// CompleterWithMax is a Completer whose calls set their own output limit, as
// the reference system's recall completer takes one per call. The simple
// strategy uses it when the planner has it.
type CompleterWithMax interface {
	CompleteWith(ctx context.Context, system, prompt string, maxTokens int) (string, Usage, error)
}

type JSONCompleter interface {
	CompleteJSON(context.Context, string, string) (string, Usage, error)
}

type Decider interface {
	Decide(context.Context, any, map[string]Question) (map[string]float64, Usage, error)
}

type Models struct {
	Planner Completer
	Judge   Decider
	// Consolidator folds timelines consolidation batches; nil uses Planner.
	Consolidator Completer
}

// ModelObserver is shown every attempt of the providers' model calls
// (Models.Observe), for metrics. ModelAttempt runs on the calling goroutine,
// holding no lock; it must be quick and safe for concurrent use.
type ModelObserver interface {
	ModelAttempt(AttemptEvent)
}

// AttemptEvent is one attempt of a model call, or one its policies stopped
// before it was sent. Kind is the provider's kind and Class the call's class
// (CallClass.String). Outcome is "ok"; "throttled", refused with HTTP 429,
// retried after its Retry-After, patiently without one, or not; "invalid", a
// 200 reply that did not parse or failed validation, retried once or not;
// "rejected", refused with HTTP 401, 402 or 403 (the credential or the
// balance), not retried; "retryable", another failure a retry may not meet,
// retried or not; "failed"; "breaker-open"; "bulkhead-full"; or "budget",
// refused by the spending cap, its background reserve or the provider's
// retry budget. Attempt is its number in the call
// (1 for the first), Duration how long the caller waited for it, Status the
// HTTP status answered (0 without an answer) and Retried whether it follows
// an earlier attempt of the call.
type AttemptEvent struct {
	Kind, Class, Outcome string
	Attempt              int
	Duration             time.Duration
	Status               int
	Retried              bool
}

// The outcomes of an attempt (AttemptEvent.Outcome), which metrics label
// attempts by.
const (
	OutcomeOK           = "ok"
	OutcomeThrottled    = "throttled"
	OutcomeInvalid      = "invalid"
	OutcomeRejected     = "rejected"
	OutcomeRetryable    = "retryable"
	OutcomeFailed       = "failed"
	OutcomeBreakerOpen  = "breaker-open"
	OutcomeBulkheadFull = "bulkhead-full"
	OutcomeBudget       = "budget"
)

// Observe shows the attempts of every provider among the models to o; nil
// stops showing them.
func (m Models) Observe(o ModelObserver) {
	for _, p := range m.providers() {
		p.observer.Store(&observerRef{observer: o})
	}
}

// Close closes every provider among the models once, with no deadline
// (CloseWithin): after their requests in flight end, abandoned ones
// included. Close them before the budget and the model cache they share.
func (m Models) Close() error { return m.CloseWithin(context.Background()) }

// CloseWithin closes every provider among the models once, by the deadline
// of ctx (Provider.CloseWithin): all of them refuse calls from the start,
// then each waits for its requests in flight until the deadline and cuts
// what still runs at it. It returns what the providers cut, in one line
// (their errors joined by "; "), nil when nothing was cut. Close them before
// the budget and the model cache they share.
func (m Models) CloseWithin(ctx context.Context) error {
	providers := m.providers()
	for _, p := range providers {
		p.refuse()
	}
	var cut []string
	for _, p := range providers {
		if err := p.drain(ctx); err != nil {
			cut = append(cut, err.Error())
		}
	}
	if len(cut) == 0 {
		return nil
	}
	return errors.New(strings.Join(cut, "; "))
}

// providers are the distinct providers among the models' roles.
func (m Models) providers() []*Provider {
	out := []*Provider{}
	for _, role := range []any{m.Planner, m.Judge, m.Consolidator} {
		if p, ok := role.(*Provider); ok && p != nil && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

func (u *Usage) add(other Usage) {
	u.InputTokens += other.InputTokens
	u.OutputTokens += other.OutputTokens
	u.USD += other.USD
	u.CNY += other.CNY
	u.Cached += other.Cached
}
