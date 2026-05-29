import { useState, type FormEvent } from "react";
import type { UseAsDraft } from "../view/draftSource";
import { manualRunDraft, runCommandDraft, runCommandKind, runCommandMeta, runCommandRequest, runFocusCommand, runReviewFacts, runReviewFocus, type RunCommandExecutionRequest, type SessionRunCommand, type SessionRunFocus, type SessionRunView } from "../view/sessionRun";
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
  const review = runReviewFocus(run.commands);
  const reviewFacts = runReviewFacts(run.commands);
  const latestFailedCommand = run.commands.find((command) => command.status === "failed");
  const filteredCommands = filter === "all" ? run.commands : run.commands.filter((command) => command.status === filter);
  const visibleCommands = trimmedQuery ? filteredCommands.filter((command) => runMatchesQuery(command, trimmedQuery)) : filteredCommands;
  const focus = runFocusCommand(visibleCommands);
  const historyCommands = focus ? visibleCommands.filter((command) => command !== focus.command) : visibleCommands;
  const historyExpanded = filter !== "all" || Boolean(trimmedQuery);
  const displayedHistoryCommands = historyExpanded ? historyCommands : historyCommands.slice(0, 8);
  const hiddenHistoryCount = historyCommands.length - displayedHistoryCommands.length;
  const canInspectRecoveredFailure = !!latestFailedCommand && focus?.command !== latestFailedCommand;
  const quickCommands = runQuickCommands(run.commands, run.latestCommandCwd);

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
          <div className="session-run-review" data-tone={review.tone ?? "neutral"} data-testid="session-run-review">
            <span>{review.label}</span>
            <strong>{review.title}</strong>
            <small>{review.detail}</small>
            {canInspectRecoveredFailure ? (
              <div className="session-run-review-actions">
                <button
                  type="button"
                  className="ghost-action"
                  onClick={() => {
                    setFilter("failed");
                    setQuery("");
                  }}
                >
                  Inspect latest failure
                </button>
                {latestFailedCommand.artifactPath && onOpenArtifact ? (
                  <button type="button" className="ghost-action" onClick={() => onOpenArtifact(latestFailedCommand.artifactPath ?? "")}>
                    Open failure output
                  </button>
                ) : null}
                {onRunCommand ? (
                  <button
                    type="button"
                    className="ghost-action primary-run-action"
                    disabled={runCommandBusy}
                    onClick={() => onRunCommand(runCommandRequest(latestFailedCommand))}
                  >
                    Rerun latest failure
                  </button>
                ) : null}
              </div>
            ) : null}
          </div>
          <div className="session-run-facts" aria-label="Run review facts">
            {reviewFacts.map((fact) => (
              <span key={fact.label} data-tone={fact.tone ?? "neutral"} title={fact.detail}>
                <small>{fact.label}</small>
                <strong>{fact.value}</strong>
                {runFactVisibleDetail(fact) ? <b>{runFactVisibleDetail(fact)}</b> : null}
              </span>
            ))}
          </div>
          <div className="session-run-filterbar" role="group" aria-label="Run filters">
            <RunFilterButton label="All" value={stats.total} active={filter === "all"} onClick={() => setFilter("all")} />
            <RunFilterButton label="Failed" value={stats.failed} active={filter === "failed"} onClick={() => setFilter("failed")} />
            <RunFilterButton label="Running" value={stats.running} active={filter === "running"} onClick={() => setFilter("running")} />
            <RunFilterButton label="Passed" value={stats.passed} active={filter === "passed"} onClick={() => setFilter("passed")} />
          </div>
        </div>
        {focus ? (
          <RunFocus
            focus={focus}
            onOpenArtifact={onOpenArtifact}
            onRunCommand={onRunCommand}
            runCommandBusy={runCommandBusy}
            onUseAsDraft={onUseAsDraft}
          />
        ) : null}
        {(onUseAsDraft || onRunCommand) && quickCommands.length > 0 ? (
          <div className="session-run-quick" data-testid="session-run-quick" aria-label="Quick run commands">
            <div>
              <strong>Quick commands</strong>
              <span>{onRunCommand ? "Run read-only checks in the session workspace." : "Prepare a command draft for Affent."}</span>
            </div>
            <div className="session-run-quick-actions">
              {quickCommands.map((item) => (
                <button
                  key={`${item.label}:${item.command}:${item.cwd ?? ""}`}
                  type="button"
                  className="ghost-action"
                  disabled={runCommandBusy}
                  title={item.command}
                  onClick={() => void handleQuickCommand(item)}
                >
                  {item.label}
                </button>
              ))}
            </div>
          </div>
        ) : null}
        {onUseAsDraft || onRunCommand ? (
          <details className="session-run-manual-disclosure" open={run.commands.length === 0}>
            <summary>
              <span>Run command</span>
              <small>{run.latestCommandCwd ? `Latest cwd: ${displayPath(run.latestCommandCwd)}` : "Session workspace"}</small>
            </summary>
            <form className="session-run-manual" data-testid="session-run-manual" onSubmit={handleManualSubmit}>
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
              </div>
            </form>
          </details>
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
        {historyCommands.length > 0 ? (
          <section className="session-run-history" data-testid="session-run-history" aria-label="Command history">
            <div className="session-run-history-head">
              <strong>Command history</strong>
              <span>
                {displayedHistoryCommands.length} shown
                {hiddenHistoryCount > 0 ? ` · ${hiddenHistoryCount} more via search/filter` : ""}
              </span>
            </div>
            <ol className="session-run-list" data-testid="session-run-list">
              {displayedHistoryCommands.map((command, index) => (
                <li key={`${command.turnNumber}:${index}:${command.command}`} className="session-run-item" data-status={command.status}>
                  <div className="session-run-main">
                    <strong title={command.command}>{commandLabel(command.command)}</strong>
                    <span>{runCommandMeta(command)}</span>
                    {command.cwd ? <small title={command.cwd}>Cwd: {displayPath(command.cwd)}</small> : null}
                    {command.detail ? <small>{command.detail}</small> : null}
                    {command.next ? <small>Next: {command.next}</small> : null}
                    {command.artifactPath ? <small title={command.artifactPath}>Output: {artifactLabel(command.artifactPath)}</small> : null}
                  </div>
                  {(command.artifactPath && onOpenArtifact) || (onRunCommand && command.status !== "passed") ? (
                    <span className="session-evidence-actions">
                    {command.artifactPath && onOpenArtifact ? (
                      <button type="button" className="ghost-action" onClick={() => onOpenArtifact(command.artifactPath ?? "")}>
                        Open command output
                      </button>
                    ) : null}
                    {onRunCommand && command.status !== "passed" ? (
                      <button type="button" className="ghost-action primary-run-action" disabled={runCommandBusy} onClick={() => onRunCommand(runCommandRequest(command))}>
                        Rerun now
                      </button>
                    ) : null}
                    </span>
                  ) : null}
                </li>
              ))}
            </ol>
          </section>
        ) : visibleCommands.length > 0 ? null : run.commands.length > 0 ? (
          <div className="session-skills-empty">No commands matching "{trimmedQuery}".</div>
        ) : (
          <div className="session-skills-empty">No shell commands in this chat.</div>
        )}
      </div>
    </details>
  );

  async function handleQuickCommand(item: RunQuickCommand) {
    if (onRunCommand) {
      await onRunCommand({ command: item.command, cwd: item.cwd });
      return;
    }
    onUseAsDraft?.(manualRunDraft(item.command, item.cwd), "run_command");
  }
}

