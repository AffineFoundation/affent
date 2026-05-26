import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import "./styles/index.css";
import { ApiClient, ApiError } from "./api/client";
import {
  cancelSessionTurn,
  createSession,
  deleteSession,
  getSessionMemory,
  getSessionHistory,
  installSkill,
  listSessions,
  listSkills,
  readSessionArtifact,
  readSkill,
  sendSessionMessage,
  streamSessionEvents,
  type SessionMemoryResponse,
  type SessionSkillInfo,
  type SessionSkillInstallRequest,
  type SessionSummary,
} from "./api/sessions";
import { ArtifactViewer, type ArtifactViewerState } from "./components/ArtifactViewer";
import { EventType, type RawEvent } from "./api/events";
import { Composer, type ComposerDraft } from "./components/Composer";
import { SessionList } from "./components/SessionList";
import { SessionMemoryPanel } from "./components/SessionMemoryPanel";
import { SessionSkillsPanel } from "./components/SessionSkillsPanel";
import { Timeline, type GuidanceReceiptView, type PendingMessageView } from "./components/Timeline";
import { WorkflowStatus } from "./components/WorkflowStatus";
import { RunDetails } from "./components/RunDetails";
import { completedTurn } from "./fixtures/completedTurn";
import { applyRawEvent, reduceRawEvents } from "./store/reduce";
import { initialSessionState, type SessionState } from "./store/sessionState";
import { deriveWorkflowStatus } from "./store/workflowStatus";
import type { DraftSource } from "./view/draftSource";
import { buildRuntimeCapabilityView } from "./view/runtimeCapabilities";
import { buildSessionRows, formatLoadingChatTitle } from "./view/sessionList";
import { buildSessionOverview, type SessionOverview } from "./view/sessionOverview";
import { isContinuationPrompt } from "./view/continuationPrompt";

type SurfaceState = "connecting" | "connected" | "live" | "loading" | "demo" | "disconnected" | "error";

interface StatusBanner {
  state: SurfaceState;
  label: string;
  detail?: string;
}

type ThemeMode = "light" | "dark";

interface HistoryLoadResult {
  session: SessionState;
  cursor: number;
}

type SkillsState =
  | { state: "idle" }
  | { state: "loading" }
  | { state: "ready"; skills: SessionSkillInfo[]; installEnabled: boolean }
  | { state: "error"; error: string };

type MemoryState =
  | { state: "idle" }
  | { state: "empty" }
  | { state: "loading" }
  | { state: "ready"; memory: SessionMemoryResponse }
  | { state: "error"; error: string };

const demoReplayDelayMs = 180;
const historyPageLimit = 500;
const maxHistoryPages = 50;
const themeStorageKey = "affent.theme";

