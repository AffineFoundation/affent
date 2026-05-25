import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import "./styles/index.css";
import { ApiClient, ApiError } from "./api/client";
import {
  cancelSessionTurn,
  createSession,
  getSessionHistory,
  listSessions,
  readSessionArtifact,
  sendSessionMessage,
  streamSessionEvents,
  type SessionSummary,
} from "./api/sessions";
import { ArtifactViewer, type ArtifactViewerState } from "./components/ArtifactViewer";
import { EventType, type RawEvent } from "./api/events";
import { Composer, type ComposerDraft } from "./components/Composer";
import { SessionList } from "./components/SessionList";
import { Timeline, type GuidanceReceiptView, type PendingMessageView } from "./components/Timeline";
import { WorkflowStatus } from "./components/WorkflowStatus";
import { RunDetails } from "./components/RunDetails";
import { completedTurn } from "./fixtures/completedTurn";
import { applyRawEvent, reduceRawEvents } from "./store/reduce";
import { initialSessionState, type SessionState } from "./store/sessionState";
import { deriveWorkflowStatus } from "./store/workflowStatus";
import type { DraftSource } from "./view/draftSource";
import { buildRuntimeCapabilityView, type RuntimeCapabilityView } from "./view/runtimeCapabilities";
import { buildSessionRows } from "./view/sessionList";
import { buildSessionOverview, type SessionOverview } from "./view/sessionOverview";
import { isContinuationPrompt } from "./view/continuationPrompt";

type SurfaceState = "connecting" | "connected" | "live" | "loading" | "demo" | "disconnected" | "error";

interface StatusBanner {
  state: SurfaceState;
  label: string;
  detail?: string;
}

const demoReplayDelayMs = 180;
const historyPageLimit = 500;
const maxHistoryPages = 50;

