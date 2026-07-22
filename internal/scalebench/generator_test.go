package scalebench

import (
	"math"
	"testing"
)

func TestGeneratorIsDeterministicAndBuildsACLHardNegative(t *testing.T) {
	config := DatasetConfig{Chunks: 1_000, Dimensions: 64, Topics: 10, Tenants: 5, Seed: 42}
	first, err := NewGenerator(config)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewGenerator(config)
	if err != nil {
		t.Fatal(err)
	}
	firstRecords := first.Records(0, 20)
	secondRecords := second.Records(0, 20)
	if len(firstRecords) != 20 || len(secondRecords) != 20 {
		t.Fatalf("unexpected batch size: %d %d", len(firstRecords), len(secondRecords))
	}
	for dimension := range firstRecords[0].Embedding {
		if firstRecords[0].Embedding[dimension] != secondRecords[0].Embedding[dimension] {
			t.Fatal("same seed produced different vectors")
		}
	}
	hardNegative := firstRecords[0]
	if hardNegative.Visibility != "internal" || hardNegative.AllowedRoles[0] != "admin" {
		t.Fatalf("first topic record must be a private admin hard negative: %#v", hardNegative)
	}
	query := first.QueryVector(0)
	if cosine(query, hardNegative.Embedding) < 0.999999 {
		t.Fatal("hard negative should be the exact topic centroid")
	}
}

func TestGeneratorStreamsStableBatches(t *testing.T) {
	generator, err := NewGenerator(DatasetConfig{Chunks: 105, Dimensions: 8, Topics: 5, Tenants: 2, Seed: 7})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(generator.Records(100, 100)); got != 5 {
		t.Fatalf("last batch=%d want=5", got)
	}
	if generator.Records(0, 1)[0].ChunkID != "bench-t0000-c0000" {
		t.Fatalf("unexpected stable ID: %s", generator.Records(0, 1)[0].ChunkID)
	}
}

func TestPercentileUsesSortedObservedLatency(t *testing.T) {
	values := []float64{1, 2, 3, 4, 100}
	if got := percentile(values, 0.95); got != 4 {
		t.Fatalf("p95=%v want=4", got)
	}
}

func cosine(a, b []float64) float64 {
	var dot, normA, normB float64
	for index := range a {
		dot += a[index] * b[index]
		normA += a[index] * a[index]
		normB += b[index] * b[index]
	}
	return dot / math.Sqrt(normA*normB)
}
