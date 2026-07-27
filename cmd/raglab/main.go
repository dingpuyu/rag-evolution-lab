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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/answereval"
	"github.com/dingpuyu/rag-evolution-lab/internal/app"
	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
	"github.com/dingpuyu/rag-evolution-lab/internal/dataset"
	"github.com/dingpuyu/rag-evolution-lab/internal/datasetaccess"
	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
	"github.com/dingpuyu/rag-evolution-lab/internal/embeddinglab"
	"github.com/dingpuyu/rag-evolution-lab/internal/evaluation"
	"github.com/dingpuyu/rag-evolution-lab/internal/generation"
	"github.com/dingpuyu/rag-evolution-lab/internal/httpapi"
	"github.com/dingpuyu/rag-evolution-lab/internal/ingest"
	"github.com/dingpuyu/rag-evolution-lab/internal/ingestionjob"
	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
	"github.com/dingpuyu/rag-evolution-lab/internal/retrieval"
	"github.com/dingpuyu/rag-evolution-lab/internal/scalebench"
	"github.com/dingpuyu/rag-evolution-lab/internal/searchharness"
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
	if os.Args[1] == "dataset-eval" {
		runDatasetEval(root, os.Args[2:])
		return
	}
	if os.Args[1] == "answer-eval" {
		runAnswerEval(root, os.Args[2:])
		return
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
	embeddingBackend := flags.String("embedding-backend", environmentOr("RAGLAB_EMBEDDING_BACKEND", "ollama"), "embedding backend: ollama or hash")
	hashDimensions := flags.Int("hash-dimensions", environmentInt("RAGLAB_HASH_EMBEDDING_DIMENSIONS", 512), "dimensions for hash embedding backend")
	milvusURL := flags.String("milvus-url", environmentOr("RAGLAB_MILVUS_URL", milvus.DefaultURL), "Milvus REST URL")
	collection := flags.String("collection", environmentOr("RAGLAB_MILVUS_COLLECTION", milvus.DefaultCollection), "Milvus collection")
	scalePrefix := flags.String("scale-prefix", "raglab_bench_100k", "100K scale collection prefix")
	scaleVersion := flags.String("scale-version", "v2", "100K scale collection version")
	lifecycleCollection := flags.String("lifecycle-collection", environmentOr("RAGLAB_LIFECYCLE_COLLECTION", "raglab_lifecycle_v1"), "incremental knowledge collection")
	lifecycleAlias := flags.String("lifecycle-alias", environmentOr("RAGLAB_LIFECYCLE_ALIAS", "raglab_knowledge_active"), "active knowledge collection alias")
	embeddingVersion := flags.String("embedding-version", environmentOr("RAGLAB_EMBEDDING_VERSION", "qwen3-embedding-4b-q4km-v1"), "immutable embedding build version")
	generationProviderDefault := strings.TrimSpace(os.Getenv("RAGLAB_GENERATION_PROVIDER"))
	if generationProviderDefault == "" {
		if strings.TrimSpace(os.Getenv("RAGLAB_GENERATION_API_KEY")) != "" || strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY")) != "" {
			generationProviderDefault = "deepseek"
		} else {
			generationProviderDefault = "ollama"
		}
	}
	generationProvider := flags.String("generation-provider", generationProviderDefault, "grounded answer provider: ollama or openai-compatible")
	generationModel := flags.String("generation-model", os.Getenv("RAGLAB_GENERATION_MODEL"), "grounded answer model; provider-specific default when empty")
	generationBaseURL := flags.String("generation-base-url", environmentOr("RAGLAB_GENERATION_BASE_URL", "https://api.deepseek.com"), "OpenAI-compatible generation base URL")
	generationTimeout := flags.Duration("generation-timeout", 2*time.Minute, "grounded answer generation timeout")
	generationMaxTokens := flags.Int("generation-max-tokens", 512, "maximum generated answer tokens")
	lifecycleState := flags.String("lifecycle-state", environmentOr("RAGLAB_LIFECYCLE_STATE", "data/lifecycle/state.json"), "durable lifecycle event state")
	ingestionJobState := flags.String("ingestion-job-state", environmentOr("RAGLAB_INGESTION_JOB_STATE", "data/ingestion/jobs.json"), "durable ingestion job state")
	ingestionWorkers := flags.Int("ingestion-workers", 1, "number of asynchronous ingestion workers")
	authSecret := flags.String("auth-secret", environmentOr("RAGLAB_AUTH_SECRET", "raglab-local-development-secret-change-me"), "JWT HMAC secret for local lab")
	authIssuer := flags.String("auth-issuer", environmentOr("RAGLAB_AUTH_ISSUER", "raglab-local"), "JWT issuer")
	authAudience := flags.String("auth-audience", environmentOr("RAGLAB_AUTH_AUDIENCE", "raglab-api"), "JWT audience")
	authOIDCIssuer := flags.String("auth-oidc-issuer", os.Getenv("RAGLAB_AUTH_OIDC_ISSUER"), "enterprise OIDC issuer; enables RS256/JWKS mode")
	authJWKSURL := flags.String("auth-jwks-url", os.Getenv("RAGLAB_AUTH_JWKS_URL"), "optional direct JWKS URL; otherwise OIDC discovery is used")
	authAccounts := flags.String("auth-accounts", environmentOr("RAGLAB_AUTH_ACCOUNTS", "data/auth/accounts.json"), "local-lab account store; unused in OIDC mode")
	platformAdminPassword := flags.String("platform-admin-password", environmentOr("RAGLAB_PLATFORM_ADMIN_PASSWORD", "RagLab-Platform-2026!"), "local-lab platform administrator password")
	postgresURL := flags.String("postgres-url", environmentOr("RAGLAB_POSTGRES_URL", "postgres://raglab:raglab-local@127.0.0.1:5433/raglab?sslmode=disable"), "PostgreSQL control-plane URL; set empty for in-memory fallback")
	_ = flags.Parse(args)

	embedder, err := newLabEmbedder(*embeddingBackend, *ollamaURL, *model, *queryInstruction, *hashDimensions)
	if err != nil {
		fatal(err)
	}
	embeddingService, err := embeddinglab.New(embedder)
	if err != nil {
		fatal(err)
	}
	milvusClient := milvus.NewClient(milvus.Config{BaseURL: *milvusURL})
	milvusService, err := milvus.NewService(milvusClient, embedder, *collection)
	if err != nil {
		fatal(err)
	}
	lifecycleService, err := milvus.NewLifecycleService(milvusClient, embedder, milvus.LifecycleConfig{
		Collection: *lifecycleCollection, Alias: *lifecycleAlias, EmbeddingVersion: *embeddingVersion,
		StatePath: *lifecycleState, ChunkRunes: 700,
	})
	if err != nil {
		fatal(err)
	}
	ingestionJobs, err := ingestionjob.New(lifecycleService, ingestionjob.Config{
		StatePath: *ingestionJobState, Workers: *ingestionWorkers, QueueCapacity: 1_024, MaxAttempts: 3,
	})
	if err != nil {
		fatal(err)
	}
	if err := ingestionJobs.Start(context.Background()); err != nil {
		fatal(err)
	}
	defer ingestionJobs.Close()
	scaleGenerator, err := scalebench.NewGenerator(scalebench.DatasetConfig{
		Chunks: 100_000, Dimensions: 1024, Topics: 1_000, Tenants: 100,
		Seed: 20260723, Profile: scalebench.ProfileHardV2,
	})
	if err != nil {
		fatal(err)
	}
	scaleService, err := scalebench.NewDemoService(milvusClient, scaleGenerator, scalebench.Collections{
		Flat: *scalePrefix + "_flat_" + *scaleVersion, HNSW: *scalePrefix + "_hnsw_" + *scaleVersion,
	})
	if err != nil {
		fatal(err)
	}
	var verifier auth.Verifier
	var devIssuer *auth.Manager
	var localAccounts *auth.AccountStore
	var datasetStore datasetaccess.Store = datasetaccess.Defaults()
	if strings.TrimSpace(*postgresURL) != "" {
		controlPlaneContext, cancelControlPlane := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelControlPlane()
		postgresStore, postgresErr := datasetaccess.OpenPostgres(controlPlaneContext, *postgresURL)
		if postgresErr != nil {
			fatal(postgresErr)
		}
		defer postgresStore.Close()
		datasetStore = postgresStore
	}
	provider := strings.ToLower(strings.TrimSpace(*generationProvider))
	if strings.TrimSpace(*generationModel) == "" {
		if provider == "deepseek" {
			*generationModel = "deepseek-v4-pro"
		} else if provider == "openai" || provider == "openai-compatible" {
			*generationModel = "deepseek-chat"
		} else {
			*generationModel = "qwen3.5:9b"
		}
	}
	generationAPIKey := strings.TrimSpace(os.Getenv("RAGLAB_GENERATION_API_KEY"))
	if generationAPIKey == "" {
		generationAPIKey = strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	}
	generationGenerator, generationErr := newGenerationGenerator(provider, *generationBaseURL, generationAPIKey, *ollamaURL, *generationModel, *generationTimeout, *generationMaxTokens)
	if generationErr != nil {
		fatal(generationErr)
	}
	authMode := "local_hs256"
	if strings.TrimSpace(*authOIDCIssuer) != "" || strings.TrimSpace(*authJWKSURL) != "" {
		oidcVerifier, oidcErr := auth.NewOIDCVerifier(auth.OIDCConfig{
			Issuer: *authOIDCIssuer, Audience: *authAudience, JWKSURL: *authJWKSURL,
		})
		if oidcErr != nil {
			fatal(oidcErr)
		}
		warmupContext, cancelWarmup := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelWarmup()
		if oidcErr := oidcVerifier.Warmup(warmupContext); oidcErr != nil {
			fatal(oidcErr)
		}
		verifier = oidcVerifier
		authMode = "oidc_rs256"
	} else {
		authManager, managerErr := auth.NewManager(auth.Config{
			Secret: []byte(*authSecret), Issuer: *authIssuer, Audience: *authAudience, TTL: time.Hour,
		})
		if managerErr != nil {
			fatal(managerErr)
		}
		verifier = authManager
		devIssuer = authManager
		localAccounts, managerErr = auth.NewAccountStore(*authAccounts)
		if managerErr != nil {
			fatal(managerErr)
		}
		for _, demo := range []struct {
			email, password, tenant string
			roles                   []string
		}{
			{"admin@raglab.local", *platformAdminPassword, "platform", []string{"platform_admin"}},
			{"alice@tenant-a.local", "RagLab-Alice-2026!", "tenant_a", []string{"admin"}},
			{"bob@tenant-b.local", "RagLab-Bob-2026!", "tenant_b", []string{"admin"}},
		} {
			if managerErr = localAccounts.EnsureDemo(demo.email, demo.password, demo.tenant, demo.roles); managerErr != nil {
				fatal(managerErr)
			}
		}
	}
	handler, err := httpapi.NewEnterpriseLabHandler(embeddingService, milvusService, scaleService, httpapi.EnterpriseOptions{
		Verifier: verifier, DevIssuer: devIssuer, LocalAccounts: localAccounts,
		Audit: auth.NewAuditLog(200), IngestionJobs: ingestionJobs, DatasetStore: datasetStore,
		Generator: generationGenerator,
	}, lifecycleService)
	if err != nil {
		fatal(err)
	}
	server := &http.Server{
		Addr: *address, Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 2 * time.Minute, IdleTimeout: time.Minute,
	}
	runHTTPServer(server, fmt.Sprintf("lab_api=http://%s embedder=%s generator=%s model=%s milvus=%s collection=%s auth_mode=%s", *address, embedder.Name(), generationGenerator.Name(), *generationModel, *milvusURL, *collection, authMode))
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

