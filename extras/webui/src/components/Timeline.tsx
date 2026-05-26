import { useEffect, useMemo, useRef, useState, type CSSProperties, type RefObject } from "react";
import type { SessionState } from "../store/sessionState";
import type { UseAsDraft } from "../view/draftSource";
import { hasIssueContext } from "../view/issueContext";
import { countMatchingTurns, countTurnsByMode, turnMatchesFilter, type TimelineFilterMode } from "../view/timelineFilter";
import { formatLoadingChatTitle } from "../view/sessionList";
import { TurnCard } from "./TurnCard";
import { TurnNavigator } from "./TurnNavigator";
import { CopyButton } from "./CopyButton";

const filterModes: { mode: TimelineFilterMode; label: string }[] = [
  { mode: "all", label: "All" },
  { mode: "errors", label: "Issues" },
  { mode: "tools", label: "Actions" },
  { mode: "messages", label: "Messages" },
  { mode: "artifacts", label: "Files" },
  { mode: "memory", label: "Memory" },
  { mode: "truncated", label: "Large outputs" },
  { mode: "repaired", label: "Auto-fixed" },
];

// The conversation is the primary product surface. Search and filters stay
// available, but are framed as a plain find tool instead of a trace console.
export function Timeline({
  session,
  sessionId,
  pendingMessage,
  guidanceReceipts = [],
  scrollRootRef,
  onOpenArtifact,
  onUseAsDraft,
  savedChatCount = 0,
  latestChat,
  onOpenLatestChat,
  initialHistoryFocus = "latest",
  loadingHistory = false,
  sessionTitle,
}: {
  session: SessionState;
  sessionId?: string;
  pendingMessage?: PendingMessageView;
  guidanceReceipts?: readonly GuidanceReceiptView[];
  scrollRootRef?: RefObject<HTMLElement | null>;
  onOpenArtifact?: (path: string) => void;
  onUseAsDraft?: UseAsDraft;
  savedChatCount?: number;
  latestChat?: LatestChatShortcut;
  onOpenLatestChat?: () => void;
  initialHistoryFocus?: "answer" | "latest";
  loadingHistory?: boolean;
  sessionTitle?: string;
}) {
  const endRef = useRef<HTMLDivElement | null>(null);
  const latestAnswerRef = useRef<HTMLDivElement | null>(null);
  const activityCount = session.events.length + (pendingMessage ? 1 : 0) + guidanceReceipts.length;
  const prevActivityCount = useRef(activityCount);
  const prevSessionId = useRef(sessionId);
  const focusAnswerOnNextHistory = useRef(initialHistoryFocus === "answer");
  const userBrowsedHistory = useRef(false);
  const autoFollowPaused = useRef(false);
  const pointerSelecting = useRef(false);
  const touchStartY = useRef<number | undefined>(undefined);
  const [following, setFollowing] = useState(true);
  const [newActivity, setNewActivity] = useState(false);
  const [filterMode, setFilterMode] = useState<TimelineFilterMode>("all");
  const [searchQuery, setSearchQuery] = useState("");
  const [toolsOpen, setToolsOpen] = useState(false);
  const [filtersOpen, setFiltersOpen] = useState(false);
  const searchText = searchQuery.trim();
  const activeFilterLabel = filterModes.find(({ mode }) => mode === filterMode)?.label ?? "All";
  const filtered = filterMode !== "all" || searchText !== "";
  const searchAvailable = filtered || toolsOpen;
  const filter = useMemo(() => ({ mode: filterMode, query: searchQuery }), [filterMode, searchQuery]);
  const visibleTurns = useMemo(
    () => session.turns.filter((turn) => turnMatchesFilter(turn, session.events, filter)),
    [filter, session.events, session.turns],
  );
  const visibleTurnNav = useMemo(
    () => visibleTurns.map((turn) => ({ turn, turnNumber: session.turns.indexOf(turn) + 1 })),
    [session.turns, visibleTurns],
  );
  const pendingFollowUp = pendingMessage?.kind === "task" && session.turns.length > 0 ? pendingMessage.text : undefined;
  const singleLiveTurn = session.status === "running" && session.turns.length === 1 && !pendingFollowUp;
  const conversationMapAvailable = Boolean(session.turns.length > 1 || pendingFollowUp || (!singleLiveTurn && hasIssueContext(session)));
  const showConversationMap = conversationMapAvailable && (visibleTurnNav.length > 0 || filtered);
  const compactConversationMap = session.status === "running" && visibleTurnNav.length === 1 && !filtered;
  const canFindInChat = showConversationMap || hasIssueContext(session);
  const matchingTurns = useMemo(
    () => countMatchingTurns(session.turns, session.events, filter),
    [filter, session.events, session.turns],
  );
  const filterCounts = useMemo(
    () => countTurnsByMode(session.turns, session.events, filterModes.map(({ mode }) => mode), searchQuery),
    [searchQuery, session.events, session.turns],
  );

  useEffect(() => {
    if (prevSessionId.current === sessionId) return;
    prevSessionId.current = sessionId;
    prevActivityCount.current = activityCount;
    focusAnswerOnNextHistory.current = initialHistoryFocus === "answer";
    userBrowsedHistory.current = false;
    autoFollowPaused.current = false;
    pointerSelecting.current = false;
    latestAnswerRef.current = null;
    setFollowing(true);
    setNewActivity(false);
    setFilterMode("all");
    setSearchQuery("");
    setToolsOpen(false);
    setFiltersOpen(false);
  }, [activityCount, initialHistoryFocus, sessionId]);

  useEffect(() => {
    if (filtered) setToolsOpen(true);
  }, [filtered]);

  useEffect(() => {
    if (filterMode !== "all") setFiltersOpen(true);
  }, [filterMode]);

  useEffect(() => {
    const scrollRoot = scrollRootRef?.current;
    const shouldIgnorePointer = (event: Event) => {
      if (event.type !== "pointerdown") return false;
      const target = event.target;
      if (!(target instanceof HTMLElement)) return false;
      return Boolean(target.closest("button, a, input, textarea, select, summary, [role='button']"));
    };
    const distanceToLatest = () => latestDistance(scrollRoot);
    const onTouchStart = (event: Event) => {
      const touchEvent = event as TouchEvent;
      touchStartY.current = touchEvent.touches[0]?.clientY;
    };
    const markUserBrowsing = (event: Event) => {
      if (shouldIgnorePointer(event)) return;
      if (pointerSelecting.current || hasActiveTextSelection()) {
        if (event.type !== "pointerdown") return;
      }
      if (isForwardOverscrollAtLatest(event, distanceToLatest(), touchStartY.current)) return;
      userBrowsedHistory.current = true;
      autoFollowPaused.current = true;
      if (event.type === "pointerdown") {
        pointerSelecting.current = true;
        return;
      }
      setFollowing(false);
    };
    const onPointerUp = () => {
      pointerSelecting.current = false;
    };
    const onSelectionChange = () => {
      if (hasActiveTextSelection()) {
        userBrowsedHistory.current = true;
        autoFollowPaused.current = true;
      }
    };
    const onScroll = () => {
      if (pointerSelecting.current || hasActiveTextSelection()) {
        return;
      }
      if (!userBrowsedHistory.current) return;
      const distance = distanceToLatest();
      if (distance < 180) autoFollowPaused.current = false;
      setFollowing(distance < 180);
      if (distance < 180) setNewActivity(false);
    };
    const target: Window | HTMLElement = scrollRoot ?? window;
    target.addEventListener("wheel", markUserBrowsing, { passive: true });
    target.addEventListener("touchstart", onTouchStart, { passive: true });
    target.addEventListener("touchmove", markUserBrowsing, { passive: true });
    target.addEventListener("pointerdown", markUserBrowsing, { passive: true });
    target.addEventListener("scroll", onScroll, { passive: true });
    window.addEventListener("pointerup", onPointerUp, { passive: true });
    document.addEventListener("selectionchange", onSelectionChange);
    return () => {
      target.removeEventListener("wheel", markUserBrowsing);
      target.removeEventListener("touchstart", onTouchStart);
      target.removeEventListener("touchmove", markUserBrowsing);
      target.removeEventListener("pointerdown", markUserBrowsing);
      target.removeEventListener("scroll", onScroll);
      window.removeEventListener("pointerup", onPointerUp);
      document.removeEventListener("selectionchange", onSelectionChange);
    };
  }, [scrollRootRef]);

  useEffect(() => {
    if (activityCount === prevActivityCount.current) return;
    const hasNewActivity = activityCount > prevActivityCount.current;
    prevActivityCount.current = activityCount;
    if (!hasNewActivity) return;
    const selectingText = pointerSelecting.current || hasActiveTextSelection();
    if (selectingText) {
      userBrowsedHistory.current = true;
      autoFollowPaused.current = true;
      setNewActivity(true);
      return;
    }
    const answerTarget = latestAnswerRef.current;
    const shouldOpenAtAnswer =
      focusAnswerOnNextHistory.current &&
      !pendingMessage &&
      !filtered &&
      session.status !== "running" &&
      Boolean(answerTarget);
    if (shouldOpenAtAnswer) {
      focusAnswerOnNextHistory.current = false;
      answerTarget?.scrollIntoView?.({ behavior: "auto", block: "start" });
      const scrollRoot = scrollRootRef?.current;
      const scrollableDistance = scrollRoot
        ? scrollRoot.scrollHeight - scrollRoot.clientHeight
        : document.documentElement.scrollHeight - window.innerHeight;
      if (scrollableDistance > 180) {
        autoFollowPaused.current = true;
        setFollowing(true);
      }
      return;
    }
    focusAnswerOnNextHistory.current = false;
    if (following && !autoFollowPaused.current) {
      endRef.current?.scrollIntoView?.({ behavior: "auto", block: "end" });
    } else {
      setNewActivity(true);
    }
  }, [activityCount, filtered, following, pendingMessage, scrollRootRef, session.status]);

  function jumpToLive() {
    userBrowsedHistory.current = false;
    autoFollowPaused.current = false;
    pointerSelecting.current = false;
    touchStartY.current = undefined;
    setFollowing(true);
    setNewActivity(false);
    endRef.current?.scrollIntoView?.({ behavior: "auto", block: "end" });
  }

  function resetFilter() {
    setFilterMode("all");
    setSearchQuery("");
    setFiltersOpen(false);
  }

  if (session.turns.length === 0 && !pendingMessage) {
    if (loadingHistory) {
      return (
        <section className="flow-turn intro-turn timeline-loading" data-testid="timeline-loading" aria-busy="true">
          <div className="conversation-turn">
            <div className="assistant-cluster">
              <div className="assistant-name">Affent</div>
              <div className="flow-step flow-step-assistant">
                <div className="flow-text intro-copy">
                  <div className="intro-heading">
                    <strong>{formatLoadingChatTitle(sessionTitle)}</strong>
                    <span>Loading the conversation before showing it.</span>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </section>
      );
    }
    const hasSavedChats = savedChatCount > 0;
    return (
      <section className="flow-turn intro-turn" data-testid="timeline-empty">
        <div className="conversation-turn">
          <div className="assistant-cluster">
            <div className="assistant-name">Affent</div>
            <div className="flow-step flow-step-assistant">
              <div className="flow-text intro-copy">
                <div className="intro-heading">
                  <strong>What should we work on?</strong>
                  <span>
                    {hasSavedChats
                      ? `Start a new task below, or reopen ${savedChatCount === 1 ? "the saved chat" : "recent work"} from Chats.`
                      : "Type a task below, or start from a draft and edit it before sending."}
                  </span>
                </div>
                {hasSavedChats && latestChat && onOpenLatestChat ? (
                  <div className="intro-latest-chat" data-testid="intro-latest-chat">
                    <span>Recent chat</span>
                    <strong>{latestChat.title}</strong>
                    {latestChat.meta ? <small>{latestChat.meta}</small> : null}
                    <div className="intro-latest-chat-actions">
                      {latestChat.draft && onUseAsDraft ? (
                        <button type="button" onClick={() => onUseAsDraft(latestChat.draft ?? latestChat.title, "recent_chat")}>
                          Use as draft
                        </button>
                      ) : null}
                      <button type="button" onClick={onOpenLatestChat}>
                        Open latest chat
                      </button>
                    </div>
                  </div>
                ) : null}
                {onUseAsDraft ? <IntroStarterPanel onUseAsDraft={onUseAsDraft} /> : null}
              </div>
            </div>
          </div>
        </div>
      </section>
    );
  }
  if (session.turns.length === 0 && pendingMessage) {
    return (
      <div className="timeline" data-testid="timeline" data-pending-first="true">
        <PendingTurn message={pendingMessage} followUp={false} />
        <div ref={endRef} className="timeline-end" aria-hidden="true" />
      </div>
    );
  }
  const showJumpToLatest = newActivity && (!following || session.status === "completed");
  const loadingBanner =
    loadingHistory && session.turns.length > 0 ? (
      <section className="timeline-loading-banner" data-testid="timeline-loading" aria-busy="true">
        <strong>{formatLoadingChatTitle(sessionTitle)}</strong>
        <span>Keeping this chat visible while the new one loads.</span>
      </section>
    ) : null;
  return (
    <>
      {showJumpToLatest ? (
        <button type="button" className="jump-live" data-new={newActivity} onClick={jumpToLive}>
          Jump to latest
        </button>
      ) : null}
      {loadingBanner}
      {showConversationMap ? (
        <div className="conversation-map" data-testid="conversation-map" data-density={compactConversationMap ? "compact" : "normal"}>
          <TurnNavigator
            items={visibleTurnNav}
            pendingTask={pendingFollowUp}
            searchQuery={searchQuery}
            findActive={filtered || toolsOpen}
            onOpenFind={canFindInChat ? () => setToolsOpen(true) : undefined}
            compact={compactConversationMap}
          />
          {searchAvailable ? (
            <TimelineFindPanel
              open={toolsOpen}
              filtered={filtered}
              matchingTurns={matchingTurns}
              turnCount={session.turns.length}
              searchQuery={searchQuery}
              searchText={searchText}
              filterMode={filterMode}
              filtersOpen={filtersOpen}
              filterCounts={filterCounts}
              onOpenChange={setToolsOpen}
              onSearchChange={setSearchQuery}
              onFilterOpenChange={setFiltersOpen}
              onFilterChange={setFilterMode}
              onReset={resetFilter}
            />
          ) : null}
        </div>
      ) : null}
      <div className="timeline" data-testid="timeline">
        {visibleTurns.length > 0 ? (
          <>
            {visibleTurns.map((turn) => {
              const turnIndex = session.turns.indexOf(turn);
              const isLatestTurn = turn === session.turns.at(-1);
              return (
                <div
                  key={turn.id}
                  ref={(node) => {
                    if (isLatestTurn) latestAnswerRef.current = turn.assistantText.trim() ? node : null;
                  }}
                  className="timeline-turn-anchor"
                >
                  <TurnCard
                    turn={turn}
                    turnNumber={turnIndex + 1}
                    anchorId={`turn-${turnIndex + 1}`}
                    events={session.events}
                    searchQuery={searchQuery}
                    sessionId={sessionId}
                    isLatest={isLatestTurn}
                    showHeader={session.turns.length > 1}
                    showBoundary={false}
                    forceWorkDetails={filterMode !== "all" && filterMode !== "messages"}
                    onOpenArtifact={onOpenArtifact}
                    onUseAsDraft={onUseAsDraft}
                  />
                </div>
              );
            })}
            {guidanceReceipts.map((receipt) => (
              <GuidanceReceipt key={receipt.id} receipt={receipt} onUseAsDraft={onUseAsDraft} />
            ))}
            {pendingMessage ? <PendingTurn message={pendingMessage} followUp={Boolean(pendingFollowUp)} /> : null}
          </>
        ) : (
          <div className="timeline-empty filtered" data-testid="timeline-filter-empty">
            <h3>No matching messages</h3>
            <p>
              {searchText ? `Search "${searchText}"` : activeFilterLabel} did not match this session.
            </p>
            {!searchAvailable ? (
              <button type="button" className="secondary-action" onClick={resetFilter}>
                Reset filters
              </button>
            ) : null}
          </div>
        )}
        <div ref={endRef} className="timeline-end" aria-hidden="true" />
      </div>
    </>
  );
}

