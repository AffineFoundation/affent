import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { SessionLoopPanel } from "./SessionLoopPanel";

describe("SessionLoopPanel", () => {
  it("surfaces the latest loop memory update as recovery context", () => {
    render(
      <SessionLoopPanel
        summary={{
          path: ".affent/loops/loop-1/LOOP.md",
          status: "running",
          bytes: 512,
          preview: "Keep market evidence recoverable.",
        }}
        state={{
          version: 1,
          loop_id: "loop-1",
          status: "running",
          initial_goal_preview: "watch market evidence for several days",
          protocol_updates: 2,
          protocol_feeds: 3,
          memory_update_events: 4,
          last_memory_update_action: "replace",
          last_memory_update_target: "memory",
          last_memory_update_topic: "markets",
          last_memory_update_preview: "Market reports must include MEM-STOCK-73 and source-led confidence.",
        }}
      />,
    );

    const panel = screen.getByTestId("session-loop-panel");
    expect(panel).toHaveTextContent("Running");
    expect(panel).toHaveTextContent("Memory");
    expect(panel).toHaveTextContent("Replaced");
    expect(panel).toHaveTextContent("memory:markets");
    expect(panel).toHaveTextContent("MEM-STOCK-73");
  });

  it("falls back to memory update counts when only aggregate state is available", () => {
    render(
      <SessionLoopPanel
        summary={{ path: ".affent/loops/loop-2/LOOP.md", status: "running", bytes: 256 }}
        state={{ version: 1, loop_id: "loop-2", status: "running", memory_update_events: 2 }}
      />,
    );

    expect(screen.getByTestId("session-loop-panel")).toHaveTextContent("2 memory updates");
  });
});
