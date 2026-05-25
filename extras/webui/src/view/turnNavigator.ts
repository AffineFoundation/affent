import type { TurnState } from "../store/sessionState";
import { summarizePreview } from "./textPreview";
import { buildTurnActivity } from "./turnActivity";

export interface TurnNavSourceItem {
  turn: TurnState;
  turnNumber: number;
}

export interface PendingTurnNavSource {
  text: string;
}

export interface TurnNavViewItem {
  id: string;
  turn?: TurnState;
  turnNumber: number;
  href: string;
  summary: string;
  activityLabel?: string;
  activitySummary?: string;
  activityTone?: string;
  status: string;
  statusLabel: string;
  statusTone: string;
  actionCount: number;
  current: boolean;
  pending: boolean;
  messageAriaLabel: string;
  stepAriaLabel: string;
}

export interface TurnNavigatorView {
  countLabel: string;
  summary: string;
  current?: TurnNavViewItem;
  items: TurnNavViewItem[];
}

export function buildTurnNavigatorView(items: readonly TurnNavSourceItem[], pending?: PendingTurnNavSource): TurnNavigatorView {
  const currentId = pending ? "__pending__" : currentTurnId(items);
  const viewItems: TurnNavViewItem[] = items.map(({ turn, turnNumber }, index) => {
    const continuedAfterLimit = turnContinuedAfterLimit(items, index);
    const continuedIntoTurnNumber = continuedAfterLimit ? items[index + 1]?.turnNumber : undefined;
    const summary = turnSummary(turn);
    const activity = buildTurnActivity(turn, { continuedAfterLimit, continuedIntoTurnNumber });
    const answerDigest = answerDigestForTurn(turn, summary);
    const activityDigest = activity?.digest.summary && activity.digest.summary !== summary
      ? { label: activity.digest.label, summary: summarize(activity.digest.summary, 74), tone: activity.digest.tone }
      : undefined;
    const digest = preferAnswerDigest(activityDigest, answerDigest);
    const activitySummary = digest?.summary;
    const activityLabel = digest?.label;
    const activityTone = digest?.tone;
    const current = turn.id === currentId;
    const actionCount = turn.toolCalls.length;
    const activitySuffix = activitySummary && activityLabel ? ` — ${activityLabel}: ${activitySummary}` : "";
    return {
      id: turn.id,
      turn,
      turnNumber,
      href: `#turn-${turnNumber}`,
      summary,
      activityLabel,
      activitySummary,
      activityTone,
      status: turn.status,
      statusLabel: statusLabel(turn, { continuedAfterLimit, resultLabel: activityLabel }),
      statusTone: turnStatusTone(turn, { continuedAfterLimit }),
      actionCount,
      current,
      pending: false,
      messageAriaLabel: `Message ${turnNumber}: ${summary}${activitySuffix}${current ? " (current)" : ""}`,
      stepAriaLabel: `Jump to message ${turnNumber}: ${summary}${current ? " (current)" : ""}`,
    };
  });
  if (pending) {
    const turnNumber = items.length + 1;
    const summary = summarize(pending.text, 54);
    viewItems.push({
      id: "__pending__",
      turnNumber,
      href: "#pending-turn",
      summary,
      activityLabel: "Waiting",
      activitySummary: "Affent will add the next update here.",
      activityTone: "running",
      status: "running",
      statusLabel: "Sending",
      statusTone: "running",
      actionCount: 0,
      current: true,
      pending: true,
      messageAriaLabel: `Message ${turnNumber}: ${summary} — Waiting: Affent will add the next update here. (current)`,
      stepAriaLabel: `Jump to pending message ${turnNumber}: ${summary} (current)`,
    });
  }

  const current = viewItems.find((item) => item.current);
  return {
    countLabel: `${viewItems.length} ${pluralize("message", viewItems.length)}`,
    summary: turnNavSummary(current),
    current,
    items: viewItems,
  };
}

function currentTurnId(items: readonly TurnNavSourceItem[]): string | undefined {
  const running = items.find(({ turn }) => turn.status === "running");
  return running?.turn.id ?? items.at(-1)?.turn.id;
}

function turnNavSummary(current: TurnNavViewItem | undefined): string {
  if (!current) return "No messages yet";
  const focus = current.pending ? current.summary : current.activitySummary || current.summary;
  const label = current.pending
    ? "Sending"
    : current.statusTone === "running"
      ? "Working"
      : current.statusTone === "error" || current.statusTone === "max_turns"
        ? "Needs attention"
        : current.activityLabel ?? "Latest";
  return `${label} · ${summarize(focus, 74)}`;
}

function preferAnswerDigest(
  activity: { label: string; summary: string; tone: string } | undefined,
  answer: { label: string; summary: string; tone: string } | undefined,
): { label: string; summary: string; tone: string } | undefined {
  if (!activity) return answer;
  if (answer && (activity.label === "Process" || isMechanicalActivitySummary(activity.summary))) return answer;
  return activity;
}

function isMechanicalActivitySummary(summary: string): boolean {
  return /^(\d+ )?actions? completed(?:;|\.|$)/i.test(summary) ||
    summary === "No action details.";
}

function turnSummary(turn: TurnState): string {
  const source = turn.userText || turn.assistantText || turn.endReason || turn.status;
  return summarize(source, 54);
}

function answerDigestForTurn(turn: TurnState, summary: string): { label: string; summary: string; tone: string } | undefined {
  if (!turn.assistantText.trim()) return undefined;
  const answer = summarizePreview(turn.assistantText, 74);
  if (!answer || answer === summary) return undefined;
  return {
    label: turn.messageStreaming ? "Writing" : "Answer",
    summary: answer,
    tone: turn.messageStreaming ? "running" : "success",
  };
}

function summarize(text: string, limit: number): string {
  const singleLine = text.replace(/\s+/g, " ").trim();
  if (singleLine.length <= limit) return singleLine;
  return `${singleLine.slice(0, Math.max(0, limit - 1))}...`;
}

function statusLabel(turn: TurnState, opts: { continuedAfterLimit?: boolean; resultLabel?: string } = {}): string {
  if (turn.status === "running") return "Working";
  if (turn.status === "completed") {
    if (turn.assistantText.trim()) return "Answered";
    return opts.resultLabel && opts.resultLabel !== "Answer" ? opts.resultLabel : "No answer";
  }
  if (turn.status === "max_turns" && opts.continuedAfterLimit) return "Continued";
  if (turn.status === "max_turns") return "Needs answer";
  if (turn.status === "error" || turn.error || turn.toolCalls.some((call) => call.status === "error")) return "Issue";
  if (turn.status === "cancelled") return "Stopped";
  return turn.status;
}

function turnStatusTone(turn: TurnState, opts: { continuedAfterLimit?: boolean } = {}): string {
  if (opts.continuedAfterLimit) return "muted";
  if (turnNeedsAttention(turn)) return "error";
  if (turn.status === "max_turns") return "max_turns";
  return turn.status;
}

function turnNeedsAttention(turn: TurnState, opts: { continuedAfterLimit?: boolean } = {}): boolean {
  if (opts.continuedAfterLimit) return false;
  if (turn.status === "error" || turn.error) return true;
  if (turn.status === "max_turns") return true;
  if (turn.status === "completed" && turn.assistantText.trim()) return false;
  return turn.toolCalls.some((call) => call.status === "error");
}

function turnContinuedAfterLimit(items: readonly TurnNavSourceItem[], index: number): boolean {
  return items[index]?.turn.status === "max_turns" && index < items.length - 1;
}

function pluralize(label: string, count: number): string {
  return count === 1 ? label : `${label}s`;
}
