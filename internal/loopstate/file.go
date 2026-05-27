package loopstate

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/affinefoundation/affent/internal/textutil"
)

const (
	ProtocolFileName = "LOOP.md"
	MaxProtocolBytes = 64 * 1024
)

type Summary struct {
	Path         string `json:"path,omitempty"`
	LoopID       string `json:"loop_id,omitempty"`
	OwnerSession string `json:"owner_session,omitempty"`
	Status       string `json:"status,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
	Bytes        int    `json:"bytes"`
	Preview      string `json:"preview,omitempty"`
}

func ProtocolDir(sessionDir, loopID string) string {
	return filepath.Join(sessionDir, ".affent", "loops", loopID)
}

func ProtocolPath(sessionDir, loopID string) string {
	return filepath.Join(ProtocolDir(sessionDir, loopID), ProtocolFileName)
}

func ProtocolRelPath(loopID string) string {
	return filepath.ToSlash(filepath.Join(".affent", "loops", loopID, ProtocolFileName))
}

func ReadProtocol(path string) (string, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	if info.IsDir() {
		return "", false, errors.New("loop protocol path is a directory")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", false, errors.New("loop protocol path must not be a symlink")
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, MaxProtocolBytes+1))
	if err != nil {
		return "", false, err
	}
	if len(raw) > MaxProtocolBytes {
		return "", false, fmt.Errorf("loop protocol file exceeds %d bytes", MaxProtocolBytes)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "", false, nil
	}
	return string(raw), true, nil
}

func WriteProtocol(path, content string) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return errors.New("loop protocol content is required")
	}
	if len([]byte(content)) > MaxProtocolBytes {
		return fmt.Errorf("loop protocol file exceeds %d bytes", MaxProtocolBytes)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.IsDir() {
			return errors.New("loop protocol path is a directory")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("loop protocol path must not be a symlink")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp := path + ".tmp"
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write([]byte(content + "\n")); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func RemoveProtocol(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.IsDir() {
		return false, errors.New("loop protocol path is a directory")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("loop protocol path must not be a symlink")
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return true, nil
}

func SummarizeFile(path, relPath string) (Summary, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Summary{}, false, nil
		}
		return Summary{}, false, err
	}
	content, found, err := ReadProtocol(path)
	if err != nil {
		return Summary{}, false, err
	}
	if !found {
		return Summary{}, false, nil
	}
	summary := Summary{
		Path:      relPath,
		UpdatedAt: formatTime(info.ModTime()),
		Bytes:     len([]byte(content)),
		Preview:   textutil.Preview(content, 240),
	}
	for _, line := range strings.Split(content, "\n") {
		key, value, ok := parseMetadataLine(line)
		if !ok {
			continue
		}
		switch key {
		case "loop_id":
			summary.LoopID = value
		case "owner_session":
			summary.OwnerSession = value
		case "status":
			summary.Status = value
		}
	}
	return summary, true, nil
}

func parseMetadataLine(line string) (string, string, bool) {
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
	key, value, ok := strings.Cut(line, ":")
	if !ok {
		return "", "", false
	}
	key = strings.ToLower(strings.TrimSpace(key))
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return "", "", false
	}
	return key, value, true
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
