import { useState } from "react";
import type { NormalizedEvent } from "../normalize/normalizeEvent";
import type { ToolCallState } from "../store/sessionState";
import { fmtDuration } from "./format";
import { artifactDisplayLabel, artifactName } from "../view/turnArtifacts";
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
        <span className="grow" />
        {call.argsRepaired || call.canonicalized ? (
          <span className="badge" data-kind="repair">repaired</span>
        ) : null}
        {call.resultTruncated ? (
          <span className="badge" data-kind="truncation">truncated</span>
        ) : null}
        {call.resultArtifactPath ? (
          <span className="badge" data-kind="artifact">artifact</span>
        ) : null}
        {call.status === "error" ? (
          <span className="badge" data-kind="error">exit {call.exitCode}</span>
        ) : null}
        {call.durationMs != null ? <span className="tool-meta">{fmtDuration(call.durationMs)}</span> : null}
      </button>
      {open ? <ToolDetails call={call} events={events} /> : null}
    </div>
  );
}

function ToolDetails({ call, events }: { call: ToolCallState; events: readonly NormalizedEvent[] }) {
  const hasResult = call.result != null && call.result !== "";
  return (
    <div className="tool-details" data-testid="tool-details">
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
