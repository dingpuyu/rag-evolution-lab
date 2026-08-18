package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/dingpuyu/rag-evolution-lab/internal/auth"
	"github.com/dingpuyu/rag-evolution-lab/internal/datasetaccess"
	"github.com/dingpuyu/rag-evolution-lab/internal/documentparser"
	"github.com/dingpuyu/rag-evolution-lab/internal/ingestionjob"
)

const maxDocumentIRPreviewBytes = 16 << 20

type documentPipelineStage struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type documentIRPreview struct {
	SchemaVersion string                 `json:"schema_version,omitempty"`
	Status        string                 `json:"status,omitempty"`
	SourceFile    string                 `json:"source_file,omitempty"`
	MIMEType      string                 `json:"mime_type,omitempty"`
	SHA256        string                 `json:"sha256,omitempty"`
	BlockCount    int                    `json:"block_count"`
	Blocks        []documentparser.Block `json:"blocks"`
}

func canManageDatasetDocuments(dataset datasetaccess.Dataset, identity auth.Identity) bool {
	return identity.HasRole("platform_admin") ||
		(identity.HasRole("admin") && dataset.Visibility == "tenant" && dataset.OwnerTenant == identity.TenantID)
}

// documentDetail joins the three sources of truth used by ingestion:
// PostgreSQL revision metadata, the durable worker job and the Document IR in
// MinIO. The endpoint is management-only so draft or private source content is
// never exposed merely because a user can query the resulting knowledge base.
func (api *DatasetAPI) documentDetail(writer http.ResponseWriter, request *http.Request) {
	dataset, identity, ok := api.authorizeDataset(writer, request)
	if !ok {
		return
	}
	if !canManageDatasetDocuments(dataset, identity) {
		writeError(writer, http.StatusForbidden, "document_forbidden", "document pipeline details require the owning tenant administrator")
		return
	}
	documentID := strings.TrimSpace(request.URL.Query().Get("document_id"))
	revision, err := strconv.ParseInt(request.URL.Query().Get("source_revision"), 10, 64)
	if documentID == "" || revision <= 0 || err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_document_revision", "document_id and a positive source_revision are required")
		return
	}
	registry, ok := api.store.(datasetaccess.DocumentRegistry)
	if !ok {
		writeError(writer, http.StatusServiceUnavailable, "document_registry_unavailable", "document registry is not configured")
		return
	}
	revisions, err := registry.ListKnowledgeDocuments(request.Context(), dataset.ID)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "document_registry_unavailable", err.Error())
		return
	}
	selected := selectKnowledgeRevision(revisions, documentID, revision)
	if selected == nil {
		writeError(writer, http.StatusNotFound, "document_revision_not_found", "document revision not found")
		return
	}

	var job *ingestionjob.Job
	if api.ingestionJobs != nil && selected.JobID != "" {
		if current, jobErr := api.ingestionJobs.Get(selected.JobID); jobErr == nil {
			job = &current
			selected.IndexStatus = current.Status
			selected.LastError = current.LastError
			if current.Result != nil {
				selected.ChunkCount = current.Result.CurrentChunks
				selected.IndexVersion = current.Result.EmbeddingVersion
			}
		}
	}

	searchable := false
	catalogStatus := "available"
	if catalog, catalogErr := api.service.CatalogForQuery(request.Context(), buildDatasetQuery(dataset, identity, "", 0)); catalogErr == nil {
		for _, document := range catalog.Documents {
			if document.DocumentID == selected.DocumentID {
				searchable = true
				break
			}
		}
	} else {
		catalogStatus = catalogErr.Error()
	}

	previewLimit := 80
	if raw := strings.TrimSpace(request.URL.Query().Get("preview_limit")); raw != "" {
		value, parseErr := strconv.Atoi(raw)
		if parseErr != nil || value < 1 || value > 200 {
			writeError(writer, http.StatusBadRequest, "invalid_preview_limit", "preview_limit must be between 1 and 200")
			return
		}
		previewLimit = value
	}
	preview := documentIRPreview{BlockCount: selected.BlockCount, Blocks: []documentparser.Block{}}
	previewError := ""
	if selected.IRURI != "" {
		documentIR, getErr := api.readDocumentIR(request.Context(), selected.IRURI)
		if getErr != nil {
			previewError = getErr.Error()
		} else {
			preview = documentIRPreview{
				SchemaVersion: documentIR.SchemaVersion, Status: documentIR.Status, SourceFile: documentIR.SourceFile,
				MIMEType: documentIR.MIMEType, SHA256: documentIR.SHA256, BlockCount: len(documentIR.Blocks),
				Blocks: previewIRBlocks(documentIR.Blocks, previewLimit),
			}
		}
	}
	pipeline := buildDocumentPipeline(*selected, job, searchable)
	completed := 0
	for _, stage := range pipeline {
		if stage.Status == "completed" {
			completed++
		}
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"dataset": dataset, "revision": selected, "job": job, "document_ir": preview,
		"preview_error": previewError, "searchable": searchable, "catalog_status": catalogStatus,
		"pipeline": pipeline, "progress_percent": completed * 100 / len(pipeline),
	})
}

