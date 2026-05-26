// Faithful TypeScript mirror of cmd/affentserve/sessions_api.go and the
// snapshot types in sessions.go. Source of truth for the session-control
// surface: GET/POST /v1/sessions, GET/DELETE /v1/sessions/{id},
// plus account-level skill settings at /v1/skills.
//
// Kept in parity with the Go json tags; the parity guard covers this too.

import type { ApiClient } from "./client";
import type { SessionHistoryResponse } from "./events";
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
  tool_errors: number;
  tool_repair_succeeded: number;
  tool_repair_failed: number;
  source_access_results?: number;
  source_access_verified?: number;
  source_access_discovery_only?: number;
  source_access_network?: number;
  source_access_dynamic_partial?: number;
  memory_updates?: number;
  memory_update_add?: number;
  memory_update_replace?: number;
  memory_update_remove?: number;
}

export interface SessionToolInfo {
  name: string;
  raw_name?: string;
  description: string;
  parameters: unknown;
  group: string;
  source?: string;
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

export interface SessionSkillInstallRequest {
  name: string;
  description?: string;
  body: string;
  source?: string;
  triggers?: string[];
  required_tools?: string[];
}

export interface SessionCapabilities {
  eval_mode: boolean;
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
  capabilities?: SessionCapabilities;
  latest_user_message?: string;
  topic_user_message?: string;
  has_plan?: boolean;
  plan_summary?: SessionPlanSummary;
  has_conversation: boolean;
  has_events: boolean;
  has_artifacts: boolean;
  has_memory: boolean;
  has_runtime_skills: boolean;
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
    withQuery(`/v1/sessions/${encodeURIComponent(sessionId)}/artifacts/${artifactUrlPath(artifactPath)}`, q),
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
