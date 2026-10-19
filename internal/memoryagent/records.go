// Package memoryagent composes the paper's bounded read-time memory strategy
// over records. It has no dependency on DSH or Agency.
package memoryagent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/mnemon-dev/mnemon/internal/records"
)

type WriteRequest struct {
	ID          string `json:"id,omitempty"`
	Text        string `json:"text"`
	Source      string `json:"source,omitempty"`
	At          string `json:"at,omitempty"`
	Scope       string `json:"scope,omitempty"`
	WorkspaceID string `json:"workspaceId,omitempty"`
	SessionID   string `json:"sessionId,omitempty"`
}

type Reference struct {
	ID   string `json:"id"`
	Hash string `json:"hash"`
}

type WriteResult struct {
	Revision string      `json:"revision"`
	Document Reference   `json:"document"`
	Records  []Reference `json:"records"`
}

type recordValue struct {
	ID          string         `json:"id"`
	Kind        string         `json:"kind"`
	Title       string         `json:"title"`
	Content     string         `json:"content"`
	Scope       string         `json:"scope"`
	WorkspaceID *string        `json:"workspaceId,omitempty"`
	SessionID   *string        `json:"sessionId,omitempty"`
	Date        *string        `json:"date,omitempty"`
	State       string         `json:"state"`
	Data        map[string]any `json:"data"`
	Signals     int            `json:"signals"`
	Version     int            `json:"version"`
	CreatedAt   string         `json:"createdAt"`
	UpdatedAt   string         `json:"updatedAt"`
	History     []any          `json:"history"`
}

func (v *recordValue) UnmarshalJSON(raw []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	// Match records-v1 and JavaScript's exact property names. Opaque capitalized
	// fields cannot replace content, identity, scope or the original proof.
	for _, field := range []struct {
		name  string
		value any
	}{
		{"id", &v.ID}, {"kind", &v.Kind}, {"title", &v.Title}, {"content", &v.Content},
		{"scope", &v.Scope}, {"workspaceId", &v.WorkspaceID}, {"sessionId", &v.SessionID},
		{"date", &v.Date}, {"state", &v.State}, {"data", &v.Data}, {"signals", &v.Signals},
		{"version", &v.Version}, {"createdAt", &v.CreatedAt}, {"updatedAt", &v.UpdatedAt}, {"history", &v.History},
	} {
		if value, ok := fields[field.name]; ok {
			if err := json.Unmarshal(value, field.value); err != nil {
				return err
			}
		}
	}
	return nil
}

type Evidence struct {
	Reference
	Text         string     `json:"text"`
	Source       string     `json:"source,omitempty"`
	Date         string     `json:"date,omitempty"`
	Parent       *Reference `json:"parent,omitempty"`
	ChangedLater bool       `json:"changedLater,omitempty"`
	Truncated    bool       `json:"truncated,omitempty"`
	Start        int        `json:"start"`
	End          int        `json:"end"`
}

var documentID = regexp.MustCompile(`^[A-Za-z0-9-]{1,75}$`)

// ErrDocumentExists is returned when a document, or one of its chunk ids, is
// already stored. Originals are append-only: a correction is a new document.
var ErrDocumentExists = errors.New("document id already exists; append a new document for a correction")

func Write(ctx context.Context, store *records.Store, req WriteRequest) (WriteResult, error) {
	if len(req.Text) == 0 || len(req.Text) > 100000 || !utf8.ValidString(req.Text) || len(req.Source) > 2048 {
		return WriteResult{}, errors.New("write requires valid UTF-8 text of 1–100000 bytes and a bounded source")
	}
	if err := prepareWrite(&req); err != nil {
		return WriteResult{}, err
	}
	at := time.Now().UTC()
	if req.At != "" {
		var err error
		at, err = time.Parse(time.RFC3339Nano, req.At)
		if err != nil {
			return WriteResult{}, errors.New("at must be RFC3339")
		}
	}
	upserts, result := documentRecords(req, at)
	return putDocument(ctx, store, upserts, result)
}

