import type { MemoryUpdateMeta } from "../api/events";
import type { WorkbenchAccessPanelState, WorkbenchMemoryPanelState, WorkbenchRuntimePanelState, WorkbenchSkillsPanelState } from "./workbenchPanels";
import type { SessionChangesView } from "./sessionChanges";
import type { SessionFilesView } from "./sessionFiles";
import type { SessionOverview } from "./sessionOverview";
import type { SessionRunView } from "./sessionRun";
import type { SessionTraceView } from "./sessionTrace";
import type { SessionWorkspaceView } from "./sessionWorkspace";
import type { TurnArtifact } from "./turnArtifacts";
import type { WorkbenchAttention, WorkbenchAttentionTarget, WorkbenchAttentionTone } from "./workbenchAttention";
import { workbenchContextUsageSummary, type WorkbenchContextUsageView } from "./workbenchContext";
import {
  shouldShowWorkbenchAccessPanel,
  shouldShowWorkbenchMemoryPanel,
  shouldShowWorkbenchRuntimePanel,
  shouldShowWorkbenchSkillsPanel,
} from "./workbenchPanels";

export type WorkbenchTab = "context" | "changes" | "run" | "artifacts" | "files" | "workspace" | "loop" | "automation" | "memory" | "skills" | "config" | "trace";
export type WorkbenchNavTone = "error" | "attention";
export type WorkbenchNavScope = "current" | "platform";

export interface WorkbenchNavItem {
  key: WorkbenchTab;
  label: string;
  detail: string;
  scope: WorkbenchNavScope;
  badge?: string;
  tone?: WorkbenchNavTone;
}

export function buildWorkbenchNavItems({
  changes,
  run,
  artifacts = [],
  files,
  workspaceBrowserActive = false,
  workspace,
  trace,
  usage,
  automation,
  attention,
  runtimeState,
  configState,
  memoryState,
  skillsState,
  latestMemoryUpdate,
}: {
  overview: SessionOverview;
  changes: SessionChangesView;
  run: SessionRunView;
  artifacts?: readonly TurnArtifact[];
  files: SessionFilesView;
  workspaceBrowserActive?: boolean;
  workspace: SessionWorkspaceView;
  trace?: SessionTraceView;
  usage?: WorkbenchContextUsageView;
  automation?: { title: string };
  attention?: WorkbenchAttention;
  runtimeState: WorkbenchRuntimePanelState;
  configState: WorkbenchAccessPanelState;
  memoryState: WorkbenchMemoryPanelState;
  skillsState: WorkbenchSkillsPanelState;
  latestMemoryUpdate?: MemoryUpdateMeta;
}): WorkbenchNavItem[] {
  const runtimeTabHasSignal = shouldShowWorkbenchRuntimePanel(runtimeState);
  const configTabHasSignal = shouldShowWorkbenchAccessPanel(configState);
  const memoryTabHasSignal = shouldShowWorkbenchMemoryPanel(memoryState, latestMemoryUpdate);
  const skillsTabHasSignal = shouldShowWorkbenchSkillsPanel(skillsState);

  const currentItems: WorkbenchNavItem[] = [
    {
      key: "context",
      label: "Task",
      scope: "current",
      detail: taskNavDetail(usage),
    },
    changesNavItem(changes, attention),
    runNavItem(run, artifacts, attention),
    filesNavItem(files, workspaceBrowserActive, workspace, attention),
    automationNavItem(automation, attention),
  ];

  return [
    ...currentItems,
    {
      key: "memory",
      label: "Memory",
      scope: "platform",
      detail: memoryNavDetail(memoryState),
      badge: memoryTabHasSignal ? memoryBadge(memoryState, latestMemoryUpdate) : undefined,
      tone: memoryState.state === "error" ? "error" : undefined,
    },
    {
      key: "skills",
      label: "Skills",
      scope: "platform",
      detail: skillsNavDetail(skillsState),
      badge: skillsTabHasSignal ? skillsBadge(skillsState) : undefined,
      tone: skillsState.state === "error" ? "error" : undefined,
    },
    {
      key: "config",
      label: "Config",
      scope: "platform",
      detail: configNavDetail(configState),
      badge: configTabHasSignal ? configBadge(configState) : undefined,
      tone: configState.state === "error" ? "error" : undefined,
    },
    traceNavItem(trace, runtimeState, runtimeTabHasSignal),
  ];
}

