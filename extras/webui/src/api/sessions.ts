// Faithful TypeScript mirror of cmd/affentserve/sessions_api.go and the
// snapshot types in sessions.go. Source of truth for the session-control
// surface: GET/POST /v1/sessions, GET/DELETE /v1/sessions/{id},
// GET /v1/sessions/{id}/loop-protocol, plus account-level skill settings
// at /v1/skills.
//
// Kept in parity with the Go json tags; the parity guard covers this too.

import type { ApiClient } from "./client";
import type { MemoryUpdateMeta, SessionHistoryResponse } from "./events";
import type { StreamEventsOptions } from "./stream";

export interface UsageSnapshot {
  input_tokens: number;
  output_tokens: number;
  turns: number;
}

export interface BrowserStatsSnapshot {
  blocked_by_type: number;
  blocked_by_domain: number;
  cache_hit: number;
  cache_miss: number;
  network_fetch: number;
}

export interface ToolStatsSnapshot {
  tool_requests: number;
  tool_name_canonicalized?: number;
  tool_args_repaired?: number;
  tool_repair_calls?: number;
  tool_errors: number;
  tool_repair_succeeded: number;
  tool_repair_failed: number;
  tool_repair_notes?: number;
  tool_repair_by_kind?: Record<string, number>;
  tool_failure_by_kind?: Record<string, number>;
  tool_duration_ms?: number;
  loop_guard_interventions?: number;
  forced_no_tools?: number;
  source_access_results?: number;
  source_access_verified?: number;
  source_access_discovery_only?: number;
  source_access_network?: number;
  source_access_dynamic_partial?: number;
  memory_search_calls?: number;
  memory_search_misses?: number;
  session_search_calls?: number;
  session_search_results?: number;
  session_search_context_hits?: number;
  session_search_matched_terms?: number;
  session_search_recent_sessions?: number;
  memory_updates?: number;
  memory_update_add?: number;
  memory_update_replace?: number;
  memory_update_remove?: number;
  tool_context_truncated?: number;
  tool_context_omitted_bytes?: number;
}

export interface SessionToolInfo {
  name: string;
  raw_name?: string;
  description: string;
  parameters: unknown;
  group: string;
  source?: string;
  arg_policy?: SessionToolArgPolicy;
}

export interface SessionToolArgPolicy {
  workspace_path_args?: string[];
}

export interface SessionToolsSurfaceInfo {
  headline: string;
  detail: string;
  tone: "ready" | "warning" | "muted" | "unknown";
  status: "allowed" | "filtered" | "restricted" | "unknown";
  disabled_reasons?: string[];
  warnings?: string[];
}

export interface SessionToolsResponse {
  session_id: string;
  count: number;
  tools: SessionToolInfo[];
  surface?: SessionToolsSurfaceInfo;
}

export interface SessionMemoryBucket {
  target: string;
  topic?: string;
  entries?: string[];
  entry_count: number;
  chars_used: number;
  chars_limit?: number;
  percent?: number;
  newest_at?: string;
}

export interface SessionMemoryResponse {
  session_id: string;
  has_memory: boolean;
  shared_user_memory?: boolean;
  user?: SessionMemoryBucket;
  core?: SessionMemoryBucket;
  topics?: SessionMemoryBucket[];
}

export interface SessionMemoryAddRequest {
  action?: "add";
  target?: string;
  topic?: string;
  content: string;
}

export interface SessionMemoryRemoveRequest {
  action: "remove";
  target?: string;
  topic?: string;
  old_text: string;
}

export interface SessionMemoryReplaceRequest {
  action: "replace";
  target?: string;
  topic?: string;
  old_text: string;
  new_content: string;
}

export interface SessionPlanResponse {
  session_id: string;
  plan: unknown;
  summary?: SessionPlanSummary;
}

export interface SessionLoopProtocolSummary {
  path?: string;
  loop_id?: string;
  owner_session?: string;
  status?: string;
  updated_at?: string;
  bytes: number;
  preview?: string;
  state?: SessionLoopState;
}