// The shell stays deliberately thin: transport helpers own HTTP details,
// the reducer owns event interpretation, and UI components receive stable
// view data. This keeps future live SSE, artifact and search work from
// turning App into a protocol parser.
export function App() {
  const client = useMemo(() => new ApiClient({ basePath: import.meta.env.VITE_AFFENT_API_BASE }), []);
  const [status, setStatus] = useState<StatusBanner>({
    state: "connecting",
    label: "Connecting",
  });
  const [sessions, setSessions] = useState<SessionSummary[]>([]);
  const [selectedSessionId, setSelectedSessionId] = useState<string | undefined>();
  const [session, setSession] = useState<SessionState>(() => initialSessionState());
  const [actionBusy, setActionBusy] = useState(false);
  const [pendingMessage, setPendingMessage] = useState<PendingMessageView | undefined>();
  const [guidanceReceipts, setGuidanceReceipts] = useState<GuidanceReceiptView[]>([]);
  const [composerDraft, setComposerDraft] = useState<ComposerDraft | undefined>();
  const [composerFocusSignal, setComposerFocusSignal] = useState(0);
  const [artifact, setArtifact] = useState<ArtifactViewerState>({ state: "idle" });
  const sendInFlightRef = useRef(false);
  const sendFailedRef = useRef(false);
  const streamClosedRef = useRef(false);
  const nextGuidanceReceiptId = useRef(0);
  const conversationScrollRef = useRef<HTMLDivElement | null>(null);
  const demoActive = status.state === "demo";
  const selectedSession = useMemo(
    () => sessions.find((candidate) => candidate.id === selectedSessionId),
    [selectedSessionId, sessions],
  );
  const selectedSessionActive = selectedSession?.active === true;
  const workflow = useMemo(() => deriveWorkflowStatus(session), [session]);
  const runtimeCapabilities = useMemo(
    () => buildRuntimeCapabilityView(selectedSession?.capabilities, { selectedSessionId }),
    [selectedSession?.capabilities, selectedSessionId],
  );
  const overview = useMemo(
    () => buildSessionOverview({
      session,
      workflow,
      hasSelectedSession: !!selectedSessionId,
      pendingTask: pendingMessage?.kind === "task" ? pendingMessage.text : undefined,
      pendingGuidance: pendingMessage?.kind === "guidance" ? pendingMessage.text : undefined,
    }),
    [pendingMessage, session, selectedSessionId, workflow],
  );
  const showWorkflowStatus = overview.tone === "error" || overview.tone === "warning";
  const showSessionNav = !demoActive && sessions.length > 0;
  const compactNav = demoActive || !showSessionNav;
  const showHeaderNewChat = !demoActive && !showSessionNav;
  const showChatContext = !demoActive && (session.turns.length > 0 || !!pendingMessage);
  const showRuntimeStatus = !demoActive && !!runtimeCapabilities && !showChatContext;
  const showSurfaceContext = showChatContext || showWorkflowStatus;
  const surfaceBusy = actionBusy || session.status === "running" || !!pendingMessage;
  const surfaceMode = session.turns.length === 0 && !pendingMessage ? "empty" : "conversation";
  const composerResumesSavedChat = !!selectedSessionId && !selectedSessionActive && session.turns.length > 0;
  const latestChatShortcut = useMemo(() => {
    if (!showSessionNav) return undefined;
    const row = buildSessionRows(sessions)[0];
    if (!row) return undefined;
    return {
      id: row.id,
      title: row.title,
      meta: row.meta.join(" · "),
    };
  }, [sessions, showSessionNav]);

  const loadHistory = useCallback(
    async (sessionId: string, signal?: AbortSignal): Promise<SessionState> => {
      setStatus({ state: "loading", label: "Loading history" });
      let detail = "no trace meta";
      let nextSession = initialSessionState();
      try {
        const events: RawEvent[] = [];
        let after = -1;
        let traceSchemaDetected = false;
        let traceSchemaVersion: number | undefined;

        for (let page = 0; page < maxHistoryPages; page += 1) {
          const history = await getSessionHistory(client, sessionId, { after, limit: historyPageLimit, signal });
          events.push(...history.events);
          if (history.trace_schema_detected) {
            traceSchemaDetected = true;
            traceSchemaVersion = history.trace_schema_version;
          }
          if (!history.has_more) break;
          if (history.next_after === after) throw new Error("history pagination stalled");
          after = history.next_after;
          if (page === maxHistoryPages - 1) throw new Error("history is too long to load safely");
        }

        detail = traceSchemaDetected ? `schema v${traceSchemaVersion}` : detail;
        nextSession = reduceRawEvents(events);
      } catch (err) {
        if (err instanceof ApiError && err.status === 404) {
          detail = "no persisted events";
        } else {
          throw err;
        }
      }
      setSession(nextSession);
      setStatus({
        state: "connected",
        label: "Connected",
        detail,
      });
      return nextSession;
    },
    [client],
  );

  useEffect(() => {
    const ac = new AbortController();
    async function load() {
      try {
        const resp = await listSessions(client, { limit: 100, signal: ac.signal });
        if (ac.signal.aborted) return;
        setSessions(resp.sessions);
        const next = resp.sessions.find((s) => s.active)?.id;
        setSelectedSessionId((current) => current && resp.sessions.some((s) => s.id === current) ? current : next);
        setSession(initialSessionState());
        setStatus({
          state: "connected",
          label: "Connected",
          detail: sessionListDetail(resp.sessions),
        });
      } catch (err) {
        if (isAbortError(err)) return;
        setSessions([]);
        setSelectedSessionId(undefined);
        setSession(initialSessionState());
        setStatus({
          state: "demo",
          label: "Preview",
          detail: formatConnectionFallback(err),
        });
      }
    }
    void load();
    return () => ac.abort();
  }, [client]);

  useEffect(() => {
    if (!demoActive) return;
    let cancelled = false;
    const timers: number[] = [];
    setSession(initialSessionState());
    setActionBusy(true);

    function schedule(index: number) {
      const event = completedTurn[index];
      if (!event || cancelled) {
        setActionBusy(false);
        setStatus((current) => ({ ...current, detail: "offline preview" }));
        return;
      }
      const timer = window.setTimeout(() => {
        if (cancelled) return;
        setSession((current) => applyRawEvent(current, event));
        if (event.type === EventType.TurnEnd) setActionBusy(false);
        schedule(index + 1);
      }, demoDelayFor(event));
      timers.push(timer);
    }

    schedule(0);
    return () => {
      cancelled = true;
      for (const timer of timers) window.clearTimeout(timer);
    };
  }, [demoActive]);

  useEffect(() => {
    if (!pendingMessage || pendingMessage.kind !== "task") return;
    const accepted = session.turns.some((turn) => (turn.userText ?? "").trim() === pendingMessage.text);
    if (accepted) setPendingMessage(undefined);
  }, [pendingMessage, session.turns]);

  useEffect(() => {
    if (session.status === "running") return;
    setGuidanceReceipts([]);
  }, [session.status]);

  useEffect(() => {
    if (!selectedSessionId || demoActive) return;
    const liveSessionId = selectedSessionId;
    const ac = new AbortController();
    async function connectLive() {
      try {
        await loadHistory(liveSessionId, ac.signal);
        if (ac.signal.aborted) return;
        if (!selectedSessionActive) return;
        streamClosedRef.current = false;
        setStatus((current) => ({ ...current, state: "live", label: "Live" }));
        await streamSessionEvents(client, liveSessionId, {
          signal: ac.signal,
          onEvent: (event) => {
            setSession((current) => applyRawEvent(current, event));
            if (event.type === EventType.TurnEnd) setActionBusy(false);
          },
        });
        if (!ac.signal.aborted) {
          streamClosedRef.current = true;
          setStatus((current) => {
            if (sendInFlightRef.current || sendFailedRef.current) return current;
            if (current.state === "error") return current;
            return {
              state: "disconnected",
              label: "Disconnected",
              detail: current.detail ?? "stream closed",
            };
          });
        }
      } catch (err) {
        if (isAbortError(err)) return;
        setStatus({ state: "error", label: "Connection issue", detail: formatError(err) });
      }
    }
    void connectLive();
    return () => ac.abort();
  }, [client, demoActive, loadHistory, selectedSessionActive, selectedSessionId]);

  function resetSessionSurface(nextSessionId: string) {
    if (nextSessionId === selectedSessionId) return;
    streamClosedRef.current = false;
    sendInFlightRef.current = false;
    sendFailedRef.current = false;
    setSelectedSessionId(nextSessionId);
    setSession(initialSessionState());
    setPendingMessage(undefined);
    setGuidanceReceipts([]);
    setArtifact({ state: "idle" });
    setActionBusy(false);
    setStatus({ state: "loading", label: "Loading history", detail: nextSessionId });
  }

  async function handleNewSession(): Promise<string | undefined> {
    setActionBusy(true);
    try {
      const created = await createSession(client);
      setSessions((current) => [created.session, ...current.filter((s) => s.id !== created.session.id)]);
      resetSessionSurface(created.session.id);
      setComposerFocusSignal((current) => current + 1);
      setStatus({ state: "connected", label: "Ready", detail: created.session.id });
      return created.session.id;
    } catch (err) {
      setStatus({ state: "error", label: "Create failed", detail: formatError(err) });
      return undefined;
    } finally {
      setActionBusy(false);
    }
  }

  async function handleSend(content: string) {
    let targetSessionId = selectedSessionId;
    const pendingKind: PendingMessageView["kind"] = targetSessionId && session.status === "running" ? "guidance" : "task";
    sendInFlightRef.current = true;
    sendFailedRef.current = false;
    setPendingMessage({ text: content, kind: pendingKind });
    setActionBusy(true);
    try {
      if (!targetSessionId) {
        const created = await createSession(client);
        targetSessionId = created.session.id;
        setSessions((current) => [created.session, ...current.filter((s) => s.id !== created.session.id)]);
        setSelectedSessionId(targetSessionId);
        setSession(initialSessionState());
      }
      await sendSessionMessage(client, targetSessionId, { content });
      sendInFlightRef.current = false;
      if (pendingKind === "guidance") {
        setPendingMessage(undefined);
        setActionBusy(false);
        setGuidanceReceipts((current) => [
          ...current.slice(-2),
          { id: ++nextGuidanceReceiptId.current, text: content },
        ]);
      }
      if (streamClosedRef.current) {
        const reconciled = await loadHistory(targetSessionId);
        if (pendingKind === "task") releaseSettledTurn(reconciled);
        setStatus({ state: "disconnected", label: "Disconnected", detail: "history refreshed" });
      } else {
        markSessionLive(targetSessionId, content);
        setStatus((current) => ({ ...current, state: "live", label: "Running" }));
      }
    } catch (err) {
      sendInFlightRef.current = false;
      sendFailedRef.current = true;
      setStatus({ state: "error", label: "Send failed", detail: formatError(err) });
      setPendingMessage(undefined);
      if (pendingKind === "guidance") setGuidanceReceipts([]);
      setActionBusy(false);
      throw err;
    }
  }

  function releaseSettledTurn(nextSession: SessionState) {
    if (nextSession.status === "running") return;
    setPendingMessage(undefined);
    setGuidanceReceipts([]);
    setActionBusy(false);
  }

  function markSessionLive(sessionId: string, latestUserMessage: string) {
    setSessions((current) => {
      let found = false;
      const next = current.map((item) => {
        if (item.id !== sessionId) return item;
        found = true;
        const existingLatest = item.latest_user_message?.trim();
        const topicUserMessage = item.topic_user_message ||
          (existingLatest && !isContinuationPrompt(existingLatest) ? existingLatest : latestUserMessage);
        return {
          ...item,
          active: true,
          durable: true,
          has_conversation: true,
          has_events: true,
          latest_user_message: latestUserMessage,
          topic_user_message: topicUserMessage,
        };
      });
      if (found) return next;
      return [
        {
          id: sessionId,
          active: true,
          durable: true,
          has_conversation: true,
          has_events: true,
          has_artifacts: false,
          has_memory: false,
          has_runtime_skills: false,
          latest_user_message: latestUserMessage,
          topic_user_message: latestUserMessage,
        },
        ...current,
      ];
    });
  }

  async function handleCancel() {
    if (!selectedSessionId) return;
    setActionBusy(true);
    try {
      await cancelSessionTurn(client, selectedSessionId);
      await loadHistory(selectedSessionId);
    } catch (err) {
      setStatus({ state: "error", label: "Cancel failed", detail: formatError(err) });
    } finally {
      setActionBusy(false);
    }
  }

  function handleUseAsDraft(content: string, source?: DraftSource) {
    setComposerDraft((current) => ({ id: (current?.id ?? 0) + 1, content, source }));
  }

  async function handleOpenArtifact(path: string) {
    if (!selectedSessionId) return;
    setArtifact({ state: "loading", path });
    try {
      const chunk = await readSessionArtifact(client, selectedSessionId, path, { offset: 0, limit: 64 * 1024 });
      setArtifact({ state: "ready", chunk, query: "" });
    } catch (err) {
      setArtifact({ state: "error", path, message: formatError(err) });
    }
  }

  function handleArtifactSearch(query: string) {
    setArtifact((current) => (current.state === "ready" ? { ...current, query } : current));
  }

  async function handleLoadMoreArtifact() {
    if (!selectedSessionId) return;
    const current = artifact;
    if (current.state !== "ready" || current.loadingMore || !current.chunk.hasMore) return;
    const nextOffset = current.chunk.offset + current.chunk.text.length;
    setArtifact({ ...current, loadingMore: true, loadError: undefined });
    try {
      const next = await readSessionArtifact(client, selectedSessionId, current.chunk.path, {
        offset: nextOffset,
        limit: 64 * 1024,
      });
      setArtifact((latest) => {
        if (latest.state !== "ready" || latest.chunk.path !== current.chunk.path) return latest;
        const text = latest.chunk.text + next.text;
        return {
          state: "ready",
          query: latest.query,
          chunk: {
            ...next,
            offset: latest.chunk.offset,
            text,
            hasMore: latest.chunk.offset + text.length < next.bytes,
          },
        };
      });
    } catch (err) {
      setArtifact((latest) =>
        latest.state === "ready"
          ? { ...latest, loadingMore: false, loadError: formatError(err) }
          : latest,
      );
    }
  }

  return (
    <div className="app" data-testid="app-shell">
      <header className="app-header">
        <h1>Affent</h1>
        <span className="connection-pill" data-state={status.state} data-testid="connection-pill">
          {status.label}
        </span>
        <span className="spacer" />
        {showHeaderNewChat ? (
          <button type="button" className="header-new-chat" disabled={actionBusy} onClick={() => void handleNewSession()}>
            New chat
          </button>
        ) : null}
      </header>
      <main className="app-main">
        {showRuntimeStatus && runtimeCapabilities ? <RuntimeStatusBar view={runtimeCapabilities} /> : null}
        <div
          className="workspace-shell"
          data-compact-nav={compactNav}
          data-session-nav={showSessionNav ? "visible" : "hidden"}
          data-testid="workspace-shell"
        >
          {showSessionNav ? (
            <SessionList
              sessions={sessions}
              selectedId={selectedSessionId}
              currentSession={session}
              demoActive={demoActive}
              onSelect={resetSessionSurface}
              onNew={() => void handleNewSession()}
            />
          ) : null}
          <section
            className="timeline-surface"
            aria-label="Conversation"
            data-busy={surfaceBusy ? "true" : "false"}
            data-mode={surfaceMode}
          >
            {showSurfaceContext ? (
              <div className="surface-context">
                {showChatContext ? <ChatContextBar overview={overview} /> : null}
                {showWorkflowStatus ? <WorkflowStatus overview={overview} /> : null}
              </div>
            ) : null}
            <div className="conversation-scroll" ref={conversationScrollRef} data-testid="conversation-scroll">
              <ArtifactViewer
                artifact={artifact}
                onClose={() => setArtifact({ state: "idle" })}
                onSearch={handleArtifactSearch}
                onLoadMore={() => void handleLoadMoreArtifact()}
                onUseAsDraft={handleUseAsDraft}
              />
              <Timeline
                session={session}
                sessionId={selectedSessionId}
                pendingMessage={pendingMessage}
                guidanceReceipts={guidanceReceipts}
                scrollRootRef={conversationScrollRef}
                onOpenArtifact={(path) => void handleOpenArtifact(path)}
                onUseAsDraft={handleUseAsDraft}
                savedChatCount={sessions.length}
                latestChat={latestChatShortcut}
                onOpenLatestChat={latestChatShortcut ? () => resetSessionSurface(latestChatShortcut.id) : undefined}
                initialHistoryFocus={selectedSessionId && !selectedSessionActive ? "answer" : "latest"}
              />
            </div>
            <Composer
              disabled={demoActive}
              disabledReason={status.detail}
              busy={actionBusy || session.status === "running"}
              hasSession={!!selectedSessionId}
              resumeSession={composerResumesSavedChat}
              draft={composerDraft}
              focusSignal={composerFocusSignal}
              runtimeCapabilities={runtimeCapabilities}
              onSubmit={handleSend}
              onCancel={handleCancel}
            />
          </section>
        </div>
      </main>
    </div>
  );
}

