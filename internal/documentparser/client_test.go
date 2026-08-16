package documentparser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientParsesDocumentIRAndBuildsPageAwareMarkdown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := request.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		file, _, err := request.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		file.Close()
		_ = json.NewEncoder(writer).Encode(DocumentIR{
			SchemaVersion: "document-ir-v1", Status: "ready", SourceFile: "manual.pdf",
			Blocks: []Block{
				{BlockType: "paragraph", Text: "SYS-NET-042", HeadingPath: []string{"错误码"}, Provenance: Provenance{Page: 7}},
				{BlockType: "table", Text: "型号 | 版本", HeadingPath: []string{"兼容矩阵"}, Provenance: Provenance{Sheet: "设备矩阵", CellRange: "A1:D8"}},
			},
		})
	}))
	defer server.Close()
	result, err := New(server.URL).Parse(context.Background(), "manual.pdf", "application/pdf", []byte("pdf"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ready" || !strings.Contains(result.Markdown(), "<!-- page: 7 -->") || !strings.Contains(result.Markdown(), "## 错误码") || !strings.Contains(result.Markdown(), "<!-- sheet: 设备矩阵; range: A1:D8 -->") {
		t.Fatalf("unexpected IR: %#v markdown=%q", result, result.Markdown())
	}
}