export interface SessionLoopState {
  version: number;
  loop_id?: string;
  owner_session?: string;
  status?: string;
  protocol_path?: string;
  created_at?: string;
  updated_at?: string;
  initial_goal_preview?: string;
  initial_plan_label?: string;
  last_protocol_update_at?: string;
  protocol_updates?: number;
  calibration_questions?: number;
  last_calibration_question_at?: string;
  last_calibration_question_preview?: string;
  calibration_answers?: number;
  last_calibration_answer_at?: string;
  last_calibration_answer_preview?: string;
  protocol_feeds?: number;
  last_protocol_feed_at?: string;
  last_protocol_feed_mode?: string;
  needs_full_protocol_feed?: boolean;
  last_plan_label?: string;
  last_plan_step_index?: number;
  last_plan_step_status?: string;
  last_plan_step?: string;
  turn_checkpoints?: number;
  last_turn_id?: string;
  last_turn_end_reason?: string;
  last_turn_at?: string;
  last_turn_input_tokens?: number;
  last_turn_output_tokens?: number;
  last_turn_tool_requests?: number;
  last_turn_tool_errors?: number;
  last_turn_loop_guards?: number;
  last_turn_forced_no_tools?: number;
  last_turn_memory_updates?: number;
  last_turn_session_search_calls?: number;
  memory_update_events?: number;
  last_memory_update_action?: string;
  last_memory_update_target?: string;
  last_memory_update_topic?: string;
  last_memory_update_location?: string;
  last_memory_update_previous_preview?: string;
  last_memory_update_next_preview?: string;
  last_memory_update_preview?: string;
  last_memory_update_at?: string;
  loop_decisions?: number;
  last_decision_id?: string;
  last_decision_kind?: string;
  last_decision_trigger?: string;
  last_decision?: string;
  last_decision_confidence?: string;
  last_decision_reason?: string;
  last_decision_required_action?: string;
  last_decision_token_budget?: number;
  last_decision_observed_input_tokens?: number;
  last_decision_projected_input_tokens?: number;
  last_decision_budget_bytes?: number;
  last_decision_at?: string;
  context_compactions?: number;
  last_context_compaction_at?: string;
  last_context_compaction_reason?: string;
  last_context_compaction_reactive?: boolean;
  event_count?: number;
  last_event_type?: string;
  last_event_summary?: string;
  last_event_at?: string;
}

export interface SessionLoopEvent {
  seq: number;
  time: string;
  type: string;
  summary?: string;
  sections_changed?: string[];
  reason?: string;
  path?: string;
  mode?: string;
  reactive?: boolean;
  feed_number?: number;
  plan_label?: string;
  plan_step_index?: number;
  plan_step_status?: string;
  plan_step?: string;
  turn_id?: string;
  turn_end_reason?: string;
  input_tokens?: number;
  output_tokens?: number;
  tool_requests?: number;
  tool_errors?: number;
  loop_guards?: number;
  forced_no_tools?: number;
  memory_updates?: number;
  session_search_calls?: number;
  decision_id?: string;
  decision_kind?: string;
  trigger?: string;
  decision?: string;
  confidence?: string;
  required_action?: string;
  call_id?: string;
  memory_action?: string;
  memory_target?: string;
  memory_topic?: string;
  memory_location?: string;
  memory_preview?: string;
  previous_preview?: string;
  next_preview?: string;
  calibration_preview?: string;
}

export interface SessionLoopProtocolResponse {
  session_id: string;
  protocol: string;
  summary?: SessionLoopProtocolSummary;
  state?: SessionLoopState;
  events?: SessionLoopEvent[];
}

export interface SessionLoopProtocolUpdateRequest {
  protocol?: string;
  activate?: boolean;
  goal?: string;
  reason?: string;
  sections_changed?: string[];
}

export interface SessionLoopProtocolDeleteResponse {
  session_id: string;
  cleared: boolean;
  state?: SessionLoopState;
  events?: SessionLoopEvent[];
}

export interface SessionSchedule {
  id: string;
  kind?: "custom" | "checkin" | "daily_checkin" | "loop_tick";
  prompt: string;
  display_text?: string;
  enabled: boolean;
  next_run_at: string;
  repeat_interval_seconds?: number;
  created_at: string;
  updated_at: string;
  last_run_at?: string;
  last_turn_id?: string;
  run_count?: number;
  last_error?: string;
}

export interface SessionSchedulesSummary {
  count: number;
  enabled: number;
  enabled_loop_ticks?: number;
  pending_loop_ticks?: number;
  error_count?: number;
  last_error?: string;
  next_run_at?: string;
  next_schedule_id?: string;
  next_schedule_kind?: "custom" | "checkin" | "daily_checkin" | "loop_tick";
  next_prompt_preview?: string;
}

