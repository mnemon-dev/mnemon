package memoryagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func mockResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}
}

func TestJEVRequiresMeteringAndTypedPresentAnswers(t *testing.T) {
	budget, err := OpenBudget(filepath.Join(t.TempDir(), "budget.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer budget.Close()
	t.Setenv("UNIT_JEV_CREDENTIAL", "test-only-placeholder")
	p, err := NewProvider(ModelConfig{Kind: "jev", Model: "jev-1.13.0", KeyEnv: "UNIT_JEV_CREDENTIAL"}, budget)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"model":"jev-1.13.0","answers":{"a":{"type":"noul","noul":0.8}}}`
	var attempts atomic.Int32
	p.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		attempts.Add(1)
		if req.URL.String() != "https://api.typesafe.ai/v1/systemone" || req.Header.Get("Authorization") == "" {
			t.Fatal("provider routing or authentication failed")
		}
		var request map[string]any
		json.NewDecoder(req.Body).Decode(&request)
		if request["model"] != "jev-1.13.0" {
			t.Fatal("model was not pinned")
		}
		return mockResponse(body), nil
	})
	questions := map[string]Question{"a": binary("Relevant?")}
	if _, _, err = p.Decide(context.Background(), "evidence", questions); err == nil {
		t.Fatal("missing paid metering was accepted")
	}
	var held, spent float64
	budget.db.QueryRow("SELECT held,spent FROM accounts WHERE kind='jev'").Scan(&held, &spent)
	if held != 0.01 || spent != 0 || attempts.Load() != 1 {
		t.Fatal("failed call retried or reservation refunded", held, spent, attempts.Load())
	}
	body = `{"model":"jev-1.13.0","answers":{"a":{"type":"noul","noul":null}},"usage":{"input_tokens":1000,"output_tokens":1}}`
	if _, _, err = p.Decide(context.Background(), "evidence", questions); err == nil {
		t.Fatal("null JEV answer became false")
	}
	body = `{"model":"jev-1.13.0","answers":{"a":{"type":"noul","noul":0.8}},"usage":{"input_tokens":1000,"output_tokens":1}}`
	answers, usage, err := p.Decide(context.Background(), "evidence", questions)
	if err != nil || answers["a"] != 0.8 || usage.USD != 0.000042 {
		t.Fatal(answers, usage, err)
	}
	budget.db.QueryRow("SELECT held,spent FROM accounts WHERE kind='jev'").Scan(&held, &spent)
	if held < 0.009999 || held > 0.010001 || spent < 0.0000839 || spent > 0.0000841 {
		t.Fatal("metering did not preserve ambiguous reservation", held, spent)
	}
}

func TestBudgetAtomicCapAcrossOwners(t *testing.T) {
	path := filepath.Join(t.TempDir(), "budget.db")
	caps := map[string]float64{"jev": 0.02}
	a, err := OpenBudgetWithCaps(path, caps)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := OpenBudgetWithCaps(path, caps)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var successes atomic.Int32
	var work sync.WaitGroup
	for i := 0; i < 20; i++ {
		work.Add(1)
		go func(i int) {
			defer work.Done()
			owner := a
			if i%2 == 1 {
				owner = b
			}
			if _, err := owner.reserve(context.Background(), "jev", "request-hash"); err == nil {
				successes.Add(1)
			}
		}(i)
	}
	work.Wait()
	if successes.Load() != 2 {
		t.Fatal("cap failed under concurrent owners", successes.Load())
	}
	var held float64
	if err := a.db.QueryRow("SELECT held FROM accounts WHERE kind='jev'").Scan(&held); err != nil || held > 0.02 {
		t.Fatal("overspent cap", held, err)
	}
}

func TestBudgetConcurrentOwnersReserveAndSettle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "budget.db")
	owners := []*Budget{}
	for range 4 {
		owner, err := OpenBudget(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { owner.Close() })
		owners = append(owners, owner)
	}
	errors := make(chan error, len(owners))
	var work sync.WaitGroup
	for _, owner := range owners {
		work.Add(1)
		go func() {
			defer work.Done()
			for range 30 {
				id, err := owner.reserve(context.Background(), "jev", "request-hash")
				if err == nil {
					err = owner.settle(context.Background(), id, Usage{InputTokens: 1000, OutputTokens: 1, USD: 0.000042})
				}
				if err != nil {
					errors <- err
					return
				}
			}
		}()
	}
	work.Wait()
	close(errors)
	for err := range errors {
		t.Fatal("concurrent settlement failed", err)
	}
	var spent, held float64
	owners[0].db.QueryRow("SELECT spent,held FROM accounts WHERE kind='jev'").Scan(&spent, &held)
	if spent < 0.005039 || spent > 0.005041 || held < -1e-8 || held > 1e-8 {
		t.Fatal("settlement lost metering or left reservations", spent, held)
	}
}

func TestLocalDecisionNullAndEndpointGuards(t *testing.T) {
	p, err := NewProvider(ModelConfig{Kind: "local", Model: "local-test", BaseURL: "http://127.0.0.1:1/v1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		return mockResponse(`{"choices":[{"message":{"content":"{\"a\":null}"}}]}`), nil
	})
	if _, _, err = p.Decide(context.Background(), "data", map[string]Question{"a": binary("yes?")}); err == nil {
		t.Fatal("null local decision became zero")
	}
	for _, config := range []ModelConfig{
		{Kind: "local", Model: "test", BaseURL: "https://example.com/v1"},
		{Kind: "local", Model: "test", BaseURL: "http://secret@localhost/v1"},
		{Kind: "jev", Model: "jev-latest"},
		{Kind: "jev", Model: "jev-1.13.0", BaseURL: "http://localhost"},
	} {
		if _, err := NewProvider(config, nil); err == nil {
			t.Fatal("unsupported endpoint/model allowed")
		}
	}
}

func TestJSONModeIsExplicitForStructuredWork(t *testing.T) {
	p, _ := NewProvider(ModelConfig{Kind: "local", Model: "local-test", BaseURL: "http://127.0.0.1:1/v1"}, nil)
	var format any
	p.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		var body map[string]any
		json.NewDecoder(req.Body).Decode(&body)
		format = body["response_format"]
		if format != nil && body["max_tokens"] != float64(2048) || format == nil && body["max_tokens"] != float64(1024) {
			t.Fatal("unexpected output budget", body["max_tokens"])
		}
		return mockResponse(`{"choices":[{"message":{"content":"{}"}}]}`), nil
	})
	if _, _, err := p.CompleteJSON(context.Background(), "Return JSON", "data"); err != nil || format.(map[string]any)["type"] != "json_object" {
		t.Fatal(format, err)
	}
	if _, _, err := p.Complete(context.Background(), "Return search lines", "data"); err != nil || format != nil {
		t.Fatal("structured output changed free-text planning", format, err)
	}
}

func TestTruncatedCompletionFailsWithoutRetry(t *testing.T) {
	p, _ := NewProvider(ModelConfig{Kind: "local", Model: "local-test", BaseURL: "http://127.0.0.1:1/v1"}, nil)
	calls := 0
	p.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return mockResponse(`{"choices":[{"message":{"content":"{}"},"finish_reason":"length"}],"usage":{"prompt_tokens":10,"completion_tokens":2048}}`), nil
	})
	if _, usage, err := p.CompleteJSON(context.Background(), "JSON only", "data"); err == nil || usage.OutputTokens != 2048 || calls != 1 {
		t.Fatal("truncated completion was accepted or retried", usage, err, calls)
	}
}

func settleDeepSeekCNY(t *testing.T, b *Budget, count int) error {
	t.Helper()
	for range count {
		id, err := b.reserve(context.Background(), "deepseek", "request-hash")
		if err != nil {
			return err
		}
		if err = b.settle(context.Background(), id, Usage{InputTokens: 1, CNY: 0.25}); err != nil {
			t.Fatal("settlement failed", err)
		}
	}
	return nil
}

func TestBudgetConfiguredCapRaisesDefault(t *testing.T) {
	raised, err := OpenBudgetWithCaps(filepath.Join(t.TempDir(), "raised.db"), map[string]float64{"deepseek": 60})
	if err != nil {
		t.Fatal(err)
	}
	defer raised.Close()
	if err = settleDeepSeekCNY(t, raised, 220); err != nil { // CNY 55 spent
		t.Fatal("configured cap did not raise the default", err)
	}
	if _, err = raised.reserve(context.Background(), "deepseek", "request-hash"); err != nil {
		t.Fatal("reservation under the raised cap failed", err)
	}

	defaulted, err := OpenBudget(filepath.Join(t.TempDir(), "default.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer defaulted.Close()
	err = settleDeepSeekCNY(t, defaulted, 220)
	if err == nil || !strings.Contains(err.Error(), "model spending cap reached") {
		t.Fatal("default DeepSeek cap did not stop at CNY 50", err)
	}
	var spent float64
	if err = defaulted.db.QueryRow("SELECT spent FROM accounts WHERE kind='deepseek'").Scan(&spent); err != nil || spent != 50 {
		t.Fatal("default cap changed", spent, err)
	}
}

func TestBudgetOperatorCapReplacesStoredCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "budget.db")
	reserveCount := func(caps map[string]float64) int {
		b, err := OpenBudgetWithCaps(path, caps)
		if err != nil {
			t.Fatal(err)
		}
		defer b.Close()
		count := 0
		for {
			if _, err = b.reserve(context.Background(), "deepseek", "request-hash"); err != nil {
				return count
			}
			count++
		}
	}
	// Reservations are CNY 0.25 and stay held, so each cap admits cap/0.25 in total.
	if got := reserveCount(map[string]float64{"deepseek": 0.5}); got != 2 {
		t.Fatal("configured cap not enforced", got)
	}
	if got := reserveCount(map[string]float64{"deepseek": 0.75}); got != 1 {
		t.Fatal("raised cap did not replace the stored cap", got)
	}
	if got := reserveCount(map[string]float64{"deepseek": 0.25}); got != 0 {
		t.Fatal("lowered cap did not replace the stored cap", got)
	}
	if got := reserveCount(nil); got != 200-3 {
		t.Fatal("nil caps did not restore the default", got)
	}
	for _, caps := range []map[string]float64{{"deepseek": -1}, {"deepseek": math.NaN()}, {"deepseek": math.Inf(1)}, {"unknown": 1}} {
		if b, err := OpenBudgetWithCaps(filepath.Join(t.TempDir(), "bad.db"), caps); err == nil {
			b.Close()
			t.Fatal("invalid budget caps were accepted", caps)
		}
	}
}

func TestBudgetOverrunIsAcceptedOnlyForOpenAI(t *testing.T) {
	b, err := OpenBudget(filepath.Join(t.TempDir(), "budget.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	id, err := b.reserve(context.Background(), "deepseek", "request-hash")
	if err != nil {
		t.Fatal(err)
	}
	if err = b.settle(context.Background(), id, Usage{InputTokens: 1, CNY: 1}); err == nil {
		t.Fatal("DeepSeek metering above its reservation was accepted")
	}
	id, err = b.reserve(context.Background(), "openai", "request-hash")
	if err != nil {
		t.Fatal(err)
	}
	if err = b.settle(context.Background(), id, Usage{InputTokens: 1, USD: 0.5}); err != nil {
		t.Fatal("completed OpenAI call was not recorded", err)
	}
	var held, spent float64
	b.db.QueryRow("SELECT held,spent FROM accounts WHERE kind='openai'").Scan(&held, &spent)
	if held != 0 || spent != 0.5 {
		t.Fatal("OpenAI overrun was not accounted", held, spent)
	}
	if _, err = b.reserve(context.Background(), "openai", "request-hash"); err != nil {
		t.Fatal("default OpenAI cap of USD 5 is not in force", err)
	}
}

func TestOpenAIProviderMetersUSD(t *testing.T) {
	budget, err := OpenBudget(filepath.Join(t.TempDir(), "budget.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer budget.Close()
	t.Setenv("UNIT_OPENAI_CREDENTIAL", "test-only-placeholder")
	var path, authorization string
	var body map[string]any
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, authorization = r.URL.Path, r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&body)
		io.WriteString(w, `{"choices":[{"message":{"content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1000000,"completion_tokens":1000000}}`)
	}))
	defer server.Close()
	p, err := NewProvider(ModelConfig{Kind: "openai", Model: "gpt-4.1-mini", BaseURL: server.URL + "/v1/", KeyEnv: "UNIT_OPENAI_CREDENTIAL"}, budget)
	if err != nil {
		t.Fatal(err)
	}
	p.client.Transport = server.Client().Transport
	text, usage, err := p.Complete(context.Background(), "system", "prompt")
	if err != nil || text != "hello" || usage.USD != 2.0 || usage.CNY != 0 || usage.InputTokens != 1000000 || usage.OutputTokens != 1000000 {
		t.Fatal(text, usage, err)
	}
	if path != "/v1/chat/completions" || authorization != "Bearer test-only-placeholder" {
		t.Fatal("OpenAI routing or authentication failed", path, authorization)
	}
	if body["model"] != "gpt-4.1-mini" || body["temperature"] != float64(0) || body["max_tokens"] != float64(1024) || body["thinking"] != nil || body["chat_template_kwargs"] != nil {
		t.Fatal("OpenAI request carries unsupported or changed fields", body)
	}
	var held, spent float64
	budget.db.QueryRow("SELECT held,spent FROM accounts WHERE kind='openai'").Scan(&held, &spent)
	if held != 0 || spent != 2.0 {
		t.Fatal("OpenAI call was not metered against the openai account", held, spent)
	}
}

func TestOpenAIEndpointAndCredentialGuards(t *testing.T) {
	budget, err := OpenBudget(filepath.Join(t.TempDir(), "budget.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer budget.Close()
	t.Setenv("UNIT_OPENAI_CREDENTIAL", "test-only-placeholder")
	for _, base := range []string{"https://api.example.com/v1", "https://api.example.com/v1/", "http://127.0.0.1:8000/v1", "http://localhost/v1", "http://[::1]:8000/v1"} {
		if _, err := NewProvider(ModelConfig{Kind: "openai", Model: "gpt-4o-mini", BaseURL: base, KeyEnv: "UNIT_OPENAI_CREDENTIAL"}, budget); err != nil {
			t.Fatal("valid OpenAI endpoint rejected", base, err)
		}
	}
	for _, base := range []string{"", "http://api.example.com/v1", "ftp://127.0.0.1/v1", "https://user:secret@api.example.com/v1", "https://api.example.com/v1?x=1", "https://api.example.com/v1#x", "api.example.com/v1"} {
		if _, err := NewProvider(ModelConfig{Kind: "openai", Model: "gpt-4o-mini", BaseURL: base, KeyEnv: "UNIT_OPENAI_CREDENTIAL"}, budget); err == nil {
			t.Fatal("unsafe OpenAI endpoint allowed", base)
		}
	}
	good := ModelConfig{Kind: "openai", Model: "gpt-4o-mini", BaseURL: "https://api.example.com/v1", KeyEnv: "UNIT_OPENAI_CREDENTIAL"}
	noKey := good
	noKey.KeyEnv = ""
	missingKey := good
	missingKey.KeyEnv = "UNIT_OPENAI_UNSET_CREDENTIAL"
	for _, config := range []ModelConfig{noKey, missingKey} {
		if _, err := NewProvider(config, budget); err == nil {
			t.Fatal("OpenAI without a credential was allowed", config)
		}
	}
	if _, err := NewProvider(good, nil); err == nil {
		t.Fatal("OpenAI without a budget was allowed")
	}
}

func TestOpenAIUnknownModelNeedsPrices(t *testing.T) {
	budget, err := OpenBudget(filepath.Join(t.TempDir(), "budget.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer budget.Close()
	t.Setenv("UNIT_OPENAI_CREDENTIAL", "test-only-placeholder")
	config := ModelConfig{Kind: "openai", Model: "foo", BaseURL: "https://api.example.com/v1", KeyEnv: "UNIT_OPENAI_CREDENTIAL"}
	if _, err = NewProvider(config, budget); err == nil || !strings.Contains(err.Error(), "priceIn") {
		t.Fatal("unknown OpenAI model without prices was accepted", err)
	}
	config.PriceIn = 1
	if _, err = NewProvider(config, budget); err == nil || !strings.Contains(err.Error(), "priceOut") {
		t.Fatal("unknown OpenAI model with one price was accepted", err)
	}
	config.PriceOut = 3
	p, err := NewProvider(config, budget)
	if err != nil {
		t.Fatal(err)
	}
	p.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		return mockResponse(`{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":1000000,"completion_tokens":500000}}`), nil
	})
	if _, usage, err := p.Complete(context.Background(), "system", "prompt"); err != nil || usage.USD != 2.5 {
		t.Fatal("configured prices were not used", usage, err)
	}
	// Prices override the built-in table for known models too.
	config = ModelConfig{Kind: "openai", Model: "gpt-4.1-mini", BaseURL: "https://api.example.com/v1", KeyEnv: "UNIT_OPENAI_CREDENTIAL", PriceIn: 1, PriceOut: 2}
	p, err = NewProvider(config, budget)
	if err != nil {
		t.Fatal(err)
	}
	p.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		return mockResponse(`{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":1000000,"completion_tokens":500000}}`), nil
	})
	if _, usage, err := p.Complete(context.Background(), "system", "prompt"); err != nil || usage.USD != 2 {
		t.Fatal("configured prices did not override the table", usage, err)
	}
	config.PriceIn = -1
	if _, err = NewProvider(config, budget); err == nil {
		t.Fatal("negative price accepted")
	}
}

func TestDeepSeekThinkingAndMaxTokens(t *testing.T) {
	budget, err := OpenBudget(filepath.Join(t.TempDir(), "budget.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer budget.Close()
	t.Setenv("UNIT_DEEPSEEK_CREDENTIAL", "test-only-placeholder")
	capture := func(config ModelConfig, jsonOnly bool) map[string]any {
		t.Helper()
		config.Kind, config.Model, config.KeyEnv = "deepseek", "deepseek-flash", "UNIT_DEEPSEEK_CREDENTIAL"
		p, err := NewProvider(config, budget)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		p.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
			json.NewDecoder(req.Body).Decode(&body)
			return mockResponse(`{"choices":[{"message":{"content":"{}"}}],"usage":{"prompt_tokens":100,"completion_tokens":10}}`), nil
		})
		complete := p.Complete
		if jsonOnly {
			complete = p.CompleteJSON
		}
		if _, _, err = complete(context.Background(), "system", "prompt"); err != nil {
			t.Fatal(err)
		}
		return body
	}
	for _, jsonOnly := range []bool{false, true} {
		body := capture(ModelConfig{Thinking: true, MaxTokens: 8192}, jsonOnly)
		thinking, _ := body["thinking"].(map[string]any)
		if thinking["type"] != "enabled" || body["reasoning_effort"] != "high" || body["max_tokens"] != float64(8192) {
			t.Fatal("thinking request is wrong", jsonOnly, body)
		}
		if _, ok := body["temperature"]; ok {
			t.Fatal("thinking request carries a temperature", jsonOnly, body)
		}
		if (body["response_format"] != nil) != jsonOnly {
			t.Fatal("JSON mode changed with thinking", jsonOnly, body)
		}

		// A token limit alone keeps thinking disabled and the temperature pinned.
		body = capture(ModelConfig{MaxTokens: 300}, jsonOnly)
		thinking, _ = body["thinking"].(map[string]any)
		if thinking["type"] != "disabled" || body["temperature"] != float64(0) || body["max_tokens"] != float64(300) || body["reasoning_effort"] != nil {
			t.Fatal("max tokens alone changed the defaults", jsonOnly, body)
		}

		body = capture(ModelConfig{}, jsonOnly)
		thinking, _ = body["thinking"].(map[string]any)
		want := float64(1024)
		if jsonOnly {
			want = 2048
		}
		if thinking["type"] != "disabled" || body["temperature"] != float64(0) || body["max_tokens"] != want || body["reasoning_effort"] != nil {
			t.Fatal("defaults changed", jsonOnly, body)
		}
	}
}

func TestCompleteWithSetsCallLimitAndKeepsCutReplies(t *testing.T) {
	var _ CompleterWithMax = (*Provider)(nil)
	p, err := NewProvider(ModelConfig{Kind: "local", Model: "local-test", BaseURL: "http://127.0.0.1:1/v1", MaxTokens: 777}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	p.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		body = nil
		json.NewDecoder(req.Body).Decode(&body)
		return mockResponse(`{"choices":[{"message":{"content":"partial line"},"finish_reason":"length"}],"usage":{"prompt_tokens":10,"completion_tokens":400}}`), nil
	})
	// The reference system's recall completer returns a reply its limit cut
	// short (natural.ts llmComplete), so the per-call limit does not fail a plan.
	text, usage, err := p.CompleteWith(context.Background(), "plan", "data", 400)
	if err != nil || text != "partial line" || usage.OutputTokens != 400 || body["max_tokens"] != float64(400) || body["response_format"] != nil || body["temperature"] != float64(0) {
		t.Fatal(text, usage, body, err)
	}
	if _, _, err = p.Complete(context.Background(), "plan", "data"); err == nil || body["max_tokens"] != float64(777) {
		t.Fatal("Complete lost its configured limit or accepted a cut reply", body, err)
	}
	if _, _, err = p.CompleteWith(context.Background(), "plan", "data", 0); err == nil {
		t.Fatal("nonpositive call limit accepted")
	}

	budget, err := OpenBudget(filepath.Join(t.TempDir(), "budget.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer budget.Close()
	t.Setenv("UNIT_DEEPSEEK_CREDENTIAL", "test-only-placeholder")
	deepseek, err := NewProvider(ModelConfig{Kind: "deepseek", Model: "deepseek-flash", KeyEnv: "UNIT_DEEPSEEK_CREDENTIAL"}, budget)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	deepseek.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, fmt.Errorf("unexpected request")
	})
	if _, _, err = deepseek.CompleteWith(context.Background(), "plan", "data", maxDeepSeekTokens+1); err == nil || calls.Load() != 0 {
		t.Fatal("DeepSeek call limit beyond its reservation reached the network", err, calls.Load())
	}
}

func TestMaxTokensAppliesAndIsBounded(t *testing.T) {
	p, err := NewProvider(ModelConfig{Kind: "local", Model: "local-test", BaseURL: "http://127.0.0.1:1/v1", MaxTokens: 777}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	p.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		json.NewDecoder(req.Body).Decode(&body)
		return mockResponse(`{"choices":[{"message":{"content":"{}"}}]}`), nil
	})
	if _, _, err = p.CompleteJSON(context.Background(), "JSON", "data"); err != nil || body["max_tokens"] != float64(777) {
		t.Fatal(body, err)
	}
	if _, err = NewProvider(ModelConfig{Kind: "local", Model: "local-test", BaseURL: "http://127.0.0.1:1/v1", MaxTokens: -1}, nil); err == nil {
		t.Fatal("negative max tokens accepted")
	}
	budget, err := OpenBudget(filepath.Join(t.TempDir(), "budget.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer budget.Close()
	t.Setenv("UNIT_DEEPSEEK_CREDENTIAL", "test-only-placeholder")
	for tokens, ok := range map[int]bool{16384: true, 16385: false} {
		_, err = NewProvider(ModelConfig{Kind: "deepseek", Model: "deepseek-flash", KeyEnv: "UNIT_DEEPSEEK_CREDENTIAL", MaxTokens: tokens}, budget)
		if (err == nil) != ok {
			t.Fatal("DeepSeek maxTokens bound is wrong", tokens, err)
		}
	}
}

func cjkCandidates(count, runes int) []map[string]string {
	text := strings.Repeat("记", runes)
	out := []map[string]string{}
	for i := range count {
		out = append(out, map[string]string{"id": fmt.Sprintf("c%d", i), "text": text})
	}
	return out
}

func binaryQuestions(count int) map[string]Question {
	questions := map[string]Question{}
	for i := range count {
		questions[fmt.Sprintf("q%d", i)] = binary(fmt.Sprintf("Is candidate c%d relevant?", i))
	}
	return questions
}

// jevServer is an httptest JEV endpoint that answers every question 0.5 and
// keeps the request bodies it received.
type jevServer struct {
	mu     sync.Mutex
	bodies []string
}

func (s *jevServer) received() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.bodies...)
}

