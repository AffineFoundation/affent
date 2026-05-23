package main

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const maxSystemPromptBytes = 256 * 1024

func readSystemPrompt(r io.Reader) (string, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxSystemPromptBytes+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxSystemPromptBytes {
		return "", fmt.Errorf("system prompt exceeds %d-byte limit", maxSystemPromptBytes)
	}
	return string(b), nil
}

// writeJSONError emits an OpenAI-shaped error object so SDKs can parse
// it uniformly. The optional err is appended to msg for context;
// errType lets callers tag the failure class (defaults to
// "affentserve_error"). All non-success responses on this server go
// through this helper.
func writeJSONError(w http.ResponseWriter, code int, msg string, err error) {
	writeJSONErrorTyped(w, code, msg, err, "affentserve_error")
}

func writeJSONErrorTyped(w http.ResponseWriter, code int, msg string, err error, errType string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	full := msg
	if err != nil {
		full = msg + ": " + strings.TrimSpace(err.Error())
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": full,
			"type":    errType,
		},
	})
}

// requireAuth gates handlers behind the optional --auth-token. Calling
// with an empty token is a no-op (token-less deployments). Token
// comparison is constant-time via crypto/subtle so a remote attacker
// can't infer the token byte-by-byte from response-time deltas.
func requireAuth(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	tokenBytes := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		// RFC 7235 §2.1: auth scheme names are case-insensitive.
		// "Bearer", "bearer", "BEARER" all need to match — spec-strict
		// clients (and a few SDKs that lowercase auth schemes for
		// canonicalization) would otherwise hit 401 with a valid token.
		const prefix = "bearer "
		if len(got) < len(prefix) || !strings.EqualFold(got[:len(prefix)], prefix) {
			writeJSONErrorTyped(w, http.StatusUnauthorized, "unauthorized", nil, "auth_error")
			return
		}
		provided := []byte(got[len(prefix):])
		// subtle.ConstantTimeCompare returns 1 only when both slices
		// have equal length AND equal contents. It still leaks length
		// via the early-return on len mismatch, but that's accepted
		// practice: tokens are fixed-length per deployment and the
		// length isn't the secret.
		if subtle.ConstantTimeCompare(provided, tokenBytes) != 1 {
			writeJSONErrorTyped(w, http.StatusUnauthorized, "unauthorized", nil, "auth_error")
			return
		}
		next.ServeHTTP(w, r)
	})
}
