package scalebench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func WriteReports(directory string, seed SeedReport, benchmark BenchmarkReport) error {
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create report directory: %w", err)
	}
	payload := struct {
		Seed      SeedReport      `json:"seed"`
		Benchmark BenchmarkReport `json:"benchmark"`
	}{Seed: seed, Benchmark: benchmark}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("encode scale report: %w", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "scale-10k-latest.json"), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write JSON report: %w", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "scale-10k-latest.md"), []byte(markdown(seed, benchmark)), 0o644); err != nil {
		return fmt.Errorf("write Markdown report: %w", err)
	}
	return nil
}

func markdown(seed SeedReport, benchmark BenchmarkReport) string {
	var output strings.Builder
	fmt.Fprintf(&output, "# 10K Milvus Scale Benchmark\n\n")
	fmt.Fprintf(&output, "生成时间：`%s`\n\n", benchmark.GeneratedAt.Format("2006-01-02 15:04:05Z"))
	fmt.Fprintf(&output, "## 数据与写入\n\n")
	fmt.Fprintf(&output, "- 数据：%d chunks / %d topics / %d tenants / %d dimensions\n", seed.Dataset.Chunks, seed.Dataset.Topics, seed.Dataset.Tenants, seed.Dataset.Dimensions)
	fmt.Fprintf(&output, "- Collection：`%s`（精确对照）与 `%s`（ANN）\n", seed.Collections.Flat, seed.Collections.HNSW)
	fmt.Fprintf(&output, "- Batch：%d，写入耗时：%.2fs，唯一数据吞吐：%.2f rows/s\n\n", seed.BatchSize, seed.DurationMS/1000, seed.RowsPerSecond)
	fmt.Fprintf(&output, "## HNSW 与 FLAT Recall 对照\n\n")
	fmt.Fprintf(&output, "| Scenario | ef | Queries | Recall@%d | QPS | P50 ms | P95 ms | P99 ms | Errors |\n", benchmark.TopK)
	fmt.Fprintf(&output, "|---|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, run := range benchmark.Runs {
		fmt.Fprintf(&output, "| %s | %d | %d | %.4f | %.2f | %.3f | %.3f | %.3f | %d |\n", run.Scenario, run.EF, run.Queries, run.RecallAtK, run.QPS, run.P50MS, run.P95MS, run.P99MS, run.Errors)
	}
	fmt.Fprintf(&output, "\n## ACL硬门禁\n\n")
	fmt.Fprintf(&output, "- Queries：%d\n- Unauthorized Retrievals：**%d**\n\n", benchmark.ACL.Queries, benchmark.ACL.UnauthorizedRetrievals)
	fmt.Fprintf(&output, "> 本报告在预计算Query Vector上测量Milvus检索阶段，不包含Embedding模型耗时；FLAT只用于Ground Truth，不作为线上索引方案。\n")
	return output.String()
}
