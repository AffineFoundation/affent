package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/affinefoundation/affent"
	"github.com/rs/zerolog"
)

// RegisterServer launches the server, fetches its tool catalog, and
// adds each tool to the affent.Registry under a "<server-name>_<tool>"
// name so multiple servers can coexist without clashing.
//
// Returns the live Client so the caller can Close() it on shutdown.
// On any failure the partial state is rolled back: the Client is
// closed and no tools are added.
func RegisterServer(ctx context.Context, reg *affent.Registry, spec ServerSpec, log zerolog.Logger) (*Client, error) {
	c, err := Start(ctx, spec, log)
	if err != nil {
		return nil, err
	}
	tools, err := c.ListTools(ctx)
	if err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("list tools on %s: %w", spec.Name, err)
	}
	for _, t := range tools {
		affentName := spec.Name + "_" + t.Name
		schema := t.InputSchema
		// affent.Tool requires a non-empty JSON Schema object.
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		// Capture loop-iteration vars so the closure points at this
		// specific tool, not the last one.
		toolName := t.Name
		desc := t.Description
		if desc == "" {
			desc = fmt.Sprintf("MCP tool %s/%s", spec.Name, t.Name)
		}
		reg.Add(&affent.Tool{
			Name:        affentName,
			Description: desc,
			Schema:      schema,
			Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
				res, err := c.CallTool(ctx, toolName, args)
				if err != nil {
					return "", err
				}
				if res.IsError {
					// Surface as plain error text so the model can read
					// it and adjust. Loop's "Error:" prefix detection in
					// dispatch wraps non-error strings; we want this to
					// look like a tool error, so prepend explicitly.
					return "", fmt.Errorf("%s", res.Text)
				}
				return res.Text, nil
			},
		})
	}
	log.Info().
		Str("mcp", spec.Name).
		Int("tools", len(tools)).
		Msg("mcp tools registered")
	return c, nil
}

// RegisterAll spins up every spec, accumulating live clients. If any
// server fails to start, all already-started clients are closed and
// the error is returned. Log-only warnings for empty spec list.
func RegisterAll(ctx context.Context, reg *affent.Registry, specs []ServerSpec, log zerolog.Logger) ([]*Client, error) {
	var clients []*Client
	for _, s := range specs {
		c, err := RegisterServer(ctx, reg, s, log)
		if err != nil {
			for _, prev := range clients {
				_ = prev.Close()
			}
			return nil, err
		}
		clients = append(clients, c)
	}
	return clients, nil
}