// prepareWrite applies the scope default, validates the scope and settles the
// document id, in the order every original write is checked.
func prepareWrite(req *WriteRequest) error {
	if req.Scope == "" {
		req.Scope = "global"
	}
	if err := validateScope(*req); err != nil {
		return err
	}
	if req.ID == "" {
		req.ID = uuid.NewString()
	}
	if !documentID.MatchString(req.ID) {
		return errors.New("invalid document id")
	}
	return nil
}

// putDocument stores a document and its chunks in one revision-checked put. It
// refuses to touch any id that already exists.
func putDocument(ctx context.Context, store *records.Store, upserts []records.Upsert, result WriteResult) (WriteResult, error) {
	ids := make([]string, 0, len(upserts))
	for _, upsert := range upserts {
		var value recordValue
		json.Unmarshal([]byte(upsert.RecordJSON), &value)
		ids = append(ids, value.ID)
	}
	snapshot, err := store.Get(ctx, records.GetRequest{IDs: ids})
	if err != nil {
		return WriteResult{}, err
	}
	if len(snapshot.Records) != 0 {
		return WriteResult{}, ErrDocumentExists
	}
	result.Revision, err = store.Put(ctx, records.PutRequest{ExpectedRevision: &snapshot.Revision, Upsert: upserts})
	return result, err
}

func validateScope(req WriteRequest) error {
	switch req.Scope {
	case "global", "daily":
		if req.WorkspaceID != "" || req.SessionID != "" {
			return errors.New("global/daily writes cannot carry workspace or session")
		}
	case "project":
		if !filepath.IsAbs(req.WorkspaceID) || req.SessionID != "" {
			return errors.New("project writes require an absolute workspace and no session")
		}
	case "session":
		if req.SessionID == "" || req.WorkspaceID != "" && !filepath.IsAbs(req.WorkspaceID) {
			return errors.New("session writes require a session and an optional absolute workspace")
		}
	default:
		return errors.New("unsupported scope")
	}
	return nil
}

func documentRecords(req WriteRequest, at time.Time) ([]records.Upsert, WriteResult) {
	var spans [][2]int
	for start := 0; start < len(req.Text); {
		end := runeEnd(req.Text, start, 600)
		spans = append(spans, [2]int{start, end})
		start = end
	}
	return buildDocument(req, at, req.Text, spans, nil)
}

// buildDocument makes the archived parent holding content and one active
// memory-record per span, a [start,end) byte range of content that verifyParent
// can check. extra is added to every chunk's data.
func buildDocument(req WriteRequest, at time.Time, content string, spans [][2]int, extra map[string]any) ([]records.Upsert, WriteResult) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	day := at.UTC().Format("2006-01-02")
	value := recordValue{ID: req.ID, Kind: "memory-document", Title: "Original document", Content: content, Scope: req.Scope, Date: &day, State: "active", Signals: 1, Version: 1, CreatedAt: now, UpdatedAt: now, History: []any{}, Data: map[string]any{"source": req.Source, "eventAt": at.Format(time.RFC3339Nano)}}
	if req.WorkspaceID != "" {
		value.WorkspaceID = &req.WorkspaceID
	}
	if req.SessionID != "" {
		value.SessionID = &req.SessionID
	}
	value.State = "archived"
	parent := encodeRecord(value, "")
	upserts := []records.Upsert{parent}
	result := WriteResult{Document: reference(req.ID, parent.RecordJSON), Records: []Reference{}}
	for ordinal, span := range spans {
		start, end := span[0], span[1]
		value.ID = fmt.Sprintf("%s-%05d", req.ID, ordinal)
		value.Kind, value.Title, value.Content = "memory-record", "Original record", content[start:end]
		value.State = "active"
		value.Data = map[string]any{"source": req.Source, "eventAt": at.Format(time.RFC3339Nano), "documentID": req.ID, "start": start, "end": end}
		for key, item := range extra {
			value.Data[key] = item
		}
		upsert := encodeRecord(value, value.Content)
		upserts = append(upserts, upsert)
		result.Records = append(result.Records, reference(value.ID, upsert.RecordJSON))
	}
	return upserts, result
}

