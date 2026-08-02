package ingest

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
)

// Chunker turns a document into deterministic child chunks. The default mode
// intentionally keeps the original header-aware behavior; PageAware and
// OverlapRunes are opt-in so existing indexes do not change unexpectedly.
type Chunker struct {
	MaxRunes     int
	OverlapRunes int
	PageAware    bool
}

func (c Chunker) Chunk(document domain.Document) []domain.Chunk {
	maxRunes := c.MaxRunes
	if maxRunes <= 0 {
		maxRunes = 700
	}
	overlap := c.OverlapRunes
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= maxRunes {
		overlap = maxRunes / 5
	}

	sections := splitMarkdownWithOptions(document.Content, maxRunes, overlap, c.PageAware)
	chunks := make([]domain.Chunk, 0, len(sections))
	for index, section := range sections {
		if section.parentSequence > 0 {
			section.parentID = fmt.Sprintf("%s#p%03d", document.ID, section.parentSequence)
		}
		chunks = append(chunks, domain.Chunk{
			ID:             fmt.Sprintf("%s#c%03d", document.ID, index+1),
			DocumentID:     document.ID,
			DocumentTitle:  document.Title,
			Content:        section.content,
			ParentID:       section.parentID,
			ParentContent:  section.parentContent,
			ParentSequence: section.parentSequence,
			SourcePage:     section.sourcePage,
			Sequence:       index + 1,
			HeadingPath:    append([]string(nil), section.headings...),
			Product:        document.Product,
			Version:        document.Version,
			Status:         document.Status,
			Visibility:     document.Visibility,
			AllowedTenants: append([]string(nil), document.AllowedTenants...),
			AllowedRoles:   append([]string(nil), document.AllowedRoles...),
			Quality:        document.Quality,
		})
	}
	return chunks
}

type section struct {
	headings       []string
	content        string
	parentID       string
	parentContent  string
	parentSequence int
	sourcePage     int
}

type parentSection struct {
	headings   []string
	content    string
	sourcePage int
}

// splitMarkdown is retained as the compatibility helper used by the original
// chunker tests and callers. New callers that need provenance use Chunker.
func splitMarkdown(content string, maxRunes int) []section {
	return splitMarkdownWithOptions(content, maxRunes, 0, false)
}

func splitMarkdownWithOptions(content string, maxRunes, overlap int, pageAware bool) []section {
	if !pageAware && overlap == 0 {
		return splitMarkdownLegacy(content, maxRunes)
	}
	parents := parseMarkdownParents(content, pageAware)
	result := make([]section, 0, len(parents))
	childIndex := 0
	for parentIndex, parent := range parents {
		prefix := ""
		if len(parent.headings) > 0 {
			prefix = strings.Join(parent.headings, " > ") + "\n"
		}
		parentContent := strings.TrimSpace(prefix + parent.content)
		parentID := fmt.Sprintf("preview#p%03d", parentIndex+1)
		parts := splitLongBlockWithOverlap(parent.content, maxRunes-len([]rune(prefix)), overlap)
		for _, part := range parts {
			childIndex++
			result = append(result, section{
				headings:       append([]string(nil), parent.headings...),
				content:        strings.TrimSpace(prefix + part),
				parentID:       parentID,
				parentContent:  parentContent,
				parentSequence: parentIndex + 1,
				sourcePage:     parent.sourcePage,
			})
		}
	}
	return result
}

// splitMarkdownLegacy preserves the original paragraph-level chunking used by
// the persisted lifecycle index. The richer parent/page grouping is enabled by
// the preview path (PageAware=true) or an explicit overlap configuration.
func splitMarkdownLegacy(content string, maxRunes int) []section {
	blocks := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n\n")
	var result []section
	var headings []string
	parentIndex := 0
	for _, raw := range blocks {
		block := strings.TrimSpace(raw)
		if block == "" {
			continue
		}
		if strings.HasPrefix(block, "#") {
			lines := strings.Split(block, "\n")
			if title, level, ok := parseHeading(lines[0]); ok {
				if level <= len(headings) {
					headings = headings[:level-1]
				}
				for len(headings) < level-1 {
					headings = append(headings, "")
				}
				headings = append(headings, title)
				block = strings.TrimSpace(strings.Join(lines[1:], "\n"))
				if block == "" {
					continue
				}
			}
		}

		prefix := ""
		if len(headings) > 0 {
			prefix = strings.Join(headings, " > ") + "\n"
		}
		parentIndex++
		parentContent := strings.TrimSpace(prefix + block)
		for _, part := range splitLongBlock(block, maxRunes-len([]rune(prefix))) {
			result = append(result, section{
				headings: append([]string(nil), headings...), content: strings.TrimSpace(prefix + part),
				parentID: fmt.Sprintf("preview#p%03d", parentIndex), parentContent: parentContent,
				parentSequence: parentIndex, sourcePage: 0,
			})
		}
	}
	return result
}