// The shell stays deliberately thin: transport helpers own HTTP details,
// the reducer owns event interpretation, and UI components receive stable
// view data. This keeps future live SSE, artifact and search work from
// turning App into a protocol parser.
export function App() {
  const client = useMemo(() => new ApiClient({ basePath: import.meta.env.VITE_AFFENT_API_BASE }), []);
  const [theme, setTheme] = useState<ThemeMode>(() => initialTheme());
  const [status, setStatus] = useState<StatusBanner>({
    state: "connecting",
    label: "Connecting",
  });
  const [sessions, setSessions] = useState<SessionSummary[]>([]);
  const [selectedSessionId, setSelectedSessionId] = useState<string | undefined>();
  const [session, setSession] = useState<SessionState>(() => initialSessionState());
  const [actionBusy, setActionBusy] = useState(false);
  const [cancelBusy, setCancelBusy] = useState(false);
  const [deletingSessionId, setDeletingSessionId] = useState<string | undefined>();
  const [pendingMessage, setPendingMessage] = useState<PendingMessageView | undefined>();
  const [guidanceReceipts, setGuidanceReceipts] = useState<GuidanceReceiptView[]>([]);
  const [skillsState, setSkillsState] = useState<SkillsState>({ state: "idle" });
  const [memoryState, setMemoryState] = useState<MemoryState>({ state: "idle" });
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [sessionsCollapsed, setSessionsCollapsed] = useState(false);
  const [mobileTopbarHidden, setMobileTopbarHidden] = useState(false);
  const [composerDraft, setComposerDraft] = useState<ComposerDraft | undefined>();
  const [composerFocusSignal, setComposerFocusSignal] = useState(0);
  const [artifact, setArtifact] = useState<ArtifactViewerState>({ state: "idle" });
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
    document.documentElement.dataset.theme = theme;
    try {
      window.localStorage.setItem(themeStorageKey, theme);
    } catch {
      // Local persistence is best effort; theme switching still works.
    }
  }, [theme]);

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
      planSummary: selectedSession?.plan_summary,
    }),
    [pendingMessage, selectedSession?.plan_summary, selectedSessionId, selectedSessionTitle, session, workflow],
  );
  const showWorkflowStatus = overview.tone === "error" || overview.tone === "warning";
  const showSessionNav = !demoActive && sessions.length > 0;
  const compactNav = demoActive || !showSessionNav;
  const showHeaderNewChat = !demoActive && !showSessionNav;
  const showChatContext = !demoActive && (session.turns.length > 0 || !!pendingMessage);
  const showSurfaceContext = showChatContext || showWorkflowStatus;
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

  useEffect(() => {
    if (demoActive || !settingsOpen) {
      setSkillsState({ state: "idle" });
      return;
    }
    const ac = new AbortController();
    setSkillsState({ state: "loading" });
    listSkills(client, ac.signal)
      .then((resp) => {
        setSkillsState({
          state: "ready",
          skills: resp.skills,
          installEnabled: resp.install_enabled,
        });
      })
      .catch((err) => {
        if (isAbortError(err)) return;
        setSkillsState({ state: "error", error: formatError(err) });
      });
    return () => ac.abort();
  }, [client, demoActive, settingsOpen]);

  useEffect(() => {
    if (demoActive || !settingsOpen) {
      setMemoryState({ state: "idle" });
      return;
    }
    if (!selectedSessionId) {
      setMemoryState({ state: "empty" });
      return;
    }
    const ac = new AbortController();
    setMemoryState({ state: "loading" });
    getSessionMemory(client, selectedSessionId, ac.signal)
      .then((memory) => {
        setMemoryState({ state: "ready", memory });
      })
      .catch((err) => {
        if (isAbortError(err)) return;
        setMemoryState({ state: "error", error: formatError(err) });
      });
    return () => ac.abort();
  }, [client, demoActive, selectedSessionId, settingsOpen]);

  const handleReadSkill = useCallback(
    async (name: string): Promise<SessionSkillInfo> => {
      const resp = await readSkill(client, name);
      return resp.skill;
    },
    [client],
  );

  const handleInstallSkill = useCallback(
    async (request: SessionSkillInstallRequest): Promise<SessionSkillInfo> => {
      const resp = await installSkill(client, request);
      setSkillsState((current) => {
        if (current.state !== "ready") return current;
        const nextSkills = [resp.skill, ...current.skills.filter((skill) => skill.name !== resp.skill.name)];
        return { ...current, skills: nextSkills, installEnabled: true };
      });
      return resp.skill;
    },
    [client],
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
    streamClosedRef.current = false;
    streamSessionIdRef.current = undefined;
    sendInFlightRef.current = false;
    sendFailedRef.current = false;
    setSelectedSessionId(undefined);
    setSession(initialSessionState());
    setPendingMessage(undefined);
    setGuidanceReceipts([]);
    setArtifact({ state: "idle" });
    setActionBusy(false);
    setCancelBusy(false);
    setComposerDraft(undefined);
    setComposerFocusSignal((current) => current + 1);
    setStatus({ state: "connected", label: "Ready", detail: "Ready to chat" });
    return undefined;
  }

  async function handleDeleteSession(sessionId: string): Promise<void> {
    setDeletingSessionId(sessionId);
    try {
      await deleteSession(client, sessionId);
      setSessions((current) => current.filter((candidate) => candidate.id !== sessionId));
      if (selectedSessionId === sessionId) {
        streamClosedRef.current = false;
        streamSessionIdRef.current = undefined;
        sendInFlightRef.current = false;
        sendFailedRef.current = false;
        setSelectedSessionId(undefined);
        setSession(initialSessionState());
        setPendingMessage(undefined);
        setGuidanceReceipts([]);
        setArtifact({ state: "idle" });
        setActionBusy(false);
        setCancelBusy(false);
      }
      setStatus({ state: "connected", label: "Ready", detail: "Chat deleted" });
    } catch (err) {
      setStatus({ state: "error", label: "Delete failed", detail: formatError(err) });
    } finally {
      setDeletingSessionId(undefined);
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
    <div
      className="app"
      data-theme={theme}
      data-mobile-topbar={mobileTopbarHidden ? "hidden" : "visible"}
      data-testid="app-shell"
    >
      {mobileTopbarHidden ? (
        <div className="mobile-chrome-restore-bar">
          <button type="button" className="mobile-chrome-restore" aria-label="Show top controls" onClick={() => setMobileTopbarHidden(false)}>
            <span className="mobile-restore-grip" aria-hidden="true">
              <span />
              <span />
            </span>
          </button>
        </div>
      ) : null}
      <div className="app-topbar">
        <header className="app-header">
          <h1>Affent</h1>
          <span className="connection-pill" data-state={status.state} data-testid="connection-pill" title={status.detail ?? status.label}>
            {connectionLabel}
          </span>
          <span className="spacer" />
          <button type="button" className="mobile-chrome-toggle" aria-label="Hide top controls" onClick={() => setMobileTopbarHidden(true)}>
            <span className="mobile-collapse-icon" aria-hidden="true">
              <span />
              <span />
            </span>
          </button>
          <div className="theme-switch" role="group" aria-label="Color theme">
            <button type="button" aria-pressed={theme === "light"} onClick={() => setTheme("light")}>
              White
            </button>
            <button type="button" aria-pressed={theme === "dark"} onClick={() => setTheme("dark")}>
              Black
            </button>
          </div>
          <details
            className="settings-menu"
            open={settingsOpen}
            onToggle={(event) => setSettingsOpen(event.currentTarget.open)}
          >
            <summary aria-label="Settings">
              <span className="settings-icon" aria-hidden="true">
                <span />
                <span />
                <span />
              </span>
              <span className="settings-label">Settings</span>
            </summary>
            <div className="settings-panel">
              <div className="settings-panel-head">
                <strong>Settings</strong>
                <span>Skills, memory, and runtime preferences live here.</span>
              </div>
              <SessionMemoryPanel
                memory={memoryState.state === "ready" ? memoryState.memory : undefined}
                loading={memoryState.state === "loading"}
                error={memoryState.state === "error" ? memoryState.error : undefined}
                noSession={memoryState.state === "empty"}
              />
              <SessionSkillsPanel
                skills={skillsState.state === "ready" ? skillsState.skills : undefined}
                loading={skillsState.state === "loading"}
                error={skillsState.state === "error" ? skillsState.error : undefined}
                defaultOpen
                installEnabled={skillsState.state === "ready" ? skillsState.installEnabled : false}
                onReadSkill={handleReadSkill}
                onInstallSkill={handleInstallSkill}
              />
            </div>
          </details>
          {showHeaderNewChat ? (
            <button type="button" className="header-new-chat" disabled={actionBusy} onClick={() => void handleNewSession()}>
              New chat
            </button>
          ) : null}
        </header>
      </div>
      <main className="app-main">
        <div
          className="workspace-shell"
          data-compact-nav={compactNav}
          data-session-nav={showSessionNav ? (sessionsCollapsed ? "collapsed" : "visible") : "hidden"}
          data-testid="workspace-shell"
        >
          {showSessionNav && sessionsCollapsed ? (
            <button
              type="button"
              className="session-rail-toggle"
              aria-label="Show chats"
              onClick={() => setSessionsCollapsed(false)}
            >
              <span aria-hidden="true">☰</span>
            </button>
          ) : null}
          {showSessionNav && !sessionsCollapsed ? (
            <SessionList
              sessions={sessions}
              selectedId={selectedSessionId}
              currentSession={session}
              pendingTask={pendingMessage?.kind === "task" ? pendingMessage.text : undefined}
              demoActive={demoActive}
              onSelect={(nextSessionId) => resetSessionSurface(nextSessionId, { preserveSession: true })}
              onNew={() => void handleNewSession()}
              onDelete={(sessionId) => void handleDeleteSession(sessionId)}
              deletingId={deletingSessionId}
              onCollapse={() => setSessionsCollapsed(true)}
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
                onOpenLatestChat={latestChatShortcut ? () => resetSessionSurface(latestChatShortcut.id, { preserveSession: true }) : undefined}
                initialHistoryFocus={selectedSessionId && !selectedSessionActive ? "answer" : "latest"}
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

function initialTheme(): ThemeMode {
  if (typeof window === "undefined") return "light";
  try {
    const stored = window.localStorage.getItem(themeStorageKey);
    if (stored === "dark" || stored === "light") return stored;
  } catch {
    return "light";
  }
  if (typeof window.matchMedia === "function" && window.matchMedia("(prefers-color-scheme: dark)").matches) {
    return "dark";
  }
  return "light";
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
