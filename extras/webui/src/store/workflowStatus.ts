import type { SessionState, ToolCallState, TurnState } from "./sessionState";

export type WorkflowPhase = "ready" | "request" | "reasoning" | "tools" | "result" | "done" | "blocked";

export interface WorkflowStatus {
  phase: WorkflowPhase;
  title: string;
  detail: string;
  active: boolean;
  currentTool?: string;
  progress: number;
}

export function deriveWorkflowStatus(session: SessionState): WorkflowStatus {
  const turn = session.turns.at(-1);
  if (!turn) {
    return {
      phase: "ready",
      title: "Ready",
      detail: "Send a request and the conversation will update live.",
      active: false,
      progress: 0,
    };
  }

  if (turn.status === "error" || turn.error) {
    return {
      phase: "blocked",
      title: "Blocked",
      detail: turn.error ? `${turn.error.code}: ${turn.error.message}` : "The request ended with an error.",
      active: false,
      progress: 100,
    };
  }

  if (turn.status === "cancelled") {
    return {
      phase: "blocked",
      title: "Cancelled",
      detail: "The current request was stopped.",
      active: false,
      progress: 100,
    };
  }

  if (turn.status === "max_turns") {
    return {
      phase: "blocked",
      title: "Final answer not produced",
      detail: "The action limit was reached before the final reply was synthesized.",
      active: false,
      progress: 88,
    };
  }

  if (turn.status === "running") return runningStatus(turn);

  return {
    phase: "done",
    title: "Result ready",
    detail: completionDetail(turn),
    active: false,
    progress: 100,
  };
}

function runningStatus(turn: TurnState): WorkflowStatus {
  const runningTool = lastRunningTool(turn.toolCalls);
  if (runningTool) {
    return {
      phase: "tools",
      title: "Working",
      detail: toolDetail(runningTool),
      active: true,
      currentTool: runningTool.tool,
      progress: 62,
    };
  }
  if (turn.messageStreaming || turn.assistantText) {
    return {
      phase: "result",
      title: "Writing result",
      detail: "The assistant is composing the response.",
      active: true,
      progress: 82,
    };
  }
  if (turn.thinkingStreaming || turn.thinkingText) {
    return {
      phase: "reasoning",
      title: "Planning",
      detail: "Planning the next useful action.",
      active: true,
      progress: 38,
    };
  }
  return {
    phase: "request",
    title: "Starting",
    detail: turn.userText ? "Reading the request." : "Preparing the request.",
    active: true,
    progress: 16,
  };
}

function lastRunningTool(calls: readonly ToolCallState[]): ToolCallState | undefined {
  for (let i = calls.length - 1; i >= 0; i--) {
    if (calls[i].status === "running") return calls[i];
  }
  return undefined;
}

function toolDetail(tool: ToolCallState): string {
  if (tool.argsRepaired || tool.canonicalized) return "Preparing the action before running it.";
  if (tool.argsTruncated) return "Large inputs are summarized in the chat.";
  return "A background action is running.";
}

function completionDetail(turn: TurnState): string {
  if (turn.toolStats?.tool_errors) return `${turn.toolStats.tool_errors} action issue(s) surfaced.`;
  if (turn.toolCalls.length > 0) return `${turn.toolCalls.length} action${turn.toolCalls.length === 1 ? "" : "s"} completed.`;
  return "No external actions were needed.";
}
