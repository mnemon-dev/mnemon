package embed

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProtocolAutoDetect(t *testing.T) {
	t.Setenv("MNEMON_EMBED_ENDPOINT", "http://127.0.0.1:18000/v1")
	c := NewClient()
	if c.Protocol() != ProtocolOpenAI {
		t.Fatalf("expected openai protocol for /v1 endpoint, got %q", c.Protocol())
	}

	t.Setenv("MNEMON_EMBED_ENDPOINT", "http://localhost:11434")
	c = NewClient()
	if c.Protocol() != ProtocolOllama {
		t.Fatalf("expected ollama protocol for default endpoint, got %q", c.Protocol())
	}

	// Explicit protocol override wins over auto-detection.
	t.Setenv("MNEMON_EMBED_ENDPOINT", "http://127.0.0.1:18000/v1")
	t.Setenv("MNEMON_EMBED_PROTOCOL", "ollama")
	c = NewClient()
	if c.Protocol() != ProtocolOllama {
		t.Fatalf("expected explicit protocol override to win, got %q", c.Protocol())
	}

	t.Setenv("MNEMON_EMBED_PROTOCOL", "openai")
	t.Setenv("MNEMON_EMBED_ENDPOINT", "http://localhost:11434")
	c = NewClient()
	if c.Protocol() != ProtocolOpenAI {
		t.Fatalf("expected explicit openai protocol, got %q", c.Protocol())
	}
}

func TestOpenAIAvailable(t *testing.T) {
	t.Setenv("MNEMON_EMBED_API_KEY", "sk-test")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("expected /v1/models, got %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("expected Bearer sk-test, got %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("MNEMON_EMBED_ENDPOINT", srv.URL+"/v1")
	c := NewClient()
	if !c.Available() {
		t.Fatal("expected Available() true for 200 /v1/models")
	}
}

func TestOpenAIEndpointWithTrailingSlash(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.WriteHeader(http.StatusOK)
		case "/v1/embeddings":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"embedding":[1.0,2.0]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	t.Setenv("MNEMON_EMBED_ENDPOINT", srv.URL+"/v1/")
	c := NewClient()
	if c.Protocol() != ProtocolOpenAI {
		t.Fatalf("expected openai protocol for /v1/ endpoint, got %q", c.Protocol())
	}
	if !c.Available() {
		t.Fatal("expected Available() true for trailing-slash endpoint")
	}
	vec, err := c.Embed("hello")
	if err != nil {
		t.Fatalf("Embed with trailing-slash endpoint: %v", err)
	}
	if len(vec) != 2 {
		t.Fatalf("expected 2 dims, got %d", len(vec))
	}
}

func TestOpenAIEmbed(t *testing.T) {
	t.Setenv("MNEMON_EMBED_MODEL", "BAAI/bge-m3")
	t.Setenv("MNEMON_EMBED_DIMENSIONS", "1024")
	t.Setenv("MNEMON_EMBED_API_KEY", "sk-test")
	var gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("expected /v1/embeddings, got %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"object":"list","data":[{"object":"embedding","embedding":[0.1,0.2,0.3],"index":0}]}`))
	}))
	defer srv.Close()

	t.Setenv("MNEMON_EMBED_ENDPOINT", srv.URL+"/v1")
	c := NewClient()
	vec, err := c.Embed("跨会话记忆测试")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != 3 {
		t.Fatalf("expected 3 dims, got %d", len(vec))
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("expected Bearer sk-test, got %q", gotAuth)
	}
	if gotBody["model"] != "BAAI/bge-m3" {
		t.Errorf("expected model in body, got %v", gotBody["model"])
	}
	if gotBody["dimensions"] != float64(1024) {
		t.Errorf("expected dimensions in body, got %v", gotBody["dimensions"])
	}
	if input, _ := gotBody["input"].(string); input != "跨会话记忆测试" {
		t.Errorf("expected input text, got %v", gotBody["input"])
	}
}

func TestOpenAIEmbedWithoutKey(t *testing.T) {
	// Keyless OpenAI-compatible servers must still work: no Authorization
	// header should be sent when MNEMON_EMBED_API_KEY is unset.
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"embedding":[1.0]}]}`))
	}))
	defer srv.Close()

	t.Setenv("MNEMON_EMBED_ENDPOINT", srv.URL+"/v1")
	c := NewClient()
	vec, err := c.Embed("hello")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != 1 {
		t.Fatalf("expected 1 dim, got %d", len(vec))
	}
	if gotAuth != "" {
		t.Errorf("expected no Authorization header without API key, got %q", gotAuth)
	}
}

func TestOpenAIEmbedEmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	t.Setenv("MNEMON_EMBED_ENDPOINT", srv.URL+"/v1")
	c := NewClient()
	if _, err := c.Embed("hello"); err == nil {
		t.Fatal("expected error for empty embedding response")
	}
}

func TestOpenAIAvailableFallsBackWithoutModelsRoute(t *testing.T) {
	// OpenAI-compatible servers without a models route (e.g. Voyage AI)
	// must still be reported available via an embeddings round-trip.
	t.Setenv("MNEMON_EMBED_API_KEY", "sk-test")
	var embedRequests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			http.NotFound(w, r)
		case "/v1/embeddings":
			if r.Method != http.MethodPost {
				t.Errorf("expected POST /v1/embeddings, got %s", r.Method)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
				t.Errorf("expected Bearer sk-test on fallback probe, got %q", got)
			}
			embedRequests++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"embedding":[1.0,2.0]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	t.Setenv("MNEMON_EMBED_ENDPOINT", srv.URL+"/v1")
	c := NewClient()
	if !c.Available() {
		t.Fatal("expected Available() true when /v1/models is 404 but /v1/embeddings works")
	}
	if embedRequests != 1 {
		t.Fatalf("expected exactly one embedding probe, got %d", embedRequests)
	}
}

func TestOpenAIAvailableFallbackRejectsAuthFailure(t *testing.T) {
	// A 404 models route plus a 401 embeddings route must report
	// unavailable: availability follows the endpoint that matters.
	t.Setenv("MNEMON_EMBED_API_KEY", "sk-bad")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			http.NotFound(w, r)
		case "/v1/embeddings":
			w.WriteHeader(http.StatusUnauthorized)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	t.Setenv("MNEMON_EMBED_ENDPOINT", srv.URL+"/v1")
	c := NewClient()
	if c.Available() {
		t.Fatal("expected Available() false when fallback probe returns 401")
	}
}

func TestOpenAIAvailableNoFallbackOnServerError(t *testing.T) {
	// Only a missing models route (404/405/501) triggers the fallback.
	// A 500 models route must report unavailable without an embeddings call.
	var embedRequests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.WriteHeader(http.StatusInternalServerError)
		case "/v1/embeddings":
			embedRequests++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"embedding":[1.0]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	t.Setenv("MNEMON_EMBED_ENDPOINT", srv.URL+"/v1")
	c := NewClient()
	if c.Available() {
		t.Fatal("expected Available() false for 500 models route")
	}
	if embedRequests != 0 {
		t.Fatalf("expected no embedding probe after 500 models route, got %d", embedRequests)
	}
}
