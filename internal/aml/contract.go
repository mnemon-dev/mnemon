// Package aml serves the Agent Memory Leaderboard (AML) Add/Search contract
// over one records store per user: Add stores a chunk of a conversation and
// returns once it is persisted and searchable, Search returns ranked stored
// evidence (records and consolidated layer blocks), never generated answers.
//
// AML publishes a run only if every task succeeds and does not retry a 4xx,
// so the adapter reads requests leniently: only what identifies and isolates
// an Add (request_id, user_id, session_id) or a Search can be refused. The
// shape of the content never is.
package aml

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mnemon-dev/mnemon/internal/memoryagent"
)

const (
	// partBytes bounds the message text of one stored part of an Add longer
	// than one document holds (memoryagent.MaxConversationBytes).
	partBytes = 90000
	// maxSessionBytes and maxRoleBytes bound the sanitized session id and
	// speaker, which every chunk heading and line repeats.
	maxSessionBytes = 200
	maxRoleBytes    = 64
)

// AddRequest is POST /add: one chunk of one user's conversation. The platform
// retries a failed Add with the same request_id.
type AddRequest struct {
	RequestID string    `json:"request_id"`
	Messages  []Message `json:"messages"`
	UserID    string    `json:"user_id"`
	SessionID string    `json:"session_id"`
}

// UnmarshalJSON reads an Add leniently: ids given as JSON numbers keep their
// decimal text, a message that is not an object is an empty message (skipped
// later), and messages that are not an array are none. Only a body that is
// not a JSON object fails.
func (r *AddRequest) UnmarshalJSON(raw []byte) error {
	var fields struct {
		RequestID json.RawMessage `json:"request_id"`
		Messages  json.RawMessage `json:"messages"`
		UserID    json.RawMessage `json:"user_id"`
		SessionID json.RawMessage `json:"session_id"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	*r = AddRequest{RequestID: scalarText(fields.RequestID), UserID: scalarText(fields.UserID), SessionID: scalarText(fields.SessionID)}
	var messages []json.RawMessage
	if json.Unmarshal(fields.Messages, &messages) == nil {
		r.Messages = make([]Message, len(messages))
		for i, message := range messages {
			r.Messages[i].UnmarshalJSON(message)
		}
	}
	return nil
}

// Message is one chat message; Timestamp is Unix milliseconds (read from
// milliseconds, seconds or RFC 3339), and 0 counts as none.
type Message struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp *int64 `json:"timestamp,omitempty"`
}

// UnmarshalJSON reads a message without ever failing. The role may be a
// string or a number. The content may be a string, a number, or multimodal
// parts, whose text parts (plain strings, or objects with a string "text"
// whose "type" ends in "text": text, input_text, output_text) are joined by
// newlines and whose other parts (images) are dropped. The timestamp may be
// a number or a numeric string (timestampMS), or an RFC 3339 string;
// anything else is no timestamp. A message that is not an object is empty.
func (m *Message) UnmarshalJSON(raw []byte) error {
	*m = Message{}
	var fields struct {
		Role      json.RawMessage `json:"role"`
		Content   json.RawMessage `json:"content"`
		Timestamp json.RawMessage `json:"timestamp"`
	}
	if json.Unmarshal(raw, &fields) != nil {
		return nil
	}
	m.Role, m.Content, m.Timestamp = scalarText(fields.Role), contentText(fields.Content), timestampMS(fields.Timestamp)
	return nil
}

// scalarText is a JSON string's value or a JSON number's decimal text; ""
// for anything else.
func scalarText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	switch c := raw[0]; {
	case c == '"':
		var text string
		if json.Unmarshal(raw, &text) == nil {
			return text
		}
	case c == '-' || c >= '0' && c <= '9':
		return string(raw)
	}
	return ""
}

// contentText is the text of a message's content.
func contentText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	switch raw[0] {
	case '[':
		var parts []json.RawMessage
		json.Unmarshal(raw, &parts)
		texts := []string{}
		for _, part := range parts {
			if text := partText(part); strings.TrimSpace(text) != "" {
				texts = append(texts, text)
			}
		}
		return strings.Join(texts, "\n")
	case '{':
		return partText(raw)
	}
	return scalarText(raw)
}

// partText is the text of one multimodal part: a string, or an object with
// a string "text" whose "type" ends in "text" (text, input_text,
// output_text); "" for an image or anything else.
func partText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var part struct {
		Type string  `json:"type"`
		Text *string `json:"text"`
	}
	if json.Unmarshal(raw, &part) == nil && strings.HasSuffix(part.Type, "text") && part.Text != nil {
		return *part.Text
	}
	return ""
}

// secondsBelow is where numeric timestamps change unit: a magnitude below
// 1e11 is Unix seconds (1e11 s is the year 5138, 1e11 ms is 1973).
const secondsBelow = 1e11

// timestampMS reads a timestamp as Unix milliseconds: a number or numeric
// string (seconds when its magnitude is below 1e11, else milliseconds, cut
// to whole milliseconds) or an RFC 3339 string; nil for anything else, and
// for 0.
func timestampMS(raw json.RawMessage) *int64 {
	var number json.Number
	if json.Unmarshal(raw, &number) != nil || number == "" {
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return nil
		}
		at, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(text))
		if err != nil {
			return nil
		}
		ms := at.UnixMilli()
		if ms == 0 {
			return nil
		}
		return &ms
	}
	value, err := number.Float64()
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) >= 1<<62 {
		return nil
	}
	ms, err := number.Int64()
	switch {
	case math.Abs(value) < secondsBelow:
		ms = int64(value * 1000)
	case err != nil:
		ms = int64(value)
	}
	if ms == 0 {
		return nil
	}
	return &ms
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

// UnmarshalJSON reads a Search leniently: the query, the options and the
// user id may be strings or numbers, options that are not an array are none,
// and top_k may be an integer, an integral float (100.0) or a numeric string;
// a fraction is cut to its integer part, and anything else is no top_k (0).
func (r *SearchRequest) UnmarshalJSON(raw []byte) error {
	var fields struct {
		Query   json.RawMessage `json:"query"`
		Options json.RawMessage `json:"options"`
		UserID  json.RawMessage `json:"user_id"`
		TopK    json.RawMessage `json:"top_k"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	*r = SearchRequest{Query: scalarText(fields.Query), UserID: scalarText(fields.UserID)}
	var options []json.RawMessage
	if json.Unmarshal(fields.Options, &options) == nil {
		for _, option := range options {
			if text := scalarText(option); text != "" {
				r.Options = append(r.Options, text)
			}
		}
	}
	var topK json.Number
	if json.Unmarshal(fields.TopK, &topK) != nil || topK == "" {
		return nil
	}
	n, err := topK.Int64()
	if err != nil {
		value, ferr := topK.Float64()
		if ferr != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil
		}
		n = int64(max(min(value, 1e6), -1e6))
	}
	r.TopK = int(max(min(n, 1e6), -1e6))
	return nil
}

