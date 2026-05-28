import { useState, type FormEvent } from "react";
import type { UseAsDraft } from "../view/draftSource";
import { manualRunDraft, runCommandDraft, runCommandEvidenceText, runCommandMeta, runCommandRequest, type RunCommandExecutionRequest, type SessionRunCommand, type SessionRunView } from "../view/sessionRun";
import { CopyButton } from "./CopyButton";

export type RunCommandAction = (request: RunCommandExecutionRequest) => Promise<void> | void;
type RunFilter = "all" | "failed" | "running" | "passed";

export function SessionRunPanel({
  run,
  defaultOpen = false,
  onOpenArtifact,
  onRunCommand,
  runCommandBusy = false,
  onUseAsDraft,
}: {
  run: SessionRunView;
  defaultOpen?: boolean;
  onOpenArtifact?: (path: string) => void;
  onRunCommand?: RunCommandAction;
  runCommandBusy?: boolean;
  onUseAsDraft?: UseAsDraft;
}) {
  const [manualCommand, setManualCommand] = useState("");
  const [manualCwd, setManualCwd] = useState("");
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<RunFilter>("all");
  const trimmedQuery = query.trim();
  const stats = runStats(run.commands);
  const filteredCommands = filter === "all" ? run.commands : run.commands.filter((command) => command.status === filter);
  const visibleCommands = trimmedQuery ? filteredCommands.filter((command) => runMatchesQuery(command, trimmedQuery)) : filteredCommands;
  const focusCommand = visibleCommands.find((command) => command.status === "failed") ?? visibleCommands.find((command) => command.status === "running");

  async function handleManualSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const command = manualCommand.trim();
    if (!command) return;
    if (onRunCommand) {
      await onRunCommand({ command, cwd: manualCwd.trim() || undefined });
      setManualCommand("");
      return;
    }
    onUseAsDraft?.(manualRunDraft(command, manualCwd), "run_command");
  }

  return (
    <details className="session-skills-panel session-run-panel" data-testid="session-run-panel" open={defaultOpen}>
      <summary className="session-skills-summary">
        <span className="session-skills-kicker">Run</span>
        <strong>{run.summary}</strong>
        <span>{run.detail}</span>
      </summary>
      <div className="session-skills-body">
        <div className="session-run-overview" aria-label="Run summary">
          <div className="session-run-overview-main">
            <span>Commands</span>
            <strong>{run.summary}</strong>
            <small>{run.detail || "No shell commands recorded."}</small>
          </div>
          <div className="session-run-filterbar" role="group" aria-label="Run filters">
            <RunFilterButton label="All" value={stats.total} active={filter === "all"} onClick={() => setFilter("all")} />
            <RunFilterButton label="Failed" value={stats.failed} active={filter === "failed"} onClick={() => setFilter("failed")} />
            <RunFilterButton label="Running" value={stats.running} active={filter === "running"} onClick={() => setFilter("running")} />
            <RunFilterButton label="Passed" value={stats.passed} active={filter === "passed"} onClick={() => setFilter("passed")} />
          </div>
        </div>
        {focusCommand ? (
          <RunFocus
            command={focusCommand}
            onOpenArtifact={onOpenArtifact}
            onRunCommand={onRunCommand}
            runCommandBusy={runCommandBusy}
            onUseAsDraft={onUseAsDraft}
          />
        ) : null}
        {onUseAsDraft || onRunCommand ? (
          <form className="session-run-manual" data-testid="session-run-manual" onSubmit={handleManualSubmit}>
            <div className="session-run-manual-head">
              <strong>Run command</strong>
              <span>{run.latestCommandCwd ? `Latest cwd: ${displayPath(run.latestCommandCwd)}` : "Session workspace"}</span>
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
                placeholder={run.latestCommandCwd || "session workspace"}
              />
            </label>
            <div className="session-run-manual-actions">
              <button type="submit" className="ghost-action primary-run-action" disabled={!manualCommand.trim() || runCommandBusy}>
                {onRunCommand ? "Run now" : "Use command as draft"}
              </button>
              {onRunCommand && onUseAsDraft ? (
                <button type="button" className="ghost-action" disabled={!manualCommand.trim()} onClick={() => onUseAsDraft(manualRunDraft(manualCommand, manualCwd), "run_command")}>
                  Use command as draft
                </button>
              ) : null}
            </div>
          </form>
        ) : null}
        {run.commands.length > 1 ? (
          <div className="session-skills-controls">
            <label className="session-skills-search">
              <span>Search commands</span>
              <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="command, cwd, status, or output" />
            </label>
            {trimmedQuery ? (
              <button type="button" className="ghost-action" onClick={() => setQuery("")}>
                Clear
              </button>
            ) : null}
          </div>
        ) : null}
        {visibleCommands.length > 0 ? (
          <ol className="session-run-list" data-testid="session-run-list">
            {visibleCommands.map((command, index) => (
              <li key={`${command.turnNumber}:${index}:${command.command}`} className="session-run-item" data-status={command.status}>
                <div className="session-run-main">
                  <strong title={command.command}>{commandLabel(command.command)}</strong>
                  <span>{runCommandMeta(command)}</span>
                  {command.cwd ? <small title={command.cwd}>Cwd: {displayPath(command.cwd)}</small> : null}
                  {command.detail ? <small>{command.detail}</small> : null}
                  {command.next ? <small>Next: {command.next}</small> : null}
                  {command.artifactPath ? <small title={command.artifactPath}>Output: {artifactLabel(command.artifactPath)}</small> : null}
                </div>
                <span className="session-evidence-actions">
                  <CopyButton label="Copy command" value={command.command} className="ghost-action" />
                  <CopyButton label="Copy run evidence" value={runCommandEvidenceText(command)} className="ghost-action" />
                  {command.artifactPath && onOpenArtifact ? (
                    <button type="button" className="ghost-action" onClick={() => onOpenArtifact(command.artifactPath ?? "")}>
                      Open command output
                    </button>
                  ) : null}
                  {onRunCommand ? (
                    <button type="button" className="ghost-action primary-run-action" disabled={runCommandBusy} onClick={() => onRunCommand(runCommandRequest(command))}>
                      Rerun now
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
        ) : run.commands.length > 0 ? (
          <div className="session-skills-empty">No commands matching "{trimmedQuery}".</div>
        ) : (
          <div className="session-skills-empty">No shell commands in this chat.</div>
        )}
      </div>
    </details>
  );
}

function RunFilterButton({
  label,
  value,
  active,
  onClick,
}: {
  label: string;
  value: number;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button type="button" className="session-run-filter" data-active={active ? "true" : "false"} onClick={onClick}>
      <span>{label}</span>
      <strong>{value}</strong>
    </button>
  );
}

function runStats(commands: readonly SessionRunCommand[]) {
  return {
    total: commands.length,
    failed: commands.filter((command) => command.status === "failed").length,
    running: commands.filter((command) => command.status === "running").length,
    passed: commands.filter((command) => command.status === "passed").length,
  };
}

function runMatchesQuery(command: SessionRunCommand, query: string): boolean {
  const haystack = [
    command.command,
    command.cwd,
    command.status,
    runCommandMeta(command),
    command.detail,
    command.next,
    command.artifactPath,
  ].filter(Boolean).join("\n").toLowerCase();
  return haystack.includes(query.toLowerCase());
}

function RunFocus({
  command,
  onOpenArtifact,
  onRunCommand,
  runCommandBusy,
  onUseAsDraft,
}: {
  command: SessionRunCommand;
  onOpenArtifact?: (path: string) => void;
  onRunCommand?: RunCommandAction;
  runCommandBusy?: boolean;
  onUseAsDraft?: UseAsDraft;
}) {
  return (
    <section className="session-run-focus" data-status={command.status} data-testid="session-run-focus" aria-label="Run focus">
      <div className="session-run-focus-main">
        <span>{command.status === "failed" ? "Recovery needed" : "Running now"}</span>
        <strong title={command.command}>{commandLabel(command.command)}</strong>
        <small>{runCommandMeta(command)}</small>
        {command.cwd ? <small title={command.cwd}>Cwd: {displayPath(command.cwd)}</small> : null}
        {command.detail ? <p>{command.detail}</p> : null}
        {command.next ? <p>Next: {command.next}</p> : null}
        {command.artifactPath ? <small title={command.artifactPath}>Output: {artifactLabel(command.artifactPath)}</small> : null}
      </div>
      <div className="session-evidence-actions">
        <CopyButton label="Copy run evidence" value={runCommandEvidenceText(command)} className="ghost-action" />
        {command.artifactPath && onOpenArtifact ? (
          <button type="button" className="ghost-action" onClick={() => onOpenArtifact(command.artifactPath ?? "")}>
            Open command output
          </button>
        ) : null}
        {onRunCommand ? (
          <button type="button" className="ghost-action primary-run-action" disabled={runCommandBusy} onClick={() => onRunCommand(runCommandRequest(command))}>
            Rerun now
          </button>
        ) : null}
        {onUseAsDraft ? (
          <button type="button" className="ghost-action" onClick={() => onUseAsDraft(runCommandDraft(command), "run_command")}>
            Rerun as draft
          </button>
        ) : null}
      </div>
    </section>
  );
}

function commandLabel(command: string): string {
  const compacted = command.replace(/\s+/g, " ").trim();
  if (compacted.length <= 180) return compacted;
  return `${compacted.slice(0, 177)}...`;
}

function displayPath(path: string): string {
  const normalized = path.replace(/\\/g, "/");
  const parts = normalized.split("/").filter(Boolean);
  if (path.length > 64 && parts.length >= 2) return `.../${parts.slice(-2).join("/")}`;
  if (parts.length <= 3) return path;
  return parts.slice(-3).join("/");
}

function artifactLabel(path: string): string {
  const normalized = path.replace(/\\/g, "/");
  return normalized.split("/").filter(Boolean).at(-1) ?? path;
}
