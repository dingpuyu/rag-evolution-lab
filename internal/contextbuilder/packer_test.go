package contextbuilder

import (
	"strings"
	"testing"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
)

func TestPackerDeduplicatesAndHonorsMaxChunks(t *testing.T) {
	candidates := []domain.RetrievedChunk{
		{Chunk: domain.Chunk{ID: "a", Content: "证据甲"}},
		{Chunk: domain.Chunk{ID: "a", Content: "证据甲"}},
		{Chunk: domain.Chunk{ID: "b", Content: "证据乙"}},
	}
	selected, stats := (Packer{MaxChunks: 2, TokenBudget: 100}).Pack(candidates)
	if len(selected) != 2 || selected[0].Chunk.ID != "a" || selected[1].Chunk.ID != "b" {
		t.Fatalf("unexpected selection: %#v", selected)
	}
	if stats.SelectedChunks != 2 {
		t.Fatalf("unexpected stats: %#v", stats)
	}
}

func TestPackerTruncatesFirstOversizedChunk(t *testing.T) {
	candidates := []domain.RetrievedChunk{{Chunk: domain.Chunk{ID: "large", DocumentTitle: "标题", Content: strings.Repeat("知识", 100)}}}
	selected, stats := (Packer{MaxChunks: 1, TokenBudget: 20}).Pack(candidates)
	if len(selected) != 1 || !stats.Truncated || stats.EstimatedTokens > 20 {
		t.Fatalf("budget was not enforced: selected=%#v stats=%#v", selected, stats)
	}
	if selected[0].Chunk.Content == candidates[0].Chunk.Content {
		t.Fatal("expected a truncated context copy")
	}
}
