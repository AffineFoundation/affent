import type { SessionState, ToolCallState } from "../store/sessionState";
import { summarizePreview } from "./textPreview";

export type SessionRunStatus = "running" | "passed" | "failed";
export type SessionRunCommandKind = "test" | "build" | "lint" | "typecheck" | "git" | "shell";

export interface SessionRunCommand {
  command: string;
  kind?: SessionRunCommandKind;
  cwd?: string;
  status: SessionRunStatus;
  turnNumber: number;
  sequence?: number;
  exitCode?: number;
  durationMs?: number;
  detail?: string;
  next?: string;
  artifactPath?: string;
}

export interface RunCommandExecutionRequest {
  command: string;
  cwd?: string;
}

export interface SessionRunView {
  commands: SessionRunCommand[];
  latestCommandCwd?: string;
  summary: string;
  detail: string;
  tone?: "warning" | "error";
}

export interface SessionRunFocus {
  command: SessionRunCommand;
  label: string;
  detail: string;
  tone: "error" | "warning" | "success";
}

export interface SessionRunReview {
  label: string;
  title: string;
  detail: string;
  tone?: "ok" | "attention" | "danger" | "neutral";
}

export interface SessionRunFact {
  label: string;
  value: string;
  detail: string;
  tone?: "ok" | "attention" | "danger" | "neutral";
}

interface SessionRunCommandInternal extends SessionRunCommand {
  sequence: number;
}

export function buildSessionRun(session: SessionState): SessionRunView {
  const commands: SessionRunCommandInternal[] = [];
  let latestCommandCwd: string | undefined;
  let sequence = 0;
  session.turns.forEach((turn, turnIndex) => {
    for (const call of turn.toolCalls) {
      sequence += 1;
      const command = commandFromCall(call, turnIndex + 1, sequence);
      if (command) {
        commands.push(command);
        if (command.cwd) latestCommandCwd = command.cwd;
      }
    }
  });
  const sorted = commands
    .sort((a, b) => commandPriority(a) - commandPriority(b) || b.turnNumber - a.turnNumber || b.sequence - a.sequence)
    .map((command) => command);
  const failed = sorted.filter((command) => command.status === "failed").length;
  const running = sorted.filter((command) => command.status === "running").length;
  const passed = sorted.filter((command) => command.status === "passed").length;
  const hasUnresolvedFailure = !!latestUnrecoveredFailedCommand(sorted);
  return {
    commands: sorted,
    latestCommandCwd,
    summary: runSummary(sorted.length, { failed, running, passed }, hasUnresolvedFailure),
    detail: runDetail(sorted.length, { failed, running, passed }),
    tone: hasUnresolvedFailure ? "error" : running > 0 ? "warning" : undefined,
  };
}

export function runCommandMeta(command: SessionRunCommand): string {
  const parts = [
    runCommandKind(command),
    runStatusLabel(command.status),
    command.exitCode != null ? `exit ${command.exitCode}` : undefined,
    command.durationMs != null ? formatDuration(command.durationMs) : undefined,
    `turn ${command.turnNumber}`,
  ].filter(Boolean);
  return parts.join(" · ");
}

export function runCommandEvidenceText(command: SessionRunCommand): string {
  const lines = [
    `Run evidence for ${command.command}`,
    `Status: ${runStatusLabel(command.status)}`,
    `Kind: ${runCommandKind(command)}`,
    command.exitCode != null ? `Exit: ${command.exitCode}` : undefined,
    command.durationMs != null ? `Duration: ${formatDuration(command.durationMs)}` : undefined,
    `Turn: ${command.turnNumber}`,
    command.cwd ? `Working directory: ${command.cwd}` : undefined,
    command.detail ? `Output: ${command.detail}` : undefined,
    command.next ? `Next: ${command.next}` : undefined,
    command.artifactPath ? `Output artifact: ${command.artifactPath}` : undefined,
  ];
  return lines.filter((line): line is string => Boolean(line)).join("\n");
}

