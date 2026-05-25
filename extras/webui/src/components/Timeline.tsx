import { useEffect, useMemo, useRef, useState, type CSSProperties, type RefObject } from "react";
import type { SessionState } from "../store/sessionState";
import type { UseAsDraft } from "../view/draftSource";
import { hasReviewContext } from "../view/reviewContext";
import { countMatchingTurns, countTurnsByMode, turnMatchesFilter, type TimelineFilterMode } from "../view/timelineFilter";
import { TurnCard } from "./TurnCard";
import { TurnNavigator } from "./TurnNavigator";

const filterModes: { mode: TimelineFilterMode; label: string }[] = [
  { mode: "all", label: "All" },
  { mode: "errors", label: "Needs attention" },
  { mode: "tools", label: "With actions" },
  { mode: "messages", label: "Chat text" },
  { mode: "artifacts", label: "Files" },
  { mode: "truncated", label: "Large output" },
  { mode: "repaired", label: "Runtime fixes" },
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
  const frozenSelectionScrollTop = useRef<number | undefined>(undefined);
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
  const showConversationMap = Boolean(session.turns.length > 1 || pendingFollowUp || hasReviewContext(session)) && visibleTurnNav.length > 0;
  const canFindInChat = showConversationMap || hasReviewContext(session);
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
    const hasActiveSelection = () => {
      const selection = document.getSelection?.();
      return Boolean(selection && !selection.isCollapsed && selection.toString().trim());
    };
    const shouldIgnorePointer = (event: Event) => {
      if (event.type !== "pointerdown") return false;
      const target = event.target;
      if (!(target instanceof HTMLElement)) return false;
      return Boolean(target.closest("button, a, input, textarea, select, summary, [role='button']"));
    };
    const markUserBrowsing = (event: Event) => {
      if (shouldIgnorePointer(event)) return;
      userBrowsedHistory.current = true;
      autoFollowPaused.current = true;
      if (event.type === "pointerdown") {
        pointerSelecting.current = true;
        frozenSelectionScrollTop.current = currentScrollTop(scrollRoot);
        return;
      }
      setFollowing(false);
    };
    const onPointerUp = () => {
      pointerSelecting.current = false;
      if (!hasActiveSelection()) frozenSelectionScrollTop.current = undefined;
    };
    const onSelectionChange = () => {
      if (!hasActiveSelection() && !pointerSelecting.current) frozenSelectionScrollTop.current = undefined;
    };
    const onScroll = () => {
      if (pointerSelecting.current || hasActiveSelection()) return;
      if (!userBrowsedHistory.current) return;
      const distance = scrollRoot
        ? scrollRoot.scrollHeight - scrollRoot.scrollTop - scrollRoot.clientHeight
        : document.documentElement.scrollHeight - window.scrollY - window.innerHeight;
      if (distance < 180) autoFollowPaused.current = false;
      setFollowing(distance < 180);
      if (distance < 180) setNewActivity(false);
    };
    const target: Window | HTMLElement = scrollRoot ?? window;
    target.addEventListener("wheel", markUserBrowsing, { passive: true });
    target.addEventListener("touchmove", markUserBrowsing, { passive: true });
    target.addEventListener("pointerdown", markUserBrowsing, { passive: true });
    target.addEventListener("scroll", onScroll, { passive: true });
    window.addEventListener("pointerup", onPointerUp, { passive: true });
    document.addEventListener("selectionchange", onSelectionChange);
    return () => {
      target.removeEventListener("wheel", markUserBrowsing);
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
    const selection = document.getSelection?.();
    const selectingText = pointerSelecting.current || Boolean(selection && !selection.isCollapsed && selection.toString().trim());
    if (selectingText) restoreFrozenSelectionScroll(scrollRootRef?.current, frozenSelectionScrollTop.current);
    const answerTarget = latestAnswerRef.current;
    const shouldOpenAtAnswer =
      focusAnswerOnNextHistory.current &&
      !pendingMessage &&
      !filtered &&
      session.status !== "running" &&
      Boolean(answerTarget);
    if (shouldOpenAtAnswer && !selectingText) {
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
    if (following && !autoFollowPaused.current && !selectingText) {
      endRef.current?.scrollIntoView?.({ behavior: "auto", block: "end" });
    } else {
      if (!selectingText) setNewActivity(true);
    }
  }, [activityCount, filtered, following, pendingMessage, scrollRootRef, session.status]);

  function jumpToLive() {
    userBrowsedHistory.current = false;
    autoFollowPaused.current = false;
    pointerSelecting.current = false;
    frozenSelectionScrollTop.current = undefined;
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
                  <button type="button" className="intro-latest-chat" onClick={onOpenLatestChat}>
                    <span>Latest chat</span>
                    <strong>{latestChat.title}</strong>
                    {latestChat.meta ? <small>{latestChat.meta}</small> : null}
                    <b>Open latest chat</b>
                  </button>
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
      <div className="timeline" data-testid="timeline">
        <PendingTurn message={pendingMessage} followUp={false} />
        <div ref={endRef} className="timeline-end" aria-hidden="true" />
      </div>
    );
  }
  return (
    <>
      {!following || newActivity ? (
        <button type="button" className="jump-live" data-new={newActivity} onClick={jumpToLive}>
          {newActivity ? "New activity - jump to latest" : "Back to latest"}
        </button>
      ) : null}
      {showConversationMap ? (
        <div className="conversation-map" data-testid="conversation-map">
          <TurnNavigator
            items={visibleTurnNav}
            pendingTask={pendingFollowUp}
            searchQuery={searchQuery}
            findActive={filtered || toolsOpen}
            onOpenFind={canFindInChat ? () => setToolsOpen(true) : undefined}
          />
        </div>
      ) : null}
      <div className="timeline" data-testid="timeline">
        {visibleTurns.length > 0 ? (
          <>
            {visibleTurns.map((turn) => {
              const turnIndex = session.turns.indexOf(turn);
              const isLatestTurn = turn === session.turns.at(-1);
              const anchorRef = isLatestTurn && turn.assistantText.trim() ? latestAnswerRef : undefined;
              return (
                <div key={turn.id} ref={anchorRef} className="timeline-turn-anchor">
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
            <button type="button" className="secondary-action" onClick={resetFilter}>
              Reset filters
            </button>
          </div>
        )}
        <div ref={endRef} className="timeline-end" aria-hidden="true" />
      </div>
      {searchAvailable ? (
        <details
          className="timeline-toolbar"
          data-testid="timeline-toolbar"
          open={toolsOpen}
          onToggle={(event) => setToolsOpen(event.currentTarget.open)}
        >
          <summary>
            <span>Find in chat</span>
            {toolsOpen || filtered ? (
              <span className="timeline-match-count" data-testid="timeline-match-count">
                {matchingTurns}/{session.turns.length} messages
              </span>
            ) : null}
          </summary>
          {toolsOpen ? (
            <div className="timeline-toolbox">
              <label className="timeline-search">
                <span>Search messages and outputs</span>
                <input
                  value={searchQuery}
                  onChange={(event) => setSearchQuery(event.target.value)}
                  placeholder="Message, source, output"
                  data-testid="timeline-search"
                />
              </label>
              <details
                className="timeline-advanced"
                data-testid="timeline-advanced-filter"
                open={filtersOpen || filterMode !== "all"}
                onToggle={(event) => setFiltersOpen(event.currentTarget.open)}
              >
                <summary>Filter results</summary>
                {filtersOpen || filterMode !== "all" ? (
                  <div className="timeline-filter" role="group" aria-label="Conversation filter">
                    {filterModes.map(({ mode, label }) => (
                      <button
                        key={mode}
                        type="button"
                        aria-pressed={filterMode === mode}
                        onClick={() => setFilterMode(mode)}
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
                <button type="button" className="timeline-reset" onClick={resetFilter}>
                  Show all
                </button>
              ) : null}
            </div>
          ) : null}
        </details>
      ) : null}
    </>
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
    prompt: "Find the failing test or runtime error, explain the cause, and propose the smallest fix.",
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
          {onUseAsDraft ? (
            <div className="message-actions">
              <button type="button" className="message-action" onClick={() => onUseAsDraft(receipt.text, "guidance_receipt")}>
                Edit guidance
              </button>
            </div>
          ) : null}
        </div>
        <div className="assistant-cluster">
          <div className="assistant-name">Affent</div>
          <div className="guidance-receipt-note">
            <span>Added to the current turn.</span>
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
    ? "Adding guidance to the live turn."
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
        <div className="flow-step flow-step-user" role="group" aria-label={isGuidance ? "Guidance message" : "You message"}>
          {isGuidance ? <span className="pending-guidance-label">Live guidance</span> : null}
          <div className="flow-text">{text}</div>
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

function currentScrollTop(scrollRoot?: HTMLElement | null): number {
  return scrollRoot ? scrollRoot.scrollTop : window.scrollY;
}

function restoreFrozenSelectionScroll(scrollRoot: HTMLElement | null | undefined, scrollTop: number | undefined) {
  if (scrollTop == null) return;
  if (scrollRoot) {
    scrollRoot.scrollTop = scrollTop;
    return;
  }
  window.scrollTo({ top: scrollTop, behavior: "auto" });
}

function summarize(text: string, limit: number): string {
  const singleLine = text.replace(/\s+/g, " ").trim();
  if (singleLine.length <= limit) return singleLine;
  return `${singleLine.slice(0, Math.max(0, limit - 1))}...`;
}
