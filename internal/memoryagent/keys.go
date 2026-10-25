package memoryagent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path"
	"slices"
	"strings"
	"sync/atomic"
	"time"
)

// An account's own provider keys (ruling R98, the BYOK contract): an account
// of the memory cloud may bring its own DeepSeek and JEV keys. For each of
// those kinds the account's key, when it has one, is sent in place of the
// host's (the environment variable the model's keyEnv names). The providers
// read their key from a ProviderKeys per call: a model call takes the key
// when it starts and keeps it through its attempts, so a key replaced
// meanwhile applies to the calls that start after the replacement, while
// those in flight finish with theirs. aml-serve keeps the account's keys in
// a key file of its own and replaces them through its admin listener;
// aml-inspect reads the same file. A key is never logged, put in an error or
// a metric, or answered: only its fingerprint (Fingerprint) is shown.

// keyKinds are the provider kinds an account may bring its own key for.
var keyKinds = []string{"deepseek", "jev"}

// KeyKinds are the provider kinds an account may bring its own key for, in
// order: deepseek and jev.
func KeyKinds() []string { return slices.Clone(keyKinds) }

// The sources of a kind's key (KeyStatus.Source).
const (
	// KeySourceAccount: the account's own key.
	KeySourceAccount = "account"
	// KeySourceHost: the host's key, the variable the model's keyEnv names.
	KeySourceHost = "host"
	// KeySourceNone: neither; the kind's model calls fail without a request.
	KeySourceNone = "none"
)

// KeyStatus says where a kind's key comes from, and which key it is by its
// fingerprint, "" with none.
type KeyStatus struct {
	Source      string `json:"source"`
	Fingerprint string `json:"fingerprint"`
}

// Fingerprint is the first 12 hex digits of the SHA-256 of key: what is shown
// of a key, never the key.
func Fingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:12]
}

// The bounds of an account key's length, in bytes.
const minKeyLength, maxKeyLength = 8, 512

// ValidKey reports whether key may be an account key: 8 to 512 printable
// ASCII characters, none of them a space.
func ValidKey(key string) bool {
	if len(key) < minKeyLength || len(key) > maxKeyLength {
		return false
	}
	for i := 0; i < len(key); i++ {
		if key[i] <= ' ' || key[i] > '~' {
			return false
		}
	}
	return true
}

// ProviderKeys is the key source of a configuration's providers: for each
// key kind (KeyKinds) the configuration's models use, the account's key over
// the host's. Its account keys are an atomic pointer to a map never changed
// once stored, so a call reads them without a lock. It is safe for
// concurrent use; the nil *ProviderKeys has the host's keys alone.
type ProviderKeys struct {
	// kinds are the key kinds the configuration uses, in keyKinds order, and
	// hostEnv each one's host variable: the keyEnv of its first model, in
	// role order (planner, judge, consolidator).
	kinds   []string
	hostEnv map[string]string
	// account are the account's keys by kind; never nil once made.
	account atomic.Pointer[map[string]string]
}

// NewProviderKeys is the key source of config's models, with the host's keys
// alone until SetAccount.
func NewProviderKeys(config Config) *ProviderKeys {
	k := &ProviderKeys{hostEnv: map[string]string{}}
	for _, model := range []*ModelConfig{config.Planner, config.Judge, config.Consolidator} {
		if model == nil || !slices.Contains(keyKinds, model.Kind) {
			continue
		}
		if _, ok := k.hostEnv[model.Kind]; !ok {
			k.hostEnv[model.Kind] = model.KeyEnv
		}
	}
	for _, kind := range keyKinds {
		if _, ok := k.hostEnv[kind]; ok {
			k.kinds = append(k.kinds, kind)
		}
	}
	k.account.Store(&map[string]string{})
	return k
}

// Kinds are the key kinds the configuration's models use, in order.
func (k *ProviderKeys) Kinds() []string { return slices.Clone(k.kinds) }

// Uses reports whether the configuration's models use the key kind.
func (k *ProviderKeys) Uses(kind string) bool { return slices.Contains(k.kinds, kind) }

