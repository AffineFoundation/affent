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
  readSessionFile,
  sessionArtifactPath,
  readSkill,
  removeSessionMemory,
  replaceSessionMemory,
  runSessionCommand,
  sendSessionMessage,
  streamSessionEvents,
  updateSessionSchedule,
  type SessionScheduleDeleteResponse,
  type SessionSchedule,
  type SessionSchedulesResponse,
  type SessionLoopProtocolDeleteResponse,
  type SessionMemoryResponse,
  type SessionMemoryAddRequest,
  type SessionMemoryRemoveRequest,
  type SessionMemoryReplaceRequest,
  type SessionPlanSummary,
  type SessionContextSummary,
  type SessionCommandRequest,
  type SessionSkillInfo,
  type SessionSkillInstallRequest,
  type SessionSummary,
} from "./api/sessions";
import { getServerStats, type ServerStatsResponse } from "./api/stats";
import {
  checkAccountGitAccess,
  deleteAccountEnv,
  ensureAccountSSHKey,
  getAccountSettings,
  setAccountEnv,
  type AccountGitCheckRequest,
  type AccountGitCheckResponse,
  type AccountSettingsResponse,
} from "./api/settings";
import { ArtifactViewer, type ArtifactViewerState } from "./components/ArtifactViewer";
import { EventType, type RawEvent } from "./api/events";
import { Composer, type ComposerDraft } from "./components/Composer";
import { SessionList } from "./components/SessionList";
import { SessionMemoryPanel } from "./components/SessionMemoryPanel";
import { SessionPlanPanel } from "./components/SessionPlanPanel";
import { SessionAutomationPanel, type SessionAutomationFocus, type SessionAutomationMetric, type SessionAutomationQueueItem } from "./components/SessionAutomationPanel";
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
import { buildSessionRun, manualRunDraft } from "./view/sessionRun";
import { buildWorkbenchArtifacts } from "./view/sessionArtifacts";
import { buildSessionWorkspace, latestRuntimeWorkspace } from "./view/sessionWorkspace";
import { buildWorkspaceFileView, type WorkspaceFileBrowserState } from "./view/workspaceFile";
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
import { buildSessionMemoryCandidates } from "./view/sessionMemory";

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
  const [runCommandBusy, setRunCommandBusy] = useState(false);
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
  const [workspaceFileBrowser, setWorkspaceFileBrowser] = useState<WorkspaceFileBrowserState>({ state: "idle" });
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
  const selectedSessionLoading = !demoActive && !!selectedSessionId && !selectedSession && (!sessionIndexReady || status.state === "loading");
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
  const workbenchArtifacts = useMemo(() => buildWorkbenchArtifacts(session), [session]);
  const runtimeWorkspace = useMemo(() => latestRuntimeWorkspace(session), [session]);
  const sessionWorkspace = useMemo(() => buildSessionWorkspace(selectedSession, sessionRun, runtimeWorkspace), [runtimeWorkspace, selectedSession, sessionRun]);
  const workbenchContextUsage = useMemo(() => buildWorkbenchContextUsage(session, selectedSession), [session, selectedSession]);
  const conversationContext = useMemo(() => buildConversationContextView(session, selectedSession?.context), [selectedSession?.context, session]);
  useEffect(() => {
    setWorkspaceFileBrowser({ state: "idle", workspacePath: sessionWorkspace.path });
  }, [selectedSessionId, sessionWorkspace.path]);
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
      setStatus({ state: "connected", label: "Ready", detail: "timer saved" });
    } catch (err) {
      setStatus({ state: "error", label: "Schedule failed", detail: formatError(err) });
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

  async function handleEditUserMessage(turnId: string, content: string) {
    const targetSessionId = selectedSessionIdRef.current;
    const trimmed = content.trim();
    if (!targetSessionId || !trimmed || actionBusy || session.status === "running" || pendingMessage) return;
    sendInFlightRef.current = true;
    sendFailedRef.current = false;
    streamClosedRef.current = false;
    streamSessionIdRef.current = undefined;
    setPendingMessage({ text: trimmed, kind: "task" });
    setGuidanceReceipts([]);
    setActionBusy(true);
    setStatus({ state: "loading", label: "Editing message", detail: "Rebuilding chat from the edited message" });
    try {
      await sendSessionMessage(client, targetSessionId, { content: trimmed, edit_turn_id: turnId });
      sendInFlightRef.current = false;
      markSessionLive(targetSessionId, trimmed);
      const reconciled = await loadHistory(targetSessionId);
      releaseAcceptedPendingTurn(reconciled.session);
      setLiveConnectTick((current) => current + 1);
      setStatus((current) => ({ ...current, state: "live", label: "Running", detail: "Edited message sent" }));
    } catch (err) {
      sendInFlightRef.current = false;
      sendFailedRef.current = true;
      setPendingMessage(undefined);
      setActionBusy(false);
      setStatus({ state: "error", label: "Edit failed", detail: formatError(err) });
      throw err;
    }
  }

  async function handleRunCommandRequest(request: SessionCommandRequest) {
    if (!selectedSessionId) {
      handleUseAsDraft(manualRunDraft(request.command, request.cwd), "run_command");
      return;
    }
    setRunCommandBusy(true);
    setStatus({ state: "loading", label: "Running command", detail: request.command });
    try {
      const resp = await runSessionCommand(client, selectedSessionId, request);
      await loadHistory(selectedSessionId);
      setStatus({
        state: resp.exit_code === 0 ? "connected" : "error",
        label: resp.exit_code === 0 ? "Command finished" : "Command failed",
        detail: `exit ${resp.exit_code}`,
      });
    } catch (err) {
      setStatus({ state: "error", label: "Command failed", detail: formatError(err) });
    } finally {
      setRunCommandBusy(false);
    }
  }

  async function handleConfigVerifyGitAccess(request: AccountGitCheckRequest): Promise<AccountGitCheckResponse> {
    setStatus({ state: "loading", label: "Checking Git access", detail: request.target });
    try {
      const resp = await checkAccountGitAccess(client, request);
      setStatus({
        state: resp.status === "ok" ? "connected" : "error",
        label: resp.status === "ok" ? "Git access reachable" : "Git access failed",
        detail: [resp.host || resp.target, `exit ${resp.exit_code}`].filter(Boolean).join(" · "),
      });
      return resp;
    } catch (err) {
      setStatus({ state: "error", label: "Git access check failed", detail: formatError(err) });
      throw err;
    }
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

  async function handleOpenWorkspacePath(path: string) {
    const cleanPath = path.trim() || ".";
    if (!selectedSessionId || !sessionWorkspace.path) return;
    const workspacePath = sessionWorkspace.path;
    setWorkspaceFileBrowser({ state: "loading", path: cleanPath, workspacePath });
    try {
      const resp = await readSessionFile(client, selectedSessionId, { path: cleanPath, limit: 64 * 1024 });
      setWorkspaceFileBrowser({ state: "ready", file: buildWorkspaceFileView(resp), workspacePath });
    } catch (err) {
      setWorkspaceFileBrowser({ state: "error", path: cleanPath, error: formatError(err), workspacePath });
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
    if (selectedSessionLoading) {
      return <WorkbenchEmpty title="Loading automation" detail="Reading loop and timer state for this chat." />;
    }
    const automationTitle = automationContext?.title ?? "No automation";
    const automationDetail = automationContext?.detail ?? "Start a loop or schedule a check-in when this chat needs follow-up.";
    const automationMetrics = automationWorkbenchMetrics(selectedSession, selectedLoopState, selectedLoopProtocolState, selectedScheduleState, automationContext);
    const automationFocus = automationWorkbenchFocus(selectedSession, selectedLoopState, selectedLoopProtocolState, selectedScheduleState, automationContext);
    const automationQueue = automationWorkbenchQueue(selectedSession, selectedLoopState, selectedLoopProtocolState, selectedScheduleState);
    const emptyScheduleSummary = { count: 0, enabled: 0 };
    const scheduleSummary = selectedSession?.schedules;
    const loadedSchedules = selectedScheduleState.state === "ready" || selectedScheduleState.state === "error" ? selectedScheduleState.schedules : undefined;
    return (
      <SessionAutomationPanel
        title={automationTitle}
        detail={automationDetail}
        metrics={automationMetrics}
        focus={automationFocus}
        queue={automationQueue}
        actions={automationFocus?.action === "answer" || automationFocus?.action === "review" ? (
          <button type="button" className="ghost-action primary-run-action" onClick={handleUseLoopProtocolDraft}>
            {automationFocus.action === "answer" ? "Answer setup" : "Review in chat"}
          </button>
        ) : undefined}
        defaultOpen
      >
        {showLoopContext ? (
          <SessionLoopPanel
            embedded
            suppressRunningCallout
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
          <SessionLoopPanel
            embedded
            defaultGoal={selectedSessionTitle ?? selectedSessionId}
            starting={loopProtocolBusy || actionBusy || session.status === "running"}
            onStart={handleStartLoop}
          />
        )}
        <SessionSchedulePanel
          embedded
          summary={scheduleSummary ?? emptyScheduleSummary}
          schedules={loadedSchedules}
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
          onScheduleCheckIn={() => handleCreateSchedule("checkin")}
          onScheduleLoopTick={() => handleCreateSchedule("loop")}
          onScheduleDaily={() => handleCreateSchedule("daily")}
        />
      </SessionAutomationPanel>
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
        onVerifyGitAccess={handleConfigVerifyGitAccess}
      />
    );
  }

  function renderMemoryPanel(defaultOpen = false) {
    const memorySnapshot = memoryStateSnapshot(memoryState);
    return (
      <SessionMemoryPanel
        memory={memorySnapshot}
        latestUpdate={latestMemoryUpdate}
        candidates={buildSessionMemoryCandidates({ memory: memorySnapshot, session: selectedSession, changes: sessionChanges, files: sessionFiles })}
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
    artifacts: workbenchArtifacts,
    files: sessionFiles,
    workspaceBrowserActive: workspaceFileBrowser.state !== "idle",
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

  useEffect(() => {
    if (!workbenchOpen || workbenchTab !== "loop") return;
    if (!showScheduleContext) return;
    if (selectedScheduleState.state !== "idle") return;
    const summary = selectedSession?.schedules;
    const hasScheduleEvidence = selectedSession?.has_schedules
      || (summary?.count ?? 0) > 0
      || (summary?.enabled ?? 0) > 0
      || (summary?.error_count ?? 0) > 0
      || !!summary?.last_error;
    if (!hasScheduleEvidence) return;
    void handleLoadSchedules();
  }, [
    selectedScheduleState.state,
    selectedSession?.has_schedules,
    selectedSession?.schedules,
    showScheduleContext,
    workbenchOpen,
    workbenchTab,
  ]);

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
            artifacts={workbenchArtifacts}
            run={sessionRun}
            session={session}
            usage={workbenchContextUsage}
            contextSummary={conversationContext}
            taskState={selectedSession?.task_state}
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
          onOpenWorkspacePath={sessionWorkspace.path ? (path) => {
            setWorkbenchTab("files");
            void handleOpenWorkspacePath(path);
          } : undefined}
          onOpenWorkspacePanel={() => setWorkbenchTab("workspace")}
          onOpenFilesPanel={() => setWorkbenchTab("files")}
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
          runCommandBusy={runCommandBusy}
          onUseAsDraft={handleUseAsDraft}
        />
      );
    }
    if (workbenchTab === "artifacts") {
      return (
        <SessionArtifactsPanel
          artifacts={workbenchArtifacts}
          defaultOpen
          onOpenArtifact={(path) => void handleOpenArtifact(path)}
        />
      );
    }
    if (workbenchTab === "files") {
      return (
        <SessionFilesPanel
          files={sessionFiles}
          workspaceBrowser={workspaceFileBrowser}
          defaultOpen
          onOpenWorkspacePath={sessionWorkspace.path ? (path) => void handleOpenWorkspacePath(path) : undefined}
          onOpenWorkspacePanel={() => setWorkbenchTab("workspace")}
          onOpenArtifact={(path) => void handleOpenArtifact(path)}
          onUseAsDraft={handleUseAsDraft}
        />
      );
    }
    if (workbenchTab === "workspace") {
      return selectedSessionId ? (
        <SessionWorkspacePanel
          workspace={sessionWorkspace}
          defaultOpen
          onOpenWorkspacePath={sessionWorkspace.path ? (path) => {
            setWorkbenchTab("files");
            void handleOpenWorkspacePath(path);
          } : undefined}
          onVerifyWorkspace={handleRunCommandRequest}
          onUseAsDraft={handleUseAsDraft}
        />
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
                onEditUserMessage={handleEditUserMessage}
                savedChatCount={sessions.length}
                latestChat={latestChatShortcut}
                onOpenLatestChat={latestChatShortcut ? () => resetSessionSurface(latestChatShortcut.id, { preserveSession: true }) : undefined}
                initialHistoryFocus={selectedSessionId && !selectedSessionActive ? "answer" : "latest"}
                loading={status.state === "loading"}
                loadingDetail={status.state === "loading" ? status.detail : undefined}
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
    const pathSessionId = sessionIdFromPath(window.location.pathname);
    const params = new URLSearchParams(window.location.search);
    const sessionId = params.get(sessionUrlParam)?.trim() || params.get(legacySessionUrlParam)?.trim();
    return sessionId || pathSessionId || undefined;
  } catch {
    return undefined;
  }
}

function sessionIdFromPath(pathname: string): string | undefined {
  const match = pathname.match(/^\/session\/([^/]+)\/?$/);
  if (!match) return undefined;
  try {
    return decodeURIComponent(match[1]).trim() || undefined;
  } catch {
    return match[1]?.trim() || undefined;
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
    const pathSessionId = sessionIdFromPath(url.pathname);
    const current = url.searchParams.get(sessionUrlParam) || url.searchParams.get(legacySessionUrlParam) || pathSessionId || undefined;
    if (sessionId) {
      const nextPath = `/session/${encodeURIComponent(sessionId)}`;
      if (current === sessionId && url.pathname === nextPath && !url.searchParams.has(sessionUrlParam) && !url.searchParams.has(legacySessionUrlParam)) return;
      url.pathname = nextPath;
      url.searchParams.delete(sessionUrlParam);
      url.searchParams.delete(legacySessionUrlParam);
    } else {
      if (!current && !url.searchParams.has(sessionUrlParam) && !url.searchParams.has(legacySessionUrlParam)) return;
      if (pathSessionId) url.pathname = "/";
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

function automationWorkbenchMetrics(
  session: SessionSummary | undefined,
  loopState: SessionSummary["loop_state"] | undefined,
  loopPanelState: LoopProtocolState,
  schedulePanelState: ScheduleState,
  _context: { title: string; detail: string } | undefined,
): SessionAutomationMetric[] {
  return compactArray([
    automationLoopMetric(session, loopState, loopPanelState),
    automationProtocolMetric(session, loopState, loopPanelState),
    automationTimerMetric(session, schedulePanelState),
    automationNextRunMetric(session, loopState, schedulePanelState),
  ]);
}

function automationWorkbenchFocus(
  session: SessionSummary | undefined,
  loopState: SessionSummary["loop_state"] | undefined,
  loopPanelState: LoopProtocolState,
  schedulePanelState: ScheduleState,
  context: { title: string; detail: string } | undefined,
): SessionAutomationFocus {
  if (loopPanelState.state === "loading") {
    return {
      label: "Checking",
      title: "Loading LOOP.md",
      detail: "Reading the protocol and recent loop events.",
      tone: "neutral",
    };
  }
  if (loopPanelState.state === "error") {
    return {
      label: "Loop error",
      title: "LOOP.md could not be loaded",
      detail: automationCompact(loopPanelState.error) ?? "Refresh the protocol or disable the loop if it is stale.",
      tone: "danger",
    };
  }
  if (schedulePanelState.state === "error") {
    return {
      label: "Timer error",
      title: "Timer details could not be loaded",
      detail: automationCompact(schedulePanelState.error) ?? "Refresh timers before changing scheduled work.",
      tone: "danger",
    };
  }
  const status = automationCompact(loopState?.status ?? session?.loop_protocol?.status)?.toLowerCase();
  const questions = loopState?.calibration_questions ?? session?.loop_protocol?.state?.calibration_questions ?? 0;
  const answers = loopState?.calibration_answers ?? session?.loop_protocol?.state?.calibration_answers ?? 0;
  const lastQuestion = automationCompact(loopState?.last_calibration_question_preview ?? session?.loop_protocol?.state?.last_calibration_question_preview);
  if (status === "draft" && answers <= 0) {
    return {
      label: "Required action",
      title: "Answer setup question",
      detail: lastQuestion ?? (questions > 0 ? "A loop draft exists but cannot run until calibration is answered." : "Wait for Affent to ask the loop calibration question."),
      tone: "attention",
      action: "answer",
    };
  }
  if (status === "draft") {
    return {
      label: "Required action",
      title: "Review activation",
      detail: "Calibration is recorded. Review durable intent and activate through chat before timer ticks run.",
      tone: "attention",
      action: "review",
    };
  }
  const schedules = session?.schedules;
  if ((schedules?.error_count ?? 0) > 0 || schedules?.last_error) {
    return {
      label: "Timer issue",
      title: `${Math.max(schedules?.error_count ?? 0, 1)} timer error`,
      detail: automationCompact(schedules?.last_error) ?? "Load timer details to inspect the failed schedule.",
      tone: "danger",
    };
  }
  if (status === "running") {
    const summary = session?.schedules;
    const visibleSchedules = schedulePanelState.state === "ready" ? schedulePanelState.schedules : [];
    const enabled = Math.max(summary?.enabled ?? 0, visibleSchedules.filter((schedule) => schedule.enabled).length);
    if (enabled <= 0) {
      return {
        label: "Automation active",
        title: "Loop running manually",
        detail: "No timer is scheduled; this loop continues only from chat or a new scheduled trigger.",
        tone: "ok",
      };
    }
    return {
      label: "Automation active",
      title: "Loop can receive timer ticks",
      detail: session?.schedules?.next_run_at
        ? `Next timer ${automationFormatTime(session.schedules.next_run_at)}`
        : automationCompact(loopState?.last_decision ?? loopState?.last_event_summary) ?? "Use chat for durable protocol updates; keep LOOP.md compact.",
      tone: "ok",
    };
  }
  if ((schedules?.enabled ?? 0) > 0) {
    return {
      label: "Timer active",
      title: `${schedules?.enabled} scheduled follow-up${schedules?.enabled === 1 ? "" : "s"}`,
      detail: schedules?.next_run_at ? `Next ${automationFormatTime(schedules.next_run_at)}` : "Load timers to inspect the next run and pause controls.",
      tone: "ok",
    };
  }
  return {
    label: "Manual control",
    title: context?.title ?? "No automation running",
    detail: context?.detail ?? "Start loop setup or create a timer only when this chat needs durable follow-up.",
    tone: "neutral",
  };
}

function automationWorkbenchQueue(
  session: SessionSummary | undefined,
  loopState: SessionSummary["loop_state"] | undefined,
  loopPanelState: LoopProtocolState,
  schedulePanelState: ScheduleState,
): SessionAutomationQueueItem[] {
  const items: SessionAutomationQueueItem[] = [];
  const status = automationCompact(loopState?.status ?? session?.loop_protocol?.status)?.toLowerCase();
  const path = automationCompact(session?.loop_protocol?.path ?? loopState?.protocol_path);
  const questions = loopState?.calibration_questions ?? session?.loop_protocol?.state?.calibration_questions ?? 0;
  const answers = loopState?.calibration_answers ?? session?.loop_protocol?.state?.calibration_answers ?? 0;
  const lastQuestion = automationCompact(loopState?.last_calibration_question_preview ?? session?.loop_protocol?.state?.last_calibration_question_preview);
  const hasLoopSignal = Boolean(status || path || session?.has_loop_protocol || session?.has_loop_state);

  if (loopPanelState.state === "loading") {
    items.push({
      id: "loop-loading",
      label: "Loop",
      title: "Loading LOOP.md",
      detail: "Reading the protocol file and recent loop events.",
      tone: "neutral",
      meta: path,
    });
  } else if (loopPanelState.state === "error") {
    items.push({
      id: "loop-error",
      label: "Loop",
      title: "LOOP.md unavailable",
      detail: automationCompact(loopPanelState.error) ?? "Reload LOOP.md before relying on this loop.",
      tone: "danger",
      meta: path,
    });
  } else if (status === "draft" && answers <= 0) {
    items.push({
      id: "loop-calibration",
      label: "Required",
      title: questions > 0 ? "Answer loop calibration" : "Wait for calibration question",
      detail: lastQuestion ?? "LOOP.md is still a draft and cannot run timer ticks yet.",
      tone: "attention",
      meta: path,
    });
  } else if (status === "draft") {
    items.push({
      id: "loop-review",
      label: "Required",
      title: "Review and activate LOOP.md",
      detail: "A calibration answer is recorded; verify durable intent, stop conditions, and recovery anchors before activation.",
      tone: "attention",
      meta: path,
    });
  } else if (status === "running") {
    // A running loop is already represented by the focus, metrics, and Loop panel.
    // Keep the queue for blocked, pending, failed, or scheduled work.
  } else if (status === "disabled") {
    items.push({
      id: "loop-disabled",
      label: "Loop",
      title: "LOOP.md is disabled",
      detail: "This session will not receive loop protocol context until setup runs again.",
      tone: "danger",
      meta: path,
    });
  } else if (path || session?.has_loop_protocol) {
    items.push({
      id: "loop-check",
      label: "Loop",
      title: "Review LOOP.md status",
      detail: "Protocol metadata exists but the runtime status is not clear yet.",
      tone: "neutral",
      meta: path,
    });
  }

  const scheduleError = automationScheduleErrorItem(session, schedulePanelState);
  if (scheduleError) items.push(scheduleError);

  if (schedulePanelState.state === "loading") {
    items.push({
      id: "timers-loading",
      label: "Timers",
      title: "Loading timer details",
      detail: "Reading saved schedules before pause, resume, or delete controls are shown.",
      tone: "neutral",
    });
  } else if (schedulePanelState.state === "ready" && schedulePanelState.schedules.length > 0) {
    const schedules = [...schedulePanelState.schedules].sort((a, b) => Date.parse(a.next_run_at) - Date.parse(b.next_run_at));
    schedules.slice(0, 4).forEach((schedule) => {
      items.push(automationScheduleQueueItem(schedule));
    });
    if (schedules.length > 4) {
      items.push({
        id: "timers-more",
        label: "Timers",
        title: `${schedules.length - 4} more saved timers`,
        detail: "Use the timer list below for full pause, resume, and delete controls.",
        tone: "neutral",
      });
    }
  } else {
    const summaryItem = automationScheduleSummaryQueueItem(session);
    if (summaryItem) items.push(summaryItem);
  }

  if (items.length === 0 && !hasLoopSignal) {
    items.push({
      id: "automation-off",
      label: "Manual",
      title: "No loop or timer armed",
      detail: "Start setup or schedule a check-in only when this session needs durable follow-up.",
      tone: "neutral",
    });
  }

  return items;
}

function automationScheduleErrorItem(
  session: SessionSummary | undefined,
  panelState: ScheduleState,
): SessionAutomationQueueItem | undefined {
  const summary = session?.schedules;
  if (panelState.state === "error") {
    return {
      id: "timers-error",
      label: "Timers",
      title: "Timer details unavailable",
      detail: automationCompact(panelState.error) ?? "Refresh timers before changing scheduled work.",
      tone: "danger",
    };
  }
  const count = summary?.error_count ?? 0;
  if (count <= 0 && !summary?.last_error) return undefined;
  return {
    id: "timers-last-error",
    label: "Timers",
    title: `${Math.max(count, 1)} timer ${Math.max(count, 1) === 1 ? "error" : "errors"}`,
    detail: automationCompact(summary?.last_error) ?? "Load timer details to inspect the failed schedule.",
    tone: "danger",
  };
}

function automationScheduleSummaryQueueItem(session: SessionSummary | undefined): SessionAutomationQueueItem | undefined {
  const summary = session?.schedules;
  if (!summary) {
    return session?.has_schedules
      ? {
        id: "timers-unloaded",
        label: "Timers",
        title: "Timer details need loading",
        detail: "Load timers to inspect next run, last error, and pause/delete controls.",
        tone: "neutral",
      }
      : undefined;
  }
  if (summary.next_run_at) {
    return {
      id: `timer-next-${summary.next_schedule_id ?? summary.next_run_at}`,
      label: scheduleKindLabel(summary.next_schedule_kind),
      title: `Next run ${automationFormatTime(summary.next_run_at)}`,
      detail: automationCompact(summary.next_prompt_preview) ?? scheduleKindDetail(summary.next_schedule_kind),
      tone: "ok",
    };
  }
  if (summary.enabled > 0) {
    return {
      id: "timers-enabled",
      label: "Timers",
      title: `${summary.enabled} enabled ${summary.enabled === 1 ? "timer" : "timers"}`,
      detail: "Load timer details to inspect the next run and recent outcome.",
      tone: "neutral",
    };
  }
  if (summary.count > 0) {
    return {
      id: "timers-paused",
      label: "Timers",
      title: `${summary.count} paused ${summary.count === 1 ? "timer" : "timers"}`,
      detail: "Resume or delete saved timers from the list below.",
      tone: "neutral",
    };
  }
  return undefined;
}

function automationScheduleQueueItem(schedule: SessionSchedule): SessionAutomationQueueItem {
  const failed = !!automationCompact(schedule.last_error);
  return {
    id: `timer-${schedule.id}`,
    label: scheduleKindLabel(schedule.kind),
    title: schedule.enabled ? `Next ${automationFormatTime(schedule.next_run_at)}` : "Paused",
    detail: automationScheduleQueueDetail(schedule),
    meta: schedule.id,
    tone: failed ? "danger" : schedule.enabled ? "ok" : "neutral",
  };
}

function automationScheduleQueueDetail(schedule: SessionSchedule): string {
  const parts = [
    automationCompact(schedule.display_text) ?? automationCompact(schedule.prompt),
    schedule.repeat_interval_seconds ? `Repeats every ${formatAutomationDuration(schedule.repeat_interval_seconds)}` : "One-time",
    schedule.run_count && schedule.run_count > 0 ? `${schedule.run_count} ${schedule.run_count === 1 ? "run" : "runs"}` : undefined,
    schedule.last_run_at ? `last ${automationFormatTime(schedule.last_run_at)}` : undefined,
    automationCompact(schedule.last_error) ? `error: ${automationCompact(schedule.last_error)}` : undefined,
  ].filter(Boolean);
  return parts.join(" · ");
}

function automationLoopMetric(
  session: SessionSummary | undefined,
  loopState: SessionSummary["loop_state"] | undefined,
  panelState: LoopProtocolState,
): SessionAutomationMetric {
  if (panelState.state === "loading") return { label: "Loop", value: "Loading", detail: "Reading LOOP.md", tone: "neutral" };
  if (panelState.state === "error") return { label: "Loop", value: "Error", detail: automationCompact(panelState.error) ?? "LOOP.md unavailable", tone: "danger" };
  const status = automationCompact(loopState?.status ?? session?.loop_protocol?.status)?.toLowerCase();
  const questions = loopState?.calibration_questions ?? session?.loop_protocol?.state?.calibration_questions ?? 0;
  const answers = loopState?.calibration_answers ?? session?.loop_protocol?.state?.calibration_answers ?? 0;
  const goal = automationCompact(loopState?.initial_goal_preview ?? session?.loop_protocol?.state?.initial_goal_preview);
  if (!status || status === "off") return { label: "Loop", value: "Off", detail: "No LOOP.md for this chat", tone: "neutral" };
  if (status === "draft") {
    if (answers > 0) return { label: "Loop", value: "Review", detail: "Calibration recorded; activate from chat", tone: "attention" };
    if (questions > 0) return { label: "Loop", value: "Draft", detail: "Answer setup question", tone: "attention" };
    return { label: "Loop", value: "Draft", detail: "Waiting for calibration question", tone: "attention" };
  }
  if (status === "running") return { label: "Loop", value: "Running", detail: goal ?? "LOOP.md active", tone: "ok" };
  if (status === "disabled") return { label: "Loop", value: "Disabled", detail: "LOOP.md will not feed future turns", tone: "danger" };
  return { label: "Loop", value: automationStatusLabel(status), detail: "Review LOOP.md status", tone: "neutral" };
}

function automationProtocolMetric(
  session: SessionSummary | undefined,
  loopState: SessionSummary["loop_state"] | undefined,
  panelState: LoopProtocolState,
): SessionAutomationMetric | undefined {
  if (panelState.state === "loading") return { label: "Protocol", value: "Loading", detail: "Reading LOOP.md and events", tone: "neutral" };
  if (panelState.state === "error") return { label: "Protocol", value: "Error", detail: automationCompact(panelState.error) ?? "LOOP.md unavailable", tone: "danger" };
  const path = automationCompact(session?.loop_protocol?.path ?? loopState?.protocol_path);
  const status = automationCompact(loopState?.status ?? session?.loop_protocol?.status)?.toLowerCase();
  if (!path && !loopState && !session?.has_loop_protocol) return undefined;
  const feeds = loopState?.protocol_feeds ?? session?.loop_protocol?.state?.protocol_feeds ?? 0;
  const updates = loopState?.protocol_updates ?? session?.loop_protocol?.state?.protocol_updates ?? 0;
  const decisions = loopState?.loop_decisions ?? session?.loop_protocol?.state?.loop_decisions ?? 0;
  const events = loopState?.event_count ?? session?.loop_protocol?.state?.event_count ?? 0;
  const detail = [
    feeds > 0 ? `${feeds} ${feeds === 1 ? "feed" : "feeds"}` : undefined,
    updates > 0 ? `${updates} ${updates === 1 ? "update" : "updates"}` : undefined,
    decisions > 0 ? `${decisions} ${decisions === 1 ? "decision" : "decisions"}` : undefined,
    events > 0 ? `${events} ${events === 1 ? "event" : "events"}` : undefined,
    automationCompact(loopState?.last_event_summary ?? session?.loop_protocol?.state?.last_event_summary),
  ].filter(Boolean).join(" · ");
  return {
    label: "Protocol",
    value: path ? automationFileLabel(path) : "LOOP.md",
    detail: detail || "Protocol file detected",
    tone: status === "running" ? "ok" : status === "draft" ? "attention" : status === "disabled" ? "danger" : "neutral",
  };
}

function automationTimerMetric(session: SessionSummary | undefined, panelState: ScheduleState): SessionAutomationMetric {
  if (panelState.state === "loading") return { label: "Timers", value: "Loading", detail: "Reading saved timers", tone: "neutral" };
  if (panelState.state === "error") return { label: "Timers", value: "Error", detail: automationCompact(panelState.error) ?? "Timer details unavailable", tone: "danger" };
  const visibleSchedules = panelState.state === "ready" ? panelState.schedules : [];
  const visibleEnabled = visibleSchedules.filter((schedule) => schedule.enabled).length;
  const summary = session?.schedules;
  const errors = summary?.error_count ?? 0;
  if (errors > 0 || summary?.last_error) return { label: "Timers", value: `${Math.max(errors, 1)} error`, detail: automationCompact(summary?.last_error) ?? "Inspect timer history", tone: "danger" };
  const enabled = Math.max(summary?.enabled ?? 0, visibleEnabled);
  const count = Math.max(summary?.count ?? 0, visibleSchedules.length);
  if (enabled > 0) return { label: "Timers", value: `${enabled} active`, detail: summary?.next_run_at ? `Next ${automationFormatTime(summary.next_run_at)}` : "Load details to inspect next run", tone: "ok" };
  if (count > 0) return { label: "Timers", value: `${count} paused`, detail: "Resume or delete saved timers", tone: "neutral" };
  return { label: "Timers", value: "Off", detail: "No scheduled follow-ups", tone: "neutral" };
}

function automationNextRunMetric(
  session: SessionSummary | undefined,
  loopState: SessionSummary["loop_state"] | undefined,
  panelState: ScheduleState,
): SessionAutomationMetric {
  if (panelState.state === "loading") return { label: "Next run", value: "Loading", detail: "Reading timer details", tone: "neutral" };
  if (panelState.state === "error") return { label: "Next run", value: "Error", detail: automationCompact(panelState.error) ?? "Timer details unavailable", tone: "danger" };
  const summary = session?.schedules;
  if (summary?.next_run_at) {
    return {
      label: "Next run",
      value: automationFormatTime(summary.next_run_at),
      detail: automationCompact(summary.next_prompt_preview) ?? scheduleKindDetail(summary.next_schedule_kind),
      tone: "ok",
    };
  }
  const visibleSchedules = panelState.state === "ready" ? panelState.schedules : [];
  const enabled = Math.max(summary?.enabled ?? 0, visibleSchedules.filter((schedule) => schedule.enabled).length);
  if (enabled > 0) return { label: "Next run", value: "Unknown", detail: "Load timers to inspect schedule", tone: "attention" };
  const loopRunning = automationCompact(loopState?.status ?? session?.loop_protocol?.status)?.toLowerCase() === "running";
  if (loopRunning) return { label: "Next run", value: "Manual", detail: "No timer scheduled for this loop", tone: "neutral" };
  return { label: "Next run", value: "None", detail: "No scheduled follow-ups", tone: "neutral" };
}

function scheduleKindDetail(kind: string | undefined): string {
  if (kind === "loop_tick") return "30m timer";
  if (kind === "daily_checkin") return "daily check-in";
  if (kind === "checkin") return "check-in";
  return "scheduled follow-up";
}

function scheduleKindLabel(kind: string | undefined): string {
  if (kind === "loop_tick") return "30m timer";
  if (kind === "daily_checkin") return "Daily check-in";
  if (kind === "checkin") return "Check-in";
  return "Timer";
}

function formatAutomationDuration(seconds: number): string {
  if (seconds % 86400 === 0) return `${seconds / 86400}d`;
  if (seconds % 3600 === 0) return `${seconds / 3600}h`;
  if (seconds % 60 === 0) return `${seconds / 60}m`;
  return `${seconds}s`;
}

function automationFormatTime(value: string): string {
  const time = Date.parse(value);
  if (!Number.isFinite(time)) return value;
  return new Intl.DateTimeFormat(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(time));
}

function automationCompact(value?: string): string | undefined {
  const next = value?.replace(/\s+/g, " ").trim();
  return next || undefined;
}

function automationStatusLabel(status: string): string {
  return status
    .split(/[_-]+/)
    .filter(Boolean)
    .map((part) => part[0]?.toUpperCase() + part.slice(1))
    .join(" ") || "Unknown";
}

function automationFileLabel(path: string): string {
  const parts = path.replace(/\\/g, "/").split("/").filter(Boolean);
  return parts.at(-1) ?? path;
}

function compactArray<T>(items: readonly (T | undefined)[]): T[] {
  return items.filter((item): item is T => item !== undefined);
}

function latestChatMeta(updated: string): string | undefined {
  return updated && updated !== "No messages yet" ? updated : undefined;
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

function webScheduleDisplayText(kind: "loop" | "checkin" | "daily", sessionTitle: string): string {
  if (kind === "loop") return `Every 30m: ${sessionTitle}`;
  if (kind === "daily") return `Daily check-in: ${sessionTitle}`;
  return `Check in 1h: ${sessionTitle}`;
}

function webScheduledLoopTickPrompt(sessionTitle: string): string {
  return [
    `Scheduled 30m timer for session: ${sessionTitle}`,
    "",
    "This is a scheduled runtime turn, not a new human instruction.",
    "Execute the scheduled check directly. Use LOOP.md only if an active loop protocol already exists and is relevant.",
    "Keep the turn compact: inspect only the evidence needed for this scheduled check, report the result, and record durable state only when it is genuinely useful for future turns.",
    "End with a concise status and any blocker that should pause future timer runs.",
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
