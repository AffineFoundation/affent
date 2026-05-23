package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/zerolog"
)

// ToolGovernanceReport is the MCP tool surface after Affent's governance
// rules have been applied. Doctor uses this to show what the model will see
// without duplicating registration logic outside the mcp package.
type ToolGovernanceReport struct {
	ServerName        string
	NamespaceEnabled  bool
	RawToolCount      int
	FilteredToolCount int
	AcceptedTools     []GovernedTool
	RejectedTools     []RejectedTool
}

type GovernedTool struct {
	RawName        string
	AdvertisedName string
}

type RejectedTool struct {
	RawName string
	Reason  string
}

// DiagnoseServerTools starts one MCP server, lists its tools, and evaluates
// the same allow/deny, namespace, schema, and collision rules used by
// RegisterServer. owners is updated for accepted advertised names so callers
// diagnosing multiple servers can detect cross-server collisions.
func DiagnoseServerTools(ctx context.Context, spec ServerSpec, owners map[string]string, log zerolog.Logger) (ToolGovernanceReport, error) {
	report := ToolGovernanceReport{
		ServerName:       spec.Name,
		NamespaceEnabled: namespaceEnabled(spec),
	}
	c, err := Start(ctx, spec, log)
	if err != nil {
		return report, err
	}
	defer c.Close()

	tools, err := c.ListTools(ctx)
	if err != nil {
		return report, fmt.Errorf("list tools on %s: %w", spec.Name, err)
	}
	report.RawToolCount = len(tools)

	filtered, err := filterServerTools(spec, tools)
	if err != nil {
		return report, err
	}
	acceptedRaw := make(map[string]bool, len(filtered))
	for _, t := range filtered {
		acceptedRaw[t.Name] = true
	}

	allow, err := normalizedToolNameSet("allow_tools", spec.ToolAllowlist)
	if err != nil {
		return report, fmt.Errorf("mcp server %s: %w", spec.Name, err)
	}
	deny, err := normalizedToolNameSet("deny_tools", spec.ToolDenylist)
	if err != nil {
		return report, fmt.Errorf("mcp server %s: %w", spec.Name, err)
	}
	for _, t := range tools {
		rawName := t.Name
		switch {
		case strings.TrimSpace(rawName) == "":
			return report, fmt.Errorf("mcp server %s returned a tool with an empty name", spec.Name)
		case acceptedRaw[rawName]:
			continue
		case deny[rawName]:
			report.FilteredToolCount++
			report.RejectedTools = append(report.RejectedTools, RejectedTool{RawName: rawName, Reason: "deny_tools"})
		case len(allow) > 0 && !allow[rawName]:
			report.FilteredToolCount++
			report.RejectedTools = append(report.RejectedTools, RejectedTool{RawName: rawName, Reason: "not in allow_tools"})
		}
	}

	for _, t := range filtered {
		rawName := t.Name
		advertisedName := advertisedToolName(spec, rawName)
		if err := validateAdvertisedToolName(advertisedName); err != nil {
			return report, fmt.Errorf("mcp tool %s/%s: %w", spec.Name, rawName, err)
		}
		if prior, ok := owners[advertisedName]; ok {
			return report, fmt.Errorf("mcp tool name collision: %q registered by both %q and %q", advertisedName, prior, spec.Name)
		}
		if _, err := normalizedInputSchema(spec.Name, rawName, t.InputSchema); err != nil {
			return report, err
		}
		report.AcceptedTools = append(report.AcceptedTools, GovernedTool{
			RawName:        rawName,
			AdvertisedName: advertisedName,
		})
		if owners != nil {
			owners[advertisedName] = spec.Name
		}
	}
	if len(report.AcceptedTools) == 0 {
		return report, fmt.Errorf("mcp server %s exposes no usable tools after allow_tools/deny_tools filtering", spec.Name)
	}
	return report, nil
}
