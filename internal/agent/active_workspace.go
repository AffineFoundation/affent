package agent

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
)

const (
	maxSessionWorkspaceActionBytes = 16
	maxSessionWorkspacePathBytes   = 2048
)

// ActiveWorkspaceState tracks the workspace directory tools should treat as
// current. The root is the configured workspace boundary; current may move to
// an existing child project after clone/create workflows.
type ActiveWorkspaceState struct {
	mu        sync.RWMutex
	sessionID string
	root      string
	current   string
	owned     bool
	onChange  func(current string) error
}

func NewActiveWorkspaceState(sessionID, root, current string, owned bool, onChange func(current string) error) *ActiveWorkspaceState {
	root = strings.TrimSpace(root)
	current = strings.TrimSpace(current)
	if current == "" {
		current = root
	}
	return &ActiveWorkspaceState{
		sessionID: sessionID,
		root:      root,
		current:   current,
		owned:     owned,
		onChange:  onChange,
	}
}

func (s *ActiveWorkspaceState) Root() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.root
}

func (s *ActiveWorkspaceState) Current() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

func (s *ActiveWorkspaceState) Owned() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.owned
}

func (s *ActiveWorkspaceState) Set(path string) (string, error) {
	if s == nil {
		return "", errors.New("workspace state is not configured")
	}
	s.mu.RLock()
	root := s.root
	s.mu.RUnlock()
	next, err := ResolveActiveWorkspacePath(root, path)
	if err != nil {
		return "", err
	}
	if err := s.updateCurrent(next); err != nil {
		return "", err
	}
	return next, nil
}

func (s *ActiveWorkspaceState) Reset() (string, error) {
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

func (s *ActiveWorkspaceState) updateCurrent(next string) error {
	next = strings.TrimSpace(next)
	if next == "" {
		return errors.New("workspace path is required")
	}
	s.mu.RLock()
	onChange := s.onChange
	s.mu.RUnlock()
	if onChange != nil {
		if err := onChange(next); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.current = next
	s.mu.Unlock()
	return nil
}

func ResolveActiveWorkspacePath(root, raw string) (string, error) {
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
	inside, err := activeWorkspacePathWithin(rootAbs, candidateAbs)
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

func RestoreActiveWorkspace(root, stored string) string {
	if strings.TrimSpace(stored) == "" {
		return root
	}
	if resolved, err := ResolveActiveWorkspacePath(root, stored); err == nil {
		return resolved
	}
	return root
}

func activeWorkspacePathWithin(root, candidate string) (bool, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false, err
	}
	candidateAbs, err := filepath.Abs(candidate)
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(rootAbs, candidateAbs)
	if err != nil {
		return false, err
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))), nil
}

type sessionWorkspaceToolResponse struct {
	SessionID      string `json:"session_id"`
	WorkspaceRoot  string `json:"workspace_root"`
	WorkspacePath  string `json:"workspace_path"`
	WorkspaceLabel string `json:"workspace_label,omitempty"`
	Changed        bool   `json:"changed,omitempty"`
	Summary        string `json:"summary"`
}

func SessionWorkspaceTool(state *ActiveWorkspaceState) *Tool {
	schema := json.RawMessage(`{
		"type": "object",
		"additionalProperties": false,
		"required": ["action"],
		"properties": {
			"action": {"type": "string", "enum": ["inspect", "set", "reset"], "description": "inspect returns the current active workspace; set changes it to an existing directory under the workspace root; reset returns to the root."},
			"path": {"type": "string", "maxLength": 2048, "description": "Workspace-root-relative or absolute directory path. Required for action=set."}
		}
	}`)
	return &Tool{
		Name:        SessionWorkspaceToolName,
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
			if state == nil {
				return "", errors.New("workspace state is not configured")
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
				WorkspaceLabel: activeWorkspaceLabel(current),
				Changed:        current != before,
				Summary:        fmt.Sprintf("active workspace is %s", activeWorkspaceLabel(current)),
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

func activeWorkspaceLabel(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if base := filepath.Base(path); base != "." && base != string(filepath.Separator) {
		return base
	}
	return path
}
