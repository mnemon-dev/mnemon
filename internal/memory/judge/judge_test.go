package judge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordedRequest struct {
	Path          string
	Authorization string
	Model         string
	State         string
	Questions     map[string]wireQuestion
}

// fakeServer is an httptest System One server. handle receives each decoded
// request and returns the HTTP status and body to send back.
type fakeServer struct {
	mu       sync.Mutex
	requests []recordedRequest
}

func (f *fakeServer) recorded() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedRequest(nil), f.requests...)
}

func startFake(t *testing.T, apiKey string, handle func(recordedRequest) (int, string)) (*Client, *fakeServer) {
	t.Helper()
	fake := &fakeServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model     string                  `json:"model"`
			State     json.RawMessage         `json:"state"`
			Questions map[string]wireQuestion `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, `{"error":"bad body"}`, http.StatusBadRequest)
			return
		}
		rec := recordedRequest{
			Path:          r.URL.Path,
			Authorization: r.Header.Get("Authorization"),
			Model:         body.Model,
			State:         string(body.State),
			Questions:     body.Questions,
		}
		fake.mu.Lock()
		fake.requests = append(fake.requests, rec)
		fake.mu.Unlock()
		status, payload := handle(rec)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("MNEMON_JUDGE_MODEL", "test-model")
	t.Setenv("MNEMON_JUDGE_ENDPOINT", srv.URL)
	t.Setenv("MNEMON_JUDGE_API_KEY", apiKey)
	t.Setenv("MNEMON_JUDGE_TIMEOUT", "")
	client := NewClient()
	if client == nil {
		t.Fatal("NewClient returned nil with MNEMON_JUDGE_MODEL set")
	}
	return client, fake
}

// noulAnswers answers every asked question with the same noul value.
func noulAnswers(req recordedRequest, value float64) string {
	answers := make([]string, 0, len(req.Questions))
	for name := range req.Questions {
		answers = append(answers, fmt.Sprintf(`%q:{"type":"noul","noul":%v}`, name, value))
	}
	return `{"model":"test-model","answers":{` + strings.Join(answers, ",") + `}}`
}

func TestNewClient_nilWhenModelUnset(t *testing.T) {
	// Given no judge model
	t.Setenv("MNEMON_JUDGE_MODEL", "")

	// When the client is built
	client := NewClient()

	// Then the judge is disabled
	if client != nil {
		t.Fatalf("NewClient() = %+v, want nil", client)
	}
}

func TestNewClient_envDefaultsAndOverrides(t *testing.T) {
	// Given only a model
	t.Setenv("MNEMON_JUDGE_MODEL", "clef")
	t.Setenv("MNEMON_JUDGE_ENDPOINT", "")
	t.Setenv("MNEMON_JUDGE_API_KEY", "")
	t.Setenv("MNEMON_JUDGE_TIMEOUT", "")

	// When the client is built
	client := NewClient()

	// Then the documented defaults apply
	if client.endpoint != DefaultEndpoint || client.timeout != DefaultTimeout || client.apiKey != "" {
		t.Fatalf("defaults = endpoint %q timeout %v key %q", client.endpoint, client.timeout, client.apiKey)
	}

	// Given explicit settings and an unusable timeout
	t.Setenv("MNEMON_JUDGE_ENDPOINT", "https://judge.example.test")
	t.Setenv("MNEMON_JUDGE_API_KEY", "secret")
	t.Setenv("MNEMON_JUDGE_TIMEOUT", "250ms")
	override := NewClient()
	t.Setenv("MNEMON_JUDGE_TIMEOUT", "soon")
	invalid := NewClient()

	// Then overrides are honored and a bad timeout falls back to the default
	if override.endpoint != "https://judge.example.test" || override.apiKey != "secret" ||
		override.timeout != 250*time.Millisecond {
		t.Fatalf("override = %+v", override)
	}
	if invalid.timeout != DefaultTimeout {
		t.Fatalf("invalid timeout resolved to %v, want %v", invalid.timeout, DefaultTimeout)
	}
}

func TestNoul_requestShapeAndParsing(t *testing.T) {
	// Given a keyed server that answers 0.9 and 0.2
	client, fake := startFake(t, "k-123", func(recordedRequest) (int, string) {
		return 200, `{"model":"test-model","answers":{
			"m0":{"type":"noul","noul":0.9},"m1":{"type":"noul","noul":0.2}},
			"usage":{"input_tokens":10,"output_tokens":2}}`
	})
	state := map[string]any{"query": "q", "memories": map[string]string{"m0": "a", "m1": "b"}}

	// When two noul questions are asked
	got, err := client.Noul(context.Background(), state, []Question{
		{Name: "m0", Instructions: "is m0 relevant?"},
		{Name: "m1", Instructions: "is m1 relevant?"},
	})

	// Then the typed answers come back and the request carried the contract
	if err != nil {
		t.Fatalf("Noul: %v", err)
	}
	if got["m0"] != 0.9 || got["m1"] != 0.2 {
		t.Fatalf("answers = %v", got)
	}
	reqs := fake.recorded()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	r := reqs[0]
	if r.Path != "/v1/systemone" || r.Model != "test-model" || r.Authorization != "Bearer k-123" {
		t.Fatalf("request = %+v", r)
	}
	if r.State != `{"memories":{"m0":"a","m1":"b"},"query":"q"}` {
		t.Fatalf("state = %s", r.State)
	}
	if q := r.Questions["m1"]; q.Type != "noul" || q.Instructions != "is m1 relevant?" {
		t.Fatalf("question m1 = %+v", q)
	}
}

func TestNoul_omitsAuthorizationWithoutKey(t *testing.T) {
	// Given a server and no API key
	client, fake := startFake(t, "", func(req recordedRequest) (int, string) { return 200, noulAnswers(req, 0.5) })

	// When a question is asked
	if _, err := client.Noul(context.Background(), "state", []Question{{Name: "a", Instructions: "?"}}); err != nil {
		t.Fatalf("Noul: %v", err)
	}

	// Then no Authorization header is sent
	if got := fake.recorded()[0].Authorization; got != "" {
		t.Fatalf("Authorization = %q, want none", got)
	}
}

func TestNoul_chunksAtSixteenWithIdenticalState(t *testing.T) {
	// Given 35 questions
	client, fake := startFake(t, "", func(req recordedRequest) (int, string) { return 200, noulAnswers(req, 0.75) })
	questions := make([]Question, 35)
	for i := range questions {
		questions[i] = Question{Name: fmt.Sprintf("m%d", i), Instructions: "?"}
	}

	// When they are asked in one call
	got, err := client.Noul(context.Background(), map[string]string{"query": "q"}, questions)

	// Then three requests carry the same state and every question is answered once
	if err != nil {
		t.Fatalf("Noul: %v", err)
	}
	reqs := fake.recorded()
	if len(reqs) != 3 {
		t.Fatalf("requests = %d, want 3", len(reqs))
	}
	asked := map[string]int{}
	for _, r := range reqs {
		if len(r.Questions) > MaxQuestionsPerRequest {
			t.Fatalf("chunk has %d questions", len(r.Questions))
		}
		if r.State != reqs[0].State {
			t.Fatalf("state differs across chunks: %s vs %s", r.State, reqs[0].State)
		}
		for name := range r.Questions {
			asked[name]++
		}
	}
	if len(asked) != 35 || len(got) != 35 {
		t.Fatalf("asked %d distinct questions, got %d answers; want 35", len(asked), len(got))
	}
	for name, n := range asked {
		if n != 1 {
			t.Fatalf("question %s asked %d times", name, n)
		}
	}
}

func TestChoice_parsesAnswerAndProbability(t *testing.T) {
	// Given a server that picks "supersedes" and a second answer carrying only confidence
	client, fake := startFake(t, "", func(recordedRequest) (int, string) {
		return 200, `{"answers":{
			"c0":{"type":"choice","choice":"supersedes","probabilities":{"supersedes":0.8,"related":0.2},"confidence":0.8},
			"c1":{"type":"choice","choice":"related","confidence":0.6}}}`
	})
	options := map[string]string{"supersedes": "replaces it", "related": "same subject"}

	// When two choice questions are asked
	got, err := client.Choice(context.Background(), "state", []ChoiceQuestion{
		{Name: "c0", Instructions: "how?", Options: options},
		{Name: "c1", Instructions: "how?", Options: options},
	})

	// Then probabilities come from the map, falling back to confidence, and criteria were sent
	if err != nil {
		t.Fatalf("Choice: %v", err)
	}
	if got["c0"] != (ChoiceAnswer{Choice: "supersedes", Probability: 0.8}) ||
		got["c1"] != (ChoiceAnswer{Choice: "related", Probability: 0.6}) {
		t.Fatalf("answers = %+v", got)
	}
	q := fake.recorded()[0].Questions["c0"]
	if q.Type != "choice" || q.Criteria["supersedes"] != "replaces it" {
		t.Fatalf("question = %+v", q)
	}
}

func TestAsk_rejectsBadResponses(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantErr string
	}{
		{"server error json", 400, `{"error":"unknown model"}`, "status 400: unknown model"},
		{"server error without json", 500, `oops`, "status 500"},
		{"malformed json", 200, `{"answers":`, "decode judge response"},
		{"missing answer", 200, `{"answers":{}}`, "missing an answer"},
		{"noul missing value", 200, `{"answers":{"a":{"type":"noul"}}}`, "missing or out-of-range"},
		{"noul out of range", 200, `{"answers":{"a":{"type":"noul","noul":1.5}}}`, "missing or out-of-range"},
		{"wrong answer type", 200, `{"answers":{"a":{"type":"choice","choice":"x"}}}`, "want \"noul\""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given a server that misbehaves
			client, _ := startFake(t, "", func(recordedRequest) (int, string) { return tc.status, tc.body })

			// When a question is asked
			_, err := client.Noul(context.Background(), "state", []Question{{Name: "a", Instructions: "?"}})

			// Then the call fails with a descriptive error
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestChoice_rejectsInvalidAnswers(t *testing.T) {
	cases := map[string]string{
		"unoffered choice":    `{"answers":{"c":{"type":"choice","choice":"nonsense","confidence":0.9}}}`,
		"no choice":           `{"answers":{"c":{"type":"choice","confidence":0.9}}}`,
		"probability range":   `{"answers":{"c":{"type":"choice","choice":"related","probabilities":{"related":2}}}}`,
		"no probability data": `{"answers":{"c":{"type":"choice","choice":"related"}}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			// Given a server returning an unusable choice answer
			client, _ := startFake(t, "", func(recordedRequest) (int, string) { return 200, body })

			// When the question is asked
			_, err := client.Choice(context.Background(), "state", []ChoiceQuestion{
				{Name: "c", Instructions: "?", Options: map[string]string{"related": "x"}},
			})

			// Then it is rejected
			if err == nil {
				t.Fatal("Choice accepted an invalid answer")
			}
		})
	}
}

