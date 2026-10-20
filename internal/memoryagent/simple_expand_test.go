package memoryagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"strings"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/records"
)

// expandNote is a dated note with fixed bytes: its eventAt day is its date.
func expandNote(id, text, eventAt string, data map[string]any) records.Upsert {
	day := eventAt[:10]
	values := map[string]any{"eventAt": eventAt}
	maps.Copy(values, data)
	v := recordValue{ID: id, Kind: "note", Title: "Note", Content: text, Scope: "global", Date: &day, State: "active", Version: 1, Signals: 1, CreatedAt: "2026-10-19T00:00:00Z", UpdatedAt: "2026-10-19T00:00:00Z", History: []any{}, Data: values}
	return encodeRecord(v, text)
}

// expandDocument is an archived original document and one memory-record per
// chunk, as buildDocument writes them, with fixed bytes.
func expandDocument(id, eventAt string, chunks []string) []records.Upsert {
	day := eventAt[:10]
	content := strings.Join(chunks, "\n\n")
	parent := recordValue{ID: id, Kind: "memory-document", Title: "Original document", Content: content, Scope: "global", Date: &day, State: "archived", Signals: 1, Version: 1, CreatedAt: "2026-10-19T00:00:00Z", UpdatedAt: "2026-10-19T00:00:00Z", History: []any{}, Data: map[string]any{"source": "test", "eventAt": eventAt}}
	upserts := []records.Upsert{encodeRecord(parent, "")}
	offset := 0
	for i, chunk := range chunks {
		child := parent
		child.ID, child.Kind, child.Title, child.Content, child.State = id+"-0000"+string(rune('0'+i)), "memory-record", "Original record", chunk, "active"
		child.Data = map[string]any{"source": "test", "eventAt": eventAt, "documentID": id, "start": offset, "end": offset + len(chunk)}
		upserts = append(upserts, encodeRecord(child, chunk))
		offset += len(chunk) + len("\n\n")
	}
	return upserts
}

func expandLink(id, kind string, weight float64) map[string]any {
	return map[string]any{"id": id, "version": 1, "type": kind, "weight": weight}
}

// expandFixture stores two dated sessions of notes and a document written on
// the first session's day, in that order, with a writer link, a writer
// correction and overlay links. Searches for the codename find a2 and its
// session's talk of the codename; the launch notes share no word with them.
//
//	session A (2023-05-08): a0 a1 a2 a3 a4    a1 → b0 semantic (writer)
//	session B (2023-05-09): b0 b1 b2           b2 supersedes b0 (writer)
//	document doc (2023-05-08): doc-00000 doc-00001 doc-00002
//	overlay: a2 → b1 temporal 0.8, a2 → doc-00001 causal 0.6
func expandFixture(t *testing.T) *records.Store {
	t.Helper()
	s, _ := agentStore(t)
	a, b := "2023-05-08T13:56:00Z", "2023-05-09T10:00:00Z"
	upserts := []records.Upsert{
		expandNote("a0", "Caroline: good morning, how was your weekend", a, nil),
		expandNote("a1", "Melanie: we picked a codename for the rocket project", a, map[string]any{"links": []any{expandLink("b0", "semantic", 0.5)}}),
		expandNote("a2", "Caroline: the codename is ORBIT", a, nil),
		expandNote("a3", "Melanie: launch window opens Friday at dawn", a, nil),
		expandNote("a4", "Caroline: sounds great, see you there", a, nil),
		expandNote("b0", "Melanie: rocket budget review went fine", b, nil),
		expandNote("b1", "Caroline: launch moved to Monday", b, nil),
		expandNote("b2", "Melanie: rocket budget review was redone", b, map[string]any{"mnemonSupersedes": map[string]any{"id": "b0", "version": 1}}),
	}
	upserts = append(upserts, expandDocument("doc", "2023-05-08T18:00:00Z", []string{"Notes: codename review meeting", "Notes: launch checklist signed", "Notes: wrap up and travel"})...)
	if _, err := s.Put(context.Background(), records.PutRequest{Upsert: upserts}); err != nil {
		t.Fatal(err)
	}
	overlay := map[string]records.DerivedMetadata{"a2": {Version: 1, Links: []records.Link{{ID: "b1", Version: 1, Type: "temporal", Weight: 0.8}, {ID: "doc-00001", Version: 1, Type: "causal", Weight: 0.6}}}}
	if err := s.ReplaceDerivedMetadata(context.Background(), "consolidation", overlay); err != nil {
		t.Fatal(err)
	}
	return s
}

// expandJudge calls needed what names ORBIT or a launch, and no longer
// current what was redone.
func expandJudge() *scriptedJudge {
	return &scriptedJudge{
		answer: func(text string, _ int) float64 {
			if strings.Contains(text, "ORBIT") || strings.Contains(text, "launch") {
				return 0.9
			}
			return 0.2
		},
		superseded: func(text string) float64 {
			if strings.Contains(text, "redone") {
				return 0.7
			}
			return 0
		},
	}
}

func expandPlanner() *limitPlanner {
	return replies("codename project\nNOTE: the codename is ORBIT", "NEED: the codename\nNEED: the launch day", "codename rocket")
}

// pinnedView is a View's JSON with its clock readings zeroed.
func pinnedView(t *testing.T, view View) string {
	t.Helper()
	view.Stats.QueryMS, view.Stats.ModelMS = 0, 0
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Without view.expand a read is what it was before evidence expansion
// existed: these hashes were taken at 1f0c9a26, before any expansion code.
func TestReadSimpleWithoutExpandUnchanged(t *testing.T) {
	s := expandFixture(t)
	for name, tc := range map[string]struct {
		config Config
		want   string
	}{
		"judged": {Config{}, "3152098fef8497187f4ece79e44f4ce162672daebe33d6c133a10fdd89a499f7"},
		"fusion": {Config{View: &ViewConfig{MaxItems: 8, Fusion: "rrf"}}, "e56741bc64f56a5a5cc56e0b5a34ebaf76145b69690cdb0de069b6a4bd5120a9"},
		"index":  {Config{Index: &records.IndexPolicy{Version: 1, Lexical: true, Relations: true, Supersedes: true}, View: &ViewConfig{Intent: "question"}}, "c5f600208ddd868356ffc40dd14b54a0b49271ee288d7ce1824a730a361399c3"},
	} {
		config := tc.config
		config.Version, config.Strategy = 1, "simple"
		a, err := New(s, config, Models{Planner: expandPlanner(), Judge: expandJudge()}, nil)
		if err != nil {
			t.Fatal(err)
		}
		view, err := a.Read(context.Background(), ReadRequest{Question: "What is the codename and when is the launch?"})
		if err != nil {
			t.Fatal(name, err)
		}
		if got := pinnedView(t, view); got != tc.want {
			raw, _ := json.Marshal(view)
			t.Errorf("%s: %s\n%s", name, got, raw)
		}
	}
}
