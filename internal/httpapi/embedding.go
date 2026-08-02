package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/dingpuyu/rag-evolution-lab/internal/agent"
	"github.com/dingpuyu/rag-evolution-lab/internal/datasetaccess"
	"github.com/dingpuyu/rag-evolution-lab/internal/embeddinglab"
	"github.com/dingpuyu/rag-evolution-lab/internal/generation"
	"github.com/dingpuyu/rag-evolution-lab/internal/knowledgegateway"
	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
	"github.com/dingpuyu/rag-evolution-lab/internal/scalebench"
)

const maxRequestBytes = 64 << 10

type EmbeddingAPI struct {
	service *embeddinglab.Service
}

func NewEmbeddingHandler(service *embeddinglab.Service) (http.Handler, error) {
	if service == nil {
		return nil, fmt.Errorf("embedding service must not be nil")
	}
	api := &EmbeddingAPI{service: service}
	mux := http.NewServeMux()
	registerEmbeddingRoutes(mux, api)
	return localDevelopmentCORS(mux), nil
}

func NewLabHandler(embeddingService *embeddinglab.Service, milvusService *milvus.Service, scaleServices ...*scalebench.DemoService) (http.Handler, error) {
	var scaleService *scalebench.DemoService
	if len(scaleServices) > 0 {
		scaleService = scaleServices[0]
	}
	return newLabHandler(embeddingService, milvusService, scaleService, nil, EnterpriseOptions{})
}

func NewEnterpriseLabHandler(embeddingService *embeddinglab.Service, milvusService *milvus.Service, scaleService *scalebench.DemoService, options EnterpriseOptions, lifecycleServices ...*milvus.LifecycleService) (http.Handler, error) {
	if options.Verifier == nil || options.Audit == nil {
		return nil, fmt.Errorf("enterprise lab requires auth verifier and audit log")
	}
	var lifecycleService *milvus.LifecycleService
	if len(lifecycleServices) > 0 {
		lifecycleService = lifecycleServices[0]
	}
	return newLabHandler(embeddingService, milvusService, scaleService, lifecycleService, options)
}

