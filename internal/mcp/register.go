package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/rs/zerolog"
)

// RegisterServer launches the server, fetches its tool catalog, and
// adds each tool to the agent.Registry under a "<server-name>_<tool>"
// name so multiple servers can coexist without clashing.
//
// Returns the live Client + the affent tool names that were
// registered so a higher-level caller (typically RegisterAll) can
// roll them out of the Registry if a later server's startup fails.
// On any failure inside RegisterServer itself the partial state is
// rolled back internally: the Client is closed and no tools are
// added.
func RegisterServer(ctx context.Context, reg *agent.Registry, spec ServerSpec, log zerolog.Logger) (*Client, []string, error) {
	c, err := Start(ctx, spec, log)
	if err != nil {
		return nil, nil, err
	}
	tools, err := c.ListTools(ctx)
	if err != nil {
		_ = c.Close()
		return nil, nil, fmt.Errorf("list tools on %s: %w", spec.Name, err)
	}
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		affentName := spec.Name + "_" + t.Name
		schema := t.InputSchema
		// agent.Tool requires a non-empty JSON Schema object.
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
		reg.Add(&agent.Tool{
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
		names = append(names, affentName)
	}
	log.Info().
		Str("mcp", spec.Name).
		Int("tools", len(tools)).
		Msg("mcp tools registered")
	return c, names, nil
}

// RegisterAll spins up every spec, accumulating live clients. If any
// server fails to start, every already-started client is closed AND
// its tools are unregistered from reg before the error is returned —
// so the caller doesn't end up with a Registry whose entries point
// at backends that have already been torn down.
func RegisterAll(ctx context.Context, reg *agent.Registry, specs []ServerSpec, log zerolog.Logger) ([]*Client, error) {
	type registered struct {
		client *Client
		names  []string
	}
	var done []registered
	for _, s := range specs {
		c, names, err := RegisterServer(ctx, reg, s, log)
		if err != nil {
			for _, r := range done {
				_ = r.client.Close()
				for _, n := range r.names {
					reg.Remove(n)
				}
			}
			return nil, err
		}
		done = append(done, registered{c, names})
	}
	clients := make([]*Client, len(done))
	for i, r := range done {
		clients[i] = r.client
	}
	return clients, nil
}
