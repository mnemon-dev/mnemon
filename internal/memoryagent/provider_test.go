package memoryagent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
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
	a, err := OpenBudget(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.db.Exec("INSERT INTO accounts(kind,cap) VALUES('jev',0.02)")
	b, err := OpenBudget(path)
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