export function runCommandDraft(command: SessionRunCommand): string {
  const lines = [
    "Rerun or recover from this command, then report the result:",
    command.command,
    "",
    runCommandEvidenceText(command),
  ];
  return lines.join("\n");
}

export function runCommandRequest(command: SessionRunCommand): RunCommandExecutionRequest {
  return {
    command: command.command,
    cwd: command.cwd,
  };
}

export function runCommandKind(command: SessionRunCommand | string): SessionRunCommandKind {
  const text = typeof command === "string" ? command : command.kind ?? command.command;
  const normalized = text.replace(/\s+/g, " ").trim().toLowerCase();
  if (/\b(?:test|pytest|vitest|jest|playwright|go test|cargo test|mvn test|gradle test)\b/.test(normalized)) return "test";
  if (/\b(?:typecheck|tsc|mypy|pyright|go vet)\b/.test(normalized)) return "typecheck";
  if (/\b(?:lint|eslint|ruff|golangci-lint|clippy)\b/.test(normalized)) return "lint";
  if (/\b(?:build|vite build|tsup|webpack|rollup|cargo build|go build|mvn package|gradle build)\b/.test(normalized)) return "build";
  if (/^(?:git|gh)\b/.test(normalized)) return "git";
  return "shell";
}

export function runFocusCommand(commands: readonly SessionRunCommand[]): SessionRunFocus | undefined {
  const unresolved = latestUnrecoveredFailedCommand(commands);
  if (unresolved) {
    return {
      command: unresolved,
      label: "Recovery needed",
      detail: unresolved.next ?? unresolved.detail ?? "This command failed and needs review before trusting the run.",
      tone: "error",
    };
  }
  const running = commands.find((command) => command.status === "running");
  if (running) {
    return {
      command: running,
      label: "Running now",
      detail: running.detail ?? "Command is still running.",
      tone: "warning",
    };
  }
  const latestPassedVerification = latestCommand(commands, (command) => command.status === "passed" && isVerificationKind(runCommandKind(command)));
  if (latestPassedVerification) {
    return {
      command: latestPassedVerification,
      label: "Latest verification",
      detail: latestPassedVerification.detail ?? "Most recent verification command passed.",
      tone: "success",
    };
  }
  const latestPassed = commands.find((command) => command.status === "passed");
  if (latestPassed) {
    return {
      command: latestPassed,
      label: "Latest command",
      detail: latestPassed.detail ?? "Most recent command passed.",
      tone: "success",
    };
  }
  return undefined;
}

export function runReviewFocus(commands: readonly SessionRunCommand[]): SessionRunReview {
  if (commands.length === 0) {
    return {
      label: "Idle",
      title: "No commands recorded",
      detail: "Shell commands and manual reruns will appear here.",
      tone: "neutral",
    };
  }
  const running = latestCommand(commands, (command) => command.status === "running");
  if (running) {
    return {
      label: "Running",
      title: commandLabel(running.command),
      detail: running.cwd ? `Cwd ${running.cwd}` : "Command is still running.",
      tone: "attention",
    };
  }
  const unresolved = latestUnrecoveredFailedCommand(commands);
  if (unresolved) {
    return {
      label: "Unresolved failure",
      title: commandLabel(unresolved.command),
      detail: unresolved.next ?? unresolved.detail ?? "Rerun or inspect output before trusting this session.",
      tone: "danger",
    };
  }
  const failedCount = commands.filter((command) => command.status === "failed").length;
  const latestPassed = latestCommand(commands, (command) => command.status === "passed");
  if (failedCount > 0 && latestPassed) {
    const latestVerification = latestCommand(commands, (command) => command.status === "passed" && isVerificationKind(runCommandKind(command)));
    return {
      label: "Recovered",
      title: `${failedCount} earlier ${plural("failure", failedCount)} followed by a pass`,
      detail: latestVerification ? `Latest verification: ${commandLabel(latestVerification.command)}` : `Latest passing command: ${commandLabel(latestPassed.command)}`,
      tone: "ok",
    };
  }
  if (latestPassed) {
    const verification = isVerificationKind(runCommandKind(latestPassed));
    return {
      label: verification ? "Verified" : "Passed",
      title: commandLabel(latestPassed.command),
      detail: latestPassed.detail ?? (verification ? "Latest verification command passed." : "Latest command passed."),
      tone: "ok",
    };
  }
  return {
    label: "Review",
    title: `${commands.length} ${plural("command", commands.length)} recorded`,
    detail: "Inspect command history before trusting the run.",
    tone: "neutral",
  };
}

