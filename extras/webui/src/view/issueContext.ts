import type { SessionState } from "../store/sessionState";

export function hasIssueContext(session: SessionState): boolean {
  if (session.turns.length > 1 || session.unknownEventCount > 0) return true;
  return session.turns.some((turn) => {
    if (turn.status === "error" || turn.status === "max_turns" || turn.error) return true;
    if (turn.toolCalls.length > 1) return true;
    return turn.toolCalls.some((call) =>
      call.status === "error" ||
      (call.status === "running" && (call.tool === "subagent_run" || call.tool === "run_task")) ||
      call.argsTruncated ||
      call.resultTruncated ||
      call.argsRepaired ||
      call.canonicalized ||
      !!call.originalTool ||
      !!call.resultArtifactPath ||
      call.tool === "subagent_run" ||
      call.tool === "run_task",
    );
  });
}
