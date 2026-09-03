package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Protocol identifies the wire protocol used to reach the embedding server.
type Protocol string

const (
	// ProtocolOllama is the Ollama /api/embed protocol (default).
	ProtocolOllama Protocol = "ollama"
	// ProtocolOpenAI is the OpenAI-compatible /v1/embeddings protocol
	// (e.g. oMLX, llama.cpp server, vLLM, LM Studio).
	ProtocolOpenAI Protocol = "openai"
)

// DefaultModel is the default Ollama embedding model.
const DefaultModel = "nomic-embed-text"

// DefaultEndpoint is the default Ollama API endpoint.
const DefaultEndpoint = "http://localhost:11434"

// Client communicates with an embedding server (an Ollama instance or an
// OpenAI-compatible server) for embedding generation.
type Client struct {
	endpoint string
	model    string
	dims     int // 0 means use native dimensions
	apiKey   string
	protocol Protocol
	http     *http.Client
}

// NewClient creates an embedding client.
// It checks MNEMON_EMBED_ENDPOINT, MNEMON_EMBED_MODEL,
// MNEMON_EMBED_DIMENSIONS, MNEMON_EMBED_API_KEY, and
// MNEMON_EMBED_PROTOCOL env vars.
func NewClient() *Client {
	return NewClientWithModel("")
}

// NewClientWithModel creates an embedding client with an explicit model
// override. Resolution order for the model: explicit argument >
// MNEMON_EMBED_MODEL env var > DefaultModel. The endpoint, dimensions,
// API key, and protocol continue to be resolved from environment vars.
//
// Protocol resolution: MNEMON_EMBED_PROTOCOL ("ollama" | "openai") wins
// when set; otherwise the protocol is auto-detected — an endpoint whose
// URL path ends in /v1 is assumed to be an OpenAI-compatible server.
func NewClientWithModel(model string) *Client {
	endpoint := os.Getenv("MNEMON_EMBED_ENDPOINT")
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	if model == "" {
		model = os.Getenv("MNEMON_EMBED_MODEL")
	}
	if model == "" {
		model = DefaultModel
	}
	dims := 0
	if d := os.Getenv("MNEMON_EMBED_DIMENSIONS"); d != "" {
		if v, err := strconv.Atoi(d); err == nil && v > 0 {
			dims = v
		}
	}
	protocol := ProtocolOllama
	explicit := false
	if p := os.Getenv("MNEMON_EMBED_PROTOCOL"); p != "" {
		switch Protocol(strings.ToLower(p)) {
		case ProtocolOllama, ProtocolOpenAI:
			protocol = Protocol(strings.ToLower(p))
			explicit = true
		default:
			fmt.Fprintf(os.Stderr, "warning: invalid MNEMON_EMBED_PROTOCOL %q, falling back to auto-detect\n", p)
		}
	}
	if !explicit {
		// Auto-detect: OpenAI-compatible servers conventionally serve the
		// API under a /v1 path prefix.
		if u, err := url.Parse(endpoint); err == nil {
			trimmed := strings.TrimRight(u.Path, "/")
			if strings.HasSuffix(trimmed, "/v1") {
				protocol = ProtocolOpenAI
			}
		}
	}
	return &Client{
		endpoint: endpoint,
		model:    model,
		dims:     dims,
		apiKey:   os.Getenv("MNEMON_EMBED_API_KEY"),
		protocol: protocol,
		http: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				// Bypass system proxy for localhost connections.
				Proxy: nil,
				DialContext: (&net.Dialer{
					Timeout:   5 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
			},
		},
	}
}

// Protocol returns the active wire protocol.
func (c *Client) Protocol() Protocol {
	return c.protocol
}

// endpointURL resolves a provider route relative to the configured endpoint.
// url.JoinPath keeps both /v1 and /v1/ endpoint forms equivalent while
// preserving any path prefix used by an OpenAI-compatible server.
func (c *Client) endpointURL(route string) (string, error) {
	endpointURL, err := url.JoinPath(c.endpoint, route)
	if err != nil {
		return "", fmt.Errorf("join embedding endpoint: %w", err)
	}
	return endpointURL, nil
}

