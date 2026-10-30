package aml

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
)

// TestAddCountsMessagesByRole: an Add counts the messages it stored by role
// (user, assistant, system, tool, else other) in /admin/status and the
// metrics, and its request line names them.
func TestAddCountsMessagesByRole(t *testing.T) {
	h := newHarness(t, nil)
	h.mustAdd(t, AddRequest{RequestID: "r1", UserID: "u1", SessionID: "s1", Messages: []Message{
		{Role: "user", Content: "I moved the demo to Friday."},
		{Role: "assistant", Content: "Noted, Friday it is."},
		{Role: "system", Content: "Be brief."},
		{Role: "Caroline", Content: "See you there."},
		{Role: "user", Content: "Thanks."},
	}})
	want := map[string]int64{"user": 2, "assistant": 1, "system": 1, "other": 1}
	if got := h.adminStatus(t).Messages; len(got) != len(want) || got["user"] != 2 || got["assistant"] != 1 || got["system"] != 1 || got["other"] != 1 {
		t.Fatalf("messages %v, want %v", got, want)
	}
	s := h.scrape(t)
	s.is(t, 2, "mnemon_aml_add_messages_total", "role", "user")
	s.is(t, 1, "mnemon_aml_add_messages_total", "role", "other")
	if lines := h.logLines("POST /add", "assistant=1,other=1,system=1,user=2"); len(lines) != 1 {
		t.Fatalf("the Add's request line does not name its roles: %q", h.logLines("POST /add"))
	}
}

// TestSearchObservesWhatItDelivered: a Search's items and characters are
// observed, an empty answer included.
func TestSearchObservesWhatItDelivered(t *testing.T) {
	h := newHarness(t, nil)
	h.mustAdd(t, addRequest("r1", "u1"))
	items := h.search(t, searchBody("u1"))
	h.search(t, SearchRequest{Query: "anything", UserID: "nobody", TopK: 5})
	s := h.scrape(t)
	s.is(t, 2, "mnemon_aml_search_items_count")
	chars := 0
	for _, item := range items {
		chars += len([]rune(item.Content))
	}
	s.is(t, float64(len(items)), "mnemon_aml_search_items_sum")
	s.is(t, float64(chars), "mnemon_aml_search_characters_sum")
}

// TestEventsKeepTheLatestWarnings: WARN and ERROR lines, and only those,
// come back newest first with the user and request they carried; the level
// and the limit are checked.
func TestEventsKeepTheLatestWarnings(t *testing.T) {
	h := newHarness(t, nil)
	ctx := withRequestUser(withRequestID(t.Context(), "req-1"))
	nameUser(ctx, strings.Repeat("ab", 16))
	h.server.logf(ctx, slog.LevelInfo, "an info line")
	h.server.logf(ctx, slog.LevelWarn, "a warning")
	h.server.logf(t.Context(), slog.LevelError, "an error")
	var body struct {
		Total  int64   `json:"total"`
		Events []Event `json:"events"`
	}
	response := h.adminCall("GET", "/admin/v1/events")
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil {
		t.Fatal(response.Code, response.Body.String())
	}
	if body.Total < 2 || len(body.Events) < 2 || body.Events[0].Message != "an error" || body.Events[1].Message != "a warning" ||
		body.Events[1].User != "abababab" || body.Events[1].RequestID != "req-1" || body.Events[1].Level != "WARN" {
		t.Fatalf("events %+v", body)
	}
	for _, event := range body.Events {
		if event.Message == "an info line" {
			t.Fatal("an INFO line was kept")
		}
	}
	if response := h.adminCall("GET", "/admin/v1/events?level=error&limit=1"); !strings.Contains(response.Body.String(), `"an error"`) || strings.Contains(response.Body.String(), "a warning") {
		t.Fatal("level=error:", response.Body.String())
	}
	for _, query := range []string{"level=info", "limit=0", "limit=501", "limit=x"} {
		if response := h.adminCall("GET", "/admin/v1/events?"+query); response.Code != http.StatusBadRequest {
			t.Errorf("%s answered %d", query, response.Code)
		}
	}
	// The memory keeps the latest eventsKept warnings.
	memory := newLineMemory()
	for i := 0; i < eventsKept+3; i++ {
		memory.add(slog.LevelWarn, Event{Time: time.Unix(int64(i), 0), Level: "WARN", Message: "m"})
	}
	if events, _, total := memory.kept(slog.LevelWarn); len(events) != eventsKept || total != eventsKept+3 || events[0].Time.Unix() != eventsKept+2 {
		t.Fatalf("memory kept %d of %d, newest %v", len(events), total, events[0].Time)
	}
}

// TestModelSpendAndReadingAreCounted: an attempt's usage counts as spend and
// tokens by kind and class; a job's reading counts by role, in /admin/status
// too.
func TestModelSpendAndReadingAreCounted(t *testing.T) {
	h := newHarness(t, nil)
	m := h.server.metrics
	m.ModelAttempt(memoryagent.AttemptEvent{Kind: "deepseek", Class: "background", Outcome: memoryagent.OutcomeOK,
		Usage: memoryagent.Usage{InputTokens: 1000, OutputTokens: 100, CNY: 0.0028, CacheHitTokens: 600}})
	m.ModelAttempt(memoryagent.AttemptEvent{Kind: "jev", Class: "interactive", Outcome: memoryagent.OutcomeOK,
		Usage: memoryagent.Usage{InputTokens: 50, OutputTokens: 5, USD: 0.001}})
	m.consolidated(memoryagent.ConsolidateResult{Reading: map[string]memoryagent.ReadUnits{"assistant": {Read: 240, Skipped: 760}, "user": {Read: 90}}})
	s := h.scrape(t)
	s.is(t, 0.0028, "mnemon_aml_model_spend_total", "class", "background", "kind", "deepseek")
	s.is(t, 600, "mnemon_aml_model_tokens_total", "class", "background", "kind", "deepseek", "type", "cached")
	s.is(t, 1000, "mnemon_aml_model_tokens_total", "class", "background", "kind", "deepseek", "type", "input")
	s.is(t, 0.001, "mnemon_aml_model_spend_total", "class", "interactive", "kind", "jev")
	s.is(t, 760, "mnemon_aml_consolidation_read_units_total", "outcome", "skipped", "role", "assistant")
	s.is(t, 90, "mnemon_aml_consolidation_read_units_total", "outcome", "read", "role", "user")
	if read := h.adminStatus(t).Read; read != nil {
		t.Fatal("a server without timelines consolidation shows a reading:", read)
	}
	if got := m.readingByRole(); got["assistant"] != (memoryagent.ReadUnits{Read: 240, Skipped: 760}) || got["user"] != (memoryagent.ReadUnits{Read: 90}) {
		t.Fatalf("reading by role %v", got)
	}
}

// TestStoresShowConsolidationState: every store says the scheduler's state
// of its user, "" when idle or without a scheduler.
func TestStoresShowConsolidationState(t *testing.T) {
	h := newHarness(t, nil)
	h.mustAdd(t, addRequest("r1", "u1"))
	code, raw, body := h.stores(t, "")
	if code != http.StatusOK || len(body.Stores) != 1 || !strings.Contains(raw, `"consolidation":""`) || !strings.Contains(raw, `"failures":0`) {
		t.Fatalf("%d %s", code, raw)
	}
}
