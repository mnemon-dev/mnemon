package aml

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sync/semaphore"
)

// The layers around a route's handler: the id every request carries, and the
// gate that admits only so many Adds and Searches at once.

const (
	// requestIDHeader carries a request's id in and out.
	requestIDHeader = "X-Request-Id"
	// maxRequestIDLength bounds an id a client gives.
	maxRequestIDLength = 128
	// admissionWait is how long a request waits for a slot before it is shed;
	// shedRetryAfter is the seconds a shed request is told to wait. The
	// platform retries a 503 for both Add and Search, so shedding is safe, but
	// it must stay rare: a slot normally frees well within the wait.
	admissionWait  = 30 * time.Second
	shedRetryAfter = "5"
)

type requestIDKey struct{}

// requestID is the id of the request ctx belongs to; "" outside a request.
func requestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

func withRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

type requestUserKey struct{}

// requestUser holds the user a request names, by the 8-hex prefix of the
// user's store key (short), once its handler knows it (nameUser): the lines
// the request logs from then on carry it as user, its request line among
// them (contextHandler; spec §5, the final review's Minor 3). It never holds
// a user id or a key. The handler sets it and a line may read it on another
// goroutine.
//
// roles holds the roles of the messages an Add stores, as "user=3,assistant=2"
// (nameRoles): its request line carries them, for an audit of what arrived.
// noEvidence is set when a Search's read found nothing the reply needs
// (nameNoEvidence): its request line carries evidence=none.
type requestUser struct {
	prefix, roles atomic.Pointer[string]
	noEvidence    atomic.Bool
}

// withRequestUser is ctx with an empty holder of the user its request will
// name.
func withRequestUser(ctx context.Context) context.Context {
	return context.WithValue(ctx, requestUserKey{}, &requestUser{})
}

// nameUser records that ctx's request is for the user whose store key is
// key (userKey): its lines carry the key's 8-hex prefix from now on. Outside
// a request it does nothing.
func nameUser(ctx context.Context, key string) {
	if holder, ok := ctx.Value(requestUserKey{}).(*requestUser); ok {
		prefix := short(key)
		holder.prefix.Store(&prefix)
	}
}

// nameRoles records the roles of the messages ctx's Add stores, by role
// bucket: its lines carry them as roles from now on. Outside a request it
// does nothing.
func nameRoles(ctx context.Context, byRole map[string]int) {
	holder, ok := ctx.Value(requestUserKey{}).(*requestUser)
	if !ok || len(byRole) == 0 {
		return
	}
	parts := make([]string, 0, len(byRole))
	for _, role := range slices.Sorted(maps.Keys(byRole)) {
		parts = append(parts, role+"="+strconv.Itoa(byRole[role]))
	}
	roles := strings.Join(parts, ",")
	holder.roles.Store(&roles)
}

// nameNoEvidence records that ctx's Search found nothing the reply needs:
// its lines carry evidence=none from now on. Outside a request it does
// nothing.
func nameNoEvidence(ctx context.Context) {
	if holder, ok := ctx.Value(requestUserKey{}).(*requestUser); ok {
		holder.noEvidence.Store(true)
	}
}

// requestNoEvidence is what nameNoEvidence recorded for ctx's request.
func requestNoEvidence(ctx context.Context) bool {
	holder, ok := ctx.Value(requestUserKey{}).(*requestUser)
	return ok && holder.noEvidence.Load()
}

// requestRoles is what nameRoles recorded for ctx's request, "" when none.
func requestRoles(ctx context.Context) string {
	if holder, ok := ctx.Value(requestUserKey{}).(*requestUser); ok {
		if roles := holder.roles.Load(); roles != nil {
			return *roles
		}
	}
	return ""
}

// userPrefix is the prefix of the user ctx's request named, "" when it
// named none or ctx is not a request's.
func userPrefix(ctx context.Context) string {
	if holder, ok := ctx.Value(requestUserKey{}).(*requestUser); ok {
		if prefix := holder.prefix.Load(); prefix != nil {
			return *prefix
		}
	}
	return ""
}

