package ingest

import (
	"reflect"
	"strings"
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

func TestChunkerPageAwareParentChildProvenance(t *testing.T) {
	document := domain.Document{
		ID:      "manual-v1",
		Content: "# 登录\n\n第一页说明。\f<!-- page: 7 -->\n## 配置\n\n第二页说明。",
	}
	chunks := (Chunker{MaxRunes: 100, PageAware: true}).Chunk(document)
	if len(chunks) != 2 {
		t.Fatalf("expected two page-aware chunks, got %d", len(chunks))
	}
	if chunks[0].ParentID != "manual-v1#p001" || chunks[1].ParentID != "manual-v1#p002" {
		t.Fatalf("unexpected parent ids: %q %q", chunks[0].ParentID, chunks[1].ParentID)
	}
	if chunks[0].SourcePage != 1 || chunks[1].SourcePage != 7 {
		t.Fatalf("unexpected source pages: %d %d", chunks[0].SourcePage, chunks[1].SourcePage)
	}
	if chunks[1].Content == "" || chunks[1].ParentContent == "" {
		t.Fatal("child and parent content must be retained for citation preview")
	}
	if !strings.Contains(chunks[1].ParentContent, "第二页说明") {
		t.Fatalf("parent content lost source text: %q", chunks[1].ParentContent)
	}
}

func TestChunkerOverlapIsDeterministicAndSharedByParent(t *testing.T) {
	document := domain.Document{ID: "long", Content: "abcdefghijklmnopqrstuvwx"}
	chunks := (Chunker{MaxRunes: 10, OverlapRunes: 3}).Chunk(document)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 overlapping chunks, got %d", len(chunks))
	}
	if chunks[0].ParentID != chunks[1].ParentID || chunks[1].ParentID != chunks[2].ParentID {
		t.Fatalf("children from one logical section must share a parent: %#v", chunks)
	}
	if !strings.Contains(chunks[1].Content, "hij") {
		t.Fatalf("expected overlap from first chunk in second chunk, got %q", chunks[1].Content)
	}
}
