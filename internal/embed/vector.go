package embed

import (
	"encoding/binary"
	"math"
)

// CosineSimilarity computes the cosine similarity between two vectors.
// Returns 0 if either vector is zero-length or has zero magnitude.
func CosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// SerializeVector encodes a vector as a little-endian float32 blob.
func SerializeVector(v []float64) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(float32(f)))
	}
	return buf
}

// DeserializeVector decodes a little-endian float32 blob into a float64 slice.
func DeserializeVector(b []byte) []float64 {
	if len(b) == 0 || len(b)%4 != 0 {
		return nil
	}
	v := make([]float64, len(b)/4)
	for i := range v {
		v[i] = float64(math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:])))
	}
	return v
}

// DeserializeLegacyVector decodes a pre-migration little-endian float64 blob.
func DeserializeLegacyVector(b []byte) []float64 {
	if len(b) == 0 || len(b)%8 != 0 {
		return nil
	}
	v := make([]float64, len(b)/8)
	for i := range v {
		v[i] = math.Float64frombits(binary.LittleEndian.Uint64(b[i*8:]))
	}
	return v
}
