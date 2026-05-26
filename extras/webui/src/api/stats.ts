import type { ApiClient } from "./client";

export interface ServerStatsResponse {
  listen?: string;
  model?: string;
  active_sessions?: number;
  running_turns?: number;
  executor_mode?: string;
  enable_browser?: boolean;
  enable_web?: boolean;
  enable_web_search?: boolean;
  enable_memory?: boolean;
  enable_builtins?: boolean;
  enable_subagent?: boolean;
  enable_focused_tasks?: boolean;
  eval_mode?: boolean;
  eval_tools?: string;
  eval_all_tools?: boolean;
  shutting_down?: boolean;
  workspace_root?: string;
  memory_root?: string;
  browser_cache_dir?: string;
  web_search_backend?: string;
  server_time?: string;
}

export function getServerStats(client: ApiClient, signal?: AbortSignal): Promise<ServerStatsResponse> {
  return client.json<ServerStatsResponse>("/v1/stats", { signal });
}
