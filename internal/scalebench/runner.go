package scalebench

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
)

type Collections struct {
	Flat string `json:"flat"`
	HNSW string `json:"hnsw"`
}

type SeedOptions struct {
	BatchSize      int
	M              int
	EFBuild        int
	MaxRetries     int
	Resume         bool
	CheckpointPath string
	Progress       func(written, total int)
}

type SeedReport struct {
	Dataset       DatasetConfig `json:"dataset"`
	Collections   Collections   `json:"collections"`
	BatchSize     int           `json:"batch_size"`
	Rows          int64         `json:"rows"`
	ResumedFrom   int           `json:"resumed_from"`
	Retries       int           `json:"retries"`
	DurationMS    float64       `json:"duration_ms"`
	RowsPerSecond float64       `json:"rows_per_second"`
}

type BenchmarkOptions struct {
	Queries     int
	Warmup      int
	TopK        int
	Concurrency int
	EFValues    []int
}

type RunResult struct {
	Scenario          string  `json:"scenario"`
	EF                int     `json:"ef"`
	Queries           int     `json:"queries"`
	RecallAtK         float64 `json:"recall_at_k"`
	TopicHitAtK       float64 `json:"topic_hit_at_k"`
	TopicPrecisionAtK float64 `json:"topic_precision_at_k"`
	QPS               float64 `json:"qps"`
	P50MS             float64 `json:"p50_ms"`
	P95MS             float64 `json:"p95_ms"`
	P99MS             float64 `json:"p99_ms"`
	Errors            int     `json:"errors"`
}

type ACLResult struct {
	Queries                int `json:"queries"`
	UnauthorizedRetrievals int `json:"unauthorized_retrievals"`
}

type BenchmarkReport struct {
	GeneratedAt time.Time     `json:"generated_at"`
	Dataset     DatasetConfig `json:"dataset"`
	Collections Collections   `json:"collections"`
	TopK        int           `json:"top_k"`
	Concurrency int           `json:"concurrency"`
	Warmup      int           `json:"warmup"`
	Runs        []RunResult   `json:"runs"`
	ACL         ACLResult     `json:"acl"`
}

type Runner struct {
	client      *milvus.Client
	generator   *Generator
	collections Collections
}

func NewRunner(client *milvus.Client, generator *Generator, collections Collections) (*Runner, error) {
	if client == nil || generator == nil {
		return nil, fmt.Errorf("scale runner requires client and generator")
	}
	if strings.TrimSpace(collections.Flat) == "" || strings.TrimSpace(collections.HNSW) == "" {
		return nil, fmt.Errorf("both FLAT and HNSW collection names are required")
	}
	return &Runner{client: client, generator: generator, collections: collections}, nil
}

