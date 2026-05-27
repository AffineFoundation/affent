import type { UseAsDraft } from "../view/draftSource";
import type { SessionRunCommand, SessionRunView } from "../view/sessionRun";
import { CopyButton } from "./CopyButton";

export function SessionRunPanel({
  run,
  defaultOpen = false,
  onOpenArtifact,
  onUseAsDraft,
}: {
  run: SessionRunView;
  defaultOpen?: boolean;
  onOpenArtifact?: (path: string) => void;
  onUseAsDraft?: UseAsDraft;
}) {
  return (
    <details className="session-skills-panel session-run-panel" data-testid="session-run-panel" open={defaultOpen}>
      <summary className="session-skills-summary">
        <span className="session-skills-kicker">Run</span>
        <strong>{run.summary}</strong>
        <span>{run.detail}</span>
      </summary>
      <div className="session-skills-body">
        {run.commands.length > 0 ? (
          <ol className="session-run-list" data-testid="session-run-list">
            {run.commands.map((command, index) => (
              <li key={`${command.turnNumber}:${index}:${command.command}`} className="session-run-item" data-status={command.status}>
                <div className="session-run-main">
                  <strong title={command.command}>{command.command}</strong>
                  <span>{commandMeta(command)}</span>
                  {command.detail ? <small>{command.detail}</small> : null}
                  {command.next ? <small>Next: {command.next}</small> : null}
                  {command.artifactPath ? <small>Output artifact: {command.artifactPath}</small> : null}
                </div>
                <span className="session-evidence-actions">
                  <CopyButton label="Copy command" value={command.command} className="ghost-action" />
                  {command.artifactPath && onOpenArtifact ? (
                    <button type="button" className="ghost-action" onClick={() => onOpenArtifact(command.artifactPath ?? "")}>
                      Open output
                    </button>
                  ) : null}
                  {onUseAsDraft ? (
                    <button type="button" className="ghost-action" onClick={() => onUseAsDraft(runDraft(command), "run_command")}>
                      Rerun
                    </button>
                  ) : null}
                </span>
              </li>
            ))}
          </ol>
        ) : (
          <div className="session-skills-empty">No shell commands in this chat.</div>
        )}
      </div>
    </details>
  );
}

function commandMeta(command: SessionRunCommand): string {
  const parts = [
    statusLabel(command.status),
    command.exitCode != null ? `exit ${command.exitCode}` : undefined,
    command.durationMs != null ? formatDuration(command.durationMs) : undefined,
    `turn ${command.turnNumber}`,
  ].filter(Boolean);
  return parts.join(" · ");
}

function statusLabel(status: SessionRunCommand["status"]): string {
  if (status === "running") return "running";
  if (status === "failed") return "failed";
  return "passed";
}

function runDraft(command: SessionRunCommand): string {
  const next = command.next ? `\nUse this recovery hint: ${command.next}` : "";
  return `Rerun this command and report the result:\n${command.command}${next}`;
}

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  const s = ms / 1000;
  return `${s.toFixed(s < 10 ? 2 : 1)}s`;
}
