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
	artifactPathPrefix       = ".affent/artifacts/tool-results"
	defaultArtifactReadLimit = 64 * 1024
	maxArtifactReadLimit     = 1024 * 1024
)

type artifactListResponse struct {
	SessionID string         `json:"session_id"`
	Artifacts []artifactInfo `json:"artifacts"`
}

type artifactInfo struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time,omitempty"`
}

func handleSessionArtifacts(pool *SessionPool, sessionID, artifactPath string, w http.ResponseWriter, r *http.Request) {
	if pool == nil {
		writeJSONError(w, http.StatusNotFound, "session not found", nil)
		return
	}
	if err := agent.ValidateSessionID(sessionID); err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid session id", err, "bad_request")
		return
	}
	sessionDir := pool.sessionDirPath(sessionID)
	if strings.Trim(artifactPath, "/") == "" {
		handleSessionArtifactList(sessionDir, sessionID, w)
		return
	}
	handleSessionArtifactRead(sessionDir, artifactPath, w, r)
}

func handleSessionArtifactList(sessionDir, sessionID string, w http.ResponseWriter) {
	if _, found, err := durableSessionDirInfo(sessionDir); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "stat session", err)
		return
	} else if !found {
		writeJSONError(w, http.StatusNotFound, "session not found", os.ErrNotExist)
		return
	}
	root := filepath.Join(sessionDir, filepath.FromSlash(artifactPathPrefix))
	entries, err := durableReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(artifactListResponse{SessionID: sessionID, Artifacts: []artifactInfo{}})
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "read artifacts", err)
		return
	}
	out := artifactListResponse{SessionID: sessionID, Artifacts: []artifactInfo{}}
	for _, ent := range entries {
		if ent.IsDir() || durableDirEntryIsSymlink(ent) {
			continue
		}
		info, err := ent.Info()
		if err != nil {
			continue
		}
		out.Artifacts = append(out.Artifacts, artifactInfo{
			Path:    path.Join(artifactPathPrefix, ent.Name()),
			Size:    info.Size(),
			ModTime: info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func handleSessionArtifactRead(sessionDir, rawPath string, w http.ResponseWriter, r *http.Request) {
	rel, err := cleanArtifactRequestPath(rawPath)
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid artifact path", err, "bad_request")
		return
	}
	full, err := resolveSessionArtifactPath(sessionDir, rel)
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid artifact path", err, "bad_request")
		return
	}
	offset, limit, err := parseArtifactReadQuery(r)
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid artifact query", err, "bad_request")
		return
	}
	f, err := os.Open(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSONError(w, http.StatusNotFound, "artifact not found", err)
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "open artifact", err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "stat artifact", err)
		return
	}
	if info.IsDir() {
		writeJSONErrorTyped(w, http.StatusBadRequest, "artifact is a directory", nil, "bad_request")
		return
	}
	if offset > info.Size() {
		offset = info.Size()
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "seek artifact", err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Affent-Artifact-Path", rel)
	w.Header().Set("X-Affent-Artifact-Bytes", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("X-Affent-Artifact-Offset", strconv.FormatInt(offset, 10))
	_, _ = io.Copy(w, io.LimitReader(f, limit))
}

func cleanArtifactRequestPath(raw string) (string, error) {
	raw = strings.TrimPrefix(raw, "/")
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		return "", err
	}
	decoded = strings.TrimSpace(decoded)
	if decoded == "" {
		return "", errors.New("artifact path is required")
	}
	decoded = strings.TrimPrefix(decoded, "/")
	if !strings.Contains(decoded, "/") {
		decoded = path.Join(artifactPathPrefix, decoded)
	}
	clean := path.Clean(decoded)
	if clean == "." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return "", fmt.Errorf("artifact path %q escapes session", raw)
	}
	if clean != artifactPathPrefix && !strings.HasPrefix(clean, artifactPathPrefix+"/") {
		return "", fmt.Errorf("artifact path must be under %s", artifactPathPrefix)
	}
	if clean == artifactPathPrefix {
		return "", errors.New("artifact filename is required")
	}
	return clean, nil
}

func resolveSessionArtifactPath(sessionDir, rel string) (string, error) {
	root := filepath.Join(sessionDir, filepath.FromSlash(artifactPathPrefix))
	full := filepath.Join(sessionDir, filepath.FromSlash(rel))
	if err := rejectSymlinkUnderDir(sessionDir, filepath.FromSlash(rel)); err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	fullAbs, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	if resolvedRoot, err := filepath.EvalSymlinks(rootAbs); err == nil {
		rootAbs = resolvedRoot
	}
	if resolvedFull, err := filepath.EvalSymlinks(fullAbs); err == nil {
		fullAbs = resolvedFull
	}
	inside, err := pathWithin(rootAbs, fullAbs)
	if err != nil {
		return "", err
	}
	if !inside {
		return "", errors.New("artifact path escapes artifact root")
	}
	return fullAbs, nil
}

func parseArtifactReadQuery(r *http.Request) (offset int64, limit int64, err error) {
	limit = defaultArtifactReadLimit
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
		if n > maxArtifactReadLimit {
			n = maxArtifactReadLimit
		}
		limit = n
	}
	return offset, limit, nil
}

func pathWithin(root, candidate string) (bool, error) {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false, fmt.Errorf("compare paths: %w", err)
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))), nil
}
