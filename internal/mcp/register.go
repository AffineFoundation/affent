package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/rs/zerolog"
)

const maxInputSchemaBytes = 32 * 1024

// RegisterServer launches the server, fetches its tool catalog, and
// adds each tool to the agent.Registry. By default tools are advertised
// under a "<server-name>_<tool>" name so multiple servers can coexist
// without clashing; ServerSpec.Namespace can opt a server into its raw
// MCP tool names for model compatibility.
//
// Returns the live Client + the affent tool names that were
// registered so a higher-level caller (typically RegisterAll) can
// roll them out of the Registry if a later server's startup fails.
// On any failure inside RegisterServer itself the partial state is
// rolled back internally: the Client is closed and no tools are
// added.
func RegisterServer(ctx context.Context, reg *agent.Registry, spec ServerSpec, log zerolog.Logger) (*Client, []string, error) {
	return registerServer(ctx, reg, spec, log, nil)
}

func registerServer(ctx context.Context, reg *agent.Registry, spec ServerSpec, log zerolog.Logger, owners map[string]string) (*Client, []string, error) {
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
		affentName := advertisedToolName(spec, t.Name)
		if prior, ok := owners[affentName]; ok {
			_ = c.Close()
			for _, n := range names {
				reg.Remove(n)
			}
			return nil, nil, fmt.Errorf("mcp tool name collision: %q registered by both %q and %q", affentName, prior, spec.Name)
		}
		if _, ok := reg.Get(affentName); ok {
			_ = c.Close()
			for _, n := range names {
				reg.Remove(n)
			}
			return nil, nil, fmt.Errorf("mcp tool name collision: %q from server %q is already registered", affentName, spec.Name)
		}
		schema, err := normalizedInputSchema(spec.Name, t.Name, t.InputSchema)
		if err != nil {
			_ = c.Close()
			for _, n := range names {
				reg.Remove(n)
			}
			return nil, nil, err
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
		if owners != nil {
			owners[affentName] = spec.Name
		}
	}
	log.Info().
		Str("mcp", spec.Name).
		Int("tools", len(tools)).
		Msg("mcp tools registered")
	return c, names, nil
}

func normalizedInputSchema(serverName, toolName string, schema json.RawMessage) (json.RawMessage, error) {
	// agent.Tool requires a non-empty JSON Schema object. MCP servers
	// occasionally omit inputSchema for argument-less tools.
	if len(schema) == 0 {
		return json.RawMessage(`{"type":"object","properties":{}}`), nil
	}
	if len(schema) > maxInputSchemaBytes {
		return nil, fmt.Errorf("mcp tool %s/%s inputSchema is %d bytes; max %d", serverName, toolName, len(schema), maxInputSchemaBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(schema))
	var obj map[string]json.RawMessage
	if err := dec.Decode(&obj); err != nil {
		return nil, fmt.Errorf("mcp tool %s/%s inputSchema is not valid JSON: %w", serverName, toolName, err)
	}
	if obj == nil {
		return nil, fmt.Errorf("mcp tool %s/%s inputSchema must be a JSON object", serverName, toolName)
	}
	var extra any
	if err := dec.Decode(&extra); err != nil {
		if err == io.EOF {
			return schema, nil
		}
		return nil, fmt.Errorf("mcp tool %s/%s inputSchema has trailing JSON: %w", serverName, toolName, err)
	}
	return nil, fmt.Errorf("mcp tool %s/%s inputSchema has trailing JSON", serverName, toolName)
}

func namespaceEnabled(spec ServerSpec) bool {
	return spec.Namespace == nil || *spec.Namespace
}

func advertisedToolName(spec ServerSpec, toolName string) string {
	if namespaceEnabled(spec) {
		return spec.Name + "_" + toolName
	}
	return toolName
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
	owners := make(map[string]string)
	for _, s := range specs {
		c, names, err := registerServer(ctx, reg, s, log, owners)
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
