package records

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"unicode"
)

// letterEmbeddings serves deterministic vectors: each text's letter counts,
// plus one so that no vector is zero.
func letterEmbeddings(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Input []string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(400)
			return
		}
		values := make([][]float64, len(body.Input))
		for i, text := range body.Input {
			v := make([]float64, 27)
			v[26] = 1
			for _, c := range strings.ToLower(text) {
				if c >= 'a' && c <= 'z' {
					v[c-'a']++
				} else if unicode.IsLetter(c) {
					v[26]++
				}
			}
			values[i] = v
		}
		json.NewEncoder(w).Encode(map[string]any{"embeddings": values})
	}))
	t.Cleanup(server.Close)
	return server
}

// goldenIndexStore holds a fixture with every kind of index metadata: writer
// links of each type, entities, event times, dates, a correction, an
// advisory supersedes link, overlay rows and fillers.
func goldenIndexStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	link := func(id, kind string, weight float64) map[string]any {
		return map[string]any{"id": id, "version": 1, "type": kind, "weight": weight}
	}
	dated := func(u Upsert, date string) Upsert {
		var v map[string]any
		json.Unmarshal([]byte(u.RecordJSON), &v)
		v["date"] = date
		raw, _ := json.Marshal(v)
		u.RecordJSON = string(raw)
		return u
	}
	upserts := []Upsert{
		indexedRecord("anchor", "Why did the outage happen at Athena", map[string]any{"links": []any{
			link("cause", "causal", 1), link("athena-team", "entity", 0.8), link("followup", "temporal", 0.6), link("similar", "semantic", 0.7)}}),
		indexedRecord("cause", "Disk filled completely on the primary", map[string]any{"links": []any{link("root", "causal", 0.9)}}),
		indexedRecord("root", "A log rotation job was disabled", nil),
		indexedRecord("athena-team", "The Athena team owns the cluster", map[string]any{"entities": []string{"Athena"}}),
		indexedRecord("followup", "Postmortem meeting next week", map[string]any{"eventAt": "2020-03-05", "links": []any{link("later", "temporal", 1)}}),
		indexedRecord("later", "Retrospective notes were published", map[string]any{"eventAt": "2020-03-09"}),
		indexedRecord("similar", "Another outage in staging", map[string]any{"links": []any{link("similar2", "semantic", 0.5)}}),
		indexedRecord("similar2", "Staging cluster restarted", nil),
		indexedRecord("old", "alpha endpoint", nil),
		indexedRecord("new", "beta endpoint alpha", map[string]any{"mnemonSupersedes": map[string]any{"id": "old", "version": 1}}),
		indexedRecord("proposal", "gamma endpoint", map[string]any{"links": []any{link("old", "supersedes", 1)}}),
		indexedRecord("dated", "release shipped", map[string]any{"eventAt": "2020-03-02T23:30:00-02:00"}),
		dated(indexedRecord("day", "release planning", nil), "2020-02-01"),
	}
	for i := 0; i < 25; i++ {
		upserts = append(upserts, indexedRecord(fmt.Sprintf("f%02d", i), fmt.Sprintf("filler note %d about weather", i), map[string]any{"eventAt": fmt.Sprintf("2019-12-%02d", i+1)}))
	}
	s := testStore(t)
	if _, err := s.Put(ctx, PutRequest{Upsert: upserts}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceDerivedMetadata(ctx, "consolidation", map[string]DerivedMetadata{
		"similar2": {Version: 1, Links: []Link{{ID: "root", Version: 1, Type: "entity", Weight: 0.5}}},
		"f03":      {Version: 1, Entities: []string{"Athena"}},
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

// goldenIndexQueries is a matrix of queries over the index routes that
// existed before traversal and recency.
func goldenIndexQueries(cfg *EmbeddingConfig) []QueryRequest {
	policies := []*IndexPolicy{nil, {}, {Version: 1, Lexical: true}, {Version: 1, Entities: true}, {Version: 1, Relations: true},
		{Version: 1, Supersedes: true}, {Version: 1, Temporal: true}, {Version: 1, Lexical: true, Entities: true, Relations: true, Temporal: true, Supersedes: true}}
	queries := []QueryRequest{}
	for _, text := range []string{"Why outage Athena", "When was the release", "alpha endpoint", "Athena", "weather", "staging", "", "zzz"} {
		for _, policy := range policies {
			plain := indexRequest(text, policy)
			queries = append(queries, plain)
			hybrid := indexRequest(text, policy)
			hybrid.Input["match"], hybrid.Embedding = json.RawMessage(`"hybrid"`), cfg
			queries = append(queries, hybrid)
			when := indexRequest(text, policy)
			when.Input["intent"] = json.RawMessage(`"WHEN"`)
			queries = append(queries, when)
			if policy != nil {
				timed := indexRequest(text, policy)
				timed.Input["since"], timed.Input["timeOrder"] = json.RawMessage(`"2020-01-01"`), json.RawMessage(`"newest"`)
				queries = append(queries, timed)
				history := indexRequest(text, policy)
				history.Input["history"] = json.RawMessage(`true`)
				queries = append(queries, history)
			}
			ids := indexRequest(text, policy)
			ids.Input["ids"] = json.RawMessage(`["anchor","old","f01"]`)
			ids.CandidateLimit = 5
			queries = append(queries, ids)
		}
	}
	return queries
}

// TestIndexRoutesGolden pins the bytes of every response of the index
// routes that existed before traversal and recency, with and without the
// runtime's caches. RECORDS_UPDATE_GOLDEN=1 rewrites the golden file.
func TestIndexRoutesGolden(t *testing.T) {
	ctx := context.Background()
	server := letterEmbeddings(t)
	cfg := &EmbeddingConfig{URL: server.URL, Model: "letters"}
	s := goldenIndexStore(t)
	results := func() []string {
		out := []string{}
		for _, q := range goldenIndexQueries(cfg) {
			got, err := s.Query(ctx, q)
			request, _ := json.Marshal(struct {
				Input          map[string]json.RawMessage
				Index          *IndexPolicy
				Embedding      bool
				CandidateLimit int
			}{q.Input, q.Index, q.Embedding != nil, q.CandidateLimit})
			response, _ := json.Marshal(got)
			out = append(out, fmt.Sprintf("%s => %s %v", request, response, err))
		}
		return out
	}
	got := results()
	const path = "testdata/index-routes-golden.json"
	if os.Getenv("RECORDS_UPDATE_GOLDEN") != "" {
		var raw bytes.Buffer
		e := json.NewEncoder(&raw)
		e.SetEscapeHTML(false)
		e.SetIndent("", " ")
		if err := e.Encode(got); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	compare := func(label string, got []string) {
		if len(got) != len(want) {
			t.Fatalf("%s: %d responses, golden has %d", label, len(got), len(want))
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("%s: response %d differs:\n got %s\nwant %s", label, i, got[i], want[i])
			}
		}
	}
	compare("stateless", got)
	stop, err := s.StartRuntime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	compare("runtime", results())
	compare("runtime warm", results())
}
