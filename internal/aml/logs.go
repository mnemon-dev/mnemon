package aml

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// The lines the server logged, in memory, for the console: GET
// /admin/v1/logs (every line, filtered and paged) and GET /admin/v1/events
// (WARN and ERROR only). journald and the log files (logfiles.go) keep every
// line; the memory keeps the latest.
const (
	// linesKept is how many lines of every level the memory keeps, and
	// eventsKept how many WARN and ERROR lines it keeps besides, which a
	// burst of request lines cannot push out.
	linesKept  = 10000
	eventsKept = 500
	// messageKept bounds a message in memory (a panic's stack, say); journald
	// and the log files keep it whole.
	messageKept = 4096
	// linesPage is the most lines one GET /admin/v1/logs answers; a query's q
	// has at most queryTerms terms and queryChars characters.
	linesPage  = 200
	queryTerms = 8
	queryChars = 200
)

// Event is a line the server logged, as GET /admin/v1/logs and GET
// /admin/v1/events show it: its number since the start (seq), its time, level
// and message; the user (8-hex store prefix), request id and an Add's roles
// it carried; and a request line's status and milliseconds (ms is left out
// at 0). Lines never hold a key or a record's content, and neither does an
// event.
type Event struct {
	Seq       int64     `json:"seq"`
	Time      time.Time `json:"time"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	User      string    `json:"user,omitempty"`
	RequestID string    `json:"requestId,omitempty"`
	Roles     string    `json:"roles,omitempty"`
	Status    int       `json:"status,omitempty"`
	Ms        int64     `json:"ms,omitempty"`
	// Evidence is "none" on the line of a Search that found nothing the
	// reply needs.
	Evidence string `json:"evidence,omitempty"`
}

// ring keeps the latest size events, and counts every one since the start.
type ring struct {
	size   int
	events []Event
	next   int
	total  int64
}

func (r *ring) add(event Event) {
	if len(r.events) < r.size {
		r.events = append(r.events, event)
	} else {
		r.events[r.next] = event
	}
	r.next = (r.next + 1) % r.size
	r.total++
}

// newestFirst is a copy of the events r keeps, the newest first.
func (r *ring) newestFirst() []Event {
	n := len(r.events)
	out := make([]Event, n)
	for i := range out {
		out[i] = r.events[((r.next-1-i)%n+n)%n]
	}
	return out
}

// lineMemory keeps the latest linesKept lines, numbered from 1 in the order
// they came, and the latest eventsKept WARN and ERROR lines besides.
type lineMemory struct {
	mu       sync.Mutex
	lines    ring
	warnings ring
}

func newLineMemory() *lineMemory {
	return &lineMemory{lines: ring{size: linesKept}, warnings: ring{size: eventsKept}}
}

func (m *lineMemory) add(level slog.Level, event Event) {
	if len(event.Message) > messageKept {
		cut := messageKept
		for cut > 0 && !utf8.RuneStart(event.Message[cut]) {
			cut--
		}
		event.Message = event.Message[:cut] + "…"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	event.Seq = m.lines.total + 1
	m.lines.add(event)
	if level >= slog.LevelWarn {
		m.warnings.add(event)
	}
}

// kept is a copy of the lines a query at level reads, the newest first: every
// line below WARN, else the warnings; the most that memory keeps; and the
// lines it has seen since the start. The copy is taken under the lock and
// read outside it, so a query never holds up a line being logged.
func (m *lineMemory) kept(level slog.Level) (lines []Event, size int, total int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	from := &m.lines
	if level >= slog.LevelWarn {
		from = &m.warnings
	}
	return from.newestFirst(), from.size, from.total
}

// linesHandler keeps every line in memory and passes it on to its handler.
// It sits under contextHandler, so the lines it sees carry the request's id,
// user and roles.
type linesHandler struct {
	slog.Handler
	memory *lineMemory
}

func (h linesHandler) Handle(ctx context.Context, record slog.Record) error {
	event := Event{Time: record.Time.UTC(), Level: record.Level.String(), Message: record.Message}
	record.Attrs(func(attr slog.Attr) bool {
		switch attr.Key {
		case "user":
			event.User = attr.Value.String()
		case "request_id":
			event.RequestID = attr.Value.String()
		case "roles":
			event.Roles = attr.Value.String()
		case "evidence":
			event.Evidence = attr.Value.String()
		case "status":
			if attr.Value.Kind() == slog.KindInt64 {
				event.Status = int(attr.Value.Int64())
			}
		case "ms":
			if attr.Value.Kind() == slog.KindInt64 {
				event.Ms = attr.Value.Int64()
			}
		}
		return true
	})
	h.memory.add(record.Level, event)
	return h.Handler.Handle(ctx, record)
}

func (h linesHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return linesHandler{h.Handler.WithAttrs(attrs), h.memory}
}

func (h linesHandler) WithGroup(name string) slog.Handler {
	return linesHandler{h.Handler.WithGroup(name), h.memory}
}

// teeHandler passes every line to both of its handlers: the process's
// (journald's) and the log files'.
type teeHandler struct{ first, second slog.Handler }

func (h teeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.first.Enabled(ctx, level) || h.second.Enabled(ctx, level)
}

func (h teeHandler) Handle(ctx context.Context, record slog.Record) error {
	if h.second.Enabled(ctx, record.Level) {
		_ = h.second.Handle(ctx, record)
	}
	if h.first.Enabled(ctx, record.Level) {
		return h.first.Handle(ctx, record)
	}
	return nil
}

func (h teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return teeHandler{h.first.WithAttrs(attrs), h.second.WithAttrs(attrs)}
}

func (h teeHandler) WithGroup(name string) slog.Handler {
	return teeHandler{h.first.WithGroup(name), h.second.WithGroup(name)}
}

func parseLevel(name string) slog.Level {
	var level slog.Level
	if level.UnmarshalText([]byte(name)) != nil {
		return slog.LevelInfo
	}
	return level
}

// lineQuery is what GET /admin/v1/logs asks for: lines at level or above that
// hold every term (case aside) in their message, user, request id, roles or
// evidence;
// only request lines of minMs or more when minMs is set; below the seq
// before, or above the seq after, when set; at most limit of them.
type lineQuery struct {
	level         slog.Level
	terms         []string
	minMs         int64
	before, after int64
	limit         int
}

func (q lineQuery) matches(event Event) bool {
	if parseLevel(event.Level) < q.level || (q.minMs > 0 && event.Ms < q.minMs) {
		return false
	}
	if len(q.terms) == 0 {
		return true
	}
	text := strings.ToLower(event.Message + "\n" + event.User + "\n" + event.RequestID + "\n" + event.Roles + "\n" + event.Evidence)
	for _, term := range q.terms {
		if !strings.Contains(text, term) {
			return false
		}
	}
	return true
}

// find is the newest q.limit lines of lines (the newest first) that q
// matches; without after, whether older ones match beyond them (more); with
// after, how many newer than after it left out (skipped): when more than the
// limit came since, the page holds the newest of them.
func (q lineQuery) find(lines []Event) (found []Event, more bool, skipped int) {
	found = []Event{}
	for _, event := range lines {
		if q.after > 0 && event.Seq <= q.after {
			break
		}
		if (q.before > 0 && event.Seq >= q.before) || !q.matches(event) {
			continue
		}
		switch {
		case len(found) < q.limit:
			found = append(found, event)
		case q.after == 0:
			return found, true, 0
		default:
			skipped++
		}
	}
	return found, false, skipped
}

// parseLineQuery reads GET /admin/v1/logs's query: level=info|warn|error
// (info), q, minMs, before or after (a line's seq), limit=<1..200, 100>; and
// what is wrong with it, "" when nothing.
func parseLineQuery(values url.Values) (lineQuery, string) {
	q := lineQuery{level: slog.LevelInfo, limit: 100}
	switch values.Get("level") {
	case "", "info":
	case "warn":
		q.level = slog.LevelWarn
	case "error":
		q.level = slog.LevelError
	default:
		return q, "level must be info, warn or error"
	}
	if raw := values.Get("q"); raw != "" {
		if utf8.RuneCountInString(raw) > queryChars {
			return q, "q must be at most 200 characters"
		}
		if q.terms = strings.Fields(strings.ToLower(raw)); len(q.terms) > queryTerms {
			return q, "q must be at most 8 terms"
		}
	}
	for _, field := range []struct {
		name  string
		into  *int64
		limit int64
	}{{"minMs", &q.minMs, 0}, {"before", &q.before, 0}, {"after", &q.after, 0}, {"limit", nil, linesPage}} {
		raw := values.Get(field.name)
		if raw == "" {
			continue
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 1 || (field.limit > 0 && n > field.limit) {
			if field.limit > 0 {
				return q, field.name + " must be 1 to " + strconv.FormatInt(field.limit, 10)
			}
			return q, field.name + " must be a positive integer"
		}
		if field.into == nil {
			q.limit = int(n)
		} else {
			*field.into = n
		}
	}
	if q.before > 0 && q.after > 0 {
		return q, "give before or after, not both"
	}
	return q, ""
}

// serveLogs is GET /admin/v1/logs: the latest lines the query matches, the
// newest first, with what the console pages and tails by (docs/admin-api.md);
// and what the log files hold, for their download.
func (s *Server) serveLogs(w http.ResponseWriter, r *http.Request) {
	q, problem := parseLineQuery(r.URL.Query())
	if problem != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
		return
	}
	lines, size, total := s.lines.kept(q.level)
	found, more, skipped := q.find(lines)
	body := map[string]any{"lines": found, "more": more, "skipped": skipped, "kept": size, "total": total}
	if len(lines) > 0 {
		body["since"] = lines[len(lines)-1].Time
	}
	if archive, ok := s.logFiles.summary(); ok {
		body["archive"] = archive
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, body)
}

// serveEvents is GET /admin/v1/events?level=warn|error&limit=<1..500, 100>:
// the latest WARN and ERROR lines (or ERROR only), the newest first, and the
// count of all since the start. GET /admin/v1/logs?level=warn reads the same
// lines, with filters and pages.
func (s *Server) serveEvents(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	q := lineQuery{level: slog.LevelWarn, limit: 100}
	switch query.Get("level") {
	case "", "warn":
	case "error":
		q.level = slog.LevelError
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "level must be warn or error"})
		return
	}
	if raw := query.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > eventsKept {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be 1 to 500"})
			return
		}
		q.limit = n
	}
	lines, _, total := s.lines.kept(slog.LevelWarn)
	events, _, _ := q.find(lines)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "events": events})
}