// jevTestProvider is a metered JEV provider whose requests go to a jevServer.
func jevTestProvider(t *testing.T) (*Provider, *jevServer) {
	t.Helper()
	budget, err := OpenBudget(filepath.Join(t.TempDir(), "budget.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { budget.Close() })
	t.Setenv("UNIT_JEV_CREDENTIAL", "test-only-placeholder")
	p, err := NewProvider(ModelConfig{Kind: "jev", Model: "jev-1.13.0", KeyEnv: "UNIT_JEV_CREDENTIAL"}, budget)
	if err != nil {
		t.Fatal(err)
	}
	server := &jevServer{}
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		server.mu.Lock()
		server.bodies = append(server.bodies, string(raw))
		server.mu.Unlock()
		var request struct{ Questions map[string]json.RawMessage }
		json.Unmarshal(raw, &request)
		answers := []string{}
		for key := range request.Questions {
			answers = append(answers, fmt.Sprintf(`%q:{"type":"noul","noul":0.5}`, key))
		}
		io.WriteString(w, `{"model":"jev-1.13.0","answers":{`+strings.Join(answers, ",")+`},"usage":{"input_tokens":1000,"output_tokens":1}}`)
	}))
	t.Cleanup(endpoint.Close)
	p.endpoint = endpoint.URL
	return p, server
}

