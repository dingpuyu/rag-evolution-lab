package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dingpuyu/rag-evolution-lab/internal/app"
	"github.com/dingpuyu/rag-evolution-lab/internal/dataset"
	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
	"github.com/dingpuyu/rag-evolution-lab/internal/evaluation"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	root, err := findProjectRoot()
	if err != nil {
		fatal(err)
	}
	runtime, err := app.Build(filepath.Join(root, "datasets", "corpus", "acmecloud"))
	if err != nil {
		fatal(err)
	}

	switch os.Args[1] {
	case "validate":
		runValidate(root, runtime)
	case "ingest":
		fmt.Printf("documents=%d chunks=%d\n", len(runtime.Documents), len(runtime.Chunks))
	case "query":
		runQuery(runtime, os.Args[2:])
	case "eval":
		runEval(root, runtime, os.Args[2:])
	case "compare":
		runCompare(root, runtime, os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func runValidate(root string, runtime *app.Runtime) {
	cases, err := dataset.LoadGolden(filepath.Join(root, "datasets", "golden"), "development")
	if err != nil {
		fatal(err)
	}
	if err := dataset.Validate(runtime.Documents, cases); err != nil {
		fatal(err)
	}
	fmt.Printf("valid documents=%d golden_cases=%d\n", len(runtime.Documents), len(cases))
}

func runQuery(runtime *app.Runtime, args []string) {
	flags := flag.NewFlagSet("query", flag.ExitOnError)
	pipelineName := flags.String("pipeline", "v0-keyword", "pipeline version")
	query := flags.String("query", "", "query text")
	tenant := flags.String("tenant", "tenant_a", "tenant id")
	role := flags.String("role", "admin", "user role")
	product := flags.String("product", "", "product filter")
	version := flags.String("version", "", "version filter")
	topK := flags.Int("top-k", 5, "maximum retrieval results")
	_ = flags.Parse(args)
	target, err := runtime.Pipeline(*pipelineName)
	if err != nil {
		fatal(err)
	}
	response, err := target.Query(context.Background(), domain.QueryRequest{
		Query:    *query,
		Pipeline: *pipelineName,
		TenantID: *tenant,
		UserRole: *role,
		Product:  *product,
		Version:  *version,
		TopK:     *topK,
	})
	if err != nil {
		fatal(err)
	}
	writeJSON(response)
}

func runEval(root string, runtime *app.Runtime, args []string) {
	flags := flag.NewFlagSet("eval", flag.ExitOnError)
	pipelineName := flags.String("pipeline", "v0-keyword", "pipeline version")
	split := flags.String("split", "development", "dataset split")
	jsonOutput := flags.Bool("json", false, "print full JSON report")
	_ = flags.Parse(args)
	report := evaluate(root, runtime, *pipelineName, *split)
	if *jsonOutput {
		writeJSON(report)
		return
	}
	printReport(report)
}

func runCompare(root string, runtime *app.Runtime, args []string) {
	flags := flag.NewFlagSet("compare", flag.ExitOnError)
	baseline := flags.String("baseline", "v0-keyword", "baseline pipeline")
	candidate := flags.String("candidate", "v1-vector", "candidate pipeline")
	split := flags.String("split", "development", "dataset split")
	_ = flags.Parse(args)
	baseReport := evaluate(root, runtime, *baseline, *split)
	candidateReport := evaluate(root, runtime, *candidate, *split)
	printReport(baseReport)
	printReport(candidateReport)
	fmt.Printf("delta hit_rate=%+.3f mrr=%+.3f recall=%+.3f\n",
		candidateReport.HitRate-baseReport.HitRate,
		candidateReport.MRR-baseReport.MRR,
		candidateReport.Recall-baseReport.Recall,
	)
}

func evaluate(root string, runtime *app.Runtime, pipelineName, split string) evaluation.Report {
	target, err := runtime.Pipeline(pipelineName)
	if err != nil {
		fatal(err)
	}
	cases, err := dataset.LoadGolden(filepath.Join(root, "datasets", "golden"), split)
	if err != nil {
		fatal(err)
	}
	report, err := evaluation.Run(context.Background(), target, split, cases)
	if err != nil {
		fatal(err)
	}
	return report
}

func printReport(report evaluation.Report) {
	fmt.Printf("pipeline=%s split=%s cases=%d hit_rate@5=%.3f mrr=%.3f doc_recall@5=%.3f unauthorized=%d\n",
		report.Pipeline, report.Split, report.Cases, report.HitRate, report.MRR, report.Recall, report.UnauthorizedRetrievals)
	for _, category := range evaluation.SortedCategories(report) {
		metrics := report.ByCategory[category]
		fmt.Printf("  %-22s cases=%d hit=%.3f mrr=%.3f recall=%.3f\n",
			category, metrics.Cases, metrics.HitRate, metrics.MRR, metrics.Recall)
	}
}

func findProjectRoot() (string, error) {
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
			return "", fmt.Errorf("could not find project root")
		}
		current = parent
	}
}

func writeJSON(value any) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: raglab <validate|ingest|query|eval|compare> [flags]")
}