func newLabHandler(embeddingService *embeddinglab.Service, milvusService *milvus.Service, scaleService *scalebench.DemoService, lifecycleService *milvus.LifecycleService, enterprise EnterpriseOptions) (http.Handler, error) {
	if embeddingService == nil {
		return nil, fmt.Errorf("embedding service must not be nil")
	}
	if milvusService == nil {
		return nil, fmt.Errorf("Milvus service must not be nil")
	}
	mux := http.NewServeMux()
	datasetStore := enterprise.DatasetStore
	if datasetStore == nil {
		datasetStore = datasetaccess.Defaults()
	}
	registerEmbeddingRoutes(mux, &EmbeddingAPI{service: embeddingService})
	vectorAPI := &MilvusAPI{service: milvusService, lifecycle: lifecycleService}
	mux.HandleFunc("GET /api/v1/milvus/status", vectorAPI.status)
	catalog := http.Handler(http.HandlerFunc(vectorAPI.catalog))
	vectorSearch := http.Handler(http.HandlerFunc(vectorAPI.search))
	var authenticator *authAPI
	if enterprise.Verifier != nil {
		authenticator = &authAPI{
			verifier: enterprise.Verifier, credentials: enterprise.CredentialVerifier, devIssuer: enterprise.DevIssuer,
			accounts: enterprise.LocalAccounts, audit: enterprise.Audit,
		}
		if enterprise.DevIssuer != nil {
			mux.HandleFunc("POST /api/v1/auth/dev-token", authenticator.devToken)
		}
		if enterprise.LocalAccounts != nil {
			mux.HandleFunc("POST /api/v1/auth/register", authenticator.register)
			mux.HandleFunc("POST /api/v1/auth/login", authenticator.login)
		}
		mux.Handle("GET /api/v1/auth/me", authenticator.requireIdentity(http.HandlerFunc(authenticator.me)))
		mux.Handle("GET /api/v1/audit/recent", authenticator.requireIdentity(http.HandlerFunc(authenticator.recentAudit)))
		catalog = authenticator.requireIdentity(catalog)
		vectorSearch = authenticator.requireIdentity(vectorSearch)
	}
	mux.Handle("GET /api/v1/milvus/catalog", catalog)
	mux.Handle("POST /api/v1/milvus/search", vectorSearch)
	if scaleService != nil {
		scaleAPI := &ScaleAPI{service: scaleService}
		mux.HandleFunc("GET /api/v1/milvus/scale/status", scaleAPI.status)
		scaleSearch := http.Handler(http.HandlerFunc(scaleAPI.search))
		if authenticator != nil {
			scaleSearch = authenticator.requireIdentity(scaleSearch)
		}
		mux.Handle("POST /api/v1/milvus/scale/search", scaleSearch)
	}
	if lifecycleService != nil {
		if authenticator == nil {
			return nil, fmt.Errorf("lifecycle API requires enterprise authentication")
		}
		lifecycleAPI := &LifecycleAPI{service: lifecycleService}
		mux.Handle("GET /api/v1/milvus/lifecycle/status", authenticator.requireIdentity(http.HandlerFunc(lifecycleAPI.status)))
		mux.Handle("POST /api/v1/milvus/lifecycle/apply", authenticator.requireIdentity(http.HandlerFunc(lifecycleAPI.apply)))
		mux.Handle("POST /api/v1/milvus/lifecycle/search", authenticator.requireIdentity(http.HandlerFunc(lifecycleAPI.search)))
		generator := enterprise.Generator
		if generator == nil {
			generator = generation.ExtractiveGenerator{}
		}
		answerService, answerErr := generation.NewServiceWithOptions(lifecycleService, generator, generation.Options{GeneralGenerator: generator})
		if answerErr != nil {
			return nil, answerErr
		}
		datasetAPI := &DatasetAPI{store: datasetStore, service: lifecycleService, answerService: answerService}
		mux.Handle("GET /api/v1/datasets", authenticator.requireIdentity(http.HandlerFunc(datasetAPI.list)))
		mux.Handle("POST /api/v1/datasets", authenticator.requireIdentity(http.HandlerFunc(datasetAPI.create)))
		mux.Handle("POST /api/v1/datasets/{dataset_id}/documents", authenticator.requireIdentity(http.HandlerFunc(datasetAPI.document)))
		mux.Handle("POST /api/v1/datasets/{dataset_id}/documents/preview", authenticator.requireIdentity(http.HandlerFunc(datasetAPI.previewDocument)))
		mux.Handle("POST /api/v1/datasets/{dataset_id}/search", authenticator.requireIdentity(http.HandlerFunc(datasetAPI.search)))
		mux.Handle("POST /api/v1/datasets/{dataset_id}/answer", authenticator.requireIdentity(http.HandlerFunc(datasetAPI.answer)))
		mux.Handle("POST /api/v1/datasets/{dataset_id}/answer/stream", authenticator.requireIdentity(http.HandlerFunc(datasetAPI.answerStream)))
		mux.Handle("GET /api/v1/control-plane/status", authenticator.requireIdentity(http.HandlerFunc(datasetAPI.status)))
		mux.Handle("GET /api/v1/memberships", authenticator.requireIdentity(http.HandlerFunc(datasetAPI.members)))
		if enterprise.ApplicationStore != nil {
			applicationAPI := &ApplicationAPI{store: enterprise.ApplicationStore, datasetStore: datasetStore}
			mux.Handle("GET /api/v1/apps", authenticator.requireIdentity(http.HandlerFunc(applicationAPI.list)))
			mux.Handle("POST /api/v1/apps", authenticator.requireIdentity(http.HandlerFunc(applicationAPI.create)))
			mux.Handle("GET /api/v1/apps/{app_id}/environments", authenticator.requireIdentity(http.HandlerFunc(applicationAPI.environments)))
			mux.Handle("POST /api/v1/apps/{app_id}/environments", authenticator.requireIdentity(http.HandlerFunc(applicationAPI.createEnvironment)))
			mux.Handle("GET /api/v1/apps/{app_id}/bindings", authenticator.requireIdentity(http.HandlerFunc(applicationAPI.bindings)))
			mux.Handle("POST /api/v1/apps/{app_id}/bindings", authenticator.requireIdentity(http.HandlerFunc(applicationAPI.createBinding)))
			if enterprise.CredentialStore != nil {
				credentialAPI := &CredentialAPI{store: enterprise.CredentialStore}
				mux.Handle("GET /api/v1/apps/{app_id}/credentials", authenticator.requireIdentity(http.HandlerFunc(credentialAPI.list)))
				mux.Handle("POST /api/v1/apps/{app_id}/credentials", authenticator.requireIdentity(http.HandlerFunc(credentialAPI.create)))
				mux.Handle("POST /api/v1/apps/{app_id}/credentials/{credential_id}/revoke", authenticator.requireIdentity(http.HandlerFunc(credentialAPI.revoke)))
			}
			gateway, gatewayErr := knowledgegateway.NewWithOptions(lifecycleService, datasetStore, enterprise.ApplicationStore, generator, knowledgegateway.Options{
				IndexStore: enterprise.IndexStore, TraceStore: enterprise.QueryTraceStore, Tracer: enterprise.Tracer,
				Cost: enterprise.Cost, Limiter: enterprise.Limiter,
			})
			if gatewayErr != nil {
				return nil, gatewayErr
			}
			gatewayAPI := &KnowledgeGatewayAPI{service: gateway}
			mux.Handle("POST /api/v1/apps/{app_id}/query", authenticator.requireIdentity(http.HandlerFunc(gatewayAPI.query)))
			mux.Handle("POST /api/v1/apps/{app_id}/answer", authenticator.requireIdentity(http.HandlerFunc(gatewayAPI.answer)))
			mux.Handle("POST /api/v1/apps/{app_id}/answer/stream", authenticator.requireIdentity(http.HandlerFunc(gatewayAPI.answerStream)))
			planner := enterprise.AgentPlanner
			if planner == nil {
				planner = agent.NewPlanner(generator)
			}
			agentService, agentErr := agent.NewService(agent.Config{
				Planner: planner,
				Tools: []agent.Tool{
					agent.KnowledgeAnswerTool{Service: gateway},
					agent.ServiceStatusTool{Reader: agent.StaticServiceStatus{
						"acmecloud": {Service: "acmecloud", Status: "operational", Message: "核心服务当前运行正常"},
					}},
					agent.AccountAccessTool{}, agent.TicketDraftTool{},
				},
			})
			if agentErr != nil {
				return nil, agentErr
			}
			agentAPI := &AgentAPI{service: agentService}
			mux.Handle("POST /api/v1/apps/{app_id}/agent/answer", authenticator.requireIdentity(http.HandlerFunc(agentAPI.answer)))
			if enterprise.QueryTraceStore != nil {
				gatewayAPI.traces = enterprise.QueryTraceStore
				mux.Handle("GET /api/v1/apps/{app_id}/traces/{trace_id}", authenticator.requireIdentity(http.HandlerFunc(gatewayAPI.trace)))
			}
			if enterprise.IndexStore != nil {
				indexAPI := &IndexAPI{store: enterprise.IndexStore, service: lifecycleService}
				mux.Handle("GET /api/v1/apps/{app_id}/environments/{environment_id}/indexes", authenticator.requireIdentity(http.HandlerFunc(indexAPI.list)))
				mux.Handle("POST /api/v1/apps/{app_id}/environments/{environment_id}/indexes/publish", authenticator.requireIdentity(http.HandlerFunc(indexAPI.publish)))
				mux.Handle("POST /api/v1/apps/{app_id}/environments/{environment_id}/indexes/rollback", authenticator.requireIdentity(http.HandlerFunc(indexAPI.rollback)))
			}
			if enterprise.IndexBuilds != nil {
				buildAPI := &IndexBuildAPI{service: enterprise.IndexBuilds}
				mux.Handle("GET /api/v1/apps/{app_id}/environments/{environment_id}/index-builds", authenticator.requireIdentity(http.HandlerFunc(buildAPI.list)))
				mux.Handle("POST /api/v1/apps/{app_id}/environments/{environment_id}/index-builds", authenticator.requireIdentity(http.HandlerFunc(buildAPI.submit)))
				mux.Handle("GET /api/v1/apps/{app_id}/index-builds/{build_id}", authenticator.requireIdentity(http.HandlerFunc(buildAPI.detail)))
			}
		}
	}
	if enterprise.IngestionJobs != nil {
		if authenticator == nil {
			return nil, fmt.Errorf("ingestion job API requires enterprise authentication")
		}
		ingestionAPI := &IngestionAPI{service: enterprise.IngestionJobs, datasetStore: datasetStore}
		mux.Handle("GET /api/v1/ingestion/jobs", authenticator.requireIdentity(http.HandlerFunc(ingestionAPI.list)))
		mux.Handle("POST /api/v1/ingestion/jobs", authenticator.requireIdentity(http.HandlerFunc(ingestionAPI.submit)))
		mux.Handle("GET /api/v1/ingestion/jobs/{job_id}", authenticator.requireIdentity(http.HandlerFunc(ingestionAPI.detail)))
		mux.Handle("POST /api/v1/ingestion/jobs/{job_id}/retry", authenticator.requireIdentity(http.HandlerFunc(ingestionAPI.retry)))
		mux.Handle("POST /api/v1/ingestion/jobs/{job_id}/cancel", authenticator.requireIdentity(http.HandlerFunc(ingestionAPI.cancel)))
		mux.Handle("GET /api/v1/datasets/{dataset_id}/ingestion/jobs", authenticator.requireIdentity(http.HandlerFunc(ingestionAPI.listDataset)))
		mux.Handle("POST /api/v1/datasets/{dataset_id}/ingestion/jobs", authenticator.requireIdentity(http.HandlerFunc(ingestionAPI.submitDataset)))
		mux.Handle("GET /api/v1/datasets/{dataset_id}/ingestion/jobs/{job_id}", authenticator.requireIdentity(http.HandlerFunc(ingestionAPI.detailDataset)))
		mux.Handle("POST /api/v1/datasets/{dataset_id}/ingestion/jobs/{job_id}/retry", authenticator.requireIdentity(http.HandlerFunc(ingestionAPI.retryDataset)))
		mux.Handle("POST /api/v1/datasets/{dataset_id}/ingestion/jobs/{job_id}/cancel", authenticator.requireIdentity(http.HandlerFunc(ingestionAPI.cancelDataset)))
	}
	return localDevelopmentCORS(mux), nil
}