// Account is a copy of the account's keys, by kind.
func (k *ProviderKeys) Account() map[string]string { return maps.Clone(*k.account.Load()) }

// KindName is kind as an error may name it: itself when it is a provider
// kind, else a description, since what stands where a kind should be may be
// a key pasted in the wrong place.
func KindName(kind string) string {
	switch kind {
	case "deepseek", "jev", "openai", "local":
		return kind
	}
	return "a kind that is not deepseek or jev"
}

// SetAccount replaces the account's keys with keys, by kind (nil or empty
// for none): the model calls that start once it has returned send them, and
// those in flight keep theirs. A kind the configuration does not use, or a
// key that is not a valid one (ValidKey), is refused, and nothing changes;
// the error names the kind, never the key.
func (k *ProviderKeys) SetAccount(keys map[string]string) error {
	next := map[string]string{}
	for _, kind := range slices.Sorted(maps.Keys(keys)) {
		switch {
		case !k.Uses(kind):
			return fmt.Errorf("account keys: the configuration has no model of %s", KindName(kind))
		case !ValidKey(keys[kind]):
			return fmt.Errorf("account keys: the %s key is not 8 to 512 printable ASCII characters without spaces", kind)
		}
		next[kind] = keys[kind]
	}
	k.account.Store(&next)
	return nil
}

// credential is the key a model of kind whose keyEnv is keyEnv sends: the
// account's key of the kind, else the variable keyEnv names; "" with
// neither. The nil *ProviderKeys has no account keys.
func (k *ProviderKeys) credential(kind, keyEnv string) string {
	if k != nil {
		if key := (*k.account.Load())[kind]; key != "" {
			return key
		}
	}
	if keyEnv == "" {
		return ""
	}
	return os.Getenv(keyEnv)
}

// Status is where each kind's key comes from, by the kinds the configuration
// uses: the account's, the host's, or none (its host variable is empty).
func (k *ProviderKeys) Status() map[string]KeyStatus {
	out := map[string]KeyStatus{}
	account := *k.account.Load()
	for _, kind := range k.kinds {
		if key := account[kind]; key != "" {
			out[kind] = KeyStatus{Source: KeySourceAccount, Fingerprint: Fingerprint(key)}
			continue
		}
		host := ""
		if env := k.hostEnv[kind]; env != "" {
			host = os.Getenv(env)
		}
		if host == "" {
			out[kind] = KeyStatus{Source: KeySourceNone}
			continue
		}
		out[kind] = KeyStatus{Source: KeySourceHost, Fingerprint: Fingerprint(host)}
	}
	return out
}

// KeyCheckTimeout bounds the check of one key (Models.CheckKey), its
// fallback included.
const KeyCheckTimeout = 10 * time.Second

var (
	// ErrKeyRejected is the error of a key the provider refused: 401 or 403,
	// a wrong, expired or revoked key; 402, an account without balance.
	ErrKeyRejected = errors.New("the provider rejected the key")
	// ErrKeyCheckUnavailable is the error of a key the check could not judge:
	// no answer (a timeout, a broken connection), or an answer that says
	// nothing of the key (a 5xx, a 429, a redirect).
	ErrKeyCheckUnavailable = errors.New("the key could not be checked")
)

// The ways a key is checked (KeyCheck.Via).
const (
	// KeyCheckModels: GET of the provider's list of models.
	KeyCheckModels = "models"
	// KeyCheckCompletion: one minimal call, when the provider has no list.
	KeyCheckCompletion = "completion"
)

// KeyCheck is how a key was checked: Via, KeyCheckModels or
// KeyCheckCompletion, and Status, the HTTP status of the check's last
// request (0 when it had no answer).
type KeyCheck struct {
	Via    string
	Status int
}

// CheckKey checks key for kind with the first provider of that kind among
// the models (Provider.CheckKey). A kind no model has cannot be checked.
func (m Models) CheckKey(ctx context.Context, kind, key string) (KeyCheck, error) {
	for _, p := range m.providers() {
		if p.config.Kind == kind {
			return p.CheckKey(ctx, key)
		}
	}
	return KeyCheck{}, fmt.Errorf("%w: no model of %s", ErrKeyCheckUnavailable, KindName(kind))
}

