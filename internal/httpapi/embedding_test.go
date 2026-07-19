package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dingpuyu/rag-evolution-lab/internal/embeddinglab"
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
