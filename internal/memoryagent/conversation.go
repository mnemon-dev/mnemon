package memoryagent

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// Conversational writes pack chat messages the way the reference system
// ("author", scripts/bench/seed.ts) packed conversations: one `speaker: text`
// line per message, long messages wrapped at spaces, then lines grouped into
// dated chunks of about 600 characters. Lengths are JavaScript string lengths,
// that is UTF-16 code units, so the chunks have the same size as the author's.

const (
	dialogueLineLimit = 540
	noteChunkLimit    = 600
	// A conversation is bounded like Write's text; the id and the speaker end
	// up in every chunk heading and line, so they stay short.
	maxConversationBytes    = 100000
	maxConversationMessages = 10000
	maxSessionIDBytes       = 256
	maxRoleBytes            = 64
)

type ConversationMessage struct {
	Role    string     `json:"role"`
	Content string     `json:"content"`
	At      *time.Time `json:"at,omitempty"`
}

type ConversationRequest struct {
	ID          string                `json:"id,omitempty"`
	Messages    []ConversationMessage `json:"messages"`
	SessionID   string                `json:"sessionId"`
	Source      string                `json:"source,omitempty"`
	Scope       string                `json:"scope,omitempty"`
	WorkspaceID *string               `json:"workspaceId,omitempty"`
}

// WriteConversation stores a conversation as an archived parent document whose
// content is the packed chunks joined by a blank line, plus one searchable
// memory-record per chunk that points at its byte range in the parent. Chunk
// text carries the date in its heading, from the first message that has a
// timestamp, or no date when none has one.
//
// The conversation's session id names the conversation in the heading and in
// every chunk's data.session. It becomes the record's own sessionId only for the
// "session" scope, like Write; other scopes cannot carry a session.
func WriteConversation(ctx context.Context, store *records.Store, req ConversationRequest) (WriteResult, error) {
	parent, chunks, err := PackConversation(req)
	if err != nil {
		return WriteResult{}, err
	}
	write := WriteRequest{ID: req.ID, Source: req.Source, Scope: req.Scope}
	if req.WorkspaceID != nil {
		write.WorkspaceID = *req.WorkspaceID
	}
	if req.Scope == "session" {
		write.SessionID = req.SessionID
	}
	if err := prepareWrite(&write); err != nil {
		return WriteResult{}, err
	}
	at, dated := conversationStart(req)
	if !dated {
		at = time.Now().UTC()
	}
	spans := make([][2]int, len(chunks))
	offset := 0
	for i, chunk := range chunks {
		spans[i] = [2]int{offset, offset + len(chunk)}
		offset += len(chunk) + len("\n\n")
	}
	upserts, result := buildDocument(write, at, parent, spans, map[string]any{"session": req.SessionID})
	return putDocument(ctx, store, upserts, result)
}

// PackConversation validates req as WriteConversation does and returns what
// it stores: the parent document's content and the chunks, one searchable
// record each; parent is the chunks joined by a blank line. A retried write
// can be compared with a stored document by its parent content. Only the
// messages and the session id are packed; id, source and scope are not.
func PackConversation(req ConversationRequest) (parent string, chunks []string, err error) {
	if err := validateConversation(req); err != nil {
		return "", nil, err
	}
	heading := "Conversation " + req.SessionID
	if at, dated := conversationStart(req); dated {
		heading += " on " + at.Format("2006/01/02 (Mon) 15:04")
	}
	chunks = NoteChunks(heading, DialogueLines(req.Messages, dialogueLineLimit), noteChunkLimit)
	return strings.Join(chunks, "\n\n"), chunks, nil
}

// conversationStart is the UTC time of the first message that has a
// timestamp; dated is false when none has one.
func conversationStart(req ConversationRequest) (at time.Time, dated bool) {
	for _, message := range req.Messages {
		if message.At != nil && !message.At.IsZero() {
			return message.At.UTC(), true
		}
	}
	return time.Time{}, false
}

func validateConversation(req ConversationRequest) error {
	if len(req.Messages) == 0 || len(req.Messages) > maxConversationMessages {
		return errors.New("conversation requires 1–10000 messages")
	}
	if req.SessionID == "" || len(req.SessionID) > maxSessionIDBytes || !utf8.ValidString(req.SessionID) || strings.ContainsAny(req.SessionID, "\r\n") {
		return errors.New("conversation requires a single-line UTF-8 session id of 1–256 bytes")
	}
	if len(req.Source) > 2048 {
		return errors.New("conversation source is too long")
	}
	total := 0
	for _, message := range req.Messages {
		role := strings.TrimFunc(message.Role, isJSSpace)
		if role == "" || len(message.Role) > maxRoleBytes || !utf8.ValidString(message.Role) || strings.ContainsAny(message.Role, "\r\n") {
			return errors.New("every message needs a single-line UTF-8 role of 1–64 bytes")
		}
		if !utf8.ValidString(message.Content) {
			return errors.New("message content must be valid UTF-8")
		}
		total += len(message.Content)
	}
	if total > maxConversationBytes {
		return errors.New("conversation exceeds 100000 bytes of message text")
	}
	return nil
}

