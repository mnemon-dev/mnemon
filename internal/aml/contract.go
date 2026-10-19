// Package aml serves the Agent Memory Leaderboard (AML) Add/Search contract
// over one records store per user: Add stores a chunk of a conversation and
// returns once it is persisted and searchable, Search returns ranked stored
// evidence (records and consolidated layer blocks), never generated answers.
package aml

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
)

// AddRequest is POST /add: one chunk of one user's conversation. The platform
// retries a failed Add with the same request_id.
type AddRequest struct {
	RequestID string    `json:"request_id"`
	Messages  []Message `json:"messages"`
	UserID    string    `json:"user_id"`
	SessionID string    `json:"session_id"`
}

// Message is one chat message of the textual track; Timestamp is Unix
// milliseconds, and 0 counts as none.
type Message struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp *int64 `json:"timestamp,omitempty"`
}

// UnmarshalJSON reads a message strictly enough to name what is wrong: the
// role and content must be strings (multimodal content arrays are outside the
// textual track) and the timestamp a number or null. A fractional timestamp
// is cut to whole milliseconds.
func (m *Message) UnmarshalJSON(raw []byte) error {
	var fields struct {
		Role      json.RawMessage `json:"role"`
		Content   json.RawMessage `json:"content"`
		Timestamp json.RawMessage `json:"timestamp"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	*m = Message{}
	if err := jsonString(fields.Role, &m.Role); err != nil {
		return errors.New("message role must be a string")
	}
	if err := jsonString(fields.Content, &m.Content); err != nil {
		if trimmed := bytes.TrimSpace(fields.Content); len(trimmed) > 0 && trimmed[0] == '[' {
			return errors.New("message content must be a string: multimodal content is not supported on the text track")
		}
		return errors.New("message content must be a string")
	}
	if len(fields.Timestamp) == 0 || string(fields.Timestamp) == "null" {
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(fields.Timestamp, &number); err != nil {
		return errors.New("message timestamp must be a number of Unix milliseconds")
	}
	ms, err := number.Int64()
	if err != nil {
		value, ferr := number.Float64()
		if ferr != nil || math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) >= 1<<62 {
			return errors.New("message timestamp must be a number of Unix milliseconds")
		}
		ms = int64(value)
	}
	m.Timestamp = &ms
	return nil
}

// jsonString decodes a JSON string; absent or null is "".
func jsonString(raw json.RawMessage, into *string) error {
	if len(raw) == 0 || string(raw) == "null" {
		*into = ""
		return nil
	}
	return json.Unmarshal(raw, into)
}

// AddResponse echoes a stored Add.
type AddResponse struct {
	Success   bool   `json:"success"`
	RequestID string `json:"request_id"`
	UserID    string `json:"user_id"`
	SessionID string `json:"session_id"`
}

// SearchRequest is POST /search. Options are a multiple-choice question's
// choices; they join the question the memory reads.
type SearchRequest struct {
	Query   string   `json:"query"`
	Options []string `json:"options,omitempty"`
	UserID  string   `json:"user_id"`
	TopK    int      `json:"top_k"`
}

// SearchItem is one piece of stored evidence, best first. Only Content is
// sure to reach the answering model, so dates live inside it; Score and
// CreatedAt are omitted when unknown.
type SearchItem struct {
	ID        string  `json:"id"`
	Content   string  `json:"content"`
	Score     float64 `json:"score,omitempty"`
	CreatedAt string  `json:"created_at,omitempty"`
}

// UnmarshalJSON accepts top_k as an integer, an integral float (100.0) or a
// numeric string; a fraction is cut to its integer part.
func (r *SearchRequest) UnmarshalJSON(raw []byte) error {
	type plain SearchRequest
	var fields struct {
		plain
		TopK json.Number `json:"top_k"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	*r = SearchRequest(fields.plain)
	if fields.TopK == "" {
		return nil
	}
	n, err := fields.TopK.Int64()
	if err != nil {
		value, ferr := fields.TopK.Float64()
		if ferr != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return errors.New("top_k must be a number")
		}
		n = int64(max(min(value, 1e6), -1e6))
	}
	r.TopK = int(max(min(n, 1e6), -1e6))
	return nil
}

// SearchResponse is the body of a Search: at most top_k items, possibly none.
type SearchResponse struct {
	Data []SearchItem `json:"data"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// validate checks what the contract requires of an Add; the conversation
// packer then checks sizes, roles and the session id.
func (r AddRequest) validate() error {
	switch {
	case strings.TrimSpace(r.RequestID) == "":
		return errors.New("request_id is required")
	case strings.TrimSpace(r.UserID) == "":
		return errors.New("user_id is required")
	case strings.TrimSpace(r.SessionID) == "":
		return errors.New("session_id is required")
	case len(r.Messages) == 0:
		return errors.New("messages must hold at least one message")
	}
	for i, message := range r.Messages {
		if strings.TrimSpace(message.Role) == "" {
			return fmt.Errorf("message %d needs a role", i)
		}
		if message.Content == "" {
			return fmt.Errorf("message %d needs non-empty text content", i)
		}
	}
	return nil
}

// conversation is the Add as memoryagent's conversational write. The document
// id is derived from the request id, so a retried Add finds its document;
// each user has their own store, so ids only need to be unique per user.
func (r AddRequest) conversation() memoryagent.ConversationRequest {
	messages := make([]memoryagent.ConversationMessage, len(r.Messages))
	for i, message := range r.Messages {
		messages[i] = memoryagent.ConversationMessage{Role: message.Role, Content: message.Content}
		if message.Timestamp != nil && *message.Timestamp != 0 {
			at := time.UnixMilli(*message.Timestamp).UTC()
			messages[i].At = &at
		}
	}
	return memoryagent.ConversationRequest{ID: documentID(r.RequestID), SessionID: r.SessionID, Messages: messages}
}

func documentID(requestID string) string {
	sum := sha256.Sum256([]byte(requestID))
	return "aml-" + hex.EncodeToString(sum[:])[:40]
}

func (r SearchRequest) validate() error {
	switch {
	case strings.TrimSpace(r.Query) == "":
		return errors.New("query is required")
	case strings.TrimSpace(r.UserID) == "":
		return errors.New("user_id is required")
	case r.TopK < 1:
		return errors.New("top_k must be at least 1")
	}
	return nil
}

// question is what the memory reads: the query, then the options when the
// question has them.
func (r SearchRequest) question() string {
	if len(r.Options) == 0 {
		return r.Query
	}
	return r.Query + "\nOptions: " + strings.Join(r.Options, " | ")
}

// searchItems lists the View for AML: the layer blocks first, then the
// records, at most topK in all.
func searchItems(view memoryagent.View, topK int) []SearchItem {
	items := []SearchItem{}
	for _, block := range view.Index {
		if len(items) == topK {
			return items
		}
		items = append(items, SearchItem{ID: "index:" + block.ID, Content: block.Text})
	}
	for _, record := range view.Records {
		if len(items) == topK {
			break
		}
		items = append(items, SearchItem{ID: record.ID, Content: record.Text, Score: record.Score, CreatedAt: record.Date})
	}
	return items
}
