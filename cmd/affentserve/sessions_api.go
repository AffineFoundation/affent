package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	agent "github.com/affinefoundation/affent/internal/agent"
)

const (
	defaultSessionListLimit    = 100
	maxSessionListLimit        = 1000
	sessionReadDirBatch        = 128
	maxSessionCreateBodyBytes  = 4096
	maxSessionTaskSummaryChars = 160
	maxSessionSummaryLineBytes = 1024 * 1024
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
	ID                string                `json:"id"`
	Active            bool                  `json:"active"`
	Durable           bool                  `json:"durable"`
	CreatedAt         string                `json:"created_at,omitempty"`
	LastUsedAt        string                `json:"last_used_at,omitempty"`
	Capabilities      *sessionCapabilities  `json:"capabilities,omitempty"`
	HasConversation   bool                  `json:"has_conversation"`
	LatestUserMessage string                `json:"latest_user_message,omitempty"`
	HasEvents         bool                  `json:"has_events"`
	HasPlan           bool                  `json:"has_plan"`
	PlanSummary       *sessionPlanSummary   `json:"plan_summary,omitempty"`
	HasArtifacts      bool                  `json:"has_artifacts"`
	HasMemory         bool                  `json:"has_memory"`
	HasRuntimeSkills  bool                  `json:"has_runtime_skills"`
	Usage             *UsageSnapshot        `json:"usage,omitempty"`
	Browser           *BrowserStatsSnapshot `json:"browser,omitempty"`
}

type sessionCapabilities struct {
	Builtins          bool `json:"builtins"`
	SkillInstall      bool `json:"skill_install"`
	Plan              bool `json:"plan"`
	Memory            bool `json:"memory"`
	SessionSearch     bool `json:"session_search"`
	Browser           bool `json:"browser"`
	BrowserScreenshot bool `json:"browser_screenshot"`
	Web               bool `json:"web"`
	WebSearch         bool `json:"web_search"`
	Subagent          bool `json:"subagent"`
	SubagentMaxDepth  int  `json:"subagent_max_depth"`
	FocusedTasks      bool `json:"focused_tasks"`
	// FocusedTaskProfiles enumerates the run_task task_type values the
	// model can actually request under this session's wiring. Omitted
	// when focused tasks are disabled or no profile's deps are
	// satisfied; lets clients build a typed task_type picker without
	// having to parse the run_task tool's JSON schema themselves.
	FocusedTaskProfiles []string `json:"focused_task_profiles,omitempty"`
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
	_, found, err := durableSessionDirInfo(pool.sessionDirPath(id))
	return err == nil && found
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
		summary = mergeSessionSummaries(summary, summarizeActiveSession(active, pool.cfg))
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

func summarizeActiveSession(s *Session, cfg Config) sessionSummary {
	s.mu.Lock()
	createdAt, lastUsedAt := s.createdAt, s.lastUsed
	s.mu.Unlock()
	usage := s.UsageSnapshot()
	browser := s.BrowserStatsSnapshot()
	caps := summarizeActiveCapabilities(s, cfg)
	return sessionSummary{
		ID:                s.ID,
		Active:            true,
		CreatedAt:         formatTime(createdAt),
		LastUsedAt:        formatTime(lastUsedAt),
		LatestUserMessage: latestUserMessageFromMessages(s.conv.Snapshot()),
		Capabilities:      &caps,
		Usage:             &usage,
		Browser:           &browser,
	}
}

func summarizeActiveCapabilities(s *Session, cfg Config) sessionCapabilities {
	hasTool := func(name string) bool {
		_, ok := s.registry.Get(name)
		return ok
	}
	focusedRegistered := hasTool(agent.FocusedTaskToolName)
	caps := sessionCapabilities{
		Builtins: hasTool("shell") &&
			hasTool("read_file") &&
			hasTool("write_file") &&
			hasTool("edit_file") &&
			hasTool("list_files"),
		SkillInstall:      hasTool("skill"),
		Plan:              hasTool(agent.PlanToolName),
		Memory:            hasTool("memory"),
		SessionSearch:     hasTool("session_search"),
		Browser:           hasTool("browser_navigate") || hasTool("browser_snapshot"),
		BrowserScreenshot: hasTool("browser_screenshot"),
		Web:               hasTool("web_fetch"),
		WebSearch:         hasTool("web_search"),
		Subagent:          hasTool(agent.SubagentToolName),
		SubagentMaxDepth:  cfg.SubagentMaxDepth,
		FocusedTasks:      focusedRegistered,
	}
	// Surface the available focused-task profiles whenever the tool
	// itself is registered. Computed via the same probe doctor uses
	// so the CLI diagnostic and the server API agree for matching
	// configurations.
	if focusedRegistered {
		caps.FocusedTaskProfiles = focusedTaskProfilesForLog(cfg)
	}
	return caps
}

func summarizeDurableSession(pool *SessionPool, id string) (sessionSummary, bool, error) {
	dir := pool.sessionDirPath(id)
	info, found, err := durableSessionDirInfo(dir)
	if err != nil {
		return sessionSummary{}, false, err
	}
	if !found {
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
	var convMod time.Time
	if exists, convMod, err = durableRegularFileModTime(filepath.Join(dir, "conversation.jsonl")); err != nil {
		return sessionSummary{}, false, err
	}
	summary.HasConversation = exists
	if exists && convMod.After(newest) {
		newest = convMod
	}
	if exists {
		summary.LatestUserMessage, err = latestUserMessageFromConversationFile(filepath.Join(dir, "conversation.jsonl"))
		if err != nil {
			return sessionSummary{}, false, err
		}
	}
	var eventMod time.Time
	if exists, eventMod, err = durableRegularFileModTime(filepath.Join(dir, "events.jsonl")); err != nil {
		return sessionSummary{}, false, err
	}
	summary.HasEvents = exists
	if exists && eventMod.After(newest) {
		newest = eventMod
	}
	var planMod time.Time
	if exists, planMod, err = durableRegularFileModTime(filepath.Join(dir, "plan.json")); err != nil {
		return sessionSummary{}, false, err
	}
	summary.HasPlan = exists
	if exists && planMod.After(newest) {
		newest = planMod
	}
	if exists {
		summary.PlanSummary = summarizeSessionPlanFile(pool, id)
	}
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
	if a.LatestUserMessage == "" && b.LatestUserMessage != "" {
		a.LatestUserMessage = b.LatestUserMessage
	}
	a.HasEvents = a.HasEvents || b.HasEvents
	a.HasPlan = a.HasPlan || b.HasPlan
	if b.PlanSummary != nil {
		a.PlanSummary = b.PlanSummary
	}
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
	if b.Capabilities != nil {
		a.Capabilities = b.Capabilities
	}
	return a
}

func durableSessionDirInfo(path string) (os.FileInfo, bool, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return nil, false, nil
	}
	return fi, true, nil
}

func latestUserMessageFromConversationFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	defer f.Close()

	var latest string
	r := bufio.NewReaderSize(f, 64*1024)
	for {
		line, tooLong, err := readSessionSummaryLine(r, maxSessionSummaryLineBytes)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		if tooLong {
			continue
		}
		var msg agent.ChatMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if msg.Role != "user" {
			continue
		}
		if summary := summarizeLatestUserMessage(msg.Content); summary != "" {
			latest = summary
		}
	}
	return latest, nil
}

