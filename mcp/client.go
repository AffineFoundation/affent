package mcp

import (
	"bufio"
	"context"
	"encoding/json"
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

// Client is one connection to a single MCP server. The server is run as
// a child process; we feed JSON-RPC over its stdin and read replies
// from stdout. Lines on stderr are logged but otherwise ignored.
type Client struct {
	spec ServerSpec
	log  zerolog.Logger

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader

	// JSON-RPC plumbing.
	nextID  atomic.Int64
	mu      sync.Mutex
	pending map[int64]chan rpcResponse
	closed  atomic.Bool
	writeMu sync.Mutex // serializes stdin writes
}

// Start launches the server and performs the initialize handshake. The
// returned client is ready for ListTools / CallTool. Caller must Close
// when done.
func Start(ctx context.Context, spec ServerSpec, log zerolog.Logger) (*Client, error) {
	if spec.Command == "" {
		return nil, errors.New("mcp: server command is empty")
	}
	if spec.Name == "" {
		return nil, errors.New("mcp: server name is empty")
	}

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

	c := &Client{
		spec:    spec,
		log:     log.With().Str("mcp", spec.Name).Logger(),
		cmd:     cmd,
		stdin:   stdin,
		stdout:  bufio.NewReaderSize(stdout, 64*1024),
		pending: make(map[int64]chan rpcResponse),
	}

	go c.readLoop()
	go c.drainStderr(stderr)

	// Initialize within a bounded window. Servers usually answer within
	// a few hundred ms; npx cold-start can hit several seconds, so 30s
	// is generous but not silly.
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := c.initialize(initCtx); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("initialize %s: %w", spec.Name, err)
	}
	return c, nil
}

// Close terminates the child process and rejects any in-flight calls.
// Idempotent.
func (c *Client) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	_ = c.stdin.Close()
	// Give the server ~1s to exit gracefully on stdin close, then kill.
	done := make(chan struct{})
	go func() {
		_ = c.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		_ = c.cmd.Process.Kill()
		<-done
	}
	c.mu.Lock()
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
	return nil
}

// Name returns the server's user-facing name.
func (c *Client) Name() string { return c.spec.Name }

// ListTools issues tools/list and returns the server's tool catalog.
func (c *Client) ListTools(ctx context.Context) ([]ToolDescriptor, error) {
	resp, err := c.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	var out toolsListResult
	if err := json.Unmarshal(resp, &out); err != nil {
		return nil, fmt.Errorf("decode tools/list: %w", err)
	}
	return out.Tools, nil
}

// CallTool invokes the named tool with the given JSON arguments. The
// returned text is the server's content blocks flattened into a single
// string; isError is true if the server reported an application-level
// failure (the call itself succeeded but the tool said no).
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (CallResult, error) {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	params, err := json.Marshal(toolsCallParams{Name: name, Arguments: args})
	if err != nil {
		return CallResult{}, err
	}
	resp, err := c.call(ctx, "tools/call", params)
	if err != nil {
		return CallResult{}, err
	}
	var out toolsCallResult
	if err := json.Unmarshal(resp, &out); err != nil {
		return CallResult{}, fmt.Errorf("decode tools/call: %w", err)
	}
	return CallResult{
		Text:    flattenContent(out.Content),
		IsError: out.IsError,
	}, nil
}

// initialize is the required handshake before any other method is
// callable. Per spec: the client sends initialize, the server replies,
// then the client sends a notifications/initialized notification.
func (c *Client) initialize(ctx context.Context) error {
	params, _ := json.Marshal(initializeParams{
		// Picking a recent published protocol version. Servers that
		// support an older or newer one usually negotiate down without
		// erroring; if a server is strict we'll find out and bump.
		ProtocolVersion: "2025-06-18",
		Capabilities:    map[string]any{},
		ClientInfo: clientInfo{
			Name:    "affent",
			Version: "0.1",
		},
	})
	resp, err := c.call(ctx, "initialize", params)
	if err != nil {
		return err
	}
	var ir initializeResult
	if err := json.Unmarshal(resp, &ir); err != nil {
		return fmt.Errorf("decode initialize: %w", err)
	}
	c.log.Info().
		Str("server_name", ir.ServerInfo.Name).
		Str("server_version", ir.ServerInfo.Version).
		Str("protocol", ir.ProtocolVersion).
		Msg("mcp initialized")
	return c.notify("notifications/initialized", nil)
}