func parseMarkdownParents(content string, pageAware bool) []parentSection {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	currentPage := 1
	var result []parentSection
	var headings []string
	var current strings.Builder
	currentSourcePage := currentPage

	flush := func() {
		text := strings.TrimSpace(current.String())
		if text == "" {
			current.Reset()
			return
		}
		result = append(result, parentSection{
			headings:   append([]string(nil), headings...),
			content:    text,
			sourcePage: currentSourcePage,
		})
		current.Reset()
	}

	pages := []string{normalized}
	if pageAware {
		pages = strings.Split(normalized, "\f")
	}
	for pageIndex, rawPage := range pages {
		if pageAware {
			currentPage = pageIndex + 1
		}
		blocks := strings.Split(rawPage, "\n\n")
		for _, raw := range blocks {
			block := strings.TrimSpace(raw)
			if block == "" {
				continue
			}
			if pageAware {
				var markerPage int
				block, markerPage = stripPageMarker(block)
				if markerPage > 0 {
					currentPage = markerPage
				}
				block = strings.TrimSpace(block)
				if block == "" {
					continue
				}
			}
			if strings.HasPrefix(block, "#") {
				lines := strings.Split(block, "\n")
				if title, level, ok := parseHeading(lines[0]); ok {
					flush()
					if level <= len(headings) {
						headings = headings[:level-1]
					}
					for len(headings) < level-1 {
						headings = append(headings, "")
					}
					headings = append(headings, title)
					block = strings.TrimSpace(strings.Join(lines[1:], "\n"))
					if block == "" {
						continue
					}
				}
			}
			if current.Len() == 0 {
				currentSourcePage = currentPage
			} else {
				current.WriteString("\n\n")
			}
			current.WriteString(block)
		}
		flush()
	}
	return result
}

func stripPageMarker(block string) (string, int) {
	const prefix = "<!-- page:"
	start := strings.Index(block, prefix)
	if start < 0 {
		return block, 0
	}
	rest := block[start+len(prefix):]
	end := strings.Index(rest, "-->")
	if end < 0 {
		return block, 0
	}
	page, err := strconv.Atoi(strings.TrimSpace(rest[:end]))
	if err != nil || page <= 0 {
		return block, 0
	}
	cleaned := strings.TrimSpace(block[:start] + " " + rest[end+3:])
	return cleaned, page
}

func parseHeading(line string) (string, int, bool) {
	trimmed := strings.TrimSpace(line)
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level >= len(trimmed) || trimmed[level] != ' ' {
		return "", 0, false
	}
	return strings.TrimSpace(trimmed[level:]), level, true
}

func splitLongBlock(block string, maxRunes int) []string {
	return splitLongBlockWithOverlap(block, maxRunes, 0)
}

func splitLongBlockWithOverlap(block string, maxRunes, overlap int) []string {
	if maxRunes <= 0 || utf8.RuneCountInString(block) <= maxRunes {
		return []string{block}
	}
	if overlap < 0 || overlap >= maxRunes {
		overlap = 0
	}
	runes := []rune(block)
	step := maxRunes - overlap
	parts := make([]string, 0, (len(runes)/step)+1)
	for start := 0; start < len(runes); {
		end := start + maxRunes
		if end > len(runes) {
			end = len(runes)
		}
		parts = append(parts, string(runes[start:end]))
		if end == len(runes) {
			break
		}
		start += step
	}
	return parts
}
