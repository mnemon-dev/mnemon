// Command fakeprovider stands in for DeepSeek and JEV in the AML fault drill
// and load test (scripts/aml/drill.sh, scripts/aml/load.sh). It answers the
// requests internal/memoryagent sends (provider.go) with cheap, plausible and
// deterministic replies, so that a Search reads and background consolidation
// folds and links without a paid call (replies.go):
//
//   - POST /chat/completions (and /v1/chat/completions) is DeepSeek's chat
//     completion: {"choices":[{"message":{"content":…},"finish_reason":"stop"}],
//     "usage":{"prompt_tokens":…,"completion_tokens":…}}.
//   - POST /v1/systemone is JEV's decision API: {"model":…,"answers":{key:
//     {"type":"noul","noul":p}},"usage":{"input_tokens":…,"output_tokens":…}}.
//
// A request needs "Authorization: Bearer <anything>", as the real APIs do;
// the credential is never logged or kept. GET /health answers 200.
//
// Each kind answers after its latency (-deepseek-latency, -jev-latency). The
// drill changes how a kind behaves at run time through a control endpoint
// that only loopback clients may use:
//
//   - POST /_fault sets one kind's fault profile ("kind": deepseek, jev or
//     all), replacing the one it had, for "seconds" (0: until cleared): a base
//     "latency" in place of the flag's; "spikeRate" of the calls slower by
//     "spikeLatency"; "errorRate" of the calls answered "errorStatus" at once,
//     with "retryAfter" as their Retry-After when it is set; "malformedRate"
//     of the replies malformed as "malformed" says (invalid-json, empty,
//     truncated, wrong-schema or mixed; see malform); and "stallRate" of the
//     replies that send their headers and half their body, then hang until
//     the client gives up or "stallFor" passes (15m by default).
//   - DELETE /_fault ends every profile; GET /_fault shows them.
//   - GET /_stats counts what each kind answered: statuses, and the spikes,
//     malformed replies and stalls among them.
//
// The faults a call meets are drawn from a generator seeded by -seed, so a
// run's sequence of draws repeats; the reply itself depends only on the
// request. It imports only the standard library, uses no cgo and reads no
// file, so it cross-compiles anywhere (CGO_ENABLED=0 GOOS=linux GOARCH=amd64
// go build).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The kinds of provider this program fakes, as memoryagent names them.
const (
	kindDeepSeek = "deepseek"
	kindJEV      = "jev"
)

// maxBody bounds a request body. The engine sends at most 160,000 bytes to
// DeepSeek and about 96,000 UTF-16 units to JEV.
const maxBody = 8 << 20

// defaultStall is how long a stalled reply hangs unless its profile says.
const defaultStall = 15 * time.Minute

