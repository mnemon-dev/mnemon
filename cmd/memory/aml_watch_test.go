package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gofrs/flock"
	"github.com/mnemon-dev/mnemon/internal/aml"
	"github.com/mnemon-dev/mnemon/internal/memoryagent"
)

// The watcher's world is fake: an aml-serve's admin and public listeners and a
// webhook, each an httptest server whose answers a test sets, and a clock a
// test moves. Nothing here waits for time to pass.

// watchToken is the secret part of the test webhook's URL.
const watchToken = "s3cr3t-t0k3n-4f9a"

// watchWantTag starts every message. It is spelled out here, not taken from the
// watcher: the runbook has operators register its word (mnemon-aml) as the
// Feishu bot's keyword, so changing it must fail a test.
const watchWantTag = "[mnemon-aml] "

var (
	// watchNow is the clock's first reading; the fake server started a day
	// before it.
	watchNow     = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	watchStarted = watchNow.Add(-24 * time.Hour)
)

// watchHealthy is the status of a server that is well: nothing counted, every
// breaker closed, no request rejected, the accounts a fifth spent, no backlog.
func watchHealthy(started time.Time) aml.AdminStatus {
	return aml.AdminStatus{
		StartedAt: started, Adds: 900, Searches: 500, SearchesLast10m: 100, LagBound: 160000,
		Breakers:       map[string]string{"deepseek": "closed", "jev": "closed", "openai": "closed"},
		BreakerTrips:   map[string]int64{"deepseek": 0, "jev": 0, "openai": 0},
		RejectedByKind: map[string]int64{"deepseek": 0, "jev": 0, "openai": 0},
		Budgets:        []memoryagent.BudgetAccount{{Kind: "deepseek", Cap: 600, Spent: 120}, {Kind: "jev", Cap: 60, Spent: 12}},
	}
}

// watchStack is what aml-watch talks to.
type watchStack struct {
	admin, public, hook *httptest.Server

	mu           sync.Mutex
	status       aml.AdminStatus
	statusRaw    string // served instead of status when set
	statusCode   int    // 0 for 200
	readyCode    int    // 0 for 200
	readyReasons []string
	healthCode   int    // 0 for 200
	hookCode     int    // 0 for 200
	hookBody     string // "" for the answer of a webhook that took the message
	hookHandler  http.HandlerFunc
	hookKeyword  string // a Feishu bot's keyword: a text without it is answered 200 with code 19024
	posts        []watchPost
	taken        int

	// adminToken is the bearer token the admin listener demands, "" for none;
	// adminRedirect, where it sends every request instead, when set. adminAuth
	// and publicAuth are the Authorization header of every request the admin
	// and the public listener received, in order.
	adminToken, adminRedirect string
	adminAuth, publicAuth     []string
}

type watchPost struct {
	path, contentType string
	body              []byte
}

func newWatchStack(t *testing.T) *watchStack {
	t.Helper()
	s := &watchStack{status: watchHealthy(watchStarted)}
	admin := http.NewServeMux()
	admin.HandleFunc("GET /ready", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.readyCode != 0 && s.readyCode != http.StatusOK {
			w.WriteHeader(s.readyCode)
			json.NewEncoder(w).Encode(map[string]any{"ready": false, "reasons": s.readyReasons})
			return
		}
		io.WriteString(w, `{"ready":true}`)
	})
	admin.HandleFunc("GET /admin/status", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.statusCode != 0 && s.statusCode != http.StatusOK {
			w.WriteHeader(s.statusCode)
			return
		}
		if s.statusRaw != "" {
			io.WriteString(w, s.statusRaw)
			return
		}
		json.NewEncoder(w).Encode(s.status)
	})
	public := http.NewServeMux()
	public.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.publicAuth = append(s.publicAuth, r.Header.Get("Authorization"))
		if s.healthCode != 0 {
			w.WriteHeader(s.healthCode)
		}
	})
	hook := http.NewServeMux()
	hook.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		handler := s.hookHandler
		s.posts = append(s.posts, watchPost{path: r.Method + " " + r.URL.RequestURI(), contentType: r.Header.Get("Content-Type"), body: body})
		code, answer, keyword := s.hookCode, s.hookBody, s.hookKeyword
		s.mu.Unlock()
		if keyword != "" && !strings.Contains(string(body), keyword) {
			answer = `{"code":19024,"msg":"Key Words Not Found"}`
		}
		if handler != nil {
			handler(w, r)
			return
		}
		if code != 0 {
			w.WriteHeader(code)
		}
		if answer == "" {
			answer = `{"StatusCode":0,"StatusMessage":"success","code":0,"msg":"success"}`
		}
		io.WriteString(w, answer)
	})
	// The admin listener keeps the Authorization header of every request, and
	// answers 401 to one without the token when it demands one, as aml-serve's
	// does.
	guarded := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		s.mu.Lock()
		s.adminAuth = append(s.adminAuth, auth)
		token, redirect := s.adminToken, s.adminRedirect
		s.mu.Unlock()
		switch {
		case token != "" && auth != "Bearer "+token:
			w.Header().Set("WWW-Authenticate", `Bearer realm="mnemon-admin"`)
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"error":"missing or invalid admin token"}`)
		case redirect != "":
			http.Redirect(w, r, redirect, http.StatusFound)
		default:
			admin.ServeHTTP(w, r)
		}
	})
	s.admin, s.public, s.hook = httptest.NewServer(guarded), httptest.NewServer(public), httptest.NewServer(hook)
	t.Cleanup(s.admin.Close)
	t.Cleanup(s.public.Close)
	t.Cleanup(s.hook.Close)
	return s
}

// webhookURL is the webhook as an operator configures it: its path is secret.
func (s *watchStack) webhookURL() string { return s.hook.URL + "/hook/" + watchToken }

// authorizations are the Authorization headers the admin and the public
// listener have received, in order ("" for a request without one).
func (s *watchStack) authorizations() (admin, public []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.adminAuth), slices.Clone(s.publicAuth)
}

// set changes the status the fake server answers with.
func (s *watchStack) set(edit func(*aml.AdminStatus)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	edit(&s.status)
}

// configure changes how the fake answers.
func (s *watchStack) configure(edit func(*watchStack)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	edit(s)
}

// attempts counts the posts the webhook received.
func (s *watchStack) attempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.posts)
}

// take returns the texts of the messages posted since the last call, whatever
// the webhook answered, whichever the payload kind.
func (s *watchStack) take(t *testing.T) []string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	texts := []string{}
	for _, post := range s.posts[s.taken:] {
		texts = append(texts, postedText(t, post.body))
	}
	s.taken = len(s.posts)
	return texts
}

// postedText is the text a payload carries: Feishu's content.text, WeCom's
// text.content or Telegram's text.
func postedText(t *testing.T, body []byte) string {
	t.Helper()
	var payload struct {
		Content struct{ Text string }
		Text    json.RawMessage
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err, string(body))
	}
	if len(payload.Text) == 0 {
		return payload.Content.Text
	}
	var telegram string
	if json.Unmarshal(payload.Text, &telegram) == nil {
		return telegram
	}
	var wecom struct{ Content string }
	if err := json.Unmarshal(payload.Text, &wecom); err != nil {
		t.Fatal(err, string(body))
	}
	return wecom.Content
}

// watchChat is the chat of the test's Telegram bot: a group's, so negative.
const watchChat = "-1001234567890"

// telegramTook is how Telegram answers a message it took.
const telegramTook = `{"ok":true,"result":{"message_id":7,"chat":{"id":-1001234567890,"type":"supergroup"},"date":1761307200}}`

// telegramURL is a Telegram bot's sendMessage on the fake webhook, as an
// operator configures it: the bot's token in the path (watchToken stands for
// it), the chat in the query.
func (s *watchStack) telegramURL(chat string) string {
	return s.hook.URL + "/bot" + watchToken + "/sendMessage?chat_id=" + chat
}

// newTelegramRig is a watch rig whose webhook is a Telegram bot, which takes
// every message until a test says otherwise.
func newTelegramRig(t *testing.T) *watchRig {
	t.Helper()
	r := newWatchRig(t)
	r.w.webhook, r.w.kind = r.stack.telegramURL(watchChat), "telegram"
	r.stack.configure(func(s *watchStack) { s.hookBody = telegramTook })
	return r
}

// watchClock is the watcher's clock.
type watchClock struct{ at time.Time }

func (c *watchClock) now() time.Time { return c.at }

// watchRig is one watcher, its clock and its world.
type watchRig struct {
	t     *testing.T
	stack *watchStack
	clock *watchClock
	w     *watcher
	logs  *bytes.Buffer
	state string
}

func newWatchRig(t *testing.T) *watchRig {
	t.Helper()
	stack := newWatchStack(t)
	r := &watchRig{t: t, stack: stack, clock: &watchClock{at: watchNow}, logs: &bytes.Buffer{}, state: filepath.Join(t.TempDir(), "state", "watch-state.json")}
	r.w = newWatcher(watchSettings{
		admin: stack.admin.URL, public: stack.public.URL, state: r.state, webhook: stack.webhookURL(), kind: "feishu",
	}, slog.New(slog.NewTextHandler(r.logs, nil)))
	r.w.now, r.w.host = r.clock.now, "aml-test"
	return r
}

// step moves the clock on, runs the watcher once and returns what it posted.
func (r *watchRig) step(after time.Duration) []string {
	r.t.Helper()
	texts, err := r.stepErr(after)
	if err != nil {
		r.t.Fatal(err)
	}
	return texts
}

func (r *watchRig) stepErr(after time.Duration) ([]string, error) {
	r.t.Helper()
	r.clock.at = r.clock.at.Add(after)
	err := r.w.run(context.Background())
	return r.stack.take(r.t), err
}

// saved reads the state file back, field by field.
func (r *watchRig) saved() map[string]json.RawMessage {
	r.t.Helper()
	raw, err := os.ReadFile(r.state)
	if err != nil {
		r.t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		r.t.Fatal(err, string(raw))
	}
	return fields
}

// firing lists the alerts in the state file, each with whether its last
// message went out.
func (r *watchRig) firing() map[string]bool {
	r.t.Helper()
	var firing map[string]struct {
		LastSent time.Time `json:"lastSent"`
	}
	if err := json.Unmarshal(r.saved()["firing"], &firing); err != nil {
		r.t.Fatal(err)
	}
	out := map[string]bool{}
	for name, alert := range firing {
		out[name] = !alert.LastSent.IsZero()
	}
	return out
}

// onlyAlert checks that texts is one message of the named alert and returns it.
func onlyAlert(t *testing.T, texts []string, alert string) string {
	t.Helper()
	if len(texts) != 1 || !strings.Contains(texts[0], " "+alert+" on aml-test") {
		t.Fatalf("want one message for %s, got %q", alert, texts)
	}
	return texts[0]
}

func TestWatchFiresAndRepeats(t *testing.T) {
	r := newWatchRig(t)
	r.stack.set(func(s *aml.AdminStatus) { s.Degraded = 5 })

	// The first run only takes the counters as they are.
	if texts := r.step(0); len(texts) != 0 {
		t.Fatal("the first run said", texts)
	}

	// A degraded Search is one message, with the alert, the value, the host
	// and the time in UTC.
	r.stack.set(func(s *aml.AdminStatus) { s.Degraded = 6 })
	first := onlyAlert(t, r.step(time.Minute), "degraded-search")
	for _, want := range []string{watchWantTag + "ALERT degraded-search on aml-test\n1 new degraded Search (6 since the process started)\n2026-10-24 12:01:00 UTC"} {
		if !strings.Contains(first, want) {
			t.Fatalf("%q lacks %q", first, want)
		}
	}
	if len(first) >= 1000 {
		t.Fatal(len(first))
	}

	// While it goes on, a run within 30 minutes of the message is silent,
	// however many Searches degrade: one more every five minutes, to 12:30.
	degraded := int64(6)
	for minute := 2; minute <= 30; minute++ {
		if minute%5 == 0 {
			degraded++
			r.stack.set(func(s *aml.AdminStatus) { s.Degraded = degraded })
		}
		if texts := r.step(time.Minute); len(texts) != 0 {
			t.Fatalf("at 12:%02d: %q", minute, texts)
		}
	}

	// Thirty minutes after the message, with the counter still moving, it is
	// said again.
	repeat := onlyAlert(t, r.step(time.Minute), "degraded-search")
	if !strings.HasPrefix(repeat, watchWantTag+"ALERT degraded-search on aml-test (still firing after 30m0s)\n") || !strings.Contains(repeat, "2026-10-24 12:31:00 UTC") {
		t.Fatal(repeat)
	}

	// Then the counter is stable. The alert stays for ten minutes after the
	// last new degraded Search (12:30), without a word…
	for minute := 32; minute < 40; minute++ {
		if texts := r.step(time.Minute); len(texts) != 0 {
			t.Fatalf("at 12:%02d: %q", minute, texts)
		}
	}
	// …and is then resolved, once.
	resolved := onlyAlert(t, r.step(time.Minute), "degraded-search")
	if !strings.HasPrefix(resolved, watchWantTag+"RESOLVED degraded-search on aml-test (after 39m0s)\n") || !strings.Contains(resolved, "2026-10-24 12:40:00 UTC") ||
		!strings.Contains(resolved, "\nwas: 12 degraded Searches since the process started") {
		t.Fatal(resolved)
	}
	if texts := r.step(time.Minute); len(texts) != 0 {
		t.Fatal("after it was resolved:", texts)
	}
	if got := r.firing(); len(got) != 0 {
		t.Fatal("the state still has", got)
	}
}

// A repeat is due 30 minutes after the last message, not a second before.
func TestWatchRepeatsAtThirtyMinutesExactly(t *testing.T) {
	r := newWatchRig(t)
	r.step(0)
	r.stack.configure(func(s *watchStack) { s.healthCode = http.StatusBadGateway })
	onlyAlert(t, r.step(time.Minute), "health")
	if texts := r.step(29*time.Minute + 59*time.Second); len(texts) != 0 {
		t.Fatal("29m59s later:", texts)
	}
	repeat := onlyAlert(t, r.step(time.Second), "health")
	if !strings.Contains(repeat, "still firing after 30m0s") {
		t.Fatal(repeat)
	}
	// The next repeat counts from the repeat.
	if texts := r.step(29*time.Minute + 59*time.Second); len(texts) != 0 {
		t.Fatal(texts)
	}
	onlyAlert(t, r.step(time.Second), "health")
}

func TestWatchPayloadKinds(t *testing.T) {
	for _, tc := range []struct {
		kind, opening string
	}{
		{"feishu", `{"msg_type":"text","content":{"text":`},
		{"wecom", `{"msgtype":"text","text":{"content":`},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			r := newWatchRig(t)
			r.w.kind = tc.kind
			r.stack.configure(func(s *watchStack) { s.healthCode = http.StatusBadGateway })
			texts := r.step(0)
			wantText := fmt.Sprintf(watchWantTag+"ALERT health on aml-test\nGET %s/health answered 502\n2026-10-24 12:00:00 UTC", r.stack.public.URL)
			if len(texts) != 1 || texts[0] != wantText {
				t.Fatalf("got %q, want %q", texts, wantText)
			}
			r.stack.mu.Lock()
			post := r.stack.posts[0]
			r.stack.mu.Unlock()
			// The body is exactly the payload of the kind: its key order, no
			// other key, the text as one JSON string.
			quoted, _ := json.Marshal(wantText)
			if want := tc.opening + string(quoted) + `}}`; string(post.body) != want {
				t.Fatalf("body %s\nwant %s", post.body, want)
			}
			if post.path != "POST /hook/"+watchToken || !strings.HasPrefix(post.contentType, "application/json") {
				t.Fatal(post.path, post.contentType)
			}
		})
	}

	// The text is carried as it is, not HTML-escaped.
	r := newWatchRig(t)
	r.stack.configure(func(s *watchStack) {
		s.readyCode, s.readyReasons = http.StatusServiceUnavailable, []string{"free disk <1 GiB & falling"}
	})
	r.step(0)
	r.stack.mu.Lock()
	raw := string(r.stack.posts[0].body)
	r.stack.mu.Unlock()
	if !strings.Contains(raw, `free disk <1 GiB & falling`) {
		t.Fatal(raw)
	}

	// Anything else is refused before a message is built, and the value is
	// not echoed.
	for _, kind := range []string{"", "slack", "Feishu "} {
		r := newWatchRig(t)
		r.w.kind = kind
		r.stack.configure(func(s *watchStack) { s.healthCode = http.StatusBadGateway })
		texts, err := r.stepErr(0)
		if err == nil || !strings.Contains(err.Error(), "AML_ALERT_KIND must be feishu or wecom") || len(texts) != 0 || strings.Contains(err.Error(), watchToken) {
			t.Fatalf("kind %q: %q %v", kind, texts, err)
		}
		if _, statErr := os.Stat(r.state); !os.IsNotExist(statErr) {
			t.Fatalf("kind %q: a refused run wrote state: %v", kind, statErr)
		}
	}
}

func TestWatchDetectsRestart(t *testing.T) {
	r := newWatchRig(t)
	r.stack.set(func(s *aml.AdminStatus) { s.Degraded, s.Dropped = 4, 2 })
	if texts := r.step(0); len(texts) != 0 {
		t.Fatal(texts)
	}
	// The same process, run after run: no restart.
	if texts := r.step(time.Minute); len(texts) != 0 {
		t.Fatal(texts)
	}

	// A start later than the last run's is a restart, and the counters, which
	// count from the start, are zero again: nothing else is said.
	restarted := watchNow.Add(90 * time.Second)
	r.stack.set(func(s *aml.AdminStatus) { *s = watchHealthy(restarted) })
	message := onlyAlert(t, r.step(time.Minute), "restart") // 12:02
	for _, want := range []string{watchWantTag + "ALERT restart on aml-test", "restarted", "2026-10-24T12:01:30Z", "2026-10-23T12:00:00Z"} {
		if !strings.Contains(message, want) {
			t.Fatalf("%q lacks %q", message, want)
		}
	}
	// The baseline is zero, not the old process's four: a Search that
	// degrades now is seen.
	r.stack.set(func(s *aml.AdminStatus) { s.Degraded = 1 })
	if message = onlyAlert(t, r.step(time.Minute), "degraded-search"); !strings.Contains(message, "1 new") { // 12:03
		t.Fatal(message)
	}

	// A restart is an event: ten minutes after it was seen, it is over; and
	// so, ten minutes after the last degraded Search, is that.
	if texts := r.step(8 * time.Minute); len(texts) != 0 { // 12:11
		t.Fatal(texts)
	}
	if message = onlyAlert(t, r.step(time.Minute), "restart"); !strings.HasPrefix(message, watchWantTag+"RESOLVED restart on aml-test") { // 12:12
		t.Fatal(message)
	}
	if message = onlyAlert(t, r.step(time.Minute), "degraded-search"); !strings.HasPrefix(message, watchWantTag+"RESOLVED degraded-search") { // 12:13
		t.Fatal(message)
	}

	// A process that restarted and counted before the watcher looked: all of
	// it is new.
	again := watchNow.Add(13*time.Minute + 30*time.Second)
	r.stack.set(func(s *aml.AdminStatus) { *s = watchHealthy(again); s.Degraded, s.Dropped = 3, 1 })
	texts := r.step(time.Minute) // 12:14
	if len(texts) != 3 {
		t.Fatal(texts)
	}
	for i, want := range []string{watchWantTag + "ALERT consolidation-dropped on aml-test\n1 new", watchWantTag + "ALERT degraded-search on aml-test\n3 new", watchWantTag + "ALERT restart on aml-test"} {
		if !strings.HasPrefix(texts[i], want) {
			t.Errorf("message %d is %q, want %q", i, texts[i], want)
		}
	}

	// A restart of a restart: the alerts are firing already, and are not said
	// again for another start a minute later (the watcher does not repeat
	// itself every minute through a crash loop)…
	r.stack.set(func(s *aml.AdminStatus) { *s = watchHealthy(again.Add(time.Minute)); s.Degraded = 1 })
	if texts = r.step(time.Minute); len(texts) != 0 { // 12:15
		t.Fatal(texts)
	}
	// …and a server that started earlier than the last one seen is another
	// process too, once they are over.
	if texts = r.step(10 * time.Minute); len(texts) != 3 { // 12:25
		t.Fatal(texts)
	}
	r.stack.set(func(s *aml.AdminStatus) { *s = watchHealthy(watchStarted) })
	if message = onlyAlert(t, r.step(time.Minute), "restart"); !strings.HasPrefix(message, watchWantTag+"ALERT restart") {
		t.Fatal(message)
	}
}

// The webhook is a secret: no output of the watcher shows it, whichever way
// the post fails, on any of its paths: the run that finds the lock held, the
// run that warns the public probe is off, the alert that cannot be delivered
// and the resolution that is owed for it. So for a Feishu hook, whose path is
// its key, and for a Telegram bot, whose token is in the path (the chat in the
// query is not shown either).
func TestWatchNeverPrintsWebhook(t *testing.T) {
	failures := map[string]func(*watchStack){
		"HTTP 500": func(s *watchStack) { s.configure(func(s *watchStack) { s.hookCode = http.StatusInternalServerError }) },
		"HTTP 400 with the path in its body": func(s *watchStack) {
			s.configure(func(s *watchStack) { s.hookCode, s.hookBody = http.StatusBadRequest, "bad webhook /hook/"+watchToken })
		},
		"Feishu code": func(s *watchStack) {
			s.configure(func(s *watchStack) {
				s.hookBody = `{"code":19024,"msg":"Key Words Not Found /hook/` + watchToken + `"}`
			})
		},
		"WeCom code": func(s *watchStack) {
			s.configure(func(s *watchStack) {
				s.hookBody = `{"errcode":93000,"errmsg":"invalid webhook url /hook/` + watchToken + `"}`
			})
		},
		"redirect": func(s *watchStack) {
			s.configure(func(s *watchStack) {
				s.hookHandler = func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Location", "/elsewhere/"+watchToken)
					w.WriteHeader(http.StatusFound)
				}
			})
		},
		"unreachable": func(s *watchStack) { s.hook.Close() },
		"connection cut": func(s *watchStack) {
			s.configure(func(s *watchStack) {
				s.hookHandler = func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }
			})
		},
	}
	// Telegram's own refusals, which describe themselves, here with the path.
	telegramFailures := map[string]func(*watchStack){
		"Telegram error_code with the path in its description": func(s *watchStack) {
			s.configure(func(s *watchStack) {
				s.hookCode, s.hookBody = http.StatusUnauthorized, `{"ok":false,"error_code":401,"description":"Unauthorized: /bot`+watchToken+`/sendMessage"}`
			})
		},
		"Telegram ok false in a 200": func(s *watchStack) {
			s.configure(func(s *watchStack) {
				s.hookBody = `{"ok":false,"error_code":400,"description":"Bad Request: chat ` + watchChat + ` not found"}`
			})
		},
		"Telegram rate limit": func(s *watchStack) {
			s.configure(func(s *watchStack) {
				s.hookCode, s.hookBody = http.StatusTooManyRequests, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 5","parameters":{"retry_after":5}}`
			})
		},
	}
	for _, kind := range []struct {
		name string
		// url is the webhook as an operator configures it, path where its posts
		// go, and took the answer of a webhook that took a message.
		url  func(*watchStack) string
		path string
		took string
	}{
		{"feishu", (*watchStack).webhookURL, "POST /hook/" + watchToken, ""},
		{"telegram", func(s *watchStack) string { return s.telegramURL(watchChat) }, "POST /bot" + watchToken + "/sendMessage", telegramTook},
	} {
		cases := map[string]func(*watchStack){}
		for name, fail := range failures {
			cases[name] = fail
		}
		if kind.name == "telegram" {
			for name, fail := range telegramFailures {
				cases[name] = fail
			}
		}
		for name, fail := range cases {
			t.Run(kind.name+"/"+name, func(t *testing.T) {
				neverPrintsWebhook(t, kind.name, kind.url, kind.path, kind.took, fail)
			})
		}
	}

	// Nor does it show when the post succeeds.
	for _, kind := range []struct{ name, took string }{{"wecom", ""}, {"telegram", telegramTook}} {
		t.Run(kind.name+" delivered", func(t *testing.T) {
			stack := newWatchStack(t)
			var out bytes.Buffer
			command := newAMLWatchCommand()
			command.SetArgs([]string{"--admin", stack.admin.URL, "--state", filepath.Join(t.TempDir(), "watch-state.json")})
			command.SetOut(&out)
			command.SetErr(&out)
			webhook := stack.webhookURL()
			if kind.name == "telegram" {
				webhook = stack.telegramURL(watchChat)
			}
			t.Setenv("AML_ALERT_WEBHOOK", webhook)
			t.Setenv("AML_ALERT_KIND", kind.name)
			stack.configure(func(s *watchStack) {
				s.hookBody = kind.took
				s.readyCode, s.readyReasons = http.StatusServiceUnavailable, []string{"breaker open: jev"}
			})
			if err := command.Execute(); err != nil {
				t.Fatal(err)
			}
			if got := stack.take(t); len(got) != 1 {
				t.Fatalf("%q\n%s", got, out.String())
			}
			neverShows(t, map[string]string{"the output": out.String()}, watchToken, stack.hook.Listener.Addr().String(), watchChat)
		})
	}
}

