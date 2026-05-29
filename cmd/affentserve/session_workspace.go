package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/sessionstate"
)

const (
	maxSessionWorkspaceActionBytes = 16
	maxSessionWorkspacePathBytes   = 2048
)

type sessionWorkspaceState struct {
	mu         sync.RWMutex
	sessionID  string
	sessionDir string
	root       string
	current    string
	owned      bool
}

func newSessionWorkspaceState(sessionID, sessionDir, root, current string, owned bool) *sessionWorkspaceState {
	root = strings.TrimSpace(root)
	current = strings.TrimSpace(current)
	if current == "" {
		current = root
	}
	return &sessionWorkspaceState{
		sessionID:  sessionID,
		sessionDir: sessionDir,
		root:       root,
		current:    current,
		owned:      owned,
	}
}

func (s *sessionWorkspaceState) Root() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.root
}

func (s *sessionWorkspaceState) Current() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

func (s *sessionWorkspaceState) Owned() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.owned
}

func (s *sessionWorkspaceState) Set(path string) (string, error) {
	if s == nil {
		return "", errors.New("workspace state is not configured")
	}
	path = strings.TrimSpace(path)
	s.mu.RLock()
	root := s.root
	s.mu.RUnlock()
	next, err := resolveActiveWorkspacePath(root, path)
	if err != nil {
		return "", err
	}
	if err := s.updateCurrent(next); err != nil {
		return "", err
	}
	return next, nil
}

func (s *sessionWorkspaceState) Reset() (string, error) {
	if s == nil {
		return "", errors.New("workspace state is not configured")
	}
	s.mu.RLock()
	root := s.root
	s.mu.RUnlock()
	if err := s.updateCurrent(root); err != nil {
		return "", err
	}
	return root, nil
}

func (s *sessionWorkspaceState) updateCurrent(next string) error {
	next = strings.TrimSpace(next)
	if next == "" {
		return errors.New("workspace path is required")
	}
	s.mu.RLock()
	meta := sessionstate.Metadata{
		SessionID:     s.sessionID,
		WorkspaceRoot: s.root,
		WorkspacePath: next,
		UpdatedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	}
	sessionDir := s.sessionDir
	s.mu.RUnlock()
	if err := sessionstate.WriteMetadata(sessionDir, meta); err != nil {
		return err
	}
	s.mu.Lock()
	s.current = next
	s.mu.Unlock()
	return nil
}

func resolveActiveWorkspacePath(root, raw string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", errors.New("workspace root is not configured")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "." || raw == "/" {
		raw = "."
	}
	var candidate string
	if filepath.IsAbs(raw) {
		candidate = filepath.Clean(raw)
	} else {
		candidate = filepath.Join(rootAbs, filepath.FromSlash(raw))
	}
	candidateAbs, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	inside, err := pathWithin(rootAbs, candidateAbs)
	if err != nil {
		return "", err
	}
	if !inside {
		return "", fmt.Errorf("workspace path %q escapes workspace root", raw)
	}
	info, err := os.Lstat(candidateAbs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace path %q is not a directory", raw)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("workspace path %q must not be a symlink", raw)
	}
	return candidateAbs, nil
}

func restoreActiveWorkspace(root, stored string) string {
	if strings.TrimSpace(stored) == "" {
		return root
	}
	if resolved, err := resolveActiveWorkspacePath(root, stored); err == nil {
		return resolved
	}
	return root
}

type sessionWorkspaceToolResponse struct {
	SessionID      string `json:"session_id"`
	WorkspaceRoot  string `json:"workspace_root"`
	WorkspacePath  string `json:"workspace_path"`
	WorkspaceLabel string `json:"workspace_label,omitempty"`
	Changed        bool   `json:"changed,omitempty"`
	Summary        string `json:"summary"`
}

func sessionWorkspaceTool(state *sessionWorkspaceState) *agent.Tool {
	schema := json.RawMessage(`{
		"type": "object",
		"additionalProperties": false,
		"required": ["action"],
		"properties": {
			"action": {"type": "string", "enum": ["inspect", "set", "reset"], "description": "inspect returns the current active workspace; set changes it to an existing directory under the workspace root; reset returns to the root."},
			"path": {"type": "string", "maxLength": 2048, "description": "Workspace-root-relative or absolute directory path. Required for action=set."}
		}
	}`)
	return &agent.Tool{
		Name:        agent.SessionWorkspaceToolName,
		Description: "Inspect or switch this session's active workspace. Use action=set after creating or cloning a project directory so later shell/file/search tools default to that project. The path must be an existing directory inside the configured workspace root.",
		Schema:      schema,
		Execute: func(_ context.Context, args json.RawMessage) (string, error) {
			var req struct {
				Action string `json:"action"`
				Path   string `json:"path"`
			}
			dec := json.NewDecoder(bytes.NewReader(args))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&req); err != nil {
				return "", err
			}
			var extra struct{}
			if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
				return "", errors.New("arguments must contain a single JSON object")
			}
			action := strings.TrimSpace(req.Action)
			if action == "" {
				return "", errors.New("action is required\nNext: retry session_workspace with action=inspect, action=set, or action=reset")
			}
			if len(action) > maxSessionWorkspaceActionBytes {
				return "", fmt.Errorf("action is %d bytes; session_workspace action supports up to %d bytes", len(action), maxSessionWorkspaceActionBytes)
			}
			if len(req.Path) > maxSessionWorkspacePathBytes {
				return "", fmt.Errorf("path is %d bytes; session_workspace path supports up to %d bytes", len(req.Path), maxSessionWorkspacePathBytes)
			}
			before := state.Current()
			switch action {
			case "inspect":
			case "set":
				if strings.TrimSpace(req.Path) == "" {
					return "", errors.New("path is required for action=set\nNext: retry session_workspace with action=set and a workspace-relative project directory")
				}
				if _, err := state.Set(req.Path); err != nil {
					return "", err
				}
			case "reset":
				if _, err := state.Reset(); err != nil {
					return "", err
				}
			default:
				return "", fmt.Errorf("unsupported action %q\nNext: retry session_workspace with action=inspect, action=set, or action=reset", action)
			}
			current := state.Current()
			resp := sessionWorkspaceToolResponse{
				SessionID:      state.sessionID,
				WorkspaceRoot:  state.Root(),
				WorkspacePath:  current,
				WorkspaceLabel: workspaceLabel(current),
				Changed:        current != before,
				Summary:        fmt.Sprintf("active workspace is %s", workspaceLabel(current)),
			}
			raw, err := json.MarshalIndent(resp, "", "  ")
			if err != nil {
				return "", err
			}
			return string(raw), nil
		},
		CatalogGroup: "Core",
	}
}
