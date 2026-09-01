package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
	"github.com/dingpuyu/rag-evolution-lab/internal/datasetaccess"
	"github.com/dingpuyu/rag-evolution-lab/internal/documentparser"
)

func TestDocumentQualityArtifactIsAuditableAndDoesNotIndex(t *testing.T) {
	confidence := 0.98
	documentIR := documentparser.DocumentIR{
		SchemaVersion: "document-ir-v4", Status: "ready", SourceFile: "manual.pdf", SHA256: "source-sha",
		Quality: documentparser.ParseQuality{Parser: "paddle-ppstructurev3", ParserVersion: "3.7.0", OCRUsed: true},
		Blocks: []documentparser.Block{{
			BlockType: "paragraph", Text: "BAT-LOW-021 处理：连接交流电源并检查电池状态", Confidence: &confidence,
			HeadingPath: []string{"维护手册", "故障处理"},
			Provenance:  documentparser.Provenance{SourceFile: "manual.pdf", Page: 2, Sheet: "兼容矩阵", CellRange: "A1:C1,A3:C3"},
		}},
		CleaningRemovals: []documentparser.CleaningRemoval{{
			Reason: "repeated_margin", Block: documentparser.Block{BlockType: "paragraph", Text: "PulseCare Medical Devices", Provenance: documentparser.Provenance{Page: 2}},
		}},
	}
	artifact := buildDocumentQualityArtifact("dev-aed-001", 500, 50, documentIR)
	if artifact.Indexed || artifact.Schema != "agent-evaluation.document-quality.artifact.v1" || artifact.ConfigFingerprint == "" {
		t.Fatalf("unexpected artifact identity: %#v", artifact)
	}
	if len(artifact.Cleaning.RemovedBlocks) != 1 || artifact.Cleaning.RemovedBlocks[0].Reason != "repeated_margin" {
		t.Fatalf("cleaner audit missing: %#v", artifact.Cleaning)
	}
	if len(artifact.Chunks) != 1 || artifact.Chunks[0].SourcePage != 2 || !strings.Contains(artifact.Chunks[0].Content, "BAT-LOW-021") {
		t.Fatalf("chunk provenance missing: %#v", artifact.Chunks)
	}
	if artifact.Chunks[0].SourceSheet != "兼容矩阵" || artifact.Chunks[0].SourceCellRange != "A1:C1,A3:C3" ||
		len(artifact.Chunks[0].HeadingPath) != 2 || artifact.Blocks[0].SourceSheet != "兼容矩阵" {
		t.Fatalf("structured locator provenance missing: chunks=%#v blocks=%#v", artifact.Chunks, artifact.Blocks)
	}
	if len(artifact.Retrieval) != 0 {
		t.Fatalf("no-index artifact unexpectedly contains retrieval output: %#v", artifact.Retrieval)
	}
}

func TestDocumentQualityArtifactRequiresOwningAdminAndNeverCallsMilvus(t *testing.T) {
	parserCalls := 0
	parserServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		parserCalls++
		_ = json.NewEncoder(writer).Encode(documentparser.DocumentIR{
			SchemaVersion: "document-ir-v4", Status: "ready", SourceFile: "manual.md",
			Quality: documentparser.ParseQuality{Parser: "native", ParserVersion: "document-parser-0.2"},
			Blocks:  []documentparser.Block{{BlockType: "paragraph", Text: "VSM-100", Provenance: documentparser.Provenance{Page: 1}}},
		})
	}))
	t.Cleanup(parserServer.Close)
	api := &DatasetAPI{store: datasetaccess.Defaults(), parser: documentparser.New(parserServer.URL)}

	viewerRequest := documentQualityMultipartRequest(t, "public-medical-device", auth.Identity{Subject: "viewer", TenantID: "tenant_a", Roles: []string{"viewer"}})
	viewerResponse := httptest.NewRecorder()
	api.documentQualityArtifact(viewerResponse, viewerRequest)
	if viewerResponse.Code != http.StatusForbidden || parserCalls != 0 {
		t.Fatalf("viewer reached parser: status=%d calls=%d body=%s", viewerResponse.Code, parserCalls, viewerResponse.Body.String())
	}

	adminRequest := documentQualityMultipartRequest(t, "tenant-a-operations", auth.Identity{Subject: "admin", TenantID: "tenant_a", Roles: []string{"admin"}})
	adminResponse := httptest.NewRecorder()
	api.documentQualityArtifact(adminResponse, adminRequest)
	if adminResponse.Code != http.StatusOK || parserCalls != 1 {
		t.Fatalf("admin preview failed: status=%d calls=%d body=%s", adminResponse.Code, parserCalls, adminResponse.Body.String())
	}
	if !strings.Contains(adminResponse.Body.String(), `"indexed":false`) ||
		!strings.Contains(adminResponse.Body.String(), `"dataset_id":"tenant-a-operations"`) ||
		!strings.Contains(adminResponse.Body.String(), `"document-ir-v4"`) {
		t.Fatalf("preview contract missing: %s", adminResponse.Body.String())
	}
}

func documentQualityMultipartRequest(t *testing.T, datasetID string, identity auth.Identity) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("case_id", "dev-aed-001"); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("max_runes", "500"); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("overlap_runes", "50"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("file", "manual.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("# manual\n\nVSM-100")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/datasets/"+datasetID+"/documents/evaluation-artifacts", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.SetPathValue("dataset_id", datasetID)
	return request.WithContext(context.WithValue(request.Context(), identityContextKey{}, identity))
}
