package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	agent "github.com/affinefoundation/affent/internal/agent"
)

const (
	defaultSessionListLimit   = 100
	maxSessionListLimit       = 1000
	sessionReadDirBatch       = 128
	maxSessionCreateBodyBytes = 4096
)

type sessionListResponse struct {
	Sessions  []sessionSummary `json:"sessions"`
	NextAfter string           `json:"next_after,omitempty"`
	HasMore   bool             `json:"has_more"`
}

type sessionDetailResponse struct {
	Session sessionSummary `json:"session"`
}

type sessionCreateRequest struct {
	SessionID string `json:"session_id,omitempty"`
}

type sessionCreateResponse struct {
	Session sessionSummary `json:"session"`
}

type sessionSummary struct {
	ID               string                `json:"id"`
	Active           bool                  `json:"active"`
	Durable          bool                  `json:"durable"`
	CreatedAt        string                `json:"created_at,omitempty"`
	LastUsedAt       string                `json:"last_used_at,omitempty"`
	HasConversation  bool                  `json:"has_conversation"`
	HasEvents        bool                  `json:"has_events"`
	HasArtifacts     bool                  `json:"has_artifacts"`
	HasMemory        bool                  `json:"has_memory"`
	HasRuntimeSkills bool                  `json:"has_runtime_skills"`
	Usage            *UsageSnapshot        `json:"usage,omitempty"`
	Browser          *BrowserStatsSnapshot `json:"browser,omitempty"`
}

type sessionListOptions struct {
	After string
	Limit int
}

func handleSessionsCollection(pool *SessionPool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleSessionList(pool, w, r)
		case http.MethodPost:
			handleSessionCreate(pool, w, r)
		default:
			writeJSONErrorTyped(w, http.StatusMethodNotAllowed, "method not allowed", nil, "bad_request")
		}
	}
}

func handleSessionList(pool *SessionPool, w http.ResponseWriter, r *http.Request) {
	if pool == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "session pool unavailable", nil)
		return
	}
	opts, err := parseSessionListQuery(r)
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid sessions query", err, "bad_request")
		return
	}
	summaries, hasMore, err := listSessionSummaries(pool, opts)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "list sessions", err)
		return
	}
	resp := sessionListResponse{Sessions: summaries, HasMore: hasMore}
	if len(summaries) > 0 && hasMore {
		resp.NextAfter = summaries[len(summaries)-1].ID
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func handleSessionCreate(pool *SessionPool, w http.ResponseWriter, r *http.Request) {
	if pool == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "session pool unavailable", nil)
		return
	}
	req, err := decodeSessionCreateRequest(w, r)
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid create session request", err, "bad_request")
		return
	}
	if req.SessionID != "" {
		if err := agent.ValidateSessionID(req.SessionID); err != nil {
			writeJSONErrorTyped(w, http.StatusBadRequest, "invalid session id", err, "bad_request")
			return
		}
	}
	existed := req.SessionID != "" && sessionKnown(pool, req.SessionID)
	sess, err := pool.GetOrCreate(req.SessionID)
	if err != nil {
		if errors.Is(err, ErrShuttingDown) {
			writeJSONError(w, http.StatusServiceUnavailable, "server shutting down", err)
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "create session", err)
		return
	}
	summary, found, err := summarizeSession(pool, sess.ID, sess)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "read session", err)
		return
	}
	if !found {
		writeJSONError(w, http.StatusInternalServerError, "created session not found", nil)
		return
	}
	status := http.StatusCreated
	if existed {
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(sessionCreateResponse{Session: summary})
}

func handleSessionDetail(pool *SessionPool, sessionID string, w http.ResponseWriter, _ *http.Request) {
	if pool == nil {
		writeJSONError(w, http.StatusNotFound, "session not found", nil)
		return
	}
	if err := agent.ValidateSessionID(sessionID); err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid session id", err, "bad_request")
		return
	}
	active := activeSessionByID(pool, sessionID)
	summary, found, err := summarizeSession(pool, sessionID, active)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "read session", err)
		return
	}
	if !found {
		writeJSONError(w, http.StatusNotFound, "session not found", nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sessionDetailResponse{Session: summary})
}