// call sends a request and waits for the matching response. Honors
// ctx cancellation; on cancel we leak the pending entry until the
// server eventually replies (or the client closes).
func (c *Client) call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	if c.closed.Load() {
		return nil, errors.New("mcp client closed")
	}
	id := c.nextID.Add(1)
	ch := make(chan rpcResponse, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := c.write(rpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp, ok := <-ch:
		if !ok {
			return nil, errors.New("mcp client closed before response")
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("mcp %s: %s (code %d)", method, resp.Error.Message, resp.Error.Code)
		}
		return resp.Result, nil
	}
}

func (c *Client) notify(method string, params json.RawMessage) error {
	return c.write(rpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	})
}

func (c *Client) write(msg rpcRequest) error {
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.stdin.Write(raw); err != nil {
		return err
	}
	_, err = c.stdin.Write([]byte("\n"))
	return err
}

// readLoop pumps incoming JSON-RPC messages into pending channels.
// Notifications (no id) are logged at debug and dropped.
func (c *Client) readLoop() {
	for {
		line, err := c.stdout.ReadBytes('\n')
		if len(line) > 0 {
			c.dispatch(line)
		}
		if err != nil {
			if err != io.EOF {
				c.log.Debug().Err(err).Msg("mcp read")
			}
			c.shutdownPending()
			return
		}
	}
}

func (c *Client) dispatch(line []byte) {
	var resp rpcResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		c.log.Debug().Err(err).Bytes("line", line).Msg("mcp parse")
		return
	}
	if resp.ID == nil {
		// Notification (e.g. notifications/message). Ignore for now;
		// future iteration can expose progress events to the loop.
		return
	}
	id, ok := normalizeID(resp.ID)
	if !ok {
		c.log.Debug().Any("id", resp.ID).Msg("mcp non-int id; dropping")
		return
	}
	c.mu.Lock()
	ch, found := c.pending[id]
	c.mu.Unlock()
	if !found {
		c.log.Debug().Int64("id", id).Msg("mcp no pending caller")
		return
	}
	select {
	case ch <- resp:
	default:
	}
}

func (c *Client) shutdownPending() {
	c.mu.Lock()
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
}

func (c *Client) drainStderr(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 8*1024), 256*1024)
	for sc.Scan() {
		c.log.Debug().Str("stderr", sc.Text()).Msg("mcp stderr")
	}
}

// normalizeID coerces JSON id values (number or string) to int64.
// We only ever issue integer ids ourselves, but a polite server might
// echo them as float64 (after a json round-trip).
func normalizeID(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	}
	return 0, false
}

// flattenContent picks all text blocks and concatenates them. Other
// block kinds get a marker so the model knows something non-textual
// was returned (binary blobs, images, resource refs).
func flattenContent(blocks []contentBlock) string {
	var buf []byte
	for i, b := range blocks {
		if i > 0 {
			buf = append(buf, '\n')
		}
		switch b.Type {
		case "text":
			buf = append(buf, b.Text...)
		case "image":
			buf = append(buf, fmt.Sprintf("[image %s, %d bytes (omitted)]", b.MimeType, len(b.Data))...)
		case "resource":
			buf = append(buf, fmt.Sprintf("[resource ref: %s]", string(b.Resource))...)
		default:
			buf = append(buf, fmt.Sprintf("[content type=%q]", b.Type)...)
		}
	}
	return string(buf)
}
