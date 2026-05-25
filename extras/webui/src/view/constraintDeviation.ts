import type { ToolCallState, TurnState } from "../store/sessionState";

export interface ConstraintDeviation {
  id: string;
  summary: string;
  detail: string;
  toolNames: string[];
}

export function detectConstraintDeviations(turn: TurnState): ConstraintDeviation[] {
  const userText = turn.userText?.trim();
  if (!userText || turn.toolCalls.length === 0) return [];

  const deviations: ConstraintDeviation[] = [];
  if (asksForNoTools(userText)) {
    deviations.push({
      id: "no-tools",
      summary: "Tools were used after the user asked for a no-tool reply.",
      detail: `Used ${formatToolNames(turn.toolCalls)} after the message asked not to call tools.`,
      toolNames: uniqueToolNames(turn.toolCalls),
    });
  } else if (asksForNoShell(userText)) {
    const shellCalls = turn.toolCalls.filter((call) => call.tool === "shell");
    if (shellCalls.length > 0) {
      deviations.push({
        id: "no-shell",
        summary: "Shell was used after the user asked not to use shell.",
        detail: `Used shell ${shellCalls.length} ${shellCalls.length === 1 ? "time" : "times"} after the message asked not to use shell.`,
        toolNames: ["shell"],
      });
    }
  }

  return deviations;
}

function asksForNoTools(text: string): boolean {
  const normalized = normalize(text);
  return [
    /不要再调用任何工具/,
    /不要调用(?:任何)?工具/,
    /不要使用(?:任何)?工具/,
    /不用(?:任何)?工具/,
    /别(?:再)?调用(?:任何)?工具/,
    /直接基于.*(?:输出|回复|回答)/,
    /\bdo not (?:call|use) (?:any )?tools?\b/,
    /\bdon't (?:call|use) (?:any )?tools?\b/,
    /\bwithout (?:using )?tools?\b/,
    /\bno tools?\b/,
  ].some((pattern) => pattern.test(normalized));
}

function asksForNoShell(text: string): boolean {
  const normalized = normalize(text);
  return [
    /不要.*shell/,
    /别.*shell/,
    /不用.*shell/,
    /不要为了搜索而用 shell/,
    /\bdo not use shell\b/,
    /\bdon't use shell\b/,
    /\bwithout shell\b/,
    /\bno shell\b/,
  ].some((pattern) => pattern.test(normalized));
}

function normalize(text: string): string {
  return text.replace(/\s+/g, " ").trim().toLowerCase();
}

function uniqueToolNames(calls: readonly ToolCallState[]): string[] {
  return Array.from(new Set(calls.map((call) => call.tool).filter(Boolean)));
}

function formatToolNames(calls: readonly ToolCallState[]): string {
  const names = uniqueToolNames(calls);
  if (names.length === 0) return "tools";
  if (names.length <= 3) return names.join(", ");
  return `${names.slice(0, 3).join(", ")} and ${names.length - 3} more`;
}
