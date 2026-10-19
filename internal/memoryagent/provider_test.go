package memoryagent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
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

func TestJEVBodyLimitCJK(t *testing.T) {
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
	var calls atomic.Int32
	var size int
	p.client.Transport = transportFunc(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		raw, _ := io.ReadAll(req.Body)
		size = len(raw)
		var request struct{ Questions map[string]json.RawMessage }
		json.Unmarshal(raw, &request)
		answers := []string{}
		for key := range request.Questions {
			answers = append(answers, fmt.Sprintf(`%q:{"type":"noul","noul":0.5}`, key))
		}
		return mockResponse(`{"model":"jev-1.13.0","answers":{` + strings.Join(answers, ",") + `},"usage":{"input_tokens":1000,"output_tokens":1}}`), nil
	})
	answers, _, err := p.Decide(context.Background(), cjkCandidates(40, 600), binaryQuestions(80))
	if err != nil || len(answers) != 80 || size <= 48000 || size > 96000 {
		t.Fatal("JEV batch of 40 CJK candidates and 80 questions was refused", size, len(answers), err)
	}
	if answers, _, err = p.Decide(context.Background(), "evidence", binaryQuestions(128)); err != nil || len(answers) != 128 {
		t.Fatal("128 JEV questions were refused", len(answers), err)
	}
	before := calls.Load()
	if _, _, err = p.Decide(context.Background(), "evidence", binaryQuestions(129)); err == nil {
		t.Fatal("129 JEV questions were accepted")
	}
	if _, _, err = p.Decide(context.Background(), "evidence", map[string]Question{}); err == nil {
		t.Fatal("zero JEV questions were accepted")
	}
	if _, _, err = p.Decide(context.Background(), cjkCandidates(40, 800), binaryQuestions(80)); err == nil || !strings.Contains(err.Error(), "96000") {
		t.Fatal("JEV body above 96000 bytes was accepted", err)
	}
	if calls.Load() != before {
		t.Fatal("rejected JEV request reached the network or budget")
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
	if _, _, err = p.Decide(context.Background(), cjkCandidates(40, 600), binaryQuestions(80)); err == nil || !strings.Contains(err.Error(), "48000") {
		t.Fatal("non-JEV body above 48000 bytes was accepted", err)
	}
	if calls.Load() != 0 {
		t.Fatal("rejected request reached the network")
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
