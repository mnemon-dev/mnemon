package aml

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// eventsKept is how many WARN and ERROR lines GET /admin/v1/events can show:
// the latest, in memory, from the process start.
const eventsKept = 500

// Event is a WARN or ERROR line the server logged, as GET /admin/v1/events
// shows it: its time, level and message, and the user (8-hex store prefix)
// and request id the line carried. Lines never hold a key or a record's
// content, and neither does an event.
type Event struct {
	Time      time.Time `json:"time"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	User      string    `json:"user,omitempty"`
	RequestID string    `json:"requestId,omitempty"`
}

// eventRing keeps the latest eventsKept events; total counts every one
// since the start.
type eventRing struct {
	mu     sync.Mutex
	events []Event
	next   int
	total  int64
}

func (r *eventRing) add(event Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.events) < eventsKept {
		r.events = append(r.events, event)
	} else {
		r.events[r.next] = event
	}
	r.next = (r.next + 1) % eventsKept
	r.total++
}

// latest is up to limit events at level or above, the newest first, and the
// count of all events since the start.
func (r *eventRing) latest(level slog.Level, limit int) ([]Event, int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []Event{}
	for i := 0; i < len(r.events) && len(out) < limit; i++ {
		event := r.events[(r.next-1-i+2*eventsKept)%len(r.events)]
		if parseLevel(event.Level) >= level {
			out = append(out, event)
		}
	}
	return out, r.total
}

func parseLevel(name string) slog.Level {
	var level slog.Level
	if level.UnmarshalText([]byte(name)) != nil {
		return slog.LevelInfo
	}
	return level
}

// eventsHandler passes every line to its handler, and keeps those at WARN or
// above in ring. It sits under contextHandler, so the lines it sees carry the
// request's user and id.
type eventsHandler struct {
	slog.Handler
	ring *eventRing
}

func (h eventsHandler) Handle(ctx context.Context, record slog.Record) error {
	if record.Level >= slog.LevelWarn {
		event := Event{Time: record.Time.UTC(), Level: record.Level.String(), Message: record.Message}
		record.Attrs(func(attr slog.Attr) bool {
			switch attr.Key {
			case "user":
				event.User = attr.Value.String()
			case "request_id":
				event.RequestID = attr.Value.String()
			}
			return true
		})
		h.ring.add(event)
	}
	return h.Handler.Handle(ctx, record)
}

func (h eventsHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return eventsHandler{h.Handler.WithAttrs(attrs), h.ring}
}

func (h eventsHandler) WithGroup(name string) slog.Handler {
	return eventsHandler{h.Handler.WithGroup(name), h.ring}
}

// serveEvents is GET /admin/v1/events?level=warn|error&limit=<1..500, 100>:
// the latest WARN and ERROR lines (or ERROR only), the newest first, and the
// count of all since the start, for the console's 事件 view.
func (s *Server) serveEvents(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	level, limit := slog.LevelWarn, 100
	switch query.Get("level") {
	case "", "warn":
	case "error":
		level = slog.LevelError
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
		limit = n
	}
	events, total := s.events.latest(level, limit)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "events": events})
}
