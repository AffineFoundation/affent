export type DraftSource =
  | "answer"
  | "artifact"
  | "artifact_match"
  | "artifact_text"
  | "continuation"
  | "evidence"
  | "error"
  | "guidance_receipt"
  | "previous_message"
  | "result"
  | "starter"
  | "tool_guidance"
  | "tool_result"
  | "retry";

export type UseAsDraft = (content: string, source?: DraftSource) => void;
export type DraftMergeMode = "append" | "replace";

const draftSourceLabels: Record<DraftSource, string> = {
  answer: "Continuing from answer",
  artifact: "File added to message",
  artifact_match: "Using matched file lines",
  artifact_text: "Using file text",
  continuation: "Requesting final answer",
  evidence: "Using evidence",
  error: "Continuing after error",
  guidance_receipt: "Editing sent note",
  previous_message: "Editing previous message",
  result: "Continuing from output",
  starter: "Starting from draft",
  tool_guidance: "Using suggested next step",
  tool_result: "Using action output",
  retry: "Retrying failed action",
};

const draftMergeModes: Record<DraftSource, DraftMergeMode> = {
  answer: "append",
  artifact: "append",
  artifact_match: "append",
  artifact_text: "append",
  continuation: "append",
  evidence: "append",
  error: "append",
  guidance_receipt: "replace",
  previous_message: "replace",
  result: "append",
  starter: "replace",
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
