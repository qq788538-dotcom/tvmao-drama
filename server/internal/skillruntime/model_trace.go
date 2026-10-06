package skillruntime

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const modelTraceFileName = "model-trace.jsonl"

type modelTraceTransport struct {
	base http.RoundTripper
	path string
	mu   sync.Mutex
}

type modelTraceUsage struct {
	PromptTokens     int `json:"promptTokens"`
	CompletionTokens int `json:"completionTokens"`
	TotalTokens      int `json:"totalTokens"`
}

type modelTraceRecord struct {
	Timestamp     string          `json:"timestamp"`
	DurationMS    int64           `json:"durationMs"`
	Method        string          `json:"method"`
	URL           string          `json:"url"`
	Status        int             `json:"status"`
	Model         string          `json:"model,omitempty"`
	Stream        bool            `json:"stream"`
	RequestBytes  int             `json:"requestBytes"`
	ResponseBytes int             `json:"responseBytes"`
	FinishReasons []string        `json:"finishReasons,omitempty"`
	ToolCalls     []string        `json:"toolCalls,omitempty"`
	Usage         modelTraceUsage `json:"usage"`
	Error         string          `json:"error,omitempty"`
	Request       any             `json:"request,omitempty"`
	Response      any             `json:"response,omitempty"`
}

func newModelTraceTransport(base http.RoundTripper, path string) *modelTraceTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &modelTraceTransport{base: base, path: path}
}

func (t *modelTraceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	started := time.Now()
	requestBody, requestReadErr := readAndRestoreRequestBody(req)
	record := modelTraceRecord{
		Timestamp:     started.UTC().Format(time.RFC3339Nano),
		Method:        req.Method,
		URL:           sanitizeTraceURL(req.URL),
		RequestBytes:  len(requestBody),
		FinishReasons: []string{},
		ToolCalls:     []string{},
	}
	readRequestMetadata(requestBody, &record)
	if requestReadErr != nil {
		record.Error = "read request body: " + requestReadErr.Error()
	}

	resp, roundTripErr := t.base.RoundTrip(req)
	if roundTripErr != nil {
		record.Error = roundTripErr.Error()
	}
	var responseBody []byte
	if resp != nil {
		record.Status = resp.StatusCode
		var responseReadErr error
		responseBody, responseReadErr = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(responseBody))
		if responseReadErr != nil {
			record.Error = "read response body: " + responseReadErr.Error()
		}
	}
	record.ResponseBytes = len(responseBody)
	readResponseMetadata(responseBody, &record)
	record.Request = decodeAndRedactTraceJSON(requestBody)
	record.Response = decodeAndRedactTraceJSON(responseBody)
	record.DurationMS = time.Since(started).Milliseconds()
	t.append(record)
	return resp, roundTripErr
}

func readAndRestoreRequestBody(req *http.Request) ([]byte, error) {
	if req == nil || req.Body == nil {
		return nil, nil
	}
	raw, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(raw))
	return raw, err
}

func readRequestMetadata(raw []byte, record *modelTraceRecord) {
	var payload struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if json.Unmarshal(raw, &payload) == nil {
		record.Model = payload.Model
		record.Stream = payload.Stream
	}
}

func readResponseMetadata(raw []byte, record *modelTraceRecord) {
	var payload struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				ToolCalls []struct {
					Function struct {
						Name string `json:"name"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return
	}
	for _, choice := range payload.Choices {
		if choice.FinishReason != "" {
			record.FinishReasons = append(record.FinishReasons, choice.FinishReason)
		}
		for _, call := range choice.Message.ToolCalls {
			if call.Function.Name != "" {
				record.ToolCalls = append(record.ToolCalls, call.Function.Name)
			}
		}
	}
	record.Usage = modelTraceUsage{
		PromptTokens:     payload.Usage.PromptTokens,
		CompletionTokens: payload.Usage.CompletionTokens,
		TotalTokens:      payload.Usage.TotalTokens,
	}
}

func decodeAndRedactTraceJSON(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return string(raw)
	}
	return redactTraceValue(value)
}

func redactTraceValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if isSecretTraceKey(key) {
				typed[key] = "[REDACTED]"
				continue
			}
			typed[key] = redactTraceValue(item)
		}
	case []any:
		for i := range typed {
			typed[i] = redactTraceValue(typed[i])
		}
	}
	return value
}

func isSecretTraceKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), " ", "_"))
	for _, marker := range []string{"authorization", "api_key", "apikey", "access_token", "refresh_token", "password", "secret"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func sanitizeTraceURL(input *url.URL) string {
	if input == nil {
		return ""
	}
	copyURL := *input
	query := copyURL.Query()
	for key := range query {
		if isSecretTraceKey(key) {
			query.Set(key, "[REDACTED]")
		}
	}
	copyURL.RawQuery = query.Encode()
	return copyURL.String()
}

func (t *modelTraceTransport) append(record modelTraceRecord) {
	raw, err := json.Marshal(record)
	if err != nil {
		log.Printf("[drama-skill-trace] encode %s: %v", t.path, err)
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	file, err := os.OpenFile(t.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		log.Printf("[drama-skill-trace] open %s: %v", t.path, err)
		return
	}
	defer file.Close()
	if _, err := file.Write(append(raw, '\n')); err != nil {
		log.Printf("[drama-skill-trace] write %s: %v", t.path, err)
	}
}
