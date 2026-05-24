package jsonl

import (
	"bufio"
	"errors"
	"io"
)

const DefaultMaxRecordBytes = 4 * 1024 * 1024

// ReadBoundedLine reads one JSONL physical line without allowing a single
// malformed record to grow memory past maxBytes. When the line exceeds maxBytes,
// it discards through the next newline and returns overLimit=true.
func ReadBoundedLine(r *bufio.Reader, maxBytes int) ([]byte, bool, error) {
	if maxBytes <= 0 {
		return nil, false, errors.New("max line bytes must be positive")
	}
	var line []byte
	overLimit := false
	for {
		frag, err := r.ReadSlice('\n')
		if len(frag) > 0 && !overLimit {
			if len(line)+len(frag) > maxBytes {
				line = nil
				overLimit = true
			} else {
				line = append(line, frag...)
			}
		}
		switch {
		case err == nil:
			return line, overLimit, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if len(frag) == 0 && len(line) == 0 && !overLimit {
				return nil, false, io.EOF
			}
			return line, overLimit, nil
		default:
			return nil, overLimit, err
		}
	}
}
