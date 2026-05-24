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
	"strconv"
	"strings"
	"time"

	agent "github.com/affinefoundation/affent/internal/agent"
)

const (
	defaultTranscriptListLimit = 100
	maxTranscriptListLimit     = 1000
	transcriptReadDirBatch     = 128
	defaultTranscriptReadLimit = 64 * 1024
	maxTranscriptReadLimit     = 1024 * 1024
)

type transcriptListResponse struct {
	SessionID   string           `json:"session_id"`
	Transcripts []transcriptInfo `json:"transcripts"`
	NextAfter   string           `json:"next_after,omitempty"`
	HasMore     bool             `json:"has_more"`
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
		handleSessionTranscriptList(sessionDir, sessionID, w, r)
		return
	}
	handleSessionTranscriptRead(sessionDir, sessionID, transcriptPath, w, r)
}

func handleSessionTranscriptList(sessionDir, sessionID string, w http.ResponseWriter, r *http.Request) {
	if _, found, err := durableSessionDirInfo(sessionDir); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "stat session", err)
		return
	} else if !found {
		writeJSONError(w, http.StatusNotFound, "session not found", os.ErrNotExist)
		return
	}
	after, limit, err := parseTranscriptListQuery(sessionID, r)
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid transcript query", err, "bad_request")
		return
	}
	paths, hasMore, err := listTranscriptPaths(sessionDir, sessionID, after, limit)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "read transcripts", err)
		return
	}
	out := transcriptListResponse{SessionID: sessionID, Transcripts: []transcriptInfo{}, HasMore: hasMore}
	for _, rel := range paths {
		full := filepath.Join(sessionDir, filepath.FromSlash(rel))
		info, err := os.Lstat(full)
		if err != nil {
			continue
		}
		if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		kind, childID, ok := transcriptInfoFromPath(sessionID, rel)
		if !ok {
			continue
		}
		out.Transcripts = append(out.Transcripts, transcriptInfo{
			Kind:    kind,
			ChildID: childID,
			Path:    rel,
			Size:    info.Size(),
			ModTime: info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	if len(out.Transcripts) > 0 && out.HasMore {
		out.NextAfter = out.Transcripts[len(out.Transcripts)-1].Path
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func parseTranscriptListQuery(sessionID string, r *http.Request) (after string, limit int, err error) {
	limit = defaultTranscriptListLimit
	q := r.URL.Query()
	if raw := q.Get("after"); raw != "" {
		after, err = cleanTranscriptRequestPath(sessionID, raw)
		if err != nil {
			return "", 0, err
		}
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return "", 0, err
		}
		if n <= 0 {
			return "", 0, errors.New("limit must be positive")
		}
		if n > maxTranscriptListLimit {
			n = maxTranscriptListLimit
		}
		limit = n
	}
	return after, limit, nil
}

func listTranscriptPaths(sessionDir, sessionID, after string, limit int) ([]string, bool, error) {
	candidates := map[string]struct{}{}
	for _, root := range transcriptRoots(sessionID) {
		dirPath := filepath.Join(sessionDir, filepath.FromSlash(root.rel))
		info, err := os.Lstat(dirPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, false, err
		}
		if !info.IsDir() {
			return nil, false, errors.New("durable path is not a directory")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, false, errors.New("durable path must not be a symlink")
		}
		dir, err := os.Open(dirPath)
		if err != nil {
			return nil, false, err
		}
		for {
			entries, err := dir.ReadDir(transcriptReadDirBatch)
			if err != nil && !errors.Is(err, io.EOF) {
				_ = dir.Close()
				return nil, false, err
			}
			for _, ent := range entries {
				if ent.IsDir() || durableDirEntryIsSymlink(ent) || !strings.HasSuffix(ent.Name(), ".jsonl") {
					continue
				}
				rel := path.Join(root.rel, ent.Name())
				if rel <= after {
					continue
				}
				addBoundedStringCandidate(candidates, rel, limit+1)
			}
			if errors.Is(err, io.EOF) {
				break
			}
		}
		if err := dir.Close(); err != nil {
			return nil, false, err
		}
	}
	paths := sortedStringCandidates(candidates)
	hasMore := len(paths) > limit
	if hasMore {
		paths = paths[:limit]
	}
	return paths, hasMore, nil
}

func transcriptInfoFromPath(sessionID, rel string) (kind string, childID string, ok bool) {
	for _, root := range transcriptRoots(sessionID) {
		if strings.HasPrefix(rel, root.rel+"/") && path.Dir(rel) == root.rel {
			return root.kind, strings.TrimSuffix(path.Base(rel), ".jsonl"), true
		}
	}
	return "", "", false
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
	if err := rejectSymlinkUnderDir(sessionDir, filepath.FromSlash(rel)); err != nil {
		return "", err
	}
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
