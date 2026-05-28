package executor

import (
	"fmt"
	"path/filepath"
	"strings"
)

func resolveWorkingDir(defaultCwd, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return defaultCwd, nil
	}
	if filepath.IsAbs(requested) || defaultCwd == "" {
		return filepath.Clean(requested), nil
	}
	clean := filepath.Clean(requested)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("working directory %q escapes default workspace %q", requested, defaultCwd)
	}
	if clean == "." {
		return defaultCwd, nil
	}
	return filepath.Join(defaultCwd, clean), nil
}