// neverPrintsWebhook runs the watcher of TestWatchNeverPrintsWebhook through
// its command, with a webhook of kind at url(stack) that fails as fail makes
// it, and checks that no output shows the webhook.
func neverPrintsWebhook(t *testing.T, kind string, url func(*watchStack) string, path, took string, fail func(*watchStack)) {
	t.Helper()
	stack := newWatchStack(t)
	stack.configure(func(s *watchStack) { s.hookBody = took })
	state := filepath.Join(t.TempDir(), "state", "watch-state.json")
	t.Setenv("AML_ALERT_WEBHOOK", url(stack))
	t.Setenv("AML_ALERT_KIND", kind)
	var out bytes.Buffer
	run := func(extra ...string) error {
		command := newAMLWatchCommand()
		command.SetArgs(append([]string{"--admin", stack.admin.URL, "--state", state}, extra...))
		command.SetOut(&out)
		command.SetErr(&out)
		err := command.Execute()
		if err != nil {
			fmt.Fprintln(&out, err) // what the product root prints after an error
		}
		return err
	}
	public := []string{"--public-url", stack.public.URL}
	// A baseline run with no public URL: it warns that the probe is off.
	if err := run(); err != nil || !strings.Contains(out.String(), "the public health probe is off because --public-url is empty") {
		t.Fatalf("%v\n%s", err, out.String())
	}
	// A run that finds another one holding the lock does nothing.
	held := flock.New(state + ".lock")
	if locked, err := held.TryLock(); !locked || err != nil {
		t.Fatal(locked, err)
	}
	err := run(public...)
	if unlockErr := held.Unlock(); unlockErr != nil || err != nil || !strings.Contains(out.String(), "another aml-watch run holds the lock") {
		t.Fatalf("%v %v\n%s", err, unlockErr, out.String())
	}
	// One more degraded Search, work that has waited too long and requests
	// DeepSeek rejected: three alerts to say, to a webhook that fails.
	// (backlog-age sorts first, so that a webhook that cannot be reached,
	// which ends the run at its first failure, is tried with it first.)
	stack.set(func(s *aml.AdminStatus) {
		s.Degraded = 1
		s.Queue.OldestQueued = 20 * time.Minute
		s.RejectedByKind["deepseek"] = 2
	})
	fail(stack)
	err = run(public...)
	if err == nil || !strings.Contains(err.Error(), "not delivered") {
		t.Fatal("a failed post did not fail the run:", err)
	}
	// The work is done: the alert that never went out is resolved, and
	// its resolution fails the same way.
	stack.set(func(s *aml.AdminStatus) { s.Queue.OldestQueued = 0 })
	err = run(public...)
	if err == nil || !strings.Contains(err.Error(), "not delivered") || !strings.Contains(out.String(), "alert=backlog-age event=resolved") {
		t.Fatalf("%v\n%s", err, out.String())
	}
	neverShows(t, map[string]string{"the output": out.String()},
		watchToken, url(stack), stack.hook.Listener.Addr().String(), "/hook/", "/bot", "/elsewhere", watchChat)
	// The state was saved all the same, the messages owed, and
	// without the secret.
	raw, readErr := os.ReadFile(state)
	if readErr != nil || !strings.Contains(string(raw), `"degraded-search"`) || !strings.Contains(string(raw), `"backlog-age"`) ||
		!strings.Contains(string(raw), `"provider-rejected:deepseek"`) || strings.Contains(string(raw), watchToken) {
		t.Errorf("state: %v\n%s", readErr, raw)
	}
	// A redirect is a failure, not an address to post to; a Telegram bot is
	// posted to without the query.
	stack.mu.Lock()
	defer stack.mu.Unlock()
	for _, post := range stack.posts {
		if post.path != path {
			t.Error("a post went to", post.path)
		}
	}
}

// A webhook that answers with something other than success is a failed
// delivery, also when the status is 200: Feishu and WeCom answer a rejected
// message that way.
func TestWatchBusinessErrorsFailTheDelivery(t *testing.T) {
	for _, tc := range []struct {
		kind, body, want string
	}{
		{"feishu", `{"code":19024,"msg":"Key Words Not Found"}`, "rejected the message (code 19024)"},
		{"feishu", `{"StatusCode":9499,"StatusMessage":"Bad Request"}`, "rejected the message (code 9499)"},
		{"wecom", `{"errcode":93000,"errmsg":"invalid webhook url"}`, "rejected the message (code 93000)"},
		{"wecom", `{"errcode":0,"errmsg":"ok"}`, ""},
		{"feishu", `{"StatusCode":0,"StatusMessage":"success","code":0,"data":{},"msg":"success"}`, ""},
		{"feishu", `ok`, ""}, // a webhook that does not answer in JSON took the message
	} {
		r := newWatchRig(t)
		r.w.kind = tc.kind
		r.stack.configure(func(s *watchStack) { s.hookBody = tc.body; s.healthCode = http.StatusBadGateway })
		texts, err := r.stepErr(0)
		if len(texts) != 1 {
			t.Fatal(texts)
		}
		if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
			t.Errorf("%s %s: %v", tc.kind, tc.body, err)
		}
		if tc.want != "" && strings.Contains(err.Error(), "Key Words") {
			t.Error("the webhook's own words are echoed:", err)
		}
	}
}

