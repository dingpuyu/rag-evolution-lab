package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
	"github.com/dingpuyu/rag-evolution-lab/internal/scalebench"
)

func main() {
	if len(os.Args) < 2 {
		fatal(fmt.Errorf("usage: ragbench all|seed|run [flags]"))
	}
	flags := flag.NewFlagSet("ragbench", flag.ExitOnError)
	chunks := flags.Int("chunks", 10_000, "number of deterministic chunks")
	dimensions := flags.Int("dimensions", 1024, "vector dimensions")
	topics := flags.Int("topics", 100, "semantic topic count")
	tenants := flags.Int("tenants", 100, "tenant count")
	seedValue := flags.Int64("seed", 20260723, "deterministic random seed")
	batchSize := flags.Int("batch-size", 100, "upsert batch size")
	queries := flags.Int("queries", 100, "benchmark query count")
	topK := flags.Int("top-k", 10, "search top K")
	concurrency := flags.Int("concurrency", 8, "HNSW query workers")
	efValues := flags.String("ef", "32,64,128", "comma-separated HNSW ef values")
	milvusURL := flags.String("milvus-url", environmentOr("RAGLAB_MILVUS_URL", milvus.DefaultURL), "Milvus REST URL")
	prefix := flags.String("collection-prefix", "raglab_bench_10k", "collection name prefix")
	timeout := flags.Duration("timeout", 30*time.Minute, "overall command timeout")
	_ = flags.Parse(os.Args[2:])

	root, err := projectRoot()
	if err != nil {
		fatal(err)
	}
	generator, err := scalebench.NewGenerator(scalebench.DatasetConfig{
		Chunks: *chunks, Dimensions: *dimensions, Topics: *topics, Tenants: *tenants, Seed: *seedValue,
	})
	if err != nil {
		fatal(err)
	}
	collections := scalebench.Collections{Flat: *prefix + "_flat_v1", HNSW: *prefix + "_hnsw_v1"}
	runner, err := scalebench.NewRunner(milvus.NewClient(milvus.Config{BaseURL: *milvusURL, Token: os.Getenv("RAGLAB_MILVUS_TOKEN")}), generator, collections)
	if err != nil {
		fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	seedOptions := scalebench.SeedOptions{BatchSize: *batchSize, M: 16, EFBuild: 200, Progress: progress}
	benchmarkOptions := scalebench.BenchmarkOptions{Queries: *queries, TopK: *topK, Concurrency: *concurrency, EFValues: parseInts(*efValues)}

	var seedReport scalebench.SeedReport
	var benchmarkReport scalebench.BenchmarkReport
	switch os.Args[1] {
	case "seed":
		seedReport, err = runner.Seed(ctx, seedOptions)
		printJSON(seedReport, err)
	case "run":
		benchmarkReport, err = runner.Benchmark(ctx, benchmarkOptions)
		printJSON(benchmarkReport, err)
	case "all":
		seedReport, err = runner.Seed(ctx, seedOptions)
		if err == nil {
			benchmarkReport, err = runner.Benchmark(ctx, benchmarkOptions)
		}
		if err == nil {
			err = scalebench.WriteReports(filepath.Join(root, "eval", "reports"), seedReport, benchmarkReport)
		}
		printJSON(struct {
			Seed      scalebench.SeedReport      `json:"seed"`
			Benchmark scalebench.BenchmarkReport `json:"benchmark"`
		}{Seed: seedReport, Benchmark: benchmarkReport}, err)
	default:
		fatal(fmt.Errorf("unknown command %q", os.Args[1]))
	}
}

func progress(written, total int) {
	if written == total || written%1000 == 0 {
		fmt.Fprintf(os.Stderr, "seeded=%d/%d\n", written, total)
	}
}

func parseInts(value string) []int {
	var values []int
	for _, part := range strings.Split(value, ",") {
		parsed, err := strconv.Atoi(strings.TrimSpace(part))
		if err == nil && parsed > 0 {
			values = append(values, parsed)
		}
	}
	return values
}

func printJSON(value any, err error) {
	if err != nil {
		fatal(err)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		fatal(err)
	}
}

func environmentOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func projectRoot() (string, error) {
	current, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("could not locate project root")
		}
		current = parent
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
