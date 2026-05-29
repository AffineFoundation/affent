package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/sessionstate"
)

const (
	defaultSessionFileReadLimit = 64 * 1024
	maxSessionFileReadLimit     = 1024 * 1024
	defaultSessionDirListLimit  = 200
	maxSessionDirListLimit      = 500
	maxSessionFileWriteBytes    = 2 * 1024 * 1024
)

type sessionFileResponse struct {
	SessionID string             `json:"session_id"`
	Path      string             `json:"path"`
	Kind      string             `json:"kind"`
	Bytes     int64              `json:"bytes,omitempty"`
	ModTime   string             `json:"mod_time,omitempty"`
	Offset    int64              `json:"offset,omitempty"`
	Text      string             `json:"text,omitempty"`
	HasMore   bool               `json:"has_more,omitempty"`
	Entries   []sessionFileEntry `json:"entries,omitempty"`
}

type sessionFileEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Bytes   int64  `json:"bytes,omitempty"`
	ModTime string `json:"mod_time,omitempty"`
}

type sessionFileWriteRequest struct {
	Path string `json:"path"`
	Text string `json:"text"`
}

func handleSessionFiles(pool *SessionPool, sessionID string, w http.ResponseWriter, r *http.Request) {
	if pool == nil {
		writeJSONError(w, http.StatusNotFound, "session not found", nil)
		return
	}
	if err := agent.ValidateSessionID(sessionID); err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid session id", err, "bad_request")
		return
	}
	workspace, err := sessionWorkspaceForFiles(pool, sessionID)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) || errors.Is(err, os.ErrNotExist) {
			writeJSONErrorTyped(w, http.StatusNotFound, "workspace not available", err, "workspace_unavailable")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "open workspace", err)
		return
	}
	handleWorkspaceFilesAtRoot(sessionID, workspace, w, r)
}

func handleWorkspaceFiles(pool *SessionPool, w http.ResponseWriter, r *http.Request) {
	if pool == nil {
		writeJSONError(w, http.StatusNotFound, "workspace not available", nil)
		return
	}
	workspace, err := workspaceRootForFiles(pool)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSONErrorTyped(w, http.StatusNotFound, "workspace not available", err, "workspace_unavailable")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "open workspace", err)
		return
	}
	handleWorkspaceFilesAtRoot("workspace", workspace, w, r)
}

func handleWorkspaceFilesAtRoot(sessionID, workspace string, w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		handleSessionFileWrite(sessionID, workspace, w, r)
		return
	}
	rel, err := cleanSessionFileRequestPath(r.URL.Query().Get("path"))
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid file path", err, "bad_request")
		return
	}
	full, err := resolveSessionWorkspacePath(workspace, rel)
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid file path", err, "bad_request")
		return
	}
	info, err := os.Lstat(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSONError(w, http.StatusNotFound, "file not found", err)
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "stat file", err)
		return
	}
	if info.Mode()&os.ModeSymlink != 0 {
		writeJSONErrorTyped(w, http.StatusBadRequest, "workspace path must not be a symlink", nil, "bad_request")
		return
	}
	if info.IsDir() {
		handleSessionFileDirectory(sessionID, full, rel, info, w, r)
		return
	}
	handleSessionFileRead(sessionID, full, rel, info, w, r)
}

func workspaceRootForFiles(pool *SessionPool) (string, error) {
	root := strings.TrimSpace(pool.cfg.WorkspaceRoot)
	if root == "" {
		var ok bool
		root, ok = defaultWorkspaceRootIfPresent()
		if !ok {
			return "", os.ErrNotExist
		}
	}
	return resolveWorkspaceRoot(root)
}

func handleSessionFileWrite(sessionID, workspace string, w http.ResponseWriter, r *http.Request) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSessionFileWriteBytes+4096))
	dec.DisallowUnknownFields()
	var req sessionFileWriteRequest
	if err := dec.Decode(&req); err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid file upload", err, "bad_request")
		return
	}
	var extra map[string]any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid file upload", errors.New("request body must contain exactly one JSON object"), "bad_request")
		return
	}
	rel, err := cleanSessionFileRequestPath(req.Path)
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid file path", err, "bad_request")
		return
	}
	if rel == "." {
		writeJSONErrorTyped(w, http.StatusBadRequest, "upload path must be a file", nil, "bad_request")
		return
	}
	if len([]byte(req.Text)) > maxSessionFileWriteBytes {
		writeJSONErrorTyped(w, http.StatusRequestEntityTooLarge, "file upload too large", nil, "payload_too_large")
		return
	}
	full, err := resolveSessionWorkspacePath(workspace, rel)
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid file path", err, "bad_request")
		return
	}
	if info, err := os.Lstat(full); err == nil {
		if info.IsDir() {
			writeJSONErrorTyped(w, http.StatusBadRequest, "upload path is a directory", nil, "bad_request")
			return
		}
		if info.Mode()&os.ModeSymlink != 0 {
			writeJSONErrorTyped(w, http.StatusBadRequest, "workspace path must not be a symlink", nil, "bad_request")
			return
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		writeJSONError(w, http.StatusInternalServerError, "stat file", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "create parent directory", err)
		return
	}
	if err := os.WriteFile(full, []byte(req.Text), 0o644); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "write file", err)
		return
	}
	info, err := os.Lstat(full)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "stat file", err)
		return
	}
	handleSessionFileRead(sessionID, full, rel, info, w, r)
}

