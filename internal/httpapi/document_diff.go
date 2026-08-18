package httpapi

import (
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/dingpuyu/rag-evolution-lab/internal/datasetaccess"
	"github.com/dingpuyu/rag-evolution-lab/internal/documentparser"
)

const maxDocumentDiffItems = 100

type documentRevisionDescriptor struct {
	SourceRevision  int64          `json:"source_revision"`
	Title           string         `json:"title"`
	DocumentVersion string         `json:"document_version,omitempty"`
	FileName        string         `json:"file_name"`
	SourceHash      string         `json:"source_hash"`
	ParserStatus    string         `json:"parser_status"`
	BlockCount      int            `json:"block_count"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

type documentMetadataChange struct {
	Field string `json:"field"`
	From  any    `json:"from,omitempty"`
	To    any    `json:"to,omitempty"`
}

type documentBlockChange struct {
	ChangeType string                    `json:"change_type"`
	Locator    string                    `json:"locator"`
	BlockType  string                    `json:"block_type"`
	Heading    []string                  `json:"heading_path,omitempty"`
	Provenance documentparser.Provenance `json:"provenance"`
	FromText   string                    `json:"from_text,omitempty"`
	ToText     string                    `json:"to_text,omitempty"`
}

type documentDiffSummary struct {
	Added     int `json:"added"`
	Removed   int `json:"removed"`
	Modified  int `json:"modified"`
	Unchanged int `json:"unchanged"`
}

type keyedDocumentBlock struct {
	Base    string
	Locator string
	Block   documentparser.Block
}

func (api *DatasetAPI) documentDiff(writer http.ResponseWriter, request *http.Request) {
	dataset, identity, ok := api.authorizeDataset(writer, request)
	if !ok {
		return
	}
	if !canManageDatasetDocuments(dataset, identity) {
		writeError(writer, http.StatusForbidden, "document_forbidden", "document revision comparison requires the owning tenant administrator")
		return
	}
	documentID := strings.TrimSpace(request.URL.Query().Get("document_id"))
	fromRevision, fromErr := strconv.ParseInt(request.URL.Query().Get("from_revision"), 10, 64)
	toRevision, toErr := strconv.ParseInt(request.URL.Query().Get("to_revision"), 10, 64)
	if documentID == "" || len(documentID) > 200 || fromErr != nil || toErr != nil || fromRevision <= 0 || toRevision <= 0 {
		writeError(writer, http.StatusBadRequest, "invalid_document_diff", "document_id, from_revision and to_revision are required")
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
	fromRecord := selectKnowledgeRevision(revisions, documentID, fromRevision)
	toRecord := selectKnowledgeRevision(revisions, documentID, toRevision)
	if fromRecord == nil || toRecord == nil {
		writeError(writer, http.StatusNotFound, "document_revision_not_found", "one or both document revisions were not found")
		return
	}
	if fromRecord.IRURI == "" || toRecord.IRURI == "" {
		writeError(writer, http.StatusConflict, "document_ir_required", "both revisions must have a persisted Document IR")
		return
	}
	fromIR, err := api.readDocumentIR(request.Context(), fromRecord.IRURI)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "document_ir_unavailable", fmt.Sprintf("read source revision: %v", err))
		return
	}
	toIR, err := api.readDocumentIR(request.Context(), toRecord.IRURI)
	if err != nil {
		writeError(writer, http.StatusServiceUnavailable, "document_ir_unavailable", fmt.Sprintf("read target revision: %v", err))
		return
	}
	summary, changes := compareDocumentBlocks(fromIR.Blocks, toIR.Blocks)
	truncated := len(changes) > maxDocumentDiffItems
	if truncated {
		changes = changes[:maxDocumentDiffItems]
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"dataset": dataset, "document_id": documentID,
		"from_revision": describeDocumentRevision(*fromRecord), "to_revision": describeDocumentRevision(*toRecord),
		"metadata_changes": compareDocumentMetadata(*fromRecord, *toRecord),
		"summary":          summary, "block_changes": changes, "truncated": truncated,
	})
}

func describeDocumentRevision(record datasetaccess.KnowledgeDocumentRevision) documentRevisionDescriptor {
	return documentRevisionDescriptor{
		SourceRevision: record.SourceRevision, Title: record.Title, DocumentVersion: record.DocumentVersion,
		FileName: record.FileName, SourceHash: record.SourceHash, ParserStatus: record.ParserStatus,
		BlockCount: record.BlockCount, Metadata: record.Metadata,
	}
}

func compareDocumentMetadata(from, to datasetaccess.KnowledgeDocumentRevision) []documentMetadataChange {
	changes := make([]documentMetadataChange, 0)
	appendChange := func(field string, oldValue, newValue any) {
		if !reflect.DeepEqual(oldValue, newValue) {
			changes = append(changes, documentMetadataChange{Field: field, From: oldValue, To: newValue})
		}
	}
	appendChange("title", from.Title, to.Title)
	appendChange("document_version", from.DocumentVersion, to.DocumentVersion)
	appendChange("file_name", from.FileName, to.FileName)
	appendChange("content_type", from.ContentType, to.ContentType)
	appendChange("source_hash", from.SourceHash, to.SourceHash)
	appendChange("parser_status", from.ParserStatus, to.ParserStatus)
	appendChange("block_count", from.BlockCount, to.BlockCount)
	keys := make(map[string]struct{}, len(from.Metadata)+len(to.Metadata))
	for key := range from.Metadata {
		keys[key] = struct{}{}
	}
	for key := range to.Metadata {
		keys[key] = struct{}{}
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		appendChange("metadata."+key, from.Metadata[key], to.Metadata[key])
	}
	return changes
}

func compareDocumentBlocks(from, to []documentparser.Block) (documentDiffSummary, []documentBlockChange) {
	fromBlocks := keyDocumentBlocks(from)
	toBlocks := keyDocumentBlocks(to)
	fromGroups := make(map[string][]keyedDocumentBlock)
	toGroups := make(map[string][]keyedDocumentBlock)
	for _, item := range fromBlocks {
		fromGroups[item.Base] = append(fromGroups[item.Base], item)
	}
	groupOrder := make([]string, 0)
	seenGroups := make(map[string]struct{})
	for _, item := range toBlocks {
		toGroups[item.Base] = append(toGroups[item.Base], item)
		if _, exists := seenGroups[item.Base]; !exists {
			seenGroups[item.Base] = struct{}{}
			groupOrder = append(groupOrder, item.Base)
		}
	}
	for _, item := range fromBlocks {
		if _, exists := seenGroups[item.Base]; !exists {
			seenGroups[item.Base] = struct{}{}
			groupOrder = append(groupOrder, item.Base)
		}
	}
	summary := documentDiffSummary{}
	changes := make([]documentBlockChange, 0)
	for _, base := range groupOrder {
		previous := fromGroups[base]
		current := toGroups[base]
		matchedPrevious := make([]bool, len(previous))
		matchedCurrent := make([]bool, len(current))
		for currentIndex := range current {
			for previousIndex := range previous {
				if matchedPrevious[previousIndex] || strings.TrimSpace(previous[previousIndex].Block.Text) != strings.TrimSpace(current[currentIndex].Block.Text) {
					continue
				}
				matchedPrevious[previousIndex], matchedCurrent[currentIndex] = true, true
				summary.Unchanged++
				break
			}
		}
		remainingPrevious := unmatchedBlocks(previous, matchedPrevious)
		remainingCurrent := unmatchedBlocks(current, matchedCurrent)
		paired := min(len(remainingPrevious), len(remainingCurrent))
		for index := 0; index < paired; index++ {
			summary.Modified++
			changes = append(changes, blockChange("modified", remainingCurrent[index], remainingPrevious[index].Block))
		}
		for _, item := range remainingCurrent[paired:] {
			summary.Added++
			changes = append(changes, blockChange("added", item, documentparser.Block{}))
		}
		for _, item := range remainingPrevious[paired:] {
			summary.Removed++
			changes = append(changes, blockChange("removed", item, item.Block))
		}
	}
	return summary, changes
}

func unmatchedBlocks(blocks []keyedDocumentBlock, matched []bool) []keyedDocumentBlock {
	result := make([]keyedDocumentBlock, 0, len(blocks))
	for index, block := range blocks {
		if !matched[index] {
			result = append(result, block)
		}
	}
	return result
}

func keyDocumentBlocks(blocks []documentparser.Block) []keyedDocumentBlock {
	result := make([]keyedDocumentBlock, 0, len(blocks))
	for index, block := range blocks {
		headingPath := block.HeadingPath
		// The first heading is normally the document title. A title correction
		// must not make every descendant look removed and re-added, so match
		// structure relative to the document root. The changed H1 itself is
		// still paired as a modified heading.
		if len(headingPath) > 0 {
			headingPath = headingPath[1:]
		}
		base := strings.Join([]string{
			block.BlockType, strings.Join(headingPath, "\x1f"), strconv.Itoa(block.Provenance.Page),
			block.Provenance.Sheet, block.Provenance.CellRange,
		}, "\x1e")
		result = append(result, keyedDocumentBlock{
			Base: base, Locator: displayBlockLocator(block, index), Block: block,
		})
	}
	return result
}

func displayBlockLocator(block documentparser.Block, index int) string {
	parts := make([]string, 0, 3)
	if len(block.HeadingPath) > 0 {
		parts = append(parts, strings.Join(block.HeadingPath, " › "))
	}
	if block.Provenance.Page > 0 {
		parts = append(parts, fmt.Sprintf("p.%d", block.Provenance.Page))
	}
	if block.Provenance.Sheet != "" {
		parts = append(parts, block.Provenance.Sheet+"!"+block.Provenance.CellRange)
	}
	if len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("block %d", index+1))
	}
	return strings.Join(parts, " · ")
}

func blockChange(changeType string, current keyedDocumentBlock, previous documentparser.Block) documentBlockChange {
	change := documentBlockChange{
		ChangeType: changeType, Locator: current.Locator, BlockType: current.Block.BlockType,
		Heading: current.Block.HeadingPath, Provenance: current.Block.Provenance,
	}
	switch changeType {
	case "added":
		change.ToText = current.Block.Text
	case "removed":
		change.FromText = current.Block.Text
	default:
		change.FromText = previous.Text
		change.ToText = current.Block.Text
	}
	return change
}
