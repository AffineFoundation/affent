import type { ApiClient } from "./client";

export interface BuildInfo {
  revision?: string;
  date?: string;
}

export interface StatsUsageSnapshot {
  input_tokens: number;
  output_tokens: number;
  turns: number;
}

export interface StatsBrowserSnapshot {
  blocked_by_type: number;
  blocked_by_domain: number;
  domain_relaxations?: number;
  cache_hit: number;
  cache_miss: number;
  network_fetch: number;
}

export interface StatsToolSnapshot {
  tool_requests: number;
  tool_requests_admitted?: number;
  tool_requests_skipped?: number;
  tool_name_canonicalized?: number;
  tool_args_repaired?: number;
  tool_repair_calls?: number;
  tool_repair_succeeded?: number;
  tool_repair_failed?: number;
  tool_repair_notes?: number;
  tool_repair_by_kind?: Record<string, number>;
  tool_failure_by_kind?: Record<string, number>;
  tool_errors: number;
  tool_duration_ms?: number;
  loop_guard_interventions?: number;
  forced_no_tools?: number;
  source_access_results?: number;
  source_access_verified?: number;
  source_access_discovery_only?: number;
  source_access_network?: number;
  source_access_dynamic_partial?: number;
  memory_updates?: number;
  memory_update_add?: number;
  memory_update_replace?: number;
  memory_update_remove?: number;
  session_search_calls?: number;
  session_search_results?: number;
  session_search_context_hits?: number;
  session_search_matched_terms?: number;
  tool_context_truncated?: number;
  tool_context_omitted_bytes?: number;
}

export interface StatsRuntimeSnapshot {
  turn_end_by_reason?: Record<string, number>;
  runtime_errors: number;
  runtime_error_by_kind?: Record<string, number>;
  context_compactions?: number;
  context_compactions_reactive?: number;
  context_compaction_removed_messages?: number;
  context_compaction_summary_bytes?: number;
  context_compaction_summary_missing?: number;
  context_compaction_summary_empty?: number;
  context_compaction_latest_model_context_window_source?: string;
}

export interface RuntimeCapabilityContract {
  status?: string;
  expected?: string[];
  available?: string[];
  missing?: string[];
  warnings?: string[];
}

export interface ScheduleRunnerStats {
  enabled?: boolean;
  active?: boolean;
  frontend_independent?: boolean;
  sweep_interval?: string;
  durable_session_state_dir?: string;
  sessions_with_schedules?: number;
  schedules?: number;
  enabled_schedules?: number;
  due_schedules?: number;
  in_flight_schedules?: number;
  error_schedules?: number;
  next_run_at?: string;
  next_session_id?: string;
  next_schedule_id?: string;
  next_schedule_kind?: string;
  next_prompt_preview?: string;
  oldest_in_flight_at?: string;
  oldest_in_flight_session_id?: string;
  oldest_in_flight_schedule_id?: string;
  last_error_session_id?: string;
  last_error_schedule_id?: string;
  last_error?: string;
  disabled_reason?: string;
}

export interface ServerSessionStats {
  id: string;
  created_at: string;
  last_used_at: string;
  usage: StatsUsageSnapshot;
  tools: StatsToolSnapshot;
  runtime: StatsRuntimeSnapshot;
  browser: StatsBrowserSnapshot;
  runtime_contract?: RuntimeCapabilityContract;
}

export interface ServerAggregateStats {
  blocked_by_type: number;
  blocked_by_domain: number;
  domain_relaxations?: number;
  cache_hit: number;
  cache_miss: number;
  network_fetch: number;
  input_tokens: number;
  output_tokens: number;
  turns: number;
  tools: StatsToolSnapshot;
  runtime: StatsRuntimeSnapshot;
}

export interface ServerStatsResponse {
  listen?: string;
  model?: string;
  build?: BuildInfo;
  max_sessions?: number;
  active_sessions?: number;
  running_turns?: number;
  executor_mode?: string;
  enable_browser?: boolean;
  enable_web?: boolean;
  enable_web_search?: boolean;
  enable_memory?: boolean;
  shared_user_memory?: boolean;
  enable_builtins?: boolean;
  enable_subagent?: boolean;
  enable_focused_tasks?: boolean;
  enable_loop_protocol?: boolean;
  eval_mode?: boolean;
  eval_tools?: string;
  eval_all_tools?: boolean;
  shutting_down?: boolean;
  workspace_root?: string;
  memory_root?: string;
  session_state_root?: string;
  browser_cache_dir?: string;
  web_search_backend?: string;
  schedule_runner?: ScheduleRunnerStats;
  server_time?: string;
  sessions?: ServerSessionStats[];
  aggregate?: ServerAggregateStats;
  boundaries?: Record<string, number | string>;
  runtime_contract?: RuntimeCapabilityContract;
}

export function getServerStats(client: ApiClient, signal?: AbortSignal): Promise<ServerStatsResponse> {
  return client.json<ServerStatsResponse>("/v1/stats", { signal });
}