func environmentFloat(name string, fallback float64) float64 {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed < 0 {
		fatal(fmt.Errorf("%s must be a non-negative number", name))
	}
	return parsed
}

func environmentBool(name string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		fatal(fmt.Errorf("%s must be a boolean", name))
	}
	return parsed
}

func environmentInt(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		fatal(fmt.Errorf("%s must be a positive integer", name))
	}
	return parsed
}

func newLabEmbedder(backend, ollamaURL, model, queryInstruction string, hashDimensions int) (retrieval.Embedder, error) {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "ollama", "local":
		if strings.TrimSpace(model) == "" {
			return nil, fmt.Errorf("RAGLAB_OLLAMA_MODEL is required for the ollama embedding backend")
		}
		return retrieval.OllamaEmbedder{BaseURL: ollamaURL, Model: model, QueryInstruction: queryInstruction}, nil
	case "hash", "deterministic":
		if hashDimensions <= 0 {
			return nil, fmt.Errorf("hash embedding dimensions must be positive")
		}
		return retrieval.HashEmbedder{Dimensions: hashDimensions}, nil
	default:
		return nil, fmt.Errorf("unsupported embedding backend %q; use ollama or hash", backend)
	}
}

func newGenerationGenerator(provider, baseURL, apiKey, ollamaURL, model string, timeout time.Duration, maxTokens int) (generation.Generator, error) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "extractive", "baseline":
		return generation.ExtractiveGenerator{}, nil
	case "ollama", "local":
		return generation.OllamaGenerator{BaseURL: ollamaURL, Model: model, Timeout: timeout, NumPredict: maxTokens}, nil
	case "openai", "openai-compatible", "deepseek":
		if strings.TrimSpace(apiKey) == "" {
			return nil, fmt.Errorf("RAGLAB_GENERATION_API_KEY is required when RAGLAB_GENERATION_PROVIDER=%s", provider)
		}
		if strings.TrimSpace(baseURL) == "" {
			baseURL = "https://api.deepseek.com"
		}
		return generation.OpenAICompatibleGenerator{
			BaseURL: baseURL, APIKey: apiKey, Model: model, Provider: provider,
			Timeout: timeout, NumPredict: maxTokens,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported RAGLAB_GENERATION_PROVIDER %q; use ollama or openai-compatible", provider)
	}
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
	jsonOutput := flags.Bool("json", false, "print a machine-readable comparison report")
	failOnRegression := flags.Bool("fail-on-regression", false, "fail when candidate quality or safety regresses against baseline")
	minHitRate := flags.Float64("min-hit-rate", 0, "minimum candidate Hit@K; zero disables the threshold")
	minMRR := flags.Float64("min-mrr", 0, "minimum candidate MRR; zero disables the threshold")
	minRecall := flags.Float64("min-recall", 0, "minimum candidate document recall; zero disables the threshold")
	minNDCG := flags.Float64("min-ndcg", 0, "minimum candidate NDCG; zero disables the threshold")
	minAnswerability := flags.Float64("min-answerability", 0, "minimum candidate answerability accuracy; zero disables the threshold")
	maxLatencyP95MS := flags.Float64("max-p95-ms", 0, "maximum candidate P95 latency in milliseconds; zero disables the threshold")
	_ = flags.Parse(args)
	baseReport := evaluate(root, runtime, *baseline, *split)
	candidateReport := evaluate(root, runtime, *candidate, *split)
	policy := evaluation.GatePolicy{
		FailOnRegression: *failOnRegression,
		MinHitRate:       *minHitRate,
		MinMRR:           *minMRR,
		MinRecall:        *minRecall,
		MinNDCG:          *minNDCG,
		MinAnswerability: *minAnswerability,
		MaxLatencyP95MS:  *maxLatencyP95MS,
	}
	violations := evaluation.CheckGate(baseReport, candidateReport, policy)
	comparison := struct {
		Baseline   evaluation.Report          `json:"baseline"`
		Candidate  evaluation.Report          `json:"candidate"`
		Delta      map[string]float64         `json:"delta"`
		Gate       evaluation.GatePolicy      `json:"gate_policy"`
		Violations []evaluation.GateViolation `json:"violations,omitempty"`
	}{
		Baseline: baseReport, Candidate: candidateReport,
		Delta: map[string]float64{
			"hit_rate_at_k":        candidateReport.HitRate - baseReport.HitRate,
			"mrr":                  candidateReport.MRR - baseReport.MRR,
			"document_recall_at_k": candidateReport.Recall - baseReport.Recall,
			"precision_at_k":       candidateReport.Precision - baseReport.Precision,
			"ndcg_at_k":            candidateReport.NDCG - baseReport.NDCG,
			"latency_p95_ms":       candidateReport.LatencyP95MS - baseReport.LatencyP95MS,
		},
		Gate: policy, Violations: violations,
	}
	if *jsonOutput {
		writeJSON(comparison)
		if len(violations) > 0 {
			os.Exit(1)
		}
		return
	}
	printReport(baseReport)
	printReport(candidateReport)
	fmt.Printf("delta hit_rate=%+.3f mrr=%+.3f recall=%+.3f precision=%+.3f ndcg=%+.3f p95_ms=%+.3f\n",
		comparison.Delta["hit_rate_at_k"], comparison.Delta["mrr"], comparison.Delta["document_recall_at_k"],
		comparison.Delta["precision_at_k"], comparison.Delta["ndcg_at_k"], comparison.Delta["latency_p95_ms"],
	)
	if policy.Enabled() {
		if len(violations) > 0 {
			fmt.Printf("gate=failed violations=%d\n", len(violations))
			for _, violation := range violations {
				fmt.Printf("  %s: %s candidate=%.3f baseline=%.3f limit=%.3f\n", violation.Metric, violation.Reason, violation.Candidate, violation.Baseline, violation.Limit)
			}
			os.Exit(1)
		}
		fmt.Println("gate=passed")
	}
}

