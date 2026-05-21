package executor

import "fmt"

// DefaultExecOutputCap caps the per-stream output an Exec call buffers
// when ExecOptions.MaxOutputBytes is left zero. Picked so even a
// runaway `find /` or `cat /dev/urandom` can't push the executor
// toward OOM while still leaving plenty of room for normal tool
// output (Go test runs, build logs, etc.).
const DefaultExecOutputCap = 1 * 1024 * 1024

// truncationMarker is a stable substring of the banner cappingWriter
// appends to a truncated stream. Used so producer and detector
// (e.g. docker.go's readFileFull) can agree on a single contract.
const truncationMarker = "more bytes truncated"

// cappingWriter is the io.Writer the Exec implementations wrap around
// stdout/stderr. The first `cap` bytes are buffered verbatim; further
// writes are accepted (so the child process can keep streaming
// without back-pressuring on a blocked pipe) but discarded. String()
// renders the captured prefix and a truncation marker when bytes were
// dropped.
type cappingWriter struct {
	buf       []byte
	cap       int
	dropped   int64
	truncated bool
}

func newCappingWriter(cap int) *cappingWriter {
	if cap <= 0 {
		cap = DefaultExecOutputCap
	}
	return &cappingWriter{cap: cap}
}

// Write captures up to cap bytes and silently drops the rest. Never
// returns an error: the contract is "I'll buffer what I can; assume
// success" so the child process drains its pipes and exits.
func (w *cappingWriter) Write(p []byte) (int, error) {
	room := w.cap - len(w.buf)
	switch {
	case room <= 0:
		w.dropped += int64(len(p))
		w.truncated = true
	case len(p) > room:
		w.buf = append(w.buf, p[:room]...)
		w.dropped += int64(len(p) - room)
		w.truncated = true
	default:
		w.buf = append(w.buf, p...)
	}
	return len(p), nil
}

func (w *cappingWriter) String() string {
	if !w.truncated {
		return string(w.buf)
	}
	return string(w.buf) + fmt.Sprintf("\n[... %d %s; %d-byte stream cap. Re-run piping through head/tail/grep, or save to a file and read in chunks.]", w.dropped, truncationMarker, w.cap)
}