// A Telegram bot takes a message at its sendMessage URL without the query:
// the chat that the query's chat_id names goes in the body, as a JSON string
// exactly as given, with the watcher's text in plain text (no parse_mode) and
// no link preview.
func TestWatchTelegramPayload(t *testing.T) {
	for _, chat := range []string{watchChat, "123456789", "@mnemon_alerts"} {
		t.Run(chat, func(t *testing.T) {
			r := newTelegramRig(t)
			r.w.webhook = r.stack.telegramURL(chat)
			r.stack.configure(func(s *watchStack) { s.healthCode = http.StatusBadGateway })
			texts := r.step(0)
			wantText := fmt.Sprintf(watchWantTag+"ALERT health on aml-test\nGET %s/health answered 502\n2026-10-24 12:00:00 UTC", r.stack.public.URL)
			if len(texts) != 1 || texts[0] != wantText {
				t.Fatalf("got %q, want %q", texts, wantText)
			}
			r.stack.mu.Lock()
			post := r.stack.posts[0]
			r.stack.mu.Unlock()
			// The body is exactly sendMessage's: its key order, no other key.
			quoted, _ := json.Marshal(wantText)
			if want := `{"chat_id":"` + chat + `","text":` + string(quoted) + `,"disable_web_page_preview":true}`; string(post.body) != want {
				t.Fatalf("body %s\nwant %s", post.body, want)
			}
			if post.path != "POST /bot"+watchToken+"/sendMessage" || !strings.HasPrefix(post.contentType, "application/json") {
				t.Fatal(post.path, post.contentType)
			}
			if got := r.firing(); len(got) != 1 || !got["health"] {
				t.Fatal("the message Telegram took is not recorded as sent:", got)
			}
		})
	}

	// The text is carried as it is: not HTML-escaped, and with no parse_mode
	// nothing in it is read as markup.
	r := newTelegramRig(t)
	r.stack.configure(func(s *watchStack) {
		s.readyCode, s.readyReasons = http.StatusServiceUnavailable, []string{"free disk <1 GiB & *falling* _fast_"}
	})
	r.step(0)
	r.stack.mu.Lock()
	raw := string(r.stack.posts[0].body)
	r.stack.mu.Unlock()
	if !strings.Contains(raw, `free disk <1 GiB & *falling* _fast_`) || strings.Contains(raw, "parse_mode") {
		t.Fatal(raw)
	}

	// The longest message is the watcher's text, under 1,000 bytes: well
	// within Telegram's 4,096 characters.
	r = newTelegramRig(t)
	reasons := []string{}
	for i := range 80 {
		reasons = append(reasons, fmt.Sprintf("data root is not writable: open /var/lib/mnemon-aml/users/%08d: permission denied — é", i))
	}
	r.stack.configure(func(s *watchStack) { s.readyCode, s.readyReasons = http.StatusServiceUnavailable, reasons })
	r.w.host = strings.Repeat("h", 200)
	if texts := r.step(0); len(texts) != 1 || len(texts[0]) >= 1000 || !strings.HasPrefix(texts[0], watchWantTag+"ALERT ready on ") {
		t.Fatalf("%q", texts)
	}
}

// Telegram took a message only when it answers 2xx with "ok":true. Any other
// answer, 429 included, is a failed delivery: the failure says the HTTP status
// and Telegram's numeric error_code, never its description, the run does not
// wait for a retry_after, and the next run sends the message again.
func TestWatchTelegramRejection(t *testing.T) {
	for _, tc := range []struct {
		code       int
		body, want string
	}{
		{http.StatusOK, `{"ok":false,"error_code":400}`, "the webhook answered HTTP 200 with error_code 400"},
		{http.StatusBadRequest, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`, "the webhook answered HTTP 400 with error_code 400"},
		{http.StatusTooManyRequests, `{"ok":false,"error_code":429,"parameters":{"retry_after":5}}`, "the webhook answered HTTP 429 with error_code 429"},
		// A retry_after longer than the run's bound: a run that waited for it
		// would show.
		{http.StatusTooManyRequests, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 30","parameters":{"retry_after":30}}`, "the webhook answered HTTP 429 with error_code 429"},
		// Answers that are not Telegram's say their status alone.
		{http.StatusOK, `{"ok":false}`, `the webhook answered HTTP 200 without "ok":true`},
		{http.StatusOK, `{"StatusCode":0,"StatusMessage":"success","code":0,"msg":"success"}`, `the webhook answered HTTP 200 without "ok":true`},
		{http.StatusOK, `ok`, `the webhook answered HTTP 200 without "ok":true`},
		{http.StatusOK, `{"ok":"true"}`, `the webhook answered HTTP 200 without "ok":true`},
		{http.StatusBadGateway, `<html>Bad Gateway</html>`, "the webhook answered HTTP 502"},
		{http.StatusInternalServerError, `{"ok":true}`, "the webhook answered HTTP 500"},
	} {
		t.Run(fmt.Sprintf("%d %s", tc.code, tc.body), func(t *testing.T) {
			r := newTelegramRig(t)
			r.stack.configure(func(s *watchStack) { s.hookCode, s.hookBody, s.healthCode = tc.code, tc.body, http.StatusBadGateway })
			start := time.Now()
			texts, err := r.stepErr(0)
			if len(texts) != 1 || err == nil || !strings.Contains(err.Error(), "("+tc.want+")") {
				t.Fatalf("%q %v, want the reason %q", texts, err, tc.want)
			}
			if took := time.Since(start); took > 10*time.Second {
				t.Fatalf("the run took %s: it waited for Telegram", took)
			}
			// Nothing of the answer but its numbers reaches the output.
			neverShows(t, map[string]string{"the error": err.Error(), "the log": r.logs.String()},
				"description", "Bad Request", "chat not found", "Too Many", "retry", "parameters", "Bad Gateway", watchToken, watchChat)
			// The message is owed, and the next run sends it.
			if got := r.firing(); len(got) != 1 || got["health"] {
				t.Fatal("the message is not kept as unsent:", got)
			}
			r.stack.configure(func(s *watchStack) { s.hookCode, s.hookBody = 0, telegramTook })
			onlyAlert(t, r.step(time.Minute), "health")
			if got := r.firing(); !got["health"] {
				t.Fatal("the message Telegram took is not recorded as sent:", got)
			}
		})
	}

	// Any 2xx with "ok":true is a delivery.
	for _, tc := range []struct {
		code int
		body string
	}{{http.StatusOK, telegramTook}, {http.StatusCreated, `{"ok":true}`}} {
		r := newTelegramRig(t)
		r.stack.configure(func(s *watchStack) { s.hookCode, s.hookBody, s.healthCode = tc.code, tc.body, http.StatusBadGateway })
		if texts, err := r.stepErr(0); err != nil || len(texts) != 1 {
			t.Errorf("%d %s: %q %v", tc.code, tc.body, texts, err)
		}
	}
}

// A Telegram webhook names its chat in the query: chat_id, its only
// parameter, a chat's number (a group's is negative) or a channel's
// @username. A missing or malformed one is refused at start, as is a URL that
// is not a bot's sendMessage; the error names AML_ALERT_WEBHOOK and the rule,
// never the value, which holds the bot's token. Any https host will do (a
// self-hosted Bot API server), or http to this machine.
func TestWatchTelegramNeedsChatID(t *testing.T) {
	bot := "https://api.telegram.org/bot" + watchToken
	for webhook, ok := range map[string]bool{
		bot + "/sendMessage?chat_id=123456789":                                  true,
		bot + "/sendMessage?chat_id=" + watchChat:                               true,
		bot + "/sendMessage?chat_id=@mnemon_alerts":                             true,
		bot + "/sendMessage?chat_id=%40mnemon_alerts":                           true,
		bot + "/sendMessage?chat_id=" + strings.Repeat("9", 20):                 true,
		bot + "/sendMessage?chat_id=@" + "a" + strings.Repeat("b", 31):          true,
		"https://bots.example.com/bot" + watchToken + "/sendMessage?chat_id=42": true,
		"http://127.0.0.1:8081/bot" + watchToken + "/sendMessage?chat_id=42":    true,
		"http://localhost:8081/bot" + watchToken + "/sendMessage?chat_id=42":    true,
		bot + "/sendMessage":                                                       false,
		bot + "/sendMessage?":                                                      false,
		bot + "/sendMessage?chat_id=":                                              false,
		bot + "/sendMessage?chat_id=abc":                                           false,
		bot + "/sendMessage?chat_id=12.5":                                          false,
		bot + "/sendMessage?chat_id=%2B42":                                         false,
		bot + "/sendMessage?chat_id=--42":                                          false,
		bot + "/sendMessage?chat_id=" + strings.Repeat("9", 21):                    false,
		bot + "/sendMessage?chat_id=@abcd":                                         false,
		bot + "/sendMessage?chat_id=@1channel":                                     false,
		bot + "/sendMessage?chat_id=@" + "a" + strings.Repeat("b", 32):             false,
		bot + "/sendMessage?chat_id=42%0A":                                         false,
		bot + "/sendMessage?chat_id=42&chat_id=43":                                 false,
		bot + "/sendMessage?chat_id=42&parse_mode=HTML":                            false,
		bot + "/sendMessage?chat_id=42;x=1":                                        false,
		bot + "/sendMessage?chat=42":                                               false,
		bot + "/getUpdates?chat_id=42":                                             false,
		bot + "/sendMessage/?chat_id=42":                                           false,
		bot + "/sendmessage?chat_id=42":                                            false,
		bot + "?chat_id=42":                                                        false,
		"http://api.telegram.org/bot" + watchToken + "/sendMessage?chat_id=42":     false,
		"https:///bot" + watchToken + "/sendMessage?chat_id=42":                    false,
		"api.telegram.org/bot" + watchToken + "/sendMessage?chat_id=42":            false,
		"https://api.telegram.org/bot" + watchToken + "/sendMessage?chat_id=4%zz2": false,
		"https://api.telegram.org/bot" + watchToken + "%zz/sendMessage?chat_id=42": false,
	} {
		w := &watcher{watchSettings: watchSettings{webhook: webhook, kind: "telegram"}}
		err := w.checkDelivery()
		if (err == nil) != ok {
			t.Errorf("%q: %v, want accepted %v", webhook, err, ok)
		}
		if err != nil && (strings.Contains(err.Error(), watchToken) || !strings.Contains(err.Error(), "AML_ALERT_WEBHOOK")) {
			t.Errorf("%q: %v", webhook, err)
		}
	}

	// Through the command, the run is refused before it reads or writes
	// anything, and nothing of the value is echoed.
	state := filepath.Join(t.TempDir(), "watch-state.json")
	for name, webhook := range map[string]string{
		"no chat_id":      bot + "/sendMessage",
		"chat_id abc":     bot + "/sendMessage?chat_id=abc",
		"empty chat_id":   bot + "/sendMessage?chat_id=",
		"not sendMessage": bot + "/getUpdates?chat_id=42",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("AML_ALERT_WEBHOOK", webhook)
			t.Setenv("AML_ALERT_KIND", "telegram")
			out, err := executeAMLWatch("--state", state)
			if err == nil || !strings.Contains(err.Error(), "AML_ALERT_WEBHOOK") || !strings.Contains(err.Error(), "chat_id") {
				t.Fatalf("%v\n%s", err, out)
			}
			neverShows(t, map[string]string{"the error": err.Error(), "the output": out}, watchToken, "abc", "42", "getUpdates")
			if _, statErr := os.Stat(state); !os.IsNotExist(statErr) {
				t.Errorf("a refused run wrote state: %v", statErr)
			}
		})
	}
}

// A message that did not go out stays unsent in the state, which is saved,
// the run exits non-zero, and the next run says it.
func TestWatchDeliveryFailureIsRetried(t *testing.T) {
	r := newWatchRig(t)
	r.step(0)
	r.stack.configure(func(s *watchStack) { s.hookCode = http.StatusInternalServerError })
	r.stack.set(func(s *aml.AdminStatus) { s.Degraded = 1 })
	texts, err := r.stepErr(time.Minute)
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") || len(texts) != 1 {
		t.Fatalf("%q %v", texts, err)
	}
	if got := r.firing(); len(got) != 1 || got["degraded-search"] {
		t.Fatal("the alert is not recorded as unsent:", got)
	}
	// The webhook is back: the message goes out as the alert it is (nobody
	// has heard of it, so it is not "still firing"), and then the alert is
	// quiet again.
	r.stack.configure(func(s *watchStack) { s.hookCode = 0 })
	if message := onlyAlert(t, r.step(time.Minute), "degraded-search"); !strings.HasPrefix(message, watchWantTag+"ALERT degraded-search") || strings.Contains(message, "still firing") {
		t.Fatal(message)
	}
	if got := r.firing(); !got["degraded-search"] {
		t.Fatal(got)
	}
	if texts = r.step(time.Minute); len(texts) != 0 {
		t.Fatal(texts)
	}

	// A "resolved" that cannot be delivered is kept and tried again.
	r.stack.configure(func(s *watchStack) { s.hookCode = http.StatusInternalServerError })
	texts, err = r.stepErr(10 * time.Minute)
	if err == nil || len(texts) != 1 || !strings.HasPrefix(texts[0], watchWantTag+"RESOLVED degraded-search") {
		t.Fatalf("%q %v", texts, err)
	}
	if got := r.firing(); len(got) != 1 {
		t.Fatal("the unsent resolution is lost:", got)
	}
	r.stack.configure(func(s *watchStack) { s.hookCode = 0 })
	if message := onlyAlert(t, r.step(time.Minute), "degraded-search"); !strings.HasPrefix(message, watchWantTag+"RESOLVED degraded-search") {
		t.Fatal(message)
	}
	if got := r.firing(); len(got) != 0 {
		t.Fatal(got)
	}
	if texts = r.step(time.Minute); len(texts) != 0 {
		t.Fatal(texts)
	}

	// An alert that fired and ended while the webhook was down never reached
	// anybody. Its resolution is owed all the same: it says the alert itself
	// was not delivered, it is kept (one entry for the alert) and tried again
	// each run until the webhook takes it.
	r.stack.set(func(s *aml.AdminStatus) { s.Degraded = 2 })
	r.stack.configure(func(s *watchStack) { s.hookCode = http.StatusInternalServerError })
	if _, err = r.stepErr(time.Minute); err == nil {
		t.Fatal("no error")
	}
	texts, err = r.stepErr(10 * time.Minute) // the ten minutes the alert is held are over
	if err == nil || len(texts) != 1 {
		t.Fatalf("%q %v", texts, err)
	}
	lines := strings.Split(texts[0], "\n")
	if len(lines) != 3 || lines[0] != watchWantTag+"RESOLVED degraded-search on aml-test (after 10m0s, alert not delivered)" ||
		lines[1] != "was: 1 new degraded Search (2 since the process started)" || !strings.HasSuffix(lines[2], " UTC") {
		t.Fatalf("%q", texts[0])
	}
	if got := r.firing(); len(got) != 1 || got["degraded-search"] {
		t.Fatal("the unsent resolution is not kept as owed:", got)
	}
	texts, err = r.stepErr(time.Minute)
	if err == nil || len(texts) != 1 || !strings.HasPrefix(texts[0], watchWantTag+"RESOLVED degraded-search on aml-test (after 11m0s, alert not delivered)\n") {
		t.Fatalf("the owed resolution was not tried again: %q %v", texts, err)
	}
	if got := r.firing(); len(got) != 1 {
		t.Fatal(got)
	}
	r.stack.configure(func(s *watchStack) { s.hookCode = 0 })
	if message := onlyAlert(t, r.step(time.Minute), "degraded-search"); !strings.HasPrefix(message, watchWantTag+"RESOLVED degraded-search on aml-test (after 12m0s, alert not delivered)\n") {
		t.Fatal(message)
	}
	if got := r.firing(); len(got) != 0 {
		t.Fatal(got)
	}
	if texts = r.step(time.Minute); len(texts) != 0 {
		t.Fatal("a resolution was said twice:", texts)
	}

	// An alert that fires again before its unsent resolution went out is
	// just firing: nobody was told it stopped.
	r.stack.set(func(s *aml.AdminStatus) { s.Degraded = 3 })
	onlyAlert(t, r.step(time.Minute), "degraded-search")
	r.stack.configure(func(s *watchStack) { s.hookCode = http.StatusInternalServerError })
	if _, err = r.stepErr(10 * time.Minute); err == nil {
		t.Fatal("no error")
	}
	r.stack.configure(func(s *watchStack) { s.hookCode = 0 })
	r.stack.set(func(s *aml.AdminStatus) { s.Degraded = 4 })
	if texts = r.step(time.Minute); len(texts) != 0 {
		t.Fatal("an alert that never stopped, as far as anybody knows, was said again:", texts)
	}
	if got := r.firing(); len(got) != 1 || !got["degraded-search"] {
		t.Fatal(got)
	}
}