function ChatContextBar({ overview }: { overview: SessionOverview }) {
  const context = chatContextDisplay(overview);
  const contextLabel = chatContextLabel({ overview, ...context });
  return (
    <div className="chat-context-bar" data-tone={overview.tone} data-testid="chat-context-bar" aria-label={contextLabel}>
      <span className="chat-context-state">{overview.stateLabel}</span>
      <span className="chat-context-copy">
        <span className="chat-context-separator" aria-hidden="true"> · </span>
        <strong className="chat-context-primary" title={context.primary}>
          {compactContextText(context.primary, 118)}
        </strong>
        {context.secondary && context.secondary !== context.primary ? (
          <>
            {" "}
            <span className="chat-context-topic" title={context.secondary}>
              <b>{context.secondaryLabel}:</b>{" "}
              <span className="chat-context-title">{compactContextText(context.secondary, 112)}</span>
            </span>
          </>
        ) : null}
      </span>
      <RunDetails
        metrics={overview.metrics}
        className="chat-context-details"
        testId="chat-context-details"
        ariaLabel="Current chat metrics"
      />
    </div>
  );
}

interface ChatContextDisplay {
  primary: string;
  secondary?: string;
  secondaryLabel: string;
}

function chatContextDisplay(overview: SessionOverview): ChatContextDisplay {
  const primary = chatContextPrimary(overview);
  const secondary = primary === overview.detail ? overview.headline : overview.detail;
  return {
    primary,
    secondary,
    secondaryLabel: chatContextSecondaryLabel(overview, primary),
  };
}

