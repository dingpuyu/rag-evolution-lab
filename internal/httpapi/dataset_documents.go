package httpapi

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/datasetaccess"
	"github.com/dingpuyu/rag-evolution-lab/internal/documentparser"
	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
	"github.com/dingpuyu/rag-evolution-lab/internal/ingest"
	"github.com/dingpuyu/rag-evolution-lab/internal/ingestionjob"
	"github.com/dingpuyu/rag-evolution-lab/internal/milvus"
)

type datasetDocumentInput struct {
	DocumentID      string                  `json:"document_id"`
	Title           string                  `json:"title"`
	Content         string                  `json:"content"`
	Version         string                  `json:"version"`
	SourceRevision  int64                   `json:"source_revision"`
	EventID         string                  `json:"event_id"`
	Operation       string                  `json:"operation"`
	MedicalMetadata medicalDocumentMetadata `json:"medical_metadata,omitempty"`
}

type medicalDocumentMetadata struct {
	Domain              string   `json:"domain,omitempty"`
	Manufacturer        string   `json:"manufacturer,omitempty"`
	ProductFamily       string   `json:"product_family,omitempty"`
	ModelCodes          []string `json:"model_codes,omitempty"`
	SoftwareVersionFrom string   `json:"software_version_from,omitempty"`
	SoftwareVersionTo   string   `json:"software_version_to,omitempty"`
	HardwareRevision    string   `json:"hardware_revision,omitempty"`
	Region              string   `json:"region,omitempty"`
	Language            string   `json:"language,omitempty"`
	EffectiveFrom       string   `json:"effective_from,omitempty"`
	EffectiveTo         string   `json:"effective_to,omitempty"`
	AuthorityLevel      string   `json:"authority_level,omitempty"`
	DocumentRevision    string   `json:"document_revision,omitempty"`
	Supersedes          []string `json:"supersedes,omitempty"`
	SourceFile          string   `json:"source_file,omitempty"`
	DeviceIdentifiers   []string `json:"device_identifiers,omitempty"`
	AffectedLots        []string `json:"affected_lots,omitempty"`
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

type documentUploadMetadata struct {
	DocumentID          string   `json:"document_id"`
	Title               string   `json:"title"`
	Version             string   `json:"version"`
	SourceRevision      int64    `json:"source_revision"`
	Domain              string   `json:"domain"`
	Manufacturer        string   `json:"manufacturer"`
	ProductFamily       string   `json:"product_family"`
	ModelCodes          []string `json:"model_codes"`
	SoftwareVersionFrom string   `json:"software_version_from"`
	SoftwareVersionTo   string   `json:"software_version_to"`
	HardwareRevision    string   `json:"hardware_revision"`
	Region              string   `json:"region"`
	Language            string   `json:"language"`
	EffectiveFrom       string   `json:"effective_from"`
	EffectiveTo         string   `json:"effective_to"`
	AuthorityLevel      string   `json:"authority_level"`
	DocumentRevision    string   `json:"document_revision"`
	Supersedes          []string `json:"supersedes"`
	DeviceIdentifiers   []string `json:"device_identifiers"`
	AffectedLots        []string `json:"affected_lots"`
	SourceType          string   `json:"source_type"`
	SourceURLs          []string `json:"source_urls"`
	CollectedAt         string   `json:"collected_at"`
	SourceReviewStatus  string   `json:"source_review_status"`
	SourceReviewedAt    string   `json:"source_reviewed_at"`
}

func normalizeSourceMetadata(metadata *documentUploadMetadata) error {
	metadata.SourceType = strings.TrimSpace(metadata.SourceType)
	metadata.CollectedAt = strings.TrimSpace(metadata.CollectedAt)
	metadata.SourceReviewStatus = strings.TrimSpace(metadata.SourceReviewStatus)
	metadata.SourceReviewedAt = strings.TrimSpace(metadata.SourceReviewedAt)
	if metadata.SourceReviewStatus == "" {
		metadata.SourceReviewStatus = "draft"
	}
	switch metadata.SourceReviewStatus {
	case "draft", "approved", "review_required":
	default:
		return fmt.Errorf("source_review_status must be draft, approved or review_required")
	}
	if metadata.CollectedAt != "" {
		if _, err := time.Parse(time.DateOnly, metadata.CollectedAt); err != nil {
			return fmt.Errorf("collected_at must use YYYY-MM-DD")
		}
	}
	if metadata.SourceReviewedAt != "" {
		if _, err := time.Parse(time.RFC3339, metadata.SourceReviewedAt); err != nil {
			return fmt.Errorf("source_reviewed_at must use RFC3339")
		}
	}
	if metadata.SourceReviewStatus == "approved" && metadata.SourceReviewedAt == "" {
		return fmt.Errorf("source_reviewed_at is required when source_review_status is approved")
	}
	if len(metadata.SourceURLs) > 8 {
		return fmt.Errorf("source_urls must contain at most 8 URLs")
	}
	seen := make(map[string]struct{}, len(metadata.SourceURLs))
	normalized := make([]string, 0, len(metadata.SourceURLs))
	for _, raw := range metadata.SourceURLs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
			return fmt.Errorf("source_urls must contain absolute HTTPS URLs without credentials")
		}
		host := strings.ToLower(parsed.Hostname())
		if host == "localhost" || strings.HasSuffix(host, ".local") {
			return fmt.Errorf("source_urls must not reference local hosts")
		}
		parsed.Fragment = ""
		canonical := parsed.String()
		if len(canonical) > 2048 {
			return fmt.Errorf("source URL exceeds 2048 characters")
		}
		if _, exists := seen[canonical]; exists {
			continue
		}
		seen[canonical] = struct{}{}
		normalized = append(normalized, canonical)
	}
	if len(normalized) > 0 && metadata.SourceType == "" {
		return fmt.Errorf("source_type is required when source_urls are provided")
	}
	metadata.SourceURLs = normalized
	return nil
}

