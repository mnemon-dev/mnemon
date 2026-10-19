package memoryagent

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
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

func relationAgent(t *testing.T, cfg *RelationConfig, policy *ReadPolicy, judge Decider) (*Agent, string) {
	t.Helper()
	s, dir := agentStore(t)
	a, err := New(s, Config{Version: 1, Policy: policy, Relations: cfg}, Models{Judge: judge}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a.relations, err = openRelations(filepath.Join(dir, "memory-relations.db"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a, dir
}

func writeTurn(t *testing.T, a *Agent, id, text, thread string, position int64) WriteResult {
	t.Helper()
	req := WriteRequest{ID: id, Text: text, At: "2020-01-01T00:00:00Z"}
	if thread != "" {
		req.Thread, req.Position = thread, &position
	}
	result, err := a.Write(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func zeroDecisions(questions map[string]Question) map[string]float64 {
	out := map[string]float64{}
	for key := range questions {
		out[key] = 0
	}
	return out
}

func TestRelationScoringDirectionReplayAndRawIdentity(t *testing.T) {
	ctx := context.Background()
	calls := 0
	judge := decisionFunc(func(_ context.Context, state any, questions map[string]Question) (map[string]float64, Usage, error) {
		calls++
		raw, _ := json.Marshal(state)
		if !strings.Contains(string(raw), "storage saturation") || !strings.Contains(string(raw), "outage due") {
			t.Error("model did not see original pair", string(raw))
		}
		answers := zeroDecisions(questions)
		answers["p0_entity"], answers["p0_semantic"], answers["p0_caused_by"] = .8, .9, .95
		return answers, Usage{InputTokens: 100}, nil
	})
	a, _ := relationAgent(t, &RelationConfig{Entity: true, Semantic: true, Causal: true}, &ReadPolicy{}, judge)
	cause := writeTurn(t, a, "cause", "storage saturation", "", 0)
	effect := writeTurn(t, a, "effect", "outage due to saturation", "", 0)
	before, _ := a.store.Get(ctx, records.GetRequest{})
	result, err := a.Relate(ctx, RelateRequest{IDs: []string{effect.Records[0].ID}})
	if err != nil || result.Edges != 3 || result.Visited != 1 || result.Remaining != 1 || result.SemanticReady {
		t.Fatal(result, err)
	}
	edges, err := a.relations.neighbors(ctx, a.relationProfile(records.Scope{}), []string{effect.Records[0].ID})
	if err != nil || len(edges) != 3 {
		t.Fatal(edges, err)
	}
	for _, e := range edges {
		if e.Type == "causal" && (e.Source.ID != cause.Records[0].ID || e.Target.ID != effect.Records[0].ID) {
			t.Fatal("causal direction changed", e)
		}
	}
	if _, err = a.Relate(ctx, RelateRequest{IDs: []string{effect.Records[0].ID}}); err != nil || calls != 1 {
		t.Fatal("replayed completed model call", calls, err)
	}
	after, _ := a.store.Get(ctx, records.GetRequest{})
	if before.Revision != after.Revision || !slices.Equal(before.Records, after.Records) {
		t.Fatal("building changed raw JSON or revision")
	}
	view, err := a.Read(ctx, ReadRequest{Question: "outage", Trace: true})
	if err != nil || !slices.ContainsFunc(view.Records, func(e Evidence) bool { return e.Reference == cause.Records[0] }) || len(view.Trace.Edges) != 3 {
		t.Fatal("graph did not recover lexically absent cause", view, err)
	}
}

func TestRelationDecisionsFailClosed(t *testing.T) {
	for _, bad := range []string{"missing", "extra", "nan", "range", "failure"} {
		t.Run(bad, func(t *testing.T) {
			judge := decisionFunc(func(_ context.Context, _ any, q map[string]Question) (map[string]float64, Usage, error) {
				v := zeroDecisions(q)
				switch bad {
				case "missing":
					delete(v, "p0_semantic")
				case "extra":
					v["p0_causes"] = 1
				case "nan":
					v["p0_semantic"] = math.NaN()
				case "range":
					v["p0_semantic"] = 1.1
				case "failure":
					return nil, Usage{}, fmt.Errorf("decision unavailable")
				}
				return v, Usage{}, nil
			})
			a, _ := relationAgent(t, &RelationConfig{Semantic: true}, &ReadPolicy{}, judge)
			writeTurn(t, a, "one", "release alpha", "", 0)
			two := writeTurn(t, a, "two", "release beta", "", 0)
			if _, err := a.Relate(context.Background(), RelateRequest{IDs: []string{two.Records[0].ID}}); err == nil {
				t.Fatal("bad decision admitted")
			}
			versions, err := a.relations.versions(context.Background(), a.relationProfile(records.Scope{}))
			if err != nil || len(versions) != 0 {
				t.Fatal("failed decision advanced checkpoint", versions, err)
			}
		})
	}
}

func TestWriteRelationFailureReportsCommittedOriginals(t *testing.T) {
	a, _ := relationAgent(t, &RelationConfig{Semantic: true, OnWrite: true}, &ReadPolicy{}, decisionFunc(func(context.Context, any, map[string]Question) (map[string]float64, Usage, error) {
		return nil, Usage{}, fmt.Errorf("decision unavailable")
	}))
	first := writeTurn(t, a, "one", "release alpha", "", 0)
	if first.Relations == nil || first.Relations.Error != "" {
		t.Fatal(first)
	}
	second := writeTurn(t, a, "two", "release beta", "", 0)
	if second.Relations == nil || second.Relations.Error == "" {
		t.Fatal("index failure concealed", second)
	}
	values, err := evidence(context.Background(), a.store, second.Revision, records.Scope{}, []string{second.Records[0].ID})
	if err != nil || len(values) != 1 || values[0].Reference != second.Records[0] || values[0].Text != "release beta" {
		t.Fatal("index failure lost committed original", values, err)
	}
}

func TestAdjacentRelationsUseOriginalOrderAndRespectBoundaries(t *testing.T) {
	a, _ := relationAgent(t, &RelationConfig{Adjacent: true, OnWrite: true}, &ReadPolicy{}, nil)
	last := writeTurn(t, a, "last", "answer final", "session-a", 2)
	first := writeTurn(t, a, "first", "question anchor", "session-a", 0)
	middle := writeTurn(t, a, "middle", "clarification", "session-a", 1)
	other := writeTurn(t, a, "other", "secret unrelated", "session-b", 1)
	snapshot, err := a.relationSnapshot(context.Background(), "", records.Scope{}, true)
	if err != nil {
		t.Fatal(err)
	}
	at := snapshot.byID[first.Records[0].ID]
	adj := snapshot.adjacent(at)
	if len(adj) != 2 || snapshot.docs[adj[0]].ID != middle.Records[0].ID || snapshot.docs[adj[1]].ID != last.Records[0].ID {
		t.Fatal("used import order instead of original order", adj)
	}
	view, err := a.Read(context.Background(), ReadRequest{Question: "anchor", Trace: true})
	if err != nil || !slices.ContainsFunc(view.Records, func(e Evidence) bool { return e.ID == last.Records[0].ID }) || slices.ContainsFunc(view.Records, func(e Evidence) bool { return e.ID == other.Records[0].ID }) {
		t.Fatal("broken structural context", view, err)
	}
	pos := int64(-1)
	if _, err := a.Write(context.Background(), WriteRequest{Text: "x", Thread: "bad", Position: &pos}); err == nil {
		t.Fatal("invalid structural position admitted")
	}
}

type contextFixtureCandidate struct {
	ID, Text string
	Context  []struct{ ID, Text, Position string }
}

func contextFixtureJudge(_ context.Context, state any, questions map[string]Question) (map[string]float64, Usage, error) {
	raw, _ := json.Marshal(state)
	var parsed struct{ Candidates []contextFixtureCandidate }
	json.Unmarshal(raw, &parsed)
	answers := zeroDecisions(questions)
	for i, c := range parsed.Candidates {
		if !strings.Contains(c.Text, "mix of drama and romance") {
			continue
		}
		found := false
		for _, x := range c.Context {
			found = found || strings.Contains(x.Text, "first full screenplay") && x.Position == "before"
		}
		if !found {
			continue
		}
		for _, key := range []string{fmt.Sprintf("n%d", i), fmt.Sprintf("c%dq0", i)} {
			if _, ok := questions[key]; ok {
				answers[key] = .99
			}
		}
		for j, x := range c.Context {
			key := fmt.Sprintf("x%dq%d", i, j)
			if _, ok := questions[key]; ok && strings.Contains(x.Text, "first full screenplay") {
				answers[key] = .98
			}
		}
	}
	return answers, Usage{}, nil
}

func TestContextualJudgmentKeepsAnswerAndItsAntecedent(t *testing.T) {
	a, _ := relationAgent(t, &RelationConfig{Adjacent: true, OnWrite: true}, &ReadPolicy{Judging: true, Coverage: true}, decisionFunc(contextFixtureJudge))
	first := writeTurn(t, a, "first", "Joanna: I finally finished my first full screenplay.", "conversation", 0)
	writeTurn(t, a, "question", "Nate: What is it about?", "conversation", 1)
	answer := writeTurn(t, a, "answer", "Joanna: Thanks Nate! It's a mix of drama and romance!", "conversation", 2)
	for i := 0; i < 60; i++ {
		writeTurn(t, a, fmt.Sprintf("distractor-%d", i), "Joanna screenplay sequel was submitted to a contest.", "", 0)
	}
	question := "first full screenplay genre"
	baseline, err := New(a.store, Config{Version: 1, Policy: &ReadPolicy{Judging: true, Coverage: true}}, Models{Judge: decisionFunc(contextFixtureJudge)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	before, err := baseline.Read(context.Background(), ReadRequest{Question: question})
	if err != nil || slices.ContainsFunc(before.Records, func(e Evidence) bool { return e.ID == answer.Records[0].ID }) {
		t.Fatal("fixture must distinguish contextual read", before, err)
	}
	view, err := a.Read(context.Background(), ReadRequest{Question: question, Trace: true})
	if err != nil || !slices.ContainsFunc(view.Records, func(e Evidence) bool { return e.Reference == answer.Records[0] }) || !slices.ContainsFunc(view.Records, func(e Evidence) bool { return e.Reference == first.Records[0] }) || !view.Needs[0].Covered || view.Relations.ContextSelected == 0 {
		t.Fatal("answer or its literal antecedent was lost", view, err)
	}
	if len(view.Records) > 16 || view.Stats.DecisionCalls > 24 || view.Trace == nil {
		t.Fatal("unbounded read", view)
	}
	if !slices.ContainsFunc(view.Trace.Candidates, func(c CandidateTrace) bool {
		return c.ID == answer.Records[0].ID && c.Selected && slices.ContainsFunc(c.Context, func(x ContextTrace) bool { return x.ID == first.Records[0].ID && x.Selected && x.Score > .5 })
	}) {
		t.Fatal("missing composition trace", view.Trace)
	}
}

func TestStaleGraphCannotCrossVersionsOrScope(t *testing.T) {
	ctx := context.Background()
	judge := decisionFunc(func(_ context.Context, _ any, q map[string]Question) (map[string]float64, Usage, error) {
		v := zeroDecisions(q)
		for k := range v {
			v[k] = .9
		}
		return v, Usage{}, nil
	})
	a, _ := relationAgent(t, &RelationConfig{Semantic: true}, &ReadPolicy{}, judge)
	target := writeTurn(t, a, "target", "storage saturation", "", 0)
	source := writeTurn(t, a, "source", "outage saturation", "", 0)
	oldRevision, _ := a.store.Get(ctx, records.GetRequest{IDs: []string{}})
	if _, err := a.Relate(ctx, RelateRequest{IDs: []string{source.Records[0].ID}}); err != nil {
		t.Fatal(err)
	}
	// Replace a target with another ordinary record. The indexed immutable
	// version must no longer be reachable through the old graph edge.
	changed := recordValue{ID: target.Records[0].ID, Kind: "note", Content: "unrelated replacement", Scope: "global", State: "active", Version: 2, Data: map[string]any{}, CreatedAt: "2026-10-19T00:00:00Z"}
	a.store.Put(ctx, records.PutRequest{Upsert: []records.Upsert{encodeRecord(changed, changed.Content)}})
	current, err := a.Read(ctx, ReadRequest{Question: "outage", Trace: true})
	if err != nil || slices.ContainsFunc(current.Records, func(e Evidence) bool { return e.ID == target.Records[0].ID }) || !current.Relations.Stale {
		t.Fatal("stale target became evidence", current, err)
	}
	historical, err := a.Read(ctx, ReadRequest{Question: "outage", Revision: oldRevision.Revision})
	if err != nil || !slices.ContainsFunc(historical.Records, func(e Evidence) bool { return e.Reference == target.Records[0] }) {
		t.Fatal("pinned historical evidence lost", historical, err)
	}
	project := "/private/project"
	private, err := a.Write(ctx, WriteRequest{ID: "private", Text: "outage secret", Scope: "project", WorkspaceID: project})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Relate(ctx, RelateRequest{IDs: []string{private.Records[0].ID}}); err == nil {
		t.Fatal("out-of-scope build admitted")
	}
}

func TestRelationConcurrentCheckpointAndReadOnlyFallback(t *testing.T) {
	a, dir := relationAgent(t, &RelationConfig{Adjacent: true, OnWrite: true}, &ReadPolicy{}, nil)
	writeTurn(t, a, "one", "anchor", "thread", 0)
	writeTurn(t, a, "two", "detail", "thread", 1)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := a.Relate(context.Background(), RelateRequest{}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	state, err := a.relations.versions(context.Background(), a.relationProfile(records.Scope{}))
	if err != nil || len(state) != 2 {
		t.Fatal(state, err)
	}
	ro, err := records.Open(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	readonly, err := Open(ro, dir, Config{Version: 1, Policy: &ReadPolicy{}, Relations: &RelationConfig{Adjacent: true}}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer readonly.Close()
	if _, err := readonly.Relate(context.Background(), RelateRequest{}); err == nil {
		t.Fatal("readonly graph build admitted")
	}
	if err := os.WriteFile(filepath.Join(dir, "memory-relations.db"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	// A separate index failure must not make the original channel unavailable.
	broken, err := Open(ro, dir, Config{Version: 1, Policy: &ReadPolicy{}, Relations: &RelationConfig{Adjacent: true}}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer broken.Close()
	view, err := broken.Read(context.Background(), ReadRequest{Question: "anchor"})
	if err != nil || len(view.Records) == 0 || !view.Relations.Stale {
		t.Fatal("corrupt graph blocked originals", view, err)
	}
}

func TestSemanticRelationCandidatesReusePreparedVectors(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var body struct{ Input []string }
		json.NewDecoder(r.Body).Decode(&body)
		vectors := [][]float64{}
		for _, text := range body.Input {
			v := []float64{0, 1}
			if strings.Contains(text, "transport canonical") || strings.Contains(text, "rail mobility") {
				v = []float64{1, 0}
			}
			vectors = append(vectors, v)
		}
		json.NewEncoder(w).Encode(map[string]any{"embeddings": vectors})
	}))
	defer server.Close()
	judge := decisionFunc(func(_ context.Context, state any, q map[string]Question) (map[string]float64, Usage, error) {
		raw, _ := json.Marshal(state)
		if !strings.Contains(string(raw), "rail mobility") {
			t.Error("dense candidate was not proposed")
		}
		v := zeroDecisions(q)
		v["p0_semantic"] = .9
		return v, Usage{}, nil
	})
	a, _ := relationAgent(t, &RelationConfig{Semantic: true}, &ReadPolicy{}, judge)
	writeTurn(t, a, "target", "rail mobility", "", 0)
	source := writeTurn(t, a, "source", "transport canonical", "", 0)
	writeTurn(t, a, "noise", "cake recipe", "", 0)
	cfg := records.EmbeddingConfig{URL: server.URL, Model: "fixture"}
	a.config.Embedding = &cfg
	pending, err := a.Relate(context.Background(), RelateRequest{IDs: []string{source.Records[0].ID}})
	if err != nil || pending.Visited != 0 || pending.Remaining != 3 || pending.SemanticReady {
		t.Fatal("missing vectors advanced the semantic checkpoint", pending, err)
	}
	if _, err := a.store.Embed(context.Background(), records.EmbedRequest{Embedding: cfg}, false); err != nil {
		t.Fatal(err)
	}
	before := requests.Load()
	result, err := a.Relate(context.Background(), RelateRequest{IDs: []string{source.Records[0].ID}})
	if err != nil || !result.SemanticReady || result.Edges != 1 || requests.Load() != before {
		t.Fatal("building filled or failed to reuse prepared vectors", result, err, before, requests.Load())
	}
	a.config.Relations.OnWrite = true
	written := writeTurn(t, a, "increment", "transport canonical extension", "", 0)
	if written.Relations == nil || written.Relations.Error != "" || written.Relations.Visited != 1 || !written.Relations.SemanticReady || requests.Load() != before+1 {
		t.Fatal("write did not prepare only its new document vector", written, requests.Load(), before)
	}
}

func TestRelationReadCacheIsScopedAndCanceledBuildRecovers(t *testing.T) {
	a, _ := relationAgent(t, &RelationConfig{Adjacent: true}, &ReadPolicy{}, nil)
	writeTurn(t, a, "public", "anchor public", "same-name", 0)
	workspace := "/private/project"
	position := int64(1)
	private, err := a.Write(context.Background(), WriteRequest{ID: "private", Text: "anchor secret", Scope: "project", WorkspaceID: workspace, Thread: "same-name", Position: &position})
	if err != nil {
		t.Fatal(err)
	}
	current, _ := a.store.Get(context.Background(), records.GetRequest{IDs: []string{}})
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.cachedRelationSnapshot(canceled, current.Revision, records.Scope{}); err == nil {
		t.Fatal("canceled cold build succeeded")
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			scope := records.Scope{}
			if i%2 == 1 {
				scope.WorkspaceID = &workspace
			}
			view, err := a.Read(context.Background(), ReadRequest{Question: "anchor", Scope: scope})
			if err != nil || slices.ContainsFunc(view.Records, func(e Evidence) bool { return e.ID == private.Records[0].ID }) != (i%2 == 1) {
				t.Errorf("scoped cache leaked or lost evidence: %v %v", view, err)
			}
		}(i)
	}
	wg.Wait()
}

func TestRelationNeedProgressResetsFailedSteps(t *testing.T) {
	r := &readRun{items: map[string]*candidate{}, order: []string{"new"}, related: &relationRead{}, paged: []bool{true}, rewritten: []bool{true}, needLeads: []string{"old"}, needEvidence: []map[string]bool{nil}}
	r.items["new"] = &candidate{item: Evidence{Reference: Reference{ID: "new"}}, judged: true, screen: .9, needed: .2, coverage: []float64{.8}}
	positive := r.positives(0)
	if !slices.Equal(positive, []string{"new"}) {
		t.Fatal("specific evidence was blocked by global relevance", positive)
	}
	r.refreshNeed(0, positive, []string{"new"})
	if r.paged[0] || r.rewritten[0] {
		t.Fatal("new evidence could not continue the bounded loop")
	}
	r.related = nil
	if len(r.positives(0)) != 0 {
		t.Fatal("legacy relevance gate changed")
	}
}

func TestContextualCandidatePreservesSourceMetadata(t *testing.T) {
	r := &readRun{related: &relationRead{context: map[string][]Evidence{}}}
	c := &candidate{item: Evidence{Text: "document body", Source: "repository/docs/design.md"}}
	state := r.contextualCandidates([]*candidate{c}, 600)
	if state[0]["source"] != c.item.Source || state[0]["text"] != c.item.Text {
		t.Fatal("contextual judgment lost provenance", state)
	}
}
