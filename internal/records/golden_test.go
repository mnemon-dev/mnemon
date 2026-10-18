package records

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestTSGolden(t *testing.T) {
	var fixture struct {
		Revision string
		Tokens   []struct {
			Text     string
			Expected []string
		}
		Upsert  []Upsert
		Queries []struct {
			Input     map[string]json.RawMessage
			Scope     Scope
			IDs       []string
			Expansion *[]string
		}
		Vectors map[string][]float64
		Fusion  struct {
			Rankings [][]string
			Expected []string
		}
	}
	data, err := os.ReadFile("testdata/golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, tc := range fixture.Tokens {
		if got := Tokens(tc.Text); !reflect.DeepEqual(got, tc.Expected) {
			t.Errorf("tokens %q: %q != %q", tc.Text, got, tc.Expected)
		}
	}
	if got := fuse(fixture.Fusion.Rankings...); !reflect.DeepEqual(got, fixture.Fusion.Expected) {
		t.Fatal("RRF", got)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" || r.Method != "POST" {
			t.Error("embedding protocol", r.Method, r.URL.Path)
		}
		var body struct {
			Model    string
			Input    []string
			Truncate bool
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != "golden" || !body.Truncate || len(body.Input) > 64 {
			t.Error("embedding request contract")
		}
		vectors := [][]float64{}
		for _, text := range body.Input {
			if !strings.HasPrefix(text, "search_document: ") && !strings.HasPrefix(text, "search_query: ") {
				t.Error("missing prefix")
			}
			text = strings.TrimPrefix(strings.TrimPrefix(text, "search_document: "), "search_query: ")
			v, ok := fixture.Vectors[text]
			if !ok {
				t.Errorf("unknown embedding text %q", text)
			}
			vectors = append(vectors, v)
		}
		json.NewEncoder(w).Encode(map[string]any{"embeddings": vectors})
	}))
	defer server.Close()
	s := testStore(t)
	ctx := context.Background()
	rev, err := s.Put(ctx, PutRequest{Upsert: fixture.Upsert})
	if err != nil {
		t.Fatal(err)
	}
	if rev != fixture.Revision {
		t.Fatalf("revision %s != TS %s", rev, fixture.Revision)
	}
	cfg := &EmbeddingConfig{URL: server.URL, Model: "golden", QueryPrefix: "search_query: ", DocumentPrefix: "search_document: "}
	for i, tc := range fixture.Queries {
		got, err := s.Query(ctx, QueryRequest{Revision: rev, Input: tc.Input, Scope: tc.Scope, Embedding: cfg})
		if err != nil {
			t.Fatalf("query %d: %v", i, err)
		}
		if !reflect.DeepEqual(got.IDs, tc.IDs) || !reflect.DeepEqual(got.Expansion, tc.Expansion) {
			t.Errorf("query %d %s: got %+v expansion=%v; want %v expansion=%v", i, tc.Input["query"], got, got.Expansion, tc.IDs, tc.Expansion)
		}
	}
}
