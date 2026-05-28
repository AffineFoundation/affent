import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type PointerEvent as ReactPointerEvent } from "react";
import "./styles/index.css";
import { ApiClient, ApiError } from "./api/client";
import {
  addSessionMemory,
  cancelSessionTurn,
  createSessionSchedule,
  createSession,
  deleteSkill,
  deleteSessionSchedule,
  deleteSessionLoopProtocol,
  deleteSession,
  getSessionMemory,
  getSessionLoopProtocol,
  getSessionPlan,
  getSessionHistory,
  installSkill,
  listSessionSchedules,
  listSessions,
  listSkills,
  readSessionArtifact,
  sessionArtifactPath,
  readSkill,
  removeSessionMemory,
  replaceSessionMemory,
  sendSessionMessage,
  streamSessionEvents,
  updateSessionLoopProtocol,
  updateSessionSchedule,
  type SessionScheduleDeleteResponse,
  type SessionSchedulesResponse,
  type SessionLoopProtocolDeleteResponse,
  type SessionLoopProtocolResponse,
  type SessionMemoryResponse,
  type SessionMemoryAddRequest,
  type SessionMemoryRemoveRequest,
  type SessionMemoryReplaceRequest,
  type SessionPlanSummary,
  type SessionContextSummary,
  type SessionSkillInfo,
  type SessionSkillInstallRequest,
  type SessionSummary,
} from "./api/sessions";
import { getServerStats, type ServerStatsResponse } from "./api/stats";
import {
  deleteAccountEnv,
  ensureAccountSSHKey,
  getAccountSettings,
  setAccountEnv,
  type AccountSettingsResponse,
} from "./api/settings";
import { ArtifactViewer, type ArtifactViewerState } from "./components/ArtifactViewer";
import { EventType, type RawEvent } from "./api/events";
import { Composer, type ComposerDraft } from "./components/Composer";
import { SessionList } from "./components/SessionList";
import { SessionMemoryPanel } from "./components/SessionMemoryPanel";
import { SessionPlanPanel } from "./components/SessionPlanPanel";
import { SessionLoopPanel } from "./components/SessionLoopPanel";
import { SessionSchedulePanel } from "./components/SessionSchedulePanel";
import { SessionSkillsPanel } from "./components/SessionSkillsPanel";
import { AccountSettingsPanel } from "./components/AccountSettingsPanel";
import { WorkbenchContextPanel } from "./components/WorkbenchContextPanel";
import { SessionArtifactsPanel } from "./components/SessionArtifactsPanel";
import { SessionFilesPanel } from "./components/SessionFilesPanel";
import { SessionChangesPanel } from "./components/SessionChangesPanel";
import { SessionRunPanel } from "./components/SessionRunPanel";
import { SessionWorkspacePanel } from "./components/SessionWorkspacePanel";
import { SessionTracePanel } from "./components/SessionTracePanel";
import { WorkbenchEmpty, WorkbenchPanel } from "./components/WorkbenchPanel";
import { WorkspaceStatusPill } from "./components/WorkspaceStatusPill";
import { Timeline, type GuidanceReceiptView, type PendingMessageView } from "./components/Timeline";
import { completedTurn } from "./fixtures/completedTurn";
import { applyRawEvent, reduceRawEvents } from "./store/reduce";
import { initialSessionState, type SessionState } from "./store/sessionState";
import { deriveWorkflowStatus } from "./store/workflowStatus";
import type { DraftSource } from "./view/draftSource";
import { buildRuntimeCapabilityView } from "./view/runtimeCapabilities";
import { buildSessionRows, formatLoadingChatTitle, isGenericChatTitle, summarizeSessionTitle } from "./view/sessionList";
import { buildSessionOverview, type SessionOverview } from "./view/sessionOverview";
import { buildSessionFiles } from "./view/sessionFiles";
import { buildSessionChanges } from "./view/sessionChanges";
import { buildSessionRun } from "./view/sessionRun";
import { buildSessionArtifacts } from "./view/sessionArtifacts";
import { buildSessionWorkspace } from "./view/sessionWorkspace";
import { buildSessionTrace } from "./view/sessionTrace";
import {
  buildConversationContextView,
  buildWorkbenchAttachment,
  buildWorkbenchContextUsage,
  workbenchContextUsageSummary,
  type WorkbenchContextUsageView,
} from "./view/workbenchContext";
import { buildWorkbenchAttention } from "./view/workbenchAttention";
import { buildWorkbenchNavItems, workbenchTabFromAttention, type WorkbenchTab } from "./view/workbenchNav";
import { buildSessionPlanFromToolResults } from "./view/sessionPlan";
import { decidePlanRunContinuation, nextIssuedPlanRunState, type PlanRunState } from "./view/planRun";
import {
  buildAutomationContext,
  shouldShowLoopContext,
  shouldShowScheduleContext,
  type AutomationLoopPanelState,
  type AutomationSchedulePanelState,
} from "./view/automationContext";
import { conversationTopicFromTurns, isContinuationPrompt } from "./view/continuationPrompt";
import { memoryUpdatesForTurn } from "./view/memoryUpdate";

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
  | { state: "loading"; memory?: SessionMemoryResponse }
  | { state: "ready"; memory: SessionMemoryResponse }
  | { state: "error"; error: string; memory?: SessionMemoryResponse };

type RuntimeStatsState =
  | { state: "idle" }
  | { state: "loading" }
  | { state: "ready"; stats: ServerStatsResponse }
  | { state: "error"; error: string };

function memoryStateSnapshot(state: MemoryState): SessionMemoryResponse | undefined {
  if (state.state === "ready" || state.state === "loading" || state.state === "error") return state.memory;
  return undefined;
}

type AccountSettingsState =
  | { state: "idle" }
  | { state: "loading" }
  | { state: "ready"; settings: AccountSettingsResponse }
  | { state: "error"; error: string; settings?: AccountSettingsResponse };

type PlanState =
  | { state: "idle" }
  | { state: "empty" }
  | { state: "loading" }
  | { state: "ready"; plan: unknown; summary?: SessionPlanSummary }
  | { state: "error"; error: string; summary?: SessionPlanSummary };

type LoopProtocolState = AutomationLoopPanelState;
type ScheduleState = AutomationSchedulePanelState;

