import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { SessionLoopPanel } from "./SessionLoopPanel";

describe("SessionLoopPanel", () => {
  it("shows the calibration-first activation flow before a loop is running", () => {
    render(<SessionLoopPanel defaultGoal="long running subnet analysis" defaultOpen />);

    const panel = screen.getByTestId("session-loop-panel");
    expect(panel).toHaveTextContent("Set up long-running work only when this chat needs it");
    expect(screen.getByRole("button", { name: "Start setup" })).toBeInTheDocument();
    expect(panel).toHaveTextContent("asks one calibration question");
  });

  it("keeps loop controls fully folded by default", () => {
    render(<SessionLoopPanel defaultGoal="long running subnet analysis" />);

    expect(screen.getByTestId("session-loop-panel")).not.toHaveAttribute("open");
  });

  it("can render inside the unified automation surface without a nested disclosure", () => {
    render(<SessionLoopPanel defaultGoal="long running subnet analysis" embedded />);

    const panel = screen.getByTestId("session-loop-panel");
    expect(panel.tagName).toBe("SECTION");
    expect(panel).toHaveAccessibleName("Loop");
    expect(panel).toHaveTextContent("Set up long-running work only when this chat needs it");
    expect(panel).not.toHaveAttribute("open");
  });

  it("uses a compact embedded running state for Workbench automation", () => {
    render(
      <SessionLoopPanel
        embedded
        summary={{ path: ".affent/loops/loop-1/LOOP.md", status: "running", bytes: 512 }}
        state={{
          version: 1,
          loop_id: "loop-1",
          status: "running",
          initial_goal_preview: "watch market evidence for several days",
        }}
        onUseAsDraft={() => undefined}
      />,
    );

    const panel = screen.getByTestId("session-loop-panel");
    expect(panel).toHaveTextContent("Loop");
    expect(panel).toHaveTextContent("Loop running");
    expect(panel).not.toHaveTextContent("Running protocol");
    expect(panel).not.toHaveTextContent(".affent/loops/loop-1/LOOP.md");
    expect(screen.queryByTestId("session-loop-progress")).toBeNull();
    expect(screen.queryByTestId("session-loop-next")).toBeNull();
    expect(screen.getByTestId("session-loop-runtimebar")).toHaveTextContent("Goal: watch market evidence for several days");
    expect(screen.getByRole("button", { name: "Update LOOP.md" })).toBeInTheDocument();
  });

  it("puts the pending calibration question next to the chat action", () => {
    render(
      <SessionLoopPanel
        summary={{ path: ".affent/loops/loop-draft/LOOP.md", status: "draft", bytes: 128 }}
        state={{
          version: 1,
          loop_id: "loop-draft",
          status: "draft",
          initial_goal_preview: "long running market check",
          calibration_questions: 1,
          last_calibration_question_preview: "What should pause this loop?",
          calibration_answers: 0,
        }}
        onUseAsDraft={() => undefined}
        defaultOpen
      />,
    );

    const next = screen.getByTestId("session-loop-next");
    expect(next).toHaveTextContent("Answer the setup question");
    expect(next).toHaveTextContent("What should pause this loop?");
    expect(screen.getByRole("button", { name: "Open answer draft" })).toBeInTheDocument();
  });

  it("labels draft protocol maintenance as answering setup in chat", () => {
    render(
      <SessionLoopPanel
        summary={{ path: ".affent/loops/loop-draft/LOOP.md", status: "draft", bytes: 128 }}
        state={{
          version: 1,
          loop_id: "loop-draft",
          status: "draft",
          initial_goal_preview: "long running market check",
          calibration_questions: 1,
          last_calibration_question_preview: "What should pause this loop?",
          calibration_answers: 1,
          last_calibration_answer_preview: "Stop when source evidence is weak.",
        }}
        onUseAsDraft={() => undefined}
        defaultOpen
      />,
    );

    const panel = screen.getByTestId("session-loop-panel");
    expect(panel).toHaveTextContent("Calibration answer recorded");
    expect(screen.queryByTestId("session-loop-callout")).toBeNull();
    expect(screen.getByTestId("session-loop-checklist")).toHaveTextContent("LOOP.md exists but is not running yet");
    expect(screen.getByTestId("session-loop-checklist")).toHaveTextContent("A calibration answer is recorded");
    expect(screen.getByTestId("session-loop-checklist")).toHaveTextContent("Current Situation <= 1200 chars");
    expect(panel).toHaveTextContent("Setup answer");
    expect(panel).toHaveTextContent("1 calibration answer");
    expect(panel).toHaveTextContent("Stop when source evidence is weak.");
    expect(panel).not.toHaveTextContent("Setup questionWhat should pause this loop?");
    expect(screen.getByTestId("session-loop-next")).toHaveTextContent("Activate LOOP.md in chat");
    expect(screen.getByRole("button", { name: "Open activation draft" })).toBeInTheDocument();
  });

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
        defaultOpen
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
        defaultOpen
      />,
    );

    expect(screen.getByTestId("session-loop-panel")).toHaveTextContent("2 memory updates");
  });

  it("surfaces context compaction state as loop recovery context", () => {
    render(
      <SessionLoopPanel
        summary={{ path: ".affent/loops/loop-compact/LOOP.md", status: "running", bytes: 512 }}
        state={{
          version: 1,
          loop_id: "loop-compact",
          status: "running",
          context_compactions: 3,
          last_context_compaction_reactive: true,
          last_context_compaction_reason: "context_overflow",
        }}
        defaultOpen
      />,
    );

    const panel = screen.getByTestId("session-loop-panel");
    expect(panel).toHaveTextContent("Compaction");
    expect(panel).toHaveTextContent("3 compactions");
    expect(panel).toHaveTextContent("reactive");
    expect(panel).toHaveTextContent("context_overflow");
  });

  it("keeps running loop counters out of the Workbench first screen", () => {
    render(
      <SessionLoopPanel
        embedded
        summary={{ path: ".affent/loops/loop-counters/LOOP.md", status: "running", bytes: 512 }}
        state={{
          version: 1,
          loop_id: "loop-counters",
          status: "running",
          initial_goal_preview: "watch market evidence",
          protocol_updates: 7,
          protocol_feeds: 9,
          loop_decisions: 4,
          event_count: 12,
          last_event_summary: "Updated LOOP.md",
          last_decision_kind: "evidence_quality",
          last_decision: "defer",
        }}
      />,
    );

    const panel = screen.getByTestId("session-loop-panel");
    expect(panel).toHaveTextContent("watch market evidence");
    expect(panel).not.toHaveTextContent("Feeds");
    expect(panel).not.toHaveTextContent("Updates");
    expect(panel).not.toHaveTextContent("Decision");
    expect(panel).not.toHaveTextContent("Latest");
    expect(panel).not.toHaveTextContent(".affent/loops/loop-counters/LOOP.md");
  });

  it("keeps recovery notes out of the embedded running first screen", () => {
    render(
      <SessionLoopPanel
        embedded
        summary={{ path: ".affent/loops/loop-compact/LOOP.md", status: "running", bytes: 512 }}
        state={{
          version: 1,
          loop_id: "loop-compact",
          status: "running",
          initial_goal_preview: "maintain automation reliability",
          memory_update_events: 2,
          last_memory_update_action: "replace",
          last_memory_update_topic: "automation",
          last_memory_update_preview: "Keep timer inspector visible on mobile.",
          context_compactions: 1,
          last_context_compaction_reason: "context_overflow",
        }}
        events={[{ seq: 1, time: "2026-05-30T00:00:00Z", type: "protocol_update", summary: "Updated LOOP.md" }]}
      />,
    );

    const panel = screen.getByTestId("session-loop-panel");
    expect(panel).toHaveTextContent("Goal: maintain automation reliability");
    expect(panel).not.toHaveTextContent("Memory");
    expect(panel).not.toHaveTextContent("Compaction");
    expect(screen.queryByTestId("session-loop-protocol-details")).toBeNull();
    expect(screen.queryByTestId("session-loop-events-details")).toBeNull();
  });

  it("renders recent loop protocol events returned with LOOP.md", () => {
    render(
      <SessionLoopPanel
        summary={{ path: ".affent/loops/loop-3/LOOP.md", status: "running", bytes: 768 }}
        state={{ version: 1, loop_id: "loop-3", status: "running" }}
        protocol="# Loop Protocol: loop-3"
        events={[
          {
            seq: 1,
            time: "2026-05-27T10:00:00Z",
            type: "loop.protocol_init",
            summary: "Initialized LOOP.md",
            reason: "loop protocol activation",
          },
          {
            seq: 2,
            time: "2026-05-27T10:05:00Z",
            type: "loop.protocol_calibration_request",
            summary: "Asked loop calibration question",
            calibration_preview: "What should pause this loop?",
          },
          {
            seq: 3,
            time: "2026-05-27T10:06:00Z",
            type: "loop.protocol_calibration",
            summary: "Recorded loop calibration answer",
            calibration_preview: "Pause when evidence is weak.",
          },
          {
            seq: 4,
            time: "2026-05-27T10:07:00Z",
            type: "loop.protocol_update",
            summary: "Updated LOOP.md",
            sections_changed: ["Current Situation", "Rules"],
            reason: "user clarified stop condition",
          },
          {
            seq: 5,
            time: "2026-05-27T10:10:00Z",
            type: "context.compacted",
            summary: "Context compacted; force next LOOP.md full feed",
            reason: "context_overflow",
            reactive: true,
          },
        ]}
        defaultOpen
      />,
    );

    const events = screen.getByTestId("session-loop-events");
    expect(events).toHaveTextContent("Context compacted");
    expect(events).toHaveTextContent("reactive");
    expect(events).toHaveTextContent("context_overflow");
    expect(events).toHaveTextContent("Updated LOOP.md");
    expect(events).toHaveTextContent("Current Situation, Rules");
    expect(events).toHaveTextContent("Recorded loop calibration answer");
    expect(events).toHaveTextContent("Pause when evidence is weak.");
    expect(events).toHaveTextContent("Asked loop calibration question");
    expect(events).toHaveTextContent("What should pause this loop?");
    const text = events.textContent ?? "";
    expect(text.indexOf("Context compacted")).toBeLessThan(text.indexOf("Initialized LOOP.md"));
  });
});
