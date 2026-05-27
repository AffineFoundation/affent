import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { RuntimeStatsPanel } from "./RuntimeStatsPanel";

describe("RuntimeStatsPanel", () => {
  it("summarizes runtime tool surface and long-run diagnostics", () => {
    render(
      <RuntimeStatsPanel
        defaultOpen
        stats={{
          model: "qwen-small",
          active_sessions: 3,
          running_turns: 1,
          executor_mode: "local",
          enable_builtins: true,
          enable_web: true,
          enable_browser: true,
          enable_memory: true,
          shared_user_memory: true,
          enable_subagent: true,
          enable_focused_tasks: true,
          eval_mode: true,
          eval_tools: "workspace,recall",
          aggregate: {
            blocked_by_type: 0,
            blocked_by_domain: 0,
            cache_hit: 4,
            cache_miss: 2,
            network_fetch: 3,
            input_tokens: 3200,
            output_tokens: 800,
            turns: 5,
            tools: {
              tool_requests: 9,
              tool_errors: 1,
              loop_guard_interventions: 2,
              forced_no_tools: 1,
              source_access_results: 3,
              source_access_verified: 2,
              source_access_network: 1,
              source_access_dynamic_partial: 1,
              memory_updates: 2,
              memory_update_add: 1,
              memory_update_replace: 1,
              session_search_calls: 1,
              session_search_results: 2,
              session_search_context_hits: 1,
              session_search_matched_terms: 3,
              tool_context_truncated: 2,
              tool_context_omitted_bytes: 3072,
            },
            runtime: {
              turn_end_by_reason: { completed: 4, max_turns: 1 },
              runtime_errors: 1,
              context_compactions: 1,
              context_compactions_reactive: 1,
              context_compaction_removed_messages: 72,
              context_compaction_summary_bytes: 4096,
              context_compaction_summary_missing: 1,
              context_compaction_summary_empty: 1,
            },
          },
        }}
      />,
    );

    const panel = screen.getByTestId("runtime-stats-panel");
    expect(panel).toHaveTextContent("qwen-small");
    expect(panel).toHaveTextContent("3 sessions · 1 running · eval · workspace,recall · executor local");
    expect(screen.getByTestId("runtime-stats-grid")).toHaveTextContent("Evidence2/3 verified · 1 network · 1 partial");
    expect(screen.getByTestId("runtime-stats-grid")).toHaveTextContent("memory (shared user)");
    expect(screen.getByTestId("runtime-stats-grid")).toHaveTextContent("Recall2 hits · 1 context · 3 terms");
    expect(screen.getByTestId("runtime-stats-grid")).toHaveTextContent("Context1 compaction · 1 reactive · -72 msgs · 4 KiB summary · 1 missing · 1 empty");
    expect(screen.getByTestId("runtime-stats-grid")).toHaveTextContent("Tool context2 trims · 3 KiB omitted");
    expect(screen.getByTestId("runtime-stats-grid")).toHaveTextContent("Loop1 max-turn · 2 guards · 1 no-tools");
    expect(screen.getByTestId("runtime-stats-grid")).toHaveTextContent("Errors1 tool · 1 runtime");
  });

  it("shows loading and error states without fake metrics", () => {
    const { rerender } = render(<RuntimeStatsPanel defaultOpen loading />);

    expect(screen.getByTestId("runtime-stats-panel")).toHaveTextContent("Loading runtime");
    expect(screen.queryByTestId("runtime-stats-grid")).toBeNull();

    rerender(<RuntimeStatsPanel defaultOpen error="stats offline" />);

    expect(screen.getByTestId("runtime-stats-panel")).toHaveTextContent("Runtime unavailable");
    expect(screen.getByRole("alert")).toHaveTextContent("stats offline");
    expect(screen.queryByTestId("runtime-stats-grid")).toBeNull();
  });

  it("keeps standard idle runtime from inventing ready diagnostics", () => {
    render(<RuntimeStatsPanel defaultOpen stats={{ model: "qwen-small", active_sessions: 0, running_turns: 0 }} />);

    const panel = screen.getByTestId("runtime-stats-panel");
    expect(panel).toHaveTextContent("qwen-small");
    expect(panel).toHaveTextContent("No runtime diagnostics for this chat.");
    expect(screen.queryByTestId("runtime-stats-grid")).toBeNull();
    expect(panel).not.toHaveTextContent("Mode");
    expect(panel).not.toHaveTextContent("Tools");
    expect(panel).not.toHaveTextContent("standard");
  });

  it("shows browser policy blocks that promote Runtime into Workbench", () => {
    render(
      <RuntimeStatsPanel
        defaultOpen
        stats={{
          model: "qwen-small",
          active_sessions: 0,
          running_turns: 0,
          aggregate: {
            blocked_by_type: 2,
            blocked_by_domain: 1,
            domain_relaxations: 1,
            cache_hit: 0,
            cache_miss: 0,
            network_fetch: 0,
            input_tokens: 0,
            output_tokens: 0,
            turns: 0,
            tools: { tool_requests: 0, tool_errors: 0 },
            runtime: { runtime_errors: 0 },
          },
        }}
      />,
    );

    const grid = screen.getByTestId("runtime-stats-grid");
    expect(grid).toHaveTextContent("Browser1 domain blocks · 2 type blocks · 1 relaxations");
    expect(within(grid).getByText("Browser").closest(".session-tools-runtime-chip")).toHaveAttribute("data-tone", "warning");
  });

  it("surfaces a compact API diagnostic in the collapsed summary", () => {
    const diagnostic = "API route /v1/stats returned the WebUI app shell. The affentserve build may not expose this route. Use the current affentserve build.";
    render(<RuntimeStatsPanel error={diagnostic} />);

    const summary = within(screen.getByTestId("runtime-stats-panel")).getByText("Runtime unavailable").closest("summary");
    expect(summary).toHaveTextContent("Stats API failed: API route /v1/stats returned the WebUI app shell.");
    expect(summary).not.toHaveTextContent("Use the current affentserve build");
  });
});
