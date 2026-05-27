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
    expect(panel).toHaveTextContent("Activation review");
    expect(panel).toHaveTextContent("Calibration recorded; ready for activation review");
    expect(screen.getByTestId("session-loop-checklist")).toHaveTextContent("LOOP.md exists but is not running yet");
    expect(screen.getByTestId("session-loop-checklist")).toHaveTextContent("A calibration answer is recorded");
    expect(screen.getByTestId("session-loop-checklist")).toHaveTextContent("Current Situation <= 1200 chars");
    expect(panel).toHaveTextContent("Setup question");
    expect(panel).toHaveTextContent("What should pause this loop?");
    expect(panel).toHaveTextContent("Calibration");
    expect(panel).toHaveTextContent("1 calibration answer");
    expect(panel).toHaveTextContent("Stop when source evidence is weak.");
    expect(screen.getByTestId("session-loop-next")).toHaveTextContent("Review and activate in chat");
    expect(screen.getByRole("button", { name: "Review in chat" })).toBeInTheDocument();
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
