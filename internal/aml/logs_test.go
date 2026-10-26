package aml

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestLineMemoryKeepsOrderAndBounds: lines come back the newest first
// numbered in the order they came, before the memory is full too; a burst of
// INFO lines pushes the older lines out but not the warnings; a long message
// is cut at a character's start.
func TestLineMemoryKeepsOrderAndBounds(t *testing.T) {
	memory := newLineMemory()
	for i := 1; i <= 3; i++ {
		memory.add(slog.LevelInfo, Event{Level: "INFO", Message: strconv.Itoa(i)})
	}
	lines, size, total := memory.kept(slog.LevelInfo)
	if len(lines) != 3 || size != linesKept || total != 3 || lines[0].Message != "3" || lines[0].Seq != 3 || lines[2].Seq != 1 {
		t.Fatalf("kept %+v (size %d, total %d)", lines, size, total)
	}
	memory.add(slog.LevelWarn, Event{Level: "WARN", Message: "a warning"})
	for i := 0; i < linesKept; i++ {
		memory.add(slog.LevelInfo, Event{Level: "INFO", Message: "burst"})
	}
	lines, _, total = memory.kept(slog.LevelInfo)
	if len(lines) != linesKept || total != linesKept+4 || lines[0].Seq != linesKept+4 || lines[len(lines)-1].Seq != 5 {
		t.Fatalf("after the burst: %d lines, total %d, seq %d..%d", len(lines), total, lines[len(lines)-1].Seq, lines[0].Seq)
	}
	warnings, size, _ := memory.kept(slog.LevelWarn)
	if len(warnings) != 1 || size != eventsKept || warnings[0].Message != "a warning" || warnings[0].Seq != 4 {
		t.Fatalf("warnings %+v", warnings)
	}
	memory.add(slog.LevelError, Event{Level: "ERROR", Message: strings.Repeat("a", messageKept-1) + "é and more"})
	if newest, _, _ := memory.kept(slog.LevelError); newest[0].Message != strings.Repeat("a", messageKept-1)+"…" {
		t.Fatalf("cut to %q", newest[0].Message[messageKept-8:])
	}
}

// logsPage is GET /admin/v1/logs's answer.
type logsPage struct {
	Lines   []Event     `json:"lines"`
	More    bool        `json:"more"`
	Skipped int         `json:"skipped"`
	Since   time.Time   `json:"since"`
	Kept    int         `json:"kept"`
	Total   int64       `json:"total"`
	Archive *logArchive `json:"archive"`
}

func (h *harness) logs(t *testing.T, query string) logsPage {
	t.Helper()
	response := h.adminCall("GET", "/admin/v1/logs?"+query)
	var page logsPage
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil {
		t.Fatal(query, response.Code, response.Body.String())
	}
	return page
}

func messages(lines []Event) string {
	out := []string{}
	for _, line := range lines {
		out = append(out, line.Message)
	}
	return strings.Join(out, " | ")
}