func (r *Runner) Seed(ctx context.Context, options SeedOptions) (SeedReport, error) {
	if options.BatchSize <= 0 {
		options.BatchSize = 100
	}
	if options.M <= 0 {
		options.M = 16
	}
	if options.EFBuild <= 0 {
		options.EFBuild = 200
	}
	if options.MaxRetries < 0 {
		options.MaxRetries = 0
	}
	started := time.Now()
	collections, err := r.client.ListCollections(ctx)
	if err != nil {
		return SeedReport{}, err
	}
	startOffset := 0
	checkpointCompleted := false
	if options.Resume {
		checkpoint, err := readCheckpoint(options.CheckpointPath)
		if err != nil {
			return SeedReport{}, err
		}
		if checkpoint.Dataset != r.generator.config || checkpoint.Collections != r.collections {
			return SeedReport{}, fmt.Errorf("checkpoint configuration does not match current dataset or collections")
		}
		for _, name := range []string{r.collections.Flat, r.collections.HNSW} {
			if !contains(collections, name) {
				return SeedReport{}, fmt.Errorf("cannot resume: collection %s does not exist", name)
			}
		}
		startOffset = checkpoint.NextOffset
		checkpointCompleted = checkpoint.Completed
		if startOffset < 0 || startOffset > r.generator.config.Chunks {
			return SeedReport{}, fmt.Errorf("invalid checkpoint offset %d", startOffset)
		}
	} else {
		for _, name := range []string{r.collections.Flat, r.collections.HNSW} {
			if contains(collections, name) {
				if err := r.client.DropCollection(ctx, name); err != nil {
					return SeedReport{}, fmt.Errorf("drop %s: %w", name, err)
				}
			}
		}
		dimensions := r.generator.config.Dimensions
		if err := r.client.CreateCollectionWithOptions(ctx, r.collections.Flat, milvus.CollectionOptions{Dimensions: dimensions, IndexType: "FLAT", MetricType: "COSINE"}); err != nil {
			return SeedReport{}, fmt.Errorf("create FLAT collection: %w", err)
		}
		if err := r.client.CreateCollectionWithOptions(ctx, r.collections.HNSW, milvus.CollectionOptions{Dimensions: dimensions, IndexType: "HNSW", MetricType: "COSINE", M: options.M, EFConstruction: options.EFBuild}); err != nil {
			return SeedReport{}, fmt.Errorf("create HNSW collection: %w", err)
		}
		if err := writeCheckpoint(options.CheckpointPath, seedCheckpoint{Dataset: r.generator.config, Collections: r.collections}); err != nil {
			return SeedReport{}, err
		}
	}
	retries := 0
	for start := startOffset; start < r.generator.config.Chunks; start += options.BatchSize {
		records := r.generator.Records(start, options.BatchSize)
		for _, collection := range []string{r.collections.Flat, r.collections.HNSW} {
			attemptRetries, err := r.upsertWithRetry(ctx, collection, records, options.MaxRetries)
			retries += attemptRetries
			if err != nil {
				return SeedReport{}, fmt.Errorf("upsert %s at offset %d: %w", collection, start, err)
			}
		}
		nextOffset := min(start+len(records), r.generator.config.Chunks)
		if err := writeCheckpoint(options.CheckpointPath, seedCheckpoint{Dataset: r.generator.config, Collections: r.collections, NextOffset: nextOffset}); err != nil {
			return SeedReport{}, err
		}
		if options.Progress != nil {
			options.Progress(nextOffset, r.generator.config.Chunks)
		}
	}
	if !checkpointCompleted {
		for _, collection := range []string{r.collections.Flat, r.collections.HNSW} {
			attemptRetries, err := r.flushWithRetry(ctx, collection, options.MaxRetries)
			retries += attemptRetries
			if err != nil {
				return SeedReport{}, fmt.Errorf("flush %s: %w", collection, err)
			}
		}
	}
	flatRows, err := r.waitForRows(ctx, r.collections.Flat, int64(r.generator.config.Chunks))
	if err != nil {
		return SeedReport{}, err
	}
	rows, err := r.waitForRows(ctx, r.collections.HNSW, int64(r.generator.config.Chunks))
	if err != nil {
		return SeedReport{}, err
	}
	if flatRows != rows {
		return SeedReport{}, fmt.Errorf("collection row mismatch: flat=%d hnsw=%d", flatRows, rows)
	}
	if err := r.waitForIndex(ctx, r.collections.Flat, "embedding_flat", int64(r.generator.config.Chunks)); err != nil {
		return SeedReport{}, err
	}
	if err := r.waitForIndex(ctx, r.collections.HNSW, "embedding_hnsw", int64(r.generator.config.Chunks)); err != nil {
		return SeedReport{}, err
	}
	if err := writeCheckpoint(options.CheckpointPath, seedCheckpoint{Dataset: r.generator.config, Collections: r.collections, NextOffset: r.generator.config.Chunks, Completed: true}); err != nil {
		return SeedReport{}, err
	}
	duration := time.Since(started)
	return SeedReport{
		Dataset: r.generator.config, Collections: r.collections, BatchSize: options.BatchSize, Rows: rows,
		ResumedFrom: startOffset, Retries: retries, DurationMS: milliseconds(duration),
		RowsPerSecond: float64(r.generator.config.Chunks-startOffset) / duration.Seconds(),
	}, nil
}

func (r *Runner) flushWithRetry(ctx context.Context, collection string, maxRetries int) (int, error) {
	for attempt := 0; ; attempt++ {
		err := r.client.FlushCollection(ctx, collection)
		if err == nil {
			return attempt, nil
		}
		if attempt >= maxRetries {
			return attempt, err
		}
		// Milvus protects Flush with a much lower rate limit than Upsert.
		delay := time.Duration(1<<attempt) * 3 * time.Second
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return attempt, ctx.Err()
		case <-timer.C:
		}
	}
}

