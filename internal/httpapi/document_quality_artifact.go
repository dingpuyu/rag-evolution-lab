package httpapi

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/documentparser"
	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
	"github.com/dingpuyu/rag-evolution-lab/internal/ingest"
)

var documentQualityCaseID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,199}$`)

type documentQualityArtifactBlock struct {
	BlockType       string   `json:"block_type"`
	Text            string   `json:"text"`
	Page            int      `json:"page,omitempty"`
	SourceSheet     string   `json:"source_sheet,omitempty"`
	SourceCellRange string   `json:"source_cell_range,omitempty"`
	HeadingPath     []string `json:"heading_path,omitempty"`
	Confidence      *float64 `json:"confidence,omitempty"`
}

type documentQualityRemovedBlock struct {
	documentQualityArtifactBlock
	Reason string `json:"reason"`
}

type documentQualityArtifactChunk struct {
	ChunkID         string   `json:"chunk_id"`
	ParentID        string   `json:"parent_id"`
	Content         string   `json:"content"`
	ParentContent   string   `json:"parent_content,omitempty"`
	SourcePage      int      `json:"source_page,omitempty"`
	SourceSheet     string   `json:"source_sheet,omitempty"`
	SourceCellRange string   `json:"source_cell_range,omitempty"`
	HeadingPath     []string `json:"heading_path,omitempty"`
}

type documentQualityArtifact struct {
	Schema            string                         `json:"schema"`
	CaseID            string                         `json:"case_id"`
	DatasetID         string                         `json:"dataset_id,omitempty"`
	Status            string                         `json:"status"`
	Indexed           bool                           `json:"indexed"`
	ConfigFingerprint string                         `json:"config_fingerprint"`
	Blocks            []documentQualityArtifactBlock `json:"blocks"`
	Cleaning          struct {
		RemovedBlocks []documentQualityRemovedBlock `json:"removed_blocks"`
	} `json:"cleaning"`
	Chunks    []documentQualityArtifactChunk `json:"chunks"`
	Retrieval []any                          `json:"retrieval"`
	Runtime   struct {
		DurationMS float64 `json:"duration_ms"`
	} `json:"runtime"`
	DocumentIR documentparser.DocumentIR `json:"document_ir"`
}

// documentQualityArtifact parses and chunks an uploaded file for evaluation,
// but deliberately does not persist the source, enqueue ingestion, embed text,
// or write an index. Only an owning tenant administrator (or platform admin)
// can inspect the full parser output and cleaner deletion audit.
func (api *DatasetAPI) documentQualityArtifact(writer http.ResponseWriter, request *http.Request) {
	dataset, identity, ok := api.authorizeDataset(writer, request)
	if !ok {
		return
	}
	if !canManageDatasetDocuments(dataset, identity) {
		writeError(writer, http.StatusForbidden, "document_forbidden", "document quality preview requires the owning tenant administrator")
		return
	}
	if api.parser == nil {
		writeError(writer, http.StatusServiceUnavailable, "document_parser_unavailable", "document parser must be configured")
		return
	}
	started := time.Now()
	request.Body = http.MaxBytesReader(writer, request.Body, 52<<20)
	if err := request.ParseMultipartForm(52 << 20); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_multipart", err.Error())
		return
	}
	caseID := strings.TrimSpace(request.FormValue("case_id"))
	if !documentQualityCaseID.MatchString(caseID) {
		writeError(writer, http.StatusBadRequest, "invalid_case_id", "case_id must use 1-200 letters, digits, dot, underscore, colon or dash")
		return
	}
	maxRunes, ok := parseDocumentQualityInteger(writer, request.FormValue("max_runes"), 700, 100, 2000, "max_runes")
	if !ok {
		return
	}
	defaultOverlap := 80
	if maxRunes < 400 {
		defaultOverlap = maxRunes / 5
	}
	overlapRunes, ok := parseDocumentQualityInteger(writer, request.FormValue("overlap_runes"), defaultOverlap, 0, maxRunes/2-1, "overlap_runes")
	if !ok {
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
	filename := path.Base(header.Filename)
	contentType := header.Header.Get("Content-Type")
	documentIR, err := api.parser.Parse(request.Context(), filename, contentType, data)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "document_parse_failed", err.Error())
		return
	}
	artifact := buildDocumentQualityArtifact(caseID, maxRunes, overlapRunes, documentIR)
	artifact.DatasetID = dataset.ID
	artifact.Runtime.DurationMS = float64(time.Since(started).Microseconds()) / 1000
	writeJSON(writer, http.StatusOK, artifact)
}

func parseDocumentQualityInteger(writer http.ResponseWriter, raw string, fallback, minimum, maximum int, name string) (int, bool) {
	value := fallback
	var err error
	if strings.TrimSpace(raw) != "" {
		value, err = strconv.Atoi(raw)
	}
	if err != nil || value < minimum || value > maximum {
		writeError(writer, http.StatusBadRequest, "invalid_document_quality_config", fmt.Sprintf("%s must be between %d and %d", name, minimum, maximum))
		return 0, false
	}
	return value, true
}

func buildDocumentQualityArtifact(caseID string, maxRunes, overlapRunes int, documentIR documentparser.DocumentIR) documentQualityArtifact {
	config, _ := json.Marshal(map[string]any{
		"document_ir_schema": documentIR.SchemaVersion,
		"parser":             documentIR.Quality.Parser,
		"parser_version":     documentIR.Quality.ParserVersion,
		"max_runes":          maxRunes,
		"overlap_runes":      overlapRunes,
		"page_aware":         true,
	})
	fingerprint := fmt.Sprintf("sha256:%x", sha256.Sum256(config))
	artifact := documentQualityArtifact{
		Schema: "agent-evaluation.document-quality.artifact.v1", CaseID: caseID,
		Status: documentIR.Status, Indexed: false, ConfigFingerprint: fingerprint,
		Blocks: make([]documentQualityArtifactBlock, 0, len(documentIR.Blocks)),
		Chunks: []documentQualityArtifactChunk{}, Retrieval: []any{}, DocumentIR: documentIR,
	}
	artifact.Cleaning.RemovedBlocks = []documentQualityRemovedBlock{}
	for _, block := range documentIR.Blocks {
		artifact.Blocks = append(artifact.Blocks, qualityArtifactBlock(block))
	}
	for _, removal := range documentIR.CleaningRemovals {
		artifact.Cleaning.RemovedBlocks = append(artifact.Cleaning.RemovedBlocks, documentQualityRemovedBlock{
			documentQualityArtifactBlock: qualityArtifactBlock(removal.Block), Reason: removal.Reason,
		})
	}
	chunks := (ingest.Chunker{MaxRunes: maxRunes, OverlapRunes: overlapRunes, PageAware: true}).Chunk(domain.Document{
		ID: caseID, Title: documentIR.SourceFile, Content: documentIR.Markdown(),
	})
	for _, chunk := range chunks {
		artifact.Chunks = append(artifact.Chunks, documentQualityArtifactChunk{
			ChunkID: chunk.ID, ParentID: chunk.ParentID, Content: chunk.Content,
			ParentContent: chunk.ParentContent, SourcePage: chunk.SourcePage,
			SourceSheet: chunk.SourceSheet, SourceCellRange: chunk.SourceCellRange,
			HeadingPath: append([]string(nil), chunk.HeadingPath...),
		})
	}
	return artifact
}

func qualityArtifactBlock(block documentparser.Block) documentQualityArtifactBlock {
	return documentQualityArtifactBlock{
		BlockType: block.BlockType, Text: block.Text, Page: block.Provenance.Page,
		SourceSheet: block.Provenance.Sheet, SourceCellRange: block.Provenance.CellRange,
		HeadingPath: append([]string(nil), block.HeadingPath...), Confidence: block.Confidence,
	}
}