function TimelineFindPanel({
  open,
  filtered,
  matchingTurns,
  turnCount,
  searchQuery,
  searchText,
  filterMode,
  filtersOpen,
  filterCounts,
  onOpenChange,
  onSearchChange,
  onFilterOpenChange,
  onFilterChange,
  onReset,
}: {
  open: boolean;
  filtered: boolean;
  matchingTurns: number;
  turnCount: number;
  searchQuery: string;
  searchText: string;
  filterMode: TimelineFilterMode;
  filtersOpen: boolean;
  filterCounts: Record<TimelineFilterMode, number>;
  onOpenChange: (open: boolean) => void;
  onSearchChange: (query: string) => void;
  onFilterOpenChange: (open: boolean) => void;
  onFilterChange: (mode: TimelineFilterMode) => void;
  onReset: () => void;
}) {
  const filterOpen = filtersOpen || filterMode !== "all";
  const inputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    if (!open) return;
    inputRef.current?.focus({ preventScroll: true });
  }, [open]);

  return (
    <details
      className="timeline-toolbar"
      data-testid="timeline-toolbar"
      open={open}
      onToggle={(event) => onOpenChange(event.currentTarget.open)}
    >
      <summary>
        <span>{searchText ? `Search "${searchText}"` : "Search in chat"}</span>
        {open || filtered ? (
          <span className="timeline-match-count" data-testid="timeline-match-count">
            {matchingTurns}/{turnCount} messages
          </span>
        ) : null}
      </summary>
      {open ? (
        <div className="timeline-toolbox">
          <label className="timeline-search">
            <span>Search messages, sources, or output</span>
            <input
              ref={inputRef}
              value={searchQuery}
              onChange={(event) => onSearchChange(event.target.value)}
              placeholder="Type to search"
              data-testid="timeline-search"
            />
          </label>
          <details
            className="timeline-advanced"
            data-testid="timeline-advanced-filter"
            open={filterOpen}
            onToggle={(event) => onFilterOpenChange(event.currentTarget.open)}
          >
            <summary>Filters</summary>
            {filterOpen ? (
              <div className="timeline-filter" role="group" aria-label="Conversation filter">
                {filterModes.map(({ mode, label }) => (
                  <button
                    key={mode}
                    type="button"
                    aria-pressed={filterMode === mode}
                    onClick={() => onFilterChange(mode)}
                  >
                    <span>{label}</span>
                    <span className="filter-count" aria-hidden="true">
                      {filterCounts[mode]}
                    </span>
                  </button>
                ))}
              </div>
            ) : null}
          </details>
          {filtered ? (
            <button type="button" className="timeline-reset" onClick={onReset}>
              Show all
            </button>
          ) : null}
        </div>
      ) : null}
    </details>
  );
}