func encodeRecord(value recordValue, text string) records.Upsert {
	return records.Upsert{RecordJSON: recordJSON(value), SearchText: text, VectorText: text}
}

func recordJSON(value recordValue) string {
	// records-v1 revisions use JSON.stringify bytes. Go's default HTML and
	// line-separator escaping would break the next SDK mutation's digest.
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value) // All values originate from the typed writer.
	raw := strings.TrimSuffix(buffer.String(), "\n")
	var out strings.Builder
	out.Grow(len(raw))
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			out.WriteByte(raw[i])
			continue
		}
		if strings.HasPrefix(raw[i:], `\u2028`) || strings.HasPrefix(raw[i:], `\u2029`) {
			separator := '\u2028'
			if raw[i+5] == '9' {
				separator = '\u2029'
			}
			out.WriteRune(separator)
			i += 5
			continue
		}
		// Copy escapes as pairs so a literal backslash-u2028 stays literal.
		out.WriteByte(raw[i])
		if i+1 < len(raw) {
			i++
			out.WriteByte(raw[i])
		}
	}
	return out.String()
}

func reference(id, raw string) Reference {
	h := sha256.Sum256([]byte(raw))
	return Reference{ID: id, Hash: hex.EncodeToString(h[:])}
}

func runeEnd(text string, start, count int) int {
	end := start
	for end < len(text) && count > 0 {
		_, width := utf8.DecodeRuneInString(text[end:])
		end += width
		count--
	}
	return end
}

func evidence(ctx context.Context, store *records.Store, revision string, scope records.Scope, ids []string) ([]Evidence, error) {
	if len(ids) > 200 {
		return nil, errors.New("evidence batch exceeds 200")
	}
	rawIDs, _ := json.Marshal(ids)
	allowed, err := store.Query(ctx, records.QueryRequest{Revision: revision, Scope: scope, Input: map[string]json.RawMessage{"ids": rawIDs, "recent": json.RawMessage(`false`)}})
	if err != nil {
		return nil, err
	}
	snapshot, err := store.Get(ctx, records.GetRequest{Revision: revision, IDs: allowed.IDs})
	if err != nil {
		return nil, err
	}
	out := []Evidence{}
	for _, raw := range snapshot.Records {
		var value recordValue
		if json.Unmarshal([]byte(raw), &value) != nil || value.Kind == "memory-document" || value.Kind == "memory-index" {
			continue
		}
		item := Evidence{Reference: reference(value.ID, raw), Text: value.Content, End: len(value.Content)}
		item.Source, _ = value.Data["source"].(string)
		item.Date, _ = value.Data["eventAt"].(string)
		if item.Date == "" && value.Date != nil {
			item.Date = *value.Date
		}
		if item.Date == "" {
			item.Date = value.CreatedAt
		}
		if value.Kind == "memory-record" {
			parent, err := verifyParent(ctx, store, revision, value)
			if err != nil {
				return nil, err
			}
			item.Parent = &parent
		}
		if strings.TrimSpace(item.Text) != "" {
			out = append(out, item)
		}
	}
	return out, nil
}

func verifyParent(ctx context.Context, store *records.Store, revision string, child recordValue) (Reference, error) {
	id, _ := child.Data["documentID"].(string)
	start, startOK := child.Data["start"].(float64)
	end, endOK := child.Data["end"].(float64)
	parent, err := store.Get(ctx, records.GetRequest{Revision: revision, IDs: []string{id}})
	if err != nil {
		return Reference{}, err
	}
	if len(parent.Records) == 1 && startOK && endOK && start >= 0 && end >= start && start == float64(int(start)) && end == float64(int(end)) {
		var value recordValue
		if json.Unmarshal([]byte(parent.Records[0]), &value) == nil && value.Kind == "memory-document" && int(end) <= len(value.Content) && value.Content[int(start):int(end)] == child.Content && value.Scope == child.Scope && sameString(value.WorkspaceID, child.WorkspaceID) && sameString(value.SessionID, child.SessionID) {
			return reference(value.ID, parent.Records[0]), nil
		}
	}
	return Reference{}, errors.New("memory record does not match its original document")
}

func sameString(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
