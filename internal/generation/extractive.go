package generation

import (
	"context"
	"fmt"
	"strings"
)

// ExtractiveGenerator is a deterministic baseline and an offline fallback. It
// deliberately makes no synthesis claim: it returns the first selected piece
// of evidence with a server-verifiable citation.
type ExtractiveGenerator struct{}

func (ExtractiveGenerator) Name() string { return "extractive-baseline" }

func (ExtractiveGenerator) Generate(_ context.Context, request Request) (Generation, error) {
	if request.Mode == ModePersona {
		return Generation{
			Output: Output{Answerable: true, Answer: personaReply(request.Query)},
			Model:  "none", PromptVersion: PersonaPromptVersion, FinishReason: "persona",
		}, nil
	}
	if len(request.Evidence) == 0 {
		return Generation{}, fmt.Errorf("extractive generator requires evidence")
	}
	best := request.Evidence[0]
	return Generation{
		Output: Output{
			Answerable: true, Answer: strings.TrimSpace(best.Content),
			Citations: []CitationReference{{ChunkID: best.ChunkID, DocumentID: best.DocumentID}},
		},
		Model: "none", PromptVersion: PromptVersion, FinishReason: "extractive",
	}, nil
}

func personaReply(query string) string {
	value := strings.ToLower(strings.TrimSpace(query))
	switch {
	case strings.Contains(value, "你是谁") || strings.Contains(value, "who are you"):
		return "我是 RAG Desk，负责把企业知识库检索、权限过滤和有依据的回答串成一条链路。"
	case strings.Contains(value, "你能做什么") || strings.Contains(value, "what can you do"):
		return "我可以处理日常问答，也会在问题涉及企业专有资料时检索授权知识库，并返回来源引用。"
	case strings.Contains(value, "你好") || strings.Contains(value, "您好") || strings.Contains(value, "hello") || strings.Contains(value, "hi"):
		return "你好，我是 RAG Desk。你可以直接提问；涉及企业资料的问题，我会先检索知识库再回答。"
	default:
		return "我是 RAG Desk，可以回答通用问题；如果你要查询企业专有流程、权限或配置，请告诉我具体问题。"
	}
}
