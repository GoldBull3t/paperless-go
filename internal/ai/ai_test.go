package ai

import (
	"context"
	"math"
	"testing"
)

func TestCosineSimilarity(t *testing.T) {
	// Identical vectors -> 1.0
	v1 := []float32{1.0, 2.0, 3.0}
	v2 := []float32{1.0, 2.0, 3.0}
	sim := CosineSimilarity(v1, v2)
	if math.Abs(float64(sim-1.0)) > 1e-4 {
		t.Fatalf("Expected ~1.0, got %f", sim)
	}

	// Orthogonal vectors -> 0.0
	v3 := []float32{1.0, 0.0}
	v4 := []float32{0.0, 1.0}
	sim2 := CosineSimilarity(v3, v4)
	if math.Abs(float64(sim2)) > 1e-4 {
		t.Fatalf("Expected ~0.0, got %f", sim2)
	}

	// Opposite vectors -> -1.0
	v5 := []float32{1.0, 2.0}
	v6 := []float32{-1.0, -2.0}
	sim3 := CosineSimilarity(v5, v6)
	if math.Abs(float64(sim3 - (-1.0))) > 1e-4 {
		t.Fatalf("Expected ~-1.0, got %f", sim3)
	}
}

func TestEmbeddingsSerialization(t *testing.T) {
	vec := []float32{0.123, -0.456, 0.789}
	raw, err := EncodeEmbedding(vec)
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	decoded, err := DecodeEmbedding(raw)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}
	if len(decoded) != len(vec) {
		t.Fatalf("Length mismatch: got %d, expected %d", len(decoded), len(vec))
	}
	if math.Abs(float64(decoded[0]-vec[0])) > 1e-4 {
		t.Fatalf("Value mismatch: got %f, expected %f", decoded[0], vec[0])
	}
}

func TestDisabledProvider(t *testing.T) {
	p := NewProvider(ProviderConfig{Provider: "none"})
	_, err := p.Chat(context.Background(), "", []ChatMessage{{Role: "user", Content: "Hello"}})
	if err == nil {
		t.Fatal("Expected error from disabled provider, got nil")
	}
}