// roundTripFunc is an http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Each alert is its own message and one that is rejected does not hold up the
// others; a webhook that cannot be reached is tried once per run, so that a
// run ends in bounded time with its state saved.
func TestWatchStopsAtAnUnreachableWebhook(t *testing.T) {
	r := newWatchRig(t)
	r.stack.configure(func(s *watchStack) {
		s.healthCode = http.StatusBadGateway
		s.readyCode, s.readyReasons = http.StatusServiceUnavailable, []string{"breaker open: jev"}
	})
	// Rejected: both are attempted.
	r.stack.configure(func(s *watchStack) { s.hookCode = http.StatusBadRequest })
	texts, err := r.stepErr(0)
	if err == nil || len(texts) != 2 || !strings.Contains(err.Error(), "2 of 2") {
		t.Fatalf("%q %v", texts, err)
	}

	// Unreachable: one attempt.
	r = newWatchRig(t)
	r.stack.configure(func(s *watchStack) {
		s.healthCode = http.StatusBadGateway
		s.readyCode, s.readyReasons = http.StatusServiceUnavailable, []string{"breaker open: jev"}
	})
	attempts := 0
	r.w.hooks = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		return nil, &errTransport{request.URL.String()}
	})}
	_, err = r.stepErr(0)
	if err == nil || attempts != 1 || !strings.Contains(err.Error(), "2 of 2") || strings.Contains(err.Error(), watchToken) {
		t.Fatal(attempts, err)
	}
	if got := r.firing(); len(got) != 2 || got["health"] || got["ready"] {
		t.Fatal(got)
	}
}

// errTransport is a transport failure whose text names the URL, as net/http's
// do.
type errTransport struct{ url string }

func (e *errTransport) Error() string { return "dial tcp: lookup " + e.url + ": no such host" }

func TestWatchWithoutWebhookLogsAndExitsZero(t *testing.T) {
	r := newWatchRig(t)
	r.w.webhook, r.w.kind = "", ""
	r.step(0)
	r.stack.set(func(s *aml.AdminStatus) { s.Degraded = 2 })
	texts, err := r.stepErr(time.Minute)
	if err != nil || len(texts) != 0 || r.stack.attempts() != 0 {
		t.Fatal(texts, err)
	}
	for _, want := range []string{"AML_ALERT_WEBHOOK is empty", "ALERT degraded-search on aml-test", "2 new"} {
		if !strings.Contains(r.logs.String(), want) {
			t.Fatalf("the log lacks %q:\n%s", want, r.logs)
		}
	}
	// The state is kept all the same, the alert not delivered…
	if got := r.firing(); len(got) != 1 || got["degraded-search"] {
		t.Fatal(got)
	}
	// …so that once the webhook is configured, what fires is said.
	r.w.webhook, r.w.kind = r.stack.webhookURL(), "wecom"
	onlyAlert(t, r.step(time.Minute), "degraded-search")

	// A resolution is owed, whether its alert reached the webhook or not: with
	// no webhook it is logged and kept, and posted once there is one.
	r.w.webhook, r.w.kind = "", ""
	r.logs.Reset()
	if texts, err = r.stepErr(30 * time.Minute); err != nil || len(texts) != 0 {
		t.Fatal(texts, err)
	}
	if !strings.Contains(r.logs.String(), watchWantTag+"RESOLVED degraded-search on aml-test (after 31m0s)") {
		t.Fatalf("the resolution was not logged:\n%s", r.logs)
	}
	if got := r.firing(); len(got) != 1 {
		t.Fatal("the owed resolution is not kept:", got)
	}
	// An alert that fires and clears while there is no webhook was never
	// delivered; its resolution says so, and is posted with the other.
	r.stack.set(func(s *aml.AdminStatus) { s.Dropped = 1 })
	if _, err = r.stepErr(time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err = r.stepErr(10 * time.Minute); err != nil {
		t.Fatal(err)
	}
	if got := r.firing(); len(got) != 2 || got["consolidation-dropped"] || !got["degraded-search"] {
		t.Fatal("one entry for each alert, only one of them delivered:", got)
	}
	r.w.webhook, r.w.kind = r.stack.webhookURL(), "feishu"
	texts = r.step(time.Minute)
	if len(texts) != 2 ||
		!strings.HasPrefix(texts[0], watchWantTag+"RESOLVED consolidation-dropped on aml-test (after 11m0s, alert not delivered)\nwas: ") ||
		!strings.HasPrefix(texts[1], watchWantTag+"RESOLVED degraded-search on aml-test (after 43m0s)\nwas: ") {
		t.Fatalf("%q", texts)
	}
	if got := r.firing(); len(got) != 0 {
		t.Fatal(got)
	}
	// With no webhook there is nothing to check a kind against.
	r.w.webhook, r.w.kind = "", "slack"
	if _, err = r.stepErr(time.Minute); err != nil {
		t.Fatal("a kind without a webhook is not checked:", err)
	}
}

func TestWatchFirstRunSetsABaseline(t *testing.T) {
	r := newWatchRig(t)
	r.stack.set(func(s *aml.AdminStatus) {
		s.Degraded, s.Dropped, s.LinkFallbacks, s.AddOutran = 7, 3, 2, 5
		s.BreakerTrips = map[string]int64{"deepseek": 4, "jev": 1}
		s.RejectedByKind = map[string]int64{"deepseek": 40, "jev": 2}
	})
	if texts := r.step(0); len(texts) != 0 {
		t.Fatal("history is not new:", texts)
	}
	if texts := r.step(time.Minute); len(texts) != 0 {
		t.Fatal(texts)
	}
	// What the first run does say is what is wrong now.
	r = newWatchRig(t)
	r.stack.set(func(s *aml.AdminStatus) { s.Degraded = 7; s.Breakers["jev"] = "open" })
	onlyAlert(t, r.step(0), "breaker-open:jev")
}

func TestWatchRules(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*aml.AdminStatus)
		want string // the alert that fires; "" for none
		text string // what its message says
	}{
		{"degraded", func(s *aml.AdminStatus) { s.Degraded = 2 }, "degraded-search", "2 new"},
		{"dropped", func(s *aml.AdminStatus) { s.Dropped = 1 }, "consolidation-dropped", "1 new"},
		{"link fallbacks", func(s *aml.AdminStatus) { s.LinkFallbacks = 3 }, "link-fallback", "3 new"},
		{"add outran", func(s *aml.AdminStatus) { s.AddOutran = 1 }, "add-outran", "1 new"},

		// More than 5% of the Searches of ten minutes, from 20 Searches on.
		{"partial 3 of 40", func(s *aml.AdminStatus) { s.SearchesLast10m, s.PartialLast10m = 40, 3 }, "partial-search", "3 of 40"},
		{"partial 2 of 40 is 5%", func(s *aml.AdminStatus) { s.SearchesLast10m, s.PartialLast10m = 40, 2 }, "", ""},
		{"partial 2 of 20", func(s *aml.AdminStatus) { s.SearchesLast10m, s.PartialLast10m = 20, 2 }, "partial-search", "2 of 20"},
		{"partial 1 of 20 is 5%", func(s *aml.AdminStatus) { s.SearchesLast10m, s.PartialLast10m = 20, 1 }, "", ""},
		{"partial 11 of 200 is 5.5%", func(s *aml.AdminStatus) { s.SearchesLast10m, s.PartialLast10m = 200, 11 }, "partial-search", "11 of 200 Searches in the last 10 minutes (5.5%)"},
		{"partial 10 of 200 is 5%", func(s *aml.AdminStatus) { s.SearchesLast10m, s.PartialLast10m = 200, 10 }, "", ""},
		{"partial 19 of 19 is too few Searches", func(s *aml.AdminStatus) { s.SearchesLast10m, s.PartialLast10m = 19, 19 }, "", ""},
		{"partial 2 of 2 is too few Searches", func(s *aml.AdminStatus) { s.SearchesLast10m, s.PartialLast10m = 2, 2 }, "", ""},
		{"partial 0 of 0", func(s *aml.AdminStatus) { s.SearchesLast10m, s.PartialLast10m = 0, 0 }, "", ""},

		{"breaker open", func(s *aml.AdminStatus) { s.Breakers["jev"] = "open"; s.BreakerTrips["jev"] = 1 }, "breaker-open:jev", "jev circuit breaker is open"},
		{"breaker open, no trip count", func(s *aml.AdminStatus) { s.Breakers["deepseek"] = "open" }, "breaker-open:deepseek", "is open"},
		{"breaker half-open", func(s *aml.AdminStatus) { s.Breakers["jev"] = "half-open" }, "", ""},
		{"breaker closed again after a trip", func(s *aml.AdminStatus) { s.BreakerTrips["openai"] = 2 }, "breaker-open:openai", "opened 2 times"},

		{"requests rejected", func(s *aml.AdminStatus) { s.RejectedByKind["jev"] = 2 }, "provider-rejected:jev", "jev rejected 2 requests (401/402/403) since the last run"},
		{"one request rejected", func(s *aml.AdminStatus) { s.RejectedByKind["deepseek"] = 1 }, "provider-rejected:deepseek", "deepseek rejected 1 request (401/402/403)"},

		// Above 80% of the cap, spent and held together; held reservations
		// above 2% of it.
		{"account at 80.1%", func(s *aml.AdminStatus) {
			s.Budgets = []memoryagent.BudgetAccount{{Kind: "deepseek", Cap: 1000, Spent: 801}}
		}, "budget-high:deepseek", "80.1%"},
		{"account at 80%", func(s *aml.AdminStatus) {
			s.Budgets = []memoryagent.BudgetAccount{{Kind: "deepseek", Cap: 1000, Spent: 800}}
		}, "", ""},
		{"account at 80% of spent and held", func(s *aml.AdminStatus) {
			s.Budgets = []memoryagent.BudgetAccount{{Kind: "jev", Cap: 1000, Spent: 790, Held: 11}}
		}, "budget-high:jev", "80.1%"},
		{"held 2.1%", func(s *aml.AdminStatus) {
			s.Budgets = []memoryagent.BudgetAccount{{Kind: "openai", Cap: 1000, Spent: 100, Held: 21}}
		}, "budget-held:openai", "2.1%"},
		{"held 2%", func(s *aml.AdminStatus) {
			s.Budgets = []memoryagent.BudgetAccount{{Kind: "openai", Cap: 1000, Spent: 100, Held: 20}}
		}, "", ""},
		{"a cap of zero has no share", func(s *aml.AdminStatus) {
			s.Budgets = []memoryagent.BudgetAccount{{Kind: "openai", Cap: 0, Spent: 5, Held: 1}}
		}, "", ""},

		{"oldest queued 15m", func(s *aml.AdminStatus) { s.Queue.OldestQueued = 15 * time.Minute }, "", ""},
		{"oldest queued past 15m", func(s *aml.AdminStatus) { s.Queue.OldestQueued = 15*time.Minute + time.Nanosecond }, "backlog-age", "15m0s"},
		{"a backlog over the bound needs time", func(s *aml.AdminStatus) { s.Queue.MaxBacklog = 170000 }, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newWatchRig(t)
			r.step(0)
			r.stack.set(tc.edit)
			texts := r.step(time.Minute)
			if tc.want == "" {
				if len(texts) != 0 {
					t.Fatal(texts)
				}
				return
			}
			if message := onlyAlert(t, texts, tc.want); !strings.Contains(message, tc.text) {
				t.Fatalf("%q lacks %q", message, tc.text)
			}
		})
	}
}

// The alerts of a kind are one each: two accounts or two breakers are two
// messages.
func TestWatchAlertsAreNamedBySubject(t *testing.T) {
	r := newWatchRig(t)
	r.step(0)
	r.stack.set(func(s *aml.AdminStatus) {
		s.Breakers["jev"], s.Breakers["deepseek"] = "open", "open"
		s.Budgets = []memoryagent.BudgetAccount{{Kind: "deepseek", Cap: 100, Spent: 90}, {Kind: "jev", Cap: 100, Spent: 85}}
	})
	texts := r.step(time.Minute)
	if len(texts) != 4 {
		t.Fatal(texts)
	}
	for i, alert := range []string{"breaker-open:deepseek", "breaker-open:jev", "budget-high:deepseek", "budget-high:jev"} {
		if !strings.HasPrefix(texts[i], watchWantTag+"ALERT "+alert+" on aml-test") {
			t.Errorf("message %d: %q, want %s", i, texts[i], alert)
		}
	}
}

// A breaker's open spell can end between two runs; the trips it left show.
// The alert stays ten minutes after the last sign of it.
func TestWatchBreakerTripBetweenRuns(t *testing.T) {
	r := newWatchRig(t)
	r.step(0)
	r.stack.set(func(s *aml.AdminStatus) { s.BreakerTrips["deepseek"] = 1 })
	message := onlyAlert(t, r.step(time.Minute), "breaker-open:deepseek")
	if !strings.Contains(message, "opened 1 time since the last run") || !strings.Contains(message, "closed") {
		t.Fatal(message)
	}
	if texts := r.step(5 * time.Minute); len(texts) != 0 {
		t.Fatal(texts)
	}
	// Open now: the ten minutes count from the last time it was seen so.
	r.stack.set(func(s *aml.AdminStatus) { s.Breakers["deepseek"] = "open"; s.BreakerTrips["deepseek"] = 2 })
	if texts := r.step(time.Minute); len(texts) != 0 {
		t.Fatal(texts)
	}
	r.stack.set(func(s *aml.AdminStatus) { s.Breakers["deepseek"] = "closed" })
	if texts := r.step(9 * time.Minute); len(texts) != 0 {
		t.Fatal("still within ten minutes of the last time it was open:", texts)
	}
	if message = onlyAlert(t, r.step(time.Minute), "breaker-open:deepseek"); !strings.HasPrefix(message, watchWantTag+"RESOLVED") {
		t.Fatal(message)
	}
}