func selectKnowledgeRevision(revisions []datasetaccess.KnowledgeDocumentRevision, documentID string, sourceRevision int64) *datasetaccess.KnowledgeDocumentRevision {
	for index := range revisions {
		if revisions[index].DocumentID == documentID && revisions[index].SourceRevision == sourceRevision {
			selected := revisions[index]
			return &selected
		}
	}
	return nil
}

func (api *DatasetAPI) readDocumentIR(ctx context.Context, uri string) (documentparser.DocumentIR, error) {
	if api.documentStore == nil {
		return documentparser.DocumentIR{}, fmt.Errorf("document store is not configured")
	}
	object, err := api.documentStore.Get(ctx, uri, maxDocumentIRPreviewBytes)
	if err != nil {
		return documentparser.DocumentIR{}, err
	}
	var documentIR documentparser.DocumentIR
	if err := json.Unmarshal(object.Data, &documentIR); err != nil {
		return documentparser.DocumentIR{}, fmt.Errorf("decode Document IR: %w", err)
	}
	return documentIR, nil
}

func buildDocumentPipeline(record datasetaccess.KnowledgeDocumentRevision, job *ingestionjob.Job, searchable bool) []documentPipelineStage {
	stageRank := map[string]int{"queued": 0, "validating": 1, "chunking": 2, "embedding": 3, "indexing": 4, "verifying": 5, "completed": 6}
	currentRank := 0
	if job != nil {
		currentRank = stageRank[job.Stage]
	}
	workerStage := func(key, label, detail string, targetRank int) documentPipelineStage {
		status := "pending"
		switch {
		case job == nil:
			status = "pending"
		case job.Status == ingestionjob.StatusFailed || job.Status == ingestionjob.StatusCancelled:
			failureRank := stageRank[job.FailureStage]
			// Validation and lifecycle chunking happen after Document IR has
			// already been produced, but before the first worker stage rendered
			// by the UI. Attribute either failure to Embedding so the pipeline
			// always contains one actionable red stage rather than only blocked
			// descendants.
			if failureRank < 3 {
				failureRank = 3
			}
			if targetRank < failureRank {
				status = "completed"
			} else if targetRank == failureRank {
				status = "failed"
			} else {
				status = "blocked"
			}
		case job.Status == ingestionjob.StatusCompleted || currentRank > targetRank:
			status = "completed"
		case currentRank == targetRank:
			status = "running"
		}
		return documentPipelineStage{Key: key, Label: label, Status: status, Detail: detail}
	}
	parserStatus := "completed"
	if record.ParserStatus == "ocr_required" || record.IndexStatus == "blocked" {
		parserStatus = "blocked"
	} else if record.ParserStatus == "" {
		parserStatus = "pending"
	}
	blockStatus := "completed"
	if parserStatus == "blocked" {
		blockStatus = "blocked"
	} else if record.BlockCount == 0 {
		blockStatus = "pending"
	}
	pipeline := []documentPipelineStage{
		{Key: "source", Label: "原件保存", Status: completedWhen(record.SourceURI != ""), Detail: record.FileName},
		{Key: "parse", Label: "Document IR", Status: parserStatus, Detail: record.ParserStatus},
		{Key: "chunk", Label: "结构化切块", Status: blockStatus, Detail: fmt.Sprintf("%d blocks", record.BlockCount)},
		workerStage("embedding", "Qwen Embedding", record.IndexVersion, 3),
		workerStage("index", "Milvus 索引", fmt.Sprintf("%d chunks", record.ChunkCount), 4),
		workerStage("verify", "写后验证", verificationDetail(job), 5),
	}
	if parserStatus == "blocked" && job == nil {
		for index := 3; index < len(pipeline); index++ {
			pipeline[index].Status = "blocked"
		}
	}
	searchStatus := "pending"
	if searchable {
		searchStatus = "completed"
	} else if parserStatus == "blocked" {
		searchStatus = "blocked"
	} else if job != nil && (job.Status == ingestionjob.StatusFailed || job.Status == ingestionjob.StatusCancelled) {
		searchStatus = "blocked"
	} else if job != nil && job.Status == ingestionjob.StatusCompleted {
		searchStatus = "failed"
	}
	pipeline = append(pipeline, documentPipelineStage{Key: "searchable", Label: "可检索", Status: searchStatus, Detail: availabilityDetail(searchable)})
	if record.LastError != "" {
		for index := len(pipeline) - 1; index >= 0; index-- {
			if pipeline[index].Status == "failed" {
				pipeline[index].Detail = record.LastError
				break
			}
		}
	}
	return pipeline
}

func completedWhen(value bool) string {
	if value {
		return "completed"
	}
	return "pending"
}

func verificationDetail(job *ingestionjob.Job) string {
	if job != nil && job.Result != nil && job.Result.Verified {
		return "Milvus rows verified"
	}
	return "waiting"
}

func availabilityDetail(searchable bool) string {
	if searchable {
		return "active collection"
	}
	return "waiting for catalog visibility"
}