// maxCheckReply bounds what is read of a check's answer, which is read only
// so that its connection can be used again.
const maxCheckReply = 64 << 10

// CheckKey checks whether the provider takes key, within KeyCheckTimeout (or
// ctx's deadline, when sooner): one GET of {base}/models, the base being the
// endpoint without its final route (DeepSeek's https://api.deepseek.com,
// JEV's https://api.typesafe.ai/v1), with key as its Bearer credential. When
// that route answers 404 or 405, one minimal call of the model takes its
// place: a chat completion of max_tokens 1, or for JEV, whose API decides
// rather than completes, a decision of one question. Both go through the
// provider's own transport (HTTP/1.1, R88) but outside the resilience layer:
// no retry, breaker, bulkhead, observer or rejection count sees them, and no
// budget is reserved or spent; the fallback's few tokens are the provider's
// to bill, and Via says it was made. A 2xx is a good key; 401, 402 or 403
// ErrKeyRejected; anything else, no answer included, ErrKeyCheckUnavailable.
// No error holds the key.
func (p *Provider) CheckKey(ctx context.Context, key string) (KeyCheck, error) {
	ctx, cancel := context.WithTimeout(ctx, KeyCheckTimeout)
	defer cancel()
	check := KeyCheck{Via: KeyCheckModels}
	var err error
	check.Status, err = p.probe(ctx, http.MethodGet, p.modelsURL(), nil, key)
	if err == nil && (check.Status == http.StatusNotFound || check.Status == http.StatusMethodNotAllowed) {
		check.Via = KeyCheckCompletion
		check.Status, err = p.probe(ctx, http.MethodPost, p.endpoint, p.checkBody(), key)
	}
	switch {
	case err != nil && timedOut(err):
		return check, fmt.Errorf("%w: no answer within the check's %v", ErrKeyCheckUnavailable, KeyCheckTimeout)
	case err != nil:
		return check, fmt.Errorf("%w: no answer", ErrKeyCheckUnavailable)
	case check.Status >= 200 && check.Status <= 299:
		return check, nil
	case rejectedStatus(check.Status):
		return check, fmt.Errorf("%w: HTTP %d", ErrKeyRejected, check.Status)
	}
	return check, fmt.Errorf("%w: HTTP %d", ErrKeyCheckUnavailable, check.Status)
}

// probe sends one request of a key check with key as its credential and
// returns the status answered; its answer is read, up to maxCheckReply, and
// dropped.
func (p *Provider) probe(ctx context.Context, method, target string, body []byte, key string) (int, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return 0, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Authorization", "Bearer "+key)
	response, err := p.client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxCheckReply))
	return response.StatusCode, nil
}

// modelsURL is the provider's list of models: its endpoint with the final
// route (/chat/completions, or JEV's /systemone) replaced by /models.
func (p *Provider) modelsURL() string {
	u, err := url.Parse(p.endpoint)
	if err != nil {
		return p.endpoint
	}
	route := "/chat/completions"
	if p.config.Kind == "jev" {
		route = "/systemone"
	}
	base, found := strings.CutSuffix(u.Path, route)
	if !found {
		base = path.Dir(u.Path)
	}
	u.Path, u.RawPath = strings.TrimRight(base, "/")+"/models", ""
	return u.String()
}

// checkBody is the fallback check's request: a chat completion of max_tokens
// 1, or a JEV decision of one question.
func (p *Provider) checkBody() []byte {
	var body any = map[string]any{"model": p.config.Model, "messages": []map[string]string{{"role": "user", "content": "ping"}}, "max_tokens": 1}
	if p.config.Kind == "jev" {
		body = map[string]any{"model": p.config.Model, "state": map[string]any{},
			"questions": map[string]Question{"check": {Type: "noul", Instructions: "Is this request a check of the API key?"}}}
	}
	encoded, _ := encodeRequest(body)
	return encoded
}