func runDatasetEval(root string, args []string) {
	flags := flag.NewFlagSet("dataset-eval", flag.ExitOnError)
	suitePath := flags.String("suite", filepath.Join(root, "datasets", "search-harness", "enterprise-search-v1.json"), "dataset search suite")
	baseURL := flags.String("api", environmentOr("RAGLAB_API_URL", "http://127.0.0.1:8080"), "enterprise lab API base URL")
	seed := flags.Bool("seed", true, "idempotently seed the suite documents through the lifecycle API")
	jsonReport := flags.String("json-report", filepath.Join(root, "eval", "reports", "dataset-search-latest.json"), "JSON report path")
	markdownReport := flags.String("markdown-report", filepath.Join(root, "eval", "reports", "dataset-search-latest.md"), "Markdown report path")
	alicePassword := flags.String("alice-password", environmentOr("RAGLAB_ALICE_PASSWORD", "RagLab-Alice-2026!"), "local lab Alice password")
	bobPassword := flags.String("bob-password", environmentOr("RAGLAB_BOB_PASSWORD", "RagLab-Bob-2026!"), "local lab Bob password")
	timeout := flags.Duration("timeout", 15*time.Minute, "whole-suite timeout")
	_ = flags.Parse(args)

	suite, err := searchharness.Load(*suitePath)
	if err != nil {
		fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	report, err := (searchharness.Runner{
		BaseURL: *baseURL,
		Passwords: map[string]string{
			"alice": *alicePassword,
			"bob":   *bobPassword,
		},
	}).Run(ctx, suite, *seed)
	if err != nil {
		fatal(err)
	}
	jsonData, err := searchharness.MarshalReport(report)
	if err != nil {
		fatal(err)
	}
	for path, data := range map[string][]byte{
		*jsonReport:     append(jsonData, '\n'),
		*markdownReport: []byte(searchharness.Markdown(report)),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			fatal(err)
		}
	}
	fmt.Printf("dataset_search_suite=%s passed=%t cases=%d failed=%d hit_rate@k=%.3f mrr=%.3f unauthorized=%d filter_violations=%d contract_violations=%d p95_ms=%.1f\n",
		report.Suite, report.Passed, report.Cases, report.FailedCases, report.HitRateAtK, report.MRR,
		report.UnauthorizedRetrievals, report.FilterViolations, report.ContractViolations, report.LatencyP95MS)
	fmt.Printf("json_report=%s\nmarkdown_report=%s\n", *jsonReport, *markdownReport)
	if !report.Passed {
		os.Exit(1)
	}
}

