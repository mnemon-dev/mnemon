package records

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
)

type EmbeddingConfig struct {
	URL            string `json:"url"`
	Model          string `json:"model"`
	QueryPrefix    string `json:"queryPrefix,omitempty"`
	DocumentPrefix string `json:"documentPrefix,omitempty"`
}
type EmbedRequest struct {
	Revision  string          `json:"revision,omitempty"`
	Embedding EmbeddingConfig `json:"embedding"`
}
type EmbedStatus struct {
	Total    int     `json:"total"`
	Embedded int     `json:"embedded"`
	Coverage float64 `json:"coverage"`
}

func unit(values []float64) []float32 {
	out := make([]float32, len(values))
	norm := 0.0
	for i, v := range values {
		out[i] = float32(v)
		f := float64(out[i])
		norm += f * f
	}
	norm = math.Sqrt(norm)
	if norm == 0 {
		norm = 1
	}
	for i, v := range out {
		out[i] = float32(float64(v) / norm)
	}
	return out
}
func dot(a, b []float32) float64 {
	if len(b) < len(a) {
		return math.NaN()
	}
	sum := 0.0
	for i, v := range a {
		sum += float64(v) * float64(b[i])
	}
	return sum
}
func encodeVector(v []float32) []byte {
	b := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(f))
	}
	return b
}
func decodeVector(b []byte) ([]float32, error) {
	if len(b)%4 != 0 {
		return nil, errors.New("Invalid stored vector")
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v, nil
}

func embed(ctx context.Context, texts []string, cfg EmbeddingConfig, prefix string) ([][]float32, error) {
	out := [][]float32{}
	client := http.Client{Timeout: 5 * time.Minute}
	for start := 0; start < len(texts); start += 64 {
		end := start + 64
		if end > len(texts) {
			end = len(texts)
		}
		input := make([]string, end-start)
		for i, t := range texts[start:end] {
			input[i] = prefix + t
		}
		body, err := json.Marshal(struct {
			Model    string   `json:"model"`
			Input    []string `json:"input"`
			Truncate bool     `json:"truncate"`
		}{cfg.Model, input, true})
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.URL, "/")+"/api/embed", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("content-type", "application/json")
		response, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("Embedding request failed: %w", err)
		}
		var reply struct {
			Embeddings [][]float64 `json:"embeddings"`
		}
		err = json.NewDecoder(response.Body).Decode(&reply)
		response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, fmt.Errorf("Embedding request failed: %d", response.StatusCode)
		}
		if err != nil || len(reply.Embeddings) != len(input) {
			return nil, errors.New("Embedding reply does not match its request")
		}
		for _, v := range reply.Embeddings {
			out = append(out, unit(v))
		}
	}
	return out, nil
}

func (s *Store) vectors(ctx context.Context, texts []string, cfg EmbeddingConfig, prefix string, fill bool) ([][]float32, error) {
	out := make([][]float32, len(texts))
	known := map[string][]float32{}
	missing := []string{}
	seen := map[string]bool{}
	keys, uncached := []string{}, []string{}
	hashes := make([]string, len(texts))
	for i, text := range texts {
		key := textHash(text)
		hashes[i] = key
		if seen[key] {
			continue
		}
		seen[key] = true
		if value := s.cachedVector(key, cfg, prefix); value != nil {
			known[key] = value
			continue
		}
		keys = append(keys, key)
		uncached = append(uncached, text)
	}
	if err := s.loadVectors(ctx, keys, cfg, prefix, known); err != nil {
		return nil, err
	}
	if fill {
		for i, key := range keys {
			if known[key] == nil {
				missing = append(missing, uncached[i])
			}
		}
	}

	if len(missing) > 0 && s.readonly {
		return nil, errors.New("Missing record embeddings; run records embed before --readonly query")
	}
	for start := 0; start < len(missing); start += 64 {
		end := start + 64
		if end > len(missing) {
			end = len(missing)
		}
		part := missing[start:end]
		values, err := embed(ctx, part, cfg, prefix)
		if err != nil {
			return nil, err
		}
		if err := s.saveVectors(ctx, part, values, cfg, prefix); err != nil {
			return nil, err
		}
		for i, text := range part {
			known[textHash(text)] = values[i]
			s.keepVector(textHash(text), cfg, prefix, values[i])
		}
	}
	for i, key := range hashes {
		out[i] = known[key]
	}
	return out, nil
}

