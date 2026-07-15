package ingest

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
)

type Chunker struct {
	MaxRunes int
}

func (c Chunker) Chunk(document domain.Document) []domain.Chunk {
	maxRunes := c.MaxRunes
	if maxRunes <= 0 {
		maxRunes = 700
	}

	sections := splitMarkdown(document.Content, maxRunes)
	chunks := make([]domain.Chunk, 0, len(sections))
	for index, section := range sections {
		chunks = append(chunks, domain.Chunk{
			ID:             fmt.Sprintf("%s#c%03d", document.ID, index+1),
			DocumentID:     document.ID,
			DocumentTitle:  document.Title,
			Content:        section.content,
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
	headings []string
	content  string
}

func splitMarkdown(content string, maxRunes int) []section {
	blocks := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n\n")
	var result []section
	var headings []string
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
		for _, part := range splitLongBlock(block, maxRunes-len([]rune(prefix))) {
			result = append(result, section{
				headings: append([]string(nil), headings...),
				content:  strings.TrimSpace(prefix + part),
			})
		}
	}
	return result
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
	if maxRunes <= 0 || utf8.RuneCountInString(block) <= maxRunes {
		return []string{block}
	}
	runes := []rune(block)
	parts := make([]string, 0, (len(runes)/maxRunes)+1)
	for len(runes) > 0 {
		end := maxRunes
		if end > len(runes) {
			end = len(runes)
		}
		parts = append(parts, string(runes[:end]))
		runes = runes[end:]
	}
	return parts
}