interface RunQuickCommand {
  label: string;
  command: string;
  cwd?: string;
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
  if (value === 0 && !active) return null;
  return (
    <button type="button" className="session-run-filter" data-active={active ? "true" : "false"} onClick={onClick}>
      <span>{label}</span>
      <strong>{value}</strong>
    </button>
  );
}

function runFactVisibleDetail(fact: { label: string; detail: string }): string | undefined {
  const detail = fact.detail.trim();
  if (!detail) return undefined;
  if (detail === "recorded command" || detail === "recorded commands") return undefined;
  if (detail === "successful commands" || detail === "no successful command") return undefined;
  if (detail === "artifact captured" || detail === "artifacts captured") return undefined;
  if (detail === "none recorded" || detail === "no command") return undefined;
  if (detail === "covered by later pass") return "recovered";
  if (detail === "test/build/lint/typecheck") return "checks";
  return detail;
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

function runQuickCommands(commands: readonly SessionRunCommand[], latestCwd?: string): RunQuickCommand[] {
  const quick: RunQuickCommand[] = [];
  const cwd = latestCwd ?? commands
    .filter((command) => command.cwd)
    .sort((a, b) => commandOrder(b) - commandOrder(a))[0]?.cwd;
  const latestVerification = commands
    .filter((command) => isVerificationCommand(command))
    .sort((a, b) => commandOrder(b) - commandOrder(a))[0];
  if (latestVerification) {
    quick.push({ label: "Rerun verification", command: latestVerification.command, cwd: latestVerification.cwd });
  }
  quick.push(
    { label: "Git status", command: "git status --short --branch", cwd },
    { label: "Diff stat", command: "git diff --stat", cwd },
    { label: "Print cwd", command: "pwd", cwd },
  );
  const seen = new Set<string>();
  return quick.filter((item) => {
    const key = `${item.command}\n${item.cwd ?? ""}`;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  }).slice(0, 4);
}

function isVerificationCommand(command: SessionRunCommand): boolean {
  const kind = runCommandKind(command);
  return kind === "test" || kind === "build" || kind === "lint" || kind === "typecheck";
}

function commandOrder(command: SessionRunCommand): number {
  return command.turnNumber * 1_000_000 + (command.sequence ?? 0);
}

function RunFocus({
  focus,
  onOpenArtifact,
  onRunCommand,
  runCommandBusy,
  onUseAsDraft,
}: {
  focus: SessionRunFocus;
  onOpenArtifact?: (path: string) => void;
  onRunCommand?: RunCommandAction;
  runCommandBusy?: boolean;
  onUseAsDraft?: UseAsDraft;
}) {
  const command = focus.command;
  return (
    <section className="session-run-focus" data-status={command.status} data-testid="session-run-focus" aria-label="Run focus">
      <div className="session-run-focus-main">
        <span>{focus.label}</span>
        <strong title={command.command}>{commandLabel(command.command)}</strong>
        <small>{focus.detail}</small>
        <div className="session-run-focus-facts">
          <RunFocusFact label="Status" value={command.status} />
          <RunFocusFact label="Kind" value={runCommandKind(command)} />
          {command.exitCode != null ? <RunFocusFact label="Exit" value={String(command.exitCode)} /> : null}
          {command.durationMs != null ? <RunFocusFact label="Duration" value={commandDurationLabel(command.durationMs)} /> : null}
          <RunFocusFact label="Turn" value={String(command.turnNumber)} />
        </div>
        {command.cwd ? <small title={command.cwd}>Cwd: {displayPath(command.cwd)}</small> : null}
        {command.detail && command.detail !== focus.detail ? <p>{command.detail}</p> : null}
        {command.next ? <p>Next: {command.next}</p> : null}
        {command.artifactPath ? <small title={command.artifactPath}>Output: {artifactLabel(command.artifactPath)}</small> : null}
      </div>
      <div className="session-evidence-actions">
        {command.artifactPath && onOpenArtifact ? (
          <button type="button" className="ghost-action" onClick={() => onOpenArtifact(command.artifactPath ?? "")}>
            Open command output
          </button>
        ) : null}
        {onRunCommand && command.status !== "passed" ? (
          <button type="button" className="ghost-action primary-run-action" disabled={runCommandBusy} onClick={() => onRunCommand(runCommandRequest(command))}>
            Rerun now
          </button>
        ) : null}
        {!onRunCommand && onUseAsDraft && command.status !== "passed" ? (
          <button type="button" className="ghost-action" onClick={() => onUseAsDraft(runCommandDraft(command), "run_command")}>
            Rerun as draft
          </button>
        ) : null}
        <CopyButton label="Copy command" value={command.command} className="ghost-action" />
      </div>
    </section>
  );
}

function RunFocusFact({ label, value }: { label: string; value: string }) {
  return (
    <span>
      <small>{label}</small>
      <strong>{value}</strong>
    </span>
  );
}

function commandDurationLabel(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  const seconds = ms / 1000;
  return `${seconds.toFixed(seconds < 10 ? 2 : 1)}s`;
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
