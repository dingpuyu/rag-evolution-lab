package verification

import (
	"testing"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
)

func TestVerifyCitationsRejectsChunkOutsideContext(t *testing.T) {
	context := []domain.RetrievedChunk{{Chunk: domain.Chunk{ID: "allowed#1", DocumentID: "allowed"}}}
	err := VerifyCitations([]domain.Citation{{ChunkID: "other#1", DocumentID: "other"}}, context)
	if err == nil {
		t.Fatal("expected citation outside context to fail")
	}
}

func TestVerifyCitationsAcceptsSelectedEvidence(t *testing.T) {
	context := []domain.RetrievedChunk{{Chunk: domain.Chunk{ID: "allowed#1", DocumentID: "allowed"}}}
	if err := VerifyCitations([]domain.Citation{{ChunkID: "allowed#1", DocumentID: "allowed"}}, context); err != nil {
		t.Fatal(err)
	}
}