// Available returns true if the embedding server responds successfully.
// Uses a 2s timeout to avoid blocking the CLI on unresponsive servers.
//
// OpenAI-compatible servers are probed via GET <endpoint>/models, the
// conventional discovery route. Some compatible providers do not serve
// that route at all (e.g. Voyage AI returns 404 while /embeddings works);
// when the models route is missing (404/405/501) the probe falls back to
// a single embedding round-trip, so availability reflects the endpoint
// the client actually depends on.
func (c *Client) Available() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var route string
	switch c.protocol {
	case ProtocolOpenAI:
		route = "models"
	default:
		route = "api/tags"
	}
	status, ok := c.probeStatus(ctx, route)
	if ok {
		return true
	}
	if c.protocol == ProtocolOpenAI && (status == 404 || status == 405 || status == 501) {
		return c.probeEmbed(ctx)
	}
	return false
}

// probeStatus issues a GET against a discovery route and reports the
// HTTP status code. Transport errors yield status 0, ok false.
func (c *Client) probeStatus(ctx context.Context, route string) (status int, ok bool) {
	endpointURL, err := c.endpointURL(route)
	if err != nil {
		return 0, false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpointURL, nil)
	if err != nil {
		return 0, false
	}
	c.applyAuth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	return resp.StatusCode, resp.StatusCode == http.StatusOK
}

// probeEmbed verifies availability with a real embedding round-trip and
// discards the vector. Only reached when the OpenAI models route does not
// exist, so auth or quota failures still report unavailable.
func (c *Client) probeEmbed(ctx context.Context) bool {
	vec, err := c.embedWithContext(ctx, "availability probe")
	if err != nil {
		return false
	}
	return len(vec) > 0
}

// embedWithContext is Embed with a caller-supplied context so the
// availability probe can enforce its 2s deadline.
func (c *Client) embedWithContext(ctx context.Context, text string) ([]float64, error) {
	req := embedRequest{Model: c.model, Input: text}
	if c.dims > 0 {
		req.Dimensions = c.dims
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	endpointURL, err := c.endpointURL(c.embedRequestRoute())
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	c.applyAuth(httpReq)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("embed request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embedding provider returned status %d", resp.StatusCode)
	}

	return c.decodeEmbedResponse(resp)
}

// Model returns the configured model name.
func (c *Client) Model() string {
	return c.model
}

// Endpoint returns the configured embedding endpoint URL.
func (c *Client) Endpoint() string {
	return c.endpoint
}

// applyAuth attaches the Bearer token for OpenAI-compatible servers.
// Ollama requires no authentication.
func (c *Client) applyAuth(req *http.Request) {
	if c.protocol == ProtocolOpenAI && c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
}

type embedRequest struct {
	Model      string `json:"model"`
	Input      string `json:"input"`
	Dimensions int    `json:"dimensions,omitempty"`
}

type ollamaEmbedResponse struct {
	Embeddings [][]float64 `json:"embeddings"`
}

type openaiEmbedResponse struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
}

// embedRequestRoute returns the protocol-specific embeddings route.
func (c *Client) embedRequestRoute() string {
	if c.protocol == ProtocolOpenAI {
		return "embeddings"
	}
	return "api/embed"
}

// Embed generates an embedding vector for the given text.
// The request body is identical for both protocols; only the endpoint
// path and the response shape differ.
func (c *Client) Embed(text string) ([]float64, error) {
	return c.embedWithContext(context.Background(), text)
}

// decodeEmbedResponse parses a successful embeddings response under the
// active protocol. Shared between Embed and the OpenAI availability
// fallback so the probe and the real call cannot drift apart.
func (c *Client) decodeEmbedResponse(resp *http.Response) ([]float64, error) {
	switch c.protocol {
	case ProtocolOpenAI:
		var result openaiEmbedResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
		if len(result.Data) == 0 || len(result.Data[0].Embedding) == 0 {
			return nil, fmt.Errorf("empty embedding returned")
		}
		return result.Data[0].Embedding, nil
	default:
		var result ollamaEmbedResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
		if len(result.Embeddings) == 0 || len(result.Embeddings[0]) == 0 {
			return nil, fmt.Errorf("empty embedding returned")
		}
		return result.Embeddings[0], nil
	}
}
