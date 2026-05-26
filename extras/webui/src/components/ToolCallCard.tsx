import { useState } from "react";
import type { NormalizedEvent } from "../normalize/normalizeEvent";
import type { ToolCallState } from "../store/sessionState";
import { fmtDuration } from "./format";
import { artifactDisplayLabel, artifactName } from "../view/turnArtifacts";
import { describeMemoryUpdate, type MemoryUpdateSummary } from "../view/memoryUpdate";
import { describeSourceAccess, sourceEvidenceLabel, type SourceAccessInfo } from "../view/sourceAccess";
import { TraceDisclosure } from "./TraceDisclosure";

// Tool steps stay compact in the flow and expand in place. Status, repair,
// truncation and artifact are structured fields — never parsed from marker
// strings.
export function ToolCallCard({
  call,
  events,
}: {
  call: ToolCallState;
  events: readonly NormalizedEvent[];
}) {
  const [open, setOpen] = useState(false);
  const memoryUpdate = describeMemoryUpdate(call);
  const sourceAccess = describeSourceAccess(call.result ?? call.resultSummary);
  const failureKinds = callFailureKinds(call);

  return (
    <div className="flow-tool" data-status={call.status} data-testid="tool-card">
      <button
        type="button"
        className="tool-summary"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
      >
        <span className="pulse-dot" data-status={call.status} aria-hidden="true" />
        <span className="tool-name">{call.tool}</span>
        {memoryUpdate ? <MemoryUpdateInline summary={memoryUpdate} /> : null}
        <span className="grow" />
        {memoryUpdate ? (
          <span className="badge" data-kind="memory">memory</span>
        ) : null}
        {call.argsRepaired || call.canonicalized ? (
          <span className="badge" data-kind="repair">repaired</span>
        ) : null}
        {call.resultTruncated ? (
          <span className="badge" data-kind="truncation">truncated</span>
        ) : null}
        {call.resultArtifactPath ? (
          <span className="badge" data-kind="artifact">artifact</span>
        ) : null}
        {sourceAccess ? (
          <span className="badge" data-kind={sourceAccess.status === "verified" || sourceAccess.status === "network" ? "schema" : "warning"}>{sourceEvidenceLabel(sourceAccess)}</span>
        ) : null}
        {failureKinds.slice(0, 2).map((kind) => (
          <span className="badge" data-kind="error" key={kind}>{kind}</span>
        ))}
        {call.status === "error" ? (
          <span className="badge" data-kind="error">exit {call.exitCode}</span>
        ) : null}
        {call.durationMs != null ? <span className="tool-meta">{fmtDuration(call.durationMs)}</span> : null}
      </button>
      {open ? <ToolDetails call={call} events={events} memoryUpdate={memoryUpdate} sourceAccess={sourceAccess} /> : null}
    </div>
  );
}

function MemoryUpdateInline({ summary }: { summary: MemoryUpdateSummary }) {
  return (
    <span className="memory-update-inline">
      <b>{summary.label}</b>
      <span>{summary.location}</span>
      <span>{summary.preview}</span>
    </span>
  );
}

function ToolDetails({
  call,
  events,
  memoryUpdate,
  sourceAccess,
}: {
  call: ToolCallState;
  events: readonly NormalizedEvent[];
  memoryUpdate?: MemoryUpdateSummary;
  sourceAccess?: SourceAccessInfo;
}) {
  const hasResult = call.result != null && call.result !== "";
  const failureKinds = callFailureKinds(call);
  return (
    <div className="tool-details" data-testid="tool-details">
      {memoryUpdate ? <MemoryUpdateDetails summary={memoryUpdate} /> : null}
      {call.canonicalized && call.originalTool ? (
        <div className="kv">
          <b>renamed</b> <code>{call.originalTool}</code> → <code>{call.tool}</code>
        </div>
      ) : null}
      {call.repairNotes?.length ? (
        <div className="kv">
          <b>repairs</b> {call.repairNotes.join("; ")}
        </div>
      ) : null}
      {failureKinds.length > 0 ? (
        <div className="kv">
          <b>failure</b> {failureKinds.join(", ")}
        </div>
      ) : null}
      {sourceAccess ? <SourceAccessDetails info={sourceAccess} /> : null}
      <div className="kv">
        <b>input</b>
        {call.argsTruncated ? " (truncated)" : ""}
      </div>
      <pre className="code">{JSON.stringify(call.args, null, 2)}</pre>
      {hasResult ? (
        <>
          <div className="kv">
            <b>output</b>
            {call.resultTruncated ? " (truncated)" : ""}
            {typeof call.exitCode === "number" ? ` · exit ${call.exitCode}` : ""}
          </div>
          <pre className="code">{call.resultSummary ?? call.result}</pre>
        </>
      ) : null}
      {call.resultArtifactPath ? (
        <div className="kv">
          <b>full output</b> <code>{artifactSummary(call)}</code>
        </div>
      ) : null}
      <TraceDisclosure events={events} className="nested-raw" />
    </div>
  );
}

function SourceAccessDetails({ info }: { info: SourceAccessInfo }) {
  return (
    <div className="kv">
      <b>source</b> {sourceEvidenceLabel(info)} · <code>{info.accessedUrl}</code>
      {info.requestedUrl ? <> · requested <code>{info.requestedUrl}</code></> : null}
    </div>
  );
}

function MemoryUpdateDetails({ summary }: { summary: MemoryUpdateSummary }) {
  return (
    <div className="memory-update-card" data-testid="memory-update-card">
      <div>
        <b>{summary.label}</b>
        <code>{summary.location}</code>
      </div>
      <p>{summary.preview}</p>
    </div>
  );
}

function callFailureKinds(call: ToolCallState): string[] {
  const seen = new Set<string>();
  return [...(call.failureKinds ?? []), call.failureKind].filter((kind): kind is string => {
    if (!kind || seen.has(kind)) return false;
    seen.add(kind);
    return true;
  });
}

function artifactSummary(call: ToolCallState): string {
  if (!call.resultArtifactPath) return "";
  return artifactDisplayLabel({
    path: call.resultArtifactPath,
    name: artifactName(call.resultArtifactPath),
    source: "",
    summary: call.resultSummary ?? undefined,
    truncated: call.resultTruncated,
    bytes: call.resultBytes,
    omittedBytes: call.resultOmittedBytes,
    capBytes: call.resultCapBytes,
  });
}