export function runReviewFacts(commands: readonly SessionRunCommand[]): SessionRunFact[] {
  const total = commands.length;
  const failed = commands.filter((command) => command.status === "failed").length;
  const passed = commands.filter((command) => command.status === "passed").length;
  const verification = commands.filter((command) => isVerificationKind(runCommandKind(command)));
  const verificationPassed = verification.filter((command) => command.status === "passed").length;
  const artifactCount = commands.filter((command) => !!command.artifactPath).length;
  const latest = latestCommand(commands);
  const unresolved = latestUnrecoveredFailedCommand(commands);
  return [
    {
      label: "Commands",
      value: String(total),
      detail: total === 1 ? "recorded command" : "recorded commands",
      tone: total > 0 ? "neutral" : "neutral",
    },
    {
      label: "Failures",
      value: String(failed),
      detail: unresolved ? "unresolved" : failed > 0 ? "covered by later pass" : "none",
      tone: unresolved ? "danger" : failed > 0 ? "ok" : "neutral",
    },
    {
      label: "Passed",
      value: String(passed),
      detail: passed > 0 ? "successful commands" : "no successful command",
      tone: passed > 0 ? "ok" : total > 0 ? "attention" : "neutral",
    },
    {
      label: "Verification",
      value: verification.length > 0 ? `${verificationPassed}/${verification.length}` : "0/0",
      detail: verification.length > 0 ? "test/build/lint/typecheck" : "none recorded",
      tone: verification.length === 0 ? "neutral" : verificationPassed === verification.length || !unresolved ? "ok" : "attention",
    },
    {
      label: "Output",
      value: String(artifactCount),
      detail: artifactCount === 1 ? "artifact captured" : "artifacts captured",
      tone: artifactCount > 0 ? "ok" : "neutral",
    },
    {
      label: "Latest",
      value: latest ? runStatusLabel(latest.status) : "none",
      detail: latest ? `turn ${latest.turnNumber}` : "no command",
      tone: latest?.status === "failed" ? "danger" : latest?.status === "running" ? "attention" : latest?.status === "passed" ? "ok" : "neutral",
    },
  ];
}

export function manualRunDraft(command: string, cwd?: string): string {
  const lines = [
    "Run this command in the session workspace, then report the exit code, working directory, and relevant output:",
    command.trim(),
    cwd?.trim() ? `Working directory: ${cwd.trim()}` : undefined,
  ];
  return lines.filter((line): line is string => Boolean(line)).join("\n");
}

function commandPriority(command: SessionRunCommand): number {
  if (command.status === "failed") return 0;
  if (command.status === "running") return 1;
  return 2;
}

function latestUnrecoveredFailedCommand(commands: readonly SessionRunCommand[]): SessionRunCommand | undefined {
  const failed = latestCommand(commands, (command) => command.status === "failed");
  if (!failed) return undefined;
  const failedOrder = commandOrder(failed);
  const laterPass = commands.some((command) => command.status === "passed" && commandOrder(command) > failedOrder && recoversFailure(failed, command));
  return laterPass ? undefined : failed;
}

function recoversFailure(failed: SessionRunCommand, passed: SessionRunCommand): boolean {
  if (normalizedCommand(failed.command) === normalizedCommand(passed.command)) return true;
  const failedKind = runCommandKind(failed);
  const passedKind = runCommandKind(passed);
  return isVerificationKind(failedKind) && failedKind === passedKind;
}

function isVerificationKind(kind: SessionRunCommandKind): boolean {
  return kind === "test" || kind === "build" || kind === "lint" || kind === "typecheck";
}

