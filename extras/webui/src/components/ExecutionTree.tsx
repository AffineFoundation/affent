import { useMemo, useState, type CSSProperties, type ReactNode } from "react";
import type { NormalizedEvent } from "../normalize/normalizeEvent";
import type { ToolCallState, TurnState } from "../store/sessionState";
import type { UseAsDraft } from "../view/draftSource";
import { buildExecutionTree, formatTokenUsageCompact, formatTokenUsageDetail, type ExecutionTreeNode } from "../view/executionTree";
import { CopyButton } from "./CopyButton";
import { fmtDuration } from "./format";
import { HighlightText } from "./HighlightText";
import { TraceDisclosure } from "./TraceDisclosure";

export function ExecutionTree({
  turn,
  events,
  searchQuery,
  sessionId,
  onOpenArtifact,
  onUseAsDraft,
}: {
  turn: TurnState;
  events: readonly NormalizedEvent[];
  searchQuery?: string;
  sessionId?: string;
  onOpenArtifact?: (path: string) => void;
  onUseAsDraft?: UseAsDraft;
}) {
  const nodes = useMemo(() => buildExecutionTree(turn), [turn]);
  const notableNodeIds = useMemo(() => collectNodeIds(nodes, isNotableNode), [nodes]);
  const openableNodeIds = useMemo(() => collectNodeIds(nodes, hasOpenableNodeBody), [nodes]);
  const activePath = useMemo(() => findRunningPath(nodes), [nodes]);
  const activePathIds = useMemo(() => new Set(activePath.map((node) => node.id)), [activePath]);
  const [openOverrides, setOpenOverrides] = useState<Record<string, boolean>>({});
  if (nodes.length === 0) return null;

  function setNodeOpen(nodeId: string, open: boolean) {
    setOpenOverrides((current) => ({ ...current, [nodeId]: open }));
  }

  function openNotableNodes() {
    setOpenOverrides((current) => {
      const next = { ...current };
      for (const id of notableNodeIds) next[id] = true;
      return next;
    });
  }

  function foldAllNodes() {
    setOpenOverrides((current) => {
      const next = { ...current };
      for (const id of openableNodeIds) next[id] = false;
      return next;
    });
  }

  return (
    <div className="execution-tree" data-testid="execution-tree">
      {activePath.length > 0 ? (
        <div className="execution-now" data-testid="execution-now">
          <span>Now</span>
          <strong title={activePathSummary(activePath)}>{activePathSummary(activePath)}</strong>
        </div>
      ) : null}
      {notableNodeIds.length > 0 ? (
        <div className="execution-tree-actions" data-testid="execution-tree-actions">
          <button type="button" onClick={openNotableNodes}>
            Show important
          </button>
          <button type="button" onClick={foldAllNodes}>
            Collapse details
          </button>
        </div>
      ) : null}
      {nodes.map((node) => (
        <ExecutionNodeView
          key={node.id}
          node={node}
          events={events.filter((ev) => node.callId && eventReferencesTool(ev, node.callId))}
          openOverrides={openOverrides}
          activeNodeIds={activePathIds}
          onOpenChange={setNodeOpen}
          searchQuery={searchQuery}
          sessionId={sessionId}
          onOpenArtifact={onOpenArtifact}
          onUseAsDraft={onUseAsDraft}
        />
      ))}
    </div>
  );
}

