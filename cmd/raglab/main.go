package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/app"
	"github.com/dingpuyu/rag-evolution-lab/internal/dataset"
	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
	"github.com/dingpuyu/rag-evolution-lab/internal/embeddinglab"
	"github.com/dingpuyu/rag-evolution-lab/internal/evaluation"
	"github.com/dingpuyu/rag-evolution-lab/internal/httpapi"
	"github.com/dingpuyu/rag-evolution-lab/internal/ingest"
	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
	"github.com/dingpuyu/rag-evolution-lab/internal/retrieval"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	if os.Args[1] == "serve-embedding" {
		runEmbeddingServer(os.Args[2:])
		return
	}
	if os.Args[1] == "milvus-seed" {
		runMilvusSeed(os.Args[2:])
		return
	}
	if os.Args[1] == "serve-lab" {
		runLabServer(os.Args[2:])
		return
	}
	root, err := findProjectRoot()
	if err != nil {
		fatal(err)
	}
	vectorBackend := strings.ToLower(strings.TrimSpace(os.Getenv("RAGLAB_VECTOR_BACKEND")))
	milvusURL := strings.TrimSpace(os.Getenv("RAGLAB_MILVUS_URL"))
	ollamaModel := strings.TrimSpace(os.Getenv("RAGLAB_OLLAMA_MODEL"))
	if vectorBackend == "milvus" || vectorBackend == "both" {
		if milvusURL == "" {
			milvusURL = milvus.DefaultURL
		}
		if ollamaModel == "" {
			ollamaModel = "qwen3-embedding:4b-local"
		}
	}
	runtime, err := app.BuildWithOptions(context.Background(), filepath.Join(root, "datasets", "corpus", "acmecloud"), app.Options{
		OllamaModel:       ollamaModel,
		OllamaURL:         os.Getenv("RAGLAB_OLLAMA_URL"),
		QueryInstruction:  os.Getenv("RAGLAB_QUERY_INSTRUCTION"),
		EmbeddingCacheDir: filepath.Join(root, "data", "cache", "embeddings"),
		MilvusURL:         milvusURL,
		MilvusToken:       os.Getenv("RAGLAB_MILVUS_TOKEN"),
		MilvusCollection:  os.Getenv("RAGLAB_MILVUS_COLLECTION"),
		MilvusSearchEF:    64,
		SkipOllamaMemory:  vectorBackend == "milvus",
	})
	if err != nil {
		fatal(err)
	}

	switch os.Args[1] {
	case "validate":
		runValidate(root, runtime, os.Args[2:])
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

func runEmbeddingServer(args []string) {
	flags := flag.NewFlagSet("serve-embedding", flag.ExitOnError)
	address := flags.String("addr", "127.0.0.1:8080", "HTTP listen address")
	backend := flags.String("backend", "auto", "embedding backend: auto, hash, or ollama")
	dimensions := flags.Int("dimensions", 512, "hash embedding dimensions")
	model := flags.String("model", os.Getenv("RAGLAB_OLLAMA_MODEL"), "Ollama embedding model")
	ollamaURL := flags.String("ollama-url", os.Getenv("RAGLAB_OLLAMA_URL"), "Ollama base URL")
	queryInstruction := flags.String("query-instruction", os.Getenv("RAGLAB_QUERY_INSTRUCTION"), "instruction used only in query_document mode")
	_ = flags.Parse(args)

	var embedder retrieval.Embedder
	switch *backend {
	case "auto":
		if *model == "" {
			embedder = retrieval.HashEmbedder{Dimensions: *dimensions}
		} else {
			embedder = retrieval.OllamaEmbedder{BaseURL: *ollamaURL, Model: *model, QueryInstruction: *queryInstruction}
		}
	case "hash":
		embedder = retrieval.HashEmbedder{Dimensions: *dimensions}
	case "ollama":
		if *model == "" {
			fatal(fmt.Errorf("--model or RAGLAB_OLLAMA_MODEL is required for ollama backend"))
		}
		embedder = retrieval.OllamaEmbedder{BaseURL: *ollamaURL, Model: *model, QueryInstruction: *queryInstruction}
	default:
		fatal(fmt.Errorf("unknown embedding backend %q", *backend))
	}
	service, err := embeddinglab.New(embedder)
	if err != nil {
		fatal(err)
	}
	handler, err := httpapi.NewEmbeddingHandler(service)
	if err != nil {
		fatal(err)
	}
	server := &http.Server{
		Addr:              *address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       time.Minute,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serverErrors := make(chan error, 1)
	go func() {
		fmt.Printf("embedding_api=http://%s embedder=%s\n", *address, embedder.Name())
		serverErrors <- server.ListenAndServe()
	}()
	select {
	case err := <-serverErrors:
		if err != nil && err != http.ErrServerClosed {
			fatal(err)
		}
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			fatal(fmt.Errorf("shutdown embedding API: %w", err))
		}
	}
}

func runMilvusSeed(args []string) {
	flags := flag.NewFlagSet("milvus-seed", flag.ExitOnError)
	model := flags.String("model", environmentOr("RAGLAB_OLLAMA_MODEL", "qwen3-embedding:4b-local"), "Ollama embedding model")
	ollamaURL := flags.String("ollama-url", os.Getenv("RAGLAB_OLLAMA_URL"), "Ollama base URL")
	milvusURL := flags.String("milvus-url", environmentOr("RAGLAB_MILVUS_URL", milvus.DefaultURL), "Milvus REST URL")
	collection := flags.String("collection", environmentOr("RAGLAB_MILVUS_COLLECTION", milvus.DefaultCollection), "Milvus collection")
	_ = flags.Parse(args)

	root, err := findProjectRoot()
	if err != nil {
		fatal(err)
	}
	documents, err := dataset.LoadCorpus(filepath.Join(root, "datasets", "corpus", "acmecloud"))
	if err != nil {
		fatal(err)
	}
	chunker := ingest.Chunker{MaxRunes: 700}
	var chunks []domain.Chunk
	for _, document := range documents {
		chunks = append(chunks, chunker.Chunk(document)...)
	}
	embedder := retrieval.OllamaEmbedder{BaseURL: *ollamaURL, Model: *model}
	service, err := milvus.NewService(milvus.NewClient(milvus.Config{BaseURL: *milvusURL}), embedder, *collection)
	if err != nil {
		fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	result, err := service.Seed(ctx, chunks)
	if err != nil {
		fatal(err)
	}
	writeJSON(result)
}

func runLabServer(args []string) {
	flags := flag.NewFlagSet("serve-lab", flag.ExitOnError)
	address := flags.String("addr", "127.0.0.1:8080", "HTTP listen address")
	model := flags.String("model", environmentOr("RAGLAB_OLLAMA_MODEL", "qwen3-embedding:4b-local"), "Ollama embedding model")
	ollamaURL := flags.String("ollama-url", os.Getenv("RAGLAB_OLLAMA_URL"), "Ollama base URL")
	queryInstruction := flags.String("query-instruction", os.Getenv("RAGLAB_QUERY_INSTRUCTION"), "query-side retrieval instruction")
	milvusURL := flags.String("milvus-url", environmentOr("RAGLAB_MILVUS_URL", milvus.DefaultURL), "Milvus REST URL")
	collection := flags.String("collection", environmentOr("RAGLAB_MILVUS_COLLECTION", milvus.DefaultCollection), "Milvus collection")
	_ = flags.Parse(args)

	embedder := retrieval.OllamaEmbedder{BaseURL: *ollamaURL, Model: *model, QueryInstruction: *queryInstruction}
	embeddingService, err := embeddinglab.New(embedder)
	if err != nil {
		fatal(err)
	}
	milvusService, err := milvus.NewService(milvus.NewClient(milvus.Config{BaseURL: *milvusURL}), embedder, *collection)
	if err != nil {
		fatal(err)
	}
	handler, err := httpapi.NewLabHandler(embeddingService, milvusService)
	if err != nil {
		fatal(err)
	}
	server := &http.Server{
		Addr: *address, Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 2 * time.Minute, IdleTimeout: time.Minute,
	}
	runHTTPServer(server, fmt.Sprintf("lab_api=http://%s embedder=%s milvus=%s collection=%s", *address, embedder.Name(), *milvusURL, *collection))
}

func runHTTPServer(server *http.Server, readyMessage string) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serverErrors := make(chan error, 1)
	go func() {
		fmt.Println(readyMessage)
		serverErrors <- server.ListenAndServe()
	}()
	select {
	case err := <-serverErrors:
		if err != nil && err != http.ErrServerClosed {
			fatal(err)
		}
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			fatal(fmt.Errorf("shutdown HTTP API: %w", err))
		}
	}
}

func environmentOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func runValidate(root string, runtime *app.Runtime, args []string) {
	flags := flag.NewFlagSet("validate", flag.ExitOnError)
	split := flags.String("split", "development", "dataset split")
	_ = flags.Parse(args)
	cases, err := dataset.LoadGolden(filepath.Join(root, "datasets", "golden"), *split)
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
	fmt.Printf("delta hit_rate=%+.3f mrr=%+.3f recall=%+.3f precision=%+.3f ndcg=%+.3f p95_ms=%+.3f\n",
		candidateReport.HitRate-baseReport.HitRate,
		candidateReport.MRR-baseReport.MRR,
		candidateReport.Recall-baseReport.Recall,
		candidateReport.Precision-baseReport.Precision,
		candidateReport.NDCG-baseReport.NDCG,
		candidateReport.LatencyP95MS-baseReport.LatencyP95MS,
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
	fmt.Printf("pipeline=%s split=%s cases=%d hit_rate@5=%.3f mrr=%.3f doc_recall@5=%.3f precision@5=%.3f ndcg@5=%.3f answerability=%.3f p50_ms=%.3f p95_ms=%.3f unauthorized=%d metadata_violations=%d citation_violations=%d\n",
		report.Pipeline, report.Split, report.Cases, report.HitRate, report.MRR, report.Recall, report.Precision, report.NDCG,
		report.AnswerabilityAccuracy, report.LatencyP50MS, report.LatencyP95MS,
		report.UnauthorizedRetrievals, report.MetadataViolations, report.CitationViolations)
	for _, category := range evaluation.SortedCategories(report) {
		metrics := report.ByCategory[category]
		fmt.Printf("  %-22s cases=%d hit=%.3f mrr=%.3f recall=%.3f precision=%.3f ndcg=%.3f\n",
			category, metrics.Cases, metrics.HitRate, metrics.MRR, metrics.Recall, metrics.Precision, metrics.NDCG)
	}
	if len(report.Routes) > 0 {
		fmt.Print("  routes")
		for _, route := range sortedCountKeys(report.Routes) {
			fmt.Printf(" %s=%d", route, report.Routes[route])
		}
		fmt.Println()
	}
}

func sortedCountKeys(values map[string]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
	fmt.Fprintln(os.Stderr, "usage: raglab <validate|ingest|query|eval|compare|serve-embedding|milvus-seed|serve-lab> [flags]")
}