function IntroStarterPanel({ onUseAsDraft }: { onUseAsDraft: UseAsDraft }) {
  const [activeIndex, setActiveIndex] = useState(0);
  const activeStarter = starterDrafts[activeIndex] ?? starterDrafts[0];

  function useDraft(starter = activeStarter) {
    onUseAsDraft(starter.prompt, "starter");
  }

  function previewDraft(index: number) {
    setActiveIndex(index);
  }

  return (
    <div className="intro-launch" aria-label="Starter drafts">
      <div className="intro-starters">
        {starterDrafts.map((starter, index) => (
          <button
            key={starter.title}
            type="button"
            aria-pressed={index === activeIndex}
            style={{ "--starter-index": index } as CSSProperties}
            onFocus={() => previewDraft(index)}
            onMouseEnter={() => previewDraft(index)}
            onClick={() => previewDraft(index)}
          >
            <span>{starter.title}</span>
            <small>{starter.preview}</small>
          </button>
        ))}
      </div>
      <div className="intro-draft-preview" data-testid="starter-preview" aria-live="polite">
        <span>Draft preview</span>
        <p>{activeStarter.prompt}</p>
        <button type="button" onClick={() => useDraft()}>
          Use draft
        </button>
      </div>
    </div>
  );
}

const starterDrafts = [
  {
    title: "Inspect project",
    preview: "Map files, risks, and next steps.",
    prompt: "Inspect this project and summarize the important files, risks, and next steps.",
  },
  {
    title: "Fix a failure",
    preview: "Find the cause and smallest fix.",
    prompt: "Find the failing test or execution error, explain the cause, and propose the smallest fix.",
  },
  {
    title: "Investigate issue",
    preview: "Check errors, slow steps, and files.",
    prompt: "Investigate the current issue, call out the likely cause, and propose the next concrete step.",
  },
];

