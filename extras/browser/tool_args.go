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
		return browserInvalidArgsWrap(fmt.Errorf("decode args: %w", err), next)
	}
	var extra struct{}
	if err := dec.Decode(&extra); err != io.EOF {
		return browserInvalidArgs("decode args: multiple JSON values", next)
	}
	return nil
}

func browserInvalidArgs(message, next string) error {
	return fmt.Errorf("%s\nFailure: kind=invalid_args\nNext: %s", message, next)
}

func browserInvalidArgsWrap(err error, next string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w\nFailure: kind=invalid_args\nNext: %s", err, next)
}
