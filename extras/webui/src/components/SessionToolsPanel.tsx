import { useEffect, useMemo, useState } from "react";
import { CopyButton } from "./CopyButton";
import type { SessionToolInfo, SessionToolsSurfaceInfo } from "../api/sessions";

interface ToolGroupView {
  key: string;
  label: string;
  count: number;
  tools: SessionToolInfo[];
}

export function SessionToolsPanel({
  tools,
  loading = false,
  error,
  surface,
}: {
  tools?: readonly SessionToolInfo[];
  loading?: boolean;
  error?: string;
  surface?: SessionToolsSurfaceInfo;
}) {
  const [query, setQuery] = useState("");
  const [selectedGroup, setSelectedGroup] = useState("all");
  const [collapsedGroups, setCollapsedGroups] = useState<Set<string>>(() => new Set());
  const allTools = tools ?? [];
  const availableGroups = useMemo(() => groupTools(allTools), [allTools]);
  const filteredTools = useMemo(() => {
    const search = query.trim().toLowerCase();
    const selectedKey = selectedGroup;
    return allTools.filter((tool) => {
      if (selectedKey !== "all" && groupKeyForTool(tool) !== selectedKey) return false;
      if (search === "") return true;
      const haystack = [
        tool.group,
        tool.source,
        tool.raw_name,
        tool.name,
        tool.description,
        formatToolParameters(tool.parameters),
      ]
        .filter(Boolean)
        .join(" ")
        .toLowerCase();
      return haystack.includes(search);
    });
  }, [allTools, query, selectedGroup]);

  const groups = useMemo(() => groupTools(filteredTools), [filteredTools]);
  const searchActive = query.trim() !== "";
  const hasTools = (tools?.length ?? 0) > 0;
  const hasGroupControls = availableGroups.length > 1;
  const allGroupsCollapsed = hasGroupControls && groups.every((group) => collapsedGroups.has(group.key));
  const selectedGroupLabel = selectedGroup === "all" ? "All" : availableGroups.find((group) => group.key === selectedGroup)?.label ?? "All";
  const visibleCounts = useMemo(() => new Map(groups.map((group) => [group.key, group.count] as const)), [groups]);
  const catalogText = useMemo(() => formatToolCatalog(groups), [groups]);
  const summary = loading ? "Loading tools" : error ? "Tools unavailable" : hasTools ? `${tools?.length ?? 0} tools available` : "No tools available";
  const summaryDetail = loading
    ? "Fetching the current tool catalog."
    : error
      ? error
      : hasTools
        ? selectedGroup === "all"
          ? formatToolGroupSummary(groups)
          : groups.length === 0
            ? `${selectedGroupLabel} · no tools match this filter`
            : `${selectedGroupLabel} · ${groups.length} visible ${groups.length === 1 ? "group" : "groups"}`
        : "Inspect the tools the model can call.";

  useEffect(() => {
    if (selectedGroup === "all") return;
    if (availableGroups.some((group) => group.key === selectedGroup)) return;
    setSelectedGroup("all");
  }, [availableGroups, selectedGroup]);

  function toggleGroup(key: string) {
    if (searchActive) return;
    setCollapsedGroups((current) => {
      const next = new Set(current);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }

  function expandAllGroups() {
    if (searchActive) return;
    setCollapsedGroups(new Set());
  }

  function collapseAllGroups() {
    if (searchActive) return;
    setCollapsedGroups(new Set(groups.map((group) => group.key)));
  }

  function selectGroup(key: string) {
    setSelectedGroup(key);
  }

  return (
    <details className="session-tools-panel" data-testid="session-tools-panel">
      <summary className="session-tools-summary">
        <span className="session-tools-kicker">Tools</span>
        <strong>{summary}</strong>
        <span>{summaryDetail}</span>
      </summary>
      <div className="session-tools-body">
        {loading ? <div className="session-tools-empty">Loading current session tools...</div> : null}
        {!loading && error ? (
          <div className="session-tools-empty error" role="alert">
            {error}
          </div>
        ) : null}
        {!loading && !error ? (
          <>
            {surface ? (
              <section className="session-tools-surface" aria-label="Allow filter status">
                <div className="session-tools-surface-head">
                  <span className="session-tools-surface-kicker">Allow / filter status</span>
                  <strong>{surface.headline}</strong>
                  <span>{surface.detail}</span>
                </div>
                <div className="session-tools-surface-chips">
                  {surfaceDiagnosticChips(surface).map((chip) => (
                    <span key={`${chip.group}:${chip.label}`} className="session-tools-surface-chip" data-tone={chip.tone} title={chip.detail}>
                      <strong>{chip.group}</strong>
                      <span>{chip.label}</span>
                    </span>
                  ))}
                  {moreSurfaceChipCount(surface) > 0 ? <span className="session-tools-surface-more">+{moreSurfaceChipCount(surface)} more</span> : null}
                </div>
              </section>
            ) : null}
            {hasTools ? (
              <>
                <label className="session-tools-search">
                  <span>Search tools</span>
                  <div className="session-tools-search-row">
                    <input
                      value={query}
                      onChange={(event) => setQuery(event.target.value)}
                      placeholder="Search names, servers, or schema"
                      data-testid="session-tools-search"
                    />
                    {query.trim() ? (
                      <button type="button" className="session-tools-search-clear" onClick={() => setQuery("")}>
                        Clear
                      </button>
                    ) : null}
                  </div>
                </label>
                {hasGroupControls ? (
                  <div className="session-tools-filters" role="tablist" aria-label="Tool groups">
                    <button
                      type="button"
                      role="tab"
                      aria-selected={selectedGroup === "all"}
                      className="session-tools-filter"
                      onClick={() => selectGroup("all")}
                    >
                      <span>All</span>
                      <strong>{filteredTools.length}</strong>
                    </button>
                    {availableGroups.map((group) => (
                      <button
                        key={group.key}
                        type="button"
                        role="tab"
                        aria-selected={selectedGroup === group.key}
                        className="session-tools-filter"
                        onClick={() => selectGroup(group.key)}
                      >
                        <span>{group.label}</span>
                        <strong>{visibleCounts.get(group.key) ?? 0}</strong>
                      </button>
                    ))}
                  </div>
                ) : null}
                {hasGroupControls ? (
                  <div className="session-tools-actions">
                    <span className="session-tools-actions-label">
                      {selectedGroup === "all"
                        ? allGroupsCollapsed
                          ? "All groups collapsed"
                          : "All groups expanded"
                        : groups.length === 0
                          ? `${selectedGroupLabel} · no matching tools`
                          : `${selectedGroupLabel} · ${groups.length} visible ${groups.length === 1 ? "group" : "groups"}`}
                    </span>
                    <div className="session-tools-actions-buttons">
                      <CopyButton
                        label="Copy diagnostic"
                        value={formatToolDiagnostic(surface, groups, query, selectedGroup)}
                        className="session-tools-utility session-tools-copy"
                      />
                      <CopyButton
                        label="Copy filtered catalog"
                        value={catalogText}
                        className="session-tools-utility session-tools-copy"
                      />
                      <CopyButton
                        label="Copy names"
                        value={formatToolNames(groups)}
                        className="session-tools-utility session-tools-copy"
                      />
                      <button type="button" className="session-tools-utility" onClick={expandAllGroups} disabled={!allGroupsCollapsed}>
                        Expand all
                      </button>
                      <button type="button" className="session-tools-utility" onClick={collapseAllGroups} disabled={allGroupsCollapsed}>
                        Collapse all
                      </button>
                    </div>
                  </div>
                ) : null}
                <div className="session-tools-groups" data-testid="session-tools-list">
                  {groups.length > 0 ? (
                    groups.map((group) => (
                      <section key={group.key} className="session-tools-group" aria-label={group.label}>
                        <button
                          type="button"
                          className="session-tools-group-head"
                          aria-expanded={searchActive ? true : !collapsedGroups.has(group.key)}
                          disabled={searchActive}
                          onClick={() => toggleGroup(group.key)}
                        >
                          <span className="session-tools-group-title">
                            <strong>{group.label}</strong>
                            <span>
                              {group.count} {group.count === 1 ? "tool" : "tools"}
                            </span>
                          </span>
                          <span className="session-tools-group-toggle" aria-hidden="true">
                            {searchActive ? "Search matches" : collapsedGroups.has(group.key) ? "Expand" : "Collapse"}
                          </span>
                        </button>
                        {!collapsedGroups.has(group.key) || searchActive ? (
                          <div className="session-tools-list">
                            {group.tools.map((tool) => (
                              <details key={`${group.key}:${tool.name}`} className="session-tool-item">
                                <summary>
                                  <span className="session-tool-name" title={tool.name}>
                                    {tool.name}
                                  </span>
                                  <span className="session-tool-desc" title={tool.description || "No description"}>
                                    {tool.description || "No description"}
                                  </span>
                                </summary>
                                <div className="session-tool-item-body">
                                  <div className="session-tool-meta">
                                    {tool.source ? <span className="session-tool-source">{tool.source}</span> : null}
                                    {tool.raw_name && tool.raw_name !== tool.name ? <span className="session-tool-raw">Raw name: {tool.raw_name}</span> : null}
                                  </div>
                                  <div className="session-tool-stats">
                                    <span>{formatSchemaComplexity(tool.parameters)}</span>
                                    {tool.description ? <span>{formatDescriptionLength(tool.description)}</span> : null}
                                  </div>
                                  <div className="session-tool-actions">
                                    <CopyButton label="Copy name" value={tool.name} className="node-action" />
                                    {tool.raw_name && tool.raw_name !== tool.name ? (
                                      <CopyButton label="Copy raw name" value={tool.raw_name} className="node-action" />
                                    ) : null}
                                    <CopyButton label="Copy schema" value={formatToolParameters(tool.parameters)} className="node-action" />
                                  </div>
                                  <pre className="session-tool-schema">{formatToolParameters(tool.parameters)}</pre>
                                </div>
                              </details>
                            ))}
                          </div>
                        ) : null}
                      </section>
                    ))
                  ) : (
                    <div className="session-tools-empty">No matching tools.</div>
                  )}
                </div>
              </>
            ) : (
              <div className="session-tools-empty">No tools were advertised for this session.</div>
            )}
          </>
        ) : null}
      </div>
    </details>
  );
}

function groupTools(tools: readonly SessionToolInfo[]): ToolGroupView[] {
  const groups = new Map<string, ToolGroupView>();
  for (const tool of tools) {
    const key = groupKeyForTool(tool);
    const existing = groups.get(key);
    if (existing) {
      existing.tools.push(tool);
      existing.count += 1;
      continue;
    }
    groups.set(key, {
      key,
      label: key,
      count: 1,
      tools: [tool],
    });
  }
  return Array.from(groups.values());
}

function groupKeyForTool(tool: SessionToolInfo): string {
  return tool.group === "MCP" && tool.source ? `MCP · ${tool.source}` : tool.group || "Other";
}

function formatToolParameters(parameters: unknown): string {
  if (parameters == null) return "{}";
  if (typeof parameters === "string") return parameters;
  try {
    return JSON.stringify(parameters, null, 2);
  } catch {
    return String(parameters);
  }
}

function formatToolCatalog(groups: readonly ToolGroupView[]): string {
  if (groups.length === 0) return "";
  return ["Tool catalog", ...groups.flatMap((group) => formatToolGroup(group))].join("\n\n");
}

function formatToolNames(groups: readonly ToolGroupView[]): string {
  if (groups.length === 0) return "";
  return ["Tool names", ...groups.flatMap((group) => formatToolGroupNames(group))].join("\n");
}

function formatSchemaComplexity(parameters: unknown): string {
  const count = countSchemaFields(parameters);
  if (count <= 0) return "Schema simple";
  if (count === 1) return "Schema 1 field";
  return `Schema ${count} fields`;
}

function formatDescriptionLength(description: string): string {
  const count = description
    .trim()
    .split(/\s+/)
    .filter(Boolean).length;
  if (count <= 0) return "Description short";
  if (count === 1) return "Description 1 word";
  return `Description ${count} words`;
}

function countSchemaFields(parameters: unknown): number {
  if (!parameters || typeof parameters !== "object" || Array.isArray(parameters)) return 0;
  const obj = parameters as Record<string, unknown>;
  let count = 0;
  const props = obj.properties;
  if (props && typeof props === "object" && !Array.isArray(props)) {
    count += Object.keys(props).length;
    for (const value of Object.values(props)) {
      count += countSchemaFields(value);
    }
  }
  const items = obj.items;
  if (items && typeof items === "object" && !Array.isArray(items)) {
    count += countSchemaFields(items);
  }
  return count;
}

function surfaceStatusChip(view: SessionToolsSurfaceInfo) {
  if (view.status === "allowed") {
    return { group: "Surface", label: "Allowed", detail: "The visible tool list matches the current session allowlist.", tone: "ready" as const };
  }
  if (view.status === "filtered") {
    return { group: "Surface", label: "Filtered", detail: "Some tool groups are filtered by runtime capabilities.", tone: "warning" as const };
  }
  if (view.status === "restricted") {
    return { group: "Surface", label: "Restricted", detail: "Most tool groups are unavailable in this session.", tone: "warning" as const };
  }
  return { group: "Surface", label: "Unknown", detail: "Tool availability has not been confirmed for this chat.", tone: "unknown" as const };
}

function surfaceDiagnosticChips(surface: SessionToolsSurfaceInfo) {
  const chips = [surfaceStatusChip(surface)];
  for (const reason of surface.disabled_reasons?.slice(0, 2) ?? []) {
    chips.push({
      group: "Filter",
      label: reason.replace(/\.$/, ""),
      detail: reason,
      tone: "warning" as const,
    });
  }
  for (const warning of surface.warnings?.slice(0, 1) ?? []) {
    chips.push({
      group: "Doctor",
      label: warning.replace(/\.$/, ""),
      detail: warning,
      tone: "unknown" as const,
    });
  }
  return chips;
}

function moreSurfaceChipCount(surface: SessionToolsSurfaceInfo): number {
  const reasonCount = surface.disabled_reasons?.length ?? 0;
  const warningCount = surface.warnings?.length ?? 0;
  const total = 1 + Math.min(reasonCount, 2) + Math.min(warningCount, 1);
  return Math.max(0, total - 3);
}

function formatToolDiagnostic(
  surface: SessionToolsSurfaceInfo | undefined,
  groups: readonly ToolGroupView[],
  query: string,
  selectedGroup: string,
): string {
  const lines = ["Tool diagnostic"];
  if (surface) {
    lines.push(`Surface: ${surface.headline}`);
    lines.push(`Status: ${surface.status}`);
    lines.push(`Tone: ${surface.tone}`);
    lines.push(`Detail: ${surface.detail}`);
    if (surface.disabled_reasons && surface.disabled_reasons.length > 0) {
      lines.push("Disabled reasons:");
      for (const reason of surface.disabled_reasons) lines.push(`- ${reason}`);
    }
    if (surface.warnings && surface.warnings.length > 0) {
      lines.push("Warnings:");
      for (const warning of surface.warnings) lines.push(`- ${warning}`);
    }
  }
  lines.push(`Filter: ${selectedGroup === "all" ? "All tools" : selectedGroup}`);
  lines.push(`Search: ${query.trim() || "none"}`);
  lines.push(`Visible groups: ${groups.length}`);
  for (const group of groups.slice(0, 4)) {
    lines.push(`- ${group.label}: ${group.count} ${group.count === 1 ? "tool" : "tools"}`);
  }
  if (groups.length > 4) {
    lines.push(`+${groups.length - 4} more groups`);
  }
  return lines.join("\n");
}

function formatToolGroupSummary(groups: readonly ToolGroupView[]): string {
  if (groups.length === 0) return "Inspect the tools the model can call.";
  const visible = groups.slice(0, 3).map((group) => `${group.label} ${group.count}`);
  if (groups.length > 3) visible.push(`+${groups.length - 3} more`);
  return visible.join(" · ");
}

function formatToolGroup(group: ToolGroupView): string[] {
  const lines = [`${group.label} (${group.count} ${group.count === 1 ? "tool" : "tools"})`];
  for (const tool of group.tools) {
    const descriptor = tool.description || "No description";
    const rawName = tool.raw_name && tool.raw_name !== tool.name ? ` (raw: ${tool.raw_name})` : "";
    lines.push(`- ${tool.name}${rawName} — ${descriptor}`);
    lines.push(`  schema:`);
    lines.push(indentBlock(formatToolParameters(tool.parameters)));
  }
  return lines;
}

function formatToolGroupNames(group: ToolGroupView): string[] {
  const lines = [`${group.label} (${group.count})`];
  for (const tool of group.tools) {
    const rawName = tool.raw_name && tool.raw_name !== tool.name ? ` (raw: ${tool.raw_name})` : "";
    lines.push(`- ${tool.name}${rawName}`);
  }
  return lines;
}

function indentBlock(text: string, prefix = "  "): string {
  return text
    .split("\n")
    .map((line) => `${prefix}${line}`)
    .join("\n");
}
