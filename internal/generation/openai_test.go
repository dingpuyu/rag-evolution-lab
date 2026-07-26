package generation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAICompatibleGeneratorUsesJSONModeAndAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("unexpected auth header %q", request.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != "deepseek-chat" || body["stream"] != false {
			t.Fatalf("unexpected request %#v", body)
		}
		format, ok := body["response_format"].(map[string]any)
		if !ok || format["type"] != "json_object" {
			t.Fatalf("JSON mode missing: %#v", body["response_format"])
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"model": "deepseek-chat",
			"choices": []any{map[string]any{
				"message":       map[string]string{"content": `{"answerable":true,"answer":"已配置","citations":[{"chunk_id":"doc#c1","document_id":"doc"}],"refusal_reason":""}`},
				"finish_reason": "stop",
			}},
			"usage": map[string]int{"prompt_tokens": 42, "completion_tokens": 8},
		})
	}))
	defer server.Close()

	generator := OpenAICompatibleGenerator{BaseURL: server.URL, APIKey: "test-token", Model: "deepseek-chat", Provider: "deepseek", Client: server.Client()}
	result, err := generator.Generate(context.Background(), Request{
		Query: "配置", Evidence: []Evidence{{ChunkID: "doc#c1", DocumentID: "doc", Content: "已配置"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Answerable || result.Answer != "已配置" || result.Model != "deepseek-chat" || result.Usage.PromptTokens != 42 || result.Usage.CompletionTokens != 8 {
		t.Fatalf("unexpected OpenAI-compatible result %#v", result)
	}
}

func TestOpenAICompatibleGeneratorParsesSSEAndEmitsAnswerDeltas(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["stream"] != true {
			t.Fatalf("stream request missing: %#v", body)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		flusher := writer.(http.Flusher)
		chunks := []string{
			`data: {"id":"1","model":"deepseek-chat","choices":[{"delta":{"content":"{\"answerable\":true,\"answer\":\"Deep"},"finish_reason":null}]}`,
			`data: {"id":"1","model":"deepseek-chat","choices":[{"delta":{"content":"Seek"},"finish_reason":null}]}`,
			`data: {"id":"1","model":"deepseek-chat","choices":[{"delta":{"content":"\",\"citations\":[{\"chunk_id\":\"doc#c1\",\"document_id\":\"doc\"}],\"refusal_reason\":\"\"}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":4}}`,
			"data: [DONE]",
		}
		for _, chunk := range chunks {
			_, _ = writer.Write([]byte(chunk + "\n\n"))
			flusher.Flush()
		}
	}))
	defer server.Close()

	generator := OpenAICompatibleGenerator{BaseURL: server.URL, APIKey: "test-token", Model: "deepseek-chat", Provider: "deepseek", Client: server.Client()}
	var deltas []string
	result, err := generator.GenerateStream(context.Background(), Request{
		Query: "配置", Evidence: []Evidence{{ChunkID: "doc#c1", DocumentID: "doc", Content: "DeepSeek"}},
	}, func(event GenerationStreamEvent) error {
		deltas = append(deltas, event.Delta)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Answer != "DeepSeek" || result.Usage.PromptTokens != 12 || strings.Join(deltas, "") != "DeepSeek" {
		t.Fatalf("unexpected stream result=%#v deltas=%q", result, deltas)
	}
}

func TestOpenAICompatibleGeneratorRequiresAPIKey(t *testing.T) {
	generator := OpenAICompatibleGenerator{Model: "deepseek-chat", Provider: "deepseek"}
	if _, err := generator.Generate(context.Background(), Request{Query: "q", Evidence: []Evidence{{Content: "e"}}}); err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("expected missing API key error, got %v", err)
	}
}
