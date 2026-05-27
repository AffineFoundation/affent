import type { SessionState, ToolCallState } from "../store/sessionState";
import { summarizePreview } from "./textPreview";

export type SessionRunStatus = "running" | "passed" | "failed";

export interface SessionRunCommand {
  command: string;
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

export function buildSessionRun(session: SessionState): SessionRunView {
  const commands: SessionRunCommand[] = [];
  session.turns.forEach((turn, turnIndex) => {
    for (const call of turn.toolCalls) {
      const command = commandFromCall(call, turnIndex + 1);
      if (command) commands.push(command);
    }
  });
  const sorted = commands.sort((a, b) => b.turnNumber - a.turnNumber);
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

function commandFromCall(call: ToolCallState, turnNumber: number): SessionRunCommand | undefined {
  if (call.tool !== "shell") return undefined;
  const command = stringArg(call, "command");
  if (!command) return undefined;
  const detail = commandDetail(call);
  return {
    command,
    status: commandStatus(call),
    turnNumber,
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