func runAnswerEval(root string, args []string) {
	flags := flag.NewFlagSet("answer-eval", flag.ExitOnError)
	searchSuitePath := flags.String("search-suite", filepath.Join(root, "datasets", "search-harness", "enterprise-search-v1.json"), "preflight search suite and seed documents")
	answerSuitePath := flags.String("suite", filepath.Join(root, "datasets", "answer-harness", "grounded-answer-v1.json"), "grounded answer suite")
	baseURL := flags.String("api", environmentOr("RAGLAB_API_URL", "http://127.0.0.1:8080"), "enterprise lab API base URL")
	jsonReport := flags.String("json-report", filepath.Join(root, "eval", "reports", "grounded-answer-latest.json"), "JSON report path")
	markdownReport := flags.String("markdown-report", filepath.Join(root, "eval", "reports", "grounded-answer-latest.md"), "Markdown report path")
	alicePassword := flags.String("alice-password", environmentOr("RAGLAB_ALICE_PASSWORD", "RagLab-Alice-2026!"), "local lab Alice password")
	bobPassword := flags.String("bob-password", environmentOr("RAGLAB_BOB_PASSWORD", "RagLab-Bob-2026!"), "local lab Bob password")
	promptCost := flags.Float64("prompt-cost-per-1m-usd", environmentFloat("RAGLAB_PROMPT_COST_PER_1M_USD", 0), "input token cost in USD per 1M tokens; zero leaves cost unconfigured")
	completionCost := flags.Float64("completion-cost-per-1m-usd", environmentFloat("RAGLAB_COMPLETION_COST_PER_1M_USD", 0), "output token cost in USD per 1M tokens; zero leaves cost unconfigured")
	stream := flags.Bool("stream", environmentBool("RAGLAB_ANSWER_EVAL_STREAM", false), "evaluate the SSE answer stream instead of the JSON answer endpoint")
	timeout := flags.Duration("timeout", 15*time.Minute, "whole-suite timeout")
	_ = flags.Parse(args)
	passwords := map[string]string{"alice": *alicePassword, "bob": *bobPassword}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	searchSuite, err := searchharness.Load(*searchSuitePath)
	if err != nil {
		fatal(err)
	}
	preflight, err := (searchharness.Runner{BaseURL: *baseURL, Passwords: passwords}).Run(ctx, searchSuite, true)
	if err != nil {
		fatal(err)
	}
	if !preflight.Passed {
		fatal(fmt.Errorf("search preflight failed with %d cases", preflight.FailedCases))
	}
	answerSuite, err := answereval.Load(*answerSuitePath)
	if err != nil {
		fatal(err)
	}
	report, err := (answereval.Runner{
		BaseURL: *baseURL, Passwords: passwords,
		Cost:   answereval.CostConfig{PromptPer1MUSD: *promptCost, CompletionPer1MUSD: *completionCost},
		Stream: *stream,
	}).Run(ctx, answerSuite)
	if err != nil {
		fatal(err)
	}
	jsonData, err := answereval.MarshalReport(report)
	if err != nil {
		fatal(err)
	}
	for path, data := range map[string][]byte{
		*jsonReport:     append(jsonData, '\n'),
		*markdownReport: []byte(answereval.Markdown(report)),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			fatal(err)
		}
	}
	fmt.Printf("grounded_answer_suite=%s passed=%t cases=%d failed=%d answerability=%.3f fact_coverage=%.3f forbidden=%d citations=%d unauthorized=%d p95_ms=%.1f tokens=%d/%d estimated_cost_usd=%.6f providers=%s models=%s\n",
		report.Suite, report.Passed, report.Cases, report.FailedCases, report.AnswerabilityAccuracy,
		report.RequiredFactCoverage, report.ForbiddenFactHits, report.CitationViolations,
		report.UnauthorizedRetrievals, report.LatencyP95MS, report.PromptTokens, report.OutputTokens,
		report.EstimatedCostUSD, strings.Join(report.Providers, ","), strings.Join(report.Models, ","))
	fmt.Printf("json_report=%s\nmarkdown_report=%s\n", *jsonReport, *markdownReport)
	if !report.Passed {
		os.Exit(1)
	}
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
	fmt.Fprintln(os.Stderr, "usage: raglab <validate|ingest|query|eval|compare|dataset-eval|answer-eval|serve-embedding|milvus-seed|serve-lab> [flags]")
}
