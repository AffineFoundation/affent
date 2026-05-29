export type DraftSource =
  | "answer"
  | "artifact"
  | "artifact_text"
  | "changed_file"
  | "config"
  | "continuation"
  | "evidence"
  | "error"
  | "file_evidence"
  | "file_range"
  | "file_snapshot"
  | "guidance_receipt"
  | "loop_setup"
  | "memory"
  | "previous_message"
  | "recent_chat"
  | "result"
  | "run_command"
  | "skill"
  | "starter"
  | "retry_reply"
  | "tool_guidance"
  | "tool_result"
  | "trace"
  | "workspace"
  | "retry";

export type UseAsDraft = (content: string, source?: DraftSource) => void;
export type DraftMergeMode = "append" | "replace";

const draftSourceLabels: Record<DraftSource, string> = {
  answer: "Continuing from answer",
  artifact: "Artifact added to message",
  artifact_text: "Using file text",
  changed_file: "Using changed file",
  config: "Using config evidence",
  continuation: "Using final answer request",
  evidence: "Using evidence",
  error: "Using error diagnostic",
  file_evidence: "Using file evidence",
  file_range: "Using file range",
  file_snapshot: "Using file snapshot",
  guidance_receipt: "Editing sent guidance",
  loop_setup: "Loop setup",
  memory: "Using memory evidence",
  previous_message: "Editing previous message",
  recent_chat: "Starting from recent chat",
  result: "Continuing from output",
  run_command: "Using command",
  skill: "Using skill evidence",
  starter: "Starting from draft",
  retry_reply: "Retrying from reply",
  tool_guidance: "Guidance added",
  tool_result: "Using action output",
  trace: "Using trace evidence",
  workspace: "Using workspace evidence",
  retry: "Retrying failed action",
};

const draftMergeModes: Record<DraftSource, DraftMergeMode> = {
  answer: "append",
  artifact: "append",
  artifact_text: "append",
  changed_file: "append",
  config: "append",
  continuation: "append",
  evidence: "append",
  error: "append",
  file_evidence: "append",
  file_range: "append",
  file_snapshot: "append",
  guidance_receipt: "replace",
  loop_setup: "replace",
  memory: "append",
  previous_message: "replace",
  recent_chat: "replace",
  result: "append",
  run_command: "append",
  skill: "append",
  starter: "replace",
  retry_reply: "replace",
  tool_guidance: "append",
  tool_result: "append",
  trace: "append",
  workspace: "append",
  retry: "append",
};

export function draftSourceLabel(source?: DraftSource): string | undefined {
  return source ? draftSourceLabels[source] : undefined;
}

export function draftMergeMode(source?: DraftSource): DraftMergeMode {
  return source ? draftMergeModes[source] : "append";
}