export interface PendingMessageView {
  text: string;
  kind: "task" | "guidance";
}

export interface LatestChatShortcut {
  title: string;
  draft?: string;
  meta?: string;
}

export interface GuidanceReceiptView {
  id: number;
  text: string;
}

function GuidanceReceipt({
  receipt,
  onUseAsDraft,
}: {
  receipt: GuidanceReceiptView;
  onUseAsDraft?: UseAsDraft;
}) {
  return (
    <section className="flow-turn guidance-receipt" data-testid="guidance-receipt" aria-label="Guidance sent">
      <div className="conversation-turn">
        <div className="flow-step flow-step-user" role="group" aria-label="Sent guidance">
          <span className="pending-guidance-label">Guidance sent</span>
          <div className="flow-text">{receipt.text}</div>
          <div className="message-actions">
            <CopyButton label="Copy guidance" value={receipt.text} className="message-action" />
            {onUseAsDraft ? (
              <button type="button" className="message-action" onClick={() => onUseAsDraft(receipt.text, "guidance_receipt")}>
                Edit guidance
              </button>
            ) : null}
          </div>
        </div>
        <div className="assistant-cluster">
          <div className="assistant-name">Affent</div>
          <div className="guidance-receipt-note">
            <span>Affent will use this in the current run.</span>
          </div>
        </div>
      </div>
    </section>
  );
}