// DialogueLines turns messages into `role: text` lines (author: dialogueLines).
// Whitespace runs in the text collapse to one space; a line longer than limit
// UTF-16 units is cut at the last space at or before limit when that space is
// past half the limit, otherwise at limit. The rest continues on the next line
// without a speaker prefix.
func DialogueLines(msgs []ConversationMessage, limit int) []string {
	if limit < 1 {
		limit = 1
	}
	var lines []string
	for _, message := range msgs {
		rest := message.Role + ": " + collapseSpace(message.Content)
		for jsLen(rest) > limit {
			// Like rest.lastIndexOf(' ', limit): the last space in units [0, limit].
			cut := jsOffset(rest, limit)
			window := rest[:jsOffset(rest, limit+1)]
			if space := strings.LastIndexByte(window, ' '); space >= 0 && jsLen(rest[:space]) > limit/2 {
				cut = space
			}
			if cut == 0 { // a limit smaller than the first character: take it whole
				_, cut = utf8.DecodeRuneInString(rest)
			}
			lines = append(lines, rest[:cut])
			rest = strings.TrimLeftFunc(rest[cut:], isJSSpace)
		}
		lines = append(lines, rest)
	}
	return lines
}

// NoteChunks groups lines into chunks (author: noteChunks), each headed by
// `heading:` and a newline. Blank lines are dropped. A new chunk starts when
// adding the next line would make join(lines)+len(heading) exceed limit UTF-16
// units, so a line is never split and a lone long line forms its own chunk.
// Like the author's rule this ignores the two units of ":\n", so a chunk can
// reach limit+2.
func NoteChunks(heading string, lines []string, limit int) []string {
	var chunks []string
	var chunk []string
	size := 0 // jsLen(strings.Join(chunk, "\n"))
	for _, block := range lines {
		for _, line := range strings.Split(block, "\n") {
			if strings.TrimFunc(line, isJSSpace) == "" {
				continue
			}
			if len(chunk) > 0 && size+1+jsLen(line)+jsLen(heading) > limit {
				chunks = append(chunks, heading+":\n"+strings.Join(chunk, "\n"))
				chunk, size = nil, 0
			}
			if len(chunk) > 0 {
				size++
			}
			chunk = append(chunk, line)
			size += jsLen(line)
		}
	}
	if len(chunk) > 0 {
		chunks = append(chunks, heading+":\n"+strings.Join(chunk, "\n"))
	}
	return chunks
}

// isJSSpace reports whether r matches JavaScript's \s and String.prototype.trim:
// Unicode space separators, the ASCII spaces, line terminators and the byte
// order mark, but not U+0085.
func isJSSpace(r rune) bool {
	return r == 0xFEFF || r != '\u0085' && unicode.IsSpace(r)
}

// collapseSpace is text.replace(/\s+/g, ' ').trim().
func collapseSpace(text string) string {
	var out strings.Builder
	out.Grow(len(text))
	pending := false
	for _, r := range text {
		if isJSSpace(r) {
			pending = out.Len() > 0
			continue
		}
		if pending {
			out.WriteByte(' ')
			pending = false
		}
		out.WriteRune(r)
	}
	return out.String()
}

// jsLen is JavaScript's string length: the number of UTF-16 code units.
func jsLen(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r >= 0x10000 {
			n++
		}
	}
	return n
}

// jsSlice is s.slice(start, end) over UTF-16 unit indexes. A cut inside a
// surrogate pair moves left, so the result is always valid UTF-8 and never
// holds half a pair: a pair at the end cut is left out, a pair at the start
// cut is kept. Out-of-range indexes clamp, and end before start gives "".
func jsSlice(s string, start, end int) string {
	from, to := jsOffset(s, start), jsOffset(s, end)
	if to < from {
		return ""
	}
	return s[from:to]
}

// jsOffset is the byte offset of UTF-16 unit index unit in s, clamped to
// [0, len(s)]; a unit inside a surrogate pair gives the offset of the pair.
func jsOffset(s string, unit int) int {
	pos := 0
	for i, r := range s {
		width := 1
		if r >= 0x10000 {
			width = 2
		}
		if pos+width > unit {
			return i
		}
		pos += width
	}
	return len(s)
}