// A provider that rejects requests (401, 402 or 403: a wrong or revoked key,
// an empty balance) pages at once (ruling R65): provider-rejected:<kind>
// fires on any rise of the kind's rejectedByKind. Its text is the kind, the
// count and what to check, and nothing the provider said. Like the other
// alerts about a counter it fires for ten minutes after the last rise, then
// resolves. Each kind is an alert of its own, so a second provider's
// rejections page as soon as they start. A server too old to count them (no
// rejectedByKind) is skipped, silently.
func TestWatchProviderRejected(t *testing.T) {
	r := newWatchRig(t)
	r.step(0) // 12:00, the baseline
	r.stack.set(func(s *aml.AdminStatus) { s.RejectedByKind["deepseek"] = 3 })
	if message := onlyAlert(t, r.step(time.Minute), "provider-rejected:deepseek"); message != watchWantTag+"ALERT provider-rejected:deepseek on aml-test\n"+
		"deepseek rejected 3 requests (401/402/403) since the last run: check the API key and the account balance (3 since the process started)\n"+
		"2026-10-24 12:01:00 UTC" {
		t.Fatalf("%q", message)
	}
	// More of them while it fires: nothing new to say.
	r.stack.set(func(s *aml.AdminStatus) { s.RejectedByKind["deepseek"] = 5 })
	if texts := r.step(time.Minute); len(texts) != 0 { // 12:02
		t.Fatal(texts)
	}
	// JEV's rejections are an alert of their own, said at once.
	r.stack.set(func(s *aml.AdminStatus) { s.RejectedByKind["jev"] = 1 })
	if message := onlyAlert(t, r.step(time.Minute), "provider-rejected:jev"); message != watchWantTag+"ALERT provider-rejected:jev on aml-test\n"+ // 12:03
		"jev rejected 1 request (401/402/403) since the last run: check the API key and the account balance (1 since the process started)\n"+
		"2026-10-24 12:03:00 UTC" {
		t.Fatalf("%q", message)
	}
	// Then they stop. Each alert fires for ten minutes after its last rise:
	// DeepSeek's (12:02) until 12:12, JEV's (12:03) until 12:13.
	for minute := 4; minute < 12; minute++ {
		if texts := r.step(time.Minute); len(texts) != 0 {
			t.Fatalf("at 12:%02d: %q", minute, texts)
		}
	}
	if message := onlyAlert(t, r.step(time.Minute), "provider-rejected:deepseek"); message != watchWantTag+"RESOLVED provider-rejected:deepseek on aml-test (after 11m0s)\n"+
		"was: deepseek rejected 5 requests (401/402/403) since the process started: check the API key and the account balance (last seen 9m0s ago)\n"+
		"2026-10-24 12:12:00 UTC" {
		t.Fatalf("%q", message)
	}
	if message := onlyAlert(t, r.step(time.Minute), "provider-rejected:jev"); !strings.HasPrefix(message, watchWantTag+"RESOLVED provider-rejected:jev on aml-test (after 10m0s)\n") {
		t.Fatalf("%q", message)
	}
	if got := r.firing(); len(got) != 0 {
		t.Fatal("the state still has", got)
	}

	// A server too old to count rejections sends no rejectedByKind: the rule
	// says nothing and keeps nothing, and the run is no error.
	r = newWatchRig(t)
	withoutRejections := func(s *watchStack) {
		raw, err := json.Marshal(s.status)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		delete(fields, "rejectedByKind")
		if raw, err = json.Marshal(fields); err != nil {
			t.Fatal(err)
		}
		s.statusRaw = string(raw)
	}
	r.stack.configure(withoutRejections)
	for _, after := range []time.Duration{0, time.Minute} {
		if texts := r.step(after); len(texts) != 0 {
			t.Fatal(texts)
		}
	}
	if counters := string(r.saved()["counters"]); strings.Contains(counters, "rejected") {
		t.Fatal(counters)
	}
	// Once it is upgraded (a restart) it counts from its start, so what it
	// has rejected since is new.
	r.stack.configure(func(s *watchStack) {
		s.statusRaw = ""
		s.status.StartedAt = watchNow.Add(90 * time.Second)
		s.status.RejectedByKind = map[string]int64{"deepseek": 2, "jev": 0}
	})
	texts := r.step(time.Minute) // 12:02
	if len(texts) != 2 || !strings.Contains(texts[0], " provider-rejected:deepseek on aml-test\ndeepseek rejected 2 requests (401/402/403) since the last run") || !strings.Contains(texts[1], " restart on aml-test\n") {
		t.Fatalf("%q", texts)
	}
}

// A backlog above the bound is an alert once it has been above for 15
// minutes of runs; a run below the bound starts the count over.
func TestWatchBacklogAboveTheBoundForFifteenMinutes(t *testing.T) {
	r := newWatchRig(t)
	r.step(0)
	over := func(backlog int64) { r.stack.set(func(s *aml.AdminStatus) { s.Queue.MaxBacklog = backlog }) }
	over(160001)
	for minute := 1; minute <= 10; minute++ {
		if texts := r.step(time.Minute); len(texts) != 0 {
			t.Fatalf("after %d minutes: %q", minute, texts)
		}
	}
	over(160000) // at the bound is within it
	if texts := r.step(time.Minute); len(texts) != 0 {
		t.Fatal(texts)
	}
	over(170000)
	for minute := 0; minute < 15; minute++ {
		if texts := r.step(time.Minute); len(texts) != 0 {
			t.Fatalf("minute %d of the second spell: %q", minute, texts)
		}
	}
	message := onlyAlert(t, r.step(time.Minute), "backlog-lag")
	for _, want := range []string{"170000 characters", "160000", "15m0s"} {
		if !strings.Contains(message, want) {
			t.Fatalf("%q lacks %q", message, want)
		}
	}
	over(1000)
	if message = onlyAlert(t, r.step(time.Minute), "backlog-lag"); !strings.HasPrefix(message, watchWantTag+"RESOLVED") {
		t.Fatal(message)
	}

	// Without a bound (consolidation is not background) there is nothing to
	// be above.
	r = newWatchRig(t)
	r.stack.set(func(s *aml.AdminStatus) { s.LagBound, s.Queue.MaxBacklog = 0, 5000000 })
	r.step(0)
	if texts := r.step(time.Hour); len(texts) != 0 {
		t.Fatal(texts)
	}
}

func TestWatchReadyAndHealth(t *testing.T) {
	r := newWatchRig(t)
	if texts := r.step(0); len(texts) != 0 {
		t.Fatal(texts)
	}

	// /ready says why it is not ready; the message has every reason.
	r.stack.configure(func(s *watchStack) {
		s.readyCode, s.readyReasons = http.StatusServiceUnavailable, []string{"breaker open: jev", "free disk 400 MiB is below 1 GiB"}
	})
	message := onlyAlert(t, r.step(time.Minute), "ready")
	for _, want := range []string{"/ready answered 503", "breaker open: jev", "free disk 400 MiB is below 1 GiB"} {
		if !strings.Contains(message, want) {
			t.Fatalf("%q lacks %q", message, want)
		}
	}
	if texts := r.step(time.Minute); len(texts) != 0 {
		t.Fatal(texts)
	}
	r.stack.configure(func(s *watchStack) { s.readyCode = 0 })
	if message = onlyAlert(t, r.step(time.Minute), "ready"); !strings.HasPrefix(message, watchWantTag+"RESOLVED ready on aml-test") {
		t.Fatal(message)
	}

	// Only a 200 is ready, and a 200 is healthy: another answer, a 404 from
	// the wrong address say, is a fault.
	r.stack.configure(func(s *watchStack) { s.readyCode = http.StatusNotFound })
	if message = onlyAlert(t, r.step(time.Minute), "ready"); !strings.Contains(message, "/ready answered 404") {
		t.Fatal(message)
	}
	r.stack.configure(func(s *watchStack) { s.readyCode = 0 })
	if message = onlyAlert(t, r.step(time.Minute), "ready"); !strings.HasPrefix(message, watchWantTag+"RESOLVED ready") {
		t.Fatal(message)
	}
	r.stack.configure(func(s *watchStack) { s.healthCode = http.StatusNotFound })
	if message = onlyAlert(t, r.step(time.Minute), "health"); !strings.Contains(message, "answered 404") {
		t.Fatal(message)
	}
	r.stack.configure(func(s *watchStack) { s.healthCode = 0 })
	onlyAlert(t, r.step(time.Minute), "health") // resolved

	// /health through the public URL.
	r.stack.configure(func(s *watchStack) { s.healthCode = http.StatusServiceUnavailable })
	message = onlyAlert(t, r.step(time.Minute), "health")
	if want := "GET " + r.stack.public.URL + "/health answered 503"; !strings.Contains(message, want) {
		t.Fatalf("%q lacks %q", message, want)
	}
	r.stack.configure(func(s *watchStack) { s.healthCode = 0 })
	if message = onlyAlert(t, r.step(time.Minute), "health"); !strings.HasPrefix(message, watchWantTag+"RESOLVED") {
		t.Fatal(message)
	}

	// Without a public URL it is not checked.
	r.w.public = ""
	r.stack.configure(func(s *watchStack) { s.healthCode = http.StatusBadGateway })
	if texts := r.step(time.Minute); len(texts) != 0 {
		t.Fatal(texts)
	}
	r.w.public = r.stack.public.URL

	// A server that does not answer at all is not ready, and the message says
	// how it did not answer.
	r.stack.configure(func(s *watchStack) { s.healthCode = 0 })
	r.stack.admin.Close()
	message = onlyAlert(t, r.step(time.Minute), "ready")
	if !strings.Contains(message, "/ready did not answer") || !strings.Contains(message, "/admin/status did not answer") || strings.Contains(message, "Get \"") {
		t.Fatal(message)
	}
}

// What the admin listener could not say is neither an alert that is over nor a
// new one: the alerts that were firing go on, and the counters are measured
// from the last reading once there is one.
func TestWatchUnreadableStatusKeepsFiringAlerts(t *testing.T) {
	r := newWatchRig(t)
	r.step(0)
	r.stack.set(func(s *aml.AdminStatus) { s.Degraded = 1; s.Breakers["jev"] = "open"; s.BreakerTrips["jev"] = 1 })
	if texts := r.step(time.Minute); len(texts) != 2 {
		t.Fatal(texts)
	}

	for name, tc := range map[string]struct {
		fail func(*watchStack)
		want string
	}{
		"HTTP 500":           {func(s *watchStack) { s.statusCode = http.StatusInternalServerError }, "/admin/status answered 500"},
		"HTTP 404":           {func(s *watchStack) { s.statusCode = http.StatusNotFound }, "/admin/status answered 404"},
		"not JSON":           {func(s *watchStack) { s.statusRaw = "<html>" }, "/admin/status did not answer with an aml-serve status"},
		"not an AML status":  {func(s *watchStack) { s.statusRaw = `{"ok":true}` }, "/admin/status did not answer with an aml-serve status"},
		"a null status":      {func(s *watchStack) { s.statusRaw = `null` }, "/admin/status did not answer with an aml-serve status"},
		"a truncated status": {func(s *watchStack) { s.statusRaw = `{"startedAt":"2026-10-2` }, "/admin/status did not answer with an aml-serve status"},
	} {
		r.stack.configure(tc.fail)
		message := onlyAlert(t, r.step(time.Minute), "ready")
		if !strings.Contains(message, tc.want) {
			t.Fatalf("%s: %q lacks %q", name, message, tc.want)
		}
		// Twenty minutes unreadable: the alerts that were firing are neither
		// resolved nor said again, and the rises of that time are not lost.
		for range 20 {
			if texts := r.step(time.Minute); len(texts) != 0 {
				t.Fatalf("%s: %q", name, texts)
			}
		}
		r.stack.configure(func(s *watchStack) { s.statusCode, s.statusRaw = 0, "" })
		r.stack.set(func(s *aml.AdminStatus) { s.Degraded++; s.Breakers["jev"] = "open" })
		texts := r.step(time.Minute)
		joined := strings.Join(texts, "\n")
		if strings.Contains(joined, "RESOLVED degraded") || strings.Contains(joined, "RESOLVED breaker") || !strings.Contains(joined, "RESOLVED ready") {
			t.Fatalf("%s: %q", name, texts)
		}
	}
	if got := r.firing(); !got["degraded-search"] || !got["breaker-open:jev"] {
		t.Fatal(got)
	}
}

func TestWatchStateFile(t *testing.T) {
	r := newWatchRig(t)
	r.stack.set(func(s *aml.AdminStatus) { s.Degraded = 2 })
	r.step(0)
	// The directory is made, the file is private, and the temporary file the
	// write goes through is gone.
	info, err := os.Stat(r.state)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
	// The directory holds the state and the lock file of the runs (kept, not
	// removed: removing a lock file would race), and no temporary file.
	if entries, err := os.ReadDir(filepath.Dir(r.state)); err != nil || len(entries) != 2 || entries[0].Name() != "watch-state.json" || entries[1].Name() != "watch-state.json.lock" {
		t.Fatal(entries, err)
	}
	if info, err := os.Stat(r.state + ".lock"); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatal("the lock file:", info, err)
	}
	fields := r.saved()
	var version int
	var counters map[string]int64
	var started time.Time
	if json.Unmarshal(fields["version"], &version) != nil || version != 1 ||
		json.Unmarshal(fields["counters"], &counters) != nil || counters["degraded"] != 2 ||
		json.Unmarshal(fields["startedAt"], &started) != nil || !started.Equal(watchStarted) {
		t.Fatalf("%s", fields)
	}
	for _, name := range []string{"firing", "rises", "since"} {
		if _, ok := fields[name]; !ok {
			t.Errorf("no %q in the state", name)
		}
	}

	// A state that is too open is made private again.
	if runtime.GOOS != "windows" {
		if err := os.Chmod(r.state, 0o644); err != nil {
			t.Fatal(err)
		}
		r.step(time.Minute)
		if info, err = os.Stat(r.state); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatal(info.Mode(), err)
		}
	}

	// A state nobody can read is a state to begin again from, said in the
	// log; the run goes on and writes a good one.
	for name, content := range map[string]string{"garbage": "{not json", "empty": "", "wrong version": `{"version":99,"counters":{"degraded":1}}`, "not an object": `[1,2]`} {
		if err := os.WriteFile(r.state, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		r.logs.Reset()
		if texts := r.step(time.Minute); len(texts) != 0 || !strings.Contains(r.logs.String(), "starting afresh") {
			t.Fatalf("%s: %q\n%s", name, texts, r.logs)
		}
		if raw, err := os.ReadFile(r.state); err != nil || !json.Valid(raw) || !strings.Contains(string(raw), `"version": 1`) {
			t.Fatalf("%s: %s %v", name, raw, err)
		}
	}
}