func main() {
	listen := flag.String("listen", "127.0.0.1:8099", "address to listen on (port 0 picks a free one)")
	deepseekLatency := flag.Duration("deepseek-latency", 0, "time every DeepSeek reply takes")
	jevLatency := flag.Duration("jev-latency", 0, "time every JEV reply takes")
	seed := flag.Uint64("seed", 1, "seed of the fault draws")
	flag.Parse()
	if *deepseekLatency < 0 || *jevLatency < 0 {
		log.Fatal("fakeprovider: latencies must not be negative")
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatalf("fakeprovider: %v", err)
	}
	// The drill and the tests read this line for the address when the port
	// is 0.
	fmt.Fprintf(os.Stderr, "fakeprovider listening on %s\n", listener.Addr())
	fake := newServer(map[string]time.Duration{kindDeepSeek: *deepseekLatency, kindJEV: *jevLatency}, *seed, time.Now)
	server := &http.Server{Handler: fake.routes(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute,
		ErrorLog: log.New(io.Discard, "", 0)}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	select {
	case err := <-served:
		log.Fatalf("fakeprovider: %v", err)
	case <-ctx.Done():
	}
	// Stalled replies would hold a graceful shutdown for their whole stall.
	server.Close()
}

// fault is a kind's fault profile (POST /_fault), parsed.
type fault struct {
	latency       *time.Duration // the base latency in place of the flag's; nil keeps it
	spikeRate     float64
	spike         time.Duration
	errorRate     float64
	errorStatus   int
	retryAfter    string
	malformedRate float64
	malformed     string
	stallRate     float64
	stallFor      time.Duration
	until         time.Time // zero: until cleared
	shown         profile   // as it was set, for GET /_fault
}

// profile is a fault as POST /_fault takes it.
type profile struct {
	Kind          string  `json:"kind"`
	Latency       string  `json:"latency,omitempty"`
	SpikeRate     float64 `json:"spikeRate,omitempty"`
	SpikeLatency  string  `json:"spikeLatency,omitempty"`
	ErrorRate     float64 `json:"errorRate,omitempty"`
	ErrorStatus   int     `json:"errorStatus,omitempty"`
	RetryAfter    string  `json:"retryAfter,omitempty"`
	MalformedRate float64 `json:"malformedRate,omitempty"`
	Malformed     string  `json:"malformed,omitempty"`
	StallRate     float64 `json:"stallRate,omitempty"`
	StallFor      string  `json:"stallFor,omitempty"`
	Seconds       float64 `json:"seconds,omitempty"`
}

// The ways a reply can be malformed (malform).
var malformations = []string{"invalid-json", "empty", "truncated", "wrong-schema"}

// parse checks a profile and makes its fault, from now.
func (p profile) parse(now time.Time) (fault, error) {
	f := fault{spikeRate: p.SpikeRate, errorRate: p.ErrorRate, errorStatus: p.ErrorStatus, retryAfter: p.RetryAfter,
		malformedRate: p.MalformedRate, malformed: p.Malformed, stallRate: p.StallRate, stallFor: defaultStall, shown: p}
	duration := func(text, name string, into *time.Duration) error {
		if text == "" {
			return nil
		}
		d, err := time.ParseDuration(text)
		if err != nil || d < 0 {
			return fmt.Errorf("%s must be a duration such as 2s", name)
		}
		*into = d
		return nil
	}
	if p.Latency != "" {
		f.latency = new(time.Duration)
		if err := duration(p.Latency, "latency", f.latency); err != nil {
			return fault{}, err
		}
	}
	if err := duration(p.SpikeLatency, "spikeLatency", &f.spike); err != nil {
		return fault{}, err
	}
	if err := duration(p.StallFor, "stallFor", &f.stallFor); err != nil {
		return fault{}, err
	}
	for _, rate := range []float64{p.SpikeRate, p.ErrorRate, p.MalformedRate, p.StallRate} {
		if rate < 0 || rate > 1 {
			return fault{}, fmt.Errorf("rates must be in [0, 1]")
		}
	}
	switch {
	case p.ErrorRate > 0 && (p.ErrorStatus < 400 || p.ErrorStatus > 599):
		return fault{}, fmt.Errorf("errorStatus must be 400–599")
	case p.MalformedRate > 0 && p.Malformed != "mixed" && !contains(malformations, p.Malformed):
		return fault{}, fmt.Errorf("malformed must be one of %s or mixed", strings.Join(malformations, ", "))
	case strings.ContainsAny(p.RetryAfter, "\r\n"):
		return fault{}, fmt.Errorf("retryAfter must be one header value")
	case p.Seconds < 0 || p.Seconds > 86400:
		return fault{}, fmt.Errorf("seconds must be in [0, 86400]")
	}
	if p.Seconds > 0 {
		f.until = now.Add(time.Duration(p.Seconds * float64(time.Second)))
	}
	return f, nil
}

// server is the fake's state: the latency of each kind, its faults, the
// fault draws and what it answered.
type server struct {
	latency map[string]time.Duration
	now     func() time.Time

	mu       sync.Mutex
	draws    *rand.Rand
	faults   map[string]fault
	answered map[string]map[string]int64
}

func newServer(latency map[string]time.Duration, seed uint64, now func() time.Time) *server {
	return &server{latency: latency, now: now, draws: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)),
		faults: map[string]fault{}, answered: map[string]map[string]int64{kindDeepSeek: {}, kindJEV: {}}}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /chat/completions", s.provider(kindDeepSeek, completion))
	mux.HandleFunc("POST /v1/chat/completions", s.provider(kindDeepSeek, completion))
	mux.HandleFunc("POST /v1/systemone", s.provider(kindJEV, decision))
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /_fault", loopbackOnly(s.setFault))
	mux.HandleFunc("DELETE /_fault", loopbackOnly(s.clearFaults))
	mux.HandleFunc("GET /_fault", loopbackOnly(s.showFaults))
	mux.HandleFunc("GET /_stats", loopbackOnly(s.stats))
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { writeError(w, http.StatusNotFound, "not found") })
	return mux
}