func TestJEVRequestLimitIsUTF16Units(t *testing.T) {
	p, server := jevTestProvider(t)
	// The simple strategy's full judgement batch: six messages and 40
	// candidates of 600 CJK units, with the needed and superseded questions
	// and their criteria. It is over 96,000 bytes but under 96,000 units.
	cjk := strings.Repeat("记", 600)
	dialogue, candidates := []Message{}, []judgeCandidate{}
	for i := range 6 {
		dialogue = append(dialogue, Message{Role: []string{"user", "assistant"}[i%2], Text: cjk})
	}
	for range 40 {
		candidates = append(candidates, judgeCandidate{Source: judgeSource, Text: cjk})
	}
	state, questions := batchRequest(dialogue, candidates)
	answers, _, err := p.Decide(context.Background(), state, questions)
	if err != nil || len(answers) != 80 || len(server.received()) != 1 {
		t.Fatal("the judgement batch was refused", len(answers), err)
	}
	if body := server.received()[0]; len(body) <= 96000 || jsLen(body) > 96000 {
		t.Fatal("unexpected batch size", len(body), jsLen(body))
	}

	// The limit is the author's JSON.stringify({state, questions}).length:
	// UTF-16 units of the request without the model, where <, >, & and the
	// line separators U+2028 and U+2029 stay unescaped (1 unit each). A state
	// that brings it to exactly 96,000 units (an emoji counts 2) reaches the
	// server as those bytes, the model first; one unit more is refused before
	// the network.
	one := map[string]Question{"q0": binary("Is it current?")}
	head, tail := `{"state":"`, `","questions":{"q0":{"type":"noul","instructions":"Is it current?"}}}`
	text := "<&>  "
	room := 96000 - jsLen(head+text+tail)
	text += strings.Repeat("😀", room/2) + strings.Repeat("记", room%2)
	if _, _, err = p.Decide(context.Background(), text, one); err != nil || len(server.received()) != 2 {
		t.Fatal("a request of exactly 96000 units was refused", err)
	}
	if body := server.received()[1]; body != `{"model":"jev-1.13.0",`+(head + text + tail)[1:] {
		t.Fatalf("JEV body is not the author's JSON: %.160q", body)
	}
	if _, _, err = p.Decide(context.Background(), text+"记", one); err == nil || !strings.Contains(err.Error(), "96000") || len(server.received()) != 2 {
		t.Fatal("a request of 96001 units was accepted", err)
	}

	// The question count stays 1–128.
	if answers, _, err = p.Decide(context.Background(), "evidence", binaryQuestions(128)); err != nil || len(answers) != 128 {
		t.Fatal("128 JEV questions were refused", len(answers), err)
	}
	before := len(server.received())
	if _, _, err = p.Decide(context.Background(), "evidence", binaryQuestions(129)); err == nil {
		t.Fatal("129 JEV questions were accepted")
	}
	if _, _, err = p.Decide(context.Background(), "evidence", map[string]Question{}); err == nil {
		t.Fatal("zero JEV questions were accepted")
	}
	if len(server.received()) != before {
		t.Fatal("a refused JEV request reached the network")
	}
}

