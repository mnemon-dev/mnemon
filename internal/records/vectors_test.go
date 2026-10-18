package records

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestVectorChunksPersistDuplicatesAndKeepRanks(t *testing.T) {
	var documents, queries atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string
			Input    []string
			Truncate bool
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if body.Model != "chunks" || !body.Truncate || len(body.Input) > 64 {
			t.Error("embedding contract")
		}
		values := make([][]float64, len(body.Input))
		for i, text := range body.Input {
			if strings.HasPrefix(text, "q: ") {
				values[i] = []float64{1, 0}
				queries.Add(1)
				continue
			}
			index, err := strconv.Atoi(strings.TrimPrefix(text, "d: "))
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			values[i] = []float64{1, float64(index + 1)}
			documents.Add(1)
		}
		json.NewEncoder(w).Encode(map[string]any{"embeddings": values})
	}))
	defer server.Close()
	s := testStore(t)
	ctx := context.Background()
	cfg := EmbeddingConfig{URL: server.URL, Model: "chunks", QueryPrefix: "q: ", DocumentPrefix: "d: "}
	rows := make([]record, 8193)
	for i := range rows {
		rows[i] = record{ID: fmt.Sprintf("%08x-0000-0000-0000-000000000000", i), VectorText: strconv.Itoa(i)}
	}
	rows[8192].VectorText = rows[0].VectorText
	want := []string{rows[0].ID, rows[8192].ID}
	for i := 1; i < 199; i++ {
		want = append(want, rows[i].ID)
	}
	selection := newSelection(rows)
	for run := 0; run < 3; run++ {
		if run == 1 {
			s.cache = newQueryCache()
		}
		got, err := s.selectionVectorRank(ctx, "needle", selection, cfg)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d ranks differ: %v %v", run, got, err)
		}
	}
	if documents.Load() != 8192 || queries.Load() != 2 {
		t.Fatalf("unexpected repeated embeddings: docs=%d queries=%d", documents.Load(), queries.Load())
	}
	var stored int
	if err := s.db.QueryRow("SELECT count(*) FROM embeddings").Scan(&stored); err != nil || stored != 8192 {
		t.Fatalf("durable unique vectors %d %v", stored, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := vectorScores(canceled, []float32{1, 0}, rows, make([][]float32, len(rows))); !errors.Is(err, context.Canceled) {
		t.Fatalf("parallel scoring cancellation: %v", err)
	}
}

func TestExactTopScoresMatchesOriginalStableSort(t *testing.T) {
	for _, size := range []int{1, 10, 100, 2501} {
		hits := make([]scored, size)
		for i := range hits {
			hits[i] = scored{fmt.Sprintf("%08d", size-i), float64((i*7919)%53) - 26}
		}
		for _, exceptional := range []float64{math.Inf(1), math.Nextafter(0, 1), math.Inf(-1), math.NaN()} {
			hits[size/2].score = exceptional
			want := slices.Clone(hits)
			sort.SliceStable(want, func(i, j int) bool {
				if want[i].score == want[j].score {
					return want[i].id < want[j].id
				}
				return want[i].score > want[j].score
			})
			for _, limit := range []int{0, 1, 10, 200} {
				got := rank(slices.Clone(hits), limit)
				ids := make([]string, min(limit, len(want)))
				for i := range ids {
					ids[i] = want[i].id
				}
				if !reflect.DeepEqual(got, ids) {
					t.Fatalf("size=%d limit=%d score=%v: %v != %v", size, limit, exceptional, got, ids)
				}
			}
		}
	}
}

func TestPreparedVectorConfigurationIsolation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string
			Input []string
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		values := make([][]float64, len(body.Input))
		for i, text := range body.Input {
			x := 1.0
			if !strings.HasPrefix(text, "q:") {
				reverse := strings.HasPrefix(text, "b:") != (body.Model == "reverse")
				if strings.HasSuffix(text, "beta") != reverse {
					x = -1
				}
			}
			values[i] = []float64{x, 0}
		}
		json.NewEncoder(w).Encode(map[string]any{"embeddings": values})
	}))
	defer server.Close()
	s := testStore(t)
	ctx := context.Background()
	if _, err := s.Put(ctx, PutRequest{Upsert: []Upsert{sample("a", "alpha"), sample("b", "beta")}}); err != nil {
		t.Fatal(err)
	}
	s.cache = newQueryCache()
	for _, tc := range []struct {
		model, prefix string
		ids           []string
	}{
		{"normal", "a:", []string{"a", "b"}}, {"normal", "b:", []string{"b", "a"}},
		{"reverse", "a:", []string{"b", "a"}}, {"normal", "a:", []string{"a", "b"}},
	} {
		cfg := EmbeddingConfig{URL: server.URL, Model: tc.model, QueryPrefix: "q:", DocumentPrefix: tc.prefix}
		got, err := s.Query(ctx, QueryRequest{Input: map[string]json.RawMessage{"query": json.RawMessage(`"needle"`), "match": json.RawMessage(`"hybrid"`)}, Embedding: &cfg})
		if err != nil || !reflect.DeepEqual(got.IDs, tc.ids) {
			t.Fatalf("model=%s prefix=%s: %+v %v", tc.model, tc.prefix, got, err)
		}
	}
}
