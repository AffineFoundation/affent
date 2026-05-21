package main

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

func readAll(r io.Reader) (string, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return "", err
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
		const prefix = "Bearer "
		if !strings.HasPrefix(got, prefix) {
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
