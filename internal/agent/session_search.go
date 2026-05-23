package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/affinefoundation/affent/internal/sessionsearch"
)

// SessionSearchHit is one matched message from a past session.
type SessionSearchHit = sessionsearch.Hit

// SessionSearchResponse is the tool's return shape.
type SessionSearchResponse = sessionsearch.Response

// sessionSearchTool searches past workspace transcripts. The current
// session is excluded so the agent does not match its own in-flight
// turns.
func sessionSearchTool(sessionsDir, currentSessionID string) *Tool {
	schema, err := json.Marshal(map[string]any{
		"type":     "object",
		"required": []string{"query"},
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"minLength":   1,
				"maxLength":   sessionsearch.MaxQueryBytes,
				"description": "Keywords or phrase.",
			},
			"top_k": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     sessionsearch.MaxTopK,
				"description": "Result count; default 5, max 20.",
			},
			"max_per_session": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     sessionsearch.MaxPerSession,
				"description": "Hits per session; default 3, max 5.",
			},
		},
	})
	if err != nil {
		panic(fmt.Sprintf("session_search schema: %v", err))
	}
	return &Tool{
		Name:        "session_search",
		Description: "Search past session transcripts in this workspace. Returns snippets with session id and turn index. Use for transcript recall; use memory for durable facts.",
		Schema:      json.RawMessage(schema),
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			var p struct {
				Query         string `json:"query"`
				TopK          int    `json:"top_k"`
				MaxPerSession int    `json:"max_per_session"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return "", fmt.Errorf("decode args: %w", err)
			}
			p.Query = sessionsearch.NormalizeQuery(p.Query)
			if p.Query == "" {
				return marshalSessionSearchResp(SessionSearchResponse{Message: "query is required"}), nil
			}
			p.TopK, p.MaxPerSession = sessionsearch.NormalizeLimits(p.TopK, p.MaxPerSession)
			if sessionsDir == "" {
				return marshalSessionSearchResp(SessionSearchResponse{Query: p.Query, Message: "session_search is not configured (no sessions directory)"}), nil
			}
			hits, err := sessionsearch.Search(ctx, sessionsDir, currentSessionID, p.Query, p.TopK, p.MaxPerSession)
			if err != nil {
				return "", err
			}
			message := ""
			if len(hits) == 0 {
				message = "no results. Next: retry with fewer or different keywords, include outcome words like passed/final/decision, or search for a related session id."
			}
			return marshalSessionSearchResp(SessionSearchResponse{
				Query:   p.Query,
				Total:   len(hits),
				Results: hits,
				Message: message,
			}), nil
		},
	}
}

func marshalSessionSearchResp(r SessionSearchResponse) string {
	if r.Results == nil {
		r.Results = []SessionSearchHit{}
	}
	out, err := json.Marshal(r)
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	return string(out)
}