func parseSessionListQuery(r *http.Request) (sessionListOptions, error) {
	opts := sessionListOptions{Limit: defaultSessionListLimit}
	q := r.URL.Query()
	if raw := q.Get("after"); raw != "" {
		if err := agent.ValidateSessionID(raw); err != nil {
			return opts, err
		}
		opts.After = raw
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return opts, err
		}
		if n <= 0 {
			return opts, errors.New("limit must be positive")
		}
		if n > maxSessionListLimit {
			n = maxSessionListLimit
		}
		opts.Limit = n
	}
	return opts, nil
}

func decodeSessionCreateRequest(w http.ResponseWriter, r *http.Request) (sessionCreateRequest, error) {
	var req sessionCreateRequest
	if r.Body == nil || r.Body == http.NoBody {
		return req, nil
	}
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSessionCreateBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		if errors.Is(err, io.EOF) {
			return req, nil
		}
		return req, err
	}
	var extra struct{}
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return req, errors.New("request body must contain a single JSON object")
	}
	return req, nil
}

func listSessionSummaries(pool *SessionPool, opts sessionListOptions) ([]sessionSummary, bool, error) {
	candidates := map[string]sessionSummary{}

	pool.mu.Lock()
	active := make([]*Session, 0, len(pool.sessions))
	for _, s := range pool.sessions {
		active = append(active, s)
	}
	pool.mu.Unlock()

	for _, s := range active {
		if s.ID <= opts.After {
			continue
		}
		summary, found, err := summarizeSession(pool, s.ID, s)
		if err != nil {
			return nil, false, err
		}
		if found {
			addSessionCandidate(candidates, summary, opts.Limit+1)
		}
	}

	root := pool.sessionRootPath()
	dir, err := os.Open(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return sortedSessionCandidates(candidates, opts.Limit), len(candidates) > opts.Limit, nil
		}
		return nil, false, err
	}
	defer dir.Close()

	for {
		entries, err := dir.ReadDir(sessionReadDirBatch)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, false, err
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			id := entry.Name()
			if id <= opts.After || agent.ValidateSessionID(id) != nil {
				continue
			}
			if _, ok := candidates[id]; ok {
				continue
			}
			summary, found, err := summarizeSession(pool, id, nil)
			if err != nil {
				continue
			}
			if found {
				addSessionCandidate(candidates, summary, opts.Limit+1)
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}

	return sortedSessionCandidates(candidates, opts.Limit), len(candidates) > opts.Limit, nil
}

func addSessionCandidate(candidates map[string]sessionSummary, summary sessionSummary, cap int) {
	if cap <= 0 {
		return
	}
	if existing, ok := candidates[summary.ID]; ok {
		candidates[summary.ID] = mergeSessionSummaries(existing, summary)
	} else {
		candidates[summary.ID] = summary
	}
	for len(candidates) > cap {
		var highest string
		for id := range candidates {
			if highest == "" || id > highest {
				highest = id
			}
		}
		delete(candidates, highest)
	}
}

func sortedSessionCandidates(candidates map[string]sessionSummary, limit int) []sessionSummary {
	ids := make([]string, 0, len(candidates))
	for id := range candidates {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) > limit {
		ids = ids[:limit]
	}
	out := make([]sessionSummary, 0, len(ids))
	for _, id := range ids {
		out = append(out, candidates[id])
	}
	return out
}

func sessionKnown(pool *SessionPool, id string) bool {
	if activeSessionByID(pool, id) != nil {
		return true
	}
	info, err := os.Stat(pool.sessionDirPath(id))
	return err == nil && info.IsDir()
}

func activeSessionByID(pool *SessionPool, id string) *Session {
	pool.mu.Lock()
	s := pool.sessions[id]
	pool.mu.Unlock()
	return s
}

func summarizeSession(pool *SessionPool, id string, active *Session) (sessionSummary, bool, error) {
	summary := sessionSummary{ID: id}
	if active != nil {
		summary = mergeSessionSummaries(summary, summarizeActiveSession(active))
	}
	durable, found, err := summarizeDurableSession(pool, id)
	if err != nil {
		return sessionSummary{}, false, err
	}
	if found {
		summary = mergeSessionSummaries(summary, durable)
	}
	return summary, active != nil || found, nil
}

