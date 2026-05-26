package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/textutil"
	"github.com/rs/zerolog"
)

const (
	maxInputSchemaBytes     = 32 * 1024
	maxToolNameBytes        = 64
	maxToolDescriptionBytes = 2048
)

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

	// On any failure after Start, close the client and unregister any
	// tools that were already added. The defer fires on every early
	// return; on success we set ok=true to skip cleanup.
	var names []string
	ok := false
	defer func() {
		if !ok {
			_ = c.Close()
			for _, n := range names {
				reg.Remove(n)
			}
		}
	}()

	tools, err := c.ListTools(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list tools on %s: %w", spec.Name, err)
	}
	tools, err = filterServerTools(spec, tools)
	if err != nil {
		return nil, nil, err
	}
	if len(tools) == 0 {
		return nil, nil, fmt.Errorf("mcp server %s exposes no usable tools after allow_tools/deny_tools filtering", spec.Name)
	}
	names = make([]string, 0, len(tools))
	for _, t := range tools {
		if strings.TrimSpace(t.Name) == "" {
			return nil, nil, fmt.Errorf("mcp server %s returned a tool with an empty name", spec.Name)
		}
		affentName := advertisedToolName(spec, t.Name)
		if err := validateAdvertisedToolName(affentName); err != nil {
			return nil, nil, fmt.Errorf("mcp tool %s/%s: %w", spec.Name, t.Name, err)
		}
		if prior, ok := owners[affentName]; ok {
			return nil, nil, fmt.Errorf("mcp tool name collision: %q registered by both %q and %q", affentName, prior, spec.Name)
		}
		if _, exists := reg.Get(affentName); exists {
			return nil, nil, fmt.Errorf("mcp tool name collision: %q from server %q is already registered", affentName, spec.Name)
		}
		schema, err := normalizedInputSchema(spec.Name, t.Name, t.InputSchema)
		if err != nil {
			return nil, nil, err
		}
		toolName := t.Name
		desc := normalizeToolDescription(t.Description)
		if desc == "" {
			desc = fmt.Sprintf("MCP tool %s/%s", spec.Name, t.Name)
		}
		reg.Add(&agent.Tool{
			Name:           affentName,
			Description:    desc,
			Schema:         schema,
			CatalogGroup:   "MCP",
			CatalogSource:  spec.Name,
			CatalogRawName: t.Name,
			Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
				res, err := c.CallTool(ctx, toolName, args)
				if err != nil {
					return "", err
				}
				if res.IsError {
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

	ok = true
	log.Info().
		Str("mcp", spec.Name).
		Int("tools", len(tools)).
		Msg("mcp tools registered")
	return c, names, nil
}

func filterServerTools(spec ServerSpec, tools []ToolDescriptor) ([]ToolDescriptor, error) {
	allow, err := normalizedToolNameSet("allow_tools", spec.ToolAllowlist)
	if err != nil {
		return nil, fmt.Errorf("mcp server %s: %w", spec.Name, err)
	}
	deny, err := normalizedToolNameSet("deny_tools", spec.ToolDenylist)
	if err != nil {
		return nil, fmt.Errorf("mcp server %s: %w", spec.Name, err)
	}
	for name := range allow {
		if deny[name] {
			return nil, fmt.Errorf("mcp server %s: tool %q appears in both allow_tools and deny_tools", spec.Name, name)
		}
	}
	exported := make(map[string]bool, len(tools))
	for _, t := range tools {
		exported[t.Name] = true
	}
	for name := range allow {
		if !exported[name] {
			return nil, fmt.Errorf("mcp server %s: allow_tools references unknown tool %q", spec.Name, name)
		}
	}
	for name := range deny {
		if !exported[name] {
			return nil, fmt.Errorf("mcp server %s: deny_tools references unknown tool %q", spec.Name, name)
		}
	}
	out := make([]ToolDescriptor, 0, len(tools))
	for _, t := range tools {
		if deny[t.Name] {
			continue
		}
		if len(allow) > 0 && !allow[t.Name] {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

func normalizedToolNameSet(field string, names []string) (map[string]bool, error) {
	out := make(map[string]bool, len(names))
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			return nil, fmt.Errorf("%s values must not be empty", field)
		}
		if out[name] {
			return nil, fmt.Errorf("%s contains duplicate tool %q", field, name)
		}
		out[name] = true
	}
	return out, nil
}

func validateAdvertisedToolName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("advertised tool name is required")
	}
	if len(name) > maxToolNameBytes {
		return fmt.Errorf("advertised tool name %q is %d bytes; max %d", name, len(name), maxToolNameBytes)
	}
	for _, r := range name {
		if r == '_' || r == '-' || ('A' <= r && r <= 'Z') || ('a' <= r && r <= 'z') || ('0' <= r && r <= '9') {
			continue
		}
		return fmt.Errorf("advertised tool name %q may contain only ASCII letters, digits, '_' or '-'", name)
	}
	return nil
}

func normalizeToolDescription(desc string) string {
	desc = strings.TrimSpace(desc)
	if len(desc) <= maxToolDescriptionBytes {
		return desc
	}
	const marker = "\n... [truncated]"
	limit := maxToolDescriptionBytes - len(marker)
	if limit < 0 {
		return desc[:textutil.AlignBackward(desc, maxToolDescriptionBytes)]
	}
	return desc[:textutil.AlignBackward(desc, limit)] + marker
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