export interface SessionSchedulesResponse {
  session_id: string;
  schedules: SessionSchedule[];
  summary?: SessionSchedulesSummary;
}

export interface SessionScheduleCreateRequest {
  kind?: "custom" | "checkin" | "daily_checkin" | "loop_tick";
  prompt: string;
  display_text?: string;
  next_run_at: string;
  repeat_interval_seconds?: number;
  enabled?: boolean;
}

export interface SessionScheduleUpdateRequest {
  enabled?: boolean;
}

export interface SessionScheduleDeleteResponse {
  session_id: string;
  schedule_id: string;
  cleared: boolean;
  summary?: SessionSchedulesSummary;
}

export interface SessionSkillInfo {
  name: string;
  description?: string;
  source?: string;
  runtime: boolean;
  required_tools?: string[];
  triggers?: string[];
  auto_activation?: {
    any?: string[];
    all_any?: string[][];
  };
  body_preview?: string;
  body_bytes: number;
  body?: string;
}

export interface SessionSkillsResponse {
  session_id: string;
  count: number;
  install_enabled: boolean;
  skills: SessionSkillInfo[];
}

export interface SessionSkillResponse {
  session_id: string;
  skill: SessionSkillInfo;
}

export interface SessionSkillDeleteResponse {
  session_id: string;
  name: string;
  deleted: boolean;
}

export interface SessionSkillInstallRequest {
  name: string;
  description?: string;
  body: string;
  source?: string;
  triggers?: string[];
  required_tools?: string[];
}

export interface SessionCommandRequest {
  command: string;
  cwd?: string;
  timeout_sec?: number;
}

export interface SessionCommandResponse {
  session_id: string;
  turn_id: string;
  call_id: string;
  exit_code: number;
  result: string;
  duration_ms?: number;
  workspace?: string;
  completed_at: string;
}

export interface SessionFileEntry {
  name: string;
  path: string;
  kind: "file" | "directory" | string;
  bytes?: number;
  mod_time?: string;
}

export interface SessionFileResponse {
  session_id: string;
  path: string;
  kind: "file" | "directory" | string;
  bytes?: number;
  mod_time?: string;
  offset?: number;
  text?: string;
  has_more?: boolean;
  entries?: SessionFileEntry[];
}

export interface SessionFileReadOptions {
  path?: string;
  offset?: number;
  limit?: number;
  signal?: AbortSignal;
}

export interface SessionCapabilities {
  eval_mode: boolean;
  eval_tools?: string;
  eval_all_tools?: boolean;
  workspace_tools?: string[];
  builtins: boolean;
  skill_install: boolean;
  plan: boolean;
  memory: boolean;
  session_search: boolean;
  symbol_context: boolean;
  repo_search: boolean;
  browser: boolean;
  browser_screenshot: boolean;
  web: boolean;
  web_search: boolean;
  subagent: boolean;
  subagent_max_depth: number;
  focused_tasks: boolean;
  focused_task_profiles?: string[];
}

export interface SessionPlanSummary {
  label: string;
  total_steps: number;
  completed_steps: number;
  active: boolean;
  blocked: boolean;
  done: boolean;
  current_step?: string;
  current_step_index?: number;
  current_step_status?: string;
  last_completed_step?: string;
  last_completed_step_index?: number;
  blocked_step?: string;
  blocked_step_index?: number;
  error: boolean;
}

export interface SessionContextSummary {
  message_count: number;
  compact_trigger: number;
  compact_percent: number;
  messages_until_compact: number;
  context_bytes?: number;
  compact_trigger_bytes?: number;
  byte_compact_percent?: number;
  bytes_until_compact?: number;
  message_compact_percent?: number;
}

export interface SessionContextCompactionSummary {
  count: number;
  reactive: number;
  removed_messages: number;
  summary_bytes?: number;
  summary_missing?: number;
  summary_empty?: number;
  latest_reason?: string;
  latest_reactive?: boolean;
  latest_summary_state?: "present" | "missing" | "empty";
  tail_only?: boolean;
}

