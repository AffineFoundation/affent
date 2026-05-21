package mcp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
)

// stdioWire runs the server as a child process and shuttles JSON-RPC
// frames over its stdin/stdout, newline-delimited.
type stdioWire struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader

	out        chan []byte   // server-originated frames
	closeCh    chan struct{} // closed when close() begins; lets readLoop bail mid-send
	readerDone chan struct{} // closed when readLoop exits, gating close(out)

	closed  atomic.Bool
	writeMu sync.Mutex

	log zerolog.Logger
}

func newStdioWire(_ context.Context, spec ServerSpec, log zerolog.Logger) (wire, error) {
	cmd := exec.Command(spec.Command, spec.Args...)
	if spec.Cwd != "" {
		cmd.Dir = spec.Cwd
	}
	if len(spec.Env) > 0 {
		cmd.Env = append(os.Environ(), spec.Env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", spec.Command, err)
	}

	w := &stdioWire{
		cmd:        cmd,
		stdin:      stdin,
		stdout:     bufio.NewReaderSize(stdout, 64*1024),
		out:        make(chan []byte, 64),
		closeCh:    make(chan struct{}),
		readerDone: make(chan struct{}),
		log:        log,
	}
	go w.readLoop()
	go w.drainStderr(stderr)
	return w, nil
}

func (w *stdioWire) sendRequest(_ context.Context, raw []byte) error {
	return w.write(raw)
}

func (w *stdioWire) sendNotification(_ context.Context, raw []byte) error {
	return w.write(raw)
}

func (w *stdioWire) replies() <-chan []byte { return w.out }

func (w *stdioWire) close() error {
	if !w.closed.CompareAndSwap(false, true) {
		return nil
	}
	// Signal readLoop before closing stdin so any send it's currently
	// blocked on (slow client + full out) unblocks immediately. Without
	// this, the reader would sit on a chan send until the server's last
	// flush arrives and EOFs the pipe.
	close(w.closeCh)
	_ = w.stdin.Close()
	done := make(chan struct{})
	go func() {
		_ = w.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		_ = w.cmd.Process.Kill()
		<-done
	}
	// Wait for readLoop to fully exit before closing out, otherwise a
	// late `case w.out <- cp:` select branch could pick the send case
	// and panic on a closed chan.
	<-w.readerDone
	close(w.out)
	return nil
}

func (w *stdioWire) write(raw []byte) error {
	if w.closed.Load() {
		return errors.New("stdio wire closed")
	}
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	if _, err := w.stdin.Write(raw); err != nil {
		return err
	}
	_, err := w.stdin.Write([]byte("\n"))
	return err
}

func (w *stdioWire) readLoop() {
	defer close(w.readerDone)
	for {
		line, err := w.stdout.ReadBytes('\n')
		if len(line) > 0 {
			// Trim the trailing newline; downstream uses json.Unmarshal
			// which doesn't care, but cleaner for logs.
			if line[len(line)-1] == '\n' {
				line = line[:len(line)-1]
			}
			cp := make([]byte, len(line))
			copy(cp, line)
			// Block until the frame is delivered. Earlier code dropped
			// on a full buffer; that silently lost JSON-RPC responses
			// and hung any caller waiting on the matching id. The
			// closeCh escape lets us bail when the wire is shutting
			// down so we don't deadlock on close.
			select {
			case w.out <- cp:
			case <-w.closeCh:
				return
			}
		}
		if err != nil {
			if err != io.EOF {
				w.log.Debug().Err(err).Msg("mcp stdio read")
			}
			return
		}
	}
}

func (w *stdioWire) drainStderr(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 8*1024), 256*1024)
	for sc.Scan() {
		w.log.Debug().Str("stderr", sc.Text()).Msg("mcp stderr")
	}
}