func (api *DatasetAPI) uploadDocument(writer http.ResponseWriter, request *http.Request) {
	dataset, identity, ok := api.authorizeDataset(writer, request)
	if !ok {
		return
	}
	if !identity.HasRole("platform_admin") && (!identity.HasRole("admin") || dataset.Visibility != "tenant" || dataset.OwnerTenant != identity.TenantID) {
		writeError(writer, http.StatusForbidden, "document_forbidden", "only the owning tenant administrator may upload documents")
		return
	}
	if api.parser == nil || api.documentStore == nil || api.ingestionJobs == nil {
		writeError(writer, http.StatusServiceUnavailable, "document_pipeline_unavailable", "document parser, object store and ingestion worker must be configured")
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 52<<20)
	if err := request.ParseMultipartForm(52 << 20); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_multipart", err.Error())
		return
	}
	file, header, err := request.FormFile("file")
	if err != nil {
		writeError(writer, http.StatusBadRequest, "file_required", "multipart field file is required")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (50<<20)+1))
	if err != nil || len(data) == 0 {
		writeError(writer, http.StatusBadRequest, "invalid_document", "document is empty or unreadable")
		return
	}
	if len(data) > 50<<20 {
		writeError(writer, http.StatusRequestEntityTooLarge, "document_too_large", "document must not exceed 50 MiB")
		return
	}
	var metadata documentUploadMetadata
	decoder := json.NewDecoder(strings.NewReader(request.FormValue("metadata")))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&metadata); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_metadata", err.Error())
		return
	}
	metadata.DocumentID = strings.TrimSpace(metadata.DocumentID)
	metadata.Title = strings.TrimSpace(metadata.Title)
	if metadata.DocumentID == "" || len(metadata.DocumentID) > 200 || metadata.Title == "" {
		writeError(writer, http.StatusBadRequest, "invalid_metadata", "document_id and title are required")
		return
	}
	if metadata.SourceRevision <= 0 {
		metadata.SourceRevision = 1
	}
	if err := normalizeSourceMetadata(&metadata); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_source_metadata", err.Error())
		return
	}
	if dataset.ID == "public-medical-device-sales" && metadata.SourceReviewStatus != "approved" {
		writeError(writer, http.StatusUnprocessableEntity, "public_source_review_required", "public medical sales documents must pass source review before indexing")
		return
	}
	filename := path.Base(header.Filename)
	contentType := header.Header.Get("Content-Type")
	objectKey := strings.Join([]string{identity.TenantID, dataset.ID, strings.ReplaceAll(metadata.DocumentID, "/", "_"), "r" + strconv.FormatInt(metadata.SourceRevision, 10), filename}, "/")
	sourceURI, err := api.documentStore.Put(request.Context(), objectKey, contentType, data)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "document_store_failed", err.Error())
		return
	}
	documentIR, err := api.parser.Parse(request.Context(), filename, contentType, data)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "document_parse_failed", err.Error())
		return
	}
	irData, _ := json.Marshal(documentIR)
	irURI, _ := api.documentStore.Put(request.Context(), objectKey+".document-ir.json", "application/json", irData)
	registry, hasRegistry := api.store.(datasetaccess.DocumentRegistry)
	sourceHash := fmt.Sprintf("%x", sha256.Sum256(data))
	registryRecord := datasetaccess.KnowledgeDocumentRevision{
		DatasetID: dataset.ID, DocumentID: metadata.DocumentID, Title: metadata.Title,
		SourceRevision: metadata.SourceRevision, DocumentVersion: metadata.Version,
		FileName: filename, ContentType: contentType, SourceURI: sourceURI, IRURI: irURI,
		SourceHash: sourceHash, ParserStatus: documentIR.Status,
		IndexStatus: "parsed", BlockCount: len(documentIR.Blocks), Metadata: map[string]any{
			"domain": metadata.Domain, "manufacturer": metadata.Manufacturer, "product_family": metadata.ProductFamily,
			"model_codes": metadata.ModelCodes, "software_version_from": metadata.SoftwareVersionFrom, "software_version_to": metadata.SoftwareVersionTo,
			"hardware_revision": metadata.HardwareRevision, "region": metadata.Region, "language": metadata.Language,
			"effective_from": metadata.EffectiveFrom, "effective_to": metadata.EffectiveTo,
			"authority_level": metadata.AuthorityLevel, "document_revision": metadata.DocumentRevision, "supersedes": metadata.Supersedes,
			"device_identifiers": metadata.DeviceIdentifiers, "affected_lots": metadata.AffectedLots,
			"source_type": metadata.SourceType, "source_urls": metadata.SourceURLs, "collected_at": metadata.CollectedAt,
			"source_review_status": metadata.SourceReviewStatus, "source_reviewed_at": metadata.SourceReviewedAt,
			"source_content_sha256": sourceHash,
		}, Warnings: documentIR.Warnings, CreatedBy: identity.Subject,
	}
	if documentIR.Status == "ocr_required" {
		registryRecord.IndexStatus = "blocked"
		registryRecord.LastError = "OCR is required before this revision can be indexed"
		if hasRegistry {
			_ = registry.UpsertKnowledgeDocument(request.Context(), registryRecord)
		}
		writeJSON(writer, http.StatusUnprocessableEntity, map[string]any{
			"status": "ocr_required", "source_uri": sourceURI, "warnings": documentIR.Warnings, "preview": previewIRBlocks(documentIR.Blocks, 20),
		})
		return
	}
	visibility := dataset.Visibility
	roles := append([]string(nil), dataset.AllowedRoles...)
	if len(roles) == 0 {
		roles = []string{"viewer", "admin", "platform_admin"}
	}
	allowedTenants := []string(nil)
	if visibility == "tenant" {
		allowedTenants = []string{dataset.OwnerTenant}
	}
	firstSheet, firstRange := "", ""
	for _, block := range documentIR.Blocks {
		if firstSheet == "" && block.Provenance.Sheet != "" {
			firstSheet, firstRange = block.Provenance.Sheet, block.Provenance.CellRange
		}
	}
	idempotencyKey := fmt.Sprintf("upload:%s:%s:%d:%s", dataset.ID, metadata.DocumentID, metadata.SourceRevision, sourceHash[:16])
	job, duplicate, err := api.ingestionJobs.Submit(ingestionjob.SubmitRequest{
		IdempotencyKey: idempotencyKey, TenantID: identity.TenantID, DatasetID: dataset.ID, CreatedBy: identity.Subject,
		Change: milvus.LifecycleChange{
			EventID: idempotencyKey, Operation: milvus.OperationUpsert, Revision: metadata.SourceRevision,
			Document: &milvus.LifecycleDocument{
				ID: metadata.DocumentID, DatasetID: dataset.ID, Title: metadata.Title, Content: documentIR.Markdown(), Product: dataset.Product,
				Version: metadata.Version, Status: "active", Visibility: visibility, AllowedTenants: allowedTenants, AllowedRoles: roles,
				Domain: metadata.Domain, Manufacturer: metadata.Manufacturer, ProductFamily: metadata.ProductFamily, ModelCodes: metadata.ModelCodes,
				SoftwareVersionFrom: metadata.SoftwareVersionFrom, SoftwareVersionTo: metadata.SoftwareVersionTo,
				HardwareRevision: metadata.HardwareRevision, Region: metadata.Region, Language: metadata.Language,
				EffectiveFrom: metadata.EffectiveFrom, EffectiveTo: metadata.EffectiveTo, AuthorityLevel: metadata.AuthorityLevel,
				DocumentRevision: metadata.DocumentRevision, Supersedes: metadata.Supersedes, SourceFile: sourceURI, SourceSheet: firstSheet, SourceCellRange: firstRange,
				DeviceIdentifiers: metadata.DeviceIdentifiers, AffectedLots: metadata.AffectedLots,
			},
		},
	})
	if err != nil {
		registryRecord.IndexStatus = "failed"
		registryRecord.LastError = err.Error()
		if hasRegistry {
			_ = registry.UpsertKnowledgeDocument(request.Context(), registryRecord)
		}
		writeError(writer, http.StatusUnprocessableEntity, "document_ingest_failed", err.Error())
		return
	}
	registryRecord.JobID = job.ID
	registryRecord.IndexStatus = job.Status
	if hasRegistry {
		if err := registry.UpsertKnowledgeDocument(request.Context(), registryRecord); err != nil {
			writeError(writer, http.StatusServiceUnavailable, "document_registry_failed", err.Error())
			return
		}
	}
	writeJSON(writer, http.StatusAccepted, map[string]any{
		"job_id": job.ID, "status": job.Status, "duplicate": duplicate, "document_id": metadata.DocumentID,
		"source_uri": sourceURI, "parser_status": documentIR.Status, "blocks": len(documentIR.Blocks), "warnings": documentIR.Warnings,
		"preview": previewIRBlocks(documentIR.Blocks, 20),
	})
}

