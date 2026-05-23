package planstate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

const MaxFileBytes = 32 * 1024

func ReadFile(path string) (json.RawMessage, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if info.IsDir() {
		return nil, false, errors.New("plan path is a directory")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, false, errors.New("plan path must not be a symlink")
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer f.Close()

	raw, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return nil, false, err
	}
	if len(raw) > MaxFileBytes {
		return nil, false, fmt.Errorf("plan file exceeds %d bytes", MaxFileBytes)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, false, errors.New("plan file is empty")
	}
	if !json.Valid(raw) {
		return nil, false, errors.New("plan file is not valid JSON")
	}
	return json.RawMessage(raw), true, nil
}

func SummarizeFile(path string) (Summary, bool) {
	raw, found, err := ReadFile(path)
	if err != nil {
		return ErrorSummary(), true
	}
	if !found {
		return Summary{Label: LabelMissing}, false
	}
	summary, err := SummarizeJSON(raw)
	if err != nil {
		return ErrorSummary(), true
	}
	return summary, true
}

func RemoveFile(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if info.IsDir() {
		return false, errors.New("plan path is a directory")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("plan path must not be a symlink")
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
