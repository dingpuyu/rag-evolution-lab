package documentparser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

type Provenance struct {
	SourceFile string `json:"source_file"`
	Page       int    `json:"page"`
	Sheet      string `json:"sheet"`
	CellRange  string `json:"cell_range"`
}

type Block struct {
	BlockType   string     `json:"block_type"`
	Text        string     `json:"text"`
	HeadingPath []string   `json:"heading_path"`
	Provenance  Provenance `json:"provenance"`
}

type DocumentIR struct {
	SchemaVersion string   `json:"schema_version"`
	Status        string   `json:"status"`
	SourceFile    string   `json:"source_file"`
	MIMEType      string   `json:"mime_type"`
	SHA256        string   `json:"sha256"`
	Blocks        []Block  `json:"blocks"`
	Warnings      []string `json:"warnings"`
}

func (document DocumentIR) Markdown() string {
	var output strings.Builder
	var lastHeadingPath []string
	for _, block := range document.Blocks {
		fmt.Fprintf(&output, "<!-- source: page=%d; sheet=%s; range=%s -->\n",
			block.Provenance.Page, safeMarkerValue(block.Provenance.Sheet), safeMarkerValue(block.Provenance.CellRange))
		heading := strings.Join(block.HeadingPath, " > ")
		if heading != "" {
			common := 0
			for common < len(lastHeadingPath) && common < len(block.HeadingPath) && lastHeadingPath[common] == block.HeadingPath[common] {
				common++
			}
			for index := common; index < len(block.HeadingPath); index++ {
				part := block.HeadingPath[index]
				part = strings.TrimSpace(strings.ReplaceAll(part, "\n", " "))
				if part == "" {
					continue
				}
				level := index + 1
				if level > 6 {
					level = 6
				}
				fmt.Fprintf(&output, "%s %s\n\n", strings.Repeat("#", level), part)
			}
			lastHeadingPath = append(lastHeadingPath[:0], block.HeadingPath...)
		}
		// A heading block has already been represented by the structured
		// heading path above. Writing its text again would pollute retrieval
		// with duplicate titles and can produce artificial parent chunks.
		if block.BlockType == "heading" && heading != "" {
			continue
		}
		output.WriteString(strings.TrimSpace(block.Text))
		output.WriteString("\n\n")
	}
	return strings.TrimSpace(output.String())
}

func safeMarkerValue(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), "-->", "")
	value = strings.ReplaceAll(value, ";", "_")
	return value
}

type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string) *Client {
	return &Client{baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"), http: &http.Client{Timeout: 90 * time.Second}}
}

func (client *Client) Parse(ctx context.Context, filename, contentType string, data []byte) (DocumentIR, error) {
	if client.baseURL == "" {
		return DocumentIR{}, fmt.Errorf("document parser URL is not configured")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return DocumentIR{}, err
	}
	if _, err := part.Write(data); err != nil {
		return DocumentIR{}, err
	}
	if err := writer.Close(); err != nil {
		return DocumentIR{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/v1/parse", &body)
	if err != nil {
		return DocumentIR{}, err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	if contentType != "" {
		request.Header.Set("X-Source-Content-Type", contentType)
	}
	response, err := client.http.Do(request)
	if err != nil {
		return DocumentIR{}, fmt.Errorf("call document parser: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return DocumentIR{}, fmt.Errorf("document parser returned %d: %s", response.StatusCode, strings.TrimSpace(string(payload)))
	}
	var result DocumentIR
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return DocumentIR{}, fmt.Errorf("decode Document IR: %w", err)
	}
	return result, nil
}
