package ingest

import (
	"reflect"
	"testing"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
)

func TestChunkerStableIDsAndHeadings(t *testing.T) {
	document := domain.Document{
		ID:      "guide-v1",
		Title:   "Guide",
		Content: "# 第一章\n\n介绍。\n\n## 配置\n\n配置内容。",
	}
	chunker := Chunker{MaxRunes: 100}
	first := chunker.Chunk(document)
	second := chunker.Chunk(document)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("chunking must be deterministic")
	}
	if len(first) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(first))
	}
	if first[0].ID != "guide-v1#c001" || first[1].ID != "guide-v1#c002" {
		t.Fatalf("unexpected chunk ids: %s %s", first[0].ID, first[1].ID)
	}
	wantHeadings := []string{"第一章", "配置"}
	if !reflect.DeepEqual(first[1].HeadingPath, wantHeadings) {
		t.Fatalf("headings=%#v want=%#v", first[1].HeadingPath, wantHeadings)
	}
}

func TestChunkerSplitsLongBlockWithoutDroppingRunes(t *testing.T) {
	document := domain.Document{ID: "long", Content: "一二三四五六七八九十"}
	chunks := (Chunker{MaxRunes: 4}).Chunk(document)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	var joined string
	for _, chunk := range chunks {
		joined += chunk.Content
	}
	if joined != document.Content {
		t.Fatalf("content changed: got %q want %q", joined, document.Content)
	}
}
