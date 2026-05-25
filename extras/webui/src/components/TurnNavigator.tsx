import type { CSSProperties } from "react";
import type { TurnState } from "../store/sessionState";
import { buildTurnNavigatorView } from "../view/turnNavigator";
import { HighlightText } from "./HighlightText";

export interface TurnNavItem {
  turn: TurnState;
  turnNumber: number;
}

export function TurnNavigator({
  items,
  pendingTask,
  searchQuery,
  findActive = false,
  onOpenFind,
}: {
  items: readonly TurnNavItem[];
  pendingTask?: string;
  searchQuery?: string;
  findActive?: boolean;
  onOpenFind?: () => void;
}) {
  if (items.length === 0) return null;
  const view = buildTurnNavigatorView(items, pendingTask ? { text: pendingTask } : undefined);
  return (
    <nav className="turn-navigator" aria-label="Messages" data-testid="turn-navigator">
      <span className="turn-nav-label">
        <span className="turn-nav-label-row">
          <span>Conversation</span>
          {onOpenFind ? (
            <button type="button" className="turn-nav-find" aria-pressed={findActive} onClick={onOpenFind}>
              Find
            </button>
          ) : null}
        </span>
        <small>
          {view.countLabel} · {view.summary}
        </small>
      </span>
      <div className="turn-nav-progress" aria-label="Conversation progress" data-testid="turn-nav-progress">
        {view.items.map((item, index) => {
          return (
            <a
              key={`${item.id}-step`}
              className="turn-nav-step"
              href={item.href}
              data-status={item.statusTone}
              data-current={item.current ? "true" : "false"}
              aria-label={item.stepAriaLabel}
              style={{ "--turn-index": index } as CSSProperties}
            >
              <span>{item.turnNumber}</span>
            </a>
          );
        })}
      </div>
      <div className="turn-nav-glance" aria-label="Conversation summaries" data-testid="turn-nav-glance">
        {view.items.map((item, index) => (
          <a
            key={`${item.id}-glance`}
            className="turn-nav-glance-item"
            href={item.href}
            data-status={item.statusTone}
            data-current={item.current ? "true" : "false"}
            aria-label={item.messageAriaLabel}
            style={{ "--turn-index": index } as CSSProperties}
          >
            <span className="turn-nav-glance-index">{item.turnNumber}</span>
            <span className="turn-nav-glance-copy">
              <strong>
                <HighlightText text={item.summary} query={searchQuery} />
              </strong>
              {item.activitySummary ? (
                <em data-tone={item.activityTone}>
                  {item.activityLabel ? <b>{item.activityLabel}</b> : null}
                  <HighlightText text={item.activitySummary} query={searchQuery} />
                </em>
              ) : null}
            </span>
            <small>{item.current ? `Current · ${item.statusLabel}` : item.statusLabel}</small>
          </a>
        ))}
      </div>
    </nav>
  );
}