export interface SessionSummary {
  id: string;
  /** Human-readable summarized chat title, when the runtime provides one. */
  title?: string;
  /** Server-generated compact chat title, when the runtime can derive one confidently. */
  summary_title?: string;
  /** Compatibility for runtimes that name the generated title explicitly. */
  generated_title?: string;
  /** In the live in-memory pool right now. */
  active: boolean;
  /** Has a durable on-disk session dir (resumable). */
  durable: boolean;
  created_at?: string;
  last_used_at?: string;
  workspace_id?: string;
  workspace_path?: string;
  workspace_label?: string;
  default_branch?: string;
  dirty_state?: string;
  last_agent_cwd?: string;
  capabilities?: SessionCapabilities;
  latest_user_message?: string;
  topic_user_message?: string;
  latest_recovery_hint?: string;
  latest_memory_update?: MemoryUpdateMeta;
  has_plan?: boolean;
  plan_summary?: SessionPlanSummary;
  has_conversation: boolean;
  has_events: boolean;
  has_artifacts: boolean;
  has_loop_protocol?: boolean;
  loop_protocol?: SessionLoopProtocolSummary;
  has_loop_state?: boolean;
  loop_state?: SessionLoopState;
  has_schedules?: boolean;
  schedules?: SessionSchedulesSummary;
  has_memory: boolean;
  has_runtime_skills: boolean;
  context?: SessionContextSummary;
  context_compactions?: SessionContextCompactionSummary;
  usage?: UsageSnapshot;
  tools?: ToolStatsSnapshot;
  browser?: BrowserStatsSnapshot;
}

/** GET /v1/sessions */
export interface SessionListResponse {
  sessions: SessionSummary[];
  next_after?: string;
  has_more: boolean;
}

/** GET /v1/sessions/{id} and POST /v1/sessions */
export interface SessionDetailResponse {
  session: SessionSummary;
}

/** POST /v1/sessions body. */
export interface SessionCreateRequest {
  session_id?: string;
}

export interface SessionMessageRequest {
  content: string;
  display_text?: string;
  mode?: "normal" | "plan_only" | "execute_plan" | "loop_setup";
}

export interface SessionMessageResponse {
  session_id: string;
  turn_id: string;
}

export interface SessionCancelResponse {
  session_id: string;
  accepted: boolean;
}

export interface SessionHistoryOptions {
  after?: number;
  limit?: number;
  signal?: AbortSignal;
}

export interface ArtifactReadOptions {
  offset?: number;
  limit?: number;
  signal?: AbortSignal;
}

export interface ArtifactChunk {
  path: string;
  bytes: number;
  offset: number;
  text: string;
  hasMore: boolean;
}

export function listSessions(
  client: ApiClient,
  opts: { after?: string; limit?: number; signal?: AbortSignal } = {},
): Promise<SessionListResponse> {
  const q = new URLSearchParams();
  if (opts.after) q.set("after", opts.after);
  if (opts.limit != null) q.set("limit", String(opts.limit));
  return client.json<SessionListResponse>(withQuery("/v1/sessions", q), { signal: opts.signal });
}

export function listSessionTools(
  client: ApiClient,
  sessionId: string,
  signal?: AbortSignal,
): Promise<SessionToolsResponse> {
  return client.json<SessionToolsResponse>(`/v1/sessions/${encodeURIComponent(sessionId)}/tools`, { signal });
}

export function getSessionMemory(
  client: ApiClient,
  sessionId: string,
  signal?: AbortSignal,
): Promise<SessionMemoryResponse> {
  return client.json<SessionMemoryResponse>(`/v1/sessions/${encodeURIComponent(sessionId)}/memory`, { signal });
}

export function addSessionMemory(
  client: ApiClient,
  sessionId: string,
  body: SessionMemoryAddRequest,
  signal?: AbortSignal,
): Promise<SessionMemoryResponse> {
  return client.json<SessionMemoryResponse>(`/v1/sessions/${encodeURIComponent(sessionId)}/memory`, {
    method: "POST",
    body,
    signal,
  });
}

export function removeSessionMemory(
  client: ApiClient,
  sessionId: string,
  body: SessionMemoryRemoveRequest,
  signal?: AbortSignal,
): Promise<SessionMemoryResponse> {
  return client.json<SessionMemoryResponse>(`/v1/sessions/${encodeURIComponent(sessionId)}/memory`, {
    method: "POST",
    body,
    signal,
  });
}

export function replaceSessionMemory(
  client: ApiClient,
  sessionId: string,
  body: SessionMemoryReplaceRequest,
  signal?: AbortSignal,
): Promise<SessionMemoryResponse> {
  return client.json<SessionMemoryResponse>(`/v1/sessions/${encodeURIComponent(sessionId)}/memory`, {
    method: "POST",
    body,
    signal,
  });
}