function taskNavDetail(usage?: WorkbenchContextUsageView): string {
  const usageSummary = workbenchContextUsageSummary(usage);
  if (usageSummary) return usageSummary;
  return "Current task";
}

function toneForAttention(tone: SessionOverview["tone"] | WorkbenchAttentionTone | undefined): WorkbenchNavTone | undefined {
  return tone === "error" ? "error" : undefined;
}

function artifactBadge(artifacts: readonly TurnArtifact[]): string | undefined {
  if (artifacts.length === 0) return undefined;
  if (artifacts.length > 99) return "99+";
  return String(artifacts.length);
}

export function workbenchTabFromAttention(target: WorkbenchAttentionTarget): WorkbenchTab {
  if (target === "automation") return "automation";
  if (target === "workspace") return "files";
  return target;
}

function changesNavItem(changes: SessionChangesView, attention?: WorkbenchAttention): WorkbenchNavItem {
  return {
    key: "changes",
    label: "Changes",
    scope: "current",
    detail: changes.files.length > 0 ? changes.detail : "Diff review",
    badge: changes.files.length > 0 ? String(changes.files.length) : undefined,
    tone: toneForAttention(attention?.target === "changes" ? attention.tone : changes.tone),
  };
}

function runNavItem(run: SessionRunView, artifacts: readonly TurnArtifact[], attention?: WorkbenchAttention): WorkbenchNavItem {
  const artifactCount = artifacts.length;
  const commandCount = run.commands.length;
  const badge = commandCount > 0 ? String(commandCount) : artifactBadge(artifacts);
  const detail = commandCount > 0 ? run.detail : artifactCount > 0 ? `${artifactCount} stored output${artifactCount === 1 ? "" : "s"}` : "Command console";
  return {
    key: "run",
    label: "Run",
    scope: "current",
    detail,
    badge,
    tone: toneForAttention(attention?.target === "run" ? attention.tone : run.tone),
  };
}

function filesNavItem(
  files: SessionFilesView,
  workspaceBrowserActive: boolean,
  workspace: SessionWorkspaceView,
  attention?: WorkbenchAttention,
): WorkbenchNavItem {
  const detail = files.items.length > 0 ? files.detail : workspace.hasData || workspaceBrowserActive ? "Workspace browser" : "Workspace files";
  const badge = workspace.issue ? "!" : files.items.length > 0 ? String(files.items.length) : undefined;
  return {
    key: "files",
    label: "Files",
    scope: "current",
    detail,
    badge,
    tone: toneForAttention(attention?.target === "workspace" ? attention.tone : attention?.target === "files" ? attention.tone : workspace.issue ? workspace.tone : files.tone),
  };
}

function automationNavItem(automation?: { title: string }, attention?: WorkbenchAttention): WorkbenchNavItem {
  return {
    key: "automation",
    label: "Automation",
    scope: "current",
    detail: automation?.title ?? "Loop and timers",
    badge: automation ? "active" : undefined,
    tone: toneForAttention(attention?.target === "automation" ? attention.tone : undefined),
  };
}

function traceNavItem(
  trace: SessionTraceView | undefined,
  runtimeState: WorkbenchRuntimePanelState,
  runtimeTabHasSignal: boolean,
): WorkbenchNavItem {
  if (trace && trace.eventCount > 0) {
    return {
      key: "trace",
      label: "Trace",
      scope: "platform",
      detail: traceNavDetail(trace),
      badge: traceBadge(trace),
      tone: undefined,
    };
  }
  return {
    key: "trace",
    label: "Trace",
    scope: "platform",
    detail: runtimeNavDetail(runtimeState),
    badge: runtimeTabHasSignal ? runtimeBadge(runtimeState) : undefined,
    tone: runtimeState.state === "error" ? "error" : undefined,
  };
}