const demoReplayDelayMs = 180;
const historyPageLimit = 500;
const maxHistoryPages = 50;
const themeStorageKey = "affent.theme";
const sessionUrlParam = "sessionId";
const legacySessionUrlParam = "session";
const minSessionPanelWidth = 220;
const maxSessionPanelWidth = 420;
const minWorkbenchPanelWidth = 420;
const maxWorkbenchPanelWidth = 940;

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
  const [selectedSessionId, setSelectedSessionId] = useState<string | undefined>(() => sessionIdFromCurrentUrl());
  const [sessionIndexReady, setSessionIndexReady] = useState(false);
  const [liveConnectTick, setLiveConnectTick] = useState(0);
  const [session, setSession] = useState<SessionState>(() => initialSessionState());
  const [actionBusy, setActionBusy] = useState(false);
  const [cancelBusy, setCancelBusy] = useState(false);
  const [loopProtocolBusy, setLoopProtocolBusy] = useState(false);
  const [scheduleBusy, setScheduleBusy] = useState<"loop" | "checkin" | "daily" | undefined>();
  const [deletingSessionId, setDeletingSessionId] = useState<string | undefined>();
  const [pendingMessage, setPendingMessage] = useState<PendingMessageView | undefined>();
  const [guidanceReceipts, setGuidanceReceipts] = useState<GuidanceReceiptView[]>([]);
  const [skillsState, setSkillsState] = useState<SkillsState>({ state: "idle" });
  const [memoryState, setMemoryState] = useState<MemoryState>({ state: "idle" });
  const [runtimeStatsState, setRuntimeStatsState] = useState<RuntimeStatsState>({ state: "idle" });
  const [accountSettingsState, setAccountSettingsState] = useState<AccountSettingsState>({ state: "idle" });
  const [accountSettingsBusy, setAccountSettingsBusy] = useState<"env" | "ssh" | undefined>();
  const [livePlanSummary, setLivePlanSummary] = useState<SessionPlanSummary | undefined>();
  const [planState, setPlanState] = useState<PlanState>({ state: "idle" });
  const [planRunState, setPlanRunState] = useState<PlanRunState | undefined>();
  const [loopProtocolState, setLoopProtocolState] = useState<LoopProtocolState>({ state: "idle" });
  const [scheduleState, setScheduleState] = useState<ScheduleState>({ state: "idle" });
  const [deletingScheduleId, setDeletingScheduleId] = useState<string | undefined>();
  const [updatingScheduleId, setUpdatingScheduleId] = useState<string | undefined>();
  const [workbenchOpen, setWorkbenchOpen] = useState(false);
  const [workbenchTab, setWorkbenchTab] = useState<WorkbenchTab>("context");
  const [sessionsCollapsed, setSessionsCollapsed] = useState(false);
  const [sessionsExpandedInWorkbench, setSessionsExpandedInWorkbench] = useState(false);
  const [sessionPanelWidth, setSessionPanelWidth] = useState(280);
  const [workbenchPanelWidth, setWorkbenchPanelWidth] = useState<number | undefined>();
  const [mobileTopbarHidden, setMobileTopbarHidden] = useState(false);
  const [composerDraft, setComposerDraft] = useState<ComposerDraft | undefined>();
  const [composerFocusSignal, setComposerFocusSignal] = useState(0);
  const [artifact, setArtifact] = useState<ArtifactViewerState>({ state: "idle" });
  const sendInFlightRef = useRef(false);
  const initialUrlSessionIdRef = useRef(selectedSessionId);
  const sendFailedRef = useRef(false);
  const streamClosedRef = useRef(false);
  const streamSessionIdRef = useRef<string | undefined>(undefined);
  const selectedSessionIdRef = useRef(selectedSessionId);
  const pendingMessageRef = useRef(pendingMessage);
  const nextGuidanceReceiptId = useRef(0);
  const planFetchKeyRef = useRef("");
  const planFetchInFlightKeyRef = useRef("");
  const conversationScrollRef = useRef<HTMLDivElement | null>(null);
  const topbarRef = useRef<HTMLDivElement | null>(null);
  const workspaceShellRef = useRef<HTMLDivElement | null>(null);
  const sessionsRef = useRef(sessions);
  useEffect(() => {
    sessionsRef.current = sessions;
  }, [sessions]);

  useEffect(() => {
    syncSessionIdToUrl(selectedSessionId);
    selectedSessionIdRef.current = selectedSessionId;
  }, [selectedSessionId]);

  useEffect(() => {
    pendingMessageRef.current = pendingMessage;
  }, [pendingMessage]);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    try {
      window.localStorage.setItem(themeStorageKey, theme);
    } catch {
      // Local persistence is best effort; theme switching still works.
    }
  }, [theme]);

  useEffect(() => {
    const root = document.documentElement;
    const topbar = topbarRef.current;
    const viewport = window.visualViewport;
    const updateViewportVars = () => {
      root.style.setProperty("--app-viewport-height", `${Math.round(viewport?.height ?? window.innerHeight)}px`);
      root.style.setProperty("--app-topbar-height", `${Math.round(topbar?.getBoundingClientRect().height ?? 0)}px`);
    };
    updateViewportVars();
    viewport?.addEventListener("resize", updateViewportVars);
    viewport?.addEventListener("scroll", updateViewportVars);
    window.addEventListener("resize", updateViewportVars);
    const observer = typeof ResizeObserver !== "undefined" && topbar ? new ResizeObserver(updateViewportVars) : undefined;
    if (observer && topbar) observer.observe(topbar);
    return () => {
      viewport?.removeEventListener("resize", updateViewportVars);
      viewport?.removeEventListener("scroll", updateViewportVars);
      window.removeEventListener("resize", updateViewportVars);
      observer?.disconnect();
    };
  }, []);

  const demoActive = status.state === "demo";
  const selectedSession = useMemo(
    () => sessions.find((candidate) => candidate.id === selectedSessionId),
    [selectedSessionId, sessions],
  );
  const selectedSessionTitle = useMemo(() => {
    return selectedSessionDisplayTitle(selectedSession, session);
  }, [selectedSession, session]);
  const selectedSessionActive = selectedSession?.active === true;
  const selectedLoopState = selectedSession?.loop_protocol?.state ?? selectedSession?.loop_state;
  const selectedLoopProtocolState = loopProtocolState.state !== "idle" && loopProtocolState.sessionId === selectedSessionId
    ? loopProtocolState
    : { state: "idle" as const };
  const selectedScheduleState = scheduleState.state !== "idle" && scheduleState.sessionId === selectedSessionId
    ? scheduleState
    : { state: "idle" as const };
  const showLoopContext = !demoActive && !!selectedSessionId && shouldShowLoopContext(selectedSession, selectedLoopState, selectedLoopProtocolState, loopProtocolBusy);
  const showScheduleContext = !demoActive && !!selectedSessionId && shouldShowScheduleContext(selectedSession, selectedScheduleState, scheduleBusy, deletingScheduleId, updatingScheduleId);
  const workflow = useMemo(() => deriveWorkflowStatus(session), [session]);
  const memoryUpdateCount = useMemo(
    () => session.turns.reduce((sum, turn) => sum + memoryUpdatesForTurn(turn).length, 0),
    [session.turns],
  );
  const latestMemoryUpdate = useMemo(() => {
    for (let index = session.turns.length - 1; index >= 0; index -= 1) {
      const updates = memoryUpdatesForTurn(session.turns[index]);
      const latest = updates.at(-1);
      if (latest) return latest;
    }
    return selectedSession?.latest_memory_update;
  }, [selectedSession?.latest_memory_update, session.turns]);
  const planMutationCount = useMemo(
    () => session.turns.reduce(
      (sum, turn) => sum + turn.toolCalls.filter((call) => (
        call.tool === "plan"
        && call.status === "success"
        && call.exitCode === 0
        && isPlanMutationAction(call.args.action)
      )).length,
      0,
    ),
    [session.turns],
  );
  const derivedPlan = useMemo(() => buildSessionPlanFromToolResults(session), [session]);
  const planSummary = livePlanSummary ?? selectedSession?.plan_summary ?? derivedPlan?.summary;
  const planPanelSummary = planState.state === "ready" || planState.state === "error"
    ? planState.summary ?? planSummary
    : planSummary;
  const planPanelPlan = planState.state === "ready" ? planState.plan : derivedPlan?.plan;
  const planPanelLoading = planState.state === "loading" && !derivedPlan;
  const planPanelError = planState.state === "error" && !derivedPlan ? planState.error : undefined;
  const planRunActive = !!planRunState && planRunState.sessionId === selectedSessionId;
  const capabilityView = useMemo(
    () => buildRuntimeCapabilityView(selectedSession?.capabilities, { selectedSessionId }),
    [selectedSession?.capabilities, selectedSessionId],
  );
  const overview = useMemo(
    () => buildSessionOverview({
      session,
      workflow,
      hasSelectedSession: !!selectedSessionId,
      pendingTask: pendingMessage?.kind === "task" ? pendingMessageDisplay(pendingMessage) : undefined,
      pendingGuidance: pendingMessage?.kind === "guidance" ? pendingMessageDisplay(pendingMessage) : undefined,
      sessionTitle: selectedSessionTitle,
      planSummary: planPanelSummary,
      contextSummary: selectedSession?.context,
      recoveryHint: selectedSession?.latest_recovery_hint,
    }),
    [pendingMessage, planPanelSummary, selectedSession?.context, selectedSession?.latest_recovery_hint, selectedSessionId, selectedSessionTitle, session, workflow],
  );
  const sessionFiles = useMemo(() => buildSessionFiles(session), [session]);
  const sessionChanges = useMemo(() => buildSessionChanges(session), [session]);
  const sessionRun = useMemo(() => buildSessionRun(session), [session]);
  const sessionArtifacts = useMemo(() => buildSessionArtifacts(session), [session]);
  const sessionWorkspace = useMemo(() => buildSessionWorkspace(selectedSession, sessionRun), [selectedSession, sessionRun]);
  const workbenchContextUsage = useMemo(() => buildWorkbenchContextUsage(session, selectedSession), [session, selectedSession]);
  const conversationContext = useMemo(() => buildConversationContextView(session, selectedSession?.context), [selectedSession?.context, session]);
  const workbenchAttachment = useMemo(
    () => buildWorkbenchAttachment({
      selectedSessionId,
      selectedSessionTitle,
      selectedSession,
      workspace: sessionWorkspace,
      usage: workbenchContextUsage,
    }),
    [selectedSession, selectedSessionId, selectedSessionTitle, sessionWorkspace, workbenchContextUsage],
  );
  const sessionTrace = useMemo(() => buildSessionTrace(session), [session]);
  const hasSessionNav = !demoActive && sessions.length > 0;
  const showSessionNav = hasSessionNav && (!workbenchOpen || sessionsExpandedInWorkbench);
  const showSessionRailToggle = hasSessionNav && (
    (showSessionNav && sessionsCollapsed)
    || (workbenchOpen && !sessionsExpandedInWorkbench)
  );
  const compactNav = demoActive || (!showSessionNav && !showSessionRailToggle);
  const sessionNavState = showSessionNav ? (sessionsCollapsed ? "collapsed" : "visible") : showSessionRailToggle ? "collapsed" : "hidden";
  const showHeaderNewChat = !demoActive && !hasSessionNav;
  const showChatContext = !demoActive && (session.turns.length > 0 || !!pendingMessage);
  const showAutomationContext = showLoopContext || showScheduleContext;
  const automationContext = showAutomationContext
    ? buildAutomationContext(selectedSession, selectedLoopState, selectedLoopProtocolState, selectedScheduleState)
    : undefined;
  const workbenchAttention = useMemo(
    () => buildWorkbenchAttention({
      overview,
      files: sessionFiles,
      changes: sessionChanges,
      run: sessionRun,
      workspace: sessionWorkspace,
      automation: automationContext,
    }),
    [automationContext, overview, sessionChanges, sessionFiles, sessionRun, sessionWorkspace],
  );
  const showSurfaceContext = showChatContext;
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
    if (demoActive || !workbenchOpen) {
      setRuntimeStatsState({ state: "idle" });
      return;
    }
    const ac = new AbortController();
    setRuntimeStatsState({ state: "loading" });
    getServerStats(client, ac.signal)
      .then((stats) => {
        setRuntimeStatsState({ state: "ready", stats });
      })
      .catch((err) => {
        if (isAbortError(err)) return;
        setRuntimeStatsState({ state: "error", error: formatError(err) });
      });
    return () => ac.abort();
  }, [client, demoActive, workbenchOpen]);

  useEffect(() => {
    if (demoActive) {
      setAccountSettingsState({ state: "idle" });
      return;
    }
    if (!workbenchOpen || workbenchTab !== "config") return;
    const ac = new AbortController();
    setAccountSettingsState({ state: "loading" });
    getAccountSettings(client, ac.signal)
      .then((settings) => {
        setAccountSettingsState({ state: "ready", settings });
      })
      .catch((err) => {
        if (isAbortError(err)) return;
        setAccountSettingsState({ state: "error", error: formatError(err) });
      });
    return () => ac.abort();
  }, [client, demoActive, workbenchOpen, workbenchTab]);

  useEffect(() => {
    if (demoActive) {
      setSkillsState({ state: "idle" });
      return;
    }
    if (!workbenchOpen || workbenchTab !== "skills") return;
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
  }, [client, demoActive, workbenchOpen, workbenchTab]);

  useEffect(() => {
    setMemoryState(selectedSessionId ? { state: "idle" } : { state: "empty" });
  }, [selectedSessionId]);

  useEffect(() => {
    if (demoActive) {
      setMemoryState({ state: "idle" });
      return;
    }
    if (!selectedSessionId) {
      setMemoryState({ state: "empty" });
      return;
    }
    if (!workbenchOpen || workbenchTab !== "memory") return;
    const ac = new AbortController();
    setMemoryState((current) => ({ state: "loading", memory: memoryStateSnapshot(current) }));
    getSessionMemory(client, selectedSessionId, ac.signal)
      .then((memory) => {
        setMemoryState({ state: "ready", memory });
      })
      .catch((err) => {
        if (isAbortError(err)) return;
        setMemoryState((current) => ({ state: "error", error: formatError(err), memory: memoryStateSnapshot(current) }));
      });
    return () => ac.abort();
  }, [client, demoActive, memoryUpdateCount, selectedSessionId, workbenchOpen, workbenchTab]);

  useEffect(() => {
    if (!selectedSessionId || memoryUpdateCount <= 0) return;
    setSessions((current) => {
      let changed = false;
      const next = current.map((item) => {
        if (item.id !== selectedSessionId || item.has_memory) return item;
        changed = true;
        return { ...item, has_memory: true };
      });
      return changed ? next : current;
    });
  }, [memoryUpdateCount, selectedSessionId]);

  useEffect(() => {
    setLivePlanSummary(undefined);
    setPlanState({ state: "idle" });
    setPlanRunState(undefined);
    planFetchKeyRef.current = "";
    planFetchInFlightKeyRef.current = "";
  }, [selectedSessionId]);

  useEffect(() => {
    if (demoActive || !selectedSessionId) {
      setPlanState({ state: "idle" });
      return;
    }
    const fallbackSummary = selectedSession?.plan_summary;
    const hasPlanHint = planMutationCount > 0 || !!selectedSession?.has_plan || !!fallbackSummary;
    if (!hasPlanHint) {
      setPlanState({ state: "empty" });
      planFetchKeyRef.current = "";
      planFetchInFlightKeyRef.current = "";
      return;
    }
    const fetchKey = `${selectedSessionId}:${planMutationCount}`;
    if (planFetchKeyRef.current === fetchKey || planFetchInFlightKeyRef.current === fetchKey) return;
    planFetchInFlightKeyRef.current = fetchKey;
    const ac = new AbortController();
    setPlanState({ state: "loading" });
    getSessionPlan(client, selectedSessionId, ac.signal)
      .then((resp) => {
        planFetchKeyRef.current = fetchKey;
        const nextSummary = resp.summary;
        setLivePlanSummary(nextSummary);
        setPlanState({ state: "ready", plan: resp.plan, summary: nextSummary });
        setSessions((current) => {
          let changed = false;
          const next = current.map((item) => {
            if (item.id !== selectedSessionId) return item;
            changed = true;
            return { ...item, has_plan: !!nextSummary, plan_summary: nextSummary };
          });
          return changed ? next : current;
        });
      })
      .catch((err) => {
        if (isAbortError(err)) return;
        if (err instanceof ApiError && err.status === 404) {
          planFetchKeyRef.current = fetchKey;
          setLivePlanSummary(undefined);
          setPlanState({ state: "empty" });
          setSessions((current) => {
            let changed = false;
            const next = current.map((item) => {
              if (item.id !== selectedSessionId || (!item.has_plan && !item.plan_summary)) return item;
              changed = true;
              return { ...item, has_plan: false, plan_summary: undefined };
            });
            return changed ? next : current;
          });
          return;
        }
        setPlanState({ state: "error", error: formatError(err), summary: fallbackSummary });
      })
      .finally(() => {
        if (planFetchInFlightKeyRef.current === fetchKey) planFetchInFlightKeyRef.current = "";
      });
    return () => ac.abort();
  }, [client, demoActive, planMutationCount, selectedSession?.has_plan, selectedSession?.plan_summary?.label, selectedSessionId]);

  useEffect(() => {
    const decision = decidePlanRunContinuation({
      state: planRunState,
      sessionId: selectedSessionId,
      busy: actionBusy,
      sessionRunning: session.status === "running",
      hasPendingMessage: !!pendingMessage,
      planLoading: planState.state === "loading",
      planMutationCount,
      fetchedPlanKey: planFetchKeyRef.current,
      summary: planPanelSummary,
    });
    if (decision.action === "idle" || decision.action === "wait") return;
    if (decision.action === "clear") {
      setPlanRunState(undefined);
      return;
    }
    if (decision.action === "pause" || decision.action === "limit") {
      setPlanRunState(undefined);
      setStatus((current) => ({ ...current, detail: decision.detail }));
      return;
    }
    void handleExecutePlanStep({ runRemaining: true });
  }, [
    actionBusy,
    pendingMessage,
    planPanelSummary?.active,
    planPanelSummary?.blocked,
    planPanelSummary?.completed_steps,
    planPanelSummary?.current_step_index,
    planPanelSummary?.done,
    planMutationCount,
    planRunState,
    planState.state,
    selectedSessionId,
    session.status,
  ]);

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

  const handleDeleteSkill = useCallback(
    async (name: string): Promise<void> => {
      await deleteSkill(client, name);
      setSkillsState((current) => {
        if (current.state !== "ready") return current;
        return { ...current, skills: current.skills.filter((skill) => skill.name !== name) };
      });
      try {
        const resp = await listSkills(client);
        setSkillsState({ state: "ready", skills: resp.skills, installEnabled: resp.install_enabled });
      } catch {
        // Deletion already succeeded. Leave the optimistic state in place.
      }
    },
    [client],
  );

  const handleRefreshSkills = useCallback(async () => {
    setSkillsState({ state: "loading" });
    try {
      const resp = await listSkills(client);
      setSkillsState({ state: "ready", skills: resp.skills, installEnabled: resp.install_enabled });
    } catch (err) {
      setSkillsState({ state: "error", error: formatError(err) });
      throw err;
    }
  }, [client]);

  const handleRefreshMemory = useCallback(async () => {
    if (!selectedSessionId) {
      setMemoryState({ state: "empty" });
      return;
    }
    setMemoryState((current) => ({ state: "loading", memory: memoryStateSnapshot(current) }));
    try {
      const memory = await getSessionMemory(client, selectedSessionId);
      setMemoryState({ state: "ready", memory });
    } catch (err) {
      setMemoryState((current) => ({ state: "error", error: formatError(err), memory: memoryStateSnapshot(current) }));
      throw err;
    }
  }, [client, selectedSessionId]);

  const handleAddMemory = useCallback(async (request: SessionMemoryAddRequest): Promise<SessionMemoryResponse> => {
    if (!selectedSessionId) {
      setMemoryState({ state: "empty" });
      throw new Error("Open a saved chat before saving memory.");
    }
    setMemoryState((current) => ({ state: "loading", memory: memoryStateSnapshot(current) }));
    try {
      const memory = await addSessionMemory(client, selectedSessionId, request);
      setMemoryState({ state: "ready", memory });
      setSessions((current) => current.map((item) => item.id === selectedSessionId ? { ...item, has_memory: true } : item));
      return memory;
    } catch (err) {
      setMemoryState((current) => ({ state: "error", error: formatError(err), memory: memoryStateSnapshot(current) }));
      throw err;
    }
  }, [client, selectedSessionId]);

  const handleRemoveMemory = useCallback(async (request: SessionMemoryRemoveRequest): Promise<SessionMemoryResponse> => {
    if (!selectedSessionId) {
      setMemoryState({ state: "empty" });
      throw new Error("Open a saved chat before editing memory.");
    }
    setMemoryState((current) => ({ state: "loading", memory: memoryStateSnapshot(current) }));
    try {
      const memory = await removeSessionMemory(client, selectedSessionId, request);
      setMemoryState({ state: "ready", memory });
      setSessions((current) => current.map((item) => item.id === selectedSessionId ? { ...item, has_memory: memory.has_memory } : item));
      return memory;
    } catch (err) {
      setMemoryState((current) => ({ state: "error", error: formatError(err), memory: memoryStateSnapshot(current) }));
      throw err;
    }
  }, [client, selectedSessionId]);

  const handleReplaceMemory = useCallback(async (request: SessionMemoryReplaceRequest): Promise<SessionMemoryResponse> => {
    if (!selectedSessionId) {
      setMemoryState({ state: "empty" });
      throw new Error("Open a saved chat before editing memory.");
    }
    setMemoryState((current) => ({ state: "loading", memory: memoryStateSnapshot(current) }));
    try {
      const memory = await replaceSessionMemory(client, selectedSessionId, request);
      setMemoryState({ state: "ready", memory });
      setSessions((current) => current.map((item) => item.id === selectedSessionId ? { ...item, has_memory: memory.has_memory } : item));
      return memory;
    } catch (err) {
      setMemoryState((current) => ({ state: "error", error: formatError(err), memory: memoryStateSnapshot(current) }));
      throw err;
    }
  }, [client, selectedSessionId]);

  const handleRefreshAccountSettings = useCallback(async () => {
    const settings = await getAccountSettings(client);
    setAccountSettingsState({ state: "ready", settings });
  }, [client]);

  const handleSetAccountEnv = useCallback(
    async (name: string, value: string) => {
      setAccountSettingsBusy("env");
      try {
        const settings = await setAccountEnv(client, { name, value });
        setAccountSettingsState({ state: "ready", settings });
      } catch (err) {
        setAccountSettingsState((current) => ({
          state: "error",
          error: formatError(err),
          settings: current.state === "ready" ? current.settings : current.state === "error" ? current.settings : undefined,
        }));
        throw err;
      } finally {
        setAccountSettingsBusy(undefined);
      }
    },
    [client],
  );

  const handleDeleteAccountEnv = useCallback(
    async (name: string) => {
      setAccountSettingsBusy("env");
      try {
        const settings = await deleteAccountEnv(client, name);
        setAccountSettingsState({ state: "ready", settings });
      } catch (err) {
        setAccountSettingsState((current) => ({
          state: "error",
          error: formatError(err),
          settings: current.state === "ready" ? current.settings : current.state === "error" ? current.settings : undefined,
        }));
        throw err;
      } finally {
        setAccountSettingsBusy(undefined);
      }
    },
    [client],
  );

  const handleEnsureAccountSSHKey = useCallback(async () => {
    setAccountSettingsBusy("ssh");
    try {
      const settings = await ensureAccountSSHKey(client);
      setAccountSettingsState({ state: "ready", settings });
    } catch (err) {
      setAccountSettingsState((current) => ({
        state: "error",
        error: formatError(err),
        settings: current.state === "ready" ? current.settings : current.state === "error" ? current.settings : undefined,
      }));
      throw err;
    } finally {
      setAccountSettingsBusy(undefined);
    }
  }, [client]);

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
        const mergedSessions = mergeSessionIndex(resp.sessions, sessionsRef.current);
        setSessions(mergedSessions);
        const requestedSessionId = initialUrlSessionIdRef.current;
        const activeSessionId = mergedSessions.find((s) => s.active)?.id;
        const nextSelected = selectedSessionIdRef.current ?? requestedSessionId ?? activeSessionId;
        setSelectedSessionId((current) => {
          const resolved = current ?? nextSelected;
          selectedSessionIdRef.current = resolved;
          return resolved;
        });
        if (!nextSelected) setSession(initialSessionState());
        if (nextSelected) {
          setStatus({
            state: "loading",
            label: "Loading chat",
            detail: loadingSessionDetail(nextSelected, mergedSessions),
          });
        } else {
          setStatus({
            state: "connected",
            label: "Connected",
            detail: sessionListDetail(mergedSessions),
          });
        }
        setSessionIndexReady(true);
      } catch (err) {
        if (isAbortError(err)) return;
        setSessions([]);
        selectedSessionIdRef.current = undefined;
        setSelectedSessionId(undefined);
        setSession(initialSessionState());
        setSessionIndexReady(true);
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
    const accepted = session.turns.some((turn) => pendingMessageMatchesTurn(pendingMessage, turn.userText));
    if (accepted) setPendingMessage(undefined);
  }, [pendingMessage, session.turns]);

  useEffect(() => {
    if (session.status === "running") return;
    setGuidanceReceipts([]);
    setCancelBusy(false);
  }, [session.status]);

  useEffect(() => {
    if (!selectedSessionId || demoActive) return;
    if (!sessionIndexReady) return;
    const liveSessionId = selectedSessionId;
    const ac = new AbortController();
    async function connectLive() {
      try {
        if (sendFailedRef.current || sendInFlightRef.current) return;
        const history = await loadHistory(liveSessionId, ac.signal);
        if (ac.signal.aborted) return;
        releaseAcceptedPendingTurn(history.session);
        if (history.session.status !== "running") {
          setActionBusy(false);
          setCancelBusy(false);
        }
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
  }, [client, demoActive, liveConnectTick, loadHistory, selectedSessionActive, selectedSessionId, sessionIndexReady]);

  function resetSessionSurface(nextSessionId: string, opts?: { preserveSession?: boolean }) {
    if (nextSessionId === selectedSessionId) return;
    streamClosedRef.current = false;
    streamSessionIdRef.current = undefined;
    sendInFlightRef.current = false;
    sendFailedRef.current = false;
    selectedSessionIdRef.current = nextSessionId;
    setSelectedSessionId(nextSessionId);
    if (!opts?.preserveSession) setSession(initialSessionState());
    setPendingMessage(undefined);
    setGuidanceReceipts([]);
    setArtifact({ state: "idle" });
    setActionBusy(false);
    setCancelBusy(false);
    setLoopProtocolBusy(false);
    setScheduleBusy(undefined);
    setScheduleState({ state: "idle" });
    setDeletingScheduleId(undefined);
    setUpdatingScheduleId(undefined);
    setStatus({ state: "loading", label: "Loading chat", detail: loadingSessionDetail(nextSessionId) });
    setLoopProtocolState({ state: "idle" });
  }

  async function handleNewSession(): Promise<string | undefined> {
    streamClosedRef.current = false;
    streamSessionIdRef.current = undefined;
    sendInFlightRef.current = false;
    sendFailedRef.current = false;
    selectedSessionIdRef.current = undefined;
    setSelectedSessionId(undefined);
    setSession(initialSessionState());
    setPendingMessage(undefined);
    setGuidanceReceipts([]);
    setArtifact({ state: "idle" });
    setActionBusy(false);
    setCancelBusy(false);
    setLoopProtocolBusy(false);
    setScheduleBusy(undefined);
    setScheduleState({ state: "idle" });
    setDeletingScheduleId(undefined);
    setUpdatingScheduleId(undefined);
    setComposerDraft(undefined);
    setComposerFocusSignal((current) => current + 1);
    setStatus({ state: "connected", label: "Ready", detail: "Ready to chat" });
    setLoopProtocolState({ state: "idle" });
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
        selectedSessionIdRef.current = undefined;
        setSelectedSessionId(undefined);
        setSession(initialSessionState());
        setPendingMessage(undefined);
        setGuidanceReceipts([]);
        setArtifact({ state: "idle" });
        setActionBusy(false);
        setCancelBusy(false);
        setLoopProtocolBusy(false);
        setScheduleBusy(undefined);
        setScheduleState({ state: "idle" });
        setDeletingScheduleId(undefined);
        setUpdatingScheduleId(undefined);
        setLoopProtocolState({ state: "idle" });
      }
      setStatus({ state: "connected", label: "Ready", detail: "Chat deleted" });
    } catch (err) {
      setStatus({ state: "error", label: "Delete failed", detail: formatError(err) });
    } finally {
      setDeletingSessionId(undefined);
    }
  }

  async function handleDisableLoopProtocol(): Promise<void> {
    if (!selectedSessionId || loopProtocolBusy) return;
    const sessionId = selectedSessionId;
    setLoopProtocolBusy(true);
    try {
      const resp = await deleteSessionLoopProtocol(client, sessionId);
      markSessionLoopProtocolDisabled(sessionId, resp);
      setLoopProtocolState({ state: "idle" });
      setStatus({ state: "connected", label: "Ready", detail: resp.cleared ? "Loop disabled" : "Loop already disabled" });
    } catch (err) {
      setStatus({ state: "error", label: "Loop disable failed", detail: formatError(err) });
    } finally {
      setLoopProtocolBusy(false);
    }
  }

  async function handleLoadLoopProtocol(): Promise<void> {
    if (!selectedSessionId || selectedLoopProtocolState.state === "loading") return;
    const sessionId = selectedSessionId;
    setLoopProtocolState({ state: "loading", sessionId });
    try {
      const protocol = await getSessionLoopProtocol(client, sessionId);
      setLoopProtocolState({ state: "ready", sessionId, protocol });
    } catch (err) {
      setLoopProtocolState({ state: "error", sessionId, error: formatError(err) });
    }
  }

  function handleUseLoopProtocolDraft() {
    const goal = selectedLoopState?.initial_goal_preview?.trim() || selectedSessionTitle || "this long-running session";
    const status = selectedLoopState?.status || selectedSession?.loop_protocol?.status;
    const calibrationQuestions = selectedLoopState?.calibration_questions ?? selectedSession?.loop_protocol?.state?.calibration_questions ?? 0;
    const calibrationQuestion = selectedLoopState?.last_calibration_question_preview || selectedSession?.loop_protocol?.state?.last_calibration_question_preview;
    const calibrationAnswers = selectedLoopState?.calibration_answers ?? selectedSession?.loop_protocol?.state?.calibration_answers ?? 0;
    const calibrationPreview = selectedLoopState?.last_calibration_answer_preview || selectedSession?.loop_protocol?.state?.last_calibration_answer_preview;
    setComposerDraft({
      id: Date.now(),
      source: "starter",
      content: webLoopProtocolDraftPrompt(goal, status, calibrationQuestions, calibrationQuestion, calibrationAnswers, calibrationPreview),
    });
    setComposerFocusSignal((current) => current + 1);
  }

  async function handleSend(content: string) {
    const loopGoal = loopStartGoalFromComposer(content);
    if (loopGoal) {
      await handleStartLoop(loopGoal);
      return;
    }
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
        selectedSessionIdRef.current = targetSessionId;
        setSelectedSessionId(targetSessionId);
        setSession(initialSessionState());
        markSessionLive(targetSessionId, content, created.session);
      } else if (pendingKind === "task") {
        markSessionLive(targetSessionId, content);
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
        if (pendingKind === "task" && targetSessionId === selectedSessionIdRef.current && sessionIndexReady) {
          setLiveConnectTick((current) => current + 1);
          setStatus((current) => ({ ...current, state: "live", label: "Running" }));
        } else {
          const reconciled = await loadHistory(targetSessionId);
          if (pendingKind === "task") releaseSettledTurn(reconciled.session, content);
          setStatus({ state: "disconnected", label: "Disconnected", detail: "chat refreshed" });
        }
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

  async function handleStartLoop(goal: string) {
    const trimmedGoal = goal.trim();
    if (!trimmedGoal) return;
    let targetSessionId = selectedSessionId;
    const displayText = `Set up loop: ${trimmedGoal}`;
    sendInFlightRef.current = true;
    sendFailedRef.current = false;
    setPendingMessage({ text: trimmedGoal, displayText, kind: "task" });
    setActionBusy(true);
    try {
      if (!targetSessionId) {
        const created = await createSession(client);
        targetSessionId = created.session.id;
        selectedSessionIdRef.current = targetSessionId;
        setSelectedSessionId(targetSessionId);
        setSession(initialSessionState());
        markSessionLive(targetSessionId, displayText, created.session);
      } else {
        markSessionLive(targetSessionId, displayText);
      }
      await sendSessionMessage(client, targetSessionId, {
        content: trimmedGoal,
        display_text: displayText,
        mode: "loop_setup",
      });
      sendInFlightRef.current = false;
      markSessionLive(targetSessionId, displayText);
      const hasOpenStream = streamSessionIdRef.current === targetSessionId && !streamClosedRef.current;
      if (!hasOpenStream) {
        const reconciled = await loadHistory(targetSessionId);
        releaseSettledTurn(reconciled.session, trimmedGoal);
        setStatus({ state: "disconnected", label: "Disconnected", detail: "chat refreshed" });
      } else {
        setStatus((current) => ({ ...current, state: "live", label: "Running" }));
      }
    } catch (err) {
      sendInFlightRef.current = false;
      sendFailedRef.current = true;
      setStatus({ state: "error", label: "Loop start failed", detail: formatError(err) });
      setPendingMessage(undefined);
      setActionBusy(false);
      throw err;
    }
  }

  async function handleCreateSchedule(kind: "loop" | "checkin" | "daily") {
    if (!selectedSessionId || scheduleBusy || actionBusy || session.status === "running") return;
    const sessionId = selectedSessionId;
    const loopTick = kind === "loop";
    const daily = kind === "daily";
    const sessionTitle = selectedSessionTitle ?? sessionId;
    const intervalSeconds = loopTick ? 30 * 60 : daily ? 24 * 60 * 60 : undefined;
    const firstDelayMs = loopTick ? 30 * 60 * 1000 : daily ? 24 * 60 * 60 * 1000 : 60 * 60 * 1000;
    const next = new Date(Date.now() + firstDelayMs);
    const scheduleDisplay = webScheduleDisplayText(kind, sessionTitle);
    const calibrationPrompt = webScheduleCalibrationPrompt(kind, sessionTitle);
    const calibrationDisplay = webScheduleCalibrationSummary(kind, sessionTitle);
    setScheduleBusy(kind);
    try {
      const resp = await createSessionSchedule(client, sessionId, {
        kind: loopTick ? "loop_tick" : daily ? "daily_checkin" : "checkin",
        prompt: loopTick
          ? webScheduledLoopTickPrompt(sessionTitle)
          : webScheduledCheckInPrompt(sessionTitle),
        display_text: scheduleDisplay,
        next_run_at: toRfc3339Seconds(next),
        repeat_interval_seconds: intervalSeconds,
        enabled: true,
      });
      markSessionSchedules(sessionId, resp);
      setScheduleState({ state: "ready", sessionId, schedules: resp.schedules });
      if (!selectedSession?.has_loop_protocol) {
        const loopProtocol = await updateSessionLoopProtocol(client, sessionId, {
          activate: true,
          goal: webScheduleCalibrationGoal(kind, sessionTitle),
        });
        markSessionLoopProtocol(sessionId, loopProtocol, webScheduleCalibrationGoal(kind, sessionTitle));
      }
      sendInFlightRef.current = true;
      sendFailedRef.current = false;
      setPendingMessage({ text: calibrationPrompt, displayText: calibrationDisplay, kind: "task" });
      setActionBusy(true);
      await sendSessionMessage(client, sessionId, { content: calibrationPrompt, display_text: calibrationDisplay });
      sendInFlightRef.current = false;
      markSessionLive(sessionId, calibrationDisplay);
      const hasOpenStream = streamSessionIdRef.current === sessionId && !streamClosedRef.current;
      if (!hasOpenStream) {
        const reconciled = await loadHistory(sessionId);
        releaseSettledTurn(reconciled.session, calibrationPrompt);
        setStatus({ state: "disconnected", label: "Disconnected", detail: "chat refreshed" });
      } else {
        setStatus((current) => ({ ...current, state: "live", label: "Running" }));
      }
    } catch (err) {
      sendInFlightRef.current = false;
      sendFailedRef.current = true;
      setStatus({ state: "error", label: "Schedule failed", detail: formatError(err) });
      setPendingMessage(undefined);
      setActionBusy(false);
    } finally {
      setScheduleBusy(undefined);
    }
  }

  async function handleLoadSchedules(): Promise<void> {
    if (!selectedSessionId || selectedScheduleState.state === "loading") return;
    const sessionId = selectedSessionId;
    setScheduleState({ state: "loading", sessionId });
    try {
      const resp = await listSessionSchedules(client, sessionId);
      markSessionSchedules(sessionId, resp);
      setScheduleState({ state: "ready", sessionId, schedules: resp.schedules });
    } catch (err) {
      setScheduleState({ state: "error", sessionId, error: formatError(err) });
    }
  }

  async function handleUpdateSchedule(scheduleId: string, enabled: boolean): Promise<void> {
    if (!selectedSessionId || deletingScheduleId || updatingScheduleId) return;
    const sessionId = selectedSessionId;
    setUpdatingScheduleId(scheduleId);
    try {
      const resp = await updateSessionSchedule(client, sessionId, scheduleId, { enabled });
      markSessionSchedules(sessionId, resp);
      setScheduleState({ state: "ready", sessionId, schedules: resp.schedules });
      setStatus({ state: "connected", label: "Ready", detail: enabled ? "Timer resumed" : "Timer paused" });
    } catch (err) {
      setStatus({ state: "error", label: enabled ? "Resume timer failed" : "Pause timer failed", detail: formatError(err) });
    } finally {
      setUpdatingScheduleId(undefined);
    }
  }

  async function handleDeleteSchedule(scheduleId: string): Promise<void> {
    if (!selectedSessionId || deletingScheduleId || updatingScheduleId) return;
    const sessionId = selectedSessionId;
    setDeletingScheduleId(scheduleId);
    try {
      const resp = await deleteSessionSchedule(client, sessionId, scheduleId);
      markSessionScheduleDelete(sessionId, resp);
      setScheduleState((current) => {
        if (current.state === "idle" || current.sessionId !== sessionId) return current;
        const currentSchedules = current.state === "ready" || current.state === "error" ? current.schedules ?? [] : [];
        return { state: "ready", sessionId, schedules: currentSchedules.filter((schedule) => schedule.id !== scheduleId) };
      });
      setStatus({ state: "connected", label: "Ready", detail: resp.cleared ? "Timer deleted" : "Timer already removed" });
    } catch (err) {
      setStatus({ state: "error", label: "Delete timer failed", detail: formatError(err) });
    } finally {
      setDeletingScheduleId(undefined);
    }
  }

  function releaseSettledTurn(nextSession: SessionState, pendingText?: string) {
    if (nextSession.status === "running") return;
    const accepted = pendingText
      ? nextSession.turns.some((turn) => (turn.userText ?? "").trim() === pendingText.trim() || pendingMessageMatchesTurn(pendingMessage, turn.userText))
      : true;
    if (accepted) {
      setPendingMessage(undefined);
      setGuidanceReceipts([]);
    }
    setActionBusy(false);
  }

  function releaseAcceptedPendingTurn(nextSession: SessionState) {
    const pending = pendingMessageRef.current;
    if (!pending) return;
    const accepted = nextSession.turns.some((turn) => pendingMessageMatchesTurn(pending, turn.userText));
    if (!accepted) return;
    setPendingMessage(undefined);
    setGuidanceReceipts([]);
  }

  function markSessionLive(sessionId: string, latestUserMessage: string, baseSession?: SessionSummary) {
    const loadedTopic = conversationTopicFromTurns(session.turns);
    setSessions((current) => {
      let found = false;
      const next = current.map((item) => {
        if (item.id !== sessionId) return item;
        found = true;
        const existingLatest = item.latest_user_message?.trim();
        const stableLoadedTopic = loadedTopic && !isContinuationPrompt(loadedTopic) ? loadedTopic : undefined;
        const topicUserMessage = item.topic_user_message ||
          stableLoadedTopic ||
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
          ...baseSession,
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

  function markSessionLoopProtocol(sessionId: string, loopProtocol: SessionLoopProtocolResponse, goal: string) {
    setSessions((current) => {
      let found = false;
      const next = current.map((item) => {
        if (item.id !== sessionId) return item;
        found = true;
        return {
          ...item,
          durable: true,
          has_loop_protocol: true,
          has_loop_state: !!loopProtocol.state,
          loop_protocol: loopProtocol.summary,
          loop_state: loopProtocol.state,
        };
      });
      if (found) return next;
      return [
        {
          id: sessionId,
          active: true,
          durable: true,
          has_conversation: false,
          has_events: false,
          has_plan: false,
          has_loop_protocol: true,
          loop_protocol: loopProtocol.summary,
          has_loop_state: !!loopProtocol.state,
          loop_state: loopProtocol.state,
          has_artifacts: false,
          has_memory: false,
          has_runtime_skills: false,
          latest_user_message: `Set up loop: ${goal}`,
          topic_user_message: goal,
        },
        ...current,
      ];
    });
  }

  function markSessionLoopProtocolDisabled(sessionId: string, loopProtocol: SessionLoopProtocolDeleteResponse) {
    setSessions((current) => current.map((item) => {
      if (item.id !== sessionId) return item;
      return {
        ...item,
        has_loop_protocol: false,
        loop_protocol: undefined,
        has_loop_state: !!loopProtocol.state,
        loop_state: loopProtocol.state,
      };
    }));
  }

  function markSessionSchedules(sessionId: string, resp: SessionSchedulesResponse) {
    setSessions((current) => current.map((item) => {
      if (item.id !== sessionId) return item;
      const hasSchedules = (resp.summary?.count ?? resp.schedules.length) > 0;
      return {
        ...item,
        durable: true,
        has_schedules: hasSchedules,
        schedules: hasSchedules ? resp.summary : undefined,
      };
    }));
  }

  function markSessionScheduleDelete(sessionId: string, resp: SessionScheduleDeleteResponse) {
    setSessions((current) => current.map((item) => {
      if (item.id !== sessionId) return item;
      const hasSchedules = (resp.summary?.count ?? 0) > 0;
      return {
        ...item,
        has_schedules: hasSchedules,
        schedules: hasSchedules ? resp.summary : undefined,
      };
    }));
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

  async function handleRunCommandRequest(content: string) {
    await handleSend(content);
  }

  async function handleExecutePlanStep(opts: { runRemaining?: boolean } = {}) {
    if (!selectedSessionId || actionBusy || session.status === "running" || pendingMessage) return;
    const summary = planPanelSummary;
    const stepIndex = summary?.current_step_index ?? 0;
    const displayText = stepIndex > 0 ? `Run plan step ${stepIndex}` : "Run plan step";
    const content = "Proceed with the active persisted plan.";
    const maxSteps = Math.max(summary?.total_steps ?? 1, 1) + 2;
    sendInFlightRef.current = true;
    sendFailedRef.current = false;
    setPendingMessage({ text: content, displayText, kind: "task" });
    setActionBusy(true);
    if (opts.runRemaining) {
      setPlanRunState((current) => nextIssuedPlanRunState({
        current,
        sessionId: selectedSessionId,
        summary,
        stepIndex,
        maxSteps,
        planMutationCount,
      }));
    }
    try {
      await sendSessionMessage(client, selectedSessionId, {
        content,
        display_text: displayText,
        mode: "execute_plan",
      });
      sendInFlightRef.current = false;
      markSessionLive(selectedSessionId, displayText);
      const hasOpenStream = streamSessionIdRef.current === selectedSessionId && !streamClosedRef.current;
      if (!hasOpenStream) {
        const reconciled = await loadHistory(selectedSessionId);
        releaseSettledTurn(reconciled.session, displayText);
        setStatus({ state: "disconnected", label: "Disconnected", detail: "chat refreshed" });
      } else {
        setStatus((current) => ({ ...current, state: "live", label: opts.runRemaining ? "Running plan" : "Running" }));
      }
    } catch (err) {
      sendInFlightRef.current = false;
      sendFailedRef.current = true;
      if (opts.runRemaining) setPlanRunState(undefined);
      setStatus({ state: "error", label: "Plan step failed", detail: formatError(err) });
      setPendingMessage(undefined);
      setActionBusy(false);
      throw err;
    }
  }

  function handleStopPlanRun() {
    setPlanRunState(undefined);
    setStatus((current) => ({
      ...current,
      detail: session.status === "running" ? "will stop after this plan step" : "plan run stopped",
    }));
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

  function renderLoopWorkbenchTab() {
    if (!showLoopContext && !showScheduleContext) {
      return <WorkbenchEmpty title="No loop or timers" detail="This chat has no LOOP.md or scheduled follow-ups yet." />;
    }
    return (
      <>
        {showLoopContext ? (
          <SessionLoopPanel
            embedded
            defaultOpen
            summary={selectedSession?.loop_protocol}
            state={selectedLoopState}
            disabling={loopProtocolBusy}
            defaultGoal={selectedSessionTitle ?? selectedSessionId}
            starting={loopProtocolBusy || actionBusy || session.status === "running"}
            onStart={handleStartLoop}
            onDisable={handleDisableLoopProtocol}
            protocol={selectedLoopProtocolState.state === "ready" ? selectedLoopProtocolState.protocol.protocol : undefined}
            events={selectedLoopProtocolState.state === "ready" ? selectedLoopProtocolState.protocol.events : undefined}
            loadingProtocol={selectedLoopProtocolState.state === "loading"}
            protocolError={selectedLoopProtocolState.state === "error" ? selectedLoopProtocolState.error : undefined}
            onLoadProtocol={handleLoadLoopProtocol}
            onUseAsDraft={handleUseLoopProtocolDraft}
          />
        ) : (
          <WorkbenchEmpty title="No LOOP.md" detail="Timers can appear here, but this chat does not have a loop protocol file yet." />
        )}
        {showScheduleContext ? (
          <SessionSchedulePanel
            embedded
            defaultOpen
            summary={selectedSession?.schedules}
            schedules={selectedScheduleState.state === "ready" || selectedScheduleState.state === "error" ? selectedScheduleState.schedules : undefined}
            busy={scheduleBusy}
            disabled={actionBusy || session.status === "running"}
            loading={selectedScheduleState.state === "loading"}
            error={selectedScheduleState.state === "error" ? selectedScheduleState.error : undefined}
            deletingId={deletingScheduleId}
            updatingId={updatingScheduleId}
            loopStatus={selectedLoopState?.status ?? selectedSession?.loop_protocol?.status}
            onLoadSchedules={handleLoadSchedules}
            onUpdateSchedule={handleUpdateSchedule}
            onDeleteSchedule={handleDeleteSchedule}
          />
        ) : (
          <WorkbenchEmpty title="No timers" detail="No scheduled loop ticks or check-ins are attached to this chat." />
        )}
      </>
    );
  }

  function renderAccountSettingsPanel(defaultOpen = false) {
    return (
      <AccountSettingsPanel
        settings={accountSettingsState.state === "ready" ? accountSettingsState.settings : accountSettingsState.state === "error" ? accountSettingsState.settings : undefined}
        loading={accountSettingsState.state === "loading"}
        error={accountSettingsState.state === "error" ? accountSettingsState.error : undefined}
        busy={accountSettingsBusy}
        defaultOpen={defaultOpen}
        surface={defaultOpen}
        onRefresh={handleRefreshAccountSettings}
        onSetEnv={handleSetAccountEnv}
        onDeleteEnv={handleDeleteAccountEnv}
        onEnsureSSHKey={handleEnsureAccountSSHKey}
        onUseAsDraft={handleUseAsDraft}
      />
    );
  }

  function renderMemoryPanel(defaultOpen = false) {
    return (
      <SessionMemoryPanel
        memory={memoryStateSnapshot(memoryState)}
        latestUpdate={latestMemoryUpdate}
        loading={memoryState.state === "loading"}
        error={memoryState.state === "error" ? memoryState.error : undefined}
        noSession={memoryState.state === "empty"}
        defaultOpen={defaultOpen}
        surface={defaultOpen}
        onRefresh={handleRefreshMemory}
        onAddMemory={handleAddMemory}
        onRemoveMemory={handleRemoveMemory}
        onReplaceMemory={handleReplaceMemory}
        onUseAsDraft={handleUseAsDraft}
      />
    );
  }

  function renderSkillsPanel(defaultOpen = false) {
    return (
      <SessionSkillsPanel
        skills={skillsState.state === "ready" ? skillsState.skills : undefined}
        loading={skillsState.state === "loading"}
        error={skillsState.state === "error" ? skillsState.error : undefined}
        defaultOpen={defaultOpen}
        surface={defaultOpen}
        installEnabled={skillsState.state === "ready" ? skillsState.installEnabled : false}
        onRefresh={handleRefreshSkills}
        onReadSkill={handleReadSkill}
        onInstallSkill={handleInstallSkill}
        onDeleteSkill={handleDeleteSkill}
        onUseAsDraft={handleUseAsDraft}
      />
    );
  }

  const workbenchNavItems = buildWorkbenchNavItems({
    overview,
    changes: sessionChanges,
    run: sessionRun,
    artifacts: sessionArtifacts,
    files: sessionFiles,
    workspace: sessionWorkspace,
    trace: sessionTrace,
    usage: workbenchContextUsage,
    automation: automationContext,
    attention: workbenchAttention,
    runtimeState: runtimeStatsState,
    configState: accountSettingsState,
    memoryState,
    skillsState,
    latestMemoryUpdate,
  });
  useEffect(() => {
    if (!workbenchNavItems.some((item) => item.key === workbenchTab)) setWorkbenchTab("context");
  }, [workbenchNavItems, workbenchTab]);

  useEffect(() => {
    if (!workbenchOpen || workbenchTab !== "loop") return;
    if (!showLoopContext || !selectedSession?.has_loop_protocol) return;
    if (selectedLoopProtocolState.state !== "idle") return;
    void handleLoadLoopProtocol();
  }, [selectedLoopProtocolState.state, selectedSession?.has_loop_protocol, showLoopContext, workbenchOpen, workbenchTab]);

  function openWorkbench(tab: WorkbenchTab = "context") {
    setWorkbenchTab(tab);
    setSessionsExpandedInWorkbench(false);
    setWorkbenchOpen(true);
  }

  function handleSelectWorkbenchTab(tab: WorkbenchTab) {
    setWorkbenchTab(tab);
  }

  function handleShowSessions() {
    setSessionsExpandedInWorkbench(true);
    setSessionsCollapsed(false);
  }

  function handleHideSessions() {
    setSessionsCollapsed(true);
    if (workbenchOpen) setSessionsExpandedInWorkbench(false);
  }

  function handleSessionResizeStart(event: ReactPointerEvent<HTMLSpanElement>) {
    event.preventDefault();
    const shellLeft = workspaceShellRef.current?.getBoundingClientRect().left ?? 0;
    const handleMove = (moveEvent: PointerEvent) => {
      setSessionPanelWidth(clamp(moveEvent.clientX - shellLeft, minSessionPanelWidth, maxSessionPanelWidth));
    };
    trackResize(event, handleMove);
  }

  function handleWorkbenchResizeStart(event: ReactPointerEvent<HTMLSpanElement>) {
    event.preventDefault();
    const handleMove = (moveEvent: PointerEvent) => {
      const maxWidth = Math.min(maxWorkbenchPanelWidth, Math.max(minWorkbenchPanelWidth, window.innerWidth * 0.64));
      setWorkbenchPanelWidth(clamp(window.innerWidth - moveEvent.clientX - 22, minWorkbenchPanelWidth, maxWidth));
    };
    trackResize(event, handleMove);
  }

  function renderWorkbenchTab() {
    if (workbenchTab === "context") {
      return (
        <>
          <WorkbenchContextPanel
            overview={overview}
            hasSelectedSession={!!selectedSessionId}
            attention={workbenchAttention?.target === "context" ? workbenchAttention : undefined}
            workspace={sessionWorkspace}
            files={sessionFiles}
            changes={sessionChanges}
            artifacts={sessionArtifacts}
            run={sessionRun}
            session={session}
            usage={workbenchContextUsage}
            contextSummary={conversationContext}
            automationTitle={automationContext?.title}
            automationDetail={automationContext?.detail}
            onSelectSection={handleSelectWorkbenchTab}
            defaultOpen
          />
        </>
      );
    }
    if (workbenchTab === "changes") {
      return (
        <SessionChangesPanel
          changes={sessionChanges}
          defaultOpen
          onOpenArtifact={(path) => void handleOpenArtifact(path)}
          onUseAsDraft={handleUseAsDraft}
        />
      );
    }
    if (workbenchTab === "run") {
      return (
        <SessionRunPanel
          run={sessionRun}
          defaultOpen
          onOpenArtifact={(path) => void handleOpenArtifact(path)}
          onRunCommand={handleRunCommandRequest}
          runCommandBusy={actionBusy}
          onUseAsDraft={handleUseAsDraft}
        />
      );
    }
    if (workbenchTab === "artifacts") {
      return (
        <SessionArtifactsPanel
          artifacts={sessionArtifacts}
          defaultOpen
          onOpenArtifact={(path) => void handleOpenArtifact(path)}
          downloadHref={
            selectedSessionId
              ? (path) => client.url(sessionArtifactPath(selectedSessionId, path))
              : undefined
          }
          onUseAsDraft={handleUseAsDraft}
        />
      );
    }
    if (workbenchTab === "files") {
      return (
        <SessionFilesPanel
          files={sessionFiles}
          defaultOpen
          onOpenArtifact={(path) => void handleOpenArtifact(path)}
          onUseAsDraft={handleUseAsDraft}
        />
      );
    }
    if (workbenchTab === "workspace") {
      return sessionWorkspace.hasData ? (
        <SessionWorkspacePanel workspace={sessionWorkspace} defaultOpen onUseAsDraft={handleUseAsDraft} />
      ) : (
        <WorkbenchEmpty title="No workspace evidence" detail="Open or start a chat with workspace-bound file or command activity." />
      );
    }
    if (workbenchTab === "loop" || workbenchTab === "automation") {
      return renderLoopWorkbenchTab();
    }
    if (workbenchTab === "memory") return renderMemoryPanel(true);
    if (workbenchTab === "skills") return renderSkillsPanel(true);
    if (workbenchTab === "config") return renderAccountSettingsPanel(true);
    return (
      <SessionTracePanel
        trace={sessionTrace}
        events={session.events}
        defaultOpen
        onOpenArtifact={(path) => void handleOpenArtifact(path)}
      />
    );
  }

  return (
    <div
      className="app"
      data-theme={theme}
      data-workbench={workbenchOpen ? "open" : "closed"}
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
      <div className="app-topbar" ref={topbarRef}>
        <header className="app-header">
          <h1>Affent</h1>
          <span className="connection-pill" data-state={status.state} data-testid="connection-pill" title={status.detail ?? status.label}>
            {connectionLabel}
          </span>
          <WorkspaceStatusPill workspace={sessionWorkspace} onOpen={() => openWorkbench("workspace")} />
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
          <button
            type="button"
            className="workbench-trigger"
            aria-label="Workbench"
            aria-expanded={workbenchOpen}
            title={workbenchAttention ? workbenchAttention.detail : undefined}
            onClick={() => {
              if (workbenchOpen) {
                setWorkbenchOpen(false);
              } else {
                openWorkbench(workbenchAttention ? workbenchTabFromAttention(workbenchAttention.target) : workbenchTab);
              }
            }}
          >
            <span className="workbench-icon" aria-hidden="true">
              <span />
              <span />
              <span />
            </span>
            <span className="workbench-label">Workbench</span>
            {workbenchAttention ? (
              <span className="workbench-attention" data-tone={workbenchAttention.tone === "error" ? "error" : undefined} aria-hidden="true" />
            ) : null}
          </button>
          {showHeaderNewChat ? (
            <button type="button" className="header-new-chat" disabled={actionBusy} onClick={() => void handleNewSession()}>
              New chat
            </button>
          ) : null}
        </header>
      </div>
      <main className="app-main">
        <div
          ref={workspaceShellRef}
          className="workspace-shell"
          data-compact-nav={compactNav}
          data-session-nav={sessionNavState}
          data-workbench={workbenchOpen ? "open" : "closed"}
          data-testid="workspace-shell"
          style={{
            "--session-panel-width": `${sessionPanelWidth}px`,
            ...(workbenchPanelWidth ? { "--workbench-panel-width": `${workbenchPanelWidth}px` } : {}),
          } as CSSProperties}
        >
          {showSessionRailToggle ? (
            <button
              type="button"
              className="session-rail-toggle"
              aria-label="Show chats"
              onClick={handleShowSessions}
            >
              <span aria-hidden="true">›</span>
            </button>
          ) : null}
          {showSessionNav && !sessionsCollapsed ? (
            <SessionList
              sessions={sessions}
              selectedId={selectedSessionId}
              currentSession={session}
              pendingTask={pendingMessage?.kind === "task" ? pendingMessageDisplay(pendingMessage) : undefined}
              demoActive={demoActive}
              onSelect={(nextSessionId) => resetSessionSurface(nextSessionId, { preserveSession: true })}
              onNew={() => void handleNewSession()}
              onDelete={(sessionId) => void handleDeleteSession(sessionId)}
              deletingId={deletingSessionId}
              onCollapse={handleHideSessions}
              onResizeStart={handleSessionResizeStart}
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
                {showChatContext ? <ChatContextBar overview={overview} usage={workbenchContextUsage} contextSummary={conversationContext} /> : null}
                <SessionPlanPanel
                  summary={planPanelSummary}
                  plan={planPanelPlan}
                  loading={planPanelLoading}
                  error={planPanelError}
                  executeBusy={actionBusy || session.status === "running" || !!pendingMessage}
                  runRemainingActive={planRunActive}
                  onExecuteCurrentStep={() => handleExecutePlanStep()}
                  onRunRemaining={() => handleExecutePlanStep({ runRemaining: true })}
                  onStopRunRemaining={handleStopPlanRun}
                />
              </div>
            ) : null}
            <div className="conversation-scroll" ref={conversationScrollRef} data-testid="conversation-scroll">
              {!workbenchOpen ? (
                <ArtifactViewer
                  artifact={artifact}
                  onClose={() => setArtifact({ state: "idle" })}
                  onSearch={handleArtifactSearch}
                  onLoadMore={() => void handleLoadMoreArtifact()}
                  onUseAsDraft={handleUseAsDraft}
                  artifactDownloadHref={
                    selectedSessionId && artifact.state === "ready"
                      ? client.url(sessionArtifactPath(selectedSessionId, artifact.chunk.path))
                      : undefined
                  }
                />
              ) : null}
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
              onStartLoop={handleStartLoop}
              onScheduleLoopTick={() => handleCreateSchedule("loop")}
              onScheduleCheckIn={() => handleCreateSchedule("checkin")}
              onScheduleDaily={() => handleCreateSchedule("daily")}
              automationAvailable={showAutomationContext}
              automationBusy={scheduleBusy}
              onCancel={handleCancel}
            />
          </section>
          {workbenchOpen ? (
            <WorkbenchPanel
              title="Workbench"
              subtitle="Global runtime console"
              attachment={workbenchAttachment}
              navItems={workbenchNavItems}
              activeTab={workbenchTab}
              onSelectTab={handleSelectWorkbenchTab}
              onResizeStart={handleWorkbenchResizeStart}
              onClose={() => {
                setWorkbenchOpen(false);
              }}
            >
              {renderWorkbenchTab()}
              <ArtifactViewer
                artifact={artifact}
                onClose={() => setArtifact({ state: "idle" })}
                onSearch={handleArtifactSearch}
                onLoadMore={() => void handleLoadMoreArtifact()}
                onUseAsDraft={handleUseAsDraft}
                artifactDownloadHref={
                  selectedSessionId && artifact.state === "ready"
                    ? client.url(sessionArtifactPath(selectedSessionId, artifact.chunk.path))
                    : undefined
                }
              />
            </WorkbenchPanel>
          ) : null}
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
  return "light";
}

function sessionIdFromCurrentUrl(): string | undefined {
  if (typeof window === "undefined") return undefined;
  try {
    const params = new URLSearchParams(window.location.search);
    const sessionId = params.get(sessionUrlParam)?.trim() || params.get(legacySessionUrlParam)?.trim();
    return sessionId || undefined;
  } catch {
    return undefined;
  }
}

function selectedSessionDisplayTitle(selectedSession: SessionSummary | undefined, session: SessionState): string | undefined {
  const row = selectedSession ? buildSessionRows([selectedSession])[0] : undefined;
  const topic = conversationTopicFromTurns(session.turns);
  const topicTitle = topic ? summarizeSessionTitle(topic) : undefined;
  if (topicTitle && (!row || row.titleSource !== "topic" || isWeakSelectedSessionTitle(row.title))) return topicTitle;
  return row?.title ?? topicTitle;
}

function isWeakSelectedSessionTitle(title: string | undefined): boolean {
  if (!title) return true;
  const value = title.trim();
  return isGenericChatTitle(value) || /^new live chat$/i.test(value) || /^\d+$/.test(value);
}

function syncSessionIdToUrl(sessionId?: string) {
  if (typeof window === "undefined") return;
  try {
    const url = new URL(window.location.href);
    const current = url.searchParams.get(sessionUrlParam) || url.searchParams.get(legacySessionUrlParam) || undefined;
    if (sessionId) {
      if (current === sessionId && !url.searchParams.has(legacySessionUrlParam)) return;
      url.searchParams.set(sessionUrlParam, sessionId);
      url.searchParams.delete(legacySessionUrlParam);
    } else {
      if (!current && !url.searchParams.has(sessionUrlParam) && !url.searchParams.has(legacySessionUrlParam)) return;
      url.searchParams.delete(sessionUrlParam);
      url.searchParams.delete(legacySessionUrlParam);
    }
    window.history.replaceState(window.history.state, "", `${url.pathname}${url.search}${url.hash}`);
  } catch {
    // URL sync is convenience only; loading and sending should keep working.
  }
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, Math.round(value)));
}

function trackResize(event: ReactPointerEvent<HTMLElement>, onMove: (event: PointerEvent) => void) {
  const target = event.currentTarget;
  target.setPointerCapture?.(event.pointerId);
  document.body.dataset.resizing = "true";
  const handleMove = (moveEvent: PointerEvent) => onMove(moveEvent);
  const handleEnd = () => {
    document.removeEventListener("pointermove", handleMove);
    document.removeEventListener("pointerup", handleEnd);
    document.removeEventListener("pointercancel", handleEnd);
    delete document.body.dataset.resizing;
  };
  document.addEventListener("pointermove", handleMove);
  document.addEventListener("pointerup", handleEnd, { once: true });
  document.addEventListener("pointercancel", handleEnd, { once: true });
}

function latestChatMeta(updated: string): string | undefined {
  return updated && updated !== "No messages yet" ? updated : undefined;
}

const loopStartMarker = "Start a long-running loop for this goal:";

function loopStartGoalFromComposer(content: string): string | undefined {
  const text = content.trim();
  const markerAt = text.toLowerCase().indexOf(loopStartMarker.toLowerCase());
  if (markerAt < 0) return undefined;
  const beforeMarker = text.slice(0, markerAt).trim();
  const afterMarker = text.slice(markerAt + loopStartMarker.length).trim();
  const goalField = loopTemplateField(afterMarker, "Goal:", [
    "Success criteria:",
    "What to keep improving or checking:",
    "When to pause and ask me:",
  ]);
  return compactLoopGoal(goalField || inlineLoopGoal(afterMarker) || beforeMarker);
}

function loopTemplateField(text: string, label: string, nextLabels: string[]): string {
  const lower = text.toLowerCase();
  const start = lower.indexOf(label.toLowerCase());
  if (start < 0) return "";
  const valueStart = start + label.length;
  let valueEnd = text.length;
  for (const next of nextLabels) {
    const nextAt = lower.indexOf(next.toLowerCase(), valueStart);
    if (nextAt >= 0 && nextAt < valueEnd) valueEnd = nextAt;
  }
  return text.slice(valueStart, valueEnd).trim();
}

function inlineLoopGoal(text: string): string {
  const firstLine = text.split(/\r\n|\r|\n/, 1)[0]?.trim() ?? "";
  if (!firstLine || firstLine.endsWith(":")) return "";
  return firstLine;
}

function compactLoopGoal(goal: string): string | undefined {
  const compacted = goal.replace(/\s+/g, " ").trim();
  if (!compacted) return undefined;
  return compacted.length > 500 ? compacted.slice(0, 497).trimEnd() + "..." : compacted;
}

function webLoopProtocolDraftPrompt(
  goal: string,
  status?: string,
  calibrationQuestions = 0,
  calibrationQuestion?: string,
  calibrationAnswers = 0,
  calibrationPreview?: string,
): string {
  const normalizedStatus = status?.trim().toLowerCase();
  if (normalizedStatus === "draft" && calibrationQuestions > 0 && calibrationAnswers === 0 && calibrationQuestion?.trim()) {
    return [
      `Loop calibration answer for: ${goal}`,
      "",
      `Pending question: ${calibrationQuestion.trim()}`,
      "",
      "My answer: ",
    ].join("\n");
  }
  const lines = [
    `Review and update LOOP.md for: ${goal}`,
    "",
    "Read the current LOOP.md with loop_protocol action=read.",
  ];
  if (normalizedStatus === "draft" && calibrationAnswers > 0) {
    lines.push(
      "A calibration answer is already recorded for this draft. Do not ask the same calibration question again unless a critical activation field is still missing.",
    );
    if (calibrationPreview?.trim()) {
      lines.push(`Recorded calibration preview: ${calibrationPreview.trim()}`);
    }
    lines.push(
      "Use the answer to supplement Current Situation, stop conditions, durable rules, self-attack checks, and recovery anchors.",
      "Keep Current Situation at or below 1200 characters; summarize instead of copying task history.",
      "If activation is now safe, set metadata status: running and call loop_protocol action=complete_activation with the full LOOP.md.",
      "If activation is still unsafe, use loop_protocol action=update_draft and ask exactly one focused missing-field question.",
    );
    return lines.join("\n");
  }
  lines.push(
    "Ask one concise calibration question before changing the protocol unless the user's requested change is already explicit; if more is missing, ask one focused follow-up in a later turn.",
    "Use loop_protocol action=update_draft for draft protocols, or complete_activation only after the user answers and the protocol is fully supplemented with Current Situation at or below 1200 characters.",
    "For a running protocol, update only durable rules, current situation, recovery anchors, or stop conditions that materially improve long-run behavior.",
  );
  return lines.join("\n");
}

function webScheduledCheckInPrompt(sessionTitle: string): string {
  return [
    `Scheduled check-in for session: ${sessionTitle}`,
    "",
    "Before continuing work, ask the user one concise question to confirm current intent, constraints, and whether LOOP.md should change.",
    "If LOOP.md exists, read it with loop_protocol action=read before proposing protocol changes.",
    "Update LOOP.md only after the user answers or when the active loop protocol already gives explicit authority.",
    "If updating Current Situation, keep it at or below 1200 characters and summarize instead of copying task history.",
    "Keep concrete task steps in plan state rather than duplicating a todo list into LOOP.md.",
  ].join("\n");
}

function webScheduleCalibrationGoal(kind: "loop" | "checkin" | "daily", sessionTitle: string): string {
  if (kind === "loop") return `Recurring loop timer for ${sessionTitle}`;
  if (kind === "daily") return `Daily scheduled check-in for ${sessionTitle}`;
  return `Scheduled check-in for ${sessionTitle}`;
}

function webScheduleCalibrationSummary(kind: "loop" | "checkin" | "daily", sessionTitle: string): string {
  if (kind === "loop") return `Calibrate loop timer: ${sessionTitle}`;
  if (kind === "daily") return `Calibrate daily timer: ${sessionTitle}`;
  return `Calibrate check-in timer: ${sessionTitle}`;
}

function webScheduleDisplayText(kind: "loop" | "checkin" | "daily", sessionTitle: string): string {
  if (kind === "loop") return `Loop every 30m: ${sessionTitle}`;
  if (kind === "daily") return `Daily check-in: ${sessionTitle}`;
  return `Check in 1h: ${sessionTitle}`;
}

function webScheduleCalibrationPrompt(kind: "loop" | "checkin" | "daily", sessionTitle: string): string {
  const label = kind === "loop" ? "recurring loop tick" : kind === "daily" ? "daily check-in" : "scheduled check-in";
  return [
    `Calibrate ${label} for session: ${sessionTitle}`,
    "",
    "The timer has been created, but calibration is still required before relying on it.",
    "Read LOOP.md with loop_protocol action=read; if it is draft, disabled, or underspecified, keep it draft.",
    "Ask the user one concise question now to clarify timer purpose, stop conditions, memory expectations, and what should change in LOOP.md.",
    "If the answer leaves another critical field missing, ask one focused follow-up in a later turn and keep LOOP.md as draft.",
    "Do not complete activation in this same turn unless the user is already answering an earlier calibration question.",
    "Do not run the scheduled work yet, and do not claim the timer is operationally calibrated until the user answers.",
    "After the user answers, update LOOP.md only for durable timer policy, Current Situation at or below 1200 characters, recovery anchors, or stop conditions; keep concrete task steps in plan state.",
  ].join("\n");
}

function webScheduledLoopTickPrompt(sessionTitle: string): string {
  return [
    `Scheduled loop tick for session: ${sessionTitle}`,
    "",
    "This is an autonomous long-run tick, not a new human instruction.",
    "Read LOOP.md with loop_protocol action=read before continuing.",
    "If LOOP.md is missing, draft, disabled, underspecified, or the user's intent is unclear, ask one concise calibration question and do not continue autonomous work.",
    "If LOOP.md is running, inspect only the minimum needed plan state, memory, recent trace, or artifacts, then advance at most one compact high-value step.",
    "Prefer evidence-backed progress over broad exploration; update plan state for task progress and update LOOP.md only for durable rules, Current Situation at or below 1200 characters, recovery anchors, or stop conditions.",
    "End with a concise status, next trigger expectation, and any blocker that should pause future loop ticks.",
  ].join("\n");
}

function toRfc3339Seconds(value: Date): string {
  return new Date(Math.ceil(value.getTime() / 1000) * 1000).toISOString().replace(/\.\d{3}Z$/, "Z");
}

function pendingMessageDisplay(message?: PendingMessageView): string | undefined {
  if (!message) return undefined;
  return message.displayText?.trim() || message.text;
}

function pendingMessageMatchesTurn(message: PendingMessageView | undefined, turnText?: string): boolean {
  const text = turnText?.trim();
  if (!message || !text) return false;
  return text === message.text.trim() || text === pendingMessageDisplay(message);
}

function isPlanMutationAction(value: unknown): boolean {
  const action = typeof value === "string" ? value.trim().toLowerCase() : "";
  return action === "set" || action === "update" || action === "clear";
}

function lastRawEventId(events: readonly RawEvent[]): number {
  const last = events[events.length - 1];
  return typeof last?.id === "number" ? last.id : -1;
}

function ChatContextBar({
  overview,
  usage,
  contextSummary,
}: {
  overview: SessionOverview;
  usage?: WorkbenchContextUsageView;
  contextSummary?: SessionContextSummary;
}) {
  const context = chatContextDisplay(overview);
  const contextLabel = chatContextLabel({ overview, ...context });
  const usageSummary = workbenchContextUsageSummary(usage);
  const contextPercent = contextSummary && contextSummary.compact_trigger > 0 ? Math.max(0, Math.round(contextSummary.compact_percent)) : undefined;
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
      {(usageSummary || contextPercent != null) ? (
        <span className="chat-context-stats" aria-label="Chat status statistics">
          {usageSummary ? <span className="chat-context-token-stat">{usageSummary}</span> : null}
          {contextPercent != null ? <ContextRing percent={contextPercent} /> : null}
        </span>
      ) : null}
    </div>
  );
}

function ContextRing({ percent }: { percent: number }) {
  const clamped = Math.max(0, Math.min(100, percent));
  const radius = 12;
  const circumference = 2 * Math.PI * radius;
  const dash = (clamped / 100) * circumference;
  return (
    <span className="chat-context-ring" aria-label={`Context used ${clamped}%`} title={`Context used ${clamped}%`}>
      <svg viewBox="0 0 32 32" aria-hidden="true">
        <circle className="chat-context-ring-track" cx="16" cy="16" r={radius} />
        <circle
          className="chat-context-ring-value"
          cx="16"
          cy="16"
          r={radius}
          strokeDasharray={`${dash} ${circumference - dash}`}
        />
      </svg>
      <b>{clamped}%</b>
    </span>
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
    if (err.type === "invalid_api_response") return err.message;
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

function mergeSessionIndex(serverSessions: readonly SessionSummary[], currentSessions: readonly SessionSummary[]): SessionSummary[] {
  const serverIds = new Set(serverSessions.map((session) => session.id));
  const optimistic = currentSessions.filter((session) => !serverIds.has(session.id) && shouldPreserveOptimisticSession(session));
  return [...optimistic, ...serverSessions];
}

function shouldPreserveOptimisticSession(session: SessionSummary): boolean {
  return sessionHasVisibleState(session);
}

function sessionHasVisibleState(session: SessionSummary): boolean {
  return Boolean(
    session.active ||
    session.has_conversation ||
    session.has_events ||
    session.has_plan ||
    session.has_loop_protocol ||
    session.has_loop_state ||
    session.has_schedules ||
    session.has_artifacts ||
    session.has_memory ||
    session.has_runtime_skills ||
    session.latest_user_message?.trim() ||
    session.topic_user_message?.trim(),
  );
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