function chatContextSecondaryLabel(overview: SessionOverview, primary: string): string {
  if (overview.stateLabel === "Sending") return "Next";
  if (primary === overview.detail) return "Task";
  return "Context";
}

function chatContextLabel({
  overview,
  primary,
  secondary,
  secondaryLabel,
}: {
  overview: SessionOverview;
  primary: string;
  secondary?: string;
  secondaryLabel: string;
}): string {
  const parts = [overview.stateLabel, primary];
  if (secondary && secondary !== primary) parts.push(`${secondaryLabel}: ${secondary}`);
  return parts.filter(Boolean).join(" · ");
}

function chatContextPrimary(overview: SessionOverview): string {
  if (overview.stateLabel === "Sending") return overview.headline;
  if (overview.stateLabel === "Sending guidance") return overview.detail;
  if (overview.detail) return overview.detail;
  return overview.headline;
}

function compactContextText(text: string, limit: number): string {
  const normalized = text.replace(/\s+/g, " ").trim();
  if (normalized.length <= limit) return normalized;
  return `${normalized.slice(0, Math.max(0, limit - 3)).trimEnd()}...`;
}

function RuntimeStatusBar({ view }: { view: RuntimeCapabilityView }) {
  return (
    <details
      className="runtime-status-bar"
      data-tone={view.tone}
      data-testid="runtime-capabilities"
      aria-label={`${view.headline}. ${view.detail}`}
    >
      <summary>
        <span className="runtime-status-kicker">Setup</span>
        <span className="runtime-capability-title">{view.headline}</span>
        <span className="runtime-capability-detail">
          <span className="runtime-capability-separator" aria-hidden="true">·</span>
          {view.detail}
        </span>
      </summary>
      <div className="runtime-capability-chips">
        {view.chips.map((chip) => (
          <span key={`${chip.group}:${chip.label}`} data-tone={chip.tone}>
            <b>{chip.group}</b>
            {" "}
            {chip.label}
          </span>
        ))}
      </div>
    </details>
  );
}

function isAbortError(err: unknown): boolean {
  return err instanceof DOMException && err.name === "AbortError";
}

function formatError(err: unknown): string {
  if (err instanceof ApiError) {
    return err.type ? `${err.type}: ${err.message}` : err.message;
  }
  if (err instanceof Error) return err.message;
  return "unknown error";
}

function formatConnectionFallback(err: unknown): string {
  if (err instanceof ApiError) return err.message;
  return "Connect affentserve to send messages.";
}

function sessionListDetail(sessions: readonly SessionSummary[]): string {
  if (sessions.length === 0) return "no sessions";
  const active = sessions.filter((session) => session.active).length;
  if (active > 0) return `${active} live · ${sessions.length} total`;
  return `${sessions.length} saved ${sessions.length === 1 ? "chat" : "chats"}`;
}

function demoDelayFor(event: RawEvent): number {
  switch (event.type) {
    case EventType.MessageDelta:
      return 120;
    case EventType.ToolRequest:
      return demoReplayDelayMs + 180;
    case EventType.ToolResult:
      return demoReplayDelayMs + 360;
    case EventType.TurnEnd:
      return demoReplayDelayMs + 120;
    default:
      return demoReplayDelayMs;
  }
}
