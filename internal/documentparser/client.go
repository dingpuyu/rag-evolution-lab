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
	lastHeading := ""
	for _, block := range document.Blocks {
		if block.Provenance.Page > 0 {
			fmt.Fprintf(&output, "<!-- page: %d -->\n", block.Provenance.Page)
		}
		if block.Provenance.Sheet != "" {
			fmt.Fprintf(&output, "<!-- sheet: %s; range: %s -->\n", safeMarkerValue(block.Provenance.Sheet), safeMarkerValue(block.Provenance.CellRange))
		}
		heading := strings.Join(block.HeadingPath, " > ")
		if heading != "" && heading != lastHeading {
			fmt.Fprintf(&output, "## %s\n\n", heading)
			lastHeading = heading
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
