// Package judge is a small client for the System One decision-model API
// (POST {endpoint}/v1/systemone). It turns yes/no and pick-one questions about
// a block of state into typed probabilities, which recall and remember use to
// drop irrelevant results and to classify semantic candidates.
//
// The judge is opt-in: NewClient returns nil unless MNEMON_JUDGE_MODEL is set,
// and every caller treats a failed judgment as advisory, never fatal.
package judge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	// DefaultEndpoint is the default local Ollama endpoint.
	DefaultEndpoint = "http://localhost:11434"
	// DefaultTimeout bounds one whole judgment, across all of its chunks.
	DefaultTimeout = 15 * time.Second
	// MaxQuestionsPerRequest is the largest question count every supported
	// provider accepts in one request (SubQ caps at 16; Ollama allows 64).
	MaxQuestionsPerRequest = 16

	systemOneRoute   = "v1/systemone"
	maxResponseBytes = 1 << 20
	maxErrorBytes    = 4 << 10
)

// Client talks to a System One server (local Ollama or a cloud provider).
type Client struct {
	endpoint string
	model    string
	apiKey   string
	timeout  time.Duration
	http     *http.Client
}

// NewClient builds a judge client from MNEMON_JUDGE_MODEL,
// MNEMON_JUDGE_ENDPOINT, MNEMON_JUDGE_API_KEY, and MNEMON_JUDGE_TIMEOUT.
// It returns nil when MNEMON_JUDGE_MODEL is empty, which means the judge is
// not configured.
func NewClient() *Client {
	model := strings.TrimSpace(os.Getenv("MNEMON_JUDGE_MODEL"))
	if model == "" {
		return nil
	}
	endpoint := os.Getenv("MNEMON_JUDGE_ENDPOINT")
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	timeout := DefaultTimeout
	if v := os.Getenv("MNEMON_JUDGE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			timeout = d
		} else {
			fmt.Fprintf(os.Stderr, "warning: invalid MNEMON_JUDGE_TIMEOUT %q, using %s\n", v, DefaultTimeout)
		}
	}
	return &Client{
		endpoint: endpoint,
		model:    model,
		apiKey:   os.Getenv("MNEMON_JUDGE_API_KEY"),
		timeout:  timeout,
		http: &http.Client{
			Transport: &http.Transport{
				// Remote endpoints follow the standard proxy environment;
				// loopback servers never do, so a stray HTTPS_PROXY cannot
				// capture local model traffic.
				Proxy: proxyFunc(endpoint),
				DialContext: (&net.Dialer{
					Timeout:   5 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
			},
		},
	}
}

// proxyFunc returns the proxy resolver for the judge endpoint: none for
// loopback, the standard environment resolution otherwise.
func proxyFunc(endpoint string) func(*http.Request) (*url.URL, error) {
	if u, err := url.Parse(endpoint); err == nil {
		if host := u.Hostname(); host == "localhost" || net.ParseIP(host).IsLoopback() {
			return func(*http.Request) (*url.URL, error) { return nil, nil }
		}
	}
	return http.ProxyFromEnvironment
}

// Model returns the configured model name.
func (c *Client) Model() string { return c.model }

// Question is a yes/no (noul) question. Name keys the answer.
type Question struct {
	Name         string
	Instructions string
}

// ChoiceQuestion is a pick-one question. Options maps each option to a
// description the model sees.
type ChoiceQuestion struct {
	Name         string
	Instructions string
	Options      map[string]string
}

// ChoiceAnswer is the validated answer to a ChoiceQuestion.
type ChoiceAnswer struct {
	// Choice is one of the question's options.
	Choice string
	// Probability is the model's probability for Choice.
	Probability float64
}

// Noul asks yes/no questions about state and returns P(yes) per question name.
// More than MaxQuestionsPerRequest questions are sent as several requests that
// each carry the same state.
func (c *Client) Noul(ctx context.Context, state any, questions []Question) (map[string]float64, error) {
	wire := make([]wireQuestion, len(questions))
	for i, q := range questions {
		wire[i] = wireQuestion{name: q.Name, Type: "noul", Instructions: q.Instructions}
	}
	answers, err := c.ask(ctx, state, wire)
	if err != nil {
		return nil, err
	}
	out := make(map[string]float64, len(questions))
	for _, q := range questions {
		a := answers[q.Name]
		if err := a.expect("noul"); err != nil {
			return nil, fmt.Errorf("question %q: %w", q.Name, err)
		}
		if a.Noul == nil || !inUnit(*a.Noul) {
			return nil, fmt.Errorf("question %q: missing or out-of-range noul value", q.Name)
		}
		out[q.Name] = *a.Noul
	}
	return out, nil
}

// Choice asks pick-one questions about state and returns the validated answer
// per question name. Chunking follows the same rule as Noul.
func (c *Client) Choice(ctx context.Context, state any, questions []ChoiceQuestion) (map[string]ChoiceAnswer, error) {
	wire := make([]wireQuestion, len(questions))
	for i, q := range questions {
		wire[i] = wireQuestion{name: q.Name, Type: "choice", Instructions: q.Instructions, Criteria: q.Options}
	}
	answers, err := c.ask(ctx, state, wire)
	if err != nil {
		return nil, err
	}
	out := make(map[string]ChoiceAnswer, len(questions))
	for _, q := range questions {
		a := answers[q.Name]
		ans, err := a.choice(q.Options)
		if err != nil {
			return nil, fmt.Errorf("question %q: %w", q.Name, err)
		}
		out[q.Name] = ans
	}
	return out, nil
}

// ask sends questions in chunks under one deadline and returns the answers
// keyed by question name. Every question must be answered.
func (c *Client) ask(ctx context.Context, state any, questions []wireQuestion) (map[string]wireAnswer, error) {
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("marshal judge state: %w", err)
	}
	seen := make(map[string]bool, len(questions))
	for _, q := range questions {
		if q.name == "" || seen[q.name] {
			return nil, fmt.Errorf("judge question name %q is empty or duplicated", q.name)
		}
		seen[q.name] = true
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	answers := make(map[string]wireAnswer, len(questions))
	for start := 0; start < len(questions); start += MaxQuestionsPerRequest {
		end := min(start+MaxQuestionsPerRequest, len(questions))
		got, err := c.post(ctx, stateJSON, questions[start:end])
		if err != nil {
			return nil, err
		}
		for _, q := range questions[start:end] {
			a, ok := got[q.name]
			if !ok {
				return nil, fmt.Errorf("judge response is missing an answer for question %q", q.name)
			}
			answers[q.name] = a
		}
	}
	return answers, nil
}

// post sends one System One request and decodes its answers.
func (c *Client) post(ctx context.Context, state json.RawMessage, chunk []wireQuestion) (map[string]wireAnswer, error) {
	questions := make(map[string]wireQuestion, len(chunk))
	for _, q := range chunk {
		questions[q.name] = q
	}
	body, err := json.Marshal(wireRequest{Model: c.model, State: state, Questions: questions})
	if err != nil {
		return nil, fmt.Errorf("marshal judge request: %w", err)
	}
	endpointURL, err := url.JoinPath(c.endpoint, systemOneRoute)
	if err != nil {
		return nil, fmt.Errorf("join judge endpoint: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build judge request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("judge request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read judge response: %w", err)
	}
	if len(data) > maxResponseBytes {
		return nil, fmt.Errorf("judge response exceeds %d bytes", maxResponseBytes)
	}
	var decoded wireResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, fmt.Errorf("decode judge response: %w", err)
	}
	return decoded.Answers, nil
}

// statusError turns a non-200 response into an error, preferring the server's
// {"error": "..."} message.
func statusError(resp *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes))
	var payload struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &payload) == nil && payload.Error != "" {
		return fmt.Errorf("judge server returned status %d: %s", resp.StatusCode, payload.Error)
	}
	return fmt.Errorf("judge server returned status %d", resp.StatusCode)
}
