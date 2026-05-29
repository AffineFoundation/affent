package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const maxModelMetadataBytes = 2 * 1024 * 1024

type ModelMetadata struct {
	ID                  string
	ContextWindowTokens int
}

// FetchModelMetadata reads OpenAI-compatible /models metadata for c.Model.
// Providers vary widely: many return only id/object, while others expose
// context_window, max_context_window, context_length, or max_model_len. Missing
// metadata is not an error for callers that can operate with an unknown window.
func (c *LLMClient) FetchModelMetadata(ctx context.Context) (ModelMetadata, error) {
	if c == nil {
		return ModelMetadata{}, fmt.Errorf("llm client is nil")
	}
	baseURL := strings.TrimRight(c.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		return ModelMetadata{}, err
	}
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return ModelMetadata{}, fmt.Errorf("models request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxLLMErrorBodyBytes))
		return ModelMetadata{}, newLLMHTTPError(resp.StatusCode, errBody)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxModelMetadataBytes+1))
	if err != nil {
		return ModelMetadata{}, fmt.Errorf("read models response: %w", err)
	}
	if len(raw) > maxModelMetadataBytes {
		return ModelMetadata{}, fmt.Errorf("models response exceeds %d-byte cap", maxModelMetadataBytes)
	}
	return ParseModelMetadata(raw, c.Model)
}

func ParseModelMetadata(raw []byte, model string) (ModelMetadata, error) {
	var envelope struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return ModelMetadata{}, fmt.Errorf("parse models response: %w", err)
	}
	model = strings.TrimSpace(model)
	if len(envelope.Data) == 0 {
		return ModelMetadata{}, fmt.Errorf("models response contained no models")
	}
	for _, item := range envelope.Data {
		id := modelMetadataString(item, "id")
		if model != "" && id != model {
			continue
		}
		return ModelMetadata{
			ID:                  id,
			ContextWindowTokens: modelContextWindowTokensFromMetadata(item),
		}, nil
	}
	return ModelMetadata{}, fmt.Errorf("model %q not found in models response", model)
}

func modelContextWindowTokensFromMetadata(item map[string]any) int {
	window := firstPositiveModelMetadataInt(item,
		"context_window",
		"model_context_window",
		"context_length",
		"max_context_length",
	)
	maxWindow := firstPositiveModelMetadataInt(item,
		"max_context_window",
		"max_model_len",
		"max_model_length",
	)
	if window <= 0 {
		return maxWindow
	}
	if maxWindow > 0 && maxWindow < window {
		return maxWindow
	}
	return window
}

func firstPositiveModelMetadataInt(item map[string]any, keys ...string) int {
	for _, key := range keys {
		if n := modelMetadataInt(item, key); n > 0 {
			return n
		}
	}
	for _, nestedKey := range []string{"metadata", "limits", "capabilities"} {
		nested, ok := item[nestedKey].(map[string]any)
		if !ok {
			continue
		}
		for _, key := range keys {
			if n := modelMetadataInt(nested, key); n > 0 {
				return n
			}
		}
	}
	return 0
}

func modelMetadataInt(item map[string]any, key string) int {
	value, ok := item[key]
	if !ok {
		return 0
	}
	switch v := value.(type) {
	case float64:
		if v > 0 {
			return int(v)
		}
	case string:
		n, ok := parsePositiveIntString(v)
		if ok {
			return n
		}
	}
	return 0
}

func modelMetadataString(item map[string]any, key string) string {
	value, ok := item[key]
	if !ok {
		return ""
	}
	s, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func parsePositiveIntString(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}