func sessionWorkspaceForFiles(pool *SessionPool, sessionID string) (string, error) {
	if sess, err := pool.Get(sessionID); err == nil && strings.TrimSpace(sess.workspace) != "" {
		return sess.workspace, nil
	}
	meta, found, err := sessionstate.ReadMetadata(pool.sessionDirPath(sessionID))
	if err != nil {
		return "", err
	}
	if !found || strings.TrimSpace(meta.WorkspacePath) == "" {
		return "", ErrSessionNotFound
	}
	workspace := strings.TrimSpace(meta.WorkspacePath)
	info, err := os.Lstat(workspace)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace is not a directory")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("workspace must not be a symlink")
	}
	return workspace, nil
}

func handleSessionFileDirectory(sessionID, full, rel string, info os.FileInfo, w http.ResponseWriter, r *http.Request) {
	limit, err := parseSessionDirLimit(r)
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid file query", err, "bad_request")
		return
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "read directory", err)
		return
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})
	out := sessionFileResponse{
		SessionID: sessionID,
		Path:      rel,
		Kind:      "directory",
		ModTime:   info.ModTime().UTC().Format(time.RFC3339),
		Entries:   []sessionFileEntry{},
	}
	for _, ent := range entries {
		if durableDirEntryIsSymlink(ent) {
			continue
		}
		entInfo, err := ent.Info()
		if err != nil {
			continue
		}
		kind := "file"
		if entInfo.IsDir() {
			kind = "directory"
		}
		out.Entries = append(out.Entries, sessionFileEntry{
			Name:    ent.Name(),
			Path:    path.Join(rel, ent.Name()),
			Kind:    kind,
			Bytes:   entInfo.Size(),
			ModTime: entInfo.ModTime().UTC().Format(time.RFC3339),
		})
		if len(out.Entries) >= limit {
			out.HasMore = len(entries) > limit
			break
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func handleSessionFileRead(sessionID, full, rel string, info os.FileInfo, w http.ResponseWriter, r *http.Request) {
	offset, limit, err := parseSessionFileReadQuery(r)
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid file query", err, "bad_request")
		return
	}
	f, err := os.Open(full)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "open file", err)
		return
	}
	defer f.Close()
	if offset > info.Size() {
		offset = info.Size()
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "seek file", err)
		return
	}
	chunkBytes := limit
	if remaining := info.Size() - offset; remaining < chunkBytes {
		chunkBytes = remaining
	}
	raw, err := io.ReadAll(io.LimitReader(f, chunkBytes))
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "read file", err)
		return
	}
	nextOffset := offset + int64(len(raw))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sessionFileResponse{
		SessionID: sessionID,
		Path:      rel,
		Kind:      "file",
		Bytes:     info.Size(),
		ModTime:   info.ModTime().UTC().Format(time.RFC3339),
		Offset:    offset,
		Text:      string(raw),
		HasMore:   nextOffset < info.Size(),
	})
}

func cleanSessionFileRequestPath(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return ".", nil
	}
	decoded := strings.TrimSpace(raw)
	if decoded == "" || decoded == "/" {
		return ".", nil
	}
	decoded = strings.TrimPrefix(decoded, "/")
	clean := path.Clean(decoded)
	if clean == "." {
		return ".", nil
	}
	if strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") || clean == ".." {
		return "", fmt.Errorf("file path %q escapes workspace", raw)
	}
	return clean, nil
}

func resolveSessionWorkspacePath(workspace, rel string) (string, error) {
	if rel == "." {
		return filepath.Abs(workspace)
	}
	if err := rejectSymlinkUnderDir(workspace, filepath.FromSlash(rel)); err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	fullAbs, err := filepath.Abs(filepath.Join(workspace, filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}
	inside, err := pathWithin(rootAbs, fullAbs)
	if err != nil {
		return "", err
	}
	if !inside {
		return "", errors.New("file path escapes workspace")
	}
	return fullAbs, nil
}

func parseSessionDirLimit(r *http.Request) (int, error) {
	limit := defaultSessionDirListLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return 0, err
		}
		if n <= 0 {
			return 0, errors.New("limit must be positive")
		}
		if n > maxSessionDirListLimit {
			n = maxSessionDirListLimit
		}
		limit = n
	}
	return limit, nil
}

func parseSessionFileReadQuery(r *http.Request) (offset int64, limit int64, err error) {
	limit = defaultSessionFileReadLimit
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
		if n > maxSessionFileReadLimit {
			n = maxSessionFileReadLimit
		}
		limit = n
	}
	return offset, limit, nil
}
