package mcp

import (
	"context"
	"errors"

	"github.com/rs/zerolog"
)

// wire is the lower half of the MCP client: a transport that ships raw
// JSON-RPC bytes one direction (sendRequest / sendNotification) and
// surfaces server-originated frames the other direction (replies).
//
// The upper half (Client in client.go) handles the JSON-RPC state
// machine — id allocation, response correlation, the initialize
// handshake — and is transport-agnostic.
type wire interface {
	// sendRequest writes one JSON-RPC request to the server. The reply
	// (matched by id) arrives on replies().
	sendRequest(ctx context.Context, raw []byte) error
	// sendNotification writes a fire-and-forget JSON-RPC notification
	// (no id, no reply expected).
	sendNotification(ctx context.Context, raw []byte) error
	// replies emits every server-originated frame: responses and
	// notifications, encoded as the raw JSON line. Closed when the
	// transport is torn down.
	replies() <-chan []byte
	// close releases the transport. Idempotent.
	close() error
}

// startWire dispatches on ServerSpec to the right transport. URL ==
// streamable-http, Command == stdio, neither is an error.
func startWire(ctx context.Context, spec ServerSpec, log zerolog.Logger) (wire, error) {
	switch {
	case spec.URL != "" && spec.Command != "":
		return nil, errors.New("mcp: ServerSpec has both URL and Command; pick one transport")
	case spec.URL != "":
		return newHTTPWire(ctx, spec, log)
	case spec.Command != "":
		return newStdioWire(ctx, spec, log)
	default:
		return nil, errors.New("mcp: ServerSpec needs URL (streamable-http) or Command (stdio)")
	}
}
