package embed

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestCosineSimilarity_Identical(t *testing.T) {
	v := []float64{1, 2, 3}
	got := CosineSimilarity(v, v)
	if math.Abs(got-1.0) > 1e-9 {
		t.Errorf("identical vectors: want 1.0, got %f", got)
	}
}

func TestCosineSimilarity_Orthogonal(t *testing.T) {
	a := []float64{1, 0, 0}
	b := []float64{0, 1, 0}
	got := CosineSimilarity(a, b)
	if math.Abs(got) > 1e-9 {
		t.Errorf("orthogonal vectors: want 0.0, got %f", got)
	}
}

func TestCosineSimilarity_Opposite(t *testing.T) {
	a := []float64{1, 2, 3}
	b := []float64{-1, -2, -3}
	got := CosineSimilarity(a, b)
	if math.Abs(got-(-1.0)) > 1e-9 {
		t.Errorf("opposite vectors: want -1.0, got %f", got)
	}
}

func TestCosineSimilarity_DifferentLength(t *testing.T) {
	a := []float64{1, 2}
	b := []float64{1, 2, 3}
	got := CosineSimilarity(a, b)
	if got != 0 {
		t.Errorf("different length: want 0, got %f", got)
	}
}

func TestCosineSimilarity_EmptyVector(t *testing.T) {
	got := CosineSimilarity(nil, nil)
	if got != 0 {
		t.Errorf("nil vectors: want 0, got %f", got)
	}
	got = CosineSimilarity([]float64{}, []float64{})
	if got != 0 {
		t.Errorf("empty vectors: want 0, got %f", got)
	}
}

func TestCosineSimilarity_ZeroVector(t *testing.T) {
	a := []float64{0, 0, 0}
	b := []float64{1, 2, 3}
	got := CosineSimilarity(a, b)
	if got != 0 {
		t.Errorf("zero vector: want 0, got %f", got)
	}
}

func TestCosineSimilarity_ScaledVector(t *testing.T) {
	a := []float64{1, 2, 3}
	b := []float64{2, 4, 6}
	got := CosineSimilarity(a, b)
	if math.Abs(got-1.0) > 1e-9 {
		t.Errorf("scaled vector: want 1.0, got %f", got)
	}
}

func TestSerializeDeserialize_Roundtrip(t *testing.T) {
	original := []float64{1.5, -2.7, 0.0, 3.14159, -0.125}
	blob := SerializeVector(original)
	restored := DeserializeVector(blob)

	wantLen := len(original) * 4
	if len(blob) != wantLen {
		t.Fatalf("blob length: want %d, got %d", wantLen, len(blob))
	}
	if len(restored) != len(original) {
		t.Fatalf("length mismatch: want %d, got %d", len(original), len(restored))
	}
	for i := range original {
		if math.Abs(original[i]-restored[i]) > 1e-6 {
			t.Errorf("index %d: want %f, got %f", i, original[i], restored[i])
		}
	}
}

func TestDeserializeVector_LegacyFloat64(t *testing.T) {
	original := []float64{1.5, -2.7, 0.0, 3.14159, math.MaxFloat64}
	blob := serializeLegacyFloat64(original)
	restored := DeserializeLegacyVector(blob)

	if len(restored) != len(original) {
		t.Fatalf("length mismatch: want %d, got %d", len(original), len(restored))
	}
	for i := range original {
		if original[i] != restored[i] {
			t.Errorf("index %d: want %f, got %f", i, original[i], restored[i])
		}
	}
}

func TestSerializeVector_Empty(t *testing.T) {
	blob := SerializeVector(nil)
	if len(blob) != 0 {
		t.Errorf("nil vector should produce empty blob, got len=%d", len(blob))
	}
}

func TestDeserializeVector_Empty(t *testing.T) {
	if v := DeserializeVector(nil); v != nil {
		t.Errorf("nil blob: want nil, got %v", v)
	}
	if v := DeserializeVector([]byte{}); v != nil {
		t.Errorf("empty blob: want nil, got %v", v)
	}
}

func TestDeserializeVector_InvalidLength(t *testing.T) {
	// 7 bytes is not a multiple of 4
	if v := DeserializeVector(make([]byte, 7)); v != nil {
		t.Errorf("invalid blob length: want nil, got %v", v)
	}
}

func TestDeserializeLegacyVector_InvalidLength(t *testing.T) {
	if v := DeserializeLegacyVector(make([]byte, 7)); v != nil {
		t.Errorf("invalid legacy blob length: want nil, got %v", v)
	}
}

func serializeLegacyFloat64(v []float64) []byte {
	buf := make([]byte, len(v)*8)
	for i, f := range v {
		binary.LittleEndian.PutUint64(buf[i*8:], math.Float64bits(f))
	}
	return buf
}
