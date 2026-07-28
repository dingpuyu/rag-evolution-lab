// Package querytrace contains the durable contract for application-level
// retrieval and generation traces. Storage implementations live outside this
// package so the Gateway stays independent from PostgreSQL.
package querytrace

import (
	"context"
	"errors"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
)

var (
	ErrNotFound = errors.New("query trace not found")
	ErrDenied   = errors.New("query trace access denied")
)

type Record struct {
	TraceID         string         `json:"trace_id"`
	AppID           string         `json:"app_id"`
	EnvironmentID   string         `json:"environment_id"`
	TenantID        string         `json:"tenant_id"`
	Subject         string         `json:"subject"`
	Query           string         `json:"query"`
	RewrittenQuery  string         `json:"rewritten_query,omitempty"`
	Status          string         `json:"status"`
	IndexVersion    string         `json:"index_version,omitempty"`
	IndexCollection string         `json:"index_collection,omitempty"`
	EmbeddingModel  string         `json:"embedding_model,omitempty"`
	Generator       string         `json:"generator,omitempty"`
	Model           string         `json:"model,omitempty"`
	PromptVersion   string         `json:"prompt_version,omitempty"`
	TopK            int            `json:"top_k"`
	CandidateCount  int            `json:"candidate_count"`
	HitCount        int            `json:"hit_count"`
	RerankApplied   bool           `json:"rerank_applied"`
	RewriteApplied  bool           `json:"rewrite_applied"`
	Answerable      *bool          `json:"answerable,omitempty"`
	RefusalReason   string         `json:"refusal_reason,omitempty"`
	EmbeddingMS     float64        `json:"embedding_ms"`
	RetrievalMS     float64        `json:"retrieval_ms"`
	GenerationMS    float64        `json:"generation_ms"`
	TotalMS         float64        `json:"total_ms"`
	PromptTokens    int            `json:"prompt_tokens"`
	OutputTokens    int            `json:"output_tokens"`
	TraceParent     string         `json:"trace_parent,omitempty"`
	SpanID          string         `json:"span_id,omitempty"`
	Provider        string         `json:"provider,omitempty"`
	InputCostUSD    float64        `json:"input_cost_usd"`
	OutputCostUSD   float64        `json:"output_cost_usd"`
	TotalCostUSD    float64        `json:"total_cost_usd"`
	Error           string         `json:"error,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	StartedAt       time.Time      `json:"started_at"`
	CompletedAt     *time.Time     `json:"completed_at,omitempty"`
}

type Store interface {
	UpsertQueryTrace(context.Context, Record) error
	GetQueryTrace(context.Context, auth.Identity, string, string) (Record, error)
}