// A state that cannot be read (it is not a missing file, nor a file of
// something else: it cannot be opened) stops the run, which changes nothing.
func TestWatchStateThatCannotBeRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("what reading a directory does is not specified the same way")
	}
	r := newWatchRig(t)
	// A directory where the file should be.
	if err := os.MkdirAll(filepath.Join(r.state, "occupied"), 0o700); err != nil {
		t.Fatal(err)
	}
	r.stack.configure(func(s *watchStack) { s.healthCode = http.StatusBadGateway })
	texts, err := r.stepErr(0)
	if err == nil || !strings.Contains(err.Error(), "state") || len(texts) != 0 {
		t.Fatalf("%q %v", texts, err)
	}
	if strings.Contains(err.Error(), watchToken) {
		t.Fatal(err)
	}
	if entries, readErr := os.ReadDir(filepath.Dir(r.state)); readErr != nil || len(entries) != 2 { // the directory where the state should be, and the lock
		t.Fatal(entries, readErr)
	}
}

// A state that cannot be written is an error, and leaves nothing behind. The
// alerts of the run are said all the same.
func TestWatchStateThatCannotBeWritten(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not stop this user")
	}
	r := newWatchRig(t)
	dir := filepath.Dir(r.state)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	r.stack.configure(func(s *watchStack) { s.healthCode = http.StatusBadGateway })
	texts, err := r.stepErr(0)
	if err == nil || !strings.Contains(err.Error(), "state") || len(texts) != 1 {
		t.Fatalf("%q %v", texts, err)
	}
	if entries, readErr := os.ReadDir(dir); readErr != nil || len(entries) != 0 {
		t.Fatal("the failed write left", entries, readErr)
	}
	// The lock is not what keeps alerts from going out: a run that cannot open
	// its lock file (not because another run holds it) goes on without it, and
	// says so.
	if got := strings.Count(r.logs.String(), "could not take the run lock"); got != 1 || strings.Contains(r.logs.String(), "holds the lock") {
		t.Fatalf("%d lines about the lock:\n%s", got, r.logs)
	}
}

func TestWatchMessagesStayUnderAThousandCharacters(t *testing.T) {
	r := newWatchRig(t)
	reasons := []string{}
	for i := range 80 {
		reasons = append(reasons, fmt.Sprintf("data root is not writable: open /var/lib/mnemon-aml/users/%08d: permission denied — é", i))
	}
	r.stack.configure(func(s *watchStack) { s.readyCode, s.readyReasons = http.StatusServiceUnavailable, reasons })
	r.w.host = strings.Repeat("h", 200)
	texts := r.step(0)
	if len(texts) != 1 || len(texts[0]) >= 1000 || len([]rune(texts[0])) >= 1000 || !strings.Contains(texts[0], "...") || !strings.Contains(texts[0], "UTC") {
		t.Fatalf("%d bytes: %q", len(texts[0]), texts)
	}
	if !strings.HasPrefix(texts[0], watchWantTag+"ALERT ready on "+r.w.host) || strings.Contains(texts[0], "\uFFFD") {
		t.Fatal(texts[0])
	}
	// What the state keeps of it is bounded too, in the run that raised the
	// alert and in the ones that find it firing.
	for range 2 {
		if info, err := os.Stat(r.state); err != nil || info.Size() > 4096 {
			t.Fatal(info.Size(), err)
		}
		if again := r.step(time.Minute); len(again) != 0 {
			t.Fatal(again)
		}
	}
	// And so is the resolution.
	r.stack.configure(func(s *watchStack) { s.readyCode = 0 })
	if texts = r.step(time.Minute); len(texts) != 1 || len(texts[0]) >= 1000 || !strings.Contains(texts[0], "\nwas: ") || !strings.HasPrefix(texts[0], watchWantTag+"RESOLVED ready on "+r.w.host) {
		t.Fatalf("%d bytes: %q", len(texts[0]), texts)
	}
}

// A run that is interrupted has not seen the server: it posts nothing, and
// leaves the state as it was.
func TestWatchInterruptedRunChangesNothing(t *testing.T) {
	r := newWatchRig(t)
	r.stack.set(func(s *aml.AdminStatus) { s.Degraded = 1 })
	r.step(0)
	before, err := os.ReadFile(r.state)
	if err != nil {
		t.Fatal(err)
	}
	r.stack.configure(func(s *watchStack) { s.healthCode = http.StatusBadGateway })
	r.stack.set(func(s *aml.AdminStatus) { s.Degraded = 2 })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.clock.at = r.clock.at.Add(time.Minute)
	err = r.w.run(ctx)
	if err == nil || !strings.Contains(err.Error(), "interrupted") || !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	after, readErr := os.ReadFile(r.state)
	if readErr != nil || !bytes.Equal(before, after) || r.stack.attempts() != 0 {
		t.Fatalf("%v %d posts\n%s\n%s", readErr, r.stack.attempts(), before, after)
	}
	// The next run sees what the interrupted one did not.
	texts := r.step(time.Minute)
	if len(texts) != 2 {
		t.Fatal(texts)
	}
}

// A cut message is whole characters, whatever their width, within the limit,
// and says it was cut.
func TestWatchTruncateKeepsWholeCharacters(t *testing.T) {
	s := strings.Repeat("é—😀x", 12) // 2, 3, 4 and 1 bytes
	for limit := 0; limit <= len(s)+2; limit++ {
		got := watchTruncate(s, limit)
		switch {
		case len(s) <= limit:
			if got != s {
				t.Fatalf("limit %d: %q", limit, got)
			}
		case limit <= 3:
			if got != "..."[:limit] {
				t.Fatalf("limit %d: %q", limit, got)
			}
		default:
			if len(got) > limit || !utf8.ValidString(got) || !strings.HasSuffix(got, "...") || !strings.HasPrefix(s, strings.TrimSuffix(got, "...")) {
				t.Fatalf("limit %d: %q", limit, got)
			}
			// No more than three bytes short of the limit: the cut is at the
			// last character that fits.
			if len(got) < limit-3 {
				t.Fatalf("limit %d: %q is cut too early", limit, got)
			}
		}
	}
}

func TestAMLWatchIsAMemoryAgentSubcommand(t *testing.T) {
	command, _, err := rootCmd.Find([]string{"memory-agent", "aml-watch"})
	if err != nil || command.Name() != "aml-watch" || command.Parent().Name() != "memory-agent" {
		t.Fatal(command, err)
	}
	for flag, value := range map[string]string{"admin": "http://127.0.0.1:8091", "public-url": "", "state": "", "admin-token-env": "", "instance": ""} {
		if f := command.Flags().Lookup(flag); f == nil || f.DefValue != value {
			t.Fatal(flag, f)
		}
	}
}

// executeAMLWatch runs aml-watch with args to its end, its output kept.
func executeAMLWatch(args ...string) (string, error) {
	command := newAMLWatchCommand()
	var out bytes.Buffer
	command.SetArgs(args)
	command.SetOut(&out)
	command.SetErr(&out)
	err := command.Execute()
	return out.String(), err
}

func TestWatchCommandChecksItsSettings(t *testing.T) {
	state := filepath.Join(t.TempDir(), "watch-state.json")
	good := []string{"--state", state}
	secret := "https://hooks.example.com/hook/" + watchToken
	for name, tc := range map[string]struct {
		args    []string
		webhook string
		kind    string
		want    string
	}{
		"no state":                {nil, "", "", "--state is required"},
		"relative state":          {[]string{"--state", "watch-state.json"}, "", "", "--state must be an absolute path"},
		"admin not a URL":         {append([]string{"--admin", "127.0.0.1:8091"}, good...), "", "", "--admin must be an http or https URL"},
		"admin with credentials":  {append([]string{"--admin", "http://user:pass@127.0.0.1:8091"}, good...), "", "", "--admin must not contain credentials"},
		"public not a URL":        {append([]string{"--public-url", "example.com"}, good...), "", "", "--public-url must be an http or https URL"},
		"public with credentials": {append([]string{"--public-url", "https://user@example.com"}, good...), "", "", "--public-url must not contain credentials"},
		"webhook without a kind":  {good, secret, "", "AML_ALERT_KIND must be feishu or wecom"},
		"webhook of another kind": {good, secret, "slack", "AML_ALERT_KIND must be feishu or wecom"},
		"webhook not a URL":       {good, "hooks.example.com/hook/" + watchToken, "feishu", "AML_ALERT_WEBHOOK must be an https URL"},
		"webhook over plain http": {good, "http://hooks.example.com/hook/" + watchToken, "feishu", "AML_ALERT_WEBHOOK must be an https URL"},
		"webhook with a bad host": {good, "https:///hook/" + watchToken, "feishu", "AML_ALERT_WEBHOOK must be an https URL"},
		"webhook unparsable":      {good, "https://hooks.example.com/hook/%zz" + watchToken, "feishu", "AML_ALERT_WEBHOOK must be an https URL"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("AML_ALERT_WEBHOOK", tc.webhook)
			t.Setenv("AML_ALERT_KIND", tc.kind)
			out, err := executeAMLWatch(tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%v\n%s", err, out)
			}
			if strings.Contains(err.Error()+out, watchToken) {
				t.Errorf("the webhook is echoed: %v\n%s", err, out)
			}
			if _, statErr := os.Stat(state); !os.IsNotExist(statErr) {
				t.Errorf("a refused run wrote state: %v", statErr)
			}
		})
	}

	// http is for a webhook on this host (a relay, a test); https anywhere.
	// Whitespace around the values, as an environment file leaves it, is not
	// part of them.
	stack := newWatchStack(t)
	t.Setenv("AML_ALERT_WEBHOOK", " "+stack.webhookURL()+"\n")
	t.Setenv("AML_ALERT_KIND", " WeCom\n")
	stack.configure(func(s *watchStack) { s.healthCode = http.StatusBadGateway })
	out, err := executeAMLWatch("--admin", stack.admin.URL, "--public-url", stack.public.URL+"/", "--state", state)
	if err != nil {
		t.Fatal(err, out)
	}
	host, _ := os.Hostname()
	if got := stack.take(t); len(got) != 1 || !strings.Contains(got[0], "ALERT health on "+host) || !strings.HasSuffix(got[0], " UTC") {
		t.Fatalf("%q (host %q)", got, host)
	}
	stack.mu.Lock()
	body := string(stack.posts[0].body)
	stack.mu.Unlock()
	if !strings.HasPrefix(body, `{"msgtype":"text","text":{"content":"`+watchWantTag+`ALERT health on `) {
		t.Fatal(body)
	}
}

// A URL that cannot be fetched is told apart from one that answered badly,
// and the watcher gives up on a server that hangs: its requests are bounded.
func TestWatchRequestsAreBounded(t *testing.T) {
	r := newWatchRig(t)
	for name, client := range map[string]*http.Client{"admin and public": r.w.reads, "webhook": r.w.hooks} {
		if client.Timeout <= 0 {
			t.Errorf("%s requests have no timeout", name)
		}
	}
	if r.w.reads.Timeout != 5*time.Second || r.w.hooks.Timeout != 10*time.Second {
		t.Fatal(r.w.reads.Timeout, r.w.hooks.Timeout)
	}
	// A webhook that redirects is a failure, not a request to follow: its
	// Location is not the operator's.
	if err := r.w.hooks.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatal(err)
	}
}

// A restart gives the server a backlog of its own: the time it has been above
// the bound starts again.
func TestWatchRestartStartsTheBacklogCountOver(t *testing.T) {
	r := newWatchRig(t)
	r.step(0)
	r.stack.set(func(s *aml.AdminStatus) { s.Queue.MaxBacklog = 170000 })
	for minute := 1; minute <= 10; minute++ { // above the bound since 12:01
		if texts := r.step(time.Minute); len(texts) != 0 {
			t.Fatalf("at 12:%02d: %q", minute, texts)
		}
	}
	r.stack.set(func(s *aml.AdminStatus) {
		*s = watchHealthy(watchNow.Add(10*time.Minute + 30*time.Second))
		s.Queue.MaxBacklog = 170000
	})
	onlyAlert(t, r.step(time.Minute), "restart") // 12:11
	for minute := 12; minute <= 25; minute++ {
		texts := r.step(time.Minute)
		if len(texts) != 0 && !(minute == 21 && strings.HasPrefix(texts[0], watchWantTag+"RESOLVED restart")) {
			t.Fatalf("at 12:%02d: %q", minute, texts)
		}
	}
	if message := onlyAlert(t, r.step(time.Minute), "backlog-lag"); !strings.Contains(message, "for 15m0s") { // 12:26, 15 minutes after the restart
		t.Fatal(message)
	}
}

// What the last run did not know is not new: its history is not news.
func TestWatchACounterTheLastRunDidNotKnowIsNotNew(t *testing.T) {
	r := newWatchRig(t)
	r.step(0)
	r.stack.set(func(s *aml.AdminStatus) { s.Breakers["groq"], s.BreakerTrips["groq"] = "closed", 3 })
	if texts := r.step(time.Minute); len(texts) != 0 {
		t.Fatal(texts)
	}
	r.stack.set(func(s *aml.AdminStatus) { s.BreakerTrips["groq"] = 4 })
	if message := onlyAlert(t, r.step(time.Minute), "breaker-open:groq"); !strings.Contains(message, "opened 1 time since the last run") {
		t.Fatal(message)
	}
}

// An unreadable status leaves the alerts it cannot speak for as they were, the
// time they have left to fire included; the alerts that do not come from it
// (ready, health) go on as usual.
func TestWatchUnreadableStatusLeavesHeldAlertsHeld(t *testing.T) {
	r := newWatchRig(t)
	r.step(0)
	r.stack.set(func(s *aml.AdminStatus) { s.Degraded = 1 })
	onlyAlert(t, r.step(time.Minute), "degraded-search") // 12:01
	r.stack.configure(func(s *watchStack) { s.statusCode = http.StatusInternalServerError })
	onlyAlert(t, r.step(time.Minute), "ready") // 12:02
	r.step(time.Minute)                        // 12:03
	r.stack.configure(func(s *watchStack) { s.statusCode = 0 })
	if message := onlyAlert(t, r.step(time.Minute), "ready"); !strings.HasPrefix(message, watchWantTag+"RESOLVED ready") { // 12:04
		t.Fatal(message)
	}
	// It rose at 12:01 and is firing until 12:11, however the minutes between
	// were read.
	for minute := 5; minute <= 10; minute++ {
		if texts := r.step(time.Minute); len(texts) != 0 {
			t.Fatalf("at 12:%02d: %q", minute, texts)
		}
	}
	if message := onlyAlert(t, r.step(time.Minute), "degraded-search"); !strings.HasPrefix(message, watchWantTag+"RESOLVED") {
		t.Fatal(message)
	}

	// A status that stays unreadable is said again every 30 minutes, to the
	// second, like any alert.
	r.stack.configure(func(s *watchStack) { s.statusCode = http.StatusInternalServerError })
	onlyAlert(t, r.step(time.Minute), "ready")
	if texts := r.step(29*time.Minute + 59*time.Second); len(texts) != 0 {
		t.Fatal(texts)
	}
	if message := onlyAlert(t, r.step(time.Second), "ready"); !strings.Contains(message, "still firing after 30m0s") {
		t.Fatal(message)
	}

	// /health is read whether or not the status is, and its alert ends when it
	// should.
	r.stack.configure(func(s *watchStack) { s.healthCode = http.StatusBadGateway })
	onlyAlert(t, r.step(time.Minute), "health")
	r.stack.configure(func(s *watchStack) { s.healthCode = 0 })
	if message := onlyAlert(t, r.step(time.Minute), "health"); !strings.HasPrefix(message, watchWantTag+"RESOLVED health") {
		t.Fatal(message)
	}
}

