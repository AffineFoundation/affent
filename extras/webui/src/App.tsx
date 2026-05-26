import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import "./styles/index.css";
import { ApiClient, ApiError } from "./api/client";
import {
  cancelSessionTurn,
  createSession,
  getSessionHistory,
  listSessions,
  listSessionTools,
  readSessionArtifact,
  sendSessionMessage,
  streamSessionEvents,
  type SessionToolInfo,
  type SessionToolsSurfaceInfo,
  type SessionSummary,
} from "./api/sessions";
import { getServerStats, type ServerStatsResponse } from "./api/stats";
import { ArtifactViewer, type ArtifactViewerState } from "./components/ArtifactViewer";
import { EventType, type RawEvent } from "./api/events";
import { Composer, type ComposerDraft } from "./components/Composer";
import { SessionList } from "./components/SessionList";
import { SessionToolsPanel } from "./components/SessionToolsPanel";
import { Timeline, type GuidanceReceiptView, type PendingMessageView } from "./components/Timeline";
import { WorkflowStatus } from "./components/WorkflowStatus";
import { RunDetails } from "./components/RunDetails";
import { completedTurn } from "./fixtures/completedTurn";
import { applyRawEvent, reduceRawEvents } from "./store/reduce";
import { initialSessionState, type SessionState } from "./store/sessionState";
import { deriveWorkflowStatus } from "./store/workflowStatus";
import type { DraftSource } from "./view/draftSource";
import { buildRuntimeCapabilityView, type RuntimeCapabilityView } from "./view/runtimeCapabilities";
import { buildSessionRows, formatLoadingChatTitle } from "./view/sessionList";
import { buildSessionOverview, type SessionOverview } from "./view/sessionOverview";
import { isContinuationPrompt } from "./view/continuationPrompt";

type SurfaceState = "connecting" | "connected" | "live" | "loading" | "demo" | "disconnected" | "error";

interface StatusBanner {
  state: SurfaceState;
  label: string;
  detail?: string;
}

type ServerStatusState = "loading" | "ready" | "unavailable";

interface HistoryLoadResult {
  session: SessionState;
  cursor: number;
}

