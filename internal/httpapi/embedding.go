package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/dingpuyu/rag-evolution-lab/internal/embeddinglab"
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
	mux.HandleFunc("GET /healthz", api.health)
	mux.HandleFunc("GET /api/v1/embeddings/info", api.info)
	mux.HandleFunc("POST /api/v1/embeddings/similarity", api.similarity)
	return localDevelopmentCORS(mux), nil
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
			writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		}
		if request.Method == http.MethodOptions {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(writer, request)
	})
}
