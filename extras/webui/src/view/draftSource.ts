export type DraftSource =
  | "answer"
  | "artifact"
  | "artifact_text"
  | "changed_file"
  | "continuation"
  | "evidence"
  | "error"
  | "file_evidence"
  | "guidance_receipt"
  | "previous_message"
  | "recent_chat"
  | "result"
  | "run_command"
  | "starter"
  | "retry_reply"
  | "tool_guidance"
  | "tool_result"
  | "retry";

export type UseAsDraft = (content: string, source?: DraftSource) => void;
export type DraftMergeMode = "append" | "replace";

const draftSourceLabels: Record<DraftSource, string> = {
  answer: "Continuing from answer",
  artifact: "File added to message",
  artifact_text: "Using file text",
  changed_file: "Using changed file",
  continuation: "Requesting final answer",
  evidence: "Using evidence",
  error: "Continuing after error",
  file_evidence: "Using file evidence",
  guidance_receipt: "Editing sent guidance",
  previous_message: "Editing previous message",
  recent_chat: "Starting from recent chat",
  result: "Continuing from output",
  run_command: "Using command",
  starter: "Starting from draft",
  retry_reply: "Retrying from reply",
  tool_guidance: "Using suggested next step",
  tool_result: "Using action output",
  retry: "Retrying failed action",
};

const draftMergeModes: Record<DraftSource, DraftMergeMode> = {
  answer: "append",
  artifact: "append",
  artifact_text: "append",
  changed_file: "append",
  continuation: "append",
  evidence: "append",
  error: "append",
  file_evidence: "append",
  guidance_receipt: "replace",
  previous_message: "replace",
  recent_chat: "replace",
  result: "append",
  run_command: "append",
  starter: "replace",
  retry_reply: "replace",
  tool_guidance: "append",
  tool_result: "append",
  retry: "append",
};

export function draftSourceLabel(source?: DraftSource): string | undefined {
  return source ? draftSourceLabels[source] : undefined;
}

export function draftMergeMode(source?: DraftSource): DraftMergeMode {
  return source ? draftMergeModes[source] : "append";
}