// usableRequestID reports whether a client's id may be used: 1 to 128
// printable ASCII characters (0x21 to 0x7e), which a header and a log line
// carry without being split.
func usableRequestID(id string) bool {
	if id == "" || len(id) > maxRequestIDLength {
		return false
	}
	for i := 0; i < len(id); i++ {
		if id[i] < 0x21 || id[i] > 0x7e {
			return false
		}
	}
	return true
}

// newRequestID is 16 random bytes in hex.
func newRequestID() string {
	var raw [16]byte
	_, _ = rand.Read(raw[:]) // never fails: it ends the process if the system has no randomness
	return hex.EncodeToString(raw[:])
}

// identified gives a request its id: the X-Request-Id it came with when that
// is usable, else a new one. The id is echoed in the response header and
// travels in the request's context to every line the request logs
// (contextHandler), as does the holder of the user it names (requestUser).
func identified(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if !usableRequestID(id) {
			id = newRequestID()
		}
		w.Header().Set(requestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(withRequestUser(withRequestID(r.Context(), id))))
	})
}

// contextHandler adds the request_id attribute to the records whose context
// belongs to a request (see identified), and the user attribute, the user's
// 8-hex prefix, once the request has named one (nameUser); and it wraps what
// it derives, so the loggers made with With and WithGroup keep adding them.
type contextHandler struct{ slog.Handler }

func (h contextHandler) Handle(ctx context.Context, record slog.Record) error {
	id, user, roles, none := requestID(ctx), userPrefix(ctx), requestRoles(ctx), requestNoEvidence(ctx)
	if id != "" || user != "" || roles != "" || none {
		record = record.Clone()
	}
	if id != "" {
		record.AddAttrs(slog.String("request_id", id))
	}
	if user != "" {
		record.AddAttrs(slog.String("user", user))
	}
	if roles != "" {
		record.AddAttrs(slog.String("roles", roles))
	}
	if none {
		record.AddAttrs(slog.String("evidence", "none"))
	}
	return h.Handler.Handle(ctx, record)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{h.Handler.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{h.Handler.WithGroup(name)}
}

// gate admits at most limit requests of a route at once, first come, first
// served.
type gate struct {
	limit int
	slots *semaphore.Weighted
}

// newGate is the gate for limit concurrent requests; with no limit (0), nil,
// which admits every request.
func newGate(limit int) *gate {
	if limit <= 0 {
		return nil
	}
	return &gate{limit: limit, slots: semaphore.NewWeighted(int64(limit))}
}

// guarded is a route's handler chain, outermost first: the request is counted
// and timed (an answer of any kind: 401 and 503 too), the API key is checked,
// a slot of the route's gate is taken (an unauthenticated request never holds
// one), and a panic is contained.
func (s *Server) guarded(operation string, g *gate, next http.HandlerFunc) http.Handler {
	return s.metrics.instrument(operation, s.authorized(s.admitted(operation, g, s.recovered(operation, next))))
}

// admitted runs next once g has a free slot for the request, and frees the
// slot when next returns. A request that finds none waits for one, for at
// most admitWait: then it is shed with 503 and a Retry-After, before its body
// is read or its store opened. A request whose context ends while it waits
// (its client left, or CancelRequests) answers 503 too. A nil g admits all.
func (s *Server) admitted(operation string, g *gate, next http.HandlerFunc) http.HandlerFunc {
	if g == nil {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		wait, stop := context.WithTimeout(ctx, s.admitWait)
		err := g.slots.Acquire(wait, 1)
		stop()
		switch {
		case err == nil:
			defer g.slots.Release(1)
			next(w, r)
		case ctx.Err() != nil:
			writeError(w, http.StatusServiceUnavailable, "request canceled")
		default:
			s.metrics.shed.WithLabelValues(operation).Inc()
			s.logf(ctx, slog.LevelWarn, "%s shed: no free slot within %s (limit %d)", operation, s.admitWait, g.limit)
			w.Header().Set("Retry-After", shedRetryAfter)
			writeError(w, http.StatusServiceUnavailable, "busy; retry")
		}
	}
}
