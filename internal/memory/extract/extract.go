// Package extract is a small client for a System One extraction model such as
// Cloudflare Clairic. It asks the model for the named entities in a memory's
// content and keeps only names that appear in that content verbatim as whole
// tokens, so the model can find entities but never invent or truncate one.
//
// Extraction is opt-in: NewClient returns nil unless MNEMON_EXTRACT_MODEL is
// set, and callers treat a failed extraction as advisory, falling back to
// heuristic extraction.
package extract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// DefaultTimeout bounds one extraction request.
	DefaultTimeout = 15 * time.Second
	// MaxEntities and MaxEntityBytes match the limits remember applies to
	// --entities.
	MaxEntities    = 50
	MaxEntityBytes = 200

	maxResponseBytes = 1 << 20
	maxErrorBytes    = 4 << 10
	maxMessageRunes  = 300

	entitiesField        = "entities"
	entitiesInstructions = "Every named thing this memory is about: people, organizations, projects, " +
		"products, tools, models, services, files, paths and domains. Copy each name exactly as written. " +
		"Skip dates, numbers, version numbers, quantities and ordinary words."
)

// ErrNoEndpoint reports a model configured without an endpoint.
var ErrNoEndpoint = errors.New("MNEMON_EXTRACT_MODEL is set but MNEMON_EXTRACT_ENDPOINT is not")

// Client talks to one extraction endpoint.
type Client struct {
	endpoint string
	model    string
	apiKey   string
	timeout  time.Duration
	http     *http.Client
}

// NewClient builds a client from MNEMON_EXTRACT_MODEL, MNEMON_EXTRACT_ENDPOINT,
// MNEMON_EXTRACT_API_KEY, and MNEMON_EXTRACT_TIMEOUT. It returns nil, nil when
// MNEMON_EXTRACT_MODEL is empty, which means extraction is not configured.
// MNEMON_EXTRACT_ENDPOINT is the full URL requests are POSTed to; there is no
// default because no local server hosts an extraction model yet.
func NewClient() (*Client, error) {
	model := strings.TrimSpace(os.Getenv("MNEMON_EXTRACT_MODEL"))
	if model == "" {
		return nil, nil
	}
	endpoint := strings.TrimSpace(os.Getenv("MNEMON_EXTRACT_ENDPOINT"))
	if endpoint == "" {
		return nil, ErrNoEndpoint
	}
	timeout := DefaultTimeout
	if v := os.Getenv("MNEMON_EXTRACT_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			timeout = d
		} else {
			fmt.Fprintf(os.Stderr, "warning: invalid MNEMON_EXTRACT_TIMEOUT %q, using %s\n", v, DefaultTimeout)
		}
	}
	return &Client{
		endpoint: endpoint,
		model:    model,
		apiKey:   os.Getenv("MNEMON_EXTRACT_API_KEY"),
		timeout:  timeout,
		http: &http.Client{
			Transport: &http.Transport{
				// Loopback servers never use a proxy, so a stray HTTPS_PROXY
				// cannot capture local model traffic.
				Proxy:       proxyFunc(endpoint),
				DialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			},
		},
	}, nil
}

func proxyFunc(endpoint string) func(*http.Request) (*url.URL, error) {
	if u, err := url.Parse(endpoint); err == nil {
		if host := u.Hostname(); host == "localhost" || net.ParseIP(host).IsLoopback() {
			return func(*http.Request) (*url.URL, error) { return nil, nil }
		}
	}
	return http.ProxyFromEnvironment
}

// Entities asks the model for the entities in content. On success the result
// is non-nil, possibly empty, deduplicated, at most MaxEntities long, and holds
// only names found in content as whole tokens.
func (c *Client) Entities(ctx context.Context, content string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	items, err := c.post(ctx, content)
	if err != nil {
		return nil, err
	}
	return groundedEntities(content, items), nil
}

type wireField struct {
	Type         string `json:"type"`
	Items        string `json:"items"`
	Instructions string `json:"instructions"`
}

type wireRequest struct {
	Model  string               `json:"model"`
	State  string               `json:"state"`
	Fields map[string]wireField `json:"fields"`
}

type wireAnswer struct {
	Type  string          `json:"type"`
	Value json.RawMessage `json:"value"`
}

type wireAnswers struct {
	Answers map[string]wireAnswer `json:"answers"`
}