export function getSessionPlan(
  client: ApiClient,
  sessionId: string,
  signal?: AbortSignal,
): Promise<SessionPlanResponse> {
  return client.json<SessionPlanResponse>(`/v1/sessions/${encodeURIComponent(sessionId)}/plan`, { signal });
}

export function getSessionLoopProtocol(
  client: ApiClient,
  sessionId: string,
  signal?: AbortSignal,
): Promise<SessionLoopProtocolResponse> {
  return client.json<SessionLoopProtocolResponse>(
    `/v1/sessions/${encodeURIComponent(sessionId)}/loop-protocol`,
    { signal },
  );
}

export function updateSessionLoopProtocol(
  client: ApiClient,
  sessionId: string,
  body: SessionLoopProtocolUpdateRequest,
  signal?: AbortSignal,
): Promise<SessionLoopProtocolResponse> {
  return client.json<SessionLoopProtocolResponse>(`/v1/sessions/${encodeURIComponent(sessionId)}/loop-protocol`, {
    method: "POST",
    body,
    signal,
  });
}

export function deleteSessionLoopProtocol(
  client: ApiClient,
  sessionId: string,
  signal?: AbortSignal,
): Promise<SessionLoopProtocolDeleteResponse> {
  return client.json<SessionLoopProtocolDeleteResponse>(
    `/v1/sessions/${encodeURIComponent(sessionId)}/loop-protocol`,
    { method: "DELETE", signal },
  );
}

export function listSessionSchedules(
  client: ApiClient,
  sessionId: string,
  signal?: AbortSignal,
): Promise<SessionSchedulesResponse> {
  return client.json<SessionSchedulesResponse>(`/v1/sessions/${encodeURIComponent(sessionId)}/schedules`, { signal });
}

export function createSessionSchedule(
  client: ApiClient,
  sessionId: string,
  body: SessionScheduleCreateRequest,
  signal?: AbortSignal,
): Promise<SessionSchedulesResponse> {
  return client.json<SessionSchedulesResponse>(`/v1/sessions/${encodeURIComponent(sessionId)}/schedules`, {
    method: "POST",
    body,
    signal,
  });
}

export function updateSessionSchedule(
  client: ApiClient,
  sessionId: string,
  scheduleId: string,
  body: SessionScheduleUpdateRequest,
  signal?: AbortSignal,
): Promise<SessionSchedulesResponse> {
  return client.json<SessionSchedulesResponse>(
    `/v1/sessions/${encodeURIComponent(sessionId)}/schedules/${encodeURIComponent(scheduleId)}`,
    { method: "PATCH", body, signal },
  );
}

export function deleteSessionSchedule(
  client: ApiClient,
  sessionId: string,
  scheduleId: string,
  signal?: AbortSignal,
): Promise<SessionScheduleDeleteResponse> {
  return client.json<SessionScheduleDeleteResponse>(
    `/v1/sessions/${encodeURIComponent(sessionId)}/schedules/${encodeURIComponent(scheduleId)}`,
    { method: "DELETE", signal },
  );
}

export function listSkills(
  client: ApiClient,
  signal?: AbortSignal,
): Promise<SessionSkillsResponse> {
  return client.json<SessionSkillsResponse>("/v1/skills", { signal });
}

export function readSkill(
  client: ApiClient,
  name: string,
  signal?: AbortSignal,
): Promise<SessionSkillResponse> {
  return client.json<SessionSkillResponse>(`/v1/skills/${encodeURIComponent(name)}`, { signal });
}

export function installSkill(
  client: ApiClient,
  body: SessionSkillInstallRequest,
  signal?: AbortSignal,
): Promise<SessionSkillResponse> {
  return client.json<SessionSkillResponse>("/v1/skills", {
    method: "POST",
    body,
    signal,
  });
}

export function deleteSkill(
  client: ApiClient,
  name: string,
  signal?: AbortSignal,
): Promise<SessionSkillDeleteResponse> {
  return client.json<SessionSkillDeleteResponse>(`/v1/skills/${encodeURIComponent(name)}`, {
    method: "DELETE",
    signal,
  });
}

export function createSession(
  client: ApiClient,
  body: SessionCreateRequest = {},
  signal?: AbortSignal,
): Promise<SessionDetailResponse> {
  return client.json<SessionDetailResponse>("/v1/sessions", { method: "POST", body, signal });
}