type SessionToolsState =
  | { state: "idle" }
  | { state: "loading" }
  | { state: "ready"; tools: SessionToolInfo[]; surface?: SessionToolsSurfaceInfo }
  | { state: "error"; message: string };

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
  const [serverStats, setServerStats] = useState<ServerStatsResponse | undefined>();
  const [serverStatusState, setServerStatusState] = useState<ServerStatusState>("loading");
  const [profileOpen, setProfileOpen] = useState(false);
  const [selectedSessionId, setSelectedSessionId] = useState<string | undefined>();
  const [session, setSession] = useState<SessionState>(() => initialSessionState());
  const [actionBusy, setActionBusy] = useState(false);
  const [cancelBusy, setCancelBusy] = useState(false);
  const [pendingMessage, setPendingMessage] = useState<PendingMessageView | undefined>();
  const [guidanceReceipts, setGuidanceReceipts] = useState<GuidanceReceiptView[]>([]);
  const [composerDraft, setComposerDraft] = useState<ComposerDraft | undefined>();
  const [composerFocusSignal, setComposerFocusSignal] = useState(0);
  const [artifact, setArtifact] = useState<ArtifactViewerState>({ state: "idle" });
  const [sessionTools, setSessionTools] = useState<SessionToolsState>({ state: "idle" });
  const sendInFlightRef = useRef(false);
  const sendFailedRef = useRef(false);
  const streamClosedRef = useRef(false);
  const streamSessionIdRef = useRef<string | undefined>(undefined);
  const nextGuidanceReceiptId = useRef(0);
  const conversationScrollRef = useRef<HTMLDivElement | null>(null);
  const sessionsRef = useRef(sessions);
  useEffect(() => {
    sessionsRef.current = sessions;
  }, [sessions]);
  useEffect(() => {
    const ac = new AbortController();
    let timer: number | undefined;

    async function loadStats() {
      try {
        const next = await getServerStats(client, ac.signal);
        if (!ac.signal.aborted) setServerStats(next);
        if (!ac.signal.aborted) setServerStatusState("ready");
      } catch (err) {
        if (!isAbortError(err)) {
          setServerStatusState("unavailable");
        }
      }
    }

    void loadStats();
    timer = window.setInterval(() => {
      void loadStats();
    }, 15_000);

    return () => {
      ac.abort();
      if (timer != null) window.clearInterval(timer);
    };
  }, [client]);
  const demoActive = status.state === "demo";
  const selectedSession = useMemo(
    () => sessions.find((candidate) => candidate.id === selectedSessionId),
    [selectedSessionId, sessions],
  );
  const selectedSessionTitle = useMemo(() => {
    if (!selectedSession) return undefined;
    const row = buildSessionRows([selectedSession])[0];
    return row?.title;
  }, [selectedSession]);
  const selectedSessionActive = selectedSession?.active === true;
  const workflow = useMemo(() => deriveWorkflowStatus(session), [session]);
  const capabilityView = useMemo(
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
      sessionTitle: selectedSessionTitle,
    }),
    [pendingMessage, selectedSessionId, selectedSessionTitle, session, workflow],
  );
  const showWorkflowStatus = overview.tone === "error" || overview.tone === "warning";
  const showSessionNav = !demoActive && sessions.length > 0;
  const compactNav = demoActive || !showSessionNav;
  const showHeaderNewChat = !demoActive && !showSessionNav;
  const showChatContext = !demoActive && (session.turns.length > 0 || !!pendingMessage);
  const showSessionTools = !demoActive && selectedSessionActive && selectedSessionId;
  const showCapabilityStatus = !demoActive && !!capabilityView && (!showChatContext || capabilityView.tone === "unknown");
  const historyLoading = status.state === "loading" && !!selectedSessionId;
  const showSurfaceContext = !historyLoading && (showChatContext || showWorkflowStatus || showSessionTools);
  const surfaceBusy = actionBusy || session.status === "running" || !!pendingMessage;
  const surfaceMode = session.turns.length === 0 && !pendingMessage ? "empty" : "conversation";
  const composerResumesSavedChat = !!selectedSessionId && !selectedSessionActive && session.turns.length > 0;
  const connectionLabel = useMemo(
    () => (status.state === "loading" ? formatLoadingChatTitle(status.detail) : status.label),
    [status.detail, status.label, status.state],
  );
  const latestChatShortcut = useMemo(() => {
    if (!showSessionNav) return undefined;
    const row = buildSessionRows(sessions)[0];
    if (!row) return undefined;
    return {
      id: row.id,
      title: row.title,
      draft: row.titleSource === "fallback" ? undefined : row.title,
      meta: latestChatMeta(row.updated),
    };
  }, [sessions, showSessionNav]);
  const resolveSessionTitle = useCallback(
    (sessionId: string | undefined, sessionList?: readonly SessionSummary[]): string | undefined => {
      if (!sessionId) return undefined;
      const source = sessionList ?? sessionsRef.current;
      const session = source.find((candidate) => candidate.id === sessionId);
      if (!session) return undefined;
      return buildSessionRows([session])[0]?.title;
    },
    [],
  );
  const loadingSessionDetail = useCallback(
    (sessionId: string | undefined, sessionList?: readonly SessionSummary[]): string => resolveSessionTitle(sessionId, sessionList) ?? "Loading chat",
    [resolveSessionTitle],
  );

  const loadHistory = useCallback(
    async (sessionId: string, signal?: AbortSignal): Promise<HistoryLoadResult> => {
      setStatus({ state: "loading", label: "Loading chat", detail: loadingSessionDetail(sessionId) });
      let detail = "Ready to chat";
      let nextSession = initialSessionState();
      let cursor = -1;
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
          cursor = history.next_after;
          if (!history.has_more) break;
          if (history.next_after === after) throw new Error("trace pagination stalled");
          after = history.next_after;
          if (page === maxHistoryPages - 1) throw new Error("trace is too long to load safely");
        }

        cursor = events.length > 0 ? Math.max(cursor, lastRawEventId(events)) : cursor;
        detail = events.length > 0 ? (traceSchemaDetected ? `schema v${traceSchemaVersion}` : detail) : detail;
        nextSession = reduceRawEvents(events);
      } catch (err) {
        if (err instanceof ApiError && err.status === 404) {
          detail = "no persisted trace events";
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
      return { session: nextSession, cursor };
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
        const nextSelected = (current: string | undefined) =>
          current && resp.sessions.some((s) => s.id === current) ? current : next;
        setSelectedSessionId(nextSelected);
        setSession(initialSessionState());
        if (next) {
          setStatus({
            state: "loading",
            label: "Loading chat",
            detail: loadingSessionDetail(next, resp.sessions),
          });
        } else {
          setStatus({
            state: "connected",
            label: "Connected",
            detail: sessionListDetail(resp.sessions),
          });
        }
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
    const sessionId = showSessionTools ? selectedSessionId : undefined;
    const ac = new AbortController();
    if (!sessionId) {
      setSessionTools({ state: "idle" });
      return () => ac.abort();
    }
    setSessionTools({ state: "loading" });
    void listSessionTools(client, sessionId, ac.signal)
      .then((resp) => {
        if (!ac.signal.aborted) {
          setSessionTools({ state: "ready", tools: resp.tools, surface: resp.surface });
        }
      })
      .catch((err) => {
        if (ac.signal.aborted || isAbortError(err)) return;
        if (err instanceof ApiError && err.status === 409) {
          setSessionTools({ state: "idle" });
          return;
        }
        setSessionTools({ state: "error", message: formatError(err) });
      });
    return () => ac.abort();
  }, [client, selectedSessionId, showSessionTools]);

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
        if (event.type === EventType.TurnEnd) {
          setActionBusy(false);
          setCancelBusy(false);
        }
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
    setCancelBusy(false);
  }, [session.status]);

  useEffect(() => {
    if (!selectedSessionId || demoActive) return;
    const liveSessionId = selectedSessionId;
    const ac = new AbortController();
    async function connectLive() {
      try {
        if (sendFailedRef.current || sendInFlightRef.current) return;
        const history = await loadHistory(liveSessionId, ac.signal);
        if (ac.signal.aborted) return;
        if (!selectedSessionActive) return;
        streamClosedRef.current = false;
        streamSessionIdRef.current = liveSessionId;
        setStatus((current) => ({ ...current, state: "live", label: "Live" }));
        await streamSessionEvents(client, liveSessionId, {
          signal: ac.signal,
          lastEventId: history.cursor,
          onEvent: (event) => {
            setSession((current) => applyRawEvent(current, event));
            if (event.type === EventType.TurnEnd) {
              setActionBusy(false);
              setCancelBusy(false);
            }
          },
        });
        if (!ac.signal.aborted) {
          streamClosedRef.current = true;
          if (streamSessionIdRef.current === liveSessionId) streamSessionIdRef.current = undefined;
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
        if (streamSessionIdRef.current === liveSessionId) streamSessionIdRef.current = undefined;
        setStatus({ state: "error", label: "Connection issue", detail: formatError(err) });
      }
    }
    void connectLive();
    return () => {
      ac.abort();
      if (streamSessionIdRef.current === liveSessionId) streamSessionIdRef.current = undefined;
    };
  }, [client, demoActive, loadHistory, selectedSessionActive, selectedSessionId]);

  function resetSessionSurface(nextSessionId: string, opts?: { preserveSession?: boolean }) {
    if (nextSessionId === selectedSessionId) return;
    streamClosedRef.current = false;
    streamSessionIdRef.current = undefined;
    sendInFlightRef.current = false;
    sendFailedRef.current = false;
    setSelectedSessionId(nextSessionId);
    if (!opts?.preserveSession) setSession(initialSessionState());
    setPendingMessage(undefined);
    setGuidanceReceipts([]);
    setArtifact({ state: "idle" });
    setActionBusy(false);
    setCancelBusy(false);
    setStatus({ state: "loading", label: "Loading chat", detail: loadingSessionDetail(nextSessionId) });
  }

  async function handleNewSession(): Promise<string | undefined> {
    setActionBusy(true);
    try {
      const created = await createSession(client);
      setSessions((current) => [created.session, ...current.filter((s) => s.id !== created.session.id)]);
      resetSessionSurface(created.session.id);
      setComposerFocusSignal((current) => current + 1);
      setStatus({ state: "connected", label: "Ready", detail: "Ready to chat" });
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
      if (pendingKind === "task") markSessionLive(targetSessionId, content);
      const hasOpenStream = streamSessionIdRef.current === targetSessionId && !streamClosedRef.current;
      if (!hasOpenStream) {
        const reconciled = await loadHistory(targetSessionId);
        if (pendingKind === "task") releaseSettledTurn(reconciled.session, content);
        setStatus({ state: "disconnected", label: "Disconnected", detail: "chat refreshed" });
      } else {
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

  function releaseSettledTurn(nextSession: SessionState, pendingText?: string) {
    if (nextSession.status === "running") return;
    const accepted = pendingText
      ? nextSession.turns.some((turn) => (turn.userText ?? "").trim() === pendingText.trim())
      : true;
    if (accepted) {
      setPendingMessage(undefined);
      setGuidanceReceipts([]);
    }
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
    if (!selectedSessionId || cancelBusy) return;
    setCancelBusy(true);
    setActionBusy(true);
    try {
      await cancelSessionTurn(client, selectedSessionId);
      await loadHistory(selectedSessionId);
    } catch (err) {
      setStatus({ state: "error", label: "Cancel failed", detail: formatError(err) });
    } finally {
      setCancelBusy(false);
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
      <div className="app-topbar">
        <header className="app-header">
          <h1>Affent</h1>
          <span className="connection-pill" data-state={status.state} data-testid="connection-pill" title={status.detail ?? status.label}>
            {connectionLabel}
          </span>
          <span className="spacer" />
          {showHeaderNewChat ? (
            <button type="button" className="header-new-chat" disabled={actionBusy} onClick={() => void handleNewSession()}>
              New chat
            </button>
          ) : null}
        </header>
        <WorkbenchStatusBar
          stats={serverStats}
          state={serverStatusState}
          busy={surfaceBusy}
          needsAttention={overview.tone === "error" || overview.tone === "warning"}
          onOpenProfile={() => setProfileOpen(true)}
        />
      </div>
      {profileOpen ? <ProfileDialog stats={serverStats} state={serverStatusState} onClose={() => setProfileOpen(false)} /> : null}
      <main className="app-main">
        {showCapabilityStatus && capabilityView ? <RuntimeStatusBar view={capabilityView} /> : null}
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
              pendingTask={pendingMessage?.kind === "task" ? pendingMessage.text : undefined}
              demoActive={demoActive}
              onSelect={(nextSessionId) => resetSessionSurface(nextSessionId, { preserveSession: true })}
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
                {showSessionTools ? (
                <SessionToolsPanel
                  tools={sessionTools.state === "ready" ? sessionTools.tools : undefined}
                  loading={sessionTools.state === "loading"}
                  error={sessionTools.state === "error" ? sessionTools.message : undefined}
                  surface={sessionTools.state === "ready" ? sessionTools.surface : undefined}
                />
                ) : null}
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
              onOpenLatestChat={latestChatShortcut ? () => resetSessionSurface(latestChatShortcut.id, { preserveSession: true }) : undefined}
              initialHistoryFocus={selectedSessionId && !selectedSessionActive ? "answer" : "latest"}
              loadingHistory={historyLoading}
              sessionTitle={status.state === "loading" ? status.detail : selectedSessionTitle}
            />
          </div>
            <Composer
              disabled={demoActive}
              disabledReason={status.detail}
              busy={actionBusy || session.status === "running"}
              cancelling={cancelBusy}
              hasSession={!!selectedSessionId}
              resumeSession={composerResumesSavedChat}
              draft={composerDraft}
              focusSignal={composerFocusSignal}
              runtimeCapabilities={capabilityView}
              onSubmit={handleSend}
              onCancel={handleCancel}
            />
          </section>
        </div>
      </main>
    </div>
  );
}

function latestChatMeta(updated: string): string | undefined {
  return updated && updated !== "No messages yet" ? updated : undefined;
}

function lastRawEventId(events: readonly RawEvent[]): number {
  const last = events[events.length - 1];
  return typeof last?.id === "number" ? last.id : -1;
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
          ariaLabel="Session metrics"
          summaryLabel="Session metrics"
          inlineLimit={1}
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
  if (overview.tone === "success" || (overview.tone === "running" && overview.stateLabel !== "Sending" && overview.stateLabel !== "Sending guidance")) {
    return {
      primary,
      secondary: undefined,
      secondaryLabel: "Task",
    };
  }
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
  const [expanded, setExpanded] = useState(false);
  const hasChips = view.chips.length > 0;
  const inlineChips = view.chips.slice(0, 2);
  const overflowChips = view.chips.slice(2);

  const header = (
    <>
      <div className="runtime-status-head">
        <span className="runtime-status-kicker">Capabilities</span>
        <span className="runtime-capability-title">{view.headline}</span>
        {!expanded && overflowChips.length > 0 ? <span className="runtime-capability-more">+{overflowChips.length} more</span> : null}
      </div>
    </>
  );

  const panel = (
    <>
      <div className="runtime-capability-panel">
        <p className="runtime-capability-panel-detail">{view.detail}</p>
        {hasChips ? (
          <div className="runtime-capability-list">
            {inlineChips.map((chip) => (
              <div key={`${chip.group}:${chip.label}`} className="runtime-capability-item" data-tone={chip.tone}>
                <b>{chip.group}</b>
                <strong>{chip.label}</strong>
                <small>{chip.detail}</small>
              </div>
            ))}
            {overflowChips.length > 0 ? (
              <details className="runtime-capability-overflow">
                <summary aria-label={`More capabilities: ${overflowChips.length} more`}>
                  +{overflowChips.length} more
                </summary>
                <div className="runtime-capability-overflow-body">
                  {overflowChips.map((chip) => (
                    <div key={`${chip.group}:${chip.label}`} className="runtime-capability-item" data-tone={chip.tone}>
                      <b>{chip.group}</b>
                      <strong>{chip.label}</strong>
                      <small>{chip.detail}</small>
                    </div>
                  ))}
                </div>
              </details>
            ) : null}
          </div>
        ) : null}
      </div>
    </>
  );

  if (!hasChips) {
    return (
      <section className="runtime-status-bar" data-tone={view.tone} data-testid="runtime-capabilities" aria-label={`${view.headline}. ${view.detail}`}>
        {header}
        {panel}
      </section>
    );
  }

  return (
    <details
      className="runtime-status-bar"
      data-tone={view.tone}
      data-testid="runtime-capabilities"
      aria-label={`${view.headline}. ${view.detail}`}
      onToggle={(event) => setExpanded(event.currentTarget.open)}
    >
      <summary>{header}</summary>
      <div className="runtime-status-expandable">
        {panel}
      </div>
    </details>
  );
}

function WorkbenchStatusBar({
  stats,
  state,
  busy,
  needsAttention,
  onOpenProfile,
}: {
  stats?: ServerStatsResponse;
  state: ServerStatusState;
  busy: boolean;
  needsAttention: boolean;
  onOpenProfile: () => void;
}) {
  const status = workbenchStatus({ stats, state, busy, needsAttention });
  return (
    <section className="workbench-status-bar" data-testid="workbench-status-bar" aria-label="Workbench status">
      <span className="workbench-status-dot" data-tone={status.tone} aria-hidden="true" />
      <div className="workbench-status-copy">
        <strong>{status.label}</strong>
        <span>{status.detail}</span>
      </div>
      <button type="button" className="profile-button" onClick={onOpenProfile}>
        Profile
      </button>
    </section>
  );
}

function ProfileDialog({ stats, state, onClose }: { stats?: ServerStatsResponse; state: ServerStatusState; onClose: () => void }) {
  const serverHealth =
    state === "loading" ? "Checking connection" : state === "unavailable" ? "Server unavailable" : stats?.shutting_down ? "Shutting down" : "Healthy";
  const tools = [
    profileSwitch("Browser", stats?.enable_browser),
    profileSwitch("Web access", stats?.enable_web),
    profileSwitch("Web search", stats?.enable_web_search),
    profileSwitch("Memory", stats?.enable_memory),
    profileSwitch("Built-in tools", stats?.enable_builtins),
    profileSwitch("Subtasks", stats?.enable_subagent || stats?.enable_focused_tasks),
    stats?.web_search_backend ? { label: "Search provider", value: stats.web_search_backend } : undefined,
    stats?.browser_cache_dir ? { label: "Browser cache", value: "On" } : undefined,
  ].filter((item): item is ProfileItem => Boolean(item));
  const runtime = [
    stats?.listen ? { label: "Listen address", value: stats.listen } : undefined,
    typeof stats?.active_sessions === "number" ? { label: "Active sessions", value: String(stats.active_sessions) } : undefined,
    typeof stats?.running_turns === "number" ? { label: "Running turns", value: String(stats.running_turns) } : undefined,
    stats?.workspace_root ? { label: "Workspace", value: formatServerRoot("Workspace", stats.workspace_root), title: stats.workspace_root } : undefined,
    stats?.memory_root ? { label: "Memory store", value: formatServerRoot("Memory", stats.memory_root), title: stats.memory_root } : undefined,
    stats?.server_time ? { label: "Updated", value: formatServerTime(stats.server_time), title: stats.server_time } : undefined,
  ].filter((item): item is ProfileItem => Boolean(item));

  return (
    <div className="profile-overlay" role="presentation" onMouseDown={onClose}>
      <section
        className="profile-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="profile-dialog-title"
        data-testid="profile-dialog"
        onMouseDown={(event) => event.stopPropagation()}
      >
        <header className="profile-dialog-head">
          <div>
            <span className="profile-kicker">Profile</span>
            <h2 id="profile-dialog-title">Environment settings</h2>
          </div>
          <button type="button" className="profile-close" aria-label="Close profile" onClick={onClose}>
            Close
          </button>
        </header>
        <div className="profile-dialog-body">
          <ProfileSection
            title="Account and keys"
            items={[
              { label: "API keys", value: "Managed by server environment" },
              { label: "Browser storage", value: "No keys stored in this page" },
            ]}
          />
          <ProfileSection
            title="Model"
            items={[
              { label: "Server", value: serverHealth },
              { label: "Model", value: stats?.model ?? "Not reported" },
              { label: "Executor", value: stats?.executor_mode ?? "Not reported" },
            ]}
          />
          <ProfileSection title="Tools" items={tools.length > 0 ? tools : [{ label: "Capabilities", value: "Not reported" }]} />
          <ProfileSection title="Advanced diagnostics" items={runtime.length > 0 ? runtime : [{ label: "Runtime", value: "Not reported" }]} />
        </div>
      </section>
    </div>
  );
}

interface ProfileItem {
  label: string;
  value: string;
  title?: string;
}

function ProfileSection({ title, items }: { title: string; items: readonly ProfileItem[] }) {
  return (
    <section className="profile-section">
      <h3>{title}</h3>
      <dl>
        {items.map((item) => (
          <div key={`${title}:${item.label}`} className="profile-row">
            <dt>{item.label}</dt>
            <dd title={item.title}>{item.value}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}

function workbenchStatus({
  stats,
  state,
  busy,
  needsAttention,
}: {
  stats?: ServerStatsResponse;
  state: ServerStatusState;
  busy: boolean;
  needsAttention: boolean;
}): { label: string; detail: string; tone: "ready" | "running" | "warning" } {
  if (state === "loading") return { label: "Connecting", detail: "Preparing the workbench", tone: "running" };
  if (state === "unavailable") return { label: "Connection issue", detail: "Open Profile for diagnostics", tone: "warning" };
  if (stats?.shutting_down) return { label: "Server stopping", detail: "Finish or save current work", tone: "warning" };
  if (needsAttention) return { label: "Needs attention", detail: "Review the current task before continuing", tone: "warning" };
  if (busy) return { label: "Working", detail: "Affent is handling the current task", tone: "running" };
  return { label: "Ready", detail: "Start a task or continue a saved chat", tone: "ready" };
}

function profileSwitch(label: string, enabled?: boolean): ProfileItem | undefined {
  if (enabled == null) return undefined;
  return {
    label,
    value: enabled ? "On" : "Off",
  };
}

function formatServerTime(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(undefined, {
    hour: "2-digit",
    minute: "2-digit",
    timeZoneName: "short",
  }).format(date);
}

function formatServerRoot(label: "Workspace" | "Memory", root: string): string {
  const normalized = root.replace(/\/+$/, "");
  const leaf = normalized.split("/").filter(Boolean).at(-1) ?? normalized;
  return `${label} ${leaf}`;
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