// TestLogsAreQueriedAndPaged: GET /admin/v1/logs filters by level, terms
// (case aside, in the message, user, request id or roles) and duration;
// pages older lines with before and more; tails with after, saying how many
// it skipped; checks its query; and says what the log files hold.
func TestLogsAreQueriedAndPaged(t *testing.T) {
	h := newHarness(t, func(config *ServerConfig) { config.LogDir = t.TempDir() })
	ctx := withRequestUser(withRequestID(t.Context(), "req-add"))
	nameUser(ctx, strings.Repeat("cd", 16))
	nameRoles(ctx, map[string]int{"user": 3, "assistant": 1})
	h.server.logRequest(ctx, "POST", "/add", 200, 1500*time.Millisecond)
	h.server.logRequest(t.Context(), "POST", "/search", 503, 30*time.Millisecond)
	h.server.logf(t.Context(), slog.LevelWarn, "a warning")
	h.server.logf(t.Context(), slog.LevelError, "an error")
	h.server.logf(t.Context(), slog.LevelInfo, "the newest")

	all := h.logs(t, "")
	if messages(all.Lines) != "the newest | an error | a warning | POST /search 503 30ms | POST /add 200 1500ms" ||
		all.Kept != linesKept || all.More || all.Since.IsZero() {
		t.Fatalf("all: %s (%+v)", messages(all.Lines), all)
	}
	add := all.Lines[4]
	if add.User != "cdcdcdcd" || add.RequestID != "req-add" || add.Roles != "assistant=1,user=3" || add.Status != 200 || add.Ms != 1500 || all.Lines[3].Status != 503 {
		t.Fatalf("the request lines: %+v %+v", add, all.Lines[3])
	}
	for query, want := range map[string]string{
		"level=warn":           "an error | a warning",
		"level=error":          "an error",
		"q=ADD+user%3D3":       "POST /add 200 1500ms",
		"q=cdcdcdcd":           "POST /add 200 1500ms",
		"q=req-add+503":        "",
		"minMs=1000":           "POST /add 200 1500ms",
		"minMs=10&q=%2Fsearch": "POST /search 503 30ms",
	} {
		if got := messages(h.logs(t, query).Lines); got != want {
			t.Errorf("%s: %q, want %q", query, got, want)
		}
	}
	if warn := h.logs(t, "level=warn"); warn.Kept != eventsKept {
		t.Errorf("level=warn reads a memory of %d", warn.Kept)
	}

	first := h.logs(t, "limit=2")
	second := h.logs(t, "limit=2&before="+strconv.FormatInt(first.Lines[1].Seq, 10))
	last := h.logs(t, "limit=2&before="+strconv.FormatInt(second.Lines[1].Seq, 10))
	if messages(first.Lines) != "the newest | an error" || !first.More || messages(second.Lines) != "a warning | POST /search 503 30ms" ||
		!second.More || messages(last.Lines) != "POST /add 200 1500ms" || last.More {
		t.Fatalf("pages: %s / %s / %s", messages(first.Lines), messages(second.Lines), messages(last.Lines))
	}
	tail := h.logs(t, "limit=2&after="+strconv.FormatInt(add.Seq, 10))
	if messages(tail.Lines) != "the newest | an error" || tail.Skipped != 2 || tail.More {
		t.Fatalf("tail: %s, skipped %d", messages(tail.Lines), tail.Skipped)
	}
	if none := h.logs(t, "after="+strconv.FormatInt(all.Lines[0].Seq, 10)); len(none.Lines) != 0 || none.Skipped != 0 {
		t.Fatalf("nothing newer: %+v", none)
	}

	for _, query := range []string{"level=debug", "limit=0", "limit=201", "limit=x", "minMs=0", "before=-1", "after=x",
		"before=5&after=2", "q=" + strings.Repeat("a+", 9), "q=" + strings.Repeat("a", 201)} {
		if response := h.adminCall("GET", "/admin/v1/logs?"+query); response.Code != http.StatusBadRequest {
			t.Errorf("%s answered %d", query, response.Code)
		}
	}

	// The writer has the lines on disk soon after.
	deadline := time.Now().Add(5 * time.Second)
	for h.logs(t, "").Archive == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if archive := h.logs(t, "").Archive; archive == nil || archive.Bytes == 0 || archive.Since.IsZero() || archive.Dropped != 0 {
		t.Fatalf("archive %+v", archive)
	}
}

// TestRequestLinesCarryStatusAndMilliseconds: the line of a request through
// the handler carries its status as an attribute, which the memory keeps.
func TestRequestLinesCarryStatusAndMilliseconds(t *testing.T) {
	h := newHarness(t, nil)
	h.call("POST", "/search", map[string]any{}, "Authorization", "Bearer wrong")
	lines, _, _ := h.server.lines.kept(slog.LevelInfo)
	for _, line := range lines {
		if strings.HasPrefix(line.Message, "POST /search 401 ") {
			if line.Status != 401 || line.RequestID == "" {
				t.Fatalf("the request line %+v", line)
			}
			return
		}
	}
	t.Fatalf("no request line in %s", messages(lines))
}

