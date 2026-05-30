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
	ID                            string
	ContextWindowTokens           int
	EffectiveContextWindowPercent int
	AutoCompactTokenLimit         int
}

// ResolveModelMetadata returns provider-advertised model metadata when
// available, then falls back to Affent's local model registry for known models.
// The source string is stable telemetry for callers and tests.
func (c *LLMClient) ResolveModelMetadata(ctx context.Context) (ModelMetadata, string, error) {
	meta, err := c.FetchModelMetadata(ctx)
	if err == nil && meta.ContextWindowTokens > 0 {
		return meta, "provider", nil
	}
	if fallback, ok := KnownModelMetadata(c.Model); ok {
		return fallback, "registry", nil
	}
	if err != nil {
		return ModelMetadata{}, "", err
	}
	return meta, "", nil
}

// KnownModelMetadata contains stable context-window facts for models whose
// OpenAI-compatible providers commonly omit metadata from /models.
func KnownModelMetadata(model string) (ModelMetadata, bool) {
	id := normalizeModelMetadataID(model)
	switch id {
	case "qwen3.6-35b-a3b":
		return ModelMetadata{
			ID:                  "qwen3.6-35b-a3b",
			ContextWindowTokens: 262144,
		}, true
	default:
		return ModelMetadata{}, false
	}
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
		if model != "" && !modelMetadataIDsMatch(id, model) {
			continue
		}
		window := modelContextWindowTokensFromMetadata(item)
		effectivePercent := modelEffectiveContextWindowPercentFromMetadata(item)
		return ModelMetadata{
			ID:                            id,
			ContextWindowTokens:           applyEffectiveContextWindowPercent(window, effectivePercent),
			EffectiveContextWindowPercent: effectivePercent,
			AutoCompactTokenLimit:         modelAutoCompactTokenLimitFromMetadata(item),
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
	if providerWindow := nestedPositiveModelMetadataInt(item, "top_provider",
		"context_length",
		"context_window",
		"max_context_window",
		"max_model_len",
	); providerWindow > 0 && (maxWindow <= 0 || providerWindow < maxWindow) {
		maxWindow = providerWindow
	}
	if window <= 0 {
		return maxWindow
	}
	if maxWindow > 0 && maxWindow < window {
		return maxWindow
	}
	return window
}

func modelAutoCompactTokenLimitFromMetadata(item map[string]any) int {
	return firstPositiveModelMetadataInt(item,
		"auto_compact_token_limit",
		"model_auto_compact_token_limit",
		"auto_compact_context_limit",
	)
}

func modelEffectiveContextWindowPercentFromMetadata(item map[string]any) int {
	percent := firstPositiveModelMetadataInt(item,
		"effective_context_window_percent",
		"model_effective_context_window_percent",
	)
	if percent > 100 {
		return 100
	}
	return percent
}

func applyEffectiveContextWindowPercent(tokens, percent int) int {
	if tokens <= 0 {
		return 0
	}
	if percent <= 0 {
		return tokens
	}
	if percent > 100 {
		percent = 100
	}
	return max(1, tokens*percent/100)
}

func firstPositiveModelMetadataInt(item map[string]any, keys ...string) int {
	for _, key := range keys {
		if n := modelMetadataInt(item, key); n > 0 {
			return n
		}
	}
	for _, nestedKey := range []string{"metadata", "limits", "capabilities", "top_provider"} {
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

func modelMetadataIDsMatch(metadataID, requested string) bool {
	metadataID = strings.TrimSpace(metadataID)
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return true
	}
	if metadataID == requested {
		return true
	}
	return normalizeModelMetadataID(metadataID) == normalizeModelMetadataID(requested)
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

func nestedPositiveModelMetadataInt(item map[string]any, nestedKey string, keys ...string) int {
	nested, ok := item[nestedKey].(map[string]any)
	if !ok {
		return 0
	}
	for _, key := range keys {
		if n := modelMetadataInt(nested, key); n > 0 {
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

func normalizeModelMetadataID(model string) string {
	model = strings.TrimSpace(strings.ToLower(model))
	model = strings.TrimPrefix(model, "qwen/")
	return model
}
