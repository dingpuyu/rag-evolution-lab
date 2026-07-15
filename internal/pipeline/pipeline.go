package pipeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
	"github.com/dingpuyu/rag-evolution-lab/internal/retrieval"
	"github.com/dingpuyu/rag-evolution-lab/internal/trace"
)

type Pipeline struct {
	name      string
	retriever retrieval.Retriever
}

func New(name string, retriever retrieval.Retriever) *Pipeline {
	return &Pipeline{name: name, retriever: retriever}
}

func (p *Pipeline) Name() string { return p.name }

func (p *Pipeline) Query(ctx context.Context, request domain.QueryRequest) (*domain.QueryResponse, error) {
	if strings.TrimSpace(request.Query) == "" {
		return nil, fmt.Errorf("query must not be empty")
	}
	if request.TopK <= 0 {
		request.TopK = 5
	}
	recorder := trace.New(p.name, request.Query)

	started := time.Now()
	results, err := p.retriever.Search(ctx, request)
	recorder.Add("retrieval", started, map[string]any{
		"retriever":    p.retriever.Name(),
		"result_count": len(results),
	})
	if err != nil {
		return nil, err
	}

	response := &domain.QueryResponse{
		Answerable: len(results) > 0,
		Retrieval:  results,
	}
	started = time.Now()
	if len(results) == 0 {
		response.Answer = "知识库中没有找到足够证据。"
	} else {
		best := results[0].Chunk
		response.Answer = best.Content
		response.Citations = []domain.Citation{{
			ChunkID:    best.ID,
			DocumentID: best.DocumentID,
			Document:   best.DocumentTitle,
			Excerpt:    best.Content,
		}}
	}
	recorder.Add("extractive_generation", started, map[string]any{
		"citation_count": len(response.Citations),
	})
	response.Trace = recorder.Finish()
	return response, nil
}
