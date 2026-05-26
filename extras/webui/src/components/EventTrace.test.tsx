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