func (r *Runner) upsertWithRetry(ctx context.Context, collection string, records []milvus.Record, maxRetries int) (int, error) {
	for attempt := 0; ; attempt++ {
		_, err := r.client.Upsert(ctx, collection, records)
		if err == nil {
			return attempt, nil
		}
		if attempt >= maxRetries {
			return attempt, err
		}
		delay := time.Duration(1<<attempt) * 200 * time.Millisecond
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return attempt, ctx.Err()
		case <-timer.C:
		}
	}
}

func (r *Runner) Benchmark(ctx context.Context, options BenchmarkOptions) (BenchmarkReport, error) {
	if options.Queries <= 0 {
		options.Queries = 100
	}
	if options.TopK <= 0 {
		options.TopK = 10
	}
	if options.Concurrency <= 0 {
		options.Concurrency = 8
	}
	if options.Warmup < 0 {
		options.Warmup = 0
	}
	if len(options.EFValues) == 0 {
		options.EFValues = []int{32, 64, 128}
	}
	for _, ef := range options.EFValues {
		if ef < options.TopK {
			return BenchmarkReport{}, fmt.Errorf("HNSW ef (%d) must be greater than or equal to topK (%d)", ef, options.TopK)
		}
	}
	scenarios := []scenario{
		{name: "active_all", filter: func(int) string { return `status == "active"` }},
		{name: "public_active", filter: func(int) string { return `visibility == "public" and status == "active"` }},
		{name: "tenant_admin_active", filter: func(topic int) string {
			return `(visibility == "public" or (array_contains(allowed_tenants, "` + r.generator.Tenant(topic) + `") and array_contains(allowed_roles, "admin"))) and status == "active"`
		}},
	}
	report := BenchmarkReport{GeneratedAt: time.Now().UTC(), Dataset: r.generator.config, Collections: r.collections, TopK: options.TopK, Concurrency: options.Concurrency, Warmup: options.Warmup}
	for _, scenario := range scenarios {
		groundTruth, err := r.groundTruth(ctx, scenario, options.Queries, options.TopK)
		if err != nil {
			return BenchmarkReport{}, err
		}
		for _, ef := range options.EFValues {
			if err := r.warmHNSW(ctx, groundTruth, options.Warmup, options.TopK, ef); err != nil {
				return BenchmarkReport{}, fmt.Errorf("warm HNSW scenario=%s ef=%d: %w", scenario.name, ef, err)
			}
			result := r.runHNSW(ctx, scenario, groundTruth, options, ef)
			report.Runs = append(report.Runs, result)
		}
	}
	report.ACL = r.checkACL(ctx, min(options.Queries, r.generator.config.Topics), options.TopK)
	return report, nil
}

type scenario struct {
	name   string
	filter func(topic int) string
}

type queryTruth struct {
	topic    int
	vector   []float64
	filter   string
	expected map[string]struct{}
}

func (r *Runner) groundTruth(ctx context.Context, scenario scenario, queries, topK int) ([]queryTruth, error) {
	truth := make([]queryTruth, 0, queries)
	for index := 0; index < queries; index++ {
		topic := index % r.generator.config.Topics
		vector := r.generator.BenchmarkQueryVector(index)
		filter := scenario.filter(topic)
		hits, err := r.client.Search(ctx, milvus.SearchRequest{Collection: r.collections.Flat, Vector: vector, Filter: filter, Limit: topK, Exact: true})
		if err != nil {
			return nil, fmt.Errorf("FLAT ground truth scenario=%s query=%d: %w", scenario.name, index, err)
		}
		expected := make(map[string]struct{}, len(hits))
		for _, hit := range hits {
			expected[hit.ChunkID] = struct{}{}
		}
		truth = append(truth, queryTruth{topic: topic, vector: vector, filter: filter, expected: expected})
	}
	return truth, nil
}

