import type { SessionState, ToolCallState } from "../store/sessionState";
import { summarizePreview } from "./textPreview";

export type SessionRunStatus = "running" | "passed" | "failed";

export interface SessionRunCommand {
  command: string;
  cwd?: string;
  status: SessionRunStatus;
  turnNumber: number;
  exitCode?: number;
  durationMs?: number;
  detail?: string;
  next?: string;
  artifactPath?: string;
}

export interface SessionRunView {
  commands: SessionRunCommand[];
  summary: string;
  detail: string;
  tone?: "warning" | "error";
}

interface SessionRunCommandInternal extends SessionRunCommand {
  sequence: number;
}

export function buildSessionRun(session: SessionState): SessionRunView {
  const commands: SessionRunCommandInternal[] = [];
  let sequence = 0;
  session.turns.forEach((turn, turnIndex) => {
    for (const call of turn.toolCalls) {
      sequence += 1;
      const command = commandFromCall(call, turnIndex + 1, sequence);
      if (command) commands.push(command);
    }
  });
  const sorted = commands
    .sort((a, b) => commandPriority(a) - commandPriority(b) || b.turnNumber - a.turnNumber || b.sequence - a.sequence)
    .map(({ sequence: _sequence, ...command }) => command);
  const failed = sorted.filter((command) => command.status === "failed").length;
  const running = sorted.filter((command) => command.status === "running").length;
  const passed = sorted.filter((command) => command.status === "passed").length;
  return {
    commands: sorted,
    summary: runSummary(sorted.length, { failed, running, passed }),
    detail: runDetail(sorted.length, { failed, running, passed }),
    tone: failed > 0 ? "error" : running > 0 ? "warning" : undefined,
  };
}

export function runCommandMeta(command: SessionRunCommand): string {
  const parts = [
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

function commandPriority(command: SessionRunCommand): number {
  if (command.status === "failed") return 0;
  if (command.status === "running") return 1;
  return 2;
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
  return summarizePreview(stripRecoveryLines(source), 120);
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
    .join("\n");
}

function runSummary(total: number, counts: { failed: number; running: number; passed: number }): string {
  if (total === 0) return "No commands";
  if (counts.failed > 0) return `${counts.failed} failed ${plural("command", counts.failed)}`;
  if (counts.running > 0) return `${counts.running} running ${plural("command", counts.running)}`;
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