// SearchItem is one piece of stored evidence, best first. Only Content is
// sure to reach the answering model, so dates live inside it. Score is
// 1/(rank+1) by the item's 0-based rank in the response (1 for the first,
// then 0.5, …), so any sort by score keeps the order; CreatedAt is omitted
// when unknown.
type SearchItem struct {
	ID        string  `json:"id"`
	Content   string  `json:"content"`
	Score     float64 `json:"score,omitempty"`
	CreatedAt string  `json:"created_at,omitempty"`
}

// SearchResponse is the body of a Search: at most top_k items, possibly none.
type SearchResponse struct {
	Data []SearchItem `json:"data"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// validate checks what identifies and isolates an Add.
func (r AddRequest) validate() error {
	switch {
	case strings.TrimSpace(r.RequestID) == "":
		return errors.New("request_id is required")
	case strings.TrimSpace(r.UserID) == "":
		return errors.New("user_id is required")
	case strings.TrimSpace(r.SessionID) == "":
		return errors.New("session_id is required")
	}
	return nil
}

// addPart is one stored document of an Add.
type addPart struct {
	conversation memoryagent.ConversationRequest
	parent       string   // the document's content
	chunks       []string // its searchable records' texts
}

// parts are the documents an Add stores, in order, and the number of
// messages without text it skipped; no parts when no message has text.
// The session id and speakers are made storable (sanitizeSession,
// sanitizeRole). An Add with more text than one document holds is split
// into parts of at most partBytes of message text, in message order; a
// longer message is cut (splitText) into consecutive messages with its role
// and timestamp. Part 0's id is derived from the request id alone, so an
// Add that fits one document is stored as before.
func (r AddRequest) parts() ([]addPart, int, error) {
	session := sanitizeSession(r.SessionID)
	messages := []memoryagent.ConversationMessage{}
	skipped, total := 0, 0
	for _, message := range r.Messages {
		if strings.TrimSpace(message.Content) == "" {
			skipped++
			continue
		}
		stored := memoryagent.ConversationMessage{Role: sanitizeRole(message.Role), Content: message.Content}
		if message.Timestamp != nil {
			at := time.UnixMilli(*message.Timestamp).UTC()
			stored.At = &at
		}
		messages = append(messages, stored)
		total += len(message.Content)
	}
	if len(messages) == 0 {
		return nil, skipped, nil
	}
	groups := [][]memoryagent.ConversationMessage{messages}
	if total > memoryagent.MaxConversationBytes || len(messages) > memoryagent.MaxConversationMessages {
		groups = splitMessages(messages)
	}
	parts := make([]addPart, len(groups))
	for i, group := range groups {
		conversation := memoryagent.ConversationRequest{ID: partID(r.RequestID, i), SessionID: session, Messages: group}
		parent, chunks, err := memoryagent.PackConversation(conversation)
		if err != nil {
			return nil, skipped, err
		}
		parts[i] = addPart{conversation: conversation, parent: parent, chunks: chunks}
	}
	return parts, skipped, nil
}

// splitMessages groups messages into parts of at most partBytes of text and
// memoryagent.MaxConversationMessages messages, cutting longer messages.
func splitMessages(messages []memoryagent.ConversationMessage) [][]memoryagent.ConversationMessage {
	var groups [][]memoryagent.ConversationMessage
	var group []memoryagent.ConversationMessage
	size := 0
	for _, message := range messages {
		for _, piece := range splitText(message.Content, partBytes) {
			if len(group) > 0 && (size+len(piece) > partBytes || len(group) == memoryagent.MaxConversationMessages) {
				groups, group, size = append(groups, group), nil, 0
			}
			cut := message
			cut.Content = piece
			group, size = append(group, cut), size+len(piece)
		}
	}
	return append(groups, group)
}

// splitText cuts text into pieces of at most limit bytes at UTF-8
// boundaries, each at the last whitespace of its second half when it has
// one; pieces of only whitespace are dropped.
func splitText(text string, limit int) []string {
	pieces := []string{}
	for len(text) > limit {
		cut := limit
		for !utf8.RuneStart(text[cut]) {
			cut--
		}
		if space := strings.LastIndexFunc(text[:cut], unicode.IsSpace); space > limit/2 {
			cut = space
		}
		if strings.TrimSpace(text[:cut]) != "" {
			pieces = append(pieces, text[:cut])
		}
		text = text[cut:]
	}
	if strings.TrimSpace(text) != "" {
		pieces = append(pieces, text)
	}
	return pieces
}

// sanitizeSession makes a session id storable in a chunk heading: every
// whitespace or control character becomes "_" (the heading names the session
// as one word), then at most maxSessionBytes at a rune boundary.
func sanitizeSession(id string) string {
	clean := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return '_'
		}
		return r
	}, id)
	return capBytes(clean, maxSessionBytes)
}

// sanitizeRole makes a speaker storable as a line prefix: whitespace and
// control runs become one space, at most maxRoleBytes; no speaker is
// "unknown".
func sanitizeRole(role string) string {
	role = strings.Join(strings.FieldsFunc(role, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || r == '\uFEFF'
	}), " ")
	if role = strings.TrimSpace(capBytes(role, maxRoleBytes)); role == "" {
		return "unknown"
	}
	return role
}

// capBytes is text cut to at most n bytes at a rune boundary.
func capBytes(text string, n int) string {
	if len(text) <= n {
		return text
	}
	for n > 0 && !utf8.RuneStart(text[n]) {
		n--
	}
	return text[:n]
}

// documentID derives an Add's document id from its request id, so a retried
// Add finds its document; each user has their own store, so ids only need to
// be unique per user.
func documentID(requestID string) string { return "aml-" + hexDigest(requestID)[:40] }

// partID is the document id of an Add's part: documentID for part 0.
func partID(requestID string, part int) string {
	if part == 0 {
		return documentID(requestID)
	}
	return "aml-" + hexDigest(requestID + "\x00" + strconv.Itoa(part))[:40]
}

func hexDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// validate checks what identifies a Search: its user. A blank query and a
// missing top_k are answered, not refused.
func (r SearchRequest) validate() error {
	if strings.TrimSpace(r.UserID) == "" {
		return errors.New("user_id is required")
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
