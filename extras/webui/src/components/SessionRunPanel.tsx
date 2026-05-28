import { useState, type FormEvent } from "react";
import type { UseAsDraft } from "../view/draftSource";
import { manualRunDraft, runCommandDraft, runCommandEvidenceText, runCommandMeta, type SessionRunView } from "../view/sessionRun";
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
  const [manualCommand, setManualCommand] = useState("");
  const [manualCwd, setManualCwd] = useState("");

  function handleManualSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const command = manualCommand.trim();
    if (!command || !onUseAsDraft) return;
    onUseAsDraft(manualRunDraft(command, manualCwd), "run_command");
  }

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
                  <span>{runCommandMeta(command)}</span>
                  {command.cwd ? <small title={command.cwd}>Cwd: {command.cwd}</small> : null}
                  {command.detail ? <small>{command.detail}</small> : null}
                  {command.next ? <small>Next: {command.next}</small> : null}
                  {command.artifactPath ? <small>Output artifact: {command.artifactPath}</small> : null}
                </div>
                <span className="session-evidence-actions">
                  <CopyButton label="Copy command" value={command.command} className="ghost-action" />
                  <CopyButton label="Copy run evidence" value={runCommandEvidenceText(command)} className="ghost-action" />
                  {command.artifactPath && onOpenArtifact ? (
                    <button type="button" className="ghost-action" onClick={() => onOpenArtifact(command.artifactPath ?? "")}>
                      Open command output
                    </button>
                  ) : null}
                  {onUseAsDraft ? (
                    <button type="button" className="ghost-action" onClick={() => onUseAsDraft(runCommandDraft(command), "run_command")}>
                      Rerun as draft
                    </button>
                  ) : null}
                </span>
              </li>
            ))}
          </ol>
        ) : (
          <div className="session-skills-empty">No shell commands in this chat.</div>
        )}
        {onUseAsDraft ? (
          <form className="session-run-manual" data-testid="session-run-manual" onSubmit={handleManualSubmit}>
            <div className="session-run-manual-head">
              <strong>Run command</strong>
              <span>Ask Affent</span>
            </div>
            <label>
              <span>Command</span>
              <input
                value={manualCommand}
                onChange={(event) => setManualCommand(event.target.value)}
                placeholder="npm test"
              />
            </label>
            <label>
              <span>Working directory</span>
              <input
                value={manualCwd}
                onChange={(event) => setManualCwd(event.target.value)}
                placeholder="session workspace"
              />
            </label>
            <button type="submit" className="ghost-action" disabled={!manualCommand.trim()}>
              Use command as draft
            </button>
          </form>
        ) : null}
      </div>
    </details>
  );
}