func previewIRBlocks(blocks []documentparser.Block, limit int) []documentparser.Block {
	if limit <= 0 || len(blocks) <= limit {
		return blocks
	}
	return blocks[:limit]
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
			ID: input.DocumentID, DatasetID: dataset.ID, Title: input.Title, Content: input.Content, Product: dataset.Product,
			Version: input.Version, Status: "active", Visibility: visibility,
			AllowedTenants: tenants, AllowedRoles: roles,
			Domain: input.MedicalMetadata.Domain, Manufacturer: input.MedicalMetadata.Manufacturer,
			ProductFamily: input.MedicalMetadata.ProductFamily, ModelCodes: input.MedicalMetadata.ModelCodes,
			SoftwareVersionFrom: input.MedicalMetadata.SoftwareVersionFrom, SoftwareVersionTo: input.MedicalMetadata.SoftwareVersionTo,
			HardwareRevision: input.MedicalMetadata.HardwareRevision, Region: input.MedicalMetadata.Region,
			Language: input.MedicalMetadata.Language, EffectiveFrom: input.MedicalMetadata.EffectiveFrom, EffectiveTo: input.MedicalMetadata.EffectiveTo,
			AuthorityLevel:   input.MedicalMetadata.AuthorityLevel,
			DocumentRevision: input.MedicalMetadata.DocumentRevision, Supersedes: input.MedicalMetadata.Supersedes, SourceFile: input.MedicalMetadata.SourceFile,
			DeviceIdentifiers: input.MedicalMetadata.DeviceIdentifiers, AffectedLots: input.MedicalMetadata.AffectedLots,
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