func TestNonJEVKeepsSmallerLimits(t *testing.T) {
	p, err := NewProvider(ModelConfig{Kind: "local", Model: "local-test", BaseURL: "http://127.0.0.1:1/v1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	p.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return mockResponse(`{"choices":[{"message":{"content":"{}"}}]}`), nil
	})
	if _, _, err = p.Decide(context.Background(), "evidence", binaryQuestions(81)); err == nil {
		t.Fatal("81 non-JEV questions were accepted")
	}
	if _, _, err = p.Decide(context.Background(), cjkCandidates(80, 700), binaryQuestions(80)); err == nil || !strings.Contains(err.Error(), "160000") {
		t.Fatal("non-JEV body above 160000 bytes was accepted", err)
	}
	if calls.Load() != 0 {
		t.Fatal("rejected request reached the network")
	}
}

func TestRequestTooLargeIsTyped(t *testing.T) {
	p, err := NewProvider(ModelConfig{Kind: "local", Model: "local-test", BaseURL: "http://127.0.0.1:1/v1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	p.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return mockResponse(`{"choices":[{"message":{"content":"{}"}}]}`), nil
	})
	big := strings.Repeat("x", maxRequestBytes)
	if _, _, err = p.CompleteWith(context.Background(), "system", big, 8192); !errors.Is(err, ErrRequestTooLarge) || !strings.Contains(err.Error(), "160000") {
		t.Fatal("oversize completion error is not ErrRequestTooLarge", err)
	}
	if _, _, err = p.Complete(context.Background(), "system", big); !errors.Is(err, ErrRequestTooLarge) {
		t.Fatal("oversize completion error is not ErrRequestTooLarge", err)
	}
	if _, _, err = p.Decide(context.Background(), cjkCandidates(80, 700), binaryQuestions(80)); !errors.Is(err, ErrRequestTooLarge) {
		t.Fatal("oversize non-JEV decision error is not ErrRequestTooLarge", err)
	}
	if _, _, err = p.CompleteWith(context.Background(), "system", "small", 8192); err != nil || errors.Is(err, ErrRequestTooLarge) || calls.Load() != 1 {
		t.Fatal("a small completion failed or oversize requests reached the network", err, calls.Load())
	}
	jev, server := jevTestProvider(t)
	one := map[string]Question{"q0": binary("Is it current?")}
	if _, _, err = jev.Decide(context.Background(), strings.Repeat("记", maxJEVRequest), one); !errors.Is(err, ErrRequestTooLarge) || !strings.Contains(err.Error(), "96000") || len(server.received()) != 0 {
		t.Fatal("oversize JEV error is not ErrRequestTooLarge", err)
	}
}

