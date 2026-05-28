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
      label: "Context",
      scope: "current",
      detail: contextNavDetail(usage),
    },
    {
      key: "loop",
      label: "Automation",
      scope: "current",
      detail: automation?.title ?? "Loop and timers",
      badge: automation ? "active" : undefined,
      tone: toneForAttention(attention?.target === "automation" ? attention.tone : undefined),
    },
  ];
  if (changes.files.length > 0 || attention?.target === "changes") {
    currentItems.push({
      key: "changes",
      label: "Changes",
      scope: "current",
      detail: changes.files.length > 0 ? changes.detail : "Changed file review",
      badge: changes.files.length > 0 ? String(changes.files.length) : undefined,
      tone: toneForAttention(attention?.target === "changes" ? attention.tone : changes.tone),
    });
  }
  if (run.commands.length > 0 || attention?.target === "run") {
    currentItems.push({
      key: "run",
      label: "Run",
      scope: "current",
      detail: run.commands.length > 0 ? run.detail : "Command history",
      badge: run.commands.length > 0 ? String(run.commands.length) : undefined,
      tone: toneForAttention(attention?.target === "run" ? attention.tone : run.tone),
    });
  }
  if (artifacts.length > 0) {
    currentItems.push({
      key: "artifacts",
      label: "Artifacts",
      scope: "current",
      detail: artifactNavDetail(artifacts),
      badge: artifactBadge(artifacts),
    });
  }
  if (files.items.length > 0 || attention?.target === "files") {
    currentItems.push({
      key: "files",
      label: "Files",
      scope: "current",
      detail: files.items.length > 0 ? files.detail : "Task file evidence",
      badge: files.items.length > 0 ? String(files.items.length) : undefined,
      tone: toneForAttention(attention?.target === "files" ? attention.tone : files.tone),
    });
  }
  if (workspace.hasData || attention?.target === "workspace") {
    currentItems.push({
      key: "workspace",
      label: "Workspace",
      scope: "current",
      detail: workspace.hasData ? workspace.summary : "No binding evidence",
      badge: workspace.issue ? "!" : undefined,
      tone: toneForAttention(attention?.target === "workspace" ? attention.tone : workspace.tone),
    });
  }
  if (trace && trace.eventCount > 0) {
    currentItems.push({
      key: "trace",
      label: "Trace",
      scope: "current",
      detail: traceNavDetail(trace),
      badge: traceBadge(trace),
      tone: undefined,
    });
  }

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
    ...(trace && trace.eventCount > 0
      ? []
      : [{
        key: "trace" as const,
        label: "Trace",
        scope: "platform" as const,
        detail: runtimeNavDetail(runtimeState),
        badge: runtimeTabHasSignal ? runtimeBadge(runtimeState) : undefined,
        tone: runtimeState.state === "error" ? "error" as const : undefined,
      }]),
  ];
}

function contextNavDetail(usage?: WorkbenchContextUsageView): string {
  const usageSummary = workbenchContextUsageSummary(usage);
  if (usageSummary) return usageSummary;
  return "Current chat";
}

function toneForAttention(tone: SessionOverview["tone"] | WorkbenchAttentionTone | undefined): WorkbenchNavTone | undefined {
  return tone === "error" ? "error" : undefined;
}

function artifactNavDetail(artifacts: readonly TurnArtifact[]): string {
  const truncated = artifacts.filter((artifact) => artifact.truncated).length;
  const totalBytes = artifacts.reduce((sum, artifact) => sum + (artifact.bytes ?? 0), 0);
  const parts = [`${artifacts.length} ${artifacts.length === 1 ? "generated file" : "generated files"}`];
  if (truncated > 0) parts.push(`${truncated} full output`);
  if (totalBytes > 0) parts.push(`${Math.ceil(totalBytes / 1024)} KiB`);
  return parts.join(" · ");
}

function artifactBadge(artifacts: readonly TurnArtifact[]): string | undefined {
  if (artifacts.length === 0) return undefined;
  if (artifacts.length > 99) return "99+";
  return String(artifacts.length);
}

export function workbenchTabFromAttention(target: WorkbenchAttentionTarget): WorkbenchTab {
  if (target === "automation") return "loop";
  return target;
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