func summarizeActiveSession(s *Session) sessionSummary {
	s.mu.Lock()
	createdAt, lastUsedAt := s.createdAt, s.lastUsed
	s.mu.Unlock()
	usage := s.UsageSnapshot()
	browser := s.BrowserStatsSnapshot()
	return sessionSummary{
		ID:         s.ID,
		Active:     true,
		CreatedAt:  formatTime(createdAt),
		LastUsedAt: formatTime(lastUsedAt),
		Usage:      &usage,
		Browser:    &browser,
	}
}

func summarizeDurableSession(pool *SessionPool, id string) (sessionSummary, bool, error) {
	dir := pool.sessionDirPath(id)
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return sessionSummary{}, false, nil
		}
		return sessionSummary{}, false, err
	}
	if !info.IsDir() {
		return sessionSummary{}, false, nil
	}
	summary := sessionSummary{
		ID:         id,
		Durable:    true,
		LastUsedAt: formatTime(info.ModTime()),
	}
	newest := info.ModTime()
	mergeStat := func(path string) (bool, error) {
		fi, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return false, nil
			}
			return false, err
		}
		if fi.ModTime().After(newest) {
			newest = fi.ModTime()
		}
		return true, nil
	}
	var exists bool
	if exists, err = mergeStat(filepath.Join(dir, "conversation.jsonl")); err != nil {
		return sessionSummary{}, false, err
	}
	summary.HasConversation = exists
	if exists, err = mergeStat(filepath.Join(dir, "events.jsonl")); err != nil {
		return sessionSummary{}, false, err
	}
	summary.HasEvents = exists
	summary.HasArtifacts = dirHasAnyEntry(filepath.Join(dir, filepath.FromSlash(artifactPathPrefix)))
	summary.HasRuntimeSkills = dirHasAnyEntry(agent.DefaultWorkspaceSkillDir(dir))
	summary.HasMemory = durableMemoryExists(dir)
	if summary.HasArtifacts {
		_, _ = mergeStat(filepath.Join(dir, filepath.FromSlash(artifactPathPrefix)))
	}
	if summary.HasRuntimeSkills {
		_, _ = mergeStat(agent.DefaultWorkspaceSkillDir(dir))
	}
	if summary.HasMemory {
		for _, p := range []string{
			filepath.Join(dir, "USER.md"),
			filepath.Join(dir, "core.md"),
			filepath.Join(dir, "topics"),
		} {
			_, _ = mergeStat(p)
		}
	}
	summary.LastUsedAt = formatTime(newest)
	return summary, true, nil
}

func mergeSessionSummaries(a, b sessionSummary) sessionSummary {
	if a.ID == "" {
		a.ID = b.ID
	}
	a.Active = a.Active || b.Active
	a.Durable = a.Durable || b.Durable
	a.HasConversation = a.HasConversation || b.HasConversation
	a.HasEvents = a.HasEvents || b.HasEvents
	a.HasArtifacts = a.HasArtifacts || b.HasArtifacts
	a.HasMemory = a.HasMemory || b.HasMemory
	a.HasRuntimeSkills = a.HasRuntimeSkills || b.HasRuntimeSkills
	if a.CreatedAt == "" {
		a.CreatedAt = b.CreatedAt
	}
	a.LastUsedAt = newerFormattedTime(a.LastUsedAt, b.LastUsedAt)
	if b.Usage != nil {
		a.Usage = b.Usage
	}
	if b.Browser != nil {
		a.Browser = b.Browser
	}
	return a
}

func durableMemoryExists(sessionDir string) bool {
	for _, path := range []string{
		filepath.Join(sessionDir, "USER.md"),
		filepath.Join(sessionDir, "core.md"),
		filepath.Join(sessionDir, "MEMORY.md"),
		filepath.Join(sessionDir, "topics"),
	} {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

func dirHasAnyEntry(dir string) bool {
	f, err := os.Open(dir)
	if err != nil {
		return false
	}
	defer f.Close()
	entries, err := f.ReadDir(1)
	return err == nil && len(entries) > 0
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func newerFormattedTime(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	at, errA := time.Parse(time.RFC3339, a)
	bt, errB := time.Parse(time.RFC3339, b)
	if errA == nil && errB == nil && bt.After(at) {
		return b
	}
	return a
}