// loopbackOnly refuses a control request from anything but a loopback
// address, should the fake listen on another.
func loopbackOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
			writeError(w, http.StatusForbidden, "the control endpoint answers loopback clients only")
			return
		}
		next(w, r)
	}
}

// plan is what one call meets, drawn when it arrives.
type plan struct {
	latency    time.Duration
	spiked     bool
	status     int // an error status to answer at once; 0 for none
	retryAfter string
	stall      time.Duration // 0: no stall
	malformed  string        // "" for a well-formed reply
}

// draw makes the plan of a call of kind from its active fault, if any.
func (s *server) draw(kind string) plan {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := plan{latency: s.latency[kind]}
	f, ok := s.faults[kind]
	if ok && !f.until.IsZero() && !s.now().Before(f.until) {
		delete(s.faults, kind)
		ok = false
	}
	if !ok {
		return p
	}
	if f.latency != nil {
		p.latency = *f.latency
	}
	if s.hit(f.errorRate) {
		p.status, p.retryAfter = f.errorStatus, f.retryAfter
		return p
	}
	if s.hit(f.spikeRate) {
		p.latency, p.spiked = p.latency+f.spike, true
	}
	switch {
	case s.hit(f.stallRate):
		p.stall = f.stallFor
	case s.hit(f.malformedRate):
		p.malformed = f.malformed
		if p.malformed == "mixed" {
			p.malformed = malformations[s.draws.IntN(len(malformations))]
		}
	}
	return p
}

// hit draws whether an event of the given rate happens; s.mu is held.
func (s *server) hit(rate float64) bool { return rate > 0 && s.draws.Float64() < rate }

// count records one outcome of a kind's call.
func (s *server) count(kind, outcome string) {
	s.mu.Lock()
	s.answered[kind][outcome]++
	s.mu.Unlock()
}

// requestError is a request the fake refuses with 400, as the real API
// refuses a malformed one.
type requestError string

func (e requestError) Error() string { return string(e) }

// provider serves one kind's API: the credential is checked, an error the
// call draws answers at once, and otherwise the reply comes after the
// latency, well-formed, malformed or stalled as drawn.
func (s *server) provider(kind string, build func(body []byte) (reply, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
		if err != nil || len(body) > maxBody {
			s.count(kind, "400")
			writeJSON(w, http.StatusBadRequest, errorBody("unreadable or oversized request body"))
			return
		}
		if scheme, credential, _ := strings.Cut(r.Header.Get("Authorization"), " "); !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(credential) == "" {
			s.count(kind, "401")
			writeJSON(w, http.StatusUnauthorized, errorBody("missing API key"))
			return
		}
		p := s.draw(kind)
		if p.status != 0 {
			s.count(kind, fmt.Sprint(p.status))
			if p.retryAfter != "" {
				w.Header().Set("Retry-After", p.retryAfter)
			}
			writeJSON(w, p.status, errorBody(http.StatusText(p.status)))
			return
		}
		if p.spiked {
			s.count(kind, "spike")
		}
		if !sleep(r.Context(), p.latency) {
			s.count(kind, "gone")
			return
		}
		value, err := build(body)
		if err != nil {
			s.count(kind, "400")
			writeJSON(w, http.StatusBadRequest, errorBody(err.Error()))
			return
		}
		raw := encode(value.value)
		switch {
		case p.stall > 0:
			s.count(kind, "stall")
			stall(w, r, raw, p.stall)
			return
		case p.malformed != "":
			var applied bool
			if raw, applied = malform(kind, value, p.malformed); applied {
				s.count(kind, "malformed:"+p.malformed)
			}
		}
		s.count(kind, "200")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
	}
}