function runtimeNavDetail(state: WorkbenchRuntimePanelState): string {
  if (state.state === "loading") return "Loading diagnostics";
  if (state.state === "error") return "Diagnostics unavailable";
  if (state.state === "ready") return state.stats.model?.trim() || "Runtime diagnostics";
  return "Runtime diagnostics";
}

function runtimeBadge(state: WorkbenchRuntimePanelState): string | undefined {
  if (state.state === "loading") return "...";
  if (state.state === "error") return "!";
  if (state.state !== "ready") return undefined;
  const issues = (state.stats.aggregate?.blocked_by_type ?? 0)
    + (state.stats.aggregate?.blocked_by_domain ?? 0)
    + (state.stats.aggregate?.tools?.tool_errors ?? 0)
    + (state.stats.aggregate?.runtime?.runtime_errors ?? 0);
  if (issues > 0) return String(issues);
  if ((state.stats.running_turns ?? 0) > 0) return "run";
  return "on";
}

function traceNavDetail(trace: SessionTraceView): string {
  if (trace.unknownCount > 0) return `${trace.recordCount} records · ${trace.unknownCount} unclassified`;
  if (trace.schemaVersion) return `${trace.recordCount} records · schema v${trace.schemaVersion}`;
  return `${trace.recordCount} grouped ${trace.recordCount === 1 ? "record" : "records"}`;
}

function traceBadge(trace: SessionTraceView): string | undefined {
  if (trace.eventCount <= 0) return undefined;
  if (trace.eventCount > 99) return "99+";
  return String(trace.eventCount);
}

function configNavDetail(state: WorkbenchAccessPanelState): string {
  if (state.state === "loading") return "Loading env and SSH";
  if (state.state === "error") return "Config unavailable";
  if (state.state === "ready") return state.settings.env.length > 0 ? `${state.settings.env.length} env configured` : "Env and SSH";
  return "Env and SSH";
}

function configBadge(state: WorkbenchAccessPanelState): string | undefined {
  if (state.state === "loading") return "...";
  if (state.state === "error") return "!";
  if (state.state !== "ready") return undefined;
  if (state.settings.env.length > 0) return String(state.settings.env.length);
  if (state.settings.ssh.exists || state.settings.ssh.public_key) return "ssh";
  return undefined;
}

function memoryNavDetail(state: WorkbenchMemoryPanelState): string {
  if (state.state === "loading") return "Loading memory";
  if (state.state === "error") return "Memory unavailable";
  if (state.state === "empty") return "Open a chat";
  if (state.state === "ready") return state.memory.has_memory ? `${state.memory.topics?.length ?? 0} topics` : "No durable memory";
  return "Durable memory";
}

function memoryBadge(state: WorkbenchMemoryPanelState, latestUpdate?: MemoryUpdateMeta): string | undefined {
  if (latestUpdate) return "updated";
  if (state.state === "loading") return "...";
  if (state.state === "error") return "!";
  if (state.state === "ready" && state.memory.has_memory) return String(state.memory.topics?.length ?? 0);
  return undefined;
}

function skillsNavDetail(state: WorkbenchSkillsPanelState): string {
  if (state.state === "loading") return "Loading skills";
  if (state.state === "error") return "Skills unavailable";
  if (state.state === "ready") return state.skills.length > 0 ? `${state.skills.length} reusable workflows` : "No reusable workflows";
  return "Reusable workflows";
}

function skillsBadge(state: WorkbenchSkillsPanelState): string | undefined {
  if (state.state === "loading") return "...";
  if (state.state === "error") return "!";
  if (state.state === "ready" && state.skills.length > 0) return String(state.skills.length);
  return undefined;
}
