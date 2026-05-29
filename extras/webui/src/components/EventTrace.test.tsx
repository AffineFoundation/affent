import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { normalizeEvents } from "../normalize/normalizeEvent";
import { EventTrace } from "./EventTrace";

describe("EventTrace", () => {
  it("keeps schema metadata out of the main event list while preserving raw payloads", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    const events = normalizeEvents([
      { id: 0, type: "trace.meta", data: { schema_version: 1 } },
      {
        id: 1,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "read_file",
          args: { path: "README.md" },
        },
      },
    ]);

    render(<EventTrace events={events} />);

    expect(screen.getByText("2 trace entries")).toBeInTheDocument();
    expect(screen.getByText("Metadata")).toBeInTheDocument();
    expect(screen.getByText("schema v1")).toBeInTheDocument();
    expect(screen.queryByText("Trace loaded")).not.toBeInTheDocument();
    expect(screen.getByText("Started action")).toBeInTheDocument();
    expect(screen.getByText("Request 1 · read_file")).toBeInTheDocument();
    expect(screen.queryByText(/turn t1/)).not.toBeInTheDocument();
    expect(screen.queryByText(/call c1/)).not.toBeInTheDocument();
    expect(screen.queryByText("tool.request")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Copy trace" }));
    expect(writeText).toHaveBeenCalledWith(`${JSON.stringify(events[0].raw)}\n${JSON.stringify(events[1].raw)}`);
    await user.click(screen.getByText("Metadata"));
    expect(screen.getByText("1 entry")).toBeInTheDocument();
    expect(screen.getByText(/"type": "trace.meta"/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Copy metadata" }));
    expect(writeText).toHaveBeenCalledWith(JSON.stringify([events[0].raw], null, 2));
  });

  it("summarizes tool results without requiring users to read raw event types", () => {
    const events = normalizeEvents([
      {
        id: 5,
        type: "tool.result",
        data: {
          call_id: "c1",
          exit_code: 0,
          duration_ms: 1250,
          result_summary: "Updated extras/webui/src/components/EventTrace.tsx",
          result: "Updated extras/webui/src/components/EventTrace.tsx",
          result_truncated: true,
          result_artifact_path: ".affent/artifacts/c1.txt",
        },
      },
    ]);

    render(<EventTrace events={events} />);

    expect(screen.getByText("Action finished")).toBeInTheDocument();
    expect(screen.getByText("1.3 s · Updated extras/webui/src/components/EventTrace.tsx · artifact c1.txt")).toBeInTheDocument();
    expect(screen.getByText("truncated")).toBeInTheDocument();
    expect(screen.getByText("full output")).toBeInTheDocument();
    expect(screen.queryByText("tool.result")).not.toBeInTheDocument();
  });

  it("opens tool result artifacts from expanded trace rows", async () => {
    const user = userEvent.setup();
    const onOpenArtifact = vi.fn();
    const events = normalizeEvents([
      {
        id: 5,
        type: "tool.result",
        data: {
          call_id: "c1",
          exit_code: 0,
          result_summary: "Saved full command output",
          result_truncated: true,
          result_artifact_path: ".affent/artifacts/tool-results/000001-c1.txt",
        },
      },
    ]);

    render(<EventTrace events={events} onOpenArtifact={onOpenArtifact} />);

    await user.click(screen.getByText("Action finished"));
    await user.click(screen.getByRole("button", { name: "Open artifact" }));

    expect(onOpenArtifact).toHaveBeenCalledWith(".affent/artifacts/tool-results/000001-c1.txt");
  });

  it("surfaces structured tool failure kinds in result rows", () => {
    const events = normalizeEvents([
      {
        id: 5,
        type: "tool.result",
        data: {
          call_id: "c1",
          exit_code: 0,
          failure_kind: "dynamic_shell",
          failure_kinds: ["dynamic_shell", "no_verified_source"],
          result_summary: "Only a dynamic shell was captured.\nNext: retry with browser network evidence\nFailure: kind=dynamic_shell",
          result: "Only a dynamic shell was captured.\nNext: retry with browser network evidence\nFailure: kind=dynamic_shell",
          result_truncated: false,
        },
      },
    ]);

    render(<EventTrace events={events} />);

    expect(screen.getByText("Action failed")).toBeInTheDocument();
    expect(screen.getByText(/next retry with browser network evidence/)).toBeInTheDocument();
    expect(screen.getByText("dynamic_shell")).toBeInTheDocument();
    expect(screen.getByText("no_verified_source")).toBeInTheDocument();
  });

  it("surfaces failed tool Next guidance in result rows", () => {
    const events = normalizeEvents([
      {
        id: 1,
        type: "tool.request",
        data: { turn_id: "t1", call_id: "c1", tool: "read_file" },
      },
      {
        id: 2,
        type: "tool.result",
        data: {
          call_id: "c1",
          exit_code: 1,
          failure_kind: "not_found",
          result_summary: "read failed\nNext: check the path from rg --files before retrying\nFailure: kind=not_found",
        },
      },
    ]);

    render(<EventTrace events={events} />);

    expect(screen.getByText("Action failed")).toBeInTheDocument();
    expect(screen.getByText(/next check the path from rg --files before retrying/)).toBeInTheDocument();
  });

  it("surfaces source evidence status in tool result rows", () => {
    const events = normalizeEvents([
      {
        id: 1,
        type: "tool.request",
        data: { turn_id: "t1", call_id: "c1", tool: "browser_network_read" },
      },
      {
        id: 2,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "c1",
          exit_code: 0,
          result_summary: "SourceAccess: browser_network_url=https://taostats.io/api/subnets/120; requested_url=https://taostats.io/subnets/120; ref=n1; status=200; content_type=application/json; source_method=network_xhr_fetch",
          result: "SourceAccess: browser_network_url=https://taostats.io/api/subnets/120; requested_url=https://taostats.io/subnets/120; ref=n1; status=200; content_type=application/json; source_method=network_xhr_fetch\n{\"price\":\"0.06342 T\"}",
          result_truncated: false,
        },
      },
    ]);

    render(<EventTrace events={events} />);

    expect(screen.getByText("Action finished")).toBeInTheDocument();
    expect(screen.getByText("browser_network_read · network source · https://taostats.io/api/subnets/120 · from https://taostats.io/subnets/120 · ref n1 · http 200 · application/json · preview {\"price\":\"0.06342 T\"}")).toBeInTheDocument();
    expect(screen.getByText("network")).toBeInTheDocument();
  });

  it("groups request lifecycle events into one readable record", async () => {
    const user = userEvent.setup();
    const raws = [
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "summarize the repo" } },
      { id: 3, type: "usage", data: { turn_id: "t1", input_tokens: 12, output_tokens: 5 } },
      { id: 4, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ];

    render(<EventTrace events={normalizeEvents(raws)} />);

    expect(screen.getByText("1-4")).toBeInTheDocument();
    expect(screen.getByText("Request trace")).toBeInTheDocument();
    expect(screen.getByText("Request 1 · summarize the repo · completed · 17 tokens")).toBeInTheDocument();
    expect(screen.queryByText("Started request")).not.toBeInTheDocument();
    expect(screen.queryByText("User message")).not.toBeInTheDocument();
    expect(screen.queryByText("Token usage")).not.toBeInTheDocument();
    expect(screen.queryByText("Request finished")).not.toBeInTheDocument();

    await user.click(screen.getByText("Request trace"));
    await user.click(screen.getByRole("button", { name: "Copy events" }));

    expect(screen.getByText("4 events")).toBeInTheDocument();
    expect(screen.getByText(/"type": "turn.start"/)).toBeInTheDocument();
    expect(screen.getByText(/"turn_id": "t1"/)).toBeInTheDocument();
    expect(screen.getByText(/"type": "turn.end"/)).toBeInTheDocument();
  });

  it("surfaces scheduled request origin in collapsed request records", () => {
    const raws = [
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      {
        id: 2,
        type: "user.message",
        data: {
          turn_id: "t1",
          source: "schedule",
          schedule_id: "sched_1",
          schedule_kind: "loop_tick",
          text: "Internal scheduled loop prompt with protocol instructions",
          display_text: "Loop every 30m: market monitor",
        },
      },
      { id: 3, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ];

    render(<EventTrace events={normalizeEvents(raws)} />);

    expect(screen.getByText("Request trace")).toBeInTheDocument();
    expect(screen.getByText("Request 1 · loop tick sched_1 · Loop every 30m: market monitor · completed")).toBeInTheDocument();
    expect(screen.queryByText(/Internal scheduled loop prompt/)).toBeNull();
    expect(screen.getByText("scheduled")).toBeInTheDocument();
    expect(screen.getByText("loop_tick")).toBeInTheDocument();
    expect(screen.getByText("sched_1")).toBeInTheDocument();
  });

  it("labels standalone scheduled user messages", () => {
    const raws = [
      {
        id: 1,
        type: "user.message",
        data: {
          turn_id: "t1",
          source: "schedule",
          schedule_id: "sched_1",
          text: "Internal daily check-in prompt",
          display_text: "Daily check-in: runtime",
        },
      },
    ];

    render(<EventTrace events={normalizeEvents(raws)} />);

    expect(screen.getByText("Scheduled message")).toBeInTheDocument();
    expect(screen.getByText("Request 1 · timer sched_1 · Daily check-in: runtime")).toBeInTheDocument();
    expect(screen.getByText("scheduled")).toBeInTheDocument();
    expect(screen.getByText("sched_1")).toBeInTheDocument();
  });

  it("surfaces guard and memory update counters in collapsed request records", () => {
    const raws = [
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "recover repeated browser failures" } },
      {
        id: 3,
        type: "turn.end",
        data: {
          turn_id: "t1",
          reason: "max_turns",
          tool_stats: {
            tool_requests: 4,
            tool_requests_admitted: 3,
            tool_requests_skipped: 1,
            tool_errors: 1,
            loop_guard_interventions: 2,
            forced_no_tools: 1,
            memory_updates: 2,
            memory_update_add: 1,
            memory_update_replace: 1,
          },
        },
      },
    ];

    render(<EventTrace events={normalizeEvents(raws)} />);

    expect(screen.getByText("Request trace")).toBeInTheDocument();
    expect(screen.getByText("Request 1 · recover repeated browser failures · max_turns · 4 actions · 3 admitted / 1 skipped · 1 failed · Guard 2 · 1 no-tools · 2 memory updates (1 add, 1 replace)")).toBeInTheDocument();
    expect(screen.queryByText(/"loop_guard_interventions"/)).not.toBeInTheDocument();
  });

  it("marks unknown events without dropping their payload", async () => {
    const user = userEvent.setup();
    const events = normalizeEvents([{ id: 99, type: "future.event", data: { turn_id: "t1", payload: "kept" } }]);

    render(<EventTrace events={events} />);

    expect(screen.getByText("unclassified")).toBeInTheDocument();
    await user.click(screen.getByText("future.event"));
    expect(screen.getByText(/"payload": "kept"/)).toBeInTheDocument();
  });

  it("copies a single raw event JSON payload", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    const raw = { id: 2, type: "turn.end", data: { turn_id: "t1", reason: "completed" } };

    render(<EventTrace events={normalizeEvents([raw])} />);

    await user.click(screen.getByText("Request finished"));
    await user.click(screen.getByRole("button", { name: "Copy event" }));

    expect(writeText).toHaveBeenCalledWith(JSON.stringify(raw, null, 2));
    expect(screen.getByText(/"type": "turn.end"/)).toBeInTheDocument();
    expect(within(screen.getByTestId("event-trace")).getByRole("button", { name: "Copied" })).toBeInTheDocument();
  });

  it("groups message delta chunks for a turn into one readable stream row", async () => {
    const user = userEvent.setup();
    const events = normalizeEvents([
      { id: 1, type: "message.delta", data: { turn_id: "t1", delta: "Hel" } },
      { id: 2, type: "message.delta", data: { turn_id: "t1", delta: "lo" } },
      { id: 3, type: "thinking.delta", data: { turn_id: "t1", delta: "Plan" } },
      { id: 4, type: "message.done", data: { turn_id: "t1", text: "Hello" } },
    ]);

    render(<EventTrace events={events} />);

    expect(screen.getByText("1-4")).toBeInTheDocument();
    expect(screen.getByText("Assistant output")).toBeInTheDocument();
    expect(screen.getByText("Request 1 · Hello")).toBeInTheDocument();
    expect(screen.queryByText("message.delta")).not.toBeInTheDocument();
    expect(screen.queryByText("Assistant answer saved")).not.toBeInTheDocument();
    expect(screen.queryByText("2 updates · 5 chars")).not.toBeInTheDocument();

    await user.click(screen.getByText("Assistant output"));

    expect(screen.getByText("Hello")).toBeInTheDocument();
    expect(screen.getByText("2 updates · 5 chars")).toBeInTheDocument();
    expect(screen.getByText("3 trace entries")).toBeInTheDocument();
    expect(screen.getByText(/"type": "message.done"/)).toBeInTheDocument();
    expect(screen.getByText("Thinking notes")).toBeInTheDocument();
  });

  it("keeps interleaved message deltas in one stream summary", async () => {
    const user = userEvent.setup();
    const events = normalizeEvents([
      { id: 1, type: "message.delta", data: { turn_id: "t1", delta: "Hel" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "c1", tool: "read_file" } },
      { id: 3, type: "message.delta", data: { turn_id: "t1", delta: "lo" } },
      { id: 4, type: "message.delta", data: { turn_id: "t2", delta: "Next" } },
    ]);

    render(<EventTrace events={events} />);

    expect(screen.getAllByText("Assistant output")).toHaveLength(2);
    expect(screen.getByText("1-3")).toBeInTheDocument();
    expect(screen.getByText("4")).toBeInTheDocument();
    expect(screen.getByText("Request 1 · Hello")).toBeInTheDocument();
    expect(screen.getByText("Started action")).toBeInTheDocument();
    expect(screen.queryByText("tool.request")).not.toBeInTheDocument();

    await user.click(screen.getAllByText("Assistant output")[0]);

    expect(screen.getByText("Hello")).toBeInTheDocument();
    expect(screen.getByText("2 trace entries")).toBeInTheDocument();
  });

  it("copies grouped delta raw event JSON payloads", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    const raws = [
      { id: 8, type: "message.delta", data: { turn_id: "t1", delta: "A" } },
      { id: 9, type: "message.delta", data: { turn_id: "t1", delta: "B" } },
      { id: 10, type: "message.done", data: { turn_id: "t1", text: "AB", finish_reason: "stop" } },
    ];

    render(<EventTrace events={normalizeEvents(raws)} />);

    await user.click(screen.getByText("Assistant output"));
    await user.click(screen.getByRole("button", { name: "Copy events" }));

    expect(writeText).toHaveBeenCalledWith(JSON.stringify(raws, null, 2));
    expect(within(screen.getByTestId("event-trace")).getByRole("button", { name: "Copied" })).toBeInTheDocument();
  });
});
