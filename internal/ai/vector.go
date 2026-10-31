package ai

import (
	"encoding/json"
	"math"
)

// CosineSimilarity computes the cosine similarity between two float vectors.
// Returns a value between -1.0 and 1.0 (typically 0.0 to 1.0 for embeddings).
func CosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dotProduct float32
	var normA float32
	var normB float32

	for i := 0; i < len(a); i++ {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (float32(math.Sqrt(float64(normA))) * float32(math.Sqrt(float64(normB))))
}

// EncodeEmbedding marshals a float32 slice to JSON string for SQLite storage
func EncodeEmbedding(v []float32) (string, error) {
	bytes, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

// DecodeEmbedding unmarshals a JSON string from SQLite into a float32 slice
func DecodeEmbedding(raw string) ([]float32, error) {
	var v []float32
	err := json.Unmarshal([]byte(raw), &v)
	return v, err
}

