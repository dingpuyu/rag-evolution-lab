package generation

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAICompatibleGenerator talks to providers that implement the Chat
// Completions wire format. DeepSeek is the default documented deployment, but
// BaseURL keeps the adapter usable with OpenAI-compatible gateways and private
// model services.
type OpenAICompatibleGenerator struct {
	BaseURL    string
	APIKey     string
	Model      string
	Provider   string
	Client     *http.Client
	Timeout    time.Duration
	NumPredict int
}

func (generator OpenAICompatibleGenerator) Name() string {
	provider := strings.TrimSpace(generator.Provider)
	if provider == "" {
		provider = "openai-compatible"
	}
	return "openai-compatible-" + provider
}

func (generator OpenAICompatibleGenerator) Generate(ctx context.Context, request Request) (Generation, error) {
	return generator.generate(ctx, request, false, nil)
}

func (generator OpenAICompatibleGenerator) GenerateStream(ctx context.Context, request Request, sink func(GenerationStreamEvent) error) (Generation, error) {
	return generator.generate(ctx, request, true, sink)
}

type openAIChatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
		Delta        struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func (generator OpenAICompatibleGenerator) generate(ctx context.Context, request Request, stream bool, sink func(GenerationStreamEvent) error) (Generation, error) {
	if strings.TrimSpace(generator.APIKey) == "" {
		return Generation{}, fmt.Errorf("%s API key is not configured", generator.Name())
	}
	if strings.TrimSpace(generator.Model) == "" {
		return Generation{}, fmt.Errorf("%s model must not be empty", generator.Name())
	}
	if strings.TrimSpace(request.Query) == "" {
		return Generation{}, fmt.Errorf("generation query must not be empty")
	}
	systemPrompt, userMessage, err := requestPrompt(Request{Query: strings.TrimSpace(request.Query), Evidence: request.Evidence, Mode: request.Mode})
	if err != nil {
		return Generation{}, err
	}
	maxTokens := generator.NumPredict
	if maxTokens <= 0 {
		maxTokens = 512
	}
	payloadBody := map[string]any{
		"model": generator.Model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userMessage},
		},
		"response_format": map[string]string{"type": "json_object"},
		"stream":          stream,
		"temperature":     0,
		"max_tokens":      maxTokens,
	}
	if stream {
		payloadBody["stream_options"] = map[string]bool{"include_usage": true}
	}
	payload, err := json.Marshal(payloadBody)
	if err != nil {
		return Generation{}, fmt.Errorf("encode %s chat request: %w", generator.Name(), err)
	}
	baseURL := strings.TrimRight(strings.TrimSpace(generator.BaseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return Generation{}, fmt.Errorf("create %s chat request: %w", generator.Name(), err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+strings.TrimSpace(generator.APIKey))
	client := generator.Client
	if client == nil {
		timeout := generator.Timeout
		if timeout <= 0 {
			timeout = 2 * time.Minute
		}
		client = &http.Client{Timeout: timeout}
	}
	started := time.Now()
	response, err := client.Do(httpRequest)
	if err != nil {
		return Generation{}, fmt.Errorf("call %s chat: %w", generator.Name(), err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 16<<10))
		return Generation{}, fmt.Errorf("%s chat returned %s: %s", generator.Name(), response.Status, strings.TrimSpace(string(body)))
	}
	var content strings.Builder
	var model, finishReason string
	var usage Usage
	if stream {
		if err := readOpenAIStream(response.Body, &content, &model, &finishReason, &usage, sink); err != nil {
			return Generation{}, err
		}
	} else {
		var decoded openAIChatResponse
		if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
			return Generation{}, fmt.Errorf("decode %s chat response: %w", generator.Name(), err)
		}
		if len(decoded.Choices) == 0 {
			return Generation{}, fmt.Errorf("%s chat response contains no choices", generator.Name())
		}
		model, finishReason = decoded.Model, decoded.Choices[0].FinishReason
		content.WriteString(decoded.Choices[0].Message.Content)
		if decoded.Usage != nil {
			usage = Usage{PromptTokens: decoded.Usage.PromptTokens, CompletionTokens: decoded.Usage.CompletionTokens}
		}
	}
	if model == "" {
		model = generator.Model
	}
	output, err := decodeGroundedOutput(content.String())
	if err != nil {
		return Generation{}, fmt.Errorf("decode %s grounded JSON output: %w", generator.Name(), err)
	}
	return Generation{
		Output: output, Model: model, PromptVersion: promptVersion(request.Mode), FinishReason: finishReason,
		LatencyMS: milliseconds(time.Since(started)), Usage: usage,
	}, nil
}

func readOpenAIStream(reader io.Reader, content *strings.Builder, model *string, finishReason *string, usage *Usage, sink func(GenerationStreamEvent) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	extractor := answerJSONStream{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk openAIChatResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return fmt.Errorf("decode openai-compatible stream chunk: %w", err)
		}
		if chunk.Model != "" {
			*model = chunk.Model
		}
		if chunk.Usage != nil {
			*usage = Usage{PromptTokens: chunk.Usage.PromptTokens, CompletionTokens: chunk.Usage.CompletionTokens}
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]
		if choice.FinishReason != "" {
			*finishReason = choice.FinishReason
		}
		if choice.Delta.Content == "" {
			continue
		}
		content.WriteString(choice.Delta.Content)
		if sink != nil {
			if delta := extractor.Push(choice.Delta.Content); delta != "" {
				if err := sink(GenerationStreamEvent{Delta: delta}); err != nil {
					return fmt.Errorf("consume %s stream: %w", "openai-compatible", err)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read openai-compatible stream: %w", err)
	}
	return nil
}