func TestExistingConfigJSONIsUnchanged(t *testing.T) {
	// View.policy hashes this JSON, so new optional fields must vanish when unset.
	raw, err := json.Marshal(Config{Version: 1, Planner: &ModelConfig{Kind: "local", Model: "m", BaseURL: "http://127.0.0.1:1/v1"}, Judge: &ModelConfig{Kind: "jev", Model: "jev-1.13.0", KeyEnv: "K"}, BudgetFile: "/b.db"})
	want := `{"version":1,"planner":{"kind":"local","model":"m","baseURL":"http://127.0.0.1:1/v1"},"judge":{"kind":"jev","model":"jev-1.13.0","keyEnv":"K"},"budgetFile":"/b.db"}`
	if err != nil || string(raw) != want {
		t.Fatal(string(raw), err)
	}
}

// attempt is one budget attempt row: what it reserved and what it settled
// at (nil while held).
type attempt struct {
	reserved float64
	actual   *float64
}

func budgetAttempts(t *testing.T, b *Budget) []attempt {
	t.Helper()
	rows, err := b.db.Query(`SELECT reserved,actual FROM attempts ORDER BY rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := []attempt{}
	for rows.Next() {
		var a attempt
		if err := rows.Scan(&a.reserved, &a.actual); err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	return out
}

func accountHeld(t *testing.T, b *Budget, kind string) (held, spent float64) {
	t.Helper()
	b.db.QueryRow("SELECT held,spent FROM accounts WHERE kind=?", kind).Scan(&held, &spent)
	return held, spent
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestCompletionBodyIsNotHTMLEscaped(t *testing.T) {
	p, err := NewProvider(ModelConfig{Kind: "local", Model: "local-test", BaseURL: "http://127.0.0.1:1/v1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	p.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		raw, _ = io.ReadAll(req.Body)
		return mockResponse(`{"choices":[{"message":{"content":"ok"}}]}`), nil
	})
	if _, _, err = p.CompleteWith(context.Background(), "a <b> & c", "x > y && <tag>", 100); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"a <b> & c"`) || !strings.Contains(string(raw), `"x > y && <tag>"`) || strings.Contains(string(raw), `\`) || strings.HasSuffix(string(raw), "\n") {
		t.Fatalf("%q", raw)
	}
	var decoded struct {
		Messages []struct{ Content string }
	}
	if json.Unmarshal(raw, &decoded) != nil || len(decoded.Messages) != 2 || decoded.Messages[1].Content != "x > y && <tag>" {
		t.Fatalf("%q", raw)
	}
}

func TestCompletionCapIs160000Bytes(t *testing.T) {
	p, err := NewProvider(ModelConfig{Kind: "local", Model: "local-test", BaseURL: "http://127.0.0.1:1/v1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var sizes []int
	p.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(req.Body)
		sizes = append(sizes, len(raw))
		return mockResponse(`{"choices":[{"message":{"content":"ok"}}]}`), nil
	})
	// A body past the old 48,000 bytes now goes out.
	if _, _, err = p.CompleteWith(context.Background(), "system", strings.Repeat("<", 100000), 8192); err != nil || len(sizes) != 1 || sizes[0] < 100000 || sizes[0] > 100300 {
		t.Fatal(sizes, err)
	}
	if _, _, err = p.CompleteWith(context.Background(), "system", strings.Repeat("x", 160000), 8192); !errors.Is(err, ErrRequestTooLarge) || !strings.Contains(err.Error(), "160000") || len(sizes) != 1 {
		t.Fatal(sizes, err)
	}
}

func TestCompletionReservationScalesWithBody(t *testing.T) {
	budget, err := OpenBudgetWithCaps(filepath.Join(t.TempDir(), "budget.db"), map[string]float64{"deepseek": 100, "openai": 100})
	if err != nil {
		t.Fatal(err)
	}
	defer budget.Close()
	t.Setenv("UNIT_DEEPSEEK_CREDENTIAL", "test-only-placeholder")
	t.Setenv("UNIT_OPENAI_CREDENTIAL", "test-only-placeholder")
	deepseek, err := NewProvider(ModelConfig{Kind: "deepseek", Model: "deepseek-flash", KeyEnv: "UNIT_DEEPSEEK_CREDENTIAL"}, budget)
	if err != nil {
		t.Fatal(err)
	}
	openai, err := NewProvider(ModelConfig{Kind: "openai", Model: "gpt-4.1-mini", BaseURL: "https://openai.example/v1", KeyEnv: "UNIT_OPENAI_CREDENTIAL"}, budget)
	if err != nil {
		t.Fatal(err)
	}
	var size int
	reply := `{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":10}}`
	for _, p := range []*Provider{deepseek, openai} {
		p.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
			raw, _ := io.ReadAll(req.Body)
			size = len(raw)
			return mockResponse(reply), nil
		})
	}
	ctx := context.Background()
	prompt := strings.Repeat("x", 47500) // a body just under 48,000 bytes
	// Bodies of at most 48,000 bytes with the default limits reserve the
	// flat amount, as before.
	for _, call := range []func() error{
		func() error { _, _, err := deepseek.Complete(ctx, "s", prompt); return err },
		func() error { _, _, err := deepseek.CompleteJSON(ctx, "s", prompt); return err },
		func() error { _, _, err := deepseek.CompleteWith(ctx, "s", prompt, timelineMaxTokens); return err },
		func() error { _, _, err := openai.Complete(ctx, "s", prompt); return err },
		func() error { _, _, err := openai.CompleteJSON(ctx, "s", prompt); return err },
	} {
		if err := call(); err != nil || size > 48000 {
			t.Fatal(size, err)
		}
	}
	attempts := budgetAttempts(t, budget)
	if len(attempts) != 5 {
		t.Fatal(attempts)
	}
	for i, want := range []float64{0.25, 0.25, 0.25, 0.01, 0.01} {
		if !near(attempts[i].reserved, want) {
			t.Fatal(i, attempts[i].reserved, want)
		}
	}
	// A larger body reserves what its input and output limit could cost,
	// with a 20% margin.
	big := strings.Repeat("x", 150000)
	if _, _, err = deepseek.CompleteWith(ctx, "s", big, timelineMaxTokens); err != nil {
		t.Fatal(err)
	}
	if want := (float64(size)*2e-6 + timelineMaxTokens*8e-6) * 1.2; !near(budgetAttempts(t, budget)[5].reserved, want) || want <= 0.25 {
		t.Fatal(budgetAttempts(t, budget)[5].reserved, want)
	}
	if _, _, err = openai.CompleteWith(ctx, "s", big, timelineMaxTokens); err != nil {
		t.Fatal(err)
	}
	if want := (float64(size)/4*0.40/1e6 + timelineMaxTokens*1.60/1e6) * 1.2; !near(budgetAttempts(t, budget)[6].reserved, want) || want <= 0.01 {
		t.Fatal(budgetAttempts(t, budget)[6].reserved, want)
	}
	// A large call may be metered above the flat amount, within its own
	// reservation.
	reply = `{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":150000,"completion_tokens":8192}}`
	if _, usage, err := deepseek.CompleteWith(ctx, "s", big, timelineMaxTokens); err != nil || usage.CNY <= 0.25 {
		t.Fatal(usage, err)
	}
	if held, _ := accountHeld(t, budget, "deepseek"); !near(held, 0) {
		t.Fatal("held", held)
	}
}