// sleep waits d unless the client leaves first, reporting whether it waited.
func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// stall sends a 200, its headers and the first half of raw, then waits
// until the client leaves or d passes, and breaks the response off.
func stall(w http.ResponseWriter, r *http.Request, raw []byte, d time.Duration) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw[:len(raw)/2])
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	sleep(r.Context(), d)
	// The response must not end well: the server closes the connection
	// without the end of the chunked body.
	panic(http.ErrAbortHandler)
}

// malform turns a well-formed reply into a malformed one, reporting whether
// the malformation applies to it:
//   - invalid-json: where the caller parses JSON, it gets prose instead (a
//     DeepSeek extraction's or answer's content, a JEV body); a planner
//     reply, which is not JSON, is left alone;
//   - empty: DeepSeek's choices empty or its content "" (alternately, by the
//     request), JEV's answers {};
//   - truncated: DeepSeek's content cut in half, finish_reason "length", as
//     a reply cut at its token limit; JEV's body cut in half;
//   - wrong-schema: a JSON envelope the caller does not read: DeepSeek's
//     reply without choices, JEV's answers of another type.
func malform(kind string, r reply, how string) ([]byte, bool) {
	if kind == kindJEV {
		answers := map[string]any{}
		switch how {
		case "invalid-json":
			return []byte("upstream answered: decision service temporarily degraded"), true
		case "truncated":
			raw := encode(r.value)
			return raw[:len(raw)/2], true
		case "wrong-schema":
			for _, key := range r.keys {
				answers[key] = map[string]any{"type": "boolean", "value": true}
			}
		}
		return encode(map[string]any{"model": r.model, "answers": answers, "usage": r.usage}), true
	}
	switch how {
	case "invalid-json":
		if !r.jsonContent {
			return encode(r.value), false
		}
		return encode(r.completion("events: the record mentions "+firstWords(r.content, 6), "stop")), true
	case "empty":
		if r.sum[0]%2 == 0 {
			envelope := r.completion("", "stop")
			envelope["choices"] = []any{}
			return encode(envelope), true
		}
		return encode(r.completion("", "stop")), true
	case "truncated":
		runes := []rune(r.content)
		return encode(r.completion(string(runes[:len(runes)/2]), "length")), true
	}
	return encode(map[string]any{"id": "fake-" + r.id, "object": "chat.completion", "model": r.model,
		"output": []any{map[string]string{"type": "text", "text": r.content}}, "usage": r.usage}), true
}

// setFault is POST /_fault.
func (s *server) setFault(w http.ResponseWriter, r *http.Request) {
	var p profile
	decoder := json.NewDecoder(io.LimitReader(r.Body, 8192))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "a fault is a JSON object of the profile's fields: "+err.Error())
		return
	}
	kinds := []string{p.Kind}
	switch p.Kind {
	case "all":
		kinds = []string{kindDeepSeek, kindJEV}
	case kindDeepSeek, kindJEV:
	default:
		writeError(w, http.StatusBadRequest, "kind must be deepseek, jev or all")
		return
	}
	f, err := p.parse(s.now())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.mu.Lock()
	for _, kind := range kinds {
		s.faults[kind] = f
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"kinds": kinds})
}

func (s *server) clearFaults(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.faults = map[string]fault{}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]bool{"cleared": true})
}

// showFaults is GET /_fault: the active profiles by kind.
func (s *server) showFaults(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	out := map[string]profile{}
	for kind, f := range s.faults {
		if f.until.IsZero() || s.now().Before(f.until) {
			out[kind] = f.shown
		}
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

// stats is GET /_stats: {"deepseek":{"200":n,"stall":n,…},"jev":{…}}.
func (s *server) stats(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	out := map[string]map[string]int64{}
	for kind, outcomes := range s.answered {
		out[kind] = map[string]int64{}
		for outcome, n := range outcomes {
			out[kind][outcome] = n
		}
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func errorBody(message string) any {
	return map[string]any{"error": map[string]string{"message": message, "type": "fake_provider_error"}}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// writeJSON writes value without HTML escaping; encoding/json sorts map keys,
// so a reply's bytes depend only on its value.
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(encode(value))
}

func encode(value any) []byte {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return []byte(`{"error":"unencodable reply"}`)
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n"))
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

// sortedKeys are m's keys in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
