package scalebench

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
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

func TestHardProfileBuildsNearbyTopicsAndDistinctQueryVariants(t *testing.T) {
	generator, err := NewGenerator(DatasetConfig{Chunks: 1_000, Dimensions: 256, Topics: 20, Tenants: 5, Seed: 9, Profile: ProfileHardV2})
	if err != nil {
		t.Fatal(err)
	}
	sameCluster := cosine(generator.QueryVector(0), generator.QueryVector(1))
	differentCluster := cosine(generator.QueryVector(0), generator.QueryVector(10))
	if sameCluster <= differentCluster || sameCluster < 0.8 {
		t.Fatalf("hard profile did not create nearby topics: same=%f different=%f", sameCluster, differentCluster)
	}
	firstVariant := generator.BenchmarkQueryVector(0)
	if similarity := cosine(generator.QueryVector(0), firstVariant); similarity >= 0.999999 || similarity < 0.9 {
		t.Fatalf("hard profile must perturb the first query: %f", similarity)
	}
	variant := generator.BenchmarkQueryVector(20)
	if similarity := cosine(generator.QueryVector(0), variant); similarity >= 0.999999 || similarity < 0.9 {
		t.Fatalf("unexpected query variant similarity: %f", similarity)
	}
}

func TestPercentileUsesSortedObservedLatency(t *testing.T) {
	values := []float64{1, 2, 3, 4, 100}
	if got := percentile(values, 0.95); got != 4 {
		t.Fatalf("p95=%v want=4", got)
	}
}

func TestBenchmarkRejectsEFBelowTopKBeforeCallingMilvus(t *testing.T) {
	generator, err := NewGenerator(DatasetConfig{Chunks: 10, Dimensions: 8, Topics: 2, Tenants: 1, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewRunner(milvus.NewClient(milvus.Config{}), generator, Collections{Flat: "flat", HNSW: "hnsw"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runner.Benchmark(context.Background(), BenchmarkOptions{TopK: 10, EFValues: []int{8}})
	if err == nil || !strings.Contains(err.Error(), "greater than or equal") {
		t.Fatalf("expected local ef validation, got %v", err)
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
