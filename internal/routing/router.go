package routing

import (
	"context"
	"fmt"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
	"github.com/dingpuyu/rag-evolution-lab/internal/retrieval"
)

type Router struct {
	classifier Classifier
	routes     map[Intent]retrieval.Retriever
	fallback   retrieval.Retriever
}

func NewRouter(classifier Classifier, routes map[Intent]retrieval.Retriever, fallback retrieval.Retriever) *Router {
	copyRoutes := make(map[Intent]retrieval.Retriever, len(routes))
	for intent, target := range routes {
		copyRoutes[intent] = target
	}
	return &Router{classifier: classifier, routes: copyRoutes, fallback: fallback}
}

func (r *Router) Name() string { return "query-router" }

func (r *Router) Search(ctx context.Context, request domain.QueryRequest) ([]domain.RetrievedChunk, error) {
	decision, target, err := r.resolve(request)
	if err != nil {
		return nil, err
	}
	results, err := target.Search(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("route %s to %s: %w", decision.Intent, target.Name(), err)
	}
	for index := range results {
		results[index].Stage = "route:" + string(decision.Intent) + "/" + results[index].Stage
	}
	return results, nil
}

func (r *Router) TraceAttributes(request domain.QueryRequest) map[string]any {
	decision, target, err := r.resolve(request)
	if err != nil {
		return map[string]any{"route_error": err.Error()}
	}
	return map[string]any{
		"route":        string(decision.Intent),
		"route_reason": decision.Reason,
		"strategy":     target.Name(),
	}
}

func (r *Router) resolve(request domain.QueryRequest) (Decision, retrieval.Retriever, error) {
	if r.classifier == nil {
		return Decision{}, nil, fmt.Errorf("query router requires a classifier")
	}
	decision := r.classifier.Classify(request)
	target := r.routes[decision.Intent]
	if target == nil {
		target = r.fallback
	}
	if target == nil {
		return decision, nil, fmt.Errorf("query router has no strategy for intent %q", decision.Intent)
	}
	return decision, target, nil
}
