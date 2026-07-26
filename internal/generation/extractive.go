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