func TestAsk_rejectsDuplicateQuestionNames(t *testing.T) {
	// Given a client and two questions sharing a name
	client, fake := startFake(t, "", func(req recordedRequest) (int, string) { return 200, noulAnswers(req, 1) })

	// When they are asked
	_, err := client.Noul(context.Background(), "s", []Question{{Name: "a"}, {Name: "a"}})

	// Then the call fails before any request is sent
	if err == nil || len(fake.recorded()) != 0 {
		t.Fatalf("err = %v, requests = %d", err, len(fake.recorded()))
	}
}

func TestAsk_timesOutOnSlowServer(t *testing.T) {
	// Given a server that never answers and a short timeout
	release := make(chan struct{})
	client, _ := startFake(t, "", func(recordedRequest) (int, string) {
		<-release
		return 200, `{}`
	})
	t.Cleanup(func() { close(release) })
	client.timeout = 50 * time.Millisecond

	// When a question is asked
	_, err := client.Noul(context.Background(), "s", []Question{{Name: "a", Instructions: "?"}})

	// Then the deadline error surfaces
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("err = %v, want a deadline error", err)
	}
}

func TestAsk_bypassesProxyForLoopback(t *testing.T) {
	// Given a proxy environment pointing at a dead address
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("NO_PROXY", "")
	client, _ := startFake(t, "", func(req recordedRequest) (int, string) { return 200, noulAnswers(req, 0.5) })

	// When the judge reaches a loopback server
	_, err := client.Noul(context.Background(), "s", []Question{{Name: "a", Instructions: "?"}})

	// Then the proxy is not used
	if err != nil {
		t.Fatalf("loopback request went through the proxy: %v", err)
	}
}
