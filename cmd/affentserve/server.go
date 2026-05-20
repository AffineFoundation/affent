package main

import (
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// newRouter wires every endpoint affentserve exposes:
//
//	GET    /healthz
//	GET    /v1/models
//	POST   /v1/chat/completions
//	GET    /v1/sessions/{id}/events
//	DELETE /v1/sessions/{id}
//
// Authentication, when --auth-token is set, wraps every endpoint
// except /healthz (which must stay reachable for load balancers).
func newRouter(cfg Config, pool *SessionPool, logger zerolog.Logger) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", handleHealth)

	authed := func(h http.Handler) http.Handler {
		return requireAuth(cfg.AuthToken, logRequests(logger, h))
	}

	mux.Handle("/v1/models", authed(http.HandlerFunc(handleModels(cfg))))
	mux.Handle("/v1/chat/completions", authed(http.HandlerFunc(handleChatCompletions(cfg, pool))))
	mux.Handle("/v1/sessions/", authed(http.HandlerFunc(handleSessionRoutes(pool))))
	mux.Handle("/v1/stats", authed(http.HandlerFunc(handleStats(cfg, pool))))

	return mux
}

// handleSessionRoutes demuxes the per-session sub-paths:
//
//	GET    /v1/sessions/{id}/events  → SSE passthrough
//	DELETE /v1/sessions/{id}         → close + remove
func handleSessionRoutes(pool *SessionPool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Path shape: /v1/sessions/{id}[/events]
		tail := strings.TrimPrefix(r.URL.Path, "/v1/sessions/")
		if tail == r.URL.Path || tail == "" {
			writeJSONErrorTyped(w, http.StatusBadRequest, "missing session id", nil, "bad_request")
			return
		}
		parts := strings.SplitN(tail, "/", 2)
		sessionID := parts[0]
		var sub string
		if len(parts) == 2 {
			sub = parts[1]
		}

		switch {
		case sub == "events" && r.Method == http.MethodGet:
			handleSessionEvents(pool, sessionID, w, r)
		case sub == "" && r.Method == http.MethodDelete:
			handleSessionDelete(pool, sessionID, w, r)
		default:
			writeJSONErrorTyped(w, http.StatusNotFound, "not found", nil, "not_found")
		}
	}
}

// logRequests is a minimal access log so operators can see what
// reached the server without standing up an external proxy.
func logRequests(logger zerolog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &responseRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(ww, r)
		logger.Info().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Int("status", ww.status).
			Dur("duration", time.Since(start)).
			Msg("request")
	})
}

type responseRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (rr *responseRecorder) WriteHeader(code int) {
	if rr.wroteHeader {
		return
	}
	rr.status = code
	rr.wroteHeader = true
	rr.ResponseWriter.WriteHeader(code)
}

// Flush forwards to the wrapped writer so SSE handlers keep working.
func (rr *responseRecorder) Flush() {
	if f, ok := rr.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