func (r *Runner) warmHNSW(ctx context.Context, truth []queryTruth, warmup, topK, ef int) error {
	if len(truth) == 0 {
		return nil
	}
	for index := 0; index < warmup; index++ {
		query := truth[index%len(truth)]
		if _, err := r.client.Search(ctx, milvus.SearchRequest{Collection: r.collections.HNSW, Vector: query.vector, Filter: query.filter, Limit: topK, EF: ef}); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) runHNSW(ctx context.Context, scenario scenario, truth []queryTruth, options BenchmarkOptions, ef int) RunResult {
	type observation struct {
		duration       time.Duration
		recall         float64
		topicHit       float64
		topicPrecision float64
		err            error
	}
	jobs := make(chan queryTruth)
	observations := make(chan observation, len(truth))
	var workers sync.WaitGroup
	started := time.Now()
	for worker := 0; worker < options.Concurrency; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for query := range jobs {
				queryStarted := time.Now()
				hits, err := r.client.Search(ctx, milvus.SearchRequest{Collection: r.collections.HNSW, Vector: query.vector, Filter: query.filter, Limit: options.TopK, EF: ef})
				observation := observation{duration: time.Since(queryStarted), err: err}
				if err == nil && len(query.expected) > 0 {
					topicPrefix := fmt.Sprintf("bench-t%04d-", query.topic)
					topicMatches := 0
					for _, hit := range hits {
						if _, ok := query.expected[hit.ChunkID]; ok {
							observation.recall += 1 / float64(len(query.expected))
						}
						if strings.HasPrefix(hit.ChunkID, topicPrefix) {
							topicMatches++
						}
					}
					if topicMatches > 0 {
						observation.topicHit = 1
					}
					if len(hits) > 0 {
						observation.topicPrecision = float64(topicMatches) / float64(len(hits))
					}
				}
				observations <- observation
			}
		}()
	}
	for _, query := range truth {
		jobs <- query
	}
	close(jobs)
	workers.Wait()
	close(observations)
	totalDuration := time.Since(started)
	durations := make([]float64, 0, len(truth))
	var recall, topicHit, topicPrecision float64
	var errors int
	for observation := range observations {
		if observation.err != nil {
			errors++
			continue
		}
		durations = append(durations, milliseconds(observation.duration))
		recall += observation.recall
		topicHit += observation.topicHit
		topicPrecision += observation.topicPrecision
	}
	sort.Float64s(durations)
	successful := len(truth) - errors
	if successful > 0 {
		recall /= float64(successful)
		topicHit /= float64(successful)
		topicPrecision /= float64(successful)
	}
	return RunResult{
		Scenario: scenario.name, EF: ef, Queries: len(truth), RecallAtK: recall,
		TopicHitAtK: topicHit, TopicPrecisionAtK: topicPrecision,
		QPS: float64(successful) / totalDuration.Seconds(), P50MS: percentile(durations, 0.50),
		P95MS: percentile(durations, 0.95), P99MS: percentile(durations, 0.99), Errors: errors,
	}
}

func (r *Runner) checkACL(ctx context.Context, queries, topK int) ACLResult {
	result := ACLResult{Queries: queries}
	for topic := 0; topic < queries; topic++ {
		filter := `(visibility == "public" or (array_contains(allowed_tenants, "` + r.generator.Tenant(topic) + `") and array_contains(allowed_roles, "viewer"))) and status == "active"`
		hits, err := r.client.Search(ctx, milvus.SearchRequest{Collection: r.collections.HNSW, Vector: r.generator.QueryVector(topic), Filter: filter, Limit: topK, EF: 64})
		if err != nil {
			result.UnauthorizedRetrievals++
			continue
		}
		for _, hit := range hits {
			if hit.Visibility == "public" {
				continue
			}
			roleAllowed := false
			for _, role := range hit.AllowedRoles {
				if role == "viewer" {
					roleAllowed = true
					break
				}
			}
			if hit.TenantID != r.generator.Tenant(topic) || !roleAllowed {
				result.UnauthorizedRetrievals++
			}
		}
	}
	return result
}

func (r *Runner) waitForRows(ctx context.Context, collection string, expected int64) (int64, error) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		stats, err := r.client.CollectionStats(ctx, collection)
		if err == nil && int64(stats.RowCount) == expected {
			return int64(stats.RowCount), nil
		}
		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("wait for %d rows in %s: %w", expected, collection, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (r *Runner) waitForIndex(ctx context.Context, collection, indexName string, expected int64) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		indexes, err := r.client.DescribeIndex(ctx, collection, indexName)
		if err == nil && len(indexes) > 0 {
			index := indexes[0]
			if index.IndexState == "Finished" && index.IndexedRowCount() == expected && index.PendingRowCount() == 0 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for index %s on %s to cover %d rows: %w", indexName, collection, expected, ctx.Err())
		case <-ticker.C:
		}
	}
}

func percentile(values []float64, ratio float64) float64 {
	if len(values) == 0 {
		return 0
	}
	index := int(float64(len(values)-1) * ratio)
	return values[index]
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func milliseconds(duration time.Duration) float64 { return float64(duration.Microseconds()) / 1000 }