// wireResponse accepts both a bare System One response ({"answers": ...}) and
// the Workers AI envelope ({"success": ..., "errors": [...], "result": {...}}).
type wireResponse struct {
	wireAnswers
	Result  *wireAnswers `json:"result"`
	Success *bool        `json:"success"`
	Errors  []struct {
		Message string `json:"message"`
	} `json:"errors"`
	Error string `json:"error"`
}

func (r *wireResponse) message() string {
	msg := r.Error
	for _, e := range r.Errors {
		if msg == "" {
			msg = e.Message
		}
	}
	if runes := []rune(msg); len(runes) > maxMessageRunes {
		msg = string(runes[:maxMessageRunes])
	}
	return msg
}

func (c *Client) post(ctx context.Context, content string) ([]string, error) {
	body, err := json.Marshal(wireRequest{
		Model: c.model,
		State: content,
		Fields: map[string]wireField{
			entitiesField: {Type: "list", Items: "text", Instructions: entitiesInstructions},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal extraction request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build extraction request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("extraction request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes))
		var payload wireResponse
		if json.Unmarshal(data, &payload) == nil && payload.message() != "" {
			return nil, fmt.Errorf("extraction server returned status %d: %s", resp.StatusCode, payload.message())
		}
		return nil, fmt.Errorf("extraction server returned status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read extraction response: %w", err)
	}
	if len(data) > maxResponseBytes {
		return nil, fmt.Errorf("extraction response exceeds %d bytes", maxResponseBytes)
	}
	var decoded wireResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, fmt.Errorf("decode extraction response: %w", err)
	}
	if decoded.Success != nil && !*decoded.Success {
		return nil, fmt.Errorf("extraction server reported failure: %s", decoded.message())
	}
	answers := decoded.Answers
	if decoded.Result != nil {
		answers = decoded.Result.Answers
	}
	answer, ok := answers[entitiesField]
	if !ok {
		return nil, fmt.Errorf("extraction response has no %q answer", entitiesField)
	}
	if answer.Type != "list" {
		return nil, fmt.Errorf("extraction answer %q has type %q, want list", entitiesField, answer.Type)
	}
	var items []string
	if len(answer.Value) > 0 && string(answer.Value) != "null" {
		if err := json.Unmarshal(answer.Value, &items); err != nil {
			return nil, fmt.Errorf("decode extraction list: %w", err)
		}
	}
	return items, nil
}

// groundedEntities keeps the model's items that name something in content.
// Each item is trimmed of the spaces, quote marks, and clause punctuation a
// span can drag in at its edges, and is dropped unless it has at least two
// runes, a letter, at most MaxEntityBytes, and an occurrence in content that is
// a whole token rather than a fragment of a longer word, path, or version.
func groundedEntities(content string, items []string) []string {
	out := []string{}
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		name := strings.TrimFunc(item, isEdgeNoise)
		if seen[name] || len(name) > MaxEntityBytes || utf8.RuneCountInString(name) < 2 ||
			!strings.ContainsFunc(name, unicode.IsLetter) || !appearsAsToken(content, name) {
			continue
		}
		seen[name] = true
		out = append(out, name)
		if len(out) == MaxEntities {
			break
		}
	}
	return out
}

func isEdgeNoise(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune(`"'“”‘’「」『』,;:，；：、。`, r)
}

// appearsAsToken reports whether name occurs in content without the token
// continuing on either side, directly ("TW" inside "zh-TW") or across a joiner
// ("5.1" inside "5.1.0", "w/demo" inside "~/w/demo").
func appearsAsToken(content, name string) bool {
	for start := 0; start <= len(content); {
		i := strings.Index(content[start:], name)
		if i < 0 {
			return false
		}
		i += start
		before, after := content[:i], content[i+len(name):]
		r1, s1 := utf8.DecodeLastRuneInString(before)
		r2, _ := utf8.DecodeLastRuneInString(before[:len(before)-s1])
		f1, s2 := utf8.DecodeRuneInString(after)
		f2, _ := utf8.DecodeRuneInString(after[s2:])
		if !continues(r1, r2) && !continues(f1, f2) {
			return true
		}
		start = i + 1
	}
	return false
}

// continues reports whether next, then beyond, moving away from a match,
// carry its token on.
func continues(next, beyond rune) bool {
	if isWordRune(next) {
		return true
	}
	return strings.ContainsRune(`.-/\:@`, next) && isWordRune(beyond)
}

// isWordRune reports runes that extend a token. CJK scripts are excluded
// because they do not separate words, so any neighbour would look like one.
func isWordRune(r rune) bool {
	if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) {
		return false
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '~'
}