func readSessionSummaryLine(r *bufio.Reader, maxBytes int) ([]byte, bool, error) {
	var line []byte
	tooLong := false
	for {
		frag, err := r.ReadSlice('\n')
		if len(frag) > 0 && !tooLong {
			if len(line)+len(frag) > maxBytes {
				line = nil
				tooLong = true
			} else {
				line = append(line, frag...)
			}
		}
		switch {
		case err == nil:
			return bytes.TrimRight(line, "\r\n"), tooLong, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if len(line) == 0 && !tooLong {
				return nil, false, io.EOF
			}
			return bytes.TrimRight(line, "\r\n"), tooLong, nil
		default:
			return nil, false, err
		}
	}
}

func latestUserMessageFromMessages(messages []agent.ChatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "user" {
			continue
		}
		if summary := summarizeLatestUserMessage(messages[i].Content); summary != "" {
			return summary
		}
	}
	return ""
}

func summarizeLatestUserMessage(text string) string {
	singleLine := strings.Join(strings.Fields(text), " ")
	runes := []rune(singleLine)
	if len(runes) <= maxSessionTaskSummaryChars {
		return singleLine
	}
	if maxSessionTaskSummaryChars <= 3 {
		return string(runes[:maxSessionTaskSummaryChars])
	}
	return string(runes[:maxSessionTaskSummaryChars-3]) + "..."
}

func durableRegularFileModTime(path string) (bool, time.Time, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, time.Time{}, nil
		}
		return false, time.Time{}, err
	}
	if fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return false, time.Time{}, nil
	}
	return true, fi.ModTime(), nil
}

func durableMemoryExists(sessionDir string) bool {
	for _, path := range []string{
		filepath.Join(sessionDir, "USER.md"),
		filepath.Join(sessionDir, "core.md"),
		filepath.Join(sessionDir, "MEMORY.md"),
		filepath.Join(sessionDir, "topics"),
	} {
		if durableStatePathExists(path) {
			return true
		}
	}
	return false
}

func durableStatePathExists(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink == 0
}

func durableReadDir(dir string) ([]os.DirEntry, error) {
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("durable path is not a directory")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("durable path must not be a symlink")
	}
	return os.ReadDir(dir)
}

func rejectSymlinkUnderDir(root, rel string) error {
	clean := filepath.Clean(rel)
	if clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.IsAbs(clean) {
		return errors.New("path escapes root")
	}
	cur := root
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("durable path must not contain symlinks")
		}
	}
	return nil
}

func durableDirEntryIsSymlink(ent os.DirEntry) bool {
	return ent.Type()&os.ModeSymlink != 0
}

func dirHasAnyEntry(dir string) bool {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
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
