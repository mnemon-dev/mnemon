package aml

import (
	"compress/gzip"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// The log files: every line the server logs, as JSON, in a file per hour
// in the log directory (--log-dir), 2026-10-27T03.jsonl while its hour runs, gzipped to
// 2026-10-27T03.jsonl.gz once it has passed; kept logFilesAge, and
// logFilesBytes at most, the oldest dropped first. GET /admin/v1/logs/export
// streams them as one gzip file: the complete log, for an offline look.
// Writing never holds up the line logged: a line that finds the queue full,
// or that the disk refuses, is dropped and counted.
const (
	logFilesAge   = 30 * 24 * time.Hour
	logFilesBytes = 1 << 30
	logQueue      = 8192
	hourLayout    = "2006-01-02T15"
)

type logFiles struct {
	dir string
	now func() time.Time
	// maxBytes is logFilesBytes but in tests.
	maxBytes int64
	lines    chan []byte
	stop     chan struct{}
	done     chan struct{}
	stopped  sync.Once
	dropped  atomic.Int64
	// mu is held by the writer while it gzips and prunes, and by an export
	// and a summary while they list (and open) the files: neither meets a
	// file half written or half removed.
	mu sync.Mutex
	// exporting admits one export at a time.
	exporting sync.Mutex
}

// openLogFiles makes dir, gzips the plain files of the hours past (a process
// that stopped left them) and prunes. Lines written to it queue until start.
func openLogFiles(dir string, now func() time.Time) (*logFiles, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f := &logFiles{dir: dir, now: now, maxBytes: logFilesBytes, lines: make(chan []byte, logQueue), stop: make(chan struct{}), done: make(chan struct{})}
	f.settle(f.hour())
	return f, nil
}

func (f *logFiles) hour() time.Time { return f.now().UTC().Truncate(time.Hour) }

// utcTime writes a line's time in UTC, as the files are named, whatever the
// host's time zone.
func utcTime(groups []string, attr slog.Attr) slog.Attr {
	if len(groups) == 0 && attr.Key == slog.TimeKey && attr.Value.Kind() == slog.KindTime {
		attr.Value = slog.TimeValue(attr.Value.Time().UTC())
	}
	return attr
}

// Write queues a copy of p, a line as slog's JSON handler writes it, or drops
// and counts it when the queue is full. It never blocks and never fails.
func (f *logFiles) Write(p []byte) (int, error) {
	select {
	case f.lines <- append([]byte(nil), p...):
	default:
		f.dropped.Add(1)
	}
	return len(p), nil
}

// start runs the writer: each queued line is appended to the file of the
// hour it is written in; once an hour has passed (a line of the next, or the
// minute's check) its file is closed, gzipped and the files pruned. On nil
// (no log directory) it does nothing, as close does.
func (f *logFiles) start() {
	if f == nil {
		return
	}
	go func() {
		defer close(f.done)
		hour := f.hour()
		var file *os.File
		defer func() {
			if file != nil {
				file.Close()
			}
		}()
		turn := func() {
			if now := f.hour(); !now.Equal(hour) {
				if file != nil {
					file.Close()
					file = nil
				}
				hour = now
				f.settle(now)
			}
		}
		write := func(line []byte) {
			turn()
			if file == nil {
				opened, err := os.OpenFile(filepath.Join(f.dir, hour.Format(hourLayout)+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
				if err != nil {
					f.dropped.Add(1)
					return
				}
				file = opened
			}
			if _, err := file.Write(line); err != nil {
				f.dropped.Add(1)
			}
		}
		tick := time.NewTicker(time.Minute)
		defer tick.Stop()
		for {
			select {
			case line := <-f.lines:
				write(line)
			case <-tick.C:
				turn()
			case <-f.stop:
				for {
					select {
					case line := <-f.lines:
						write(line)
					default:
						return
					}
				}
			}
		}
	}()
}

// close stops the writer once it has written what is queued; a line logged
// after is not written. Call it once start has run.
func (f *logFiles) close() {
	if f == nil {
		return
	}
	f.stopped.Do(func() { close(f.stop) })
	<-f.done
}

// logFile is one file in the log directory: the hour it holds, whether it
// is gzipped, its size.
type logFile struct {
	path    string
	hour    time.Time
	gzipped bool
	size    int64
}

// list is the log files, the oldest hour first, an hour's gzip before its
// plain file; what is not one (an interrupted gzip's .tmp) is left out.
func (f *logFiles) list() []logFile {
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		return nil
	}
	files := []logFile{}
	for _, entry := range entries {
		name, gzipped := strings.CutSuffix(entry.Name(), ".jsonl.gz")
		if !gzipped {
			var plain bool
			if name, plain = strings.CutSuffix(name, ".jsonl"); !plain {
				continue
			}
		}
		hour, err := time.Parse(hourLayout, name)
		info, infoErr := entry.Info()
		if err != nil || infoErr != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, logFile{filepath.Join(f.dir, entry.Name()), hour, gzipped, info.Size()})
	}
	slices.SortFunc(files, func(a, b logFile) int {
		if c := a.hour.Compare(b.hour); c != 0 {
			return c
		}
		if a.gzipped {
			return -1
		}
		return 1
	})
	return files
}

// held is files with one per hour: its gzip when it has one.
func held(files []logFile) []logFile {
	return slices.CompactFunc(slices.Clone(files), func(a, b logFile) bool { return a.hour.Equal(b.hour) })
}

// settle gzips the plain files of the hours before now (a plain file whose
// hour has its gzip already is only removed), removes an interrupted gzip's
// leftovers, and prunes: the files of hours logFilesAge before now, then the
// oldest while all hold more than logFilesBytes. The hour now is not touched.
func (f *logFiles) settle(now time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if leftovers, err := filepath.Glob(filepath.Join(f.dir, "*.tmp")); err == nil {
		for _, leftover := range leftovers {
			os.Remove(leftover)
		}
	}
	files := f.list()
	for i, file := range files {
		if file.gzipped || !file.hour.Before(now) {
			continue
		}
		if i > 0 && files[i-1].gzipped && files[i-1].hour.Equal(file.hour) {
			os.Remove(file.path)
			continue
		}
		gzipFile(file.path)
	}
	files = held(f.list())
	var total int64
	for _, file := range files {
		total += file.size
	}
	for _, file := range files {
		if !file.hour.Before(now) || (!file.hour.Before(now.Add(-logFilesAge)) && total <= f.maxBytes) {
			break
		}
		if os.Remove(file.path) == nil {
			total -= file.size
		}
	}
}

// gzipFile writes path's gzip beside it, through a temporary file renamed
// once complete, then removes path. On a failure path is kept, to be tried
// again at the next hour.
func gzipFile(path string) {
	in, err := os.Open(path)
	if err != nil {
		return
	}
	defer in.Close()
	target := path + ".gz"
	out, err := os.OpenFile(target+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	zw := gzip.NewWriter(out)
	_, err = io.Copy(zw, in)
	if err == nil {
		err = zw.Close()
	}
	if err == nil {
		err = out.Sync()
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(target+".tmp", target)
	}
	if err != nil {
		os.Remove(target + ".tmp")
		return
	}
	os.Remove(path)
}

// logArchive is what the log files hold, as GET /admin/v1/logs shows it: the
// first hour kept, the bytes on disk, and the lines dropped since the start.
type logArchive struct {
	Since   time.Time `json:"since"`
	Bytes   int64     `json:"bytes"`
	Dropped int64     `json:"dropped"`
}

// summary is what the files hold; false when there are none, or no log
// directory (nil).
func (f *logFiles) summary() (logArchive, bool) {
	if f == nil {
		return logArchive{}, false
	}
	f.mu.Lock()
	files := held(f.list())
	f.mu.Unlock()
	if len(files) == 0 {
		return logArchive{}, false
	}
	archive := logArchive{Since: files[0].hour, Dropped: f.dropped.Load()}
	for _, file := range files {
		archive.Bytes += file.size
	}
	return archive, true
}

// exportFile is a log file open for an export, a plain one read to the size
// it had when opened.
type exportFile struct {
	*os.File
	logFile
}

// open opens the files of the hours that end after from and start before to
// (either zero: no bound), the oldest first.
func (f *logFiles) open(from, to time.Time) ([]exportFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	opened := []exportFile{}
	for _, file := range held(f.list()) {
		if (!from.IsZero() && !file.hour.Add(time.Hour).After(from)) || (!to.IsZero() && !file.hour.Before(to)) {
			continue
		}
		handle, err := os.Open(file.path)
		if err == nil {
			var info os.FileInfo
			if info, err = handle.Stat(); err == nil {
				file.size = info.Size()
				opened = append(opened, exportFile{handle, file})
				continue
			}
			handle.Close()
		}
		for _, done := range opened {
			done.Close()
		}
		return nil, err
	}
	return opened, nil
}

// copyTo writes the file to w as a gzip member: a gzipped file as it is, a
// plain one through a gzip writer.
func (e exportFile) copyTo(w io.Writer) error {
	if e.gzipped {
		_, err := io.Copy(w, e.File)
		return err
	}
	zw := gzip.NewWriter(w)
	if _, err := io.Copy(zw, io.LimitReader(e.File, e.size)); err != nil {
		return err
	}
	return zw.Close()
}

// exportRange reads ?from=&to= (RFC 3339, either left out), and what is
// wrong with them, "" when nothing.
func exportRange(values url.Values) (from, to time.Time, problem string) {
	for _, bound := range []struct {
		name string
		into *time.Time
	}{{"from", &from}, {"to", &to}} {
		if raw := values.Get(bound.name); raw != "" {
			at, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return from, to, bound.name + " must be an RFC 3339 time"
			}
			*bound.into = at.UTC()
		}
	}
	if !from.IsZero() && !to.IsZero() && !from.Before(to) {
		return from, to, "from must be before to"
	}
	return from, to, ""
}

// serveLogExport is GET /admin/v1/logs/export?from=&to=: every line the log
// files hold for the whole hours the range touches, the oldest first, as one
// gzip file of JSON lines (a member per hour, which gzip, zcat and Go's and
// Python's readers read as one stream). One export runs at a time; another
// answers 429. Each file has a minute to be written, past the admin
// listener's write timeout, so a long export is not cut and a stalled
// client is. Without a log directory it is a 404.
func (s *Server) serveLogExport(w http.ResponseWriter, r *http.Request) {
	if s.logFiles == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "this server keeps no log files"})
		return
	}
	from, to, problem := exportRange(r.URL.Query())
	if problem != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": problem})
		return
	}
	if !s.logFiles.exporting.TryLock() {
		w.Header().Set("Retry-After", "30")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "a log export is running; retry"})
		return
	}
	defer s.logFiles.exporting.Unlock()
	files, err := s.logFiles.open(from, to)
	if err != nil {
		s.logf(r.Context(), slog.LevelError, "log export failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "the log files could not be read"})
		return
	}
	defer func() {
		for _, file := range files {
			file.Close()
		}
	}()
	name := "mnemon-logs-empty.jsonl.gz"
	if len(files) > 0 {
		name = "mnemon-logs-" + files[0].hour.Format(hourLayout) + "-" + files[len(files)-1].hour.Format(hourLayout) + ".jsonl.gz"
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	if len(files) == 0 {
		_ = gzip.NewWriter(w).Close() // an empty gzip file
		return
	}
	control := http.NewResponseController(w)
	for _, file := range files {
		_ = control.SetWriteDeadline(time.Now().Add(time.Minute))
		if file.copyTo(w) != nil {
			return // the client left
		}
	}
}
