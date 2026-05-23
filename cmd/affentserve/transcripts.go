package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	agent "github.com/affinefoundation/affent/internal/agent"
)

const (
	defaultTranscriptReadLimit = 64 * 1024
	maxTranscriptReadLimit     = 1024 * 1024
)

type transcriptListResponse struct {
	SessionID   string           `json:"session_id"`
	Transcripts []transcriptInfo `json:"transcripts"`
}

type transcriptInfo struct {
	Kind    string `json:"kind"`
	ChildID string `json:"child_id"`
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time,omitempty"`
}

func handleSessionTranscripts(pool *SessionPool, sessionID, transcriptPath string, w http.ResponseWriter, r *http.Request) {
	if pool == nil {
		writeJSONError(w, http.StatusNotFound, "session not found", nil)
		return
	}
	if err := agent.ValidateSessionID(sessionID); err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid session id", err, "bad_request")
		return
	}
	sessionDir := pool.sessionDirPath(sessionID)
	if strings.Trim(transcriptPath, "/") == "" {
		handleSessionTranscriptList(sessionDir, sessionID, w)
		return
	}
	handleSessionTranscriptRead(sessionDir, sessionID, transcriptPath, w, r)
}

func handleSessionTranscriptList(sessionDir, sessionID string, w http.ResponseWriter) {
	if _, found, err := durableSessionDirInfo(sessionDir); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "stat session", err)
		return
	} else if !found {
		writeJSONError(w, http.StatusNotFound, "session not found", os.ErrNotExist)
		return
	}
	out := transcriptListResponse{SessionID: sessionID, Transcripts: []transcriptInfo{}}
	for _, root := range transcriptRoots(sessionID) {
		dir := filepath.Join(sessionDir, filepath.FromSlash(root.rel))
		entries, err := durableReadDir(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			writeJSONError(w, http.StatusInternalServerError, "read transcripts", err)
			return
		}
		for _, ent := range entries {
			if ent.IsDir() || durableDirEntryIsSymlink(ent) || !strings.HasSuffix(ent.Name(), ".jsonl") {
				continue
			}
			info, err := ent.Info()
			if err != nil {
				continue
			}
			childID := strings.TrimSuffix(ent.Name(), ".jsonl")
			out.Transcripts = append(out.Transcripts, transcriptInfo{
				Kind:    root.kind,
				ChildID: childID,
				Path:    path.Join(root.rel, ent.Name()),
				Size:    info.Size(),
				ModTime: info.ModTime().UTC().Format(time.RFC3339),
			})
		}
	}
	sort.Slice(out.Transcripts, func(i, j int) bool {
		return out.Transcripts[i].Path < out.Transcripts[j].Path
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func handleSessionTranscriptRead(sessionDir, sessionID, rawPath string, w http.ResponseWriter, r *http.Request) {
	rel, err := cleanTranscriptRequestPath(sessionID, rawPath)
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid transcript path", err, "bad_request")
		return
	}
	full, err := resolveSessionTranscriptPath(sessionDir, sessionID, rel)
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid transcript path", err, "bad_request")
		return
	}
	offset, limit, err := parseTranscriptReadQuery(r)
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid transcript query", err, "bad_request")
		return
	}
	f, err := os.Open(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSONError(w, http.StatusNotFound, "transcript not found", err)
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "open transcript", err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "stat transcript", err)
		return
	}
	if info.IsDir() {
		writeJSONErrorTyped(w, http.StatusBadRequest, "transcript is a directory", nil, "bad_request")
		return
	}
	if offset > info.Size() {
		offset = info.Size()
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "seek transcript", err)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Affent-Transcript-Path", rel)
	w.Header().Set("X-Affent-Transcript-Bytes", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("X-Affent-Transcript-Offset", strconv.FormatInt(offset, 10))
	_, _ = io.Copy(w, io.LimitReader(f, limit))
}

type transcriptRoot struct {
	kind string
	rel  string
}

func transcriptRoots(sessionID string) []transcriptRoot {
	return []transcriptRoot{
		{kind: "focused_task", rel: path.Join("focused-tasks", sessionID)},
		{kind: "subagent", rel: path.Join("subagents", sessionID)},
	}
}

func cleanTranscriptRequestPath(sessionID, raw string) (string, error) {
	raw = strings.TrimPrefix(raw, "/")
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		return "", err
	}
	decoded = strings.TrimSpace(strings.TrimPrefix(decoded, "/"))
	if decoded == "" {
		return "", errors.New("transcript path is required")
	}
	clean := path.Clean(decoded)
	if clean == "." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return "", fmt.Errorf("transcript path %q escapes session", raw)
	}
	if !strings.HasSuffix(clean, ".jsonl") {
		return "", errors.New("transcript path must end with .jsonl")
	}
	for _, root := range transcriptRoots(sessionID) {
		if strings.HasPrefix(clean, root.rel+"/") && path.Dir(clean) == root.rel {
			return clean, nil
		}
	}
	return "", errors.New("transcript path must be under focused-tasks/<session_id>/ or subagents/<session_id>/")
}

func resolveSessionTranscriptPath(sessionDir, sessionID, rel string) (string, error) {
	full := filepath.Join(sessionDir, filepath.FromSlash(rel))
	fullAbs, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	if resolvedFull, err := filepath.EvalSymlinks(fullAbs); err == nil {
		fullAbs = resolvedFull
	}
	allowed := false
	for _, root := range transcriptRoots(sessionID) {
		rootAbs, err := filepath.Abs(filepath.Join(sessionDir, filepath.FromSlash(root.rel)))
		if err != nil {
			return "", err
		}
		if resolvedRoot, err := filepath.EvalSymlinks(rootAbs); err == nil {
			rootAbs = resolvedRoot
		}
		inside, err := pathWithin(rootAbs, fullAbs)
		if err != nil {
			return "", err
		}
		if inside {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", errors.New("transcript path escapes transcript roots")
	}
	return fullAbs, nil
}

func parseTranscriptReadQuery(r *http.Request) (offset int64, limit int64, err error) {
	limit = defaultTranscriptReadLimit
	q := r.URL.Query()
	if raw := q.Get("offset"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return 0, 0, err
		}
		if n < 0 {
			return 0, 0, errors.New("offset must be non-negative")
		}
		offset = n
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return 0, 0, err
		}
		if n <= 0 {
			return 0, 0, errors.New("limit must be positive")
		}
		if n > maxTranscriptReadLimit {
			n = maxTranscriptReadLimit
		}
		limit = n
	}
	return offset, limit, nil
}