function PendingTurn({ message, followUp }: { message: PendingMessageView; followUp: boolean }) {
  const text = message.text;
  const isGuidance = message.kind === "guidance";
  const responseLabel = isGuidance
    ? "Applying your guidance to the current run."
    : followUp
      ? "Waiting for the next update in this chat."
      : "Preparing the first update.";
  return (
    <section
      id="pending-turn"
      className="flow-turn pending-turn"
      data-testid="pending-turn"
      data-kind={message.kind}
      data-status="running"
      aria-live="polite"
    >
      <header className="flow-turn-head">
        <div className="turn-title-group">
          <div className="turn-index">{isGuidance ? "Guidance" : "You"}</div>
          <div className="turn-title" data-testid="turn-title" title={text}>
            {summarize(text, 72)}
          </div>
        </div>
        <div className="flow-status">
          <span className="pulse-dot" data-status="running" aria-hidden="true" />
          <span>{isGuidance ? "Sending" : "Starting"}</span>
        </div>
      </header>
      <div className="conversation-turn">
        <div className="flow-step flow-step-user" role="group" aria-label={isGuidance ? "Guidance for current run" : "You message"}>
          {isGuidance ? <span className="pending-guidance-label">Live guidance</span> : null}
          <div className="flow-text">{text}</div>
          <div className="message-actions">
            <CopyButton label={isGuidance ? "Copy guidance" : "Copy message"} value={text} className="message-action" />
          </div>
        </div>
        <div className="assistant-cluster">
          <div className="assistant-name">Affent</div>
          <div className="pending-response">
            <span className="pending-dots" aria-hidden="true">
              <i />
              <i />
              <i />
            </span>
            <span>{responseLabel}</span>
          </div>
        </div>
      </div>
    </section>
  );
}

