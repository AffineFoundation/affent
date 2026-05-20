package main

import (
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
// with an empty token is a no-op (token-less deployments).
func requireAuth(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(got, prefix) || got[len(prefix):] != token {
			writeJSONErrorTyped(w, http.StatusUnauthorized, "unauthorized", nil, "auth_error")
			return
		}
		next.ServeHTTP(w, r)
	})
}