// A status with fields this watcher does not know (the server is newer) is
// read.
func TestWatchReadsAStatusWithNewFields(t *testing.T) {
	r := newWatchRig(t)
	r.stack.configure(func(s *watchStack) {
		raw, err := json.Marshal(s.status)
		if err != nil {
			t.Fatal(err)
		}
		s.statusRaw = strings.TrimSuffix(string(raw), "}") + `,"someFutureField":{"a":[1,2]},"queue2":null}`
	})
	if texts := r.step(0); len(texts) != 0 {
		t.Fatal(texts)
	}
	if texts := r.step(time.Minute); len(texts) != 0 {
		t.Fatal(texts)
	}
}

// A state file with null maps (hand-edited, or from a tool that drops empty
// ones) is read, and written back with maps.
func TestWatchStateWithNullMaps(t *testing.T) {
	r := newWatchRig(t)
	if err := os.MkdirAll(filepath.Dir(r.state), 0o700); err != nil {
		t.Fatal(err)
	}
	null := `{"version":1,"startedAt":"` + watchStarted.Format(time.RFC3339) + `","counters":null,"rises":null,"since":null,"firing":null}`
	if err := os.WriteFile(r.state, []byte(null), 0o600); err != nil {
		t.Fatal(err)
	}
	// With the status unreadable the maps are carried over.
	r.stack.configure(func(s *watchStack) { s.statusCode = http.StatusInternalServerError })
	onlyAlert(t, r.step(0), "ready")
	saved := r.saved()
	for _, name := range []string{"counters", "rises", "since"} {
		if string(saved[name]) != "{}" {
			t.Errorf("%s is %s", name, saved[name])
		}
	}
}

// The state is replaced whole or not at all, and a write that fails leaves no
// temporary file.
func TestSaveWatchStateLeavesNothingBehind(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("renaming a file over a directory is not specified the same way")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "watch-state.json")
	// A directory with something in it where the file should be: the rename
	// fails.
	if err := os.MkdirAll(filepath.Join(path, "occupied"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := saveWatchState(path, newWatchState()); err == nil || !strings.Contains(err.Error(), "state") {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 || entries[0].Name() != "watch-state.json" {
		t.Fatal("the failed write left", entries, err)
	}
}

// The time in a message is UTC, whatever the clock's zone.
func TestWatchSaysTheTimeInUTC(t *testing.T) {
	zone := time.FixedZone("CST", 8*3600)
	if text := watchText(watchNew, "health", "aml-test", "", "down", watchNow.In(zone)); !strings.HasSuffix(text, "\n2026-10-24 12:00:00 UTC") {
		t.Fatal(text)
	}
	r := newWatchRig(t)
	r.clock.at = watchNow.In(zone) // 20:00 in the clock's zone
	r.stack.configure(func(s *watchStack) { s.healthCode = http.StatusBadGateway })
	if texts := r.step(0); len(texts) != 1 || !strings.HasSuffix(texts[0], "\n2026-10-24 12:00:00 UTC") {
		t.Fatal(texts)
	}
	var state struct{ CheckedAt time.Time }
	raw, err := os.ReadFile(r.state)
	if err != nil || json.Unmarshal(raw, &state) != nil || !state.CheckedAt.Equal(watchNow) || !strings.Contains(string(raw), `"checkedAt": "2026-10-24T12:00:00Z"`) {
		t.Fatalf("%v\n%s", err, raw)
	}
}

// timeoutError is a net.Error that timed out.
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

// A failed request says what kind of failure it was, and never repeats the
// error net/http made, which names the webhook's URL.
func TestWatchTransportFailuresSayOnlyTheirKind(t *testing.T) {
	secret := "https://hooks.example.com/hook/" + watchToken
	wrap := func(err error) error { return &url.Error{Op: "Post", URL: secret, Err: err} }
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"canceled":  {wrap(context.Canceled), "the post was interrupted"},
		"deadline":  {wrap(context.DeadlineExceeded), "the webhook did not answer in time"},
		"timeout":   {wrap(timeoutError{}), "the webhook did not answer in time"},
		"refused":   {wrap(errors.New("dial tcp " + secret + ": connect: connection refused")), "the webhook could not be reached"},
		"unwrapped": {errors.New("lookup " + secret + ": no such host"), "the webhook could not be reached"},
	} {
		got := watchTransportFailure(tc.err)
		if got.Error() != tc.want || !got.unreachable {
			t.Errorf("%s: %q (unreachable %v), want %q", name, got.Error(), got.unreachable, tc.want)
		}
	}
}

// Every message starts with the tag whose word the operator registers as the
// Feishu bot's keyword: a bot protected by a keyword answers a text without it
// 200 with a code, which is a failed delivery. The tag is on every kind of
// message, a resolution and the resolution of an alert that never went out
// included (those two had no "ALERT" in them).
func TestWatchEveryMessageCarriesTheTag(t *testing.T) {
	r := newWatchRig(t)
	r.stack.configure(func(s *watchStack) { s.hookKeyword = "mnemon-aml" })
	all := []string{}
	say := func(after time.Duration) {
		t.Helper()
		all = append(all, r.step(after)...) // fails the test if the keyword was not found
	}
	say(0)
	// An alert, said again, resolved.
	r.stack.configure(func(s *watchStack) { s.healthCode = http.StatusBadGateway })
	say(time.Minute)
	say(30 * time.Minute)
	r.stack.configure(func(s *watchStack) { s.healthCode = 0 })
	say(time.Minute)
	// An alert that did not go out, resolved.
	r.stack.configure(func(s *watchStack) {
		s.hookCode = http.StatusInternalServerError
		s.readyCode, s.readyReasons = http.StatusServiceUnavailable, []string{"breaker open: jev"}
	})
	failed, err := r.stepErr(time.Minute)
	if err == nil {
		t.Fatal("no error")
	}
	all = append(all, failed...)
	r.stack.configure(func(s *watchStack) { s.hookCode, s.readyCode = 0, 0 })
	say(time.Minute)

	if len(all) != 5 {
		t.Fatalf("%q", all)
	}
	for i, kind := range []string{"ALERT health on aml-test\n", "ALERT health on aml-test (still firing after 30m0s)\n", "RESOLVED health on aml-test (after 31m0s)\n",
		"ALERT ready on aml-test\n", "RESOLVED ready on aml-test (after 1m0s, alert not delivered)\n"} {
		if !strings.HasPrefix(all[i], "[mnemon-aml] "+kind) {
			t.Errorf("message %d is %q, want it to start with %q", i, all[i], "[mnemon-aml] "+kind)
		}
	}
	// A bot whose keyword is the old word of the alerts takes none of the
	// resolutions: that was the defect.
	r.stack.configure(func(s *watchStack) { s.hookKeyword = "ALERT" })
	r.stack.configure(func(s *watchStack) { s.healthCode = http.StatusBadGateway })
	r.step(time.Minute)
	r.stack.configure(func(s *watchStack) { s.healthCode = 0 })
	if _, err = r.stepErr(time.Minute); err == nil || !strings.Contains(err.Error(), "rejected the message (code 19024)") {
		t.Fatal("a resolution was taken by a bot that wants ALERT:", err)
	}
}

// With no public URL the run says, every run, that the public health probe is
// off: a forgotten flag is not silent.
func TestWatchWarnsThatThePublicHealthProbeIsOff(t *testing.T) {
	const warning = "the public health probe is off because --public-url is empty"
	warnings := func(logs string) int {
		n := 0
		for _, line := range strings.Split(logs, "\n") {
			if strings.Contains(line, warning) {
				if !strings.Contains(line, "level=WARN") {
					t.Errorf("not a warning: %s", line)
				}
				n++
			}
		}
		return n
	}
	t.Setenv("AML_ALERT_WEBHOOK", "") // the command part posts nothing
	t.Setenv("AML_ALERT_KIND", "")
	r := newWatchRig(t)
	r.w.public = ""
	r.stack.configure(func(s *watchStack) { s.healthCode = http.StatusBadGateway }) // nobody looks
	for run := 1; run <= 3; run++ {
		if texts := r.step(time.Minute); len(texts) != 0 {
			t.Fatal(texts)
		}
		if got := warnings(r.logs.String()); got != run {
			t.Fatalf("after %d runs, %d warnings:\n%s", run, got, r.logs)
		}
	}
	// With the probe on, nothing is said of it.
	r.w.public = r.stack.public.URL
	r.logs.Reset()
	onlyAlert(t, r.step(time.Minute), "health")
	if got := warnings(r.logs.String()); got != 0 {
		t.Fatalf("%d warnings with the probe on:\n%s", got, r.logs)
	}

	// The same through the command: its flag is optional, and its absence is
	// said once per run.
	stack := newWatchStack(t)
	state := filepath.Join(t.TempDir(), "watch-state.json")
	out, err := executeAMLWatch("--admin", stack.admin.URL, "--state", state)
	if err != nil || warnings(out) != 1 {
		t.Fatalf("%v\n%s", err, out)
	}
	if out, err = executeAMLWatch("--admin", stack.admin.URL, "--public-url", stack.public.URL, "--state", state); err != nil || warnings(out) != 0 {
		t.Fatalf("%v\n%s", err, out)
	}
}

// Delivery is at-least-once whatever the alert and however long the webhook is
// down: an alert that never went out and then cleared owes a resolution, which
// says so and is tried every run, with no cap on the tries and no age after
// which it is given up, until the webhook takes it. The state keeps one entry
// for the alert all along.
func TestWatchOwedResolutionsAreRetriedWithoutCap(t *testing.T) {
	r := newWatchRig(t)
	r.step(0)
	r.stack.configure(func(s *watchStack) {
		s.hookCode = http.StatusInternalServerError
		s.healthCode = http.StatusBadGateway
	})
	if _, err := r.stepErr(time.Minute); err == nil { // the alert, which does not go out
		t.Fatal("no error")
	}
	r.stack.configure(func(s *watchStack) { s.healthCode = 0 })
	// Three days of a webhook that is down, a run an hour.
	for hour := 1; hour <= 72; hour++ {
		before := r.stack.attempts()
		texts, err := r.stepErr(time.Hour)
		want := fmt.Sprintf("[mnemon-aml] RESOLVED health on aml-test (after %s, alert not delivered)\nwas: GET %s/health answered 502\n", (time.Duration(hour) * time.Hour).String(), r.stack.public.URL)
		if err == nil || len(texts) != 1 || r.stack.attempts() != before+1 || !strings.HasPrefix(texts[0], want) {
			t.Fatalf("hour %d: %q %v (want %q)", hour, texts, err, want)
		}
		if got := r.firing(); len(got) != 1 || got["health"] {
			t.Fatalf("hour %d: the state has %v", hour, got)
		}
	}
	// The webhook takes it.
	r.stack.configure(func(s *watchStack) { s.hookCode = 0 })
	message := onlyAlert(t, r.step(time.Hour), "health")
	if !strings.HasPrefix(message, "[mnemon-aml] RESOLVED health on aml-test (after 73h0m0s, alert not delivered)\nwas: GET ") {
		t.Fatal(message)
	}
	if got := r.firing(); len(got) != 0 {
		t.Fatal(got)
	}
	if texts := r.step(time.Hour); len(texts) != 0 {
		t.Fatal("said twice:", texts)
	}
}

// An alert that fires again while its resolution is owed is one alert: the
// resolution is not sent for the earlier episode, the alert goes out as the
// new alert nobody has heard of, and the alert has one state entry throughout.
func TestWatchAnAlertThatFiresAgainWhileItsResolutionIsOwed(t *testing.T) {
	r := newWatchRig(t)
	r.step(0)
	r.stack.configure(func(s *watchStack) { s.hookCode, s.healthCode = http.StatusInternalServerError, http.StatusBadGateway })
	if _, err := r.stepErr(time.Minute); err == nil {
		t.Fatal("no error")
	}
	r.stack.configure(func(s *watchStack) { s.healthCode = 0 })
	texts, err := r.stepErr(time.Minute) // the resolution is owed
	if err == nil || len(texts) != 1 || !strings.Contains(texts[0], "RESOLVED health") {
		t.Fatalf("%q %v", texts, err)
	}
	r.stack.configure(func(s *watchStack) { s.healthCode = http.StatusBadGateway })
	texts, err = r.stepErr(time.Minute) // it fires again, to the same webhook
	if err == nil || len(texts) != 1 || !strings.HasPrefix(texts[0], "[mnemon-aml] ALERT health on aml-test\n") || strings.Contains(texts[0], "still firing") {
		t.Fatalf("%q %v", texts, err)
	}
	if got := r.firing(); len(got) != 1 || got["health"] {
		t.Fatal(got)
	}
	r.stack.configure(func(s *watchStack) { s.hookCode = 0 })
	if message := onlyAlert(t, r.step(time.Minute), "health"); !strings.HasPrefix(message, "[mnemon-aml] ALERT health on aml-test\n") {
		t.Fatal(message)
	}
	// Then it ends, and the resolution is the ordinary one: this alert was
	// delivered.
	r.stack.configure(func(s *watchStack) { s.healthCode = 0 })
	if message := onlyAlert(t, r.step(time.Minute), "health"); !strings.HasPrefix(message, "[mnemon-aml] RESOLVED health on aml-test (after 4m0s)\n") {
		t.Fatal(message)
	}
}

// Only one aml-watch run works at a time: a run that finds the lock of the
// state held by another does nothing, and exits 0.
func TestWatchOverlappingRunDoesNothing(t *testing.T) {
	stack := newWatchStack(t)
	state := filepath.Join(t.TempDir(), "state", "watch-state.json")
	t.Setenv("AML_ALERT_WEBHOOK", stack.webhookURL())
	t.Setenv("AML_ALERT_KIND", "feishu")
	args := []string{"--admin", stack.admin.URL, "--public-url", stack.public.URL, "--state", state}

	// A first run, which says the public side is down, and leaves the state
	// and the lock file.
	stack.configure(func(s *watchStack) { s.healthCode = http.StatusBadGateway })
	if out, err := executeAMLWatch(args...); err != nil || strings.Contains(out, "holds the lock") {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := stack.take(t); len(got) != 1 || !strings.Contains(got[0], "ALERT health") {
		t.Fatal(got)
	}
	// Now there is plenty for a run to do: health is back, and a Search degraded.
	stack.configure(func(s *watchStack) { s.healthCode = 0 })
	stack.set(func(s *aml.AdminStatus) { s.Degraded = 1 })
	before, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}

	// The test is the other run.
	held := flock.New(state + ".lock")
	if locked, err := held.TryLock(); !locked || err != nil {
		t.Fatal(locked, err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			if err := held.Unlock(); err != nil { // before t.TempDir's cleanup: Windows will not remove an open file
				t.Error(err)
			}
		}
	}
	defer release()
	attempts := stack.attempts()
	out, err := executeAMLWatch(args...)
	if err != nil {
		t.Fatalf("a run that found the lock held failed: %v\n%s", err, out)
	}
	if lines := strings.Count(out, "another aml-watch run holds the lock"); lines != 1 || strings.Count(out, "\n") != 1 || !strings.Contains(out, "level=WARN") || !strings.Contains(out, state+".lock") {
		t.Fatalf("it should say once that another run holds the lock:\n%s", out)
	}
	if got := stack.attempts(); got != attempts {
		t.Fatal("a run that did not hold the lock posted", got-attempts)
	}
	if after, err := os.ReadFile(state); err != nil || !bytes.Equal(before, after) {
		t.Fatalf("a run that did not hold the lock saved the state: %v\n%s\n%s", err, before, after)
	}
	entries, err := os.ReadDir(filepath.Dir(state))
	if err != nil || len(entries) != 2 { // the state and the lock; no temporary file
		t.Fatal(entries, err)
	}

	// Once the other run is over the next one does its work, which the skipped
	// run left whole, and gives the lock back.
	release()
	if out, err = executeAMLWatch(args...); err != nil || strings.Contains(out, "holds the lock") {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := stack.take(t); len(got) != 2 || !strings.Contains(got[0], "ALERT degraded-search") || !strings.Contains(got[1], "RESOLVED health") {
		t.Fatalf("%q", got)
	}
	if locked, err := held.TryLock(); !locked || err != nil {
		t.Fatal("the run kept the lock:", locked, err)
	}
	held.Unlock()
}

// A run that is refused for its settings touches nothing, the lock file
// included.
func TestWatchRefusedRunLeavesNoLockFile(t *testing.T) {
	r := newWatchRig(t)
	r.w.kind = "slack"
	if _, err := r.stepErr(0); err == nil {
		t.Fatal("no error")
	}
	for _, path := range []string{r.state, r.state + ".lock", filepath.Dir(r.state)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s: %v", path, err)
		}
	}
}

// An http webhook is for this host: a loopback address or localhost, in any
// case. Everything else must be https, and nothing is echoed when it is not.
func TestWatchWebhookSchemeRules(t *testing.T) {
	for webhook, ok := range map[string]bool{
		"https://hooks.example.com/hook/" + watchToken:     true,
		"https://open.feishu.cn/open-apis/bot/v2/hook/abc": true,
		"http://127.0.0.1:8080/hook/" + watchToken:         true,
		"http://[::1]:8080/hook/" + watchToken:             true,
		"http://localhost:8080/hook/" + watchToken:         true,
		"http://LOCALHOST:8080/hook/" + watchToken:         true,
		"http://LocalHost/hook/" + watchToken:              true,
		"https://localhost.example.com/hook/" + watchToken: true,
		"http://hooks.example.com/hook/" + watchToken:      false,
		"http://localhost.example.com/hook/" + watchToken:  false,
		"http://notlocalhost/hook/" + watchToken:           false,
		"http://localhost.evil.com:80/hook/" + watchToken:  false,
		"http://127.0.0.1.example.com/hook/" + watchToken:  false,
		"ftp://localhost/hook/" + watchToken:               false,
		"localhost:8080/hook/" + watchToken:                false,
		"//localhost/hook/" + watchToken:                   false,
		"https:///hook/" + watchToken:                      false,
		"hooks.example.com/hook/" + watchToken:             false,
		"https://hooks.example.com/hook/%zz" + watchToken:  false,
	} {
		w := &watcher{watchSettings: watchSettings{webhook: webhook, kind: "feishu"}}
		err := w.checkDelivery()
		if (err == nil) != ok {
			t.Errorf("%q: %v, want accepted %v", webhook, err, ok)
		}
		if err != nil && (strings.Contains(err.Error(), watchToken) || !strings.Contains(err.Error(), "AML_ALERT_WEBHOOK must be an https URL")) {
			t.Errorf("%q: %v", webhook, err)
		}
	}
}

// ---- the admin token ----

// watchAdminToken is the bearer token of the admin listener the watcher reads.
const watchAdminToken = "mnm_w4tch-adm1n-t0ken-0123456789abcdefghij"

// neverShows fails for every text of texts that holds a secret.
func neverShows(t *testing.T, texts map[string]string, secrets ...string) {
	t.Helper()
	for name, text := range texts {
		for _, secret := range secrets {
			if strings.Contains(text, secret) {
				t.Errorf("%s shows %q:\n%s", name, secret, text)
			}
		}
	}
}

// The watcher sends the admin token on every read of the admin listener, and
// only there: not to the public listener, not along a redirect. The token is
// in no message, log line, error or state, however the reads end.
func TestWatchSendsAdminToken(t *testing.T) {
	bearer := "Bearer " + watchAdminToken

	t.Run("every admin read, and nothing else", func(t *testing.T) {
		r := newWatchRig(t)
		r.stack.configure(func(s *watchStack) { s.adminToken = watchAdminToken })
		r.w.adminToken = watchAdminToken
		if texts := r.step(0); len(texts) != 0 {
			t.Fatal("the first run said", texts)
		}
		r.stack.set(func(s *aml.AdminStatus) { s.Degraded = 1 })
		if texts := r.step(time.Minute); len(texts) != 1 {
			t.Fatal(texts)
		}
		// /ready and /admin/status, twice; /health is the public listener's.
		admin, public := r.stack.authorizations()
		if !slices.Equal(admin, []string{bearer, bearer, bearer, bearer}) || !slices.Equal(public, []string{"", ""}) {
			t.Fatalf("admin reads %q, public reads %q", admin, public)
		}
		state, _ := os.ReadFile(r.state)
		neverShows(t, map[string]string{"the log": r.logs.String(), "the state": string(state)}, watchAdminToken)
	})

	t.Run("no token configured, no header", func(t *testing.T) {
		r := newWatchRig(t)
		r.step(0)
		if admin, _ := r.stack.authorizations(); !slices.Equal(admin, []string{"", ""}) {
			t.Fatalf("%q", admin)
		}
	})

	// A token the server refuses is an alert that says so by the status, and
	// repeats nothing: not the token, not the header.
	t.Run("a refused token", func(t *testing.T) {
		r := newWatchRig(t)
		r.stack.configure(func(s *watchStack) { s.adminToken = watchAdminToken })
		r.w.adminToken = "not-" + watchAdminToken
		message := onlyAlert(t, r.step(0), "ready")
		if !strings.Contains(message, "/ready answered 401; /admin/status answered 401") {
			t.Fatal(message)
		}
		state, _ := os.ReadFile(r.state)
		neverShows(t, map[string]string{"the message": message, "the log": r.logs.String(), "the state": string(state)}, watchAdminToken, r.w.adminToken, "Bearer")
	})

	// A redirect is not followed with the token: its Location is not the
	// operator's, and the public listener here is on the same host.
	t.Run("a redirect", func(t *testing.T) {
		r := newWatchRig(t)
		r.stack.configure(func(s *watchStack) {
			s.adminToken = watchAdminToken
			s.adminRedirect = r.stack.public.URL + "/health"
		})
		r.w.adminToken = watchAdminToken
		message := onlyAlert(t, r.step(0), "ready")
		if !strings.Contains(message, "/ready answered 302; /admin/status answered 302") {
			t.Fatal(message)
		}
		_, public := r.stack.authorizations()
		for _, header := range public {
			if header != "" {
				t.Fatalf("the public listener was sent %q", header)
			}
		}
		neverShows(t, map[string]string{"the message": message, "the log": r.logs.String()}, watchAdminToken)
	})

	// Through the command, which reads the token from its variable: however
	// the admin listener fails, no output, error or state shows the token.
	for name, fail := range map[string]func(*watchStack){
		"the server refuses the token": func(s *watchStack) { s.configure(func(s *watchStack) { s.adminToken = "another-" + watchAdminToken }) },
		"HTTP 500":                     func(s *watchStack) { s.configure(func(s *watchStack) { s.readyCode, s.statusCode = 500, 500 }) },
		"not a status":                 func(s *watchStack) { s.configure(func(s *watchStack) { s.statusRaw = "<html>bad gateway</html>" }) },
		"unreachable":                  func(s *watchStack) { s.admin.Close() },
	} {
		t.Run(name, func(t *testing.T) {
			stack := newWatchStack(t)
			stack.configure(func(s *watchStack) { s.adminToken = watchAdminToken })
			state := filepath.Join(t.TempDir(), "state", "watch-state.json")
			t.Setenv("AML_ALERT_WEBHOOK", stack.webhookURL())
			t.Setenv("AML_ALERT_KIND", "feishu")
			t.Setenv("AML_TEST_ADMIN_TOKEN", watchAdminToken)
			var out bytes.Buffer
			run := func() error {
				command := newAMLWatchCommand()
				command.SetArgs([]string{"--admin", stack.admin.URL, "--public-url", stack.public.URL, "--state", state, "--admin-token-env", "AML_TEST_ADMIN_TOKEN"})
				command.SetOut(&out)
				command.SetErr(&out)
				err := command.Execute()
				if err != nil {
					fmt.Fprintln(&out, err) // what the product root prints after an error
				}
				return err
			}
			// All is well with the token: nothing to say.
			if err := run(); err != nil || len(stack.take(t)) != 0 {
				t.Fatalf("%v\n%s", err, out.String())
			}
			fail(stack)
			if err := run(); err != nil {
				t.Fatalf("%v\n%s", err, out.String())
			}
			posted := stack.take(t)
			if len(posted) != 1 || !strings.Contains(posted[0], " ready on ") {
				t.Fatalf("%q\n%s", posted, out.String())
			}
			raw, _ := os.ReadFile(state)
			neverShows(t, map[string]string{"the output": out.String(), "the message": posted[0], "the state": string(raw)}, watchAdminToken, "Bearer")
		})
	}
}

// The reads follow redirects as net/http does, ten at most, when they carry no
// token: the policy that stops a read with the token is the only difference.
func TestWatchFollowsAtMostTenRedirects(t *testing.T) {
	var last atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hop := 0
		if r.URL.Path != "/health" {
			fmt.Sscanf(r.URL.Path, "/hop/%d", &hop)
		}
		if int64(hop) >= last.Load() {
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/hop/%d", hop+1), http.StatusFound)
	}))
	t.Cleanup(server.Close)
	w := newWatcher(watchSettings{public: server.URL}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for hops, want := range map[int64]string{0: "", 3: "", 100: "stopped after 10 redirects"} {
		last.Store(hops)
		got := w.probeHealth(context.Background())
		if want == "" && got != "" || want != "" && !strings.Contains(got, want) {
			t.Errorf("%d redirects: %q, want %q", hops, got, want)
		}
	}
}

