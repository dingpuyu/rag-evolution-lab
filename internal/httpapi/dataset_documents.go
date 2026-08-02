package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
	"github.com/dingpuyu/rag-evolution-lab/internal/ingest"
	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
)

type datasetDocumentInput struct {
	DocumentID     string `json:"document_id"`
	Title          string `json:"title"`
	Content        string `json:"content"`
	Version        string `json:"version"`
	SourceRevision int64  `json:"source_revision"`
	EventID        string `json:"event_id"`
	Operation      string `json:"operation"`
}

type documentPreviewInput struct {
	Title        string `json:"title"`
	Content      string `json:"content"`
	MaxRunes     int    `json:"max_runes"`
	OverlapRunes int    `json:"overlap_runes"`
}

type documentPreviewChunk struct {
	ID             string   `json:"id"`
	ParentID       string   `json:"parent_id"`
	ParentSequence int      `json:"parent_sequence"`
	SourcePage     int      `json:"source_page"`
	Sequence       int      `json:"sequence"`
	HeadingPath    []string `json:"heading_path,omitempty"`
	Content        string   `json:"content"`
	ParentContent  string   `json:"parent_content"`
}

func (api *DatasetAPI) document(writer http.ResponseWriter, request *http.Request) {
	dataset, identity, ok := api.authorizeDataset(writer, request)
	if !ok {
		return
	}
	if !identity.HasRole("platform_admin") &&
		(!identity.HasRole("admin") || dataset.Visibility != "tenant" || dataset.OwnerTenant != identity.TenantID) {
		writeError(writer, http.StatusForbidden, "document_forbidden", "only the owning tenant administrator may mutate this knowledge base")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxLifecycleRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input datasetDocumentInput
	if err := decoder.Decode(&input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := ensureEOF(decoder); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	input.DocumentID = strings.TrimSpace(input.DocumentID)
	input.Title = strings.TrimSpace(input.Title)
	input.Version = strings.TrimSpace(input.Version)
	input.EventID = strings.TrimSpace(input.EventID)
	input.Operation = strings.ToLower(strings.TrimSpace(input.Operation))
	if input.Operation == "" {
		input.Operation = milvus.OperationUpsert
	}
	if input.DocumentID == "" || len(input.DocumentID) > 200 {
		writeError(writer, http.StatusBadRequest, "invalid_document", "document_id is required and must not exceed 200 characters")
		return
	}
	if input.EventID == "" {
		input.EventID = fmt.Sprintf("portal-%d", time.Now().UnixNano())
	}
	if input.SourceRevision <= 0 {
		input.SourceRevision = 1
	}
	change := milvus.LifecycleChange{
		EventID: input.EventID, Operation: input.Operation, Revision: input.SourceRevision, DocumentID: input.DocumentID,
	}
	if input.Operation == milvus.OperationUpsert {
		if input.Title == "" || strings.TrimSpace(input.Content) == "" {
			writeError(writer, http.StatusBadRequest, "invalid_document", "title and content are required for an upsert")
			return
		}
		roles := append([]string(nil), dataset.AllowedRoles...)
		if len(roles) == 0 {
			roles = []string{"admin"}
		}
		tenants := []string(nil)
		visibility := dataset.Visibility
		if visibility == "tenant" {
			tenants = []string{dataset.OwnerTenant}
		}
		change.Document = &milvus.LifecycleDocument{
			ID: input.DocumentID, Title: input.Title, Content: input.Content, Product: dataset.Product,
			Version: input.Version, Status: "active", Visibility: visibility,
			AllowedTenants: tenants, AllowedRoles: roles,
		}
	}
	result, err := api.service.Apply(request.Context(), change)
	if err != nil {
		status := http.StatusUnprocessableEntity
		code := "document_ingest_failed"
		if strings.Contains(err.Error(), "stale or conflicting revision") || strings.Contains(err.Error(), "already used") {
			status = http.StatusConflict
			code = "document_revision_conflict"
		}
		writeError(writer, status, code, err.Error())
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"dataset": dataset, "identity": identity, "result": result})
}

// previewDocument validates the structure-aware ingestion result without
// writing to Milvus. It lets an operator inspect page provenance, parent-child
// grouping and overlap before spending embedding/indexing cost.
func (api *DatasetAPI) previewDocument(writer http.ResponseWriter, request *http.Request) {
	if _, _, ok := api.authorizeDataset(writer, request); !ok {
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, maxLifecycleRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input documentPreviewInput
	if err := decoder.Decode(&input); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := ensureEOF(decoder); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	input.Title = strings.TrimSpace(input.Title)
	input.Content = strings.TrimSpace(input.Content)
	if input.Title == "" || input.Content == "" {
		writeError(writer, http.StatusBadRequest, "invalid_preview", "title and content are required")
		return
	}
	if len([]rune(input.Title)) > 200 || len([]rune(input.Content)) > 1_000_000 {
		writeError(writer, http.StatusBadRequest, "invalid_preview", "title must be <= 200 runes and content must be <= 1,000,000 runes")
		return
	}
	if input.MaxRunes <= 0 {
		input.MaxRunes = 500
	}
	if input.MaxRunes < 100 || input.MaxRunes > 2000 {
		writeError(writer, http.StatusBadRequest, "invalid_preview", "max_runes must be between 100 and 2000")
		return
	}
	if input.OverlapRunes < 0 || input.OverlapRunes >= input.MaxRunes/2 {
		writeError(writer, http.StatusBadRequest, "invalid_preview", "overlap_runes must be non-negative and less than half of max_runes")
		return
	}

	chunks := (ingest.Chunker{MaxRunes: input.MaxRunes, OverlapRunes: input.OverlapRunes, PageAware: true}).Chunk(domain.Document{
		ID: "preview", Title: input.Title, Content: input.Content,
	})
	parents := make(map[string]struct{}, len(chunks))
	pages := make(map[int]struct{}, len(chunks))
	previewChunks := make([]documentPreviewChunk, 0, len(chunks))
	for _, chunk := range chunks {
		parents[chunk.ParentID] = struct{}{}
		if chunk.SourcePage > 0 {
			pages[chunk.SourcePage] = struct{}{}
		}
		previewChunks = append(previewChunks, documentPreviewChunk{
			ID: chunk.ID, ParentID: chunk.ParentID, ParentSequence: chunk.ParentSequence,
			SourcePage: chunk.SourcePage, Sequence: chunk.Sequence,
			HeadingPath: append([]string(nil), chunk.HeadingPath...), Content: chunk.Content,
			ParentContent: chunk.ParentContent,
		})
	}
	pageList := make([]int, 0, len(pages))
	for page := range pages {
		pageList = append(pageList, page)
	}
	sort.Ints(pageList)
	writeJSON(writer, http.StatusOK, map[string]any{
		"chunker_version": "header-page-parent-v1",
		"max_runes":       input.MaxRunes,
		"overlap_runes":   input.OverlapRunes,
		"parent_count":    len(parents),
		"child_count":     len(previewChunks),
		"pages":           pageList,
		"chunks":          previewChunks,
	})
}
