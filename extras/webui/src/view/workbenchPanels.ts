import type { AccountSettingsResponse } from "../api/settings";
import type { ServerStatsResponse } from "../api/stats";
import type { MemoryUpdateMeta } from "../api/events";
import type { SessionMemoryResponse, SessionSkillInfo } from "../api/sessions";

type IdleLoadingOrEmptyState =
  | { state: "idle" }
  | { state: "empty" }
  | { state: "loading" };

export type WorkbenchRuntimePanelState =
  | IdleLoadingOrEmptyState
  | { state: "ready"; stats: ServerStatsResponse }
  | { state: "error"; error: string };

export type WorkbenchAccessPanelState =
  | IdleLoadingOrEmptyState
  | { state: "ready"; settings: AccountSettingsResponse }
  | { state: "error"; error: string; settings?: AccountSettingsResponse };

export type WorkbenchMemoryPanelState =
  | IdleLoadingOrEmptyState
  | { state: "ready"; memory: SessionMemoryResponse }
  | { state: "error"; error: string };

export type WorkbenchSkillsPanelState =
  | IdleLoadingOrEmptyState
  | { state: "ready"; skills: readonly SessionSkillInfo[] }
  | { state: "error"; error: string };

export function shouldShowWorkbenchRuntimePanel(state: WorkbenchRuntimePanelState): boolean {
  if (state.state === "loading" || state.state === "error") return true;
  if (state.state !== "ready") return false;
  const stats = state.stats;
  const aggregate = stats.aggregate;
  const tools = aggregate?.tools;
  const runtime = aggregate?.runtime;
  return !!(
    stats.shutting_down
    || (stats.running_turns ?? 0) > 0
    || stats.eval_mode
    || stats.eval_all_tools
    || (aggregate?.blocked_by_type ?? 0) > 0
    || (aggregate?.blocked_by_domain ?? 0) > 0
    || (aggregate?.domain_relaxations ?? 0) > 0
    || (aggregate?.network_fetch ?? 0) > 0
    || (tools?.tool_errors ?? 0) > 0
    || (tools?.source_access_results ?? 0) > 0
    || (tools?.memory_updates ?? 0) > 0
    || (tools?.session_search_calls ?? 0) > 0
    || (tools?.loop_guard_interventions ?? 0) > 0
    || (tools?.tool_context_truncated ?? 0) > 0
    || (runtime?.runtime_errors ?? 0) > 0
    || (runtime?.context_compactions ?? 0) > 0
  );
}

export function shouldShowWorkbenchAccessPanel(state: WorkbenchAccessPanelState): boolean {
  if (state.state === "loading" || state.state === "error") return true;
  if (state.state !== "ready") return false;
  const ssh = state.settings.ssh;
  return state.settings.env.length > 0 || !!ssh.exists || !!ssh.public_key || !!ssh.public_key_error;
}

export function shouldShowWorkbenchMemoryPanel(
  state: WorkbenchMemoryPanelState,
  latestUpdate?: MemoryUpdateMeta,
): boolean {
  if (state.state === "loading" || state.state === "error") return true;
  if (latestUpdate) return true;
  return state.state === "ready" && state.memory.has_memory;
}

export function shouldShowWorkbenchSkillsPanel(state: WorkbenchSkillsPanelState): boolean {
  if (state.state === "loading" || state.state === "error") return true;
  return state.state === "ready" && state.skills.length > 0;
}