function normalizedCommand(command: string): string {
  return command.replace(/\s+/g, " ").trim().toLowerCase();
}

function latestCommand(commands: readonly SessionRunCommand[], predicate: (command: SessionRunCommand) => boolean = () => true): SessionRunCommand | undefined {
  return commands
    .filter(predicate)
    .sort((a, b) => commandOrder(b) - commandOrder(a))[0];
}

function commandOrder(command: SessionRunCommand): number {
  return command.turnNumber * 1_000_000 + (command.sequence ?? 0);
}

function commandLabel(command: string): string {
  const compacted = command.replace(/\s+/g, " ").trim();
  if (compacted.length <= 140) return compacted;
  return `${compacted.slice(0, 137)}...`;
}

function runStatusLabel(status: SessionRunStatus): string {
  if (status === "running") return "running";
  if (status === "failed") return "failed";
  return "passed";
}

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  const s = ms / 1000;
  return `${s.toFixed(s < 10 ? 2 : 1)}s`;
}

function commandFromCall(call: ToolCallState, turnNumber: number, sequence: number): SessionRunCommandInternal | undefined {
  if (call.tool !== "shell") return undefined;
  const command = stringArg(call, "command");
  if (!command) return undefined;
  const detail = commandDetail(call);
  return {
    command,
    kind: runCommandKind(command),
    cwd: stringArg(call, "cwd"),
    status: commandStatus(call),
    turnNumber,
    sequence,
    exitCode: call.exitCode,
    durationMs: call.durationMs,
    detail,
    next: nextHint(call.resultSummary, call.result),
    artifactPath: call.resultArtifactPath,
  };
}

function commandStatus(call: ToolCallState): SessionRunStatus {
  if (call.status === "running") return "running";
  return call.status === "error" || (call.exitCode != null && call.exitCode !== 0) ? "failed" : "passed";
}

function commandDetail(call: ToolCallState): string | undefined {
  const source = call.resultSummary || call.result || call.failureKind;
  if (!source) return undefined;
  const cleaned = stripRecoveryLines(source);
  return cleaned ? summarizePreview(cleaned, 120) : undefined;
}

function nextHint(summary?: string, result?: string): string | undefined {
  const text = [summary, result && result !== summary ? result : undefined].filter(Boolean).join("\n");
  const match = text.match(/(?:^|\n)Next:\s*([\s\S]*?)(?:\nFailure:|\n[A-Z][A-Za-z _-]{0,40}:|$)/);
  const value = match?.[1]?.trim();
  return value ? summarizePreview(value, 120) : undefined;
}

function stripRecoveryLines(text: string): string {
  return text
    .split(/\r?\n/)
    .filter((line) => !/^\s*(Next|Failure):/i.test(line))
    .filter((line) => !/tool result context budget exhausted/i.test(line))
    .join("\n");
}

function runSummary(total: number, counts: { failed: number; running: number; passed: number }, hasUnresolvedFailure: boolean): string {
  if (total === 0) return "No commands";
  if (hasUnresolvedFailure) return `${counts.failed} failed ${plural("command", counts.failed)}`;
  if (counts.running > 0) return `${counts.running} running ${plural("command", counts.running)}`;
  if (counts.failed > 0) return `${counts.failed} recovered ${plural("failure", counts.failed)}`;
  return `${counts.passed} passed ${plural("command", counts.passed)}`;
}

function runDetail(total: number, counts: { failed: number; running: number; passed: number }): string {
  if (total === 0) return "No shell commands in this chat.";
  const parts: string[] = [];
  if (counts.failed > 0) parts.push(`${counts.failed} failed`);
  if (counts.running > 0) parts.push(`${counts.running} running`);
  if (counts.passed > 0) parts.push(`${counts.passed} passed`);
  return parts.join(" · ");
}

function plural(label: string, count: number): string {
  return count === 1 ? label : `${label}s`;
}

function stringArg(call: ToolCallState, key: string): string | undefined {
  const value = call.args[key];
  return typeof value === "string" && value.trim() ? value.trim() : undefined;
}
