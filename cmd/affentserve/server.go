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
//	GET    /                       (embedded WebUI when built with -tags webui)
//	GET    /v1/models
//	POST   /v1/chat/completions
//	GET    /v1/sessions
//	POST   /v1/sessions
//	GET    /v1/skills
//	GET    /v1/skills/{name}
//	POST   /v1/skills
//	GET    /v1/sessions/{id}
//	GET    /v1/sessions/{id}/events
//	GET    /v1/sessions/{id}/history
//	GET    /v1/sessions/{id}/plan
//	DELETE /v1/sessions/{id}/plan
//	GET    /v1/sessions/{id}/tools
//	GET    /v1/sessions/{id}/transcripts
//	GET    /v1/sessions/{id}/transcripts/{path...}
//	GET    /v1/sessions/{id}/artifacts
//	GET    /v1/sessions/{id}/artifacts/{path...}
//	POST   /v1/sessions/{id}/messages
//	POST   /v1/sessions/{id}/cancel
//	DELETE /v1/sessions/{id}
//
// Authentication, when --auth-token is set, wraps every endpoint
// except /healthz (which must stay reachable for load balancers).
func newRouter(cfg Config, pool *SessionPool, logger zerolog.Logger) http.Handler {
	mux := http.NewServeMux()

	mux.Handle("/healthz", http.HandlerFunc(handleHealth(pool)))

	authed := func(h http.Handler) http.Handler {
		return requireAuth(cfg.AuthToken, logRequests(logger, h))
	}

	mux.Handle("/v1/models", authed(http.HandlerFunc(handleModels(cfg))))
	mux.Handle("/v1/chat/completions", authed(http.HandlerFunc(handleChatCompletions(cfg, pool))))
	mux.Handle("/v1/skills", authed(http.HandlerFunc(handleAccountSkills(pool))))
	mux.Handle("/v1/skills/", authed(http.HandlerFunc(handleAccountSkillRoutes(pool))))
	mux.Handle("/v1/sessions", authed(http.HandlerFunc(handleSessionsCollection(pool))))
	mux.Handle("/v1/sessions/", authed(http.HandlerFunc(handleSessionRoutes(pool))))
	mux.Handle("/v1/stats", authed(http.HandlerFunc(handleStats(cfg, pool))))

	if web := webUIHandler(); web != nil {
		mux.Handle("/", logRequests(logger, web))
	}

	return mux
}

// handleSessionRoutes demuxes the per-session sub-paths:
//
//	GET    /v1/sessions/{id}         → durable/active session detail
//	GET    /v1/sessions/{id}/events  → SSE passthrough
//	GET    /v1/sessions/{id}/history → persisted JSONL replay
//	GET    /v1/sessions/{id}/plan    → persisted plan snapshot
//	DELETE /v1/sessions/{id}/plan    → remove persisted plan snapshot
//	GET    /v1/sessions/{id}/tools   → active session tool catalog
//	GET    /v1/sessions/{id}/transcripts[/path] → child loop transcripts
//	GET    /v1/sessions/{id}/artifacts[/path] → tool-result artifacts
//	POST   /v1/sessions/{id}/messages → start async user turn
//	POST   /v1/sessions/{id}/cancel  → cancel active turn
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
		case sub == "" && r.Method == http.MethodGet:
			handleSessionDetail(pool, sessionID, w, r)
		case sub == "events" && r.Method == http.MethodGet:
			handleSessionEvents(pool, sessionID, w, r)
		case sub == "history" && r.Method == http.MethodGet:
			handleSessionHistory(pool, sessionID, w, r)
		case sub == "plan" && r.Method == http.MethodGet:
			handleSessionPlan(pool, sessionID, w, r)
		case sub == "plan" && r.Method == http.MethodDelete:
			handleSessionPlanDelete(pool, sessionID, w, r)
		case sub == "tools" && r.Method == http.MethodGet:
			handleSessionTools(pool, sessionID, w, r)
		case (sub == "transcripts" || strings.HasPrefix(sub, "transcripts/")) && r.Method == http.MethodGet:
			handleSessionTranscripts(pool, sessionID, strings.TrimPrefix(sub, "transcripts"), w, r)
		case (sub == "artifacts" || strings.HasPrefix(sub, "artifacts/")) && r.Method == http.MethodGet:
			handleSessionArtifacts(pool, sessionID, strings.TrimPrefix(sub, "artifacts"), w, r)
		case sub == "messages" && r.Method == http.MethodPost:
			handleSessionMessage(pool, sessionID, w, r)
		case sub == "cancel" && r.Method == http.MethodPost:
			handleSessionCancel(pool, sessionID, w, r)
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
