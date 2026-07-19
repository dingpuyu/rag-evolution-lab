package embeddinglab

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dingpuyu/rag-evolution-lab/internal/retrieval"
)

const (
	ModeSymmetric     = "symmetric"
	ModeQueryDocument = "query_document"
	maxTextRunes      = 8000
	maxPreview        = 64
)

type Service struct {
	embedder retrieval.Embedder
}

func New(embedder retrieval.Embedder) (*Service, error) {
	if embedder == nil {
		return nil, fmt.Errorf("embedder must not be nil")
	}
	return &Service{embedder: embedder}, nil
}

type SimilarityRequest struct {
	TextA             string `json:"text_a"`
	TextB             string `json:"text_b"`
	Mode              string `json:"mode,omitempty"`
	PreviewDimensions int    `json:"preview_dimensions,omitempty"`
	IncludeVectors    bool   `json:"include_vectors,omitempty"`
}

type VectorSummary struct {
	Preview []float64 `json:"preview"`
	Norm    float64   `json:"l2_norm"`
	Minimum float64   `json:"minimum"`
	Maximum float64   `json:"maximum"`
	Vector  []float64 `json:"vector,omitempty"`
}

type SimilarityMetrics struct {
	CosineSimilarity  float64 `json:"cosine_similarity"`
	DotProduct        float64 `json:"dot_product"`
	EuclideanDistance float64 `json:"euclidean_distance"`
}

type SimilarityResponse struct {
	Embedder    string            `json:"embedder"`
	Mode        string            `json:"mode"`
	Dimensions  int               `json:"dimensions"`
	VectorA     VectorSummary     `json:"vector_a"`
	VectorB     VectorSummary     `json:"vector_b"`
	Metrics     SimilarityMetrics `json:"metrics"`
	LatencyMS   float64           `json:"latency_ms"`
	Explanation string            `json:"explanation"`
}

func (s *Service) EmbedderName() string { return s.embedder.Name() }

func (s *Service) Similarity(ctx context.Context, request SimilarityRequest) (SimilarityResponse, error) {
	request.TextA = strings.TrimSpace(request.TextA)
	request.TextB = strings.TrimSpace(request.TextB)
	if request.TextA == "" || request.TextB == "" {
		return SimilarityResponse{}, fmt.Errorf("text_a and text_b must not be empty")
	}
	if utf8.RuneCountInString(request.TextA) > maxTextRunes || utf8.RuneCountInString(request.TextB) > maxTextRunes {
		return SimilarityResponse{}, fmt.Errorf("each text must contain at most %d characters", maxTextRunes)
	}
	mode := request.Mode
	if mode == "" {
		mode = ModeSymmetric
	}
	if mode != ModeSymmetric && mode != ModeQueryDocument {
		return SimilarityResponse{}, fmt.Errorf("mode must be %q or %q", ModeSymmetric, ModeQueryDocument)
	}
	previewDimensions := request.PreviewDimensions
	if previewDimensions <= 0 {
		previewDimensions = 12
	}
	if previewDimensions > maxPreview {
		return SimilarityResponse{}, fmt.Errorf("preview_dimensions must not exceed %d", maxPreview)
	}

	started := time.Now()
	var vectorA, vectorB []float64
	var err error
	if mode == ModeSymmetric {
		var vectors [][]float64
		vectors, err = s.embedder.EmbedDocuments(ctx, []string{request.TextA, request.TextB})
		if err == nil {
			if len(vectors) != 2 {
				err = fmt.Errorf("embedder returned %d vectors, expected 2", len(vectors))
			} else {
				vectorA, vectorB = vectors[0], vectors[1]
			}
		}
	} else {
		vectorA, err = s.embedder.EmbedQuery(ctx, request.TextA)
		if err == nil {
			var vectors [][]float64
			vectors, err = s.embedder.EmbedDocuments(ctx, []string{request.TextB})
			if err == nil {
				if len(vectors) != 1 {
					err = fmt.Errorf("embedder returned %d document vectors, expected 1", len(vectors))
				} else {
					vectorB = vectors[0]
				}
			}
		}
	}
	if err != nil {
		return SimilarityResponse{}, fmt.Errorf("create embeddings with %s: %w", s.embedder.Name(), err)
	}
	if err := validatePair(vectorA, vectorB); err != nil {
		return SimilarityResponse{}, fmt.Errorf("invalid embeddings from %s: %w", s.embedder.Name(), err)
	}

	dot, normA, normB, squaredDistance := vectorMetrics(vectorA, vectorB)
	cosine := dot / (normA * normB)
	response := SimilarityResponse{
		Embedder:   s.embedder.Name(),
		Mode:       mode,
		Dimensions: len(vectorA),
		VectorA:    summarize(vectorA, previewDimensions, request.IncludeVectors),
		VectorB:    summarize(vectorB, previewDimensions, request.IncludeVectors),
		Metrics: SimilarityMetrics{
			CosineSimilarity:  cosine,
			DotProduct:        dot,
			EuclideanDistance: math.Sqrt(squaredDistance),
		},
		LatencyMS: float64(time.Since(started).Microseconds()) / 1000,
	}
	if mode == ModeSymmetric {
		response.Explanation = "两段文字使用相同的文档编码路径；适合通用语义相似度实验。"
	} else {
		response.Explanation = "text_a 使用 Query 编码路径，text_b 使用 Document 编码路径；适合验证非对称 RAG 检索。"
	}
	return response, nil
}

func validatePair(a, b []float64) error {
	if len(a) == 0 || len(b) == 0 {
		return fmt.Errorf("embedding vectors must not be empty")
	}
	if len(a) != len(b) {
		return fmt.Errorf("dimension mismatch: %d and %d", len(a), len(b))
	}
	for index := range a {
		if math.IsNaN(a[index]) || math.IsInf(a[index], 0) || math.IsNaN(b[index]) || math.IsInf(b[index], 0) {
			return fmt.Errorf("non-finite value at dimension %d", index)
		}
	}
	_, normA, normB, _ := vectorMetrics(a, b)
	if normA == 0 || normB == 0 {
		return fmt.Errorf("embedding vectors must have non-zero norm")
	}
	return nil
}

func vectorMetrics(a, b []float64) (dot, normA, normB, squaredDistance float64) {
	for index := range a {
		dot += a[index] * b[index]
		normA += a[index] * a[index]
		normB += b[index] * b[index]
		difference := a[index] - b[index]
		squaredDistance += difference * difference
	}
	return dot, math.Sqrt(normA), math.Sqrt(normB), squaredDistance
}

func summarize(vector []float64, previewDimensions int, includeVector bool) VectorSummary {
	minimum, maximum := vector[0], vector[0]
	for _, value := range vector[1:] {
		minimum = min(minimum, value)
		maximum = max(maximum, value)
	}
	previewLength := min(previewDimensions, len(vector))
	result := VectorSummary{
		Preview: append([]float64(nil), vector[:previewLength]...),
		Minimum: minimum,
		Maximum: maximum,
	}
	_, result.Norm, _, _ = vectorMetrics(vector, vector)
	if includeVector {
		result.Vector = append([]float64(nil), vector...)
	}
	return result
}