export function deleteSession(
  client: ApiClient,
  sessionId: string,
  signal?: AbortSignal,
): Promise<void> {
  return client.json<void>(`/v1/sessions/${encodeURIComponent(sessionId)}`, { method: "DELETE", signal });
}

export function getSessionHistory(
  client: ApiClient,
  sessionId: string,
  opts: SessionHistoryOptions = {},
): Promise<SessionHistoryResponse> {
  const q = new URLSearchParams();
  if (opts.after != null) q.set("after", String(opts.after));
  if (opts.limit != null) q.set("limit", String(opts.limit));
  return client.json<SessionHistoryResponse>(
    withQuery(`/v1/sessions/${encodeURIComponent(sessionId)}/history`, q),
    { signal: opts.signal },
  );
}

export function sendSessionMessage(
  client: ApiClient,
  sessionId: string,
  body: SessionMessageRequest,
  signal?: AbortSignal,
): Promise<SessionMessageResponse> {
  return client.json<SessionMessageResponse>(`/v1/sessions/${encodeURIComponent(sessionId)}/messages`, {
    method: "POST",
    body,
    signal,
  });
}

export function runSessionCommand(
  client: ApiClient,
  sessionId: string,
  body: SessionCommandRequest,
  signal?: AbortSignal,
): Promise<SessionCommandResponse> {
  return client.json<SessionCommandResponse>(`/v1/sessions/${encodeURIComponent(sessionId)}/commands`, {
    method: "POST",
    body,
    signal,
  });
}

export function readSessionFile(
  client: ApiClient,
  sessionId: string,
  opts: SessionFileReadOptions = {},
): Promise<SessionFileResponse> {
  const q = new URLSearchParams();
  if (opts.path) q.set("path", opts.path);
  if (opts.offset != null) q.set("offset", String(opts.offset));
  if (opts.limit != null) q.set("limit", String(opts.limit));
  return client.json<SessionFileResponse>(
    withQuery(`/v1/sessions/${encodeURIComponent(sessionId)}/files`, q),
    { signal: opts.signal },
  );
}

export function cancelSessionTurn(
  client: ApiClient,
  sessionId: string,
  signal?: AbortSignal,
): Promise<SessionCancelResponse> {
  return client.json<SessionCancelResponse>(`/v1/sessions/${encodeURIComponent(sessionId)}/cancel`, {
    method: "POST",
    signal,
  });
}

export function streamSessionEvents(
  client: ApiClient,
  sessionId: string,
  options: StreamEventsOptions,
): Promise<void> {
  return client.streamEvents(`/v1/sessions/${encodeURIComponent(sessionId)}/events`, options);
}

export async function readSessionArtifact(
  client: ApiClient,
  sessionId: string,
  artifactPath: string,
  opts: ArtifactReadOptions = {},
): Promise<ArtifactChunk> {
  const q = new URLSearchParams();
  if (opts.offset != null) q.set("offset", String(opts.offset));
  if (opts.limit != null) q.set("limit", String(opts.limit));
  const resp = await client.raw(
    withQuery(sessionArtifactPath(sessionId, artifactPath), q),
    { signal: opts.signal, accept: "application/octet-stream" },
  );
  const text = await resp.text();
  const path = resp.headers.get("X-Affent-Artifact-Path") ?? artifactPath;
  const bytes = readIntHeader(resp, "X-Affent-Artifact-Bytes", text.length);
  const offset = readIntHeader(resp, "X-Affent-Artifact-Offset", opts.offset ?? 0);
  return {
    path,
    bytes,
    offset,
    text,
    hasMore: offset + text.length < bytes,
  };
}

export function sessionArtifactPath(sessionId: string, artifactPath: string): string {
  return `/v1/sessions/${encodeURIComponent(sessionId)}/artifacts/${artifactUrlPath(artifactPath)}`;
}

function withQuery(path: string, q: URLSearchParams): string {
  const s = q.toString();
  return s === "" ? path : `${path}?${s}`;
}

function artifactUrlPath(path: string): string {
  return path.split("/").map(encodeURIComponent).join("/");
}

function readIntHeader(resp: Response, header: string, fallback: number): number {
  const raw = resp.headers.get(header);
  if (!raw) return fallback;
  const parsed = Number.parseInt(raw, 10);
  return Number.isFinite(parsed) ? parsed : fallback;
}
