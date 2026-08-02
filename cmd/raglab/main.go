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
	"github.com/dingpuyu/rag-evolution-lab/internal/cost"
	"github.com/dingpuyu/rag-evolution-lab/internal/dataset"
	"github.com/dingpuyu/rag-evolution-lab/internal/datasetaccess"
	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
	"github.com/dingpuyu/rag-evolution-lab/internal/embeddinglab"
	"github.com/dingpuyu/rag-evolution-lab/internal/evaluation"
	"github.com/dingpuyu/rag-evolution-lab/internal/generation"
	"github.com/dingpuyu/rag-evolution-lab/internal/httpapi"
	"github.com/dingpuyu/rag-evolution-lab/internal/indexbuild"
	"github.com/dingpuyu/rag-evolution-lab/internal/ingest"
	"github.com/dingpuyu/rag-evolution-lab/internal/ingestionjob"
	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
	"github.com/dingpuyu/rag-evolution-lab/internal/querytrace"
	"github.com/dingpuyu/rag-evolution-lab/internal/ratelimit"
	"github.com/dingpuyu/rag-evolution-lab/internal/retrieval"
	"github.com/dingpuyu/rag-evolution-lab/internal/runtimeharness"
	"github.com/dingpuyu/rag-evolution-lab/internal/scalebench"
	"github.com/dingpuyu/rag-evolution-lab/internal/searchharness"
	"github.com/dingpuyu/rag-evolution-lab/internal/telemetry"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/trace"
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
	if os.Args[1] == "enterprise-eval" {
		runEnterpriseEval(root, os.Args[2:])
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
		OllamaDimensions:  environmentIntOrZero("RAGLAB_EMBEDDING_DIMENSIONS"),
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
	backend := flags.String("backend", environmentOr("RAGLAB_EMBEDDING_BACKEND", "auto"), "embedding backend: auto, hash, ollama, or openai-compatible")
	dimensions := flags.Int("dimensions", 512, "hash embedding dimensions")
	model := flags.String("model", os.Getenv("RAGLAB_OLLAMA_MODEL"), "Ollama embedding model")
	ollamaURL := flags.String("ollama-url", os.Getenv("RAGLAB_OLLAMA_URL"), "Ollama base URL")
	openAIBaseURL := flags.String("embedding-base-url", os.Getenv("RAGLAB_EMBEDDING_BASE_URL"), "OpenAI-compatible embedding base URL, e.g. https://tokenhub.tencentmaas.com/v1")
	openAIKey := flags.String("embedding-api-key", firstNonEmptyEnv("RAGLAB_EMBEDDING_API_KEY", "QWEN_API_KEY", "DASHSCOPE_API_KEY", "TOKENHUB_API_KEY"), "OpenAI-compatible embedding API key")
	openAIModel := flags.String("embedding-model", os.Getenv("RAGLAB_EMBEDDING_MODEL"), "OpenAI-compatible embedding model")
	openAIDimensions := flags.Int("embedding-dimensions", environmentIntOrZero("RAGLAB_EMBEDDING_DIMENSIONS"), "optional expected embedding dimensions")
	embeddingBatchSize := flags.Int("embedding-batch-size", environmentIntOrZero("RAGLAB_EMBEDDING_BATCH_SIZE"), "maximum texts per OpenAI-compatible embedding request")
	queryInstruction := flags.String("query-instruction", os.Getenv("RAGLAB_QUERY_INSTRUCTION"), "instruction used only in query_document mode")
	_ = flags.Parse(args)

	embedder, err := newLabEmbedder(labEmbedderConfig{
		Backend: *backend, OllamaURL: *ollamaURL, OllamaModel: *model, QueryInstruction: *queryInstruction,
		OllamaDimensions: *openAIDimensions,
		HashDimensions:   *dimensions, OpenAIBaseURL: *openAIBaseURL, OpenAIAPIKey: *openAIKey,
		OpenAIModel: *openAIModel, OpenAIDimensions: *openAIDimensions, OpenAIBatchSize: *embeddingBatchSize,
	})
	if err != nil {
		fatal(err)
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
	backend := flags.String("embedding-backend", environmentOr("RAGLAB_EMBEDDING_BACKEND", "ollama"), "embedding backend: ollama, hash, or openai-compatible")
	model := flags.String("model", environmentOr("RAGLAB_OLLAMA_MODEL", "qwen3-embedding:4b-local"), "Ollama embedding model")
	ollamaURL := flags.String("ollama-url", os.Getenv("RAGLAB_OLLAMA_URL"), "Ollama base URL")
	baseURL := flags.String("embedding-base-url", os.Getenv("RAGLAB_EMBEDDING_BASE_URL"), "OpenAI-compatible embedding base URL")
	apiKey := flags.String("embedding-api-key", firstNonEmptyEnv("RAGLAB_EMBEDDING_API_KEY", "QWEN_API_KEY", "DASHSCOPE_API_KEY", "TOKENHUB_API_KEY"), "OpenAI-compatible embedding API key")
	embeddingModel := flags.String("embedding-model", os.Getenv("RAGLAB_EMBEDDING_MODEL"), "OpenAI-compatible embedding model")
	embeddingDimensions := flags.Int("embedding-dimensions", environmentIntOrZero("RAGLAB_EMBEDDING_DIMENSIONS"), "optional expected embedding dimensions")
	embeddingBatchSize := flags.Int("embedding-batch-size", environmentIntOrZero("RAGLAB_EMBEDDING_BATCH_SIZE"), "maximum texts per OpenAI-compatible embedding request")
	hashDimensions := flags.Int("hash-dimensions", environmentInt("RAGLAB_HASH_EMBEDDING_DIMENSIONS", 512), "hash embedding dimensions")
	queryInstruction := flags.String("query-instruction", os.Getenv("RAGLAB_QUERY_INSTRUCTION"), "instruction used only for query embeddings")
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
	embedder, err := newLabEmbedder(labEmbedderConfig{
		Backend: *backend, OllamaURL: *ollamaURL, OllamaModel: *model, QueryInstruction: *queryInstruction,
		OllamaDimensions: *embeddingDimensions,
		HashDimensions:   *hashDimensions, OpenAIBaseURL: *baseURL, OpenAIAPIKey: *apiKey,
		OpenAIModel: *embeddingModel, OpenAIDimensions: *embeddingDimensions, OpenAIBatchSize: *embeddingBatchSize,
	})
	if err != nil {
		fatal(err)
	}
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
	// The enterprise Dataset API and Application Gateway query the lifecycle
	// alias rather than the legacy demo collection. Keep this command as the
	// one-command local bootstrap by applying the same corpus to that managed
	// collection as well. Re-running it is safe: revisions advance for
	// documents already present in the local lifecycle state file.
	lifecycleCollection := environmentOr("RAGLAB_LIFECYCLE_COLLECTION", "raglab_lifecycle_v1")
	lifecycleAlias := environmentOr("RAGLAB_LIFECYCLE_ALIAS", "raglab_knowledge_active")
	lifecycleState := environmentOr("RAGLAB_LIFECYCLE_STATE", "/var/lib/raglab/lifecycle/state.json")
	lifecycleVersion := environmentOr("RAGLAB_EMBEDDING_VERSION", "qwen3-embedding-4b-q4km-v1")
	lifecycle, lifecycleErr := milvus.NewLifecycleService(milvus.NewClient(milvus.Config{BaseURL: *milvusURL}), embedder, milvus.LifecycleConfig{
		Collection: lifecycleCollection, Alias: lifecycleAlias, EmbeddingVersion: lifecycleVersion,
		StatePath: lifecycleState, ChunkRunes: 700,
	})
	if lifecycleErr != nil {
		fatal(lifecycleErr)
	}
	lifecycleResults := make([]milvus.LifecycleResult, 0, len(documents))
	for _, document := range documents {
		revision := int64(1)
		if state, ok := lifecycle.Status().Documents[document.ID]; ok && state.Revision >= revision {
			revision = state.Revision + 1
		}
		change := milvus.LifecycleChange{
			EventID: fmt.Sprintf("corpus-seed-%s-r%d", document.ID, revision), Operation: milvus.OperationUpsert,
			Revision: revision, DocumentID: document.ID,
			Document: &milvus.LifecycleDocument{
				ID: document.ID, Title: document.Title, Content: document.Content, Product: document.Product,
				Version: document.Version, Status: document.Status, Visibility: document.Visibility,
				AllowedTenants: append([]string(nil), document.AllowedTenants...), AllowedRoles: append([]string(nil), document.AllowedRoles...),
			},
		}
		lifecycleResult, applyErr := lifecycle.Apply(ctx, change)
		if applyErr != nil {
			fatal(fmt.Errorf("seed lifecycle document %q: %w", document.ID, applyErr))
		}
		lifecycleResults = append(lifecycleResults, lifecycleResult)
	}
	writeJSON(map[string]any{
		"collection": result, "lifecycle_collection": lifecycleCollection, "lifecycle_alias": lifecycleAlias,
		"lifecycle_documents": len(lifecycleResults), "lifecycle_chunks": sumLifecycleChunks(lifecycleResults),
	})
}

func sumLifecycleChunks(results []milvus.LifecycleResult) int {
	total := 0
	for _, result := range results {
		total += result.CurrentChunks
	}
	return total
}

func runLabServer(args []string) {
	flags := flag.NewFlagSet("serve-lab", flag.ExitOnError)
	address := flags.String("addr", "127.0.0.1:8080", "HTTP listen address")
	model := flags.String("model", environmentOr("RAGLAB_OLLAMA_MODEL", "qwen3-embedding:4b-local"), "Ollama embedding model")
	ollamaURL := flags.String("ollama-url", os.Getenv("RAGLAB_OLLAMA_URL"), "Ollama base URL")
	queryInstruction := flags.String("query-instruction", os.Getenv("RAGLAB_QUERY_INSTRUCTION"), "query-side retrieval instruction")
	embeddingBackend := flags.String("embedding-backend", environmentOr("RAGLAB_EMBEDDING_BACKEND", "ollama"), "embedding backend: ollama, hash, or openai-compatible")
	hashDimensions := flags.Int("hash-dimensions", environmentInt("RAGLAB_HASH_EMBEDDING_DIMENSIONS", 512), "dimensions for hash embedding backend")
	embeddingBaseURL := flags.String("embedding-base-url", os.Getenv("RAGLAB_EMBEDDING_BASE_URL"), "OpenAI-compatible embedding base URL")
	embeddingAPIKey := flags.String("embedding-api-key", firstNonEmptyEnv("RAGLAB_EMBEDDING_API_KEY", "QWEN_API_KEY", "DASHSCOPE_API_KEY", "TOKENHUB_API_KEY"), "OpenAI-compatible embedding API key")
	embeddingModel := flags.String("embedding-model", os.Getenv("RAGLAB_EMBEDDING_MODEL"), "OpenAI-compatible embedding model")
	embeddingDimensions := flags.Int("embedding-dimensions", environmentIntOrZero("RAGLAB_EMBEDDING_DIMENSIONS"), "optional expected embedding dimensions")
	embeddingBatchSize := flags.Int("embedding-batch-size", environmentIntOrZero("RAGLAB_EMBEDDING_BATCH_SIZE"), "maximum texts per OpenAI-compatible embedding request")
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
	requireOIDC := flags.Bool("require-oidc", environmentBool("RAGLAB_REQUIRE_OIDC", false), "fail startup unless enterprise OIDC/RS256 mode is configured")
	authAccounts := flags.String("auth-accounts", environmentOr("RAGLAB_AUTH_ACCOUNTS", "data/auth/accounts.json"), "local-lab account store; unused in OIDC mode")
	platformAdminPassword := flags.String("platform-admin-password", environmentOr("RAGLAB_PLATFORM_ADMIN_PASSWORD", "RagLab-Platform-2026!"), "local-lab platform administrator password")
	postgresURL := flags.String("postgres-url", environmentOr("RAGLAB_POSTGRES_URL", "postgres://raglab:raglab-local@127.0.0.1:5433/raglab?sslmode=disable"), "PostgreSQL control-plane URL; set empty for in-memory fallback")
	otelEndpoint := flags.String("otel-endpoint", os.Getenv("RAGLAB_OTEL_ENDPOINT"), "OTLP HTTP endpoint, e.g. localhost:4318")
	otelServiceName := flags.String("otel-service-name", environmentOr("RAGLAB_OTEL_SERVICE_NAME", "rag-evolution-lab"), "OpenTelemetry service name")
	rateBackend := flags.String("rate-limit-backend", environmentOr("RAGLAB_RATE_LIMIT_BACKEND", "memory"), "rate limiter backend: memory or redis")
	redisURL := flags.String("redis-url", environmentOr("RAGLAB_REDIS_URL", "redis://127.0.0.1:6379/0"), "Redis URL used by the shared rate limiter")
	redisPrefix := flags.String("redis-prefix", environmentOr("RAGLAB_REDIS_PREFIX", "raglab:ratelimit"), "Redis key prefix for rate limiter buckets")
	rateRPM := flags.Int("rate-limit-rpm", environmentInt("RAGLAB_RATE_LIMIT_RPM", 120), "per-tenant/application requests per minute")
	rateBurst := flags.Int("rate-limit-burst", environmentInt("RAGLAB_RATE_LIMIT_BURST", 30), "per-tenant/application burst capacity")
	tokenQuota := flags.Int("token-quota-per-minute", environmentInt("RAGLAB_TOKEN_QUOTA_PER_MINUTE", 100000), "per-tenant/application token quota per minute")
	costInput := flags.Float64("cost-input-usd-per-1m", environmentFloat("RAGLAB_COST_INPUT_USD_PER_1M", 0), "input token cost in USD per million")
	costOutput := flags.Float64("cost-output-usd-per-1m", environmentFloat("RAGLAB_COST_OUTPUT_USD_PER_1M", 0), "output token cost in USD per million")
	_ = flags.Parse(args)
	telemetryProvider, telemetryErr := telemetry.Setup(context.Background(), telemetry.Config{Enabled: strings.TrimSpace(*otelEndpoint) != "", Endpoint: *otelEndpoint, ServiceName: *otelServiceName})
	if telemetryErr != nil {
		fatal(telemetryErr)
	}
	defer telemetryProvider.Shutdown(context.Background())
	var gatewayTracer trace.Tracer = telemetry.Tracer("raglab.knowledge-gateway")
	gatewayCost := &cost.Calculator{InputPerMillion: *costInput, OutputPerMillion: *costOutput}
	ratePolicy := ratelimit.Policy{RequestsPerMinute: *rateRPM, Burst: *rateBurst, TokensPerMinute: *tokenQuota}
	var gatewayLimiter ratelimit.Gate
	rateBackendName := strings.ToLower(strings.TrimSpace(*rateBackend))
	switch rateBackendName {
	case "memory", "local", "":
		gatewayLimiter = ratelimit.New(ratePolicy)
	case "redis":
		options, redisErr := redis.ParseURL(*redisURL)
		if redisErr != nil {
			fatal(fmt.Errorf("parse redis URL: %w", redisErr))
		}
		if options.DialTimeout == 0 {
			options.DialTimeout = 2 * time.Second
		}
		if options.ReadTimeout == 0 {
			options.ReadTimeout = 2 * time.Second
		}
		if options.WriteTimeout == 0 {
			options.WriteTimeout = 2 * time.Second
		}
		redisClient := redis.NewClient(options)
		pingContext, cancelPing := context.WithTimeout(context.Background(), 5*time.Second)
		redisErr = redisClient.Ping(pingContext).Err()
		cancelPing()
		if redisErr != nil {
			_ = redisClient.Close()
			fatal(fmt.Errorf("connect rate limiter Redis: %w", redisErr))
		}
		gatewayLimiter = ratelimit.NewRedis(redisClient, ratePolicy, *redisPrefix)
		defer redisClient.Close()
	default:
		fatal(fmt.Errorf("unknown rate limiter backend %q", *rateBackend))
	}

	embedder, err := newLabEmbedder(labEmbedderConfig{
		Backend: *embeddingBackend, OllamaURL: *ollamaURL, OllamaModel: *model, QueryInstruction: *queryInstruction,
		OllamaDimensions: *embeddingDimensions,
		HashDimensions:   *hashDimensions,
		OpenAIBaseURL:    *embeddingBaseURL, OpenAIAPIKey: *embeddingAPIKey,
		OpenAIModel: *embeddingModel, OpenAIDimensions: *embeddingDimensions, OpenAIBatchSize: *embeddingBatchSize,
	})
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
	var datasetStore datasetaccess.Store = datasetaccess.Defaults()
	var applicationStore datasetaccess.ApplicationStore
	var indexStore datasetaccess.IndexStore
	var queryTraceStore querytrace.Store
	var indexBuildStore indexbuild.Store
	var ingestionRepository ingestionjob.Repository
	var postgresStore *datasetaccess.PostgresStore
	if strings.TrimSpace(*postgresURL) != "" {
		controlPlaneContext, cancelControlPlane := context.WithTimeout(context.Background(), 10*time.Second)
		var postgresErr error
		postgresStore, postgresErr = datasetaccess.OpenPostgres(controlPlaneContext, *postgresURL)
		cancelControlPlane()
		if postgresErr != nil {
			fatal(postgresErr)
		}
		defer postgresStore.Close()
		datasetStore = postgresStore
		applicationStore = postgresStore
		indexStore = postgresStore
		queryTraceStore = postgresStore
		indexBuildStore = postgresStore
		ingestionRepository = postgresStore.IngestionRepository()
	}
	var indexBuilds *indexbuild.Service
	if indexBuildStore != nil {
		indexBuilds, err = indexbuild.New(indexBuildStore, milvus.NewIndexBuilder(lifecycleService), indexbuild.Config{Workers: 1, QueueCapacity: 128, MaxAttempts: 3})
		if err != nil {
			fatal(err)
		}
		if err := indexBuilds.Start(context.Background()); err != nil {
			fatal(err)
		}
		defer indexBuilds.Close()
	}
	ingestionJobs, err := ingestionjob.New(lifecycleService, ingestionjob.Config{
		StatePath: *ingestionJobState, Workers: *ingestionWorkers, QueueCapacity: 1_024, MaxAttempts: 3,
		Repository: ingestionRepository,
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
			if postgresStore != nil {
				identity, authenticateErr := localAccounts.Authenticate(demo.email, demo.password)
				if authenticateErr != nil {
					fatal(authenticateErr)
				}
				if managerErr = postgresStore.ProvisionIdentity(context.Background(), identity); managerErr != nil {
					fatal(managerErr)
				}
			}
		}
	}
	if *requireOIDC && authMode != "oidc_rs256" {
		fatal(fmt.Errorf("RAGLAB_REQUIRE_OIDC is enabled but enterprise OIDC/RS256 is not configured"))
	}
	var identityProvisioner auth.IdentityProvisioner
	if devIssuer != nil && postgresStore != nil {
		// Local accounts are an explicitly local-only bootstrap path. OIDC mode
		// leaves provisioning to an invitation/admin workflow instead.
		identityProvisioner = postgresStore
	}
	handler, err := httpapi.NewEnterpriseLabHandler(embeddingService, milvusService, scaleService, httpapi.EnterpriseOptions{
		Verifier: verifier, IdentityProvisioner: identityProvisioner, DevIssuer: devIssuer, LocalAccounts: localAccounts,
		CredentialVerifier: func() auth.CredentialVerifier {
			if postgresStore, ok := datasetStore.(*datasetaccess.PostgresStore); ok {
				return postgresStore
			}
			return nil
		}(),
		Audit: auth.NewAuditLog(200), IngestionJobs: ingestionJobs, DatasetStore: datasetStore, ApplicationStore: applicationStore,
		IndexStore: indexStore, QueryTraceStore: queryTraceStore,
		IndexBuilds: indexBuilds,
		CredentialStore: func() datasetaccess.CredentialStore {
			if postgresStore, ok := datasetStore.(*datasetaccess.PostgresStore); ok {
				return postgresStore
			}
			return nil
		}(),
		Tracer: gatewayTracer, Cost: gatewayCost, Limiter: gatewayLimiter,
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

func environmentIntOrZero(name string) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		fatal(fmt.Errorf("%s must be a positive integer", name))
	}
	return parsed
}

func firstNonEmptyEnv(names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

type labEmbedderConfig struct {
	Backend          string
	OllamaURL        string
	OllamaModel      string
	OllamaDimensions int
	QueryInstruction string
	HashDimensions   int
	OpenAIBaseURL    string
	OpenAIAPIKey     string
	OpenAIModel      string
	OpenAIDimensions int
	OpenAIBatchSize  int
}

func newLabEmbedder(config labEmbedderConfig) (retrieval.Embedder, error) {
	switch strings.ToLower(strings.TrimSpace(config.Backend)) {
	case "auto":
		if strings.TrimSpace(config.OpenAIModel) != "" && strings.TrimSpace(config.OpenAIAPIKey) != "" {
			config.Backend = "openai-compatible"
		} else if strings.TrimSpace(config.OllamaModel) != "" {
			config.Backend = "ollama"
		} else {
			config.Backend = "hash"
		}
		return newLabEmbedder(config)
	case "ollama", "local":
		if strings.TrimSpace(config.OllamaModel) == "" {
			return nil, fmt.Errorf("RAGLAB_OLLAMA_MODEL is required for the ollama embedding backend")
		}
		return retrieval.OllamaEmbedder{BaseURL: config.OllamaURL, Model: config.OllamaModel, Dimensions: config.OllamaDimensions, QueryInstruction: config.QueryInstruction}, nil
	case "hash", "deterministic":
		if config.HashDimensions <= 0 {
			return nil, fmt.Errorf("hash embedding dimensions must be positive")
		}
		return retrieval.HashEmbedder{Dimensions: config.HashDimensions}, nil
	case "openai", "openai-compatible", "tokenhub":
		if strings.TrimSpace(config.OpenAIAPIKey) == "" {
			return nil, fmt.Errorf("RAGLAB_EMBEDDING_API_KEY (or QWEN_API_KEY/DASHSCOPE_API_KEY) is required for the openai-compatible embedding backend")
		}
		if strings.TrimSpace(config.OpenAIBaseURL) == "" {
			return nil, fmt.Errorf("RAGLAB_EMBEDDING_BASE_URL is required for the openai-compatible embedding backend")
		}
		if strings.TrimSpace(config.OpenAIModel) == "" {
			return nil, fmt.Errorf("RAGLAB_EMBEDDING_MODEL is required for the openai-compatible embedding backend")
		}
		return retrieval.OpenAICompatibleEmbedder{
			BaseURL: config.OpenAIBaseURL, APIKey: config.OpenAIAPIKey, Model: config.OpenAIModel,
			Dimensions: config.OpenAIDimensions, BatchSize: config.OpenAIBatchSize, QueryInstruction: config.QueryInstruction,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported embedding backend %q; use ollama, hash, or openai-compatible", config.Backend)
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

func runEnterpriseEval(root string, args []string) {
	flags := flag.NewFlagSet("enterprise-eval", flag.ExitOnError)
	baseURL := flags.String("api", environmentOr("RAGLAB_API_URL", "http://127.0.0.1:8080"), "enterprise lab API base URL")
	email := flags.String("email", environmentOr("RAGLAB_ENTERPRISE_EVAL_EMAIL", "alice@tenant-a.local"), "administrator email used by the harness")
	password := flags.String("password", environmentOr("RAGLAB_ENTERPRISE_EVAL_PASSWORD", "RagLab-Alice-2026!"), "administrator password used by the harness")
	applicationID := flags.String("app-id", environmentOr("RAGLAB_ENTERPRISE_EVAL_APP_ID", "tenant_a-support-agent"), "application boundary")
	environmentID := flags.String("environment-id", "", "application environment; defaults to <app-id>-dev")
	collection := flags.String("collection", environmentOr("RAGLAB_LIFECYCLE_COLLECTION", "raglab_lifecycle_v1"), "ready Milvus collection used for optional index build")
	crossAppID := flags.String("cross-app-id", environmentOr("RAGLAB_ENTERPRISE_EVAL_CROSS_APP_ID", "tenant_b-support-agent"), "different application used for credential isolation")
	embeddingModel := flags.String("embedding-model", "", "optional embedding model assertion for the manifest")
	embeddingVersion := flags.String("embedding-version", "", "optional embedding version assertion for the manifest")
	chunkerVersion := flags.String("chunker-version", "", "optional chunker version recorded in the manifest")
	sourceRevision := flags.Int64("source-revision", 0, "optional source revision recorded in the manifest")
	build := flags.Bool("build", false, "submit and poll an asynchronous index build (mutates control-plane state)")
	publish := flags.Bool("publish", false, "publish, supersede and rollback the built index (implies -build)")
	rateLimitRequests := flags.Int("rate-limit-requests", 0, "optional number of credential queries used to observe a 429; configure a low server burst first")
	timeout := flags.Duration("timeout", 5*time.Minute, "whole-suite timeout")
	jsonReport := flags.String("json-report", filepath.Join(root, "eval", "reports", "enterprise-runtime-latest.json"), "JSON report path")
	markdownReport := flags.String("markdown-report", filepath.Join(root, "eval", "reports", "enterprise-runtime-latest.md"), "Markdown report path")
	_ = flags.Parse(args)
	if *publish {
		*build = true
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	report, err := runtimeharness.Run(ctx, runtimeharness.Config{
		BaseURL: *baseURL, Email: *email, Password: *password, ApplicationID: *applicationID,
		EnvironmentID: *environmentID, Collection: *collection, CrossAppID: *crossAppID,
		EmbeddingModel: *embeddingModel, EmbeddingVer: *embeddingVersion, ChunkerVersion: *chunkerVersion,
		SourceRevision: *sourceRevision, Build: *build, Publish: *publish, RateLimitRequests: *rateLimitRequests, Timeout: *timeout,
	})
	if report.Cases > 0 {
		jsonData, marshalErr := runtimeharness.MarshalReport(report)
		if marshalErr != nil {
			fatal(marshalErr)
		}
		for path, data := range map[string][]byte{
			*jsonReport: append(jsonData, '\n'), *markdownReport: []byte(runtimeharness.Markdown(report)),
		} {
			if mkdirErr := os.MkdirAll(filepath.Dir(path), 0o755); mkdirErr != nil {
				fatal(mkdirErr)
			}
			if writeErr := os.WriteFile(path, data, 0o644); writeErr != nil {
				fatal(writeErr)
			}
		}
		fmt.Printf("enterprise_runtime_suite=%s passed=%t cases=%d failed=%d app=%s environment=%s\n", report.Suite, report.Passed, report.Cases, report.FailedCases, report.Application.ID, report.Application.Environment)
		fmt.Printf("json_report=%s\nmarkdown_report=%s\n", *jsonReport, *markdownReport)
	}
	if err != nil {
		fatal(err)
	}
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
	fmt.Fprintln(os.Stderr, "usage: raglab <validate|ingest|query|eval|compare|dataset-eval|answer-eval|enterprise-eval|serve-embedding|milvus-seed|serve-lab> [flags]")
}
