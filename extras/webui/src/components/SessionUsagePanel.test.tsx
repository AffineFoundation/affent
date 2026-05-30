import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { reduceRawEvents } from "../store/reduce";
import { SessionUsagePanel } from "./SessionUsagePanel";

describe("SessionUsagePanel", () => {
  it("shows token totals, context policy, compactions, and cost breakdown", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "thinking.done", data: { turn_id: "t1", text: "inspect the failure and compare the workspace diff" } },
      { id: 3, type: "tool.request", data: { turn_id: "t1", call_id: "shell-1", tool: "shell", args: { command: "npm test" }, args_truncated: false, args_bytes: 24, args_omitted_bytes: 0, args_cap_bytes: 4096 } },
      { id: 4, type: "tool.result", data: { turn_id: "t1", call_id: "shell-1", exit_code: 0, result_summary: "ok", result: "ok", result_truncated: false, result_bytes: 2, result_omitted_bytes: 0, result_cap_bytes: 4096, context_estimated_tokens: 180 } },
    ]);

    render(
      <SessionUsagePanel
        hasSelectedSession
        session={session}
        usage={{
          totalTokens: 1540,
          inputTokens: 1200,
          outputTokens: 340,
          latestTurnInputTokens: 1200,
          latestTurnOutputTokens: 340,
          trend: [
            { label: "Turn 1", value: 1540, inputTokens: 1200, outputTokens: 340, valueLabel: "0.0015M tokens", detail: "t1" },
          ],
          items: [{ label: "Session tokens", value: "0.0015M tokens (0.0012M in / 0.0003M out)" }],
        }}
        contextSummary={{
          message_count: 96,
          compact_trigger: 120,
          compact_percent: 80,
          messages_until_compact: 24,
          estimated_conversation_tokens: 900,
          estimated_tool_schema_tokens: 220,
          tool_schema_budget_tokens: 300,
          model_context_window_tokens: 100000,
          model_context_window_source: "provider",
          reserved_output_tokens: 30000,
          compact_trigger_input_tokens: 70000,
          compact_trigger_input_percent: 80,
          request_input_tokens_until_compact: 4200,
        }}
        compactions={{
          count: 3,
          reactive: 2,
          removed_messages: 64,
          latest_reason: "context_overflow",
        }}
      />,
    );

    expect(screen.getByTestId("session-usage-panel")).toHaveTextContent("Session token usage");
    expect(screen.getByTestId("session-usage-panel")).toHaveTextContent("1,540");
    expect(screen.getByTestId("session-usage-panel")).toHaveTextContent("1,200");
    expect(screen.getByTestId("session-usage-panel")).toHaveTextContent("340");
    expect(screen.getByTestId("session-usage-panel")).toHaveTextContent("100k tokens");
    expect(screen.getByTestId("session-usage-panel")).toHaveTextContent("70k input · 80%");
    expect(screen.getByTestId("session-usage-panel")).toHaveTextContent("3");
    expect(screen.getByTestId("session-usage-panel")).toHaveTextContent("2 reactive");
    expect(screen.getByTestId("session-usage-panel")).toHaveTextContent("64 messages removed");
    expect(screen.getByTestId("session-usage-cost")).toHaveTextContent("Conversation input");
    expect(screen.getByTestId("session-usage-cost")).toHaveTextContent("Tool context");
    expect(screen.getByTestId("session-usage-cost")).toHaveTextContent("220 schema + 180 results");
    expect(screen.getByTestId("session-usage-cost")).toHaveTextContent("Thinking");
    expect(screen.getByTestId("session-usage-cost")).toHaveTextContent("estimated from saved thinking text");
  });
});
