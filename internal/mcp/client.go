package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/affinefoundation/affent/internal/textutil"
	"github.com/rs/zerolog"
)

const (
	maxMCPToolResultBytes        = 256 * 1024
	mcpToolResultTruncatedMarker = "\n... [truncated; MCP tool result cap reached]"
)

// Client is one connection to a single MCP server. The transport (stdio
// or streamable-http) is hidden behind the wire interface — Client only
// owns the JSON-RPC state machine.
type Client struct {
	spec ServerSpec
	log  zerolog.Logger

	w wire

	nextID  atomic.Int64
	mu      sync.Mutex
	pending map[int64]chan rpcResponse
	closed  atomic.Bool
}

// Start dials/launches the server and performs the initialize handshake.
// The returned client is ready for ListTools / CallTool. Caller must
// Close when done.
func Start(ctx context.Context, spec ServerSpec, log zerolog.Logger) (*Client, error) {
	if spec.Name == "" {
		return nil, errors.New("mcp: server name is empty")
	}
	w, err := startWire(ctx, spec, log.With().Str("mcp", spec.Name).Logger())
	if err != nil {
		return nil, err
	}
	c := &Client{
		spec:    spec,
		log:     log.With().Str("mcp", spec.Name).Logger(),
		w:       w,
		pending: make(map[int64]chan rpcResponse),
	}
	go c.readLoop()

	initCtx, cancel := context.WithTimeout(ctx, resolveInitTimeout(spec))
	defer cancel()
	if err := c.initialize(initCtx); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("initialize %s: %w", spec.Name, err)
	}
	return c, nil
}

func resolveInitTimeout(spec ServerSpec) time.Duration {
	if spec.InitTimeout > 0 {
		return spec.InitTimeout
	}
	return DefaultInitTimeout
}

// Close terminates the transport and rejects any in-flight calls.
// Idempotent.
func (c *Client) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	_ = c.w.close()
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

// initialize is the required handshake: client → server `initialize`
// request, server replies, client sends `notifications/initialized`.
func (c *Client) initialize(ctx context.Context) error {
	params, _ := json.Marshal(initializeParams{
		ProtocolVersion: ProtocolVersion,
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
	return c.notify(ctx, "notifications/initialized", nil)
}

// call sends a request and waits for the matching response. Honors ctx
// cancellation: on ctx.Done the defer below removes the pending entry
// before returning, so a late response from the server arrives at
// dispatch and is dropped (the comment in dispatch's "no pending
// caller" path covers that). No leak.
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

	raw, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return nil, err
	}
	if err := c.w.sendRequest(ctx, raw); err != nil {
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

func (c *Client) notify(ctx context.Context, method string, params json.RawMessage) error {
	raw, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return err
	}
	return c.w.sendNotification(ctx, raw)
}

// readLoop consumes server-originated frames from the wire, routes
// responses back to call() via pending channels, and drops notifications
// at debug level (the v0 client doesn't surface progress yet).
func (c *Client) readLoop() {
	for line := range c.w.replies() {
		c.dispatch(line)
	}
	c.shutdownPending()
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
	// Keep the lock held across the send. Close/shutdownPending also
	// hold the lock while they close the per-call channels, so taking
	// it here makes the look-up-and-deliver pair atomic with respect
	// to a teardown — without it, dispatch could capture ch, release
	// the lock, lose the race to Close which closes ch, then panic
	// (send-on-closed) inside the select. The send is non-blocking
	// (ch is buffered to 1; default fires if it's full), so holding
	// the lock for the duration is cheap.
	c.mu.Lock()
	defer c.mu.Unlock()
	ch, found := c.pending[id]
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

// normalizeID coerces JSON id values (number or string) to int64. We
// only ever issue integer ids ourselves, but a polite server might echo
// them as float64 (after a json round-trip).
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
// block kinds get a marker so the model knows something non-textual was
// returned.
func flattenContent(blocks []contentBlock) string {
	var buf strings.Builder
	contentLimit := maxMCPToolResultBytes - len(mcpToolResultTruncatedMarker)
	if contentLimit < 0 {
		contentLimit = maxMCPToolResultBytes
	}
	truncated := false
	appendPart := func(part string) {
		if truncated || part == "" {
			return
		}
		remaining := contentLimit - buf.Len()
		if remaining <= 0 {
			truncated = true
			return
		}
		if len(part) <= remaining {
			buf.WriteString(part)
			return
		}
		cut := textutil.AlignBackward(part, remaining)
		buf.WriteString(part[:cut])
		truncated = true
	}
	for i, b := range blocks {
		if i > 0 {
			appendPart("\n")
		}
		switch b.Type {
		case "text":
			appendPart(b.Text)
		case "image":
			appendPart(fmt.Sprintf("[image %s, %d bytes (omitted)]", b.MimeType, len(b.Data)))
		case "resource":
			appendPart(fmt.Sprintf("[resource ref: %s]", string(b.Resource)))
		default:
			appendPart(fmt.Sprintf("[content type=%q]", b.Type))
		}
	}
	if truncated && buf.Len()+len(mcpToolResultTruncatedMarker) <= maxMCPToolResultBytes {
		buf.WriteString(mcpToolResultTruncatedMarker)
	}
	return buf.String()
}
