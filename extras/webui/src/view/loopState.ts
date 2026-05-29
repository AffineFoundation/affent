import { EventType } from "../api/events";
import type { SessionLoopState } from "../api/sessions";
import type { NormalizedEvent } from "../normalize/normalizeEvent";

export function mergeLoopStateFromEvents(
  summary: SessionLoopState | undefined,
  events: readonly NormalizedEvent[],
): SessionLoopState | undefined {
  const live = loopStateFromEvents(events);
  if (!live) return summary;
  return {
    ...summary,
    ...live,
    version: summary?.version ?? live.version,
    initial_goal_preview: summary?.initial_goal_preview ?? live.initial_goal_preview,
    initial_plan_label: summary?.initial_plan_label ?? live.initial_plan_label,
    created_at: summary?.created_at ?? live.created_at,
  };
}

export function loopStateFromEvents(events: readonly NormalizedEvent[]): SessionLoopState | undefined {
  let state: SessionLoopState | undefined;
  for (const event of events) {
    if (
      event.type !== EventType.LoopProtocolCalibrationRequest &&
      event.type !== EventType.LoopProtocolCalibration &&
      event.type !== EventType.LoopProtocolActivation &&
      event.type !== EventType.LoopProtocolFeed
    ) {
      continue;
    }
    const data = event.data;
    if (!data || typeof data !== "object") continue;
    state = {
      ...(state ?? { version: 1 }),
      loop_id: readString(data, "loop_id") ?? state?.loop_id,
      status: readString(data, "status") ?? state?.status,
      protocol_path: readString(data, "protocol_path") ?? state?.protocol_path,
      protocol_updates: readNumber(data, "protocol_updates") ?? state?.protocol_updates,
      calibration_questions: readNumber(data, "calibration_questions") ?? state?.calibration_questions,
      last_calibration_question_preview: readString(data, "last_calibration_question_preview") ?? state?.last_calibration_question_preview,
      calibration_answers: readNumber(data, "calibration_answers") ?? state?.calibration_answers,
      last_calibration_answer_preview: readString(data, "last_calibration_answer_preview") ?? state?.last_calibration_answer_preview,
      protocol_feeds: readNumber(data, "protocol_feeds") ?? state?.protocol_feeds,
      last_protocol_feed_mode: readString(data, "mode") ?? state?.last_protocol_feed_mode,
      last_plan_label: readString(data, "plan_label") ?? state?.last_plan_label,
      last_plan_step_index: readNumber(data, "plan_current_step_index") ?? state?.last_plan_step_index,
      last_plan_step_status: readString(data, "plan_current_step_status") ?? state?.last_plan_step_status,
      last_plan_step: readString(data, "plan_current_step") ?? state?.last_plan_step,
    };
  }
  return state;
}

function readString(data: object, key: string): string | undefined {
  const value = (data as Record<string, unknown>)[key];
  return typeof value === "string" && value.trim() ? value.trim() : undefined;
}

function readNumber(data: object, key: string): number | undefined {
  const value = (data as Record<string, unknown>)[key];
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}
