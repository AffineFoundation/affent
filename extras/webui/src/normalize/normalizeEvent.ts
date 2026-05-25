import { EventType, type RawEvent } from "../api/events";

const KNOWN_TYPES = new Set<string>(Object.values(EventType));

/**
 * A wire event after the normalization layer. Live SSE, persisted
 * history, and imported traces all reduce to this shape so the store,
 * view model, and components never branch on transport.
 */
export interface NormalizedEvent {
  /** Per-session sequence id from the wire (can repeat across restarts). */
  id: number;
  /** Raw type string, always preserved even when unknown to this build. */
  type: string;
  /** True when `type` is one Affent emits today. Unknown events are kept,
   * not dropped, so a newer server doesn't blank the timeline. */
  known: boolean;
  /** turn_id when the payload carries one; undefined for tool.result and
   * trace.meta, which associate via call_id / stream position instead. */
  turnId?: string;
  /** The event payload (JSON-parsed only). Narrow it by `type`. */
  data: unknown;
  /** The original wire event, kept for inline trace views and exact round-tripping. */
  raw: RawEvent;
}

function readTurnId(data: unknown): string | undefined {
  if (data && typeof data === "object" && "turn_id" in data) {
    const v = (data as { turn_id: unknown }).turn_id;
    if (typeof v === "string" && v !== "") return v;
  }
  return undefined;
}

/** Normalize one raw wire event. Pure: no I/O, no shared state. */
export function normalizeEvent(raw: RawEvent): NormalizedEvent {
  return {
    id: raw.id,
    type: raw.type,
    known: KNOWN_TYPES.has(raw.type),
    turnId: readTurnId(raw.data),
    data: raw.data,
    raw,
  };
}

/** Normalize a batch (e.g. a history page or an imported trace). */
export function normalizeEvents(raws: readonly RawEvent[]): NormalizedEvent[] {
  return raws.map(normalizeEvent);
}