func registerEmbeddingRoutes(mux *http.ServeMux, api *EmbeddingAPI) {
	mux.HandleFunc("GET /healthz", api.health)
	mux.HandleFunc("GET /api/v1/embeddings/info", api.info)
	mux.HandleFunc("POST /api/v1/embeddings/similarity", api.similarity)
}

type MilvusAPI struct {
	service   *milvus.Service
	lifecycle *milvus.LifecycleService
}

type ScaleAPI struct {
	service *scalebench.DemoService
}

func (a *MilvusAPI) status(writer http.ResponseWriter, request *http.Request) {
	status, err := a.service.Status(request.Context())
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "milvus_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, status)
}

func (a *MilvusAPI) catalog(writer http.ResponseWriter, request *http.Request) {
	identity := identityFromContext(request.Context())
	if identity.Subject != "" && !identity.HasRole("platform_admin") {
		writeError(writer, http.StatusForbidden, "catalog_forbidden", "stored document catalog requires platform_admin")
		return
	}
	var (
		catalog milvus.Catalog
		err     error
	)
	if a.lifecycle != nil {
		catalog, err = a.lifecycle.Catalog(request.Context())
	} else {
		catalog, err = a.service.Catalog(request.Context())
	}
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "milvus_catalog_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, catalog)
}