// gunzipAll is every member of a gzip stream, read as one.
func gunzipAll(t *testing.T, data []byte) string {
	t.Helper()
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// exported is what an export of the files from..to holds.
func exported(t *testing.T, files *logFiles, from, to time.Time) string {
	t.Helper()
	opened, err := files.open(from, to)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	for _, file := range opened {
		if err := file.copyTo(&out); err != nil {
			t.Fatal(err)
		}
		file.Close()
	}
	if out.Len() == 0 {
		return ""
	}
	return gunzipAll(t, out.Bytes())
}

// TestLogFilesGzipPruneAndExport: lines go to the file of their hour, which
// is gzipped once the hour has passed; an export joins the hours a range
// touches, gzipped and plain; files past their age, beyond the size, or left
// by an interrupted process are settled; a full queue drops and counts.
func TestLogFilesGzipPruneAndExport(t *testing.T) {
	dir := t.TempDir()
	var mu sync.Mutex
	now := time.Date(2026, 10, 27, 3, 10, 0, 0, time.UTC)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	set := func(at time.Time) { mu.Lock(); now = at; mu.Unlock() }

	// What a stopped process left: an old file, a plain file of an hour past,
	// a plain file beside its gzip, and an interrupted gzip.
	old := filepath.Join(dir, "2026-09-20T01.jsonl.gz")
	for name, content := range map[string]string{
		"2026-09-20T01.jsonl.gz":     "",
		"2026-10-27T01.jsonl":        "{\"msg\":\"left plain\"}\n",
		"2026-10-27T00.jsonl":        "a duplicate\n",
		"2026-10-27T02.jsonl.gz.tmp": "half",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write([]byte("{\"msg\":\"hour zero\"}\n"))
	zw.Close()
	os.WriteFile(filepath.Join(dir, "2026-10-27T00.jsonl.gz"), gz.Bytes(), 0o600)

	files, err := openLogFiles(dir, clock)
	if err != nil {
		t.Fatal(err)
	}
	names := func() string {
		entries, _ := os.ReadDir(dir)
		out := []string{}
		for _, entry := range entries {
			out = append(out, entry.Name())
		}
		return strings.Join(out, " ")
	}
	if got := names(); got != "2026-10-27T00.jsonl.gz 2026-10-27T01.jsonl.gz" {
		t.Fatalf("settled at the start: %s (old %s)", got, old)
	}

	files.start()
	files.Write([]byte("{\"msg\":\"a\"}\n"))
	// The writer files a line by the hour it writes it in: wait for it.
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if info, err := os.Stat(filepath.Join(dir, "2026-10-27T03.jsonl")); err == nil && info.Size() > 0 {
			break
		}
	}
	set(time.Date(2026, 10, 27, 4, 5, 0, 0, time.UTC))
	files.Write([]byte("{\"msg\":\"b\"}\n"))
	files.close()
	if got := names(); got != "2026-10-27T00.jsonl.gz 2026-10-27T01.jsonl.gz 2026-10-27T03.jsonl.gz 2026-10-27T04.jsonl" {
		t.Fatalf("after an hour: %s", got)
	}
	everything := "{\"msg\":\"hour zero\"}\n{\"msg\":\"left plain\"}\n{\"msg\":\"a\"}\n{\"msg\":\"b\"}\n"
	if got := exported(t, files, time.Time{}, time.Time{}); got != everything {
		t.Fatalf("everything: %q", got)
	}
	from, to := time.Date(2026, 10, 27, 3, 30, 0, 0, time.UTC), time.Date(2026, 10, 27, 4, 0, 0, 0, time.UTC)
	if got := exported(t, files, from, to); got != "{\"msg\":\"a\"}\n" {
		t.Fatalf("03:30 to 04:00: %q", got)
	}
	if got := exported(t, files, to, time.Time{}); got != "{\"msg\":\"b\"}\n" {
		t.Fatalf("from 04:00: %q", got)
	}
	if archive, ok := files.summary(); !ok || !archive.Since.Equal(time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)) || archive.Bytes == 0 {
		t.Fatalf("summary %+v", archive)
	}

	// Past the size, the oldest go first; the hour now is kept.
	files.maxBytes = 1
	files.settle(time.Date(2026, 10, 27, 4, 0, 0, 0, time.UTC))
	if got := names(); got != "2026-10-27T04.jsonl" {
		t.Fatalf("pruned by size: %s", got)
	}

	// A full queue drops, and counts, what it cannot take.
	idle, err := openLogFiles(t.TempDir(), clock)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < logQueue+2; i++ {
		idle.Write([]byte("x\n"))
	}
	if idle.dropped.Load() != 2 {
		t.Fatalf("dropped %d", idle.dropped.Load())
	}
}

// TestLogExportEndpoint: GET /admin/v1/logs/export answers the lines logged
// as a gzip attachment, checks its range, and runs one at a time.
func TestLogExportEndpoint(t *testing.T) {
	if response := newHarness(t, nil).adminCall("GET", "/admin/v1/logs/export"); response.Code != http.StatusNotFound {
		t.Fatal("without a log directory:", response.Code)
	}
	// Outside the data root, as the settings file.
	root := t.TempDir()
	if server, err := NewServer(ServerConfig{DataRoot: root, MemoryConfig: simpleConfig(), AuthKey: testKey, LogDir: filepath.Join(root, "logs")}); err == nil || !strings.Contains(err.Error(), "the log directory") {
		if server != nil {
			server.Close()
		}
		t.Fatal("a log directory in the data root:", err)
	}
	h := newHarness(t, func(config *ServerConfig) { config.LogDir = t.TempDir() })
	h.server.logf(t.Context(), slog.LevelWarn, "a line to export")
	deadline := time.Now().Add(5 * time.Second)
	var body string
	for time.Now().Before(deadline) {
		response := h.adminCall("GET", "/admin/v1/logs/export")
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/gzip" ||
			!strings.HasPrefix(response.Header().Get("Content-Disposition"), `attachment; filename="mnemon-logs-`) {
			t.Fatal(response.Code, response.Header(), response.Body.String())
		}
		if body = gunzipAll(t, response.Body.Bytes()); strings.Contains(body, "a line to export") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	var line map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(body), "\n") {
		if strings.Contains(raw, "a line to export") && json.Unmarshal([]byte(raw), &line) == nil {
			break
		}
	}
	if line["msg"] != "a line to export" || line["level"] != "WARN" || !strings.HasSuffix(line["time"].(string), "Z") {
		t.Fatalf("exported %q", body)
	}
	for _, query := range []string{"from=yesterday", "to=2026-10-27", "from=2026-10-27T04:00:00Z&to=2026-10-27T03:00:00Z"} {
		if response := h.adminCall("GET", "/admin/v1/logs/export?"+query); response.Code != http.StatusBadRequest {
			t.Errorf("%s answered %d", query, response.Code)
		}
	}
	h.server.logFiles.exporting.Lock()
	response := h.adminCall("GET", "/admin/v1/logs/export")
	h.server.logFiles.exporting.Unlock()
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") == "" {
		t.Fatal("a second export:", response.Code)
	}
}
