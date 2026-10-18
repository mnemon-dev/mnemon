package memoryagent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/records"
)

func agentStore(t *testing.T) (*records.Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := records.Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, dir
}

func TestCorruptDerivedDatabaseKeepsOriginalsReadable(t *testing.T) {
	s, dir := agentStore(t)
	if _, err := Write(context.Background(), s, WriteRequest{Text: "Redis raw evidence"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "memory-index.db"), []byte("corrupt derived data"), 0600); err != nil {
		t.Fatal(err)
	}
	config := Config{Version: 1, Policy: &ReadPolicy{Consolidation: true}, Judge: &ModelConfig{Kind: "local", Model: "not-called", BaseURL: "http://127.0.0.1:1/v1"}}
	a, err := Open(s, dir, config, false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	view, err := a.Read(context.Background(), ReadRequest{Question: "Redis"})
	if err != nil || !view.IndexStale || len(view.Records) != 1 || view.Stats.DecisionCalls != 0 {
		t.Fatal("corrupt optional index blocked original evidence", view, err)
	}
	if _, err := a.Consolidate(context.Background(), ConsolidateRequest{Reset: true}); err == nil {
		t.Fatal("unavailable derived database reached consolidation")
	}
}

func TestOriginalWriteChunkProofAndCAS(t *testing.T) {
	s, _ := agentStore(t)
	ctx := context.Background()
	text := " \n" + strings.Repeat("原文😀\t", 400) + "\r\nEND  "
	result, err := Write(ctx, s, WriteRequest{ID: "original", Text: text, Source: "source/file", At: "2020-01-01T12:00:00+08:00"})
	if err != nil || len(result.Records) < 2 {
		t.Fatal(result, err)
	}
	ids := []string{}
	for _, ref := range result.Records {
		ids = append(ids, ref.ID)
	}
	items, err := evidence(ctx, s, result.Revision, records.Scope{}, ids)
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for i, item := range items {
		joined += item.Text
		if item.Reference != result.Records[i] || item.Parent == nil || *item.Parent != result.Document {
			t.Fatal("lost original proof", item)
		}
	}
	if joined != text {
		t.Fatal("original whitespace/UTF-8 bytes changed")
	}
	if _, err = Write(ctx, s, WriteRequest{ID: "original", Text: "replacement"}); err == nil {
		t.Fatal("append-only document overwritten")
	}
	// Existing chunk IDs also prevent an otherwise-new document overwriting data.
	seedNote(t, s, "collision-00000", "unrelated old record")
	if _, err = Write(ctx, s, WriteRequest{ID: "collision", Text: "replacement"}); err == nil {
		t.Fatal("existing chunk overwritten")
	}
	var successes atomic.Int32
	var work sync.WaitGroup
	for i := 0; i < 2; i++ {
		work.Add(1)
		go func() {
			defer work.Done()
			if _, err := Write(ctx, s, WriteRequest{ID: "race", Text: "one committed document"}); err == nil {
				successes.Add(1)
			}
		}()
	}
	work.Wait()
	if successes.Load() != 1 {
		t.Fatal("CAS did not admit exactly one writer", successes.Load())
	}
	// Tampering with a child cannot become valid original evidence.
	bad := recordValue{ID: ids[0], Kind: "memory-record", Scope: "global", State: "active", CreatedAt: "2026-10-19T00:00:00Z", Content: "forged", Data: map[string]any{"documentID": "original", "start": 0, "end": 6}}
	if _, err = s.Put(ctx, records.PutRequest{Upsert: []records.Upsert{encodeRecord(bad, bad.Content)}}); err != nil {
		t.Fatal(err)
	}
	if _, err = evidence(ctx, s, "", records.Scope{}, ids[:1]); err == nil {
		t.Fatal("forged block admitted")
	}
	if historical, err := evidence(ctx, s, result.Revision, records.Scope{}, ids[:1]); err != nil || historical[0].Text != items[0].Text {
		t.Fatal("old snapshot lost original", historical, err)
	}
}

type completeFunc func(context.Context, string, string) (string, Usage, error)

func (f completeFunc) Complete(ctx context.Context, system, prompt string) (string, Usage, error) {
	return f(ctx, system, prompt)
}

type decisionFunc func(context.Context, any, map[string]Question) (map[string]float64, Usage, error)

func (f decisionFunc) Decide(ctx context.Context, state any, questions map[string]Question) (map[string]float64, Usage, error) {
	return f(ctx, state, questions)
}

func fixtureJudge(_ context.Context, state any, questions map[string]Question) (map[string]float64, Usage, error) {
	// The model port is scripted; semantic quality is measured on datasets, not
	// asserted by this fake. It exercises alias binding and loop state only.
	var data struct {
		Candidates []struct{ Text string }
		Items      []struct{ Text string }
	}
	raw, _ := json.Marshal(state)
	json.Unmarshal(raw, &data)
	out := map[string]float64{}
	for key := range questions {
		var i, k int
		switch {
		case strings.HasPrefix(key, "s"):
			out[key] = 0
		case strings.HasPrefix(key, "i"):
			out[key] = 1
		case strings.HasPrefix(key, "n"):
			fmt.Sscanf(key, "n%d", &i)
			out[key] = 0.1
			if i < len(data.Candidates) && strings.Contains(data.Candidates[i].Text, "ORBIT") {
				out[key] = 0.9
			}
		default:
			fmt.Sscanf(key, "c%dq%d", &i, &k)
			out[key] = 0.1
			if i < len(data.Candidates) && strings.Contains(data.Candidates[i].Text, "ORBIT") {
				out[key] = 0.9
			}
		}
	}
	return out, Usage{}, nil
}

func seedNote(t *testing.T, s *records.Store, id, text string) Reference {
	t.Helper()
	v := recordValue{ID: id, Kind: "note", Title: "Note", Content: text, Scope: "global", State: "active", Version: 1, Signals: 1, CreatedAt: "2026-10-19T00:00:00Z", UpdatedAt: "2026-10-19T00:00:00Z", History: []any{}, Data: map[string]any{}}
	upsert := encodeRecord(v, text)
	if _, err := s.Put(context.Background(), records.PutRequest{Upsert: []records.Upsert{upsert}}); err != nil {
		t.Fatal(err)
	}
	return reference(id, upsert.RecordJSON)
}

func TestReadRewritesAfterEmptyInitialSearch(t *testing.T) {
	s, _ := agentStore(t)
	ref := seedNote(t, s, "answer", "codename ORBIT")
	var rewrites atomic.Int32
	planner := completeFunc(func(_ context.Context, system, prompt string) (string, Usage, error) {
		if strings.Contains(system, "Name at most") {
			return "NEED: secret code", Usage{}, nil
		}
		if strings.Contains(system, "missing need") {
			rewrites.Add(1)
			if !strings.Contains(prompt, "searchesAlreadyRun") {
				t.Error("rewrite forgot previous searches")
			}
			return "codename", Usage{}, nil
		}
		return "nonexistent-xyz\nNOTE: nonexistent-abc", Usage{}, nil
	})
	policy := &ReadPolicy{Planning: true, Screening: true, Judging: true, Coverage: true, Loop: true}
	a, err := New(s, Config{Version: 1, Policy: policy}, Models{Planner: planner, Judge: decisionFunc(fixtureJudge)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Mutating a caller's configuration cannot change the active composition.
	policy.Loop = false
	view, err := a.Read(context.Background(), ReadRequest{Question: "What is the password?"})
	if err != nil || len(view.Records) != 1 || view.Records[0].Reference != ref || !view.Needs[0].Covered || view.StopReason != "covered" || rewrites.Load() != 1 {
		t.Fatal(view, err, rewrites.Load())
	}
	if view.Stats.Searches > 12 || view.Stats.DecisionCalls > 24 || view.Stats.PlannerCalls > 6 {
		t.Fatal("work exceeded budgets", view.Stats)
	}
}

func TestReadAblationsAndInvalidJudgements(t *testing.T) {
	s, _ := agentStore(t)
	seedNote(t, s, "answer", "codename ORBIT")
	var gotKeys []string
	judge := decisionFunc(func(ctx context.Context, state any, questions map[string]Question) (map[string]float64, Usage, error) {
		for key := range questions {
			gotKeys = append(gotKeys, key)
		}
		return fixtureJudge(ctx, state, questions)
	})
	a, err := New(s, Config{Version: 1, Policy: &ReadPolicy{Coverage: true}}, Models{Judge: judge}, nil)
	if err != nil {
		t.Fatal(err)
	}
	view, err := a.Read(context.Background(), ReadRequest{Question: "codename"})
	if err != nil || !view.Needs[0].Covered || slices.Contains(gotKeys, "n0") || slices.Contains(gotKeys, "s0") {
		t.Fatal("judging ablation still asks needed/superseded", view, gotKeys, err)
	}
	a, _ = New(s, Config{Version: 1, Policy: &ReadPolicy{Judging: true}}, Models{Judge: judge}, nil)
	view, err = a.Read(context.Background(), ReadRequest{Question: "codename"})
	if err != nil || view.Needs[0].Covered || view.Needs[0].Evaluated {
		t.Fatal("disabled coverage reported success", view, err)
	}
	invalid := decisionFunc(func(context.Context, any, map[string]Question) (map[string]float64, Usage, error) {
		return map[string]float64{}, Usage{}, nil
	})
	a, _ = New(s, Config{Version: 1, Policy: &ReadPolicy{Judging: true}}, Models{Judge: invalid}, nil)
	if _, err = a.Read(context.Background(), ReadRequest{Question: "codename"}); err == nil {
		t.Fatal("missing model answers admitted")
	}
}

func TestConsolidationRetrievesLinkedOriginalAndRetiresStaleHints(t *testing.T) {
	s, dir := agentStore(t)
	ref := seedNote(t, s, "answer", "codename ORBIT")
	overlay, err := OpenOverlay(filepath.Join(dir, "memory-index.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer overlay.Close()
	planner := completeFunc(func(context.Context, string, string) (string, Usage, error) {
		return `{"items":[{"kind":"value","title":"password","text":"The password is ORBIT","records":["r0"]}]}`, Usage{}, nil
	})
	a, err := New(s, Config{Version: 1, Policy: &ReadPolicy{Judging: true, Coverage: true, Consolidation: true}}, Models{Planner: planner, Judge: decisionFunc(fixtureJudge)}, overlay)
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.Consolidate(context.Background(), ConsolidateRequest{})
	if err != nil || result.Visited != 1 || result.Added != 1 || result.Remaining != 0 {
		t.Fatal(result, err)
	}
	view, err := a.Read(context.Background(), ReadRequest{Question: "password"})
	if err != nil || len(view.Records) != 1 || view.Records[0].Reference != ref || len(view.Index) != 1 {
		t.Fatal("index did not lead back to raw record", view, err)
	}
	seedNote(t, s, "answer", "Updated value no longer contains old evidence")
	view, err = a.Read(context.Background(), ReadRequest{Question: "password"})
	if err != nil || len(view.Records) != 0 || len(view.Index) != 0 || !view.IndexStale {
		t.Fatal("stale overlay was used as evidence", view, err)
	}
	if _, err = readConsolidation(`{"items":[{"kind":"topic","title":"x","text":"y","records":["r200"]}]}`, []Evidence{{Reference: ref}}); err == nil {
		t.Fatal("foreign consolidation reference accepted")
	}
	state, token, err := overlay.load(context.Background(), records.Scope{})
	if err != nil {
		t.Fatal(err)
	}
	state.Revision = "changed"
	if err = overlay.save(context.Background(), records.Scope{}, token, state); err != nil {
		t.Fatal(err)
	}
	if err = overlay.save(context.Background(), records.Scope{}, token, state); err == nil {
		t.Fatal("stale consolidation publication accepted")
	}
}

func TestIndexHintsRequireSelectedCurrentOriginals(t *testing.T) {
	s, _ := agentStore(t)
	ref := seedNote(t, s, "answer", "codename ORBIT")
	var calls int
	judge := decisionFunc(func(_ context.Context, state any, questions map[string]Question) (map[string]float64, Usage, error) {
		calls++
		raw, _ := json.Marshal(state)
		if !strings.Contains(string(raw), `"originals"`) || !strings.Contains(string(raw), "codename ORBIT") || len(questions) != 1 {
			t.Fatal("index judgment lacks its selected original", string(raw), questions)
		}
		return map[string]float64{"i0": 1}, Usage{}, nil
	})
	a, _ := New(s, Config{Version: 1, Policy: &ReadPolicy{Consolidation: true}}, Models{Judge: judge}, nil)
	r := readRun{agent: a, items: map[string]*candidate{ref.ID: {judged: true, needed: 1}}, indexItems: []IndexItem{{ID: "index", Text: "ORBIT", Records: []Reference{ref}}, {ID: "missing", Records: []Reference{{ID: "not-selected"}}}}}
	selected := map[string]Evidence{ref.ID: {Reference: ref, Text: "codename ORBIT"}}
	items, err := r.selectedIndex(context.Background(), selected)
	if err != nil || len(items) != 1 || calls != 1 {
		t.Fatal(items, err, calls)
	}
	r.items[ref.ID].superseded = 1
	items, err = r.selectedIndex(context.Background(), selected)
	if err != nil || len(items) != 0 || calls != 1 {
		t.Fatal("stale hint reached a model or View", items, err, calls)
	}
	if len(retainedIndex([]IndexItem{{Records: []Reference{ref}}}, []Evidence{{Reference: ref, Truncated: true}})) != 0 {
		t.Fatal("truncated original supports an index claim")
	}
}

func TestNeededHistoricalEvidenceIsMarkedInsteadOfDiscarded(t *testing.T) {
	s, _ := agentStore(t)
	ref := seedNote(t, s, "old", "The old codename was ORBIT")
	needed := true
	judge := decisionFunc(func(_ context.Context, _ any, questions map[string]Question) (map[string]float64, Usage, error) {
		answers := map[string]float64{}
		for key := range questions {
			answers[key] = 0
			if key == "s0" || needed {
				answers[key] = 1
			}
		}
		return answers, Usage{}, nil
	})
	a, err := New(s, Config{Version: 1, Policy: &ReadPolicy{Judging: true, Coverage: true}}, Models{Judge: judge}, nil)
	if err != nil {
		t.Fatal(err)
	}
	view, err := a.Read(context.Background(), ReadRequest{Question: "What was the old codename?"})
	if err != nil || len(view.Records) != 1 || view.Records[0].Reference != ref || !view.Records[0].ChangedLater || !view.Needs[0].HasEvidence || view.Records[0].Text != "The old codename was ORBIT" {
		t.Fatal("needed history was lost or its original text changed", view, err)
	}
	needed = false
	view, err = a.Read(context.Background(), ReadRequest{Question: "What is the current codename?"})
	if err != nil || len(view.Records) != 0 || view.Needs[0].HasEvidence {
		t.Fatal("unneeded old value became a current witness", view, err)
	}
}

func TestConsolidationRejectsMissingArrayAndExplainsInvalidFields(t *testing.T) {
	for _, text := range []string{`{}`, `{"items":null}`} {
		if _, err := readConsolidation(text, nil); err == nil {
			t.Fatal("missing items became an empty successful batch", text)
		}
	}
	if values, err := readConsolidation(`{"items":[]}`, nil); err != nil || len(values) != 0 {
		t.Fatal("explicit empty result was rejected", values, err)
	}
	if err := validateIndexItem("preference", "x", "y", []string{"r0"}); err == nil || !strings.Contains(err.Error(), "kind") {
		t.Fatal("invalid kind lacked a useful diagnostic", err)
	}
	if err := validateIndexItem("topic", "x", "y", nil); err == nil || !strings.Contains(err.Error(), "records") {
		t.Fatal("missing reference aliases were accepted", err)
	}
}

func TestConsolidationBuilderNeedsOnlyACompletionModel(t *testing.T) {
	s, dir := agentStore(t)
	seedNote(t, s, "answer", "codename ORBIT")
	overlay, err := OpenOverlay(filepath.Join(dir, "memory-index.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer overlay.Close()
	planner := completeFunc(func(context.Context, string, string) (string, Usage, error) {
		return `{"items":[{"kind":"value","title":"codename","text":"ORBIT","records":["r0"]}]}`, Usage{}, nil
	})
	a, err := New(s, Config{Version: 1, Policy: &ReadPolicy{Consolidation: true}}, Models{Planner: planner}, overlay)
	if err != nil {
		t.Fatal("builder unexpectedly requires a decision model", err)
	}
	result, err := a.Consolidate(context.Background(), ConsolidateRequest{})
	if err != nil || result.Visited != 1 || result.Added != 1 {
		t.Fatal(result, err)
	}
	if _, err := a.Read(context.Background(), ReadRequest{Question: "codename"}); err == nil || !strings.Contains(err.Error(), "decision model") {
		t.Fatal("read accepted a builder-only model configuration", err)
	}
}

func TestConsolidationResumesVectorsWithoutRepeatingModelWork(t *testing.T) {
	s, dir := agentStore(t)
	seedNote(t, s, "answer", "codename ORBIT")
	overlay, err := OpenOverlay(filepath.Join(dir, "memory-index.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer overlay.Close()
	var ready atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var request struct{ Input []string }
		json.NewDecoder(r.Body).Decode(&request)
		vectors := [][]float64{}
		for range request.Input {
			vectors = append(vectors, []float64{1, 0})
		}
		json.NewEncoder(w).Encode(map[string]any{"embeddings": vectors})
	}))
	defer server.Close()
	var calls atomic.Int32
	planner := completeFunc(func(context.Context, string, string) (string, Usage, error) {
		calls.Add(1)
		return `{"items":[{"kind":"value","title":"password","text":"ORBIT","records":["r0"]}]}`, Usage{}, nil
	})
	config := Config{Version: 1, Policy: &ReadPolicy{Consolidation: true}, Embedding: &records.EmbeddingConfig{URL: server.URL, Model: "unit"}}
	models := Models{Planner: planner, Judge: decisionFunc(fixtureJudge)}
	a, err := New(s, config, models, overlay)
	if err != nil {
		t.Fatal(err)
	}
	first, err := a.Consolidate(context.Background(), ConsolidateRequest{})
	if err != nil || first.EmbeddingReady || first.Remaining != 0 || first.Added != 1 {
		t.Fatal("failed vectors discarded a valid lexical index", first, err)
	}
	ready.Store(true)
	second, err := a.Consolidate(context.Background(), ConsolidateRequest{})
	if err != nil || !second.EmbeddingReady || second.Visited != 0 || calls.Load() != 1 {
		t.Fatal("vector recovery repeated completion work", second, err, calls.Load())
	}
	state, _, err := overlay.load(context.Background(), records.Scope{})
	if err != nil || len(state.Vectors) != 1 {
		t.Fatal("recovered vectors were not durable", state, err)
	}
	ro, err := records.Open(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	readonly, err := New(ro, config, models, overlay)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readonly.Consolidate(context.Background(), ConsolidateRequest{}); err == nil || calls.Load() != 1 {
		t.Fatal("readonly consolidation reached the model", err, calls.Load())
	}
}