func (s *Store) saveVectors(ctx context.Context, texts []string, values [][]float32, cfg EmbeddingConfig, prefix string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, text := range texts {
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO embeddings VALUES(?,?,?,?)", textHash(text), cfg.Model, prefix, encodeVector(values[i])); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) vectorRank(ctx context.Context, query string, records []record, cfg EmbeddingConfig) ([]string, error) {
	return s.selectionVectorRank(ctx, query, newSelection(records), cfg)
}

func (s *Store) selectionVectorRank(ctx context.Context, query string, v *querySelection, cfg EmbeddingConfig) ([]string, error) {
	target, err := s.queryVector(ctx, query, cfg)
	if err != nil {
		return nil, err
	}
	values, err := s.selectionVectors(ctx, v, cfg)
	if err != nil {
		return nil, err
	}
	hits := make([]scored, len(v.docs))
	for i, r := range v.docs {
		if i%2048 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		hits[i] = scored{r.ID, dot(target, values[i])}
	}
	return rank(hits, 200), nil
}

func (s *Store) selectionVectors(ctx context.Context, v *querySelection, cfg EmbeddingConfig) ([][]float32, error) {
	key := vectorKey("", cfg, cfg.DocumentPrefix)
	for {
		v.mu.Lock()
		if values := v.vectors[key]; values != nil {
			v.mu.Unlock()
			return values, nil
		}
		if pending := v.vectorLoading[key]; pending != nil {
			v.mu.Unlock()
			select {
			case <-pending:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if v.vectorLoading == nil {
			v.vectorLoading = map[string]chan struct{}{}
		}
		v.vectorLoading[key] = make(chan struct{})
		v.mu.Unlock()
		values, err := s.loadSelectionVectors(ctx, v.docs, cfg)
		v.mu.Lock()
		if err == nil && s.cache != nil && len(v.docs) <= 200000 {
			if v.vectors == nil {
				v.vectors = map[string][][]float32{}
			}
			if len(v.vectorOrder) == 2 {
				delete(v.vectors, v.vectorOrder[0])
				v.vectorOrder = v.vectorOrder[1:]
			}
			v.vectors[key] = values
			v.vectorOrder = append(v.vectorOrder, key)
		}
		close(v.vectorLoading[key])
		delete(v.vectorLoading, key)
		v.mu.Unlock()
		return values, err
	}
}

func (s *Store) loadSelectionVectors(ctx context.Context, records []record, cfg EmbeddingConfig) ([][]float32, error) {
	out := make([][]float32, 0, len(records))
	for start := 0; start < len(records); start += 2048 {
		part := records[start:min(start+2048, len(records))]
		texts := make([]string, len(part))
		for i, r := range part {
			texts[i] = r.VectorText
		}
		values, err := s.vectors(ctx, texts, cfg, cfg.DocumentPrefix, true)
		if err != nil {
			return nil, err
		}
		out = append(out, values...)
	}
	return out, nil
}

func (s *Store) Embed(ctx context.Context, req EmbedRequest, statusOnly bool) (EmbedStatus, error) {
	if !statusOnly {
		if err := s.buildLexical(ctx); err != nil {
			return EmbedStatus{}, err
		}
	}
	rev, hash, err := revision(ctx, s.db, req.Revision)
	if err != nil {
		return EmbedStatus{}, err
	}
	records, err := s.queryRecords(ctx, rev, hash)
	if err != nil {
		return EmbedStatus{}, err
	}
	status := EmbedStatus{Total: len(records)}
	for start := 0; start < len(records); start += 2048 {
		end := min(start+2048, len(records))
		texts := make([]string, end-start)
		for i, r := range records[start:end] {
			texts[i] = r.VectorText
		}
		values, err := s.vectors(ctx, texts, req.Embedding, req.Embedding.DocumentPrefix, !statusOnly)
		if err != nil {
			return EmbedStatus{}, err
		}
		for _, v := range values {
			if v != nil {
				status.Embedded++
			}
		}
	}

	status.Coverage = 1
	if status.Total > 0 {
		status.Coverage = float64(status.Embedded) / float64(status.Total)
	}
	return status, nil
}

// Bound both SQL parameters and temporary BLOB/float copies. Serve publishes
// immutable vectors in its resident map; stateless CLI queries discard chunks.
func (s *Store) loadVectors(ctx context.Context, keys []string, cfg EmbeddingConfig, prefix string, known map[string][]float32) error {
	for start := 0; start < len(keys); start += 512 {
		end := min(start+512, len(keys))
		part := keys[start:end]
		args := []any{cfg.Model, prefix}
		for _, key := range part {
			args = append(args, key)
		}
		query := "SELECT text_hash,vector FROM embeddings WHERE model=? AND prefix=? AND text_hash IN (" + strings.TrimSuffix(strings.Repeat("?,", len(part)), ",") + ")"
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var key string
			var blob []byte
			if err = rows.Scan(&key, &blob); err != nil {
				rows.Close()
				return err
			}
			value, e := decodeVector(blob)
			if e != nil {
				rows.Close()
				return e
			}
			known[key] = value
			s.keepVector(key, cfg, prefix, value)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
