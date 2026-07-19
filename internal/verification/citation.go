package verification

import (
	"fmt"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
)

// VerifyCitations prevents a generator from citing evidence that was retrieved
// but not actually selected into the final model context.
func VerifyCitations(citations []domain.Citation, context []domain.RetrievedChunk) error {
	allowed := make(map[string]string, len(context))
	for _, selected := range context {
		allowed[selected.Chunk.ID] = selected.Chunk.DocumentID
	}
	for _, citation := range citations {
		documentID, ok := allowed[citation.ChunkID]
		if !ok {
			return fmt.Errorf("citation chunk %q is not in selected context", citation.ChunkID)
		}
		if citation.DocumentID != documentID {
			return fmt.Errorf("citation chunk %q has document %q, expected %q", citation.ChunkID, citation.DocumentID, documentID)
		}
	}
	return nil
}