func (a *MilvusAPI) search(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input milvus.Query
	if err := decoder.Decode(&input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := ensureEOF(decoder); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if identity := identityFromContext(request.Context()); identity.Subject != "" {
		input.Tenant = identity.TenantID
		input.Role = identity.PrimaryRole()
	}
	result, err := a.service.Search(request.Context(), input)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "vector_search_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (a *ScaleAPI) status(writer http.ResponseWriter, request *http.Request) {
	status, err := a.service.Status(request.Context())
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "scale_milvus_unavailable", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, status)
}

func (a *ScaleAPI) search(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input scalebench.DemoQuery
	if err := decoder.Decode(&input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := ensureEOF(decoder); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	identity := identityFromContext(request.Context())
	var result scalebench.DemoSearchResult
	var err error
	if identity.Subject == "" {
		result, err = a.service.Search(request.Context(), input)
	} else {
		result, err = a.service.SearchAs(request.Context(), input, scalebench.DemoIdentity{TenantID: identity.TenantID, Roles: identity.Roles})
	}
	if err != nil {
		if errors.Is(err, scalebench.ErrForbidden) {
			writeError(writer, http.StatusForbidden, "forbidden", err.Error())
			return
		}
		writeError(writer, http.StatusUnprocessableEntity, "scale_search_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func (a *EmbeddingAPI) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *EmbeddingAPI) info(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]any{
		"embedder": a.service.EmbedderName(),
		"modes":    []string{embeddinglab.ModeSymmetric, embeddinglab.ModeQueryDocument},
		"endpoint": "/api/v1/embeddings/similarity",
	})
}

func (a *EmbeddingAPI) similarity(writer http.ResponseWriter, request *http.Request) {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input embeddinglab.SimilarityRequest
	if err := decoder.Decode(&input); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(writer, http.StatusRequestEntityTooLarge, "request_too_large", fmt.Sprintf("request body must not exceed %d bytes", maxRequestBytes))
			return
		}
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := ensureEOF(decoder); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	response, err := a.service.Similarity(request.Context(), input)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "embedding_failed", err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func ensureEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("request body must contain exactly one JSON object")
		}
		return err
	}
	return nil
}

func writeError(writer http.ResponseWriter, status int, code, message string) {
	writeJSON(writer, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

// The demo UI and API run on different localhost ports during development.
// Do not reflect arbitrary origins: only localhost/127.0.0.1 are allowed.
func localDevelopmentCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		origin := request.Header.Get("Origin")
		if strings.HasPrefix(origin, "http://localhost:") || strings.HasPrefix(origin, "http://127.0.0.1:") {
			writer.Header().Set("Access-Control-Allow-Origin", origin)
			writer.Header().Set("Vary", "Origin")
			writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			writer.Header().Set("Access-Control-Expose-Headers", "X-Request-ID")
			writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		}
		if request.Method == http.MethodOptions {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(writer, request)
	})
}
