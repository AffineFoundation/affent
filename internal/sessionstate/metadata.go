package sessionstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	MetadataFileName      = "session.json"
	MetadataSchemaVersion = 1
)

type Metadata struct {
	SchemaVersion int    `json:"schema_version"`
	SessionID     string `json:"session_id"`
	WorkspaceRoot string `json:"workspace_root,omitempty"`
	WorkspacePath string `json:"workspace_path,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}

func ReadMetadata(sessionDir string) (Metadata, bool, error) {
	sessionDir = strings.TrimSpace(sessionDir)
	if sessionDir == "" {
		return Metadata{}, false, nil
	}
	path := filepath.Join(sessionDir, MetadataFileName)
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Metadata{}, false, nil
		}
		return Metadata{}, false, err
	}
	if info.IsDir() {
		return Metadata{}, true, fmt.Errorf("%s is a directory", MetadataFileName)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Metadata{}, true, fmt.Errorf("%s must not be a symlink", MetadataFileName)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Metadata{}, true, err
	}
	var meta Metadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return Metadata{}, true, err
	}
	return meta, true, nil
}

func WriteMetadata(sessionDir string, meta Metadata) error {
	sessionDir = strings.TrimSpace(sessionDir)
	if sessionDir == "" {
		return fmt.Errorf("session dir is required")
	}
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return err
	}
	meta.SchemaVersion = MetadataSchemaVersion
	if strings.TrimSpace(meta.UpdatedAt) == "" {
		meta.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	tmp, err := os.CreateTemp(sessionDir, "."+MetadataFileName+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, filepath.Join(sessionDir, MetadataFileName))
}
