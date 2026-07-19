package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dingpuyu/rag-evolution-lab/internal/embeddinglab"
	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
	"github.com/dingpuyu/rag-evolution-lab/internal/retrieval"
)

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	service, err := embeddinglab.New(retrieval.HashEmbedder{Dimensions: 16})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewEmbeddingHandler(service)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestLabHandlerExposesMilvusStatusAndSearch(t *testing.T) {
	milvusServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v2/vectordb/collections/describe":
			_, _ = writer.Write([]byte(`{"code":0,"data":{"collectionID":42,"collectionName":"chunks","load":"LoadStateLoaded","fields":[{"name":"embedding","type":"FloatVector","params":[{"key":"dim","value":"8"}]}],"indexes":[{"fieldName":"embedding","indexName":"embedding_hnsw","metricType":"COSINE"}]}}`))
		case "/v2/vectordb/collections/get_stats":
			_, _ = writer.Write([]byte(`{"code":0,"data":{"rowCount":"38"}}`))
		case "/v2/vectordb/indexes/describe":
			_, _ = writer.Write([]byte(`{"code":0,"data":[{"fieldName":"embedding","indexName":"embedding_hnsw","indexType":"HNSW","metricType":"COSINE"}]}`))
		case "/v2/vectordb/entities/search":
			_, _ = writer.Write([]byte(`{"code":0,"data":[{"chunk_id":"identity#c001","title":"SSO Guide","distance":0.93}]}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer milvusServer.Close()

	embedder := retrieval.HashEmbedder{Dimensions: 8}
	embeddingService, err := embeddinglab.New(embedder)
	if err != nil {
		t.Fatal(err)
	}
	vectorService, err := milvus.NewService(milvus.NewClient(milvus.Config{BaseURL: milvusServer.URL}), embedder, "chunks")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewLabHandler(embeddingService, vectorService)
	if err != nil {
		t.Fatal(err)
	}

	statusResponse := httptest.NewRecorder()
	handler.ServeHTTP(statusResponse, httptest.NewRequest(http.MethodGet, "/api/v1/milvus/status", nil))
	if statusResponse.Code != http.StatusOK || !strings.Contains(statusResponse.Body.String(), `"row_count":38`) || !strings.Contains(statusResponse.Body.String(), `"index_type":"HNSW"`) {
		t.Fatalf("unexpected status response %d: %s", statusResponse.Code, statusResponse.Body.String())
	}

	searchResponse := httptest.NewRecorder()
	handler.ServeHTTP(searchResponse, httptest.NewRequest(http.MethodPost, "/api/v1/milvus/search", strings.NewReader(`{"query":"配置单点登录","tenant_id":"tenant_a","top_k":3}`)))
	if searchResponse.Code != http.StatusOK || !strings.Contains(searchResponse.Body.String(), `"chunk_id":"identity#c001"`) {
		t.Fatalf("unexpected search response %d: %s", searchResponse.Code, searchResponse.Body.String())
	}
}

func TestSimilarityEndpoint(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/embeddings/similarity", strings.NewReader(`{
		"text_a":"企业单点登录", "text_b":"员工只登录一次", "preview_dimensions":4
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	newTestHandler(t).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		Dimensions int `json:"dimensions"`
		VectorA    struct {
			Preview []float64 `json:"preview"`
		} `json:"vector_a"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Dimensions != 16 || len(body.VectorA.Preview) != 4 {
		t.Fatalf("unexpected body: %#v", body)
	}
}

func TestSimilarityEndpointRejectsUnknownField(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/embeddings/similarity", strings.NewReader(`{
		"text_a":"a", "text_b":"b", "unknown":true
	}`))
	response := httptest.NewRecorder()
	newTestHandler(t).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status %d: %s", response.Code, response.Body.String())
	}
}

func TestEmbeddingAPICORSOnlyAllowsLocalhost(t *testing.T) {
	for _, test := range []struct {
		origin  string
		allowed bool
	}{
		{origin: "http://localhost:3000", allowed: true},
		{origin: "https://attacker.example", allowed: false},
	} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/embeddings/info", nil)
		request.Header.Set("Origin", test.origin)
		response := httptest.NewRecorder()
		newTestHandler(t).ServeHTTP(response, request)
		got := response.Header().Get("Access-Control-Allow-Origin")
		if (got != "") != test.allowed {
			t.Fatalf("origin %q allowed=%v, header=%q", test.origin, test.allowed, got)
		}
	}
}