func TestFailedModelRequestReleasesOnlyUnsentOrRefused(t *testing.T) {
	budget, err := OpenBudget(filepath.Join(t.TempDir(), "budget.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer budget.Close()
	t.Setenv("UNIT_OPENAI_CREDENTIAL", "test-only-placeholder")
	status := http.StatusInternalServerError
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":10}}`)
	}))
	defer server.Close()
	p, err := NewProvider(ModelConfig{Kind: "openai", Model: "gpt-4.1-mini", BaseURL: server.URL + "/v1", KeyEnv: "UNIT_OPENAI_CREDENTIAL"}, budget)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// A non-2xx answer was not a completed call: released, recorded at 0.
	for _, code := range []int{http.StatusInternalServerError, http.StatusTooManyRequests, http.StatusBadRequest} {
		status = code
		if _, _, err = p.Complete(ctx, "s", "p"); err == nil || !strings.Contains(err.Error(), fmt.Sprint(code)) {
			t.Fatal(code, err)
		}
		// Outages and rate limits hit every request alike; a 400 is about this one.
		if errors.Is(err, ErrModelUnavailable) != (code != http.StatusBadRequest) {
			t.Fatal("unavailable classification", code, err)
		}
	}
	if held, spent := accountHeld(t, budget, "openai"); !near(held, 0) || spent != 0 {
		t.Fatal("a refused call kept its reservation", held, spent)
	}
	for _, a := range budgetAttempts(t, budget) {
		if a.actual == nil || *a.actual != 0 {
			t.Fatal("a released attempt is not recorded at 0", a)
		}
	}
	// A request that never connected is released too.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := listener.Addr().String()
	listener.Close()
	unreachable, err := NewProvider(ModelConfig{Kind: "openai", Model: "gpt-4.1-mini", BaseURL: "http://" + closed + "/v1", KeyEnv: "UNIT_OPENAI_CREDENTIAL"}, budget)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = unreachable.Complete(ctx, "s", "p"); err == nil || !errors.Is(err, ErrModelUnavailable) {
		t.Fatal("a refused connection is not an unavailable model", err)
	}
	if held, _ := accountHeld(t, budget, "openai"); !near(held, 0) {
		t.Fatal("an unsent call kept its reservation", held)
	}
	// A failure after the request may have been sent, or a malformed 2xx
	// reply, keeps the reservation held.
	p.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		return nil, &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}
	})
	if _, _, err = p.Complete(ctx, "s", "p"); err == nil {
		t.Fatal("a reset succeeded")
	}
	p.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) { return mockResponse(`{"choices":[]}`), nil })
	if _, _, err = p.Complete(ctx, "s", "p"); err == nil {
		t.Fatal("a malformed reply succeeded")
	}
	if held, _ := accountHeld(t, budget, "openai"); !near(held, 0.02) {
		t.Fatal("an ambiguous call released its reservation", held)
	}
	// A dial failure reported by the transport is recognized as unsent.
	p.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: "openai.example", IsNotFound: true}}
	})
	if _, _, err = p.Complete(ctx, "s", "p"); err == nil {
		t.Fatal("a failed dial succeeded")
	}
	if held, _ := accountHeld(t, budget, "openai"); !near(held, 0.02) {
		t.Fatal("a failed dial kept its reservation", held)
	}
	// A canceled caller's error says so.
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	p.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() })
	if _, _, err = p.Complete(canceled, "s", "p"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestBudgetCapErrorIsTyped(t *testing.T) {
	b, err := OpenBudgetWithCaps(filepath.Join(t.TempDir(), "budget.db"), map[string]float64{"deepseek": 0.3})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if _, err = b.reserve(context.Background(), "deepseek", "h"); err != nil {
		t.Fatal(err)
	}
	if _, err = b.reserve(context.Background(), "deepseek", "h"); !errors.Is(err, ErrBudgetCap) || !strings.Contains(err.Error(), "model spending cap reached") {
		t.Fatal(err)
	}
}

func TestModelTimeoutSeconds(t *testing.T) {
	base := ModelConfig{Kind: "local", Model: "local-test", BaseURL: "http://127.0.0.1:1/v1"}
	for seconds, want := range map[int]time.Duration{0: 90 * time.Second, 1: time.Second, 600: 10 * time.Minute, 1800: 30 * time.Minute} {
		config := base
		config.TimeoutSeconds = seconds
		p, err := NewProvider(config, nil)
		if err != nil || p.client.Timeout != want {
			t.Fatal(seconds, err)
		}
	}
	for _, seconds := range []int{-1, 1801} {
		config := base
		config.TimeoutSeconds = seconds
		if _, err := NewProvider(config, nil); err == nil {
			t.Fatal("timeoutSeconds accepted", seconds)
		}
	}
	raw, _ := json.Marshal(ModelConfig{Kind: "deepseek", Model: "deepseek-flash", TimeoutSeconds: 600})
	if string(raw) != `{"kind":"deepseek","model":"deepseek-flash","timeoutSeconds":600}` {
		t.Fatal(string(raw))
	}
}

func TestBudgetAccountsReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "budget.db")
	b, err := OpenBudgetWithCaps(path, map[string]float64{"deepseek": 200})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ctx := context.Background()
	if accounts, err := b.Accounts(ctx); err != nil || len(accounts) != 0 {
		t.Fatal(accounts, err)
	}
	id, err := b.reserve(ctx, "deepseek", "h")
	if err != nil {
		t.Fatal(err)
	}
	if err = b.settle(ctx, id, Usage{InputTokens: 1, CNY: 0.1}); err != nil {
		t.Fatal(err)
	}
	if _, err = b.reserve(ctx, "deepseek", "h"); err != nil {
		t.Fatal(err)
	}
	if _, err = b.reserve(ctx, "jev", "h"); err != nil {
		t.Fatal(err)
	}
	accounts, err := b.Accounts(ctx)
	if err != nil || len(accounts) != 2 || accounts[0].Kind != "deepseek" || accounts[1].Kind != "jev" {
		t.Fatal(accounts, err)
	}
	if a := accounts[0]; a.Cap != 200 || !near(a.Spent, 0.1) || !near(a.Held, 0.25) {
		t.Fatal(a)
	}
	if a := accounts[1]; a.Cap != 10 || a.Spent != 0 || !near(a.Held, 0.01) {
		t.Fatal(a)
	}
	// Another process's cap is not this one's: the configured cap reports.
	other, err := OpenBudgetWithCaps(path, map[string]float64{"deepseek": 300})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if accounts, err = other.Accounts(ctx); err != nil || accounts[0].Cap != 300 {
		t.Fatal(accounts, err)
	}
}