function ExecutionNodeView({
  node,
  events,
  openOverrides,
  activeNodeIds,
  onOpenChange,
  searchQuery,
  sessionId,
  onOpenArtifact,
  onUseAsDraft,
}: {
  node: ExecutionTreeNode;
  events: readonly NormalizedEvent[];
  openOverrides: Record<string, boolean>;
  activeNodeIds: ReadonlySet<string>;
  onOpenChange: (nodeId: string, open: boolean) => void;
  searchQuery?: string;
  sessionId?: string;
  onOpenArtifact?: (path: string) => void;
  onUseAsDraft?: UseAsDraft;
}) {
  const autoOpen = (isDelegation(node) && node.status === "running") || nodeSearchMatches(node, searchQuery);
  const open = openOverrides[node.id] ?? autoOpen;
  const hasBody = hasNodeBody(node) || node.children.length > 0 || events.length > 0;

  function toggleOpen() {
    onOpenChange(node.id, !open);
  }

  return (
    <div
      className="execution-node"
      data-kind={node.kind}
      data-status={node.status}
      data-depth={node.depth}
      data-active-path={activeNodeIds.has(node.id) ? "true" : "false"}
      data-testid="execution-node"
      style={{ "--depth": node.depth } as CSSProperties}
    >
      <button
        type="button"
        className="execution-row"
        aria-expanded={open}
        disabled={!hasBody}
        onClick={toggleOpen}
      >
        <span className="tree-rail" aria-hidden="true" />
        <span className="pulse-dot" data-status={node.status} aria-hidden="true" />
        <span className="node-copy">
          <span className="node-label"><HighlightText text={node.label} query={searchQuery} /></span>
          <span className="node-title"><HighlightText text={node.title} query={searchQuery} /></span>
          {node.subtitle ? (
            <span className="node-subtitle"><HighlightText text={node.subtitle} query={searchQuery} /></span>
          ) : null}
          {node.preview ? (
            <span className="node-preview"><HighlightText text={node.preview} query={searchQuery} /></span>
          ) : null}
          {node.status === "error" && node.nextHint ? (
            <span className="node-next" data-testid="node-next-hint">
              <b>Next</b> <HighlightText text={node.nextHint} query={searchQuery} />
            </span>
          ) : null}
        </span>
        <span className="node-meta">
          <StatusBadge node={node} />
          {node.kind === "mcp" ? <span className="badge" data-kind="mcp">mcp</span> : null}
          {node.argsTruncated || node.resultTruncated ? <span className="badge" data-kind="truncation">truncated</span> : null}
          {node.repairNotes?.length || node.originalTool ? <span className="badge" data-kind="repair">repaired</span> : null}
          {node.resultArtifactPath ? <span className="badge" data-kind="artifact">artifact</span> : null}
          {node.tokenUsage ? <span className="tool-meta">{formatTokenUsageCompact(node.tokenUsage)}</span> : null}
          {node.durationMs != null ? <span className="tool-meta">{fmtDuration(node.durationMs)}</span> : null}
        </span>
        <span className="node-chevron" aria-hidden="true">
          {hasBody ? "⌄" : ""}
        </span>
      </button>
      {open && hasBody ? (
        <div className="execution-body">
          <NodeDetails
            node={node}
            events={events}
            searchQuery={searchQuery}
            sessionId={sessionId}
            onOpenArtifact={onOpenArtifact}
            onUseAsDraft={onUseAsDraft}
          />
          {node.children.length > 0 ? (
            <div className="execution-children">
              {node.children.map((child) => (
                <ExecutionNodeView
                  key={child.id}
                  node={child}
                  events={[]}
                  openOverrides={openOverrides}
                  activeNodeIds={activeNodeIds}
                  onOpenChange={onOpenChange}
                  searchQuery={searchQuery}
                  sessionId={sessionId}
                  onOpenArtifact={onOpenArtifact}
                  onUseAsDraft={onUseAsDraft}
                />
              ))}
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

function StatusBadge({ node }: { node: ExecutionTreeNode }) {
  if (node.status === "running") return <span className="badge" data-kind="live">running</span>;
  if (node.status === "error") {
    return <span className="badge" data-kind="error">{node.exitCode == null ? "failed" : `exit ${node.exitCode}`}</span>;
  }
  return <span className="badge" data-kind="success">done</span>;
}

function findRunningPath(nodes: readonly ExecutionTreeNode[]): ExecutionTreeNode[] {
  for (const node of nodes) {
    const childPath = findRunningPath(node.children);
    if (node.status === "running" || childPath.length > 0) return [node, ...childPath];
  }
  return [];
}

function activePathSummary(nodes: readonly ExecutionTreeNode[]): string {
  return nodes
    .map((node) => node.title || node.label || node.tool)
    .filter(Boolean)
    .map((label) => summarize(label, 48))
    .join(" / ");
}

function NodeDetails({
  node,
  events,
  searchQuery,
  sessionId,
  onOpenArtifact,
  onUseAsDraft,
}: {
  node: ExecutionTreeNode;
  events: readonly NormalizedEvent[];
  searchQuery?: string;
  sessionId?: string;
  onOpenArtifact?: (path: string) => void;
  onUseAsDraft?: UseAsDraft;
}) {
  const primaryResult = node.report ?? node.summary ?? node.resultSummary ?? node.resultText;
  const nextHint = node.nextHint;
  const hasOutcome =
    !!nextHint ||
    !!node.summary ||
    !!node.report ||
    !!(node.resultSummary && !node.summary && !node.report) ||
    !!(node.resultText && node.resultText !== node.resultSummary && !hasStructuredResult(node)) ||
    node.findings.length > 0 ||
    node.warnings.length > 0 ||
    node.notFound.length > 0 ||
    node.suggestedNext.length > 0;
  const hasMetadata =
    !!node.objective ||
    !!node.tool ||
    !!node.childSessionId ||
    !!node.turnEndReason ||
    !!node.mcpServer ||
    !!node.mcpTool ||
    !!node.originalTool ||
    !!node.originalArgsSummary ||
    !!node.repairNotes?.length ||
    node.metrics.length > 0 ||
    !!node.resultArtifactPath;
  const hasPayload = !!node.args || events.length > 0;
  const hasRepairComparison = !!(node.originalTool || node.originalArgsSummary || node.repairNotes?.length);
  return (
    <div className="node-details" data-testid="tool-details">
      <ActionInspectorSummary node={node} searchQuery={searchQuery} />
      <div className="node-actions" aria-label="Tool actions">
        <CopyButton label="Copy action record" value={actionRecordText(node, primaryResult)} />
        {node.args ? <CopyButton label="Copy input" value={JSON.stringify(node.args, null, 2)} /> : null}
        {primaryResult ? <CopyButton label="Copy output" value={primaryResult} /> : null}
        {primaryResult && onUseAsDraft ? (
          <button type="button" className="node-action" onClick={() => onUseAsDraft(resultDraft(node, primaryResult), "tool_result")}>
            Use output
          </button>
        ) : null}
        {node.status === "error" && onUseAsDraft ? (
          <button type="button" className="node-action" onClick={() => onUseAsDraft(retryDraft(node), "retry")}>
            Retry action
          </button>
        ) : null}
        {node.resultArtifactPath && sessionId ? (
          <button type="button" className="node-action" onClick={() => onOpenArtifact?.(node.resultArtifactPath ?? "")}>
            Open artifact
          </button>
        ) : null}
      </div>
      {hasOutcome ? (
        <DetailSection title="Output">
          {nextHint ? (
            <div className="next-hint" data-testid="next-hint">
              <span>
                <b>Next</b> <HighlightText text={nextHint} query={searchQuery} />
              </span>
              {onUseAsDraft ? (
                <button type="button" className="node-action" onClick={() => onUseAsDraft(`Continue: ${nextHint}`, "tool_guidance")}>
                  Use as message
                </button>
              ) : null}
            </div>
          ) : null}
          {node.summary ? <TextBlock label="summary" text={node.summary} searchQuery={searchQuery} /> : null}
          {node.report ? <TextBlock label="agent summary" text={node.report} searchQuery={searchQuery} /> : null}
          {node.findings.length ? <FindingsList findings={node.findings} /> : null}
          {node.warnings.length ? <StringList label="warnings" items={node.warnings} /> : null}
          {node.notFound.length ? <StringList label="not found" items={node.notFound} /> : null}
          {node.suggestedNext.length ? <StringList label="suggested next" items={node.suggestedNext} /> : null}
          {node.resultSummary && !node.summary && !node.report ? <TextBlock label="output summary" text={node.resultSummary} searchQuery={searchQuery} /> : null}
          {node.resultText && node.resultText !== node.resultSummary && !hasStructuredResult(node) ? (
              <TextBlock
                label="output"
                text={node.resultText}
                meta={formatBytes(node.resultBytes, node.resultOmittedBytes, node.resultCapBytes, node.resultTruncated)}
                searchQuery={searchQuery}
              />
          ) : null}
        </DetailSection>
      ) : null}
      {hasRepairComparison ? (
        <DetailSection title="Repair comparison">
          <div className="repair-compare" data-testid="repair-comparison">
            <div className="repair-pane">
              <div className="kv">
                <b>model request</b>
              </div>
              {node.originalTool ? <DetailLine label="tool" value={node.originalTool} mono searchQuery={searchQuery} /> : null}
              {node.originalArgsSummary ? (
                <pre className="code compact-code"><HighlightText text={node.originalArgsSummary} query={searchQuery} /></pre>
              ) : (
                <div className="kv">No original input summary was captured.</div>
              )}
            </div>
            <div className="repair-pane">
              <div className="kv">
                <b>executed request</b>
              </div>
              <DetailLine label="tool" value={node.tool} mono searchQuery={searchQuery} />
              {node.args ? <pre className="code compact-code">{JSON.stringify(node.args, null, 2)}</pre> : null}
            </div>
          </div>
          {node.repairNotes?.length ? <StringList label="repair notes" items={node.repairNotes} /> : null}
        </DetailSection>
      ) : null}
      {hasMetadata ? (
        <DetailSection title="Action details">
          {node.objective ? <DetailLine label={node.kind === "focused_task" ? "objective" : "task"} value={node.objective} searchQuery={searchQuery} /> : null}
          <DetailLine label="action" value={node.tool} mono searchQuery={searchQuery} />
          {node.childSessionId ? <DetailLine label="child session" value={node.childSessionId} mono /> : null}
          {node.turnEndReason ? <DetailLine label="turn end" value={node.turnEndReason} /> : null}
          {node.mcpServer ? <DetailLine label="mcp server" value={node.mcpServer} mono /> : null}
          {node.mcpTool ? <DetailLine label="MCP action" value={node.mcpTool} mono /> : null}
          {node.metrics.length ? (
            <div className="node-metrics">
              {node.metrics.map((metric) => (
                <span key={`${metric.label}-${metric.value}`}>
                  {metric.label} <b>{metric.value}</b>
                </span>
              ))}
            </div>
          ) : null}
          {node.resultArtifactPath ? <DetailLine label="artifact" value={node.resultArtifactPath} mono /> : null}
        </DetailSection>
      ) : null}
      {hasPayload ? (
        <DetailSection
          title="Technical details"
          summary={events.length ? `${events.length} log ${events.length === 1 ? "entry" : "entries"}` : "input"}
          collapsible
          defaultOpen={!!searchQuery?.trim()}
        >
          {node.args ? (
            <>
              <DetailLine label="input" value={formatBytes(node.argsBytes, node.argsOmittedBytes, node.argsCapBytes, node.argsTruncated)} />
              <pre className="code">{JSON.stringify(node.args, null, 2)}</pre>
            </>
          ) : null}
          {events.length ? (
            <TraceDisclosure events={events} className="nested-raw" />
          ) : null}
        </DetailSection>
      ) : null}
    </div>
  );
}

function ActionInspectorSummary({ node, searchQuery }: { node: ExecutionTreeNode; searchQuery?: string }) {
  const items = compactSummaryItems([
    { label: "Status", value: statusLabel(node), tone: node.status },
    node.durationMs != null ? { label: "Duration", value: fmtDuration(node.durationMs) } : undefined,
    node.exitCode != null ? { label: "Exit", value: String(node.exitCode), tone: node.exitCode === 0 ? "success" : "error" } : undefined,
    node.tokenUsage ? { label: "Usage", value: formatTokenUsageDetail(node.tokenUsage) } : undefined,
    node.tokenUsage?.costUsd != null ? { label: "Cost", value: formatCost(node.tokenUsage.costUsd) } : undefined,
    node.resultArtifactPath ? { label: "File", value: "full output", tone: "artifact" } : undefined,
    node.repairNotes?.length || node.originalTool ? { label: "Repair", value: `${node.repairNotes?.length || 1} note${(node.repairNotes?.length || 1) === 1 ? "" : "s"}`, tone: "warning" } : undefined,
    node.resultTruncated || node.argsTruncated ? { label: "Limit", value: "truncated", tone: "warning" } : undefined,
  ]);

  return (
    <section className="action-inspector-summary" data-testid="action-inspector-summary">
      <div className="action-inspector-main">
        <span>{node.label}</span>
        <strong title={node.title}>
          <HighlightText text={node.title} query={searchQuery} />
        </strong>
        {node.subtitle ? <code><HighlightText text={node.subtitle} query={searchQuery} /></code> : null}
      </div>
      <div className="action-inspector-facts" aria-label="Action summary">
        {items.map((item) => (
          <span key={`${item.label}-${item.value}`} data-tone={item.tone}>
            <b>{item.label}</b> {item.value}
          </span>
        ))}
      </div>
    </section>
  );
}

function formatCost(value: number): string {
  if (value === 0) return "$0";
  if (value < 0.01) return `$${value.toFixed(4)}`;
  return `$${value.toFixed(2)}`;
}

type SummaryTone = "success" | "error" | "running" | "warning" | "artifact";

interface SummaryItem {
  label: string;
  value: string;
  tone?: SummaryTone;
}

function compactSummaryItems(items: Array<SummaryItem | undefined>): SummaryItem[] {
  return items.filter((item): item is SummaryItem => !!item);
}

function DetailSection({
  title,
  summary,
  collapsible,
  defaultOpen,
  children,
}: {
  title: string;
  summary?: string;
  collapsible?: boolean;
  defaultOpen?: boolean;
  children: ReactNode;
}) {
  if (collapsible) {
    return (
      <details className="detail-section detail-section-collapsible" open={defaultOpen}>
        <summary>
          <h4>{title}</h4>
          {summary ? <span>{summary}</span> : null}
        </summary>
        <div className="detail-section-body">{children}</div>
      </details>
    );
  }
  return (
    <section className="detail-section">
      <h4>{title}</h4>
      <div className="detail-section-body">{children}</div>
    </section>
  );
}

function DetailLine({ label, value, mono, searchQuery }: { label: string; value?: string; mono?: boolean; searchQuery?: string }) {
  if (!value) return null;
  return (
    <div className="kv">
      <b>{label}</b> {mono ? <code><HighlightText text={value} query={searchQuery} /></code> : <HighlightText text={value} query={searchQuery} />}
    </div>
  );
}

function statusLabel(node: ExecutionTreeNode): string {
  if (node.status === "running") return "running";
  if (node.status === "error") return "failed";
  return "done";
}

function actionRecordText(node: ExecutionTreeNode, primaryResult?: string): string {
  const lines = [
    `Action: ${node.title}`,
    `Kind: ${node.label}`,
    `Tool: ${node.tool}`,
    `Status: ${statusLabel(node)}`,
  ];
  if (node.durationMs != null) lines.push(`Duration: ${fmtDuration(node.durationMs)}`);
  if (node.exitCode != null) lines.push(`Exit: ${node.exitCode}`);
  if (node.objective) lines.push(`Task: ${node.objective}`);
  if (node.mcpServer) lines.push(`MCP server: ${node.mcpServer}`);
  if (node.mcpTool) lines.push(`MCP action: ${node.mcpTool}`);
  if (node.resultArtifactPath) lines.push(`Artifact: ${node.resultArtifactPath}`);
  if (node.nextHint) lines.push(`Next: ${node.nextHint}`);
  if (node.args) lines.push(`Input:\n${JSON.stringify(node.args, null, 2)}`);
  if (primaryResult) lines.push(`Output:\n${summarize(primaryResult, 2000)}`);
  return lines.join("\n");
}

function retryDraft(node: ExecutionTreeNode): string {
  const parts = [
    `Retry the failed action: ${node.title}`,
    `Tool: ${node.tool}`,
  ];
  if (node.nextHint) parts.push(`Next: ${node.nextHint}`);
  if (node.args) parts.push(`Args:\n${JSON.stringify(node.args, null, 2)}`);
  return parts.join("\n");
}

function resultDraft(node: ExecutionTreeNode, result: string): string {
  return [
    "Use this output in the next step:",
    `Action: ${node.title}`,
    `Tool: ${node.tool}`,
    `Output:\n${summarize(result, 2000)}`,
  ].join("\n");
}

function summarize(text: string, limit: number): string {
  const trimmed = text.trim();
  if (trimmed.length <= limit) return trimmed;
  return `${trimmed.slice(0, Math.max(0, limit - 3)).trimEnd()}...`;
}

function TextBlock({ label, text, meta, searchQuery }: { label: string; text: string; meta?: string; searchQuery?: string }) {
  return (
    <div className="text-block">
      <div className="kv">
        <b>{label}</b>
        {meta ? ` ${meta}` : ""}
      </div>
      <pre className="code"><HighlightText text={text} query={searchQuery} /></pre>
    </div>
  );
}

function FindingsList({ findings }: { findings: ExecutionTreeNode["findings"] }) {
  return (
    <div className="finding-list">
      <div className="kv">
        <b>findings</b>
      </div>
      {findings.map((finding, idx) => (
        <div key={`${finding.title}-${idx}`} className="finding-item">
          <b>{finding.title}</b>
          {finding.detail ? <span>{finding.detail}</span> : null}
          <small>
            {[finding.source, finding.confidence, finding.severity].filter(Boolean).join(" · ")}
          </small>
        </div>
      ))}
    </div>
  );
}

function StringList({ label, items }: { label: string; items: string[] }) {
  return (
    <div className="string-list">
      <div className="kv">
        <b>{label}</b>
      </div>
      <ul>
        {items.map((item) => (
          <li key={item}>{item}</li>
        ))}
      </ul>
    </div>
  );
}

function formatBytes(bytes?: number, omitted?: number, cap?: number, truncated?: boolean): string {
  const parts: string[] = [];
  if (bytes != null) parts.push(`${bytes} bytes`);
  if (cap != null) parts.push(`cap ${cap}`);
  if (truncated && omitted != null) parts.push(`${omitted} omitted`);
  return parts.length ? `(${parts.join(", ")})` : "";
}

function hasNodeBody(node: ExecutionTreeNode): boolean {
  return !!(
    node.args ||
    node.resultSummary ||
    node.resultText ||
    node.summary ||
    node.report ||
    node.objective ||
    node.childSessionId ||
    node.turnEndReason ||
    node.mcpServer ||
    node.originalTool ||
    node.originalArgsSummary ||
    node.repairNotes?.length ||
    node.metrics.length ||
    node.findings.length ||
    node.warnings.length ||
    node.notFound.length ||
    node.suggestedNext.length ||
    node.resultArtifactPath
  );
}

function hasOpenableNodeBody(node: ExecutionTreeNode): boolean {
  return hasNodeBody(node) || node.children.length > 0;
}

function nodeSearchMatches(node: ExecutionTreeNode, searchQuery?: string): boolean {
  const query = normalizeSearch(searchQuery);
  if (!query) return false;
  const chunks = [
    node.label,
    node.title,
    node.subtitle,
    node.preview,
    node.tool,
    node.originalTool,
    node.originalArgsSummary,
    node.resultSummary,
    node.resultText,
    node.nextHint,
    node.resultArtifactPath,
    node.summary,
    node.report,
    node.objective,
    node.childSessionId,
    node.turnEndReason,
    node.mcpServer,
    node.mcpTool,
    JSON.stringify(node.args),
    ...(node.repairNotes ?? []),
    ...node.findings.flatMap((finding) => [
      finding.title,
      finding.detail,
      finding.source,
      finding.confidence,
      finding.severity,
    ]),
    ...node.warnings,
    ...node.notFound,
    ...node.suggestedNext,
    ...node.metrics.flatMap((metric) => [metric.label, metric.value]),
  ];
  const selfMatches = normalizeSearch(chunks.filter(Boolean).join("\n")).includes(query);
  return selfMatches || node.children.some((child) => nodeSearchMatches(child, searchQuery));
}

function normalizeSearch(value?: string): string {
  return value?.trim().toLowerCase() ?? "";
}

function isNotableNode(node: ExecutionTreeNode): boolean {
  return !!(
    node.status === "error" ||
    node.status === "running" ||
    node.argsTruncated ||
    node.resultTruncated ||
    node.resultArtifactPath ||
    node.repairNotes?.length ||
    node.originalTool
  );
}

function collectNodeIds(nodes: readonly ExecutionTreeNode[], predicate: (node: ExecutionTreeNode) => boolean): string[] {
  const ids: string[] = [];
  for (const node of nodes) {
    if (predicate(node) && hasOpenableNodeBody(node)) ids.push(node.id);
    ids.push(...collectNodeIds(node.children, predicate));
  }
  return ids;
}

function isDelegation(node: ExecutionTreeNode): boolean {
  return node.kind === "subagent" || node.kind === "focused_task";
}

function hasStructuredResult(node: ExecutionTreeNode): boolean {
  return !!(node.summary || node.report || node.findings.length || node.warnings.length || node.notFound.length || node.suggestedNext.length);
}

function eventReferencesTool(event: NormalizedEvent, callId: ToolCallState["callId"]): boolean {
  return !!event.data && typeof event.data === "object" && (event.data as { call_id?: unknown }).call_id === callId;
}
