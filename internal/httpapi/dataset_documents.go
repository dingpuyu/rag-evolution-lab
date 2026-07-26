package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

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
