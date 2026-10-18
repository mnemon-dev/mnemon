package records

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
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
	rows := make([]record, 2501)
	for i := range rows {
		rows[i] = record{ID: fmt.Sprintf("%08x-0000-0000-0000-000000000000", i), VectorText: strconv.Itoa(i)}
	}
	rows[2500].VectorText = rows[0].VectorText
	want := []string{rows[0].ID, rows[2500].ID}
	for i := 1; i < 199; i++ {
		want = append(want, rows[i].ID)
	}
	for run := 0; run < 3; run++ {
		if run == 1 {
			s.cache = newQueryCache()
		}
		got, err := s.vectorRank(ctx, "needle", rows, cfg)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("run %d ranks differ: %v %v", run, got, err)
		}
	}
	if documents.Load() != 2500 || queries.Load() != 2 {
		t.Fatalf("unexpected repeated embeddings: docs=%d queries=%d", documents.Load(), queries.Load())
	}
	var stored int
	if err := s.db.QueryRow("SELECT count(*) FROM embeddings").Scan(&stored); err != nil || stored != 2500 {
		t.Fatalf("durable unique vectors %d %v", stored, err)
	}
}
