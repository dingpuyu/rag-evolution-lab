package httpapi

import (
	"testing"

	"github.com/dingpuyu/rag-evolution-lab/internal/datasetaccess"
	"github.com/dingpuyu/rag-evolution-lab/internal/documentparser"
)

func TestCompareDocumentBlocksDistinguishesRealChanges(t *testing.T) {
	heading := []string{"网络错误", "SYS-NET-042"}
	provenance := documentparser.Provenance{SourceFile: "manual.docx"}
	from := []documentparser.Block{
		{BlockType: "heading", Text: "SYS-NET-042", HeadingPath: heading, Provenance: provenance},
		{BlockType: "paragraph", Text: "网络接口无法取得地址。", HeadingPath: heading, Provenance: provenance},
		{BlockType: "paragraph", Text: "检查网线。", HeadingPath: heading, Provenance: provenance},
		{BlockType: "table", Text: "旧配置入口", HeadingPath: heading, Provenance: documentparser.Provenance{SourceFile: "manual.docx", Sheet: "旧表", CellRange: "A1:B2"}},
	}
	to := []documentparser.Block{
		{BlockType: "heading", Text: "SYS-NET-042", HeadingPath: heading, Provenance: provenance},
		{BlockType: "paragraph", Text: "网络接口无法取得地址。", HeadingPath: heading, Provenance: provenance},
		{BlockType: "paragraph", Text: "检查网线和交换机端口。", HeadingPath: heading, Provenance: provenance},
		{BlockType: "paragraph", Text: "记录接口状态后再升级支持。", HeadingPath: heading, Provenance: provenance},
	}
	summary, changes := compareDocumentBlocks(from, to)
	if summary != (documentDiffSummary{Added: 1, Removed: 1, Modified: 1, Unchanged: 2}) {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	if len(changes) != 3 {
		t.Fatalf("expected three visible changes, got %#v", changes)
	}
	types := map[string]int{}
	for _, change := range changes {
		types[change.ChangeType]++
	}
	if types["added"] != 1 || types["removed"] != 1 || types["modified"] != 1 {
		t.Fatalf("unexpected change types: %#v", types)
	}
}

func TestCompareDocumentMetadataIncludesMedicalScope(t *testing.T) {
	from := datasetaccess.KnowledgeDocumentRevision{
		Title: "手册", DocumentVersion: "2.6", FileName: "manual-r1.docx", SourceHash: "old", BlockCount: 4,
		Metadata: map[string]any{"model_codes": []any{"VSM-100"}, "source_review_status": "draft"},
	}
	to := datasetaccess.KnowledgeDocumentRevision{
		Title: "手册", DocumentVersion: "2.6", FileName: "manual-r2.docx", SourceHash: "new", BlockCount: 5,
		Metadata: map[string]any{"model_codes": []any{"VSM-100", "VSM-100 Pro"}, "source_review_status": "approved"},
	}
	changes := compareDocumentMetadata(from, to)
	fields := make(map[string]bool, len(changes))
	for _, change := range changes {
		fields[change.Field] = true
	}
	for _, expected := range []string{"file_name", "source_hash", "block_count", "metadata.model_codes", "metadata.source_review_status"} {
		if !fields[expected] {
			t.Fatalf("missing metadata change %s in %#v", expected, changes)
		}
	}
}

func TestCompareDocumentBlocksDoesNotMisclassifyInsertionAsModification(t *testing.T) {
	heading := []string{"同一章节"}
	provenance := documentparser.Provenance{SourceFile: "manual.md"}
	from := []documentparser.Block{
		{BlockType: "paragraph", Text: "第一段", HeadingPath: heading, Provenance: provenance},
		{BlockType: "paragraph", Text: "第二段", HeadingPath: heading, Provenance: provenance},
	}
	to := []documentparser.Block{
		{BlockType: "paragraph", Text: "第一段", HeadingPath: heading, Provenance: provenance},
		{BlockType: "paragraph", Text: "新增段落", HeadingPath: heading, Provenance: provenance},
		{BlockType: "paragraph", Text: "第二段", HeadingPath: heading, Provenance: provenance},
	}
	summary, _ := compareDocumentBlocks(from, to)
	if summary.Added != 1 || summary.Modified != 0 || summary.Unchanged != 2 {
		t.Fatalf("insertion should remain an insertion: %#v", summary)
	}
}

func TestCompareDocumentBlocksIgnoresChangedDocumentTitleForDescendants(t *testing.T) {
	from := []documentparser.Block{
		{BlockType: "heading", Text: "Manual R1", HeadingPath: []string{"Manual R1"}},
		{BlockType: "heading", Text: "Error 42", HeadingPath: []string{"Manual R1", "Error 42"}},
		{BlockType: "paragraph", Text: "old explanation", HeadingPath: []string{"Manual R1", "Error 42"}},
	}
	to := []documentparser.Block{
		{BlockType: "heading", Text: "Manual R2", HeadingPath: []string{"Manual R2"}},
		{BlockType: "heading", Text: "Error 42", HeadingPath: []string{"Manual R2", "Error 42"}},
		{BlockType: "paragraph", Text: "new explanation", HeadingPath: []string{"Manual R2", "Error 42"}},
	}

	summary, _ := compareDocumentBlocks(from, to)
	if summary.Modified != 2 || summary.Unchanged != 1 || summary.Added != 0 || summary.Removed != 0 {
		t.Fatalf("document title change leaked into descendant matching: %#v", summary)
	}
}
