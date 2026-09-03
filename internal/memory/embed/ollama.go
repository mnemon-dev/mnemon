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

// Available returns true if the embedding server's discovery endpoint
// responds successfully. Uses a 2s timeout to avoid blocking the CLI on
// unresponsive servers.
func (c *Client) Available() bool {
	return c.AvailableContext(context.Background())
}

// AvailableContext is Available with caller cancellation in addition to the
// bounded discovery timeout.
func (c *Client) AvailableContext(ctx context.Context) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var route string
	switch c.protocol {
	case ProtocolOpenAI:
		route = "models"
	default:
		route = "api/tags"
	}
	endpointURL, err := c.endpointURL(route)
	if err != nil {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpointURL, nil)
	if err != nil {
		return false
	}
	c.applyAuth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
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

// Embed generates an embedding vector for the given text.
// The request body is identical for both protocols; only the endpoint
// path and the response shape differ.
func (c *Client) Embed(text string) ([]float64, error) {
	return c.EmbedContext(context.Background(), text)
}

// EmbedContext generates an embedding vector and cancels the provider request
// when its caller is canceled.
func (c *Client) EmbedContext(ctx context.Context, text string) ([]float64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req := embedRequest{Model: c.model, Input: text}
	if c.dims > 0 {
		req.Dimensions = c.dims
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	var route string
	switch c.protocol {
	case ProtocolOpenAI:
		route = "embeddings"
	default:
		route = "api/embed"
	}
	endpointURL, err := c.endpointURL(route)
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
