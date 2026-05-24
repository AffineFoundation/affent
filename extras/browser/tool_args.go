package browser

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

func decodeBrowserToolArgs(raw json.RawMessage, dst any, next string) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("decode args: %w\nNext: %s", err, next)
	}
	var extra struct{}
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("decode args: multiple JSON values\nNext: %s", next)
	}
	return nil
}
