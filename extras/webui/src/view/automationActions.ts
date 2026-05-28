export type AutomationActionKind = "loop_setup" | "checkin" | "loop_tick" | "daily";

export function automationActionLabel(kind: AutomationActionKind, busy = false): string {
  if (busy) return kind === "loop_setup" ? "Setting up" : "Scheduling";
  if (kind === "loop_setup") return "Set up long-running loop";
  if (kind === "checkin") return "Schedule 1h check-in";
  if (kind === "loop_tick") return "Schedule 30m loop tick";
  return "Schedule daily check-in";
}