// A flag that names a variable needs it: the run is refused, with the
// variable's name and nothing written, when it is unset or empty.
func TestWatchAdminTokenVariable(t *testing.T) {
	state := filepath.Join(t.TempDir(), "watch-state.json")
	const variable = "AML_TEST_WATCH_ADMIN_TOKEN"
	for name, set := range map[string]bool{"unset": false, "empty": true} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(variable, "")
			if !set {
				os.Unsetenv(variable)
			}
			out, err := executeAMLWatch("--state", state, "--admin-token-env", variable)
			if err == nil || !strings.Contains(err.Error(), "--admin-token-env") || !strings.Contains(err.Error(), variable) || !strings.Contains(err.Error(), "unset or empty") {
				t.Fatalf("%v\n%s", err, out)
			}
			if _, statErr := os.Stat(state); !os.IsNotExist(statErr) {
				t.Errorf("a refused run wrote state: %v", statErr)
			}
		})
	}
	// Set, it is read once, as it is: no trimming, no length rule (the
	// server's rule).
	t.Setenv(variable, "short")
	command := newAMLWatchCommand()
	if err := command.ParseFlags([]string{"--state", state, "--admin-token-env", variable}); err != nil {
		t.Fatal(err)
	}
	if settings, err := amlWatchSettings(command); err != nil || settings.adminToken != "short" {
		t.Fatal(settings.adminToken, err)
	}
}

// ---- the instance's name ----

// With --instance every message header says the instance: on host/name, in a
// new alert, a repeat, a resolution and the resolution of an alert that was
// never delivered. Without it the header is the host alone.
func TestWatchNamesTheInstance(t *testing.T) {
	r := newWatchRig(t)
	r.w.instance = "beta-1"
	r.step(0)
	r.stack.configure(func(s *watchStack) { s.healthCode = http.StatusBadGateway })
	if first := onlyAlert(t, r.step(time.Minute), "health"); !strings.HasPrefix(first, watchWantTag+"ALERT health on aml-test/beta-1\n") {
		t.Fatal(first)
	}
	if repeat := onlyAlert(t, r.step(30*time.Minute), "health"); !strings.HasPrefix(repeat, watchWantTag+"ALERT health on aml-test/beta-1 (still firing after 30m0s)\n") {
		t.Fatal(repeat)
	}
	r.stack.configure(func(s *watchStack) { s.healthCode = 0 })
	if resolved := onlyAlert(t, r.step(time.Minute), "health"); !strings.HasPrefix(resolved, watchWantTag+"RESOLVED health on aml-test/beta-1 (after 31m0s)\n") {
		t.Fatal(resolved)
	}

	// An alert that never went out and is resolved: the note that says so is
	// after the name too.
	r = newWatchRig(t)
	r.w.instance = "beta-1"
	r.step(0)
	r.stack.configure(func(s *watchStack) { s.hookCode, s.healthCode = http.StatusInternalServerError, http.StatusBadGateway })
	if texts, err := r.stepErr(time.Minute); err == nil || len(texts) != 1 || !strings.HasPrefix(texts[0], watchWantTag+"ALERT health on aml-test/beta-1\n") {
		t.Fatalf("%q %v", texts, err)
	}
	r.stack.configure(func(s *watchStack) { s.hookCode, s.healthCode = 0, 0 })
	if resolved := onlyAlert(t, r.step(time.Minute), "health"); !strings.HasPrefix(resolved, watchWantTag+"RESOLVED health on aml-test/beta-1 (after 1m0s, alert not delivered)\n") {
		t.Fatal(resolved)
	}

	// Without the flag the bytes are the host's alone.
	r = newWatchRig(t)
	r.step(0)
	r.stack.configure(func(s *watchStack) { s.healthCode = http.StatusBadGateway })
	if first := onlyAlert(t, r.step(time.Minute), "health"); !strings.HasPrefix(first, watchWantTag+"ALERT health on aml-test\n") {
		t.Fatal(first)
	}

	// The name is 2 to 31 characters: lowercase letters, digits and hyphens,
	// starting with a letter. The run is refused otherwise, naming the flag.
	state := filepath.Join(t.TempDir(), "watch-state.json")
	settings := func(name string) (watchSettings, error) {
		command := newAMLWatchCommand()
		if err := command.ParseFlags([]string{"--state", state, "--instance", name}); err != nil {
			t.Fatal(err)
		}
		return amlWatchSettings(command)
	}
	for _, name := range []string{"", "ab", "a1", "beta-1", "evaluation", "a-b-c-1-2-3", strings.Repeat("a", 31)} {
		if got, err := settings(name); err != nil || got.instance != name {
			t.Errorf("%q: %q %v", name, got.instance, err)
		}
	}
	for _, name := range []string{"a", "A1", "Beta-1", "1beta", "-beta", "beta_1", "beta 1", "beta.1", "beta/1", "bêta", "beta-1\n", strings.Repeat("a", 32)} {
		if _, err := settings(name); err == nil || !strings.Contains(err.Error(), "--instance") {
			t.Errorf("%q: %v", name, err)
		}
		if out, err := executeAMLWatch("--state", state, "--instance", name); err == nil || !strings.Contains(err.Error(), "--instance") {
			t.Errorf("%q: %v\n%s", name, err, out)
		}
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Errorf("a refused run wrote state: %v", err)
	}
}