function latestDistance(scrollRoot?: HTMLElement | null): number {
  if (scrollRoot) return scrollRoot.scrollHeight - scrollRoot.scrollTop - scrollRoot.clientHeight;
  return document.documentElement.scrollHeight - window.scrollY - window.innerHeight;
}

function isForwardOverscrollAtLatest(event: Event, distanceToLatest: number, touchStartY?: number): boolean {
  if (distanceToLatest > 24) return false;
  if (typeof WheelEvent !== "undefined" && event instanceof WheelEvent) return event.deltaY > 0;
  if ("deltaY" in event && typeof event.deltaY === "number") return event.deltaY > 0;
  if (typeof TouchEvent !== "undefined" && event instanceof TouchEvent) {
    const currentY = event.touches[0]?.clientY;
    return touchStartY != null && currentY != null && currentY < touchStartY;
  }
  return false;
}

function hasActiveTextSelection(): boolean {
  const selection = document.getSelection?.();
  return Boolean(selection && !selection.isCollapsed && selection.toString().trim());
}

function summarize(text: string, limit: number): string {
  const singleLine = text.replace(/\s+/g, " ").trim();
  if (singleLine.length <= limit) return singleLine;
  return `${singleLine.slice(0, Math.max(0, limit - 1))}...`;
}
