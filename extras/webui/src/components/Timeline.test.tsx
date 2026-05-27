import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { useRef } from "react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { completedTurn } from "../fixtures/completedTurn";
import { argsRepaired, completedSubagentTree, maxTurns, resultTruncated, runningSubagent, toolError, turnError } from "../fixtures/scenarios";
import { reduceRawEvents } from "../store/reduce";
import type { RawEvent } from "../api/events";
import { Timeline } from "./Timeline";

function renderTimeline(
  raws: RawEvent[],
  sessionId?: string,
  onOpenArtifact?: (path: string) => void,
  onUseAsDraft?: (content: string, source?: string) => void,
) {
  const session = reduceRawEvents(raws);
  return render(<Timeline session={session} sessionId={sessionId} onOpenArtifact={onOpenArtifact} onUseAsDraft={onUseAsDraft} />);
}

async function openMessageOptions(user: ReturnType<typeof userEvent.setup>, scope: HTMLElement) {
  await user.click(within(scope).getByRole("button", { name: "Message options" }));
}

describe("Timeline", () => {
  it("renders the assistant answer with folded completed work details", async () => {
    const user = userEvent.setup();
    renderTimeline(completedTurn);
    expect(screen.getByTestId("msg-assistant")).toHaveTextContent("There are two files.");
    expect(screen.getByTestId("agent-activity")).toHaveTextContent("What Affent did");
    expect(screen.getByTestId("agent-activity-digest")).toHaveTextContent("Result");
    expect(screen.getByTestId("agent-activity-digest")).toHaveTextContent("README.md main.go");
    expect(screen.getByTestId("agent-activity-digest")).not.toHaveTextContent("138 tokens");
    expect(screen.getByTestId("agent-activity").textContent?.match(/README\.md main\.go/g)).toHaveLength(1);
    expect(screen.queryByTestId("agent-activity-brief")).toBeNull();
    expect(screen.getByRole("button", { name: /What Affent did/ })).toHaveAttribute("aria-expanded", "false");
    await user.click(screen.getByRole("button", { name: /What Affent did/ }));
    expect(screen.queryByTestId("agent-activity-brief")).toBeNull();
    expect(screen.getByTestId("agent-activity")).not.toHaveTextContent("Goallist the files");
    expect(screen.getByTestId("agent-activity")).not.toHaveTextContent("Working plan");
    expect(screen.getByTestId("agent-activity")).toHaveTextContent("List current directory");
    expect(screen.queryByTestId("execution-tree")).toBeNull();
    await user.click(screen.getByRole("button", { name: /Run summary/ }));
    expect(screen.getByTestId("execution-tree")).toHaveTextContent("List current directory");
    const toolDetails = screen.getByRole("button", { name: /Run summary/ });
    expect(toolDetails).toHaveTextContent("Run summary");
    expect(toolDetails).not.toHaveTextContent("1 completed action");
    expect(toolDetails).toHaveAccessibleName("Run summary · List files: . · 12ms");
    const visibleWorkDetails = toolDetails.textContent?.replace(/\s+/g, " ").trim() ?? "";
    expect(visibleWorkDetails).toContain("Run summary");
    expect(visibleWorkDetails).toContain("List files: .");
    expect(visibleWorkDetails).not.toContain("+1 more");
    expect(visibleWorkDetails).not.toContain("Action details ·");
    expect(screen.getByTestId("turn-head")).not.toHaveTextContent("138 tokens");
    expect(screen.queryByTestId("turn-runtime-meta")).toBeNull();
    expect(screen.queryByText("Technical trace")).toBeNull();
  });

  it("keeps multi-turn chats clean without a conversation map above the thread", () => {
    renderTimeline([...completedTurn, ...messageOnlyTurn]);

    expect(screen.queryByTestId("conversation-map")).toBeNull();
    expect(screen.queryByTestId("turn-navigator")).toBeNull();
    expect(screen.queryByTestId("turn-nav-glance")).toBeNull();
    expect(screen.queryByTestId("timeline-toolbar")).toBeNull();
    expect(screen.getAllByTestId("turn-title").map((node) => node.textContent)).toEqual([
      "list the files",
      "message only",
    ]);
    expect(screen.queryByTestId("turn-boundary")).toBeNull();
    const heads = screen.getAllByTestId("turn-head");
    expect(heads[0]).toHaveAttribute("data-visible", "false");
    expect(heads[1]).toHaveAttribute("data-visible", "false");
    expect(heads[0]).not.toHaveTextContent("Message 1");
    expect(heads[0]).toHaveTextContent("list the files");
    expect(heads[0]).toHaveTextContent("Done");
    expect(heads[0]).toHaveTextContent("1 action");
    expect(heads[0]).toHaveTextContent("12ms");
    expect(heads[0]).not.toHaveTextContent("138 tokens");
    expect(heads[1]).not.toHaveTextContent("Message 2");
    expect(heads[1]).toHaveTextContent("message only");
    expect(heads[1]).toHaveTextContent("Done");
    expect(screen.queryByText("0 actions")).toBeNull();
  });

  it("keeps long timelines unfiltered so the chat stays clean and complete", () => {
    const events: RawEvent[] = [
      { id: 1, type: "turn.start", data: { turn_id: "source-turn" } },
      { id: 2, type: "user.message", data: { turn_id: "source-turn", text: "inspect taostats source" } },
      {
        id: 3,
        type: "tool.request",
        data: {
          turn_id: "source-turn",
          call_id: "web-1",
          tool: "browser_navigate",
          args: { url: "https://taostats.io/subnets" },
          args_truncated: false,
          args_bytes: 36,
          args_omitted_bytes: 0,
          args_cap_bytes: 8192,
        },
      },
      {
        id: 4,
        type: "tool.result",
        data: {
          call_id: "web-1",
          exit_code: 0,
          duration_ms: 120,
          result_summary: "SourceAccess: requested_url=https://taostats.io/subnets; browser_rendered_url=https://taostats.io/subnets; page_text_below=partial_dynamic_page_evidence\nDynamic widgets were incomplete.",
          result: "SourceAccess: requested_url=https://taostats.io/subnets; browser_rendered_url=https://taostats.io/subnets; page_text_below=partial_dynamic_page_evidence\nDynamic widgets were incomplete.",
          result_truncated: false,
          result_bytes: 180,
          result_omitted_bytes: 0,
          result_cap_bytes: 8192,
        },
      },
      { id: 5, type: "message.done", data: { turn_id: "source-turn", text: "Taostats needs rendered evidence." } },
      { id: 6, type: "turn.end", data: { turn_id: "source-turn", reason: "completed" } },
      { id: 7, type: "turn.start", data: { turn_id: "guard-turn" } },
      { id: 8, type: "user.message", data: { turn_id: "guard-turn", text: "recover repeated calls" } },
      { id: 9, type: "message.done", data: { turn_id: "guard-turn", text: "I stopped the repeated loop." } },
      {
        id: 10,
        type: "turn.end",
        data: {
          turn_id: "guard-turn",
          reason: "max_turns",
          tool_stats: { tool_requests: 2, loop_guard_interventions: 1, forced_no_tools: 1 },
        },
      },
      { id: 15, type: "turn.start", data: { turn_id: "recall-turn" } },
      { id: 16, type: "user.message", data: { turn_id: "recall-turn", text: "resume alpha coast analysis" } },
      {
        id: 17,
        type: "tool.request",
        data: {
          turn_id: "recall-turn",
          call_id: "recall-1",
          tool: "session_search",
          args: { query: "Alpha Coast marker" },
          args_truncated: false,
          args_bytes: 36,
          args_omitted_bytes: 0,
          args_cap_bytes: 8192,
        },
      },
      {
        id: 18,
        type: "tool.result",
        data: {
          call_id: "recall-1",
          exit_code: 0,
          duration_ms: 24,
          result_summary: "{\"total\":2}",
          result: "{\"total\":2}",
          result_truncated: false,
        },
      },
      {
        id: 19,
        type: "turn.end",
        data: {
          turn_id: "recall-turn",
          reason: "completed",
          tool_stats: { tool_requests: 1, session_search_calls: 1, session_search_results: 2, session_search_context_hits: 1, session_search_matched_terms: 3 },
        },
      },
      { id: 20, type: "turn.start", data: { turn_id: "plain-turn" } },
      { id: 21, type: "user.message", data: { turn_id: "plain-turn", text: "write final note" } },
      { id: 22, type: "message.done", data: { turn_id: "plain-turn", text: "Plain note." } },
      { id: 23, type: "turn.end", data: { turn_id: "plain-turn", reason: "completed" } },
    ];
    renderTimeline(events);

    expect(screen.queryByTestId("timeline-filter")).toBeNull();
    expect(screen.queryByRole("group", { name: "Timeline filter" })).toBeNull();
    expect(screen.queryByRole("searchbox", { name: "Search turns" })).toBeNull();
    expect(screen.getByTestId("timeline")).toHaveTextContent("inspect taostats source");
    expect(screen.getByTestId("timeline")).toHaveTextContent("resume alpha coast analysis");
    expect(screen.getByTestId("timeline")).toHaveTextContent("recover repeated calls");
    expect(screen.getByTestId("timeline")).toHaveTextContent("write final note");
  });

  it("keeps artifact summaries visible in the activity digest when evidence is also present", () => {
    renderTimeline([
      { id: 40, type: "turn.start", data: { turn_id: "t4" } },
      { id: 41, type: "user.message", data: { turn_id: "t4", text: "research affine" } },
      {
        id: 42,
        type: "tool.request",
        data: {
          turn_id: "t4",
          call_id: "fetch-affine",
          tool: "web_fetch",
          args: { url: "https://www.affine.io/" },
          args_truncated: false,
          args_bytes: 32,
          args_omitted_bytes: 0,
          args_cap_bytes: 8192,
        },
      },
      {
        id: 43,
        type: "tool.result",
        data: {
          call_id: "fetch-affine",
          exit_code: 0,
          duration_ms: 40,
          result_summary: "AFFINE subnet 120",
          result: "AFFINE subnet 120",
          result_truncated: false,
          result_bytes: 20,
          result_omitted_bytes: 0,
          result_cap_bytes: 8192,
        },
      },
      {
        id: 44,
        type: "tool.request",
        data: {
          turn_id: "t4",
          call_id: "save-output",
          tool: "shell",
          args: { command: "cat big.log" },
          args_truncated: false,
          args_bytes: 24,
          args_omitted_bytes: 0,
          args_cap_bytes: 8192,
        },
      },
      {
        id: 45,
        type: "tool.result",
        data: {
          call_id: "save-output",
          exit_code: 0,
          duration_ms: 88,
          result_summary: "line 1\nline 2\n…(truncated)",
          result: "line 1\nline 2\n… [output truncated]",
          result_truncated: true,
          result_bytes: 8192,
          result_omitted_bytes: 1048576,
          result_cap_bytes: 8192,
          result_artifact_path: ".affent/artifacts/tool-results/000001-c1.txt",
        },
      },
      { id: 46, type: "message.done", data: { turn_id: "t4", text: "Affine is subnet 120.", finish_reason: "stop" } },
      { id: 47, type: "turn.end", data: { turn_id: "t4", reason: "completed" } },
    ]);

    const digest = screen.getByTestId("agent-activity-digest");
    expect(digest).toHaveTextContent("1 file (8 KiB, 1 MiB omitted)");
    expect(digest.textContent?.replace(/\s+/g, " ").trim()).toContain("1 file (8 KiB, 1 MiB omitted)");
  });

  it("compresses the turn header into a single visible summary line", () => {
    renderTimeline(resultTruncated);

    expect(screen.getByTestId("turn-head")).toHaveTextContent("cat big.log");
    expect(screen.getByTestId("turn-head")).toHaveTextContent("1 action");
    expect(screen.getByTestId("turn-head")).toHaveTextContent("1 file");
    expect(screen.getByTestId("turn-head")).toHaveTextContent("88ms");
    expect(screen.getByTestId("turn-head")).toHaveTextContent("+2 more");
    expect(screen.queryByText("0 actions")).toBeNull();
  });

  it("uses an approachable empty state before the first message", () => {
    const onUseAsDraft = vi.fn();
    renderTimeline([], undefined, undefined, onUseAsDraft);

    expect(screen.getByTestId("timeline-empty")).toHaveTextContent("What should we work on?");
    expect(screen.getByTestId("timeline-empty")).toHaveTextContent("start from a draft");
    expect(screen.getByRole("button", { name: /Inspect project/ })).toBeInTheDocument();
    expect(screen.getByTestId("starter-preview")).toHaveTextContent("Inspect this project and summarize");
    expect(screen.getByTestId("timeline-empty")).not.toHaveTextContent("Message");
  });

  it("offers a direct way back to the latest saved chat without auto-opening it", async () => {
    const user = userEvent.setup();
    const onOpenLatestChat = vi.fn();
    const onUseAsDraft = vi.fn();
    render(
      <Timeline
        session={reduceRawEvents([])}
        savedChatCount={3}
        latestChat={{ title: "affine research", draft: "affine research", meta: "sess_9a7...f4b20d · 2026-05-24 17:37 UTC" }}
        onOpenLatestChat={onOpenLatestChat}
        onUseAsDraft={onUseAsDraft}
      />,
    );

    expect(screen.getByTestId("timeline-empty")).toHaveTextContent("affine research");
    expect(screen.queryByTestId("timeline")).toBeNull();
    await user.click(screen.getByRole("button", { name: "Use as draft" }));
    expect(onUseAsDraft).toHaveBeenCalledWith("affine research", "recent_chat");
    expect(onOpenLatestChat).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: /Open latest chat/ }));

    expect(onOpenLatestChat).toHaveBeenCalledTimes(1);
  });

  it("previews starter drafts before placing them in the composer", async () => {
    const user = userEvent.setup();
    const onUseAsDraft = vi.fn();
    renderTimeline([], undefined, undefined, onUseAsDraft);

    await user.hover(screen.getByRole("button", { name: /Investigate issue/ }));

    expect(screen.getByTestId("starter-preview")).toHaveTextContent("Investigate the current issue");
    await user.click(screen.getByRole("button", { name: "Use draft" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(
      "Investigate the current issue, call out the likely cause, and propose the next concrete step.",
      "starter",
    );
  });

  it("selects a starter on click, then turns the preview into an editable draft", async () => {
    const user = userEvent.setup();
    const onUseAsDraft = vi.fn();
    renderTimeline([], undefined, undefined, onUseAsDraft);

    await user.click(screen.getByRole("button", { name: /Fix a failure/ }));

    expect(onUseAsDraft).not.toHaveBeenCalled();
    expect(screen.getByTestId("starter-preview")).toHaveTextContent("Find the failing test or execution error");
    await user.click(screen.getByRole("button", { name: "Use draft" }));

    expect(onUseAsDraft).toHaveBeenCalledWith(
      "Find the failing test or execution error, explain the cause, and propose the smallest fix.",
      "starter",
    );
  });

  it("shows a pending turn immediately after a task is submitted", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    const session = reduceRawEvents([]);
    render(<Timeline session={session} pendingMessage={{ text: "summarize the repo", kind: "task" }} />);

    expect(screen.queryByTestId("timeline-empty")).toBeNull();
    expect(screen.getByTestId("timeline")).toHaveAttribute("data-pending-first", "true");
    expect(screen.getByTestId("pending-turn")).toHaveTextContent("summarize the repo");
    expect(screen.getByTestId("pending-turn")).toHaveAttribute("data-kind", "task");
    expect(screen.getByTestId("turn-title")).toHaveTextContent("summarize the repo");
    expect(screen.getByTestId("pending-turn")).toHaveTextContent("Starting");
    expect(screen.getByTestId("pending-turn")).not.toHaveTextContent("events");

    await openMessageOptions(user, screen.getByTestId("pending-turn"));
    await user.click(screen.getByRole("button", { name: "Copy" }));

    expect(writeText).toHaveBeenCalledWith("summarize the repo");
  });

  it("shows a pending follow-up as the next conversation message", () => {
    const session = reduceRawEvents(completedTurn);
    render(<Timeline session={session} pendingMessage={{ text: "explain main.go", kind: "task" }} />);

    expect(screen.getByTestId("pending-turn")).toHaveTextContent("explain main.go");
    expect(screen.getByTestId("pending-turn")).toHaveTextContent("Waiting for the next update in this chat.");
    expect(screen.getByTestId("pending-turn")).not.toHaveTextContent("Preparing the first update.");
    expect(screen.queryByTestId("conversation-map")).toBeNull();
    expect(screen.queryByTestId("turn-navigator")).toBeNull();
  });

  it("shows pending live guidance as intervention instead of a new task", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    const session = reduceRawEvents(runningSubagent);
    render(<Timeline session={session} pendingMessage={{ text: "Guidance for current run: check tests first", kind: "guidance" }} />);

    expect(screen.getByTestId("pending-turn")).toHaveAttribute("data-kind", "guidance");
    expect(screen.getByTestId("pending-turn")).toHaveTextContent("Guidance");
    expect(screen.getByTestId("pending-turn")).toHaveTextContent("Live guidance");
    expect(screen.getByTestId("pending-turn")).toHaveTextContent("Sending");
    expect(screen.getByLabelText("Guidance for current run")).toHaveTextContent("check tests first");
    expect(screen.getByTestId("pending-turn")).toHaveTextContent("Applying your guidance to the current run.");
    expect(screen.getByTestId("pending-turn")).not.toHaveTextContent("Preparing the first update.");

    await openMessageOptions(user, screen.getByTestId("pending-turn"));
    await user.click(screen.getByRole("button", { name: "Copy" }));

    expect(writeText).toHaveBeenCalledWith("Guidance for current run: check tests first");
  });

  it("keeps submitted live guidance visible as an editable receipt", async () => {
    const user = userEvent.setup();
    const onUseAsDraft = vi.fn();
    const session = reduceRawEvents(runningSubagent);
    render(
      <Timeline
        session={session}
        guidanceReceipts={[{ id: 1, text: "Guidance for current run: check tests first" }]}
        onUseAsDraft={onUseAsDraft}
      />,
    );

    expect(screen.getByTestId("guidance-receipt")).toHaveTextContent("Guidance sent");
    expect(screen.getByTestId("guidance-receipt")).toHaveTextContent("check tests first");
    expect(screen.getByTestId("guidance-receipt")).toHaveTextContent("Affent will use this in the current run.");
    expect(screen.queryByTestId("pending-turn")).toBeNull();

    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    await openMessageOptions(user, screen.getByTestId("guidance-receipt"));
    await user.click(screen.getByRole("button", { name: "Copy" }));
    expect(writeText).toHaveBeenCalledWith("Guidance for current run: check tests first");

    await openMessageOptions(user, screen.getByTestId("guidance-receipt"));
    await user.click(screen.getByRole("button", { name: "Edit guidance" }));

    expect(onUseAsDraft).toHaveBeenCalledWith("Guidance for current run: check tests first", "guidance_receipt");
  });

  it("keeps speaker names out of message bubble chrome", () => {
    renderTimeline(completedTurn);

    expect(screen.getByLabelText("You message")).toBeInTheDocument();
    expect(screen.getByLabelText("Affent message")).toBeInTheDocument();
    expect(within(screen.getByTestId("msg-user")).queryByText("You")).toBeNull();
    expect(within(screen.getByTestId("msg-assistant")).queryByText("Affent")).toBeNull();
  });

  it("keeps a running tool visible as an Affent chat update", () => {
    renderTimeline(runningSubagent);

    const runningAnswer = screen.getByTestId("running-answer");
    expect(runningAnswer).toHaveTextContent("Working on this");
    expect(runningAnswer).toHaveTextContent("Inspect docs for WebUI trace requirements");
    expect(runningAnswer).toHaveTextContent("1 running");
    expect(screen.queryByTestId("conversation-map")).toBeNull();
    expect(screen.queryByTestId("turn-navigator")).toBeNull();
    expect(screen.queryByTestId("turn-nav-glance")).toBeNull();
    expect(screen.queryByRole("button", { name: "Copy answer" })).toBeNull();
    expect(screen.getByTestId("agent-activity")).toHaveTextContent("What Affent is doing");
    expect(screen.getByTestId("agent-activity-digest")).toHaveTextContent("Now");
    expect(screen.getByTestId("agent-activity-digest")).toHaveTextContent("Inspect docs for WebUI trace requirements");
    expect(screen.getByTestId("agent-activity-brief")).not.toHaveTextContent("Current focus");
    expect(screen.getByTestId("agent-activity-brief")).toHaveTextContent("You can still guide this run");
    expect(screen.getByTestId("agent-activity-tree")).toHaveTextContent("Delegate");
    expect(screen.getByTestId("agent-activity-tree")).toHaveTextContent("Inspect docs for WebUI trace requirements");
    expect(screen.getByTestId("agent-activity-tree")).toHaveTextContent("running");
    expect(screen.queryByTestId("work-thread")).toBeNull();
    expect(screen.queryByTestId("execution-tree")).toBeNull();
  });

  it("keeps a single live turn focused on chat activity instead of trace navigation", () => {
    renderTimeline(runningSubagent);

    expect(screen.queryByRole("button", { name: "Search in chat" })).toBeNull();
    expect(screen.getByTestId("agent-activity")).toHaveAttribute("data-open", "true");
    expect(screen.getByTestId("agent-activity-tree")).toHaveTextContent("Inspect docs for WebUI trace requirements");
    expect(screen.queryByTestId("timeline-toolbar")).toBeNull();
  });

  it("keeps activity tree text selectable by using a separate disclosure control", async () => {
    const user = userEvent.setup();
    renderTimeline(completedSubagentTree);

    await user.click(screen.getByRole("button", { name: /What Affent did/ }));

    const rows = screen.getAllByTestId("agent-activity-node-row");
    expect(rows[0].tagName).toBe("DIV");

    const toggle = screen.getByRole("button", {
      name: /Expand Find the WebUI trace requirements/,
    });
    const expandableRow = toggle.closest(".agent-activity-node-row");
    expect(expandableRow).toHaveAttribute("data-interactive", "true");
    expect(toggle).toHaveAttribute("aria-expanded", "false");

    await user.click(toggle);

    expect(toggle).toHaveAttribute("aria-expanded", "true");
  });

  it("turns a live activity brief into guidance", async () => {
    const user = userEvent.setup();
    const onUseAsDraft = vi.fn();
    renderTimeline(runningSubagent, undefined, undefined, onUseAsDraft);

    await user.click(within(screen.getByTestId("agent-activity-brief")).getByRole("button", { name: "Guide run" }));

    expect(onUseAsDraft).toHaveBeenCalledWith("Guidance for current run: ", "tool_guidance");
  });

  it("keeps single-message chats free of message counters", () => {
    renderTimeline(runningStarted);

    expect(screen.getByTestId("running-answer")).toHaveTextContent("Reading your request");
    expect(screen.getByTestId("turn-head")).toHaveAttribute("data-visible", "false");
    expect(screen.queryByTestId("turn-boundary")).toBeNull();
    expect(screen.queryByTestId("conversation-map")).toBeNull();
    expect(screen.queryByTestId("timeline-toolbar")).toBeNull();
  });

  it("does not repeat the user message in the first running update", () => {
    renderTimeline(runningStarted);

    const runningAnswer = screen.getByTestId("running-answer");
    expect(runningAnswer).toHaveTextContent("Reading your request");
    expect(runningAnswer).toHaveTextContent("Preparing the first step.");
    expect(runningAnswer).not.toHaveTextContent("summarize the repo");
  });

  it("hides completed reasoning from the main scan path", () => {
    renderTimeline([...completedTurn, ...messageOnlyTurn]);

    expect(screen.queryByRole("button", { name: /Thinking/ })).toBeNull();
    expect(screen.queryByText("Technical trace")).toBeNull();
    expect(screen.getByTestId("agent-activity")).not.toHaveTextContent("I should list files.");
    expect(screen.queryByText(/\d+ events/)).not.toBeInTheDocument();
    expect(screen.queryByTestId("event-trace")).toBeNull();
  });

  it("keeps historical completed reasoning out of the main scan path", () => {
    renderTimeline([...completedTurn, ...messageOnlyTurn]);

    expect(screen.queryByRole("button", { name: /Thinking/ })).toBeNull();
    expect(screen.getAllByTestId("agent-activity")).toHaveLength(1);
    expect(screen.getByTestId("agent-activity-digest")).toHaveTextContent("README.md main.go");
  });

  it("shows the assistant answer before runtime details when a turn is complete", () => {
    renderTimeline(completedTurn);

    const answer = screen.getByTestId("msg-assistant");
    const work = screen.getByTestId("work-thread");
    const activity = screen.getByTestId("agent-activity");

    expect(answer.compareDocumentPosition(work) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(answer.compareDocumentPosition(activity) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("surfaces streaming assistant text as a live writing state", () => {
    renderTimeline(completedTurn.filter((event) => event.id <= 8));

    const answer = screen.getByTestId("msg-assistant");
    expect(answer).toHaveAttribute("data-streaming", "true");
    expect(answer).toHaveTextContent("There are two files.");
    expect(within(answer).getByRole("status")).toHaveTextContent("Writing");
    expect(screen.queryByRole("button", { name: "Ask follow-up" })).toBeNull();
  });

  it("removes the live writing state when the answer is complete", () => {
    renderTimeline(completedTurn);

    const answer = screen.getByTestId("msg-assistant");
    expect(answer).toHaveAttribute("data-streaming", "false");
    expect(within(answer).queryByRole("status")).toBeNull();
  });

  it("hides issue controls for a simple one-message answer", () => {
    renderTimeline(completedTurn);

    expect(screen.queryByTestId("timeline-toolbar")).toBeNull();
  });

  it("copies the assistant answer from the chat bubble", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    renderTimeline(completedTurn);

    await openMessageOptions(user, screen.getByTestId("msg-assistant"));
    await user.click(screen.getByRole("button", { name: "Copy plain text" }));

    expect(writeText).toHaveBeenCalledWith("There are two files.");
    expect(screen.queryByRole("button", { name: "Copy markdown" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Copy plain text" })).toBeNull();
    expect(within(screen.getByTestId("msg-assistant")).getByRole("button", { name: "Message options" })).toBeInTheDocument();
  });

  it("copies the user's message from the chat bubble", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    renderTimeline(completedTurn, "s1", undefined, vi.fn());

    await openMessageOptions(user, screen.getByTestId("msg-user"));
    await user.click(screen.getByRole("button", { name: "Copy" }));

    expect(writeText).toHaveBeenCalledWith("list the files");
    expect(screen.queryByRole("button", { name: "Copy" })).toBeNull();
  });

  it("turns an assistant answer into a follow-up draft", async () => {
    const user = userEvent.setup();
    const onUseAsDraft = vi.fn();
    renderTimeline(completedTurn, "s1", undefined, onUseAsDraft);

    await openMessageOptions(user, screen.getByTestId("msg-assistant"));
    await user.click(screen.getByRole("button", { name: "Ask follow-up" }));

    expect(onUseAsDraft).toHaveBeenCalledWith("Continue from this answer: There are two files.", "answer");
  });

  it("turns an assistant answer into a retry draft", async () => {
    const user = userEvent.setup();
    const onUseAsDraft = vi.fn();
    renderTimeline(completedTurn, "s1", undefined, onUseAsDraft);

    await openMessageOptions(user, screen.getByTestId("msg-assistant"));
    await user.click(screen.getByRole("button", { name: "Retry from here" }));

    expect(onUseAsDraft).toHaveBeenCalledWith("Retry from this reply:\n\nThere are two files.", "retry_reply");
  });

  it("keeps markdown structure when retrying an assistant reply", async () => {
    const user = userEvent.setup();
    const onUseAsDraft = vi.fn();
    renderTimeline([
      { id: 0, type: "turn.start", data: { turn_id: "t1" } },
      { id: 1, type: "user.message", data: { turn_id: "t1", text: "show the summary" } },
      {
        id: 2,
        type: "message.done",
        data: {
          turn_id: "t1",
          text: "# Summary\n\n- alpha\n- beta\n\n```ts\nconst value = 1;\n```",
        },
      },
      { id: 3, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ], "s1", undefined, onUseAsDraft);

    await openMessageOptions(user, screen.getByTestId("msg-assistant"));
    await user.click(screen.getByRole("button", { name: "Retry from here" }));

    expect(onUseAsDraft).toHaveBeenCalledWith(
      "Retry from this reply:\n\n# Summary\n\n- alpha\n- beta\n\n```ts\nconst value = 1;\n```",
      "retry_reply",
    );
  });

  it("keeps previous prompt editing out of message chrome", () => {
    const onUseAsDraft = vi.fn();
    renderTimeline(completedTurn, "s1", undefined, onUseAsDraft);

    expect(screen.queryByRole("button", { name: "Edit prompt" })).toBeNull();
    expect(onUseAsDraft).not.toHaveBeenCalled();
  });

  it("expands a tool card inline on click", async () => {
    const user = userEvent.setup();
    renderTimeline(completedTurn);
    expect(screen.queryByTestId("tool-details")).toBeNull();
    await user.click(screen.getByRole("button", { name: /Action details|Run summary/ }));
    await user.click(within(screen.getByTestId("execution-tree")).getByRole("button", { name: /List current directory/ }));
    const details = screen.getByTestId("tool-details");
    expect(details).toBeInTheDocument();
    expect(screen.getByTestId("action-inspector-summary")).toHaveTextContent("File action");
    expect(screen.getByTestId("action-inspector-summary")).toHaveTextContent("Status done");
    expect(screen.getByTestId("execution-node")).toHaveTextContent("done");
    expect(details).not.toHaveTextContent("list_files");
    expect(details).toHaveTextContent("Output");
    expect(details).not.toHaveTextContent("action type");
    expect(details).toHaveTextContent("Overview");
    expect(details).toHaveTextContent(/input \+ \d+ trace entries/);
    expect(details).toHaveTextContent("request input");
    expect(within(details).getByText("Raw trace")).toBeInTheDocument();
    expect(within(details).getByText(/\d+ trace entries/, { selector: ".subtle-count" })).toBeInTheDocument();
    expect(within(details).queryByTestId("event-trace")).toBeNull();

    await user.click(within(details).getByText("Raw trace"));

    expect(within(details).getByTestId("event-trace")).toBeInTheDocument();
  });

  it("shows an exit-code badge for a failed tool", () => {
    renderTimeline(toolError);
    fireEvent.click(screen.getByRole("button", { name: /Action details/ }));
    const card = screen.getByTestId("execution-node");
    expect(card).toHaveAttribute("data-status", "error");
    expect(within(card).getByText(/exit 2/)).toBeInTheDocument();
    expect(screen.getByTestId("work-thread")).toHaveTextContent("Action details");
    expect(screen.getByTestId("work-thread")).toHaveTextContent("1 tool issue");
    expect(screen.getByTestId("work-summary")).toHaveTextContent("1 tool issue");
    expect(screen.getByTestId("work-summary")).toHaveTextContent("make");
  });

  it("badges a truncated result with an artifact", () => {
    renderTimeline(resultTruncated);
    expect(screen.getByTestId("fallback-answer")).toHaveTextContent("Action output was truncated");
    expect(screen.getByTestId("fallback-answer")).toHaveTextContent("line 1");
    expect(screen.getByTestId("fallback-answer")).toHaveTextContent("Full output is available below.");
    expect(screen.getByTestId("turn-head")).toHaveTextContent("+2 more");
    fireEvent.click(screen.getByRole("button", { name: /Action details|Run summary/ }));
    fireEvent.click(screen.getAllByRole("button", { name: /cat big.log/ })[1]);
    const card = screen.getByTestId("execution-node");
    expect(within(card).getByText("truncated")).toBeInTheDocument();
    expect(within(card).getByText("artifact")).toBeInTheDocument();
    expect(within(card).getByText("(8 KiB, 1 MiB omitted)")).toBeInTheDocument();
    expect(screen.getByTestId("work-summary")).toHaveTextContent("1 truncated");
    expect(screen.getByTestId("work-summary")).toHaveTextContent("1 file");
    expect(screen.getAllByTestId("action-inspector-summary")[0]).toHaveTextContent("+3 more");
  });

  it("copies fallback results and turns them into follow-up drafts", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    const onUseAsDraft = vi.fn();
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    renderTimeline(resultTruncated, "s1", undefined, onUseAsDraft);

    await user.click(screen.getByRole("button", { name: "Copy output" }));
    expect(writeText).toHaveBeenCalledWith(
      "Action output was truncated\nline 1\nline 2\n…(truncated)\nFull output is available below.",
    );

    await user.click(screen.getByRole("button", { name: "Ask follow-up" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(
      "Continue from this output: line 1 line 2 …(truncated) Full output is available below.",
      "result",
    );
  });

  it("turns fallback output into a retry draft", async () => {
    const user = userEvent.setup();
    const onUseAsDraft = vi.fn();
    renderTimeline(resultTruncated, "s1", undefined, onUseAsDraft);

    await user.click(screen.getByRole("button", { name: "Retry from here" }));

    expect(onUseAsDraft).toHaveBeenCalledWith(
      "Retry from this reply:\n\nAction output was truncated\nline 1\nline 2\n…(truncated)\nFull output is available below.",
      "retry_reply",
    );
  });

  it("shows original versus executed args for repaired tool calls", async () => {
    const user = userEvent.setup();
    renderTimeline(argsRepaired);

    await user.click(screen.getByRole("button", { name: /Action details|Run summary/ }));
    await user.click(within(screen.getByTestId("execution-tree")).getByRole("button", { name: /main.go/ }));

    expect(screen.getByTestId("repair-comparison")).toHaveTextContent("original action");
    expect(screen.getByTestId("repair-comparison")).toHaveTextContent("readFile");
    expect(screen.getByTestId("repair-comparison")).toHaveTextContent("\"filename\":\"main.go\"");
    expect(screen.getByTestId("repair-comparison")).toHaveTextContent("executed action");
    expect(screen.getByTestId("repair-comparison")).toHaveTextContent("read_file");
    expect(screen.getByTestId("repair-comparison")).toHaveTextContent('"path": "main.go"');
    expect(screen.getByText("repair notes")).toBeInTheDocument();
    expect(screen.getByText("coerced filename -> path")).toBeInTheDocument();
  });

  it("surfaces output files in the chat without opening the work tree", async () => {
    const user = userEvent.setup();
    const onOpenArtifact = vi.fn();
    const onUseAsDraft = vi.fn();
    renderTimeline(resultTruncated, "s1", onOpenArtifact, onUseAsDraft);

    expect(screen.getByTestId("turn-artifacts")).toHaveTextContent("Full output");
    expect(screen.getByTestId("turn-artifacts")).toHaveTextContent("000001-c1.txt");
    expect(screen.getByTestId("turn-artifacts")).toHaveTextContent("cat big.log");
    expect(screen.queryByTestId("execution-tree")).toBeNull();

    await user.click(within(screen.getByTestId("turn-artifacts")).getByRole("button", { name: "Use in message" }));
    expect(onUseAsDraft).toHaveBeenCalledWith("Use this file in the next step: .affent/artifacts/tool-results/000001-c1.txt", "artifact");

    await user.click(within(screen.getByTestId("turn-artifacts")).getByRole("button", { name: "Open file" }));
    expect(onOpenArtifact).toHaveBeenCalledWith(".affent/artifacts/tool-results/000001-c1.txt");
  });

  it("keeps the execution tree free of extra top-level controls", () => {
    renderTimeline(resultTruncated, "s1");

    expect(screen.getByRole("button", { name: /Action details|Run summary/ })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Show important" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Collapse details" })).toBeNull();
  });

  it("auto-expands the currently running subagent activity", () => {
    renderTimeline(runningSubagent);

    expect(screen.getByTestId("agent-activity")).toHaveAttribute("data-open", "true");
    expect(screen.getByTestId("agent-activity-tree")).toHaveTextContent("Delegate");
    expect(screen.getByTestId("agent-activity-tree")).toHaveTextContent("Running");
    expect(screen.getByTestId("agent-activity-tree")).toHaveTextContent("Inspect docs for WebUI trace requirements");
    expect(screen.getAllByTestId("agent-activity-node")[0]).toHaveAttribute("data-open", "true");
    expect(screen.getAllByTestId("agent-activity-node")[0]).toHaveAttribute("data-status", "running");
    expect(screen.queryByTestId("execution-tree")).toBeNull();
  });

  it("folds historical failed activity after a later turn completes", () => {
    const events: RawEvent[] = [
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "research affine" } },
      {
        id: 3,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "web_fetch",
          args: { url: "https://example.invalid" },
          args_truncated: false,
          args_bytes: 32,
          args_omitted_bytes: 0,
          args_cap_bytes: 65536,
        },
      },
      {
        id: 4,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "c1",
          exit_code: 1,
          duration_ms: 42,
          result_summary: "DNS failed",
          result: "DNS failed",
          result_truncated: false,
          result_bytes: 10,
          result_omitted_bytes: 0,
          result_cap_bytes: 262144,
        },
      },
      { id: 5, type: "turn.end", data: { turn_id: "t1", reason: "max_turns" } },
      { id: 6, type: "turn.start", data: { turn_id: "t2" } },
      { id: 7, type: "user.message", data: { turn_id: "t2", text: "continue and summarize" } },
      { id: 8, type: "message.done", data: { turn_id: "t2", text: "Here is the report." } },
      { id: 9, type: "turn.end", data: { turn_id: "t2", reason: "completed" } },
    ];

    renderTimeline(events);

    const activity = screen.getByTestId("agent-activity");
    expect(activity).toHaveAttribute("data-open", "false");
    expect(activity).toHaveTextContent("Continued");
    expect(activity).not.toHaveTextContent("Issues");
    expect(screen.getByRole("button", { name: /Earlier work/ })).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByTestId("agent-activity-brief")).toBeNull();
    expect(screen.queryByTestId("fallback-answer")).toBeNull();
    expect(screen.queryByTestId("continuation-card")).toBeNull();
  });

  it("keeps the latest failed activity open so the user can continue", () => {
    const events: RawEvent[] = [
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "research affine" } },
      {
        id: 3,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "web_fetch",
          args: { url: "https://example.invalid" },
          args_truncated: false,
          args_bytes: 32,
          args_omitted_bytes: 0,
          args_cap_bytes: 65536,
        },
      },
      {
        id: 4,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "c1",
          exit_code: 1,
          duration_ms: 42,
          result_summary: "DNS failed",
          result: "DNS failed",
          result_truncated: false,
          result_bytes: 10,
          result_omitted_bytes: 0,
          result_cap_bytes: 262144,
        },
      },
      { id: 5, type: "turn.end", data: { turn_id: "t1", reason: "max_turns" } },
    ];

    renderTimeline(events);

    expect(screen.getByTestId("agent-activity")).toHaveAttribute("data-open", "true");
    expect(screen.getByRole("button", { name: /Issue Continue/ })).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByTestId("agent-activity-brief")).toHaveTextContent("Next");
  });

  it("keeps historical subagents folded until the user opens them", async () => {
    const user = userEvent.setup();
    renderTimeline(completedSubagentTree);

    expect(screen.getByRole("button", { name: /What Affent did/ })).toHaveAttribute("aria-expanded", "false");
    expect(screen.getByTestId("agent-activity-digest-evidence")).toHaveTextContent("Sources");
    expect(screen.getByTestId("agent-activity-digest-evidence")).toHaveTextContent("docs");
    expect(screen.getByTestId("agent-activity-digest-evidence")).toHaveTextContent("+1 more");
    await user.click(screen.getByRole("button", { name: /What Affent did/ }));
    expect(screen.queryByTestId("agent-activity-digest-evidence")).toBeNull();
    const activityTree = screen.getByTestId("agent-activity-tree");
    const activityBrief = screen.getByTestId("agent-activity-brief");
    expect(activityBrief).toHaveTextContent("Goal");
    expect(activityBrief).toHaveTextContent("delegate docs inspection");
    expect(activityBrief).toHaveTextContent("Sources");
    expect(activityBrief).toHaveTextContent("docs/webui-product-design.md");
    expect(activityBrief).toHaveTextContent("Next");
    expect(activityBrief).toHaveTextContent("Replace result parsing with explicit child trace events");
    expect(activityTree).toHaveTextContent("392 tokens");
    expect(within(activityTree).getByRole("button", { name: /Find the WebUI trace requirements/ })).toHaveAttribute("aria-expanded", "false");
    expect(within(activityTree).getAllByText("Read").length).toBeGreaterThan(0);
    expect(within(activityTree).getByText("docs/webui-product-design.md")).toBeInTheDocument();
    expect(within(activityTree).getByText("MCP")).toBeInTheDocument();
    expect(within(activityTree).queryByText("Search")).toBeNull();
    await user.click(within(activityTree).getByRole("button", { name: /Find the WebUI trace requirements/ }));
    expect(within(activityTree).getAllByText("MCP").length).toBeGreaterThan(0);
    expect(within(activityTree).getByText("Search")).toBeInTheDocument();
    expect(screen.queryByTestId("execution-tree")).toBeNull();

    await user.click(screen.getByRole("button", { name: /Action details|Run summary/ }));
    const executionTree = screen.getByTestId("execution-tree");
    expect(within(executionTree).getByRole("button", { name: /Find the WebUI trace requirements/ })).toBeInTheDocument();
    expect(within(executionTree).getByRole("button", { name: /Verify trace tree requirements/ })).toBeInTheDocument();
    expect(within(executionTree).getByRole("button", { name: /Conclusion: WebUI must render trace details/ })).toBeInTheDocument();
    expect(within(executionTree).getByRole("button", { name: /Trace UI needs hierarchical detail/ })).toBeInTheDocument();

    const subagent = within(executionTree).getByRole("button", { name: /Find the WebUI trace requirements/ });
    expect(subagent).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByTestId("tool-details")).toBeNull();

    await user.click(subagent);
    expect(subagent).toHaveAttribute("aria-expanded", "true");
    expect(within(screen.getAllByTestId("tool-details")[0]).getAllByText(/WebUI must render trace details/).length).toBeGreaterThan(0);
    expect(within(screen.getAllByTestId("tool-details")[0]).getAllByText("Delegated worker").length).toBeGreaterThan(0);
    expect(within(screen.getAllByTestId("tool-details")[0]).queryByText("subagent_run")).toBeNull();
    expect(screen.getAllByTestId("tool-details")[0]).toHaveTextContent("+3 more");
    expect(screen.getByText("MCP action")).toBeInTheDocument();
    expect(screen.getAllByText("External MCP service").length).toBeGreaterThan(0);
    expect(screen.getAllByText("subagent_01").length).toBeGreaterThan(0);
  });

  it("copies the processed agent activity summary without opening raw details", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    renderTimeline(completedSubagentTree);

    await user.click(within(screen.getByTestId("agent-activity")).getByRole("button", { name: "Copy activity" }));
    await user.click(within(screen.getByRole("menu")).getByRole("button", { name: "Copy summary" }));

    expect(writeText).toHaveBeenCalledTimes(1);
    const copied = writeText.mock.calls[0][0] as string;
    expect(copied).toContain("What Affent did (Done)");
    expect(copied).toContain("Result: WebUI must render trace details");
    expect(copied).toContain("Goal: delegate docs inspection");
    expect(copied).toContain("Read docs/webui-product-design.md");
    expect(copied).toContain("Delegate: Find the WebUI trace requirements");
    expect(copied).toContain("MCP: Search");
    expect(copied).not.toContain("tool.request");
    expect(screen.queryByTestId("execution-tree")).toBeNull();
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("copies failed activity details directly from the user-facing activity header", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    renderTimeline(toolError);

    await user.click(screen.getByRole("button", { name: "Copy issues" }));

    expect(writeText).toHaveBeenCalledTimes(1);
    const copied = writeText.mock.calls[0][0] as string;
    expect(copied).toContain("# issue 1: make");
    expect(copied).toContain("Tool: shell");
    expect(copied).toContain("Status: failed");
    expect(copied).toContain("Exit: 2");
    expect(copied).toContain('"command": "make"');
    expect(copied).toContain("Next: check the Makefile path");
    expect(copied).toContain("Output:\nmake: *** No rule to make target. Stop.");
    expect(screen.queryByTestId("execution-tree")).toBeNull();
  });

  it("copies detailed activity records without opening work details", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    renderTimeline(completedSubagentTree);

    await user.click(within(screen.getByTestId("agent-activity")).getByRole("button", { name: "Copy activity" }));
    await user.click(within(screen.getByRole("menu")).getByRole("button", { name: "Copy activity details" }));

    expect(writeText).toHaveBeenCalledTimes(1);
    const copied = writeText.mock.calls[0][0] as string;
    expect(copied).toContain("What Affent did (Done)");
    expect(copied).toContain("# 1 Find the WebUI trace requirements");
    expect(copied).toContain("Tool: subagent_run");
    expect(copied).toContain('"task": "Find the WebUI trace requirements"');
    expect(copied).toContain("# 1.2 docs/webui-product-design.md");
    expect(copied).toContain("Tool: read_file");
    expect(copied).toContain('"max_bytes": 4096');
    expect(copied).toContain("# 1.3 Search");
    expect(copied).toContain("Tool: MCP_search");
    expect(screen.queryByTestId("execution-tree")).toBeNull();
  });

  it("copies one activity tree step from the user-facing activity view", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    renderTimeline(completedSubagentTree);

    await user.click(screen.getByRole("button", { name: /What Affent did/ }));
    const activityTree = screen.getByTestId("agent-activity-tree");
    await user.click(within(activityTree).getByRole("button", { name: /Expand Find the WebUI trace requirements/ }));
    const docNode = screen.getAllByTestId("agent-activity-node").find((node) =>
      node.getAttribute("data-depth") === "1" && node.textContent?.includes("docs/webui-product-design.md")
    );
    expect(docNode).toBeDefined();

    await user.click(within(docNode as HTMLElement).getByRole("button", { name: "Copy step" }));

    expect(writeText).toHaveBeenCalledTimes(1);
    const copied = writeText.mock.calls[0][0] as string;
    expect(copied).toContain("Action: docs/webui-product-design.md");
    expect(copied).toContain("Tool: read_file");
    expect(copied).toContain("Exit: 0");
    expect(copied).toContain('"path": "docs/webui-product-design.md"');
    expect(copied).not.toContain("Tool: subagent_run");
  });

  it("turns a processed activity next step into an editable message", async () => {
    const user = userEvent.setup();
    const onUseAsDraft = vi.fn();
    renderTimeline(completedSubagentTree, undefined, undefined, onUseAsDraft);

    await user.click(screen.getByRole("button", { name: /What Affent did/ }));
    await user.click(within(screen.getByTestId("agent-activity-brief")).getByRole("button", { name: "Use next step" }));

    expect(onUseAsDraft).toHaveBeenCalledWith(
      "Continue: Replace result parsing with explicit child trace events when backend exposes them.",
      "tool_guidance",
    );
  });

  it("turns processed evidence into an editable follow-up", async () => {
    const user = userEvent.setup();
    const onUseAsDraft = vi.fn();
    renderTimeline(completedSubagentTree, undefined, undefined, onUseAsDraft);

    await user.click(screen.getByRole("button", { name: /What Affent did/ }));
    await user.click(within(screen.getByTestId("agent-activity-brief")).getByRole("button", { name: "Use sources" }));

    expect(onUseAsDraft).toHaveBeenCalledWith(
      [
        "Use this evidence in the next step:",
        "- Read docs/webui-product-design.md",
        "- Read docs/focused-tasks.md",
        "- MCP webui trace",
        "- Listed docs",
      ].join("\n"),
      "evidence",
    );
  });

  it("turns folded evidence preview into an editable follow-up without opening activity", async () => {
    const user = userEvent.setup();
    const onUseAsDraft = vi.fn();
    renderTimeline(completedSubagentTree, undefined, undefined, onUseAsDraft);

    expect(screen.getByRole("button", { name: /What Affent did/ })).toHaveAttribute("aria-expanded", "false");
    await user.click(within(screen.getByTestId("agent-activity-digest-evidence")).getByRole("button", { name: "Use sources" }));

    expect(screen.getByRole("button", { name: /What Affent did/ })).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByTestId("agent-activity-brief")).toBeNull();
    expect(onUseAsDraft).toHaveBeenCalledWith(
      [
        "Use this evidence in the next step:",
        "- Read docs/webui-product-design.md",
        "- Read docs/focused-tasks.md",
        "- MCP webui trace",
        "- Listed docs",
      ].join("\n"),
      "evidence",
    );
  });

  it("turns a specific activity node next step into an editable message", async () => {
    const user = userEvent.setup();
    const onUseAsDraft = vi.fn();
    renderTimeline(completedSubagentTree, undefined, undefined, onUseAsDraft);

    await user.click(screen.getByRole("button", { name: /What Affent did/ }));
    await user.click(within(screen.getByTestId("agent-activity-tree")).getByRole("button", { name: "Use this next step" }));

    expect(onUseAsDraft).toHaveBeenCalledWith(
      "Continue: Replace result parsing with explicit child trace events when backend exposes them.",
      "tool_guidance",
    );
  });

  it("does not auto-collapse a historical subagent the user opened", async () => {
    const user = userEvent.setup();
    const { rerender } = renderTimeline(completedSubagentTree);
    await user.click(screen.getByRole("button", { name: /Action details|Run summary/ }));
    const subagent = within(screen.getByTestId("execution-tree")).getByRole("button", { name: /Find the WebUI trace requirements/ });

    await user.click(subagent);
    rerender(<Timeline session={reduceRawEvents([...completedSubagentTree])} />);

    expect(within(screen.getByTestId("execution-tree")).getByRole("button", { name: /Find the WebUI trace requirements/ })).toHaveAttribute("aria-expanded", "true");
    expect(within(screen.getAllByTestId("tool-details")[0]).getAllByText(/WebUI must render trace details/).length).toBeGreaterThan(0);
  });

  it("shows an in-panel jump control when live activity arrives while browsing history", () => {
    const { rerender } = render(<ScrollHarness events={completedTurn} />);
    const scrollRoot = screen.getByTestId("scroll-root");
    Object.defineProperties(scrollRoot, {
      clientHeight: { configurable: true, value: 300 },
      scrollHeight: { configurable: true, value: 1200 },
      scrollTop: { configurable: true, value: 0 },
    });

    fireEvent.wheel(scrollRoot);
    fireEvent.scroll(scrollRoot);
    rerender(<ScrollHarness events={[...completedTurn, ...messageOnlyTurn]} />);

    expect(screen.getByRole("button", { name: /jump to latest/i })).toBeInTheDocument();
  });

  it("shows the jump control when a local guidance receipt arrives while browsing history", () => {
    const { rerender } = render(<ScrollHarness events={completedTurn} />);
    const scrollRoot = screen.getByTestId("scroll-root");
    Object.defineProperties(scrollRoot, {
      clientHeight: { configurable: true, value: 300 },
      scrollHeight: { configurable: true, value: 1200 },
      scrollTop: { configurable: true, value: 0 },
    });

    fireEvent.wheel(scrollRoot);
    fireEvent.scroll(scrollRoot);
    rerender(<ScrollHarness events={completedTurn} guidanceReceipts={[{ id: 1, text: "check tests first" }]} />);

    expect(screen.getByRole("button", { name: /jump to latest/i })).toBeInTheDocument();

    Object.defineProperty(scrollRoot, "scrollTop", { configurable: true, value: 900 });
    fireEvent.scroll(scrollRoot);

    expect(screen.getByRole("button", { name: /jump to latest/i })).toBeInTheDocument();
  });

  it("follows local guidance receipts when already at the latest message", () => {
    const { scrollIntoView } = installScrollIntoViewSpy();
    const { rerender } = render(<ScrollHarness events={completedTurn} />);

    rerender(<ScrollHarness events={completedTurn} guidanceReceipts={[{ id: 1, text: "check tests first" }]} />);

    expect(scrollIntoView).toHaveBeenCalledWith({ behavior: "auto", block: "end" });
  });

  it("keeps following stable when the user keeps scrolling past the bottom", () => {
    const { scrollIntoView } = installScrollIntoViewSpy();
    const { rerender } = render(<ScrollHarness events={completedTurn} />);
    const scrollRoot = screen.getByTestId("scroll-root");
    Object.defineProperties(scrollRoot, {
      clientHeight: { configurable: true, value: 300 },
      scrollHeight: { configurable: true, value: 1200 },
      scrollTop: { configurable: true, value: 900 },
    });

    fireEvent.wheel(scrollRoot, { deltaY: 120 });
    fireEvent.scroll(scrollRoot);

    expect(screen.queryByRole("button", { name: /latest/i })).toBeNull();

    rerender(<ScrollHarness events={completedTurn} guidanceReceipts={[{ id: 1, text: "check tests first" }]} />);

    expect(scrollIntoView).toHaveBeenCalledWith({ behavior: "auto", block: "end" });
  });

  it("pauses live follow when the scroll position moves away without a wheel event", () => {
    const { scrollIntoView } = installScrollIntoViewSpy();
    const { rerender } = render(<ScrollHarness events={runningStarted} />);
    const scrollRoot = screen.getByTestId("scroll-root");
    Object.defineProperties(scrollRoot, {
      clientHeight: { configurable: true, value: 300 },
      scrollHeight: { configurable: true, value: 1200 },
      scrollTop: { configurable: true, value: 420 },
    });

    fireEvent.scroll(scrollRoot);
    rerender(<ScrollHarness events={runningSubagent} />);

    expect(scrollIntoView).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: /jump to latest/i })).toBeInTheDocument();
  });

  it("follows running updates to the newest content instead of pinning the turn header", () => {
    const { scrollIntoView } = installScrollIntoViewSpy();
    const { rerender } = render(<ScrollHarness events={runningStarted} />);

    rerender(<ScrollHarness events={runningSubagent} />);

    expect(scrollIntoView).toHaveBeenCalledWith({ behavior: "auto", block: "end" });
  });

  it("opens saved completed history at the latest answer without adding jump chrome", () => {
    const { scrollIntoView } = installScrollIntoViewSpy();
    const { rerender } = render(<ScrollHarness events={[]} initialHistoryFocus="answer" />);
    const scrollRoot = screen.getByTestId("scroll-root");
    Object.defineProperties(scrollRoot, {
      clientHeight: { configurable: true, value: 300 },
      scrollHeight: { configurable: true, value: 1200 },
      scrollTop: { configurable: true, value: 0 },
    });

    rerender(<ScrollHarness events={completedTurn} initialHistoryFocus="answer" />);

    expect(scrollIntoView).toHaveBeenCalledWith({ behavior: "auto", block: "start" });
    expect(screen.queryByRole("button", { name: "Jump to latest" })).toBeNull();
  });

  it("keeps saved history reading stable but surfaces later activity", () => {
    const { rerender } = render(<ScrollHarness events={[]} initialHistoryFocus="answer" />);
    const scrollRoot = screen.getByTestId("scroll-root");
    Object.defineProperties(scrollRoot, {
      clientHeight: { configurable: true, value: 300 },
      scrollHeight: { configurable: true, value: 1200 },
      scrollTop: { configurable: true, value: 0 },
    });

    rerender(<ScrollHarness events={completedTurn} initialHistoryFocus="answer" />);
    expect(screen.queryByRole("button", { name: /latest/i })).toBeNull();

    rerender(
      <ScrollHarness
        events={completedTurn}
        guidanceReceipts={[{ id: 1, text: "check tests first" }]}
        initialHistoryFocus="answer"
      />,
    );

    expect(screen.getByRole("button", { name: /jump to latest/i })).toBeInTheDocument();
  });

  it("does not change scroll position while the user is selecting text", () => {
    const { scrollIntoView } = installScrollIntoViewSpy();
    const { rerender } = render(<ScrollHarness events={completedTurn} />);
    const scrollRoot = screen.getByTestId("scroll-root");
    Object.defineProperties(scrollRoot, {
      scrollTop: { configurable: true, writable: true, value: 144 },
    });

    fireEvent.pointerDown(scrollRoot);
    scrollRoot.scrollTop = 260;
    fireEvent.scroll(scrollRoot);
    expect(scrollRoot.scrollTop).toBe(260);

    scrollRoot.scrollTop = 260;
    rerender(<ScrollHarness events={completedTurn} guidanceReceipts={[{ id: 1, text: "check tests first" }]} />);

    expect(scrollIntoView).not.toHaveBeenCalled();
    expect(scrollRoot.scrollTop).toBe(260);
    expect(screen.getByRole("button", { name: /jump to latest/i })).toBeInTheDocument();
  });

  it("does not force live follow during a mobile long press before selection appears", () => {
    const { scrollIntoView } = installScrollIntoViewSpy();
    const { rerender } = render(<ScrollHarness events={completedTurn} />);
    const scrollRoot = screen.getByTestId("scroll-root");
    Object.defineProperties(scrollRoot, {
      clientHeight: { configurable: true, value: 300 },
      scrollHeight: { configurable: true, value: 1200 },
      scrollTop: { configurable: true, writable: true, value: 900 },
    });

    fireEvent.touchStart(scrollRoot, { touches: [{ clientY: 180 }] });
    fireEvent.scroll(scrollRoot);
    rerender(<ScrollHarness events={completedTurn} guidanceReceipts={[{ id: 1, text: "check tests first" }]} />);

    expect(scrollIntoView).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: /jump to latest/i })).toBeInTheDocument();
  });

  it("does not synthesize edge scrolling while selecting text", () => {
    render(<ScrollHarness events={completedTurn} />);
    const scrollRoot = screen.getByTestId("scroll-root");
    Object.defineProperties(scrollRoot, {
      clientHeight: { configurable: true, value: 300 },
      scrollHeight: { configurable: true, value: 1200 },
      scrollTop: { configurable: true, writable: true, value: 144 },
    });
    scrollRoot.getBoundingClientRect = vi.fn(() => ({
      top: 0,
      right: 600,
      bottom: 300,
      left: 0,
      width: 600,
      height: 300,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    }));
    const requestAnimationFrameSpy = vi.spyOn(window, "requestAnimationFrame");

    fireEvent.pointerDown(scrollRoot, { clientY: 120, pointerType: "mouse" });
    const pointerMove = new Event("pointermove");
    Object.defineProperty(pointerMove, "clientY", { value: 292 });
    Object.defineProperty(pointerMove, "pointerType", { value: "mouse" });
    window.dispatchEvent(pointerMove);

    expect(scrollRoot.scrollTop).toBe(144);
    expect(requestAnimationFrameSpy).not.toHaveBeenCalled();
  });

  it("does not force live follow scroll when text selection starts outside the pointer path", () => {
    const { scrollIntoView } = installScrollIntoViewSpy();
    const { rerender } = render(<ScrollHarness events={completedTurn} />);
    const scrollRoot = screen.getByTestId("scroll-root");
    Object.defineProperties(scrollRoot, {
      scrollTop: { configurable: true, writable: true, value: 180 },
    });
    vi.spyOn(document, "getSelection").mockReturnValue({
      isCollapsed: false,
      toString: () => "selected answer text",
    } as Selection);

    document.dispatchEvent(new Event("selectionchange"));
    scrollRoot.scrollTop = 480;
    fireEvent.scroll(scrollRoot);

    expect(scrollRoot.scrollTop).toBe(480);

    scrollRoot.scrollTop = 480;
    rerender(<ScrollHarness events={completedTurn} guidanceReceipts={[{ id: 1, text: "check tests first" }]} />);

    expect(scrollIntoView).not.toHaveBeenCalled();
    expect(scrollRoot.scrollTop).toBe(480);
    expect(screen.getByRole("button", { name: /jump to latest/i })).toBeInTheDocument();
  });

  it("hides return-to-latest while browsing older messages until new activity arrives", () => {
    const { scrollIntoView } = installScrollIntoViewSpy();
    render(<ScrollHarness events={completedTurn} />);
    const scrollRoot = screen.getByTestId("scroll-root");
    Object.defineProperties(scrollRoot, {
      clientHeight: { configurable: true, value: 300 },
      scrollHeight: { configurable: true, value: 1200 },
      scrollTop: { configurable: true, value: 0 },
    });

    act(() => {
      fireEvent.wheel(scrollRoot);
      fireEvent.scroll(scrollRoot);
    });

    expect(screen.queryByRole("button", { name: /latest/i })).toBeNull();
    expect(scrollIntoView).not.toHaveBeenCalled();
  });

  it("copies structured args from an expanded tool node", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    renderTimeline(completedTurn);

    await user.click(screen.getByRole("button", { name: /Action details|Run summary/ }));
    await user.click(within(screen.getByTestId("execution-tree")).getByRole("button", { name: /List current directory/ }));
    await user.click(screen.getByRole("button", { name: "Copy input" }));

    expect(writeText).toHaveBeenCalledWith(JSON.stringify({ path: "." }, null, 2));
    expect(screen.getByRole("button", { name: "Copied" })).toBeInTheDocument();
  });

  it("copies a complete action record from an expanded tool node", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    renderTimeline(completedTurn);

    await user.click(screen.getByRole("button", { name: /Action details|Run summary/ }));
    await user.click(within(screen.getByTestId("execution-tree")).getByRole("button", { name: /List current directory/ }));
    await user.click(screen.getByRole("button", { name: "Copy action details" }));

    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("Action: List current directory"));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("Status: done"));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining('"path": "."'));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("Output:\nREADME.md\nmain.go"));
  });

  it("copies all execution details including nested subagent tool calls", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    renderTimeline(completedSubagentTree);

    await user.click(screen.getByRole("button", { name: /Action details|Run summary/ }));
    fireEvent.click(within(screen.getByTestId("execution-tree-actions")).getByRole("button", { name: "Copy details" }));
    await user.click(screen.getByRole("button", { name: "Copy all details" }));

    expect(writeText).toHaveBeenCalledTimes(1);
    const copied = writeText.mock.calls[0][0] as string;
    expect(copied).toContain("# 1 Find the WebUI trace requirements");
    expect(copied).toContain("Tool: subagent_run");
    expect(copied).toContain('"task": "Find the WebUI trace requirements"');
    expect(copied).toContain("# 1.1 List docs");
    expect(copied).toContain("Tool: list_files");
    expect(copied).toContain('"path": "docs"');
    expect(copied).toContain("# 1.2 docs/webui-product-design.md");
    expect(copied).toContain('"max_bytes": 4096');
    expect(copied).toContain("# 1.3 Search");
    expect(copied).toContain("Tool: MCP_search");
    expect(copied).toContain("# 2.1 docs/focused-tasks.md");
    expect(screen.queryByRole("button", { name: "Copy all details" })).toBeNull();
  });

  it("copies only failed execution details from work details", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    renderTimeline(toolError);

    await user.click(screen.getByRole("button", { name: /Action details|Run summary/ }));
    await user.click(within(screen.getByTestId("execution-tree-actions")).getByRole("button", { name: "Copy issues" }));

    expect(writeText).toHaveBeenCalledTimes(1);
    const copied = writeText.mock.calls[0][0] as string;
    expect(copied).toContain("# issue 1: make");
    expect(copied).toContain("Tool: shell");
    expect(copied).toContain("Status: failed");
    expect(copied).toContain('"command": "make"');
    expect(copied).toContain("Output:\nmake: *** No rule to make target. Stop.");
    expect(copied).not.toContain("list_files");
  });

  it("copies one nested subagent tool call record", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    renderTimeline(completedSubagentTree);

    await user.click(screen.getByRole("button", { name: /Action details|Run summary/ }));
    const executionTree = screen.getByTestId("execution-tree");
    await user.click(within(executionTree).getByRole("button", { name: /Find the WebUI trace requirements/ }));
    await user.click(within(executionTree).getByRole("button", { name: /File action docs\/webui-product-design\.md/ }));
    const details = screen.getAllByTestId("tool-details").at(-1);
    expect(details).toBeDefined();
    await user.click(within(details as HTMLElement).getByRole("button", { name: "Copy action details" }));

    expect(writeText).toHaveBeenCalledTimes(1);
    const copied = writeText.mock.calls[0][0] as string;
    expect(copied).toContain("Action: docs/webui-product-design.md");
    expect(copied).toContain("Tool: read_file");
    expect(copied).toContain("Exit: 0");
    expect(copied).toContain('"path": "docs/webui-product-design.md"');
    expect(copied).toContain('"max_bytes": 4096');
    expect(copied).not.toContain("Tool: subagent_run");
  });

  it("turns an expanded tool result into a follow-up draft", async () => {
    const user = userEvent.setup();
    const onUseAsDraft = vi.fn();
    renderTimeline(completedTurn, undefined, undefined, onUseAsDraft);

    await user.click(screen.getByRole("button", { name: /Action details|Run summary/ }));
    await user.click(within(screen.getByTestId("execution-tree")).getByRole("button", { name: /List current directory/ }));
    await user.click(screen.getByRole("button", { name: "Use output" }));

    expect(onUseAsDraft).toHaveBeenCalledWith(
      [
        "Use this output in the next step:",
        "Action: List current directory",
        "Tool: list_files",
        "Output:\nREADME.md\nmain.go",
      ].join("\n"),
      "tool_result",
    );
  });

  it("surfaces Next guidance from a failed tool result", async () => {
    const user = userEvent.setup();
    const onUseAsDraft = vi.fn();
    renderTimeline(toolError, undefined, undefined, onUseAsDraft);

    await user.click(screen.getByRole("button", { name: /Action details|Run summary/ }));
    expect(screen.getByTestId("node-next-hint")).toHaveTextContent("check the Makefile path");
    await user.click(within(screen.getByTestId("execution-tree")).getByRole("button", { name: /make/ }));
    await user.click(screen.getByRole("button", { name: "Use as message" }));

    expect(screen.getByTestId("next-hint")).toHaveTextContent("check the Makefile path");
    expect(onUseAsDraft).toHaveBeenCalledWith("Continue: check the Makefile path", "tool_guidance");
  });

  it("turns a failed tool into a retry draft", async () => {
    const user = userEvent.setup();
    const onUseAsDraft = vi.fn();
    renderTimeline(toolError, undefined, undefined, onUseAsDraft);

    await user.click(screen.getByRole("button", { name: /Action details|Run summary/ }));
    await user.click(within(screen.getByTestId("execution-tree")).getByRole("button", { name: /make/ }));
    await user.click(screen.getByRole("button", { name: "Retry action" }));

    expect(onUseAsDraft).toHaveBeenCalledWith(
      [
        "Retry the failed action: make",
        "Tool: shell",
        "Next: check the Makefile path",
        'Args:\n{\n  "command": "make"\n}',
      ].join("\n"),
      "retry",
    );
  });

  it("turns runtime errors into an actionable diagnostic block", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    const onUseAsDraft = vi.fn();
    renderTimeline(turnError, undefined, undefined, onUseAsDraft);

    const card = screen.getByTestId("error-card");
    expect(card).toHaveTextContent("Provider returned an error");
    expect(card).toHaveTextContent("The model provider returned HTTP 503.");
    expect(card).toHaveTextContent("upstream_5xx");
    expect(card).toHaveTextContent("recoverable");
    expect(card).toHaveTextContent("continue from the message box below");
    expect(card).toHaveTextContent("details stay attached to this chat");

    await user.click(screen.getByRole("button", { name: "Continue with this" }));
    expect(onUseAsDraft).toHaveBeenCalledWith("Continue after upstream_5xx: provider returned 503", "error");

    await user.click(screen.getByRole("button", { name: "Copy diagnostic" }));

    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("code: upstream_5xx"));
    expect(screen.getByRole("button", { name: "Copied" })).toBeInTheDocument();
  });

  it("copies runtime diagnostics with the shared fallback path", async () => {
    const user = userEvent.setup();
    const execCommand = vi.fn().mockReturnValue(true);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: undefined });
    Object.defineProperty(document, "execCommand", { configurable: true, value: execCommand });
    renderTimeline(turnError);

    await user.click(screen.getByRole("button", { name: "Copy diagnostic" }));

    expect(execCommand).toHaveBeenCalledWith("copy");
    expect(screen.getByRole("button", { name: "Copied" })).toBeInTheDocument();
  });

  it("offers a continuation draft when a turn reaches the action limit", async () => {
    const user = userEvent.setup();
    const onUseAsDraft = vi.fn();
    renderTimeline(maxTurns, undefined, undefined, onUseAsDraft);

    expect(screen.getByTestId("fallback-answer")).toHaveTextContent("Needs final answer");
    expect(screen.getByTestId("fallback-answer")).toHaveTextContent("Affent reached its action limit");
    expect(screen.getByTestId("continuation-card")).toHaveTextContent("Final answer not produced");
    expect(screen.getByTestId("continuation-card")).toHaveTextContent("Affent gathered evidence");
    expect(screen.queryByTestId("turn-navigator")).toBeNull();
    await user.click(screen.getByRole("button", { name: "Ask for final answer" }));

    expect(onUseAsDraft).toHaveBeenCalledWith(
      "Do not call more tools. Based only on the evidence already gathered in this chat, produce the final answer.",
      "continuation",
    );
  });

  it("summarizes historical action-limit turns as handoffs without repeating the status label", () => {
    renderTimeline([...maxTurns, ...messageOnlyTurn]);

    const firstActivity = screen.getAllByTestId("agent-activity")[0];
    const digest = within(firstActivity).getByTestId("agent-activity-digest");
    expect(firstActivity.textContent?.match(/Continued/g)).toHaveLength(1);
    expect(digest).toHaveTextContent("Handoff");
    expect(digest).toHaveTextContent("Ran 1 action; message 2 continued the task.");
    expect(digest).toHaveAccessibleName("Handoff · Ran 1 action; message 2 continued the task.");
    expect(digest.textContent?.replace(/\s+/g, " ").trim()).toContain("Handoff · Ran 1 action; message 2 continued the task.");
    expect(digest.textContent?.replace(/\s+/g, " ").trim()).not.toContain("HandoffRan");
    expect(digest).not.toHaveTextContent("This message reached the action limit");
    expect(screen.queryByTestId("work-thread")).toBeNull();
  });

  it("keeps historical handoff tool details hidden in the main scan path", () => {
    renderTimeline([
      { id: 0, type: "turn.start", data: { turn_id: "t1" } },
      {
        id: 1,
        type: "tool.request",
        data: { turn_id: "t1", call_id: "c1", tool: "memory", args: { action: "list" }, args_truncated: false, args_bytes: 18, args_omitted_bytes: 0, args_cap_bytes: 8192 },
      },
      { id: 2, type: "tool.result", data: { call_id: "c1", exit_code: 0, duration_ms: 3, result_summary: "ok", result: "ok", result_truncated: false, result_bytes: 2, result_omitted_bytes: 0, result_cap_bytes: 8192 } },
      {
        id: 3,
        type: "tool.request",
        data: { turn_id: "t1", call_id: "c2", tool: "memory", args: { action: "search", query: "affine" }, args_truncated: false, args_bytes: 36, args_omitted_bytes: 0, args_cap_bytes: 8192 },
      },
      { id: 4, type: "tool.result", data: { call_id: "c2", exit_code: 0, duration_ms: 4, result_summary: "ok", result: "ok", result_truncated: false, result_bytes: 2, result_omitted_bytes: 0, result_cap_bytes: 8192 } },
      { id: 5, type: "turn.end", data: { turn_id: "t1", reason: "max_turns", tool_stats: { tool_requests: 2, tool_duration_ms: 7 } } },
      ...messageOnlyTurn.map((event) => ({ ...event, id: event.id + 100 })),
    ]);

    expect(screen.queryByTestId("work-thread")).toBeNull();
  });

  it("keeps confirmed memory update content visible on historical turns", () => {
    renderTimeline([
      { id: 0, type: "turn.start", data: { turn_id: "t1" } },
      { id: 1, type: "user.message", data: { turn_id: "t1", text: "remember the market reporting convention" } },
      {
        id: 2,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "mem-add",
          tool: "memory",
          args: {
            action: "add",
            target: "memory",
            topic: "markets",
            content: "Market reports must include MEM-STOCK-73 and source-led confidence.",
          },
          args_truncated: false,
          args_bytes: 116,
          args_omitted_bytes: 0,
          args_cap_bytes: 8192,
        },
      },
      {
        id: 3,
        type: "tool.result",
        data: {
          call_id: "mem-add",
          exit_code: 0,
          duration_ms: 4,
          result_summary: "{\"ok\":true,\"target\":\"memory\",\"topic\":\"markets\",\"message\":\"added\"}",
          result: "{\"ok\":true,\"target\":\"memory\",\"topic\":\"markets\",\"message\":\"added\"}",
          result_truncated: false,
          result_bytes: 64,
          result_omitted_bytes: 0,
          result_cap_bytes: 8192,
        },
      },
      { id: 4, type: "turn.end", data: { turn_id: "t1", reason: "completed", tool_stats: { tool_requests: 1, memory_updates: 1, memory_update_add: 1 } } },
      ...messageOnlyTurn,
    ]);

    expect(screen.queryByTestId("work-thread")).toBeNull();
    expect(screen.getByTestId("memory-update-strip")).toHaveTextContent("Saved memory");
    expect(screen.getByTestId("memory-update-strip")).toHaveTextContent("memory:markets");
    expect(screen.getByTestId("memory-update-strip")).toHaveTextContent("MEM-STOCK-73");
  });

  it("keeps runtime surface details out of the default timeline", () => {
    renderTimeline([
      { id: 0, type: "turn.start", data: { turn_id: "t1" } },
      { id: 1, type: "user.message", data: { turn_id: "t1", text: "research taostats" } },
      {
        id: 2,
        type: "runtime.surface",
        data: {
          turn_id: "t1",
          tool_count: 4,
          tools: [
            { name: "web_fetch", group: "Research" },
            { name: "web_search", group: "Research" },
            { name: "memory", group: "Memory" },
            { name: "run_task", group: "Core" },
          ],
          capabilities: { web_fetch: true, web_search: true, memory: true, focused_tasks: true, workspace_tools: ["read_file"] },
          max_turn_steps: 20,
          tool_result_event_cap_bytes: 262144,
          tool_result_context_budget_bytes: 32768,
        },
      },
      { id: 3, type: "error", data: { turn_id: "t1", code: "llm_request", message: "connection refused", recoverable: false } },
    ]);

    expect(screen.queryByTestId("runtime-surface-strip")).toBeNull();
    expect(screen.queryByText("Runtime surface")).toBeNull();
    expect(screen.queryByText("web_search")).toBeNull();
    expect(screen.getByTestId("error-card")).toHaveTextContent("connection refused");
  });

  it("renders compact web evidence while keeping the full source available", () => {
    renderTimeline(webFetchTurn);

    const evidence = screen.getByTestId("agent-activity-digest-evidence");
    expect(evidence).toHaveTextContent("Source");
    expect(evidence).toHaveTextContent("affine.io");
    expect(evidence).toHaveTextContent("Fetched affine.io");
    expect(screen.getByTestId("agent-activity-digest").textContent?.replace(/\s+/g, " ").trim()).toContain("Fetched affine.io");
    expect(evidence).not.toHaveTextContent("https://www.affine.io/");
    const source = screen.getByRole("link", { name: "Fetched: https://www.affine.io/" });
    expect(source).toHaveAttribute("href", "https://www.affine.io/");
    expect(source).toHaveAttribute("title", "https://www.affine.io/");
    expect(source).toHaveAttribute("target", "_blank");
    expect(source).toHaveAttribute("rel", "noreferrer");
  });

  it("opens truncated result artifacts inside the current session surface", async () => {
    const user = userEvent.setup();
    const onOpenArtifact = vi.fn();
    renderTimeline(resultTruncated, "s1", onOpenArtifact);

    expect(screen.getByTestId("turn-artifacts")).toHaveTextContent("000001-c1.txt");
    await user.click(within(screen.getByTestId("turn-artifacts")).getByRole("button", { name: "Open file" }));

    expect(onOpenArtifact).toHaveBeenCalledWith(".affent/artifacts/tool-results/000001-c1.txt");
  });
});

function ScrollHarness({
  events,
  guidanceReceipts = [],
  initialHistoryFocus = "latest",
}: {
  events: RawEvent[];
  guidanceReceipts?: { id: number; text: string }[];
  initialHistoryFocus?: "answer" | "latest";
}) {
  const ref = useRef<HTMLDivElement | null>(null);
  return (
    <div ref={ref} data-testid="scroll-root">
      <Timeline
        session={reduceRawEvents(events)}
        sessionId="s1"
        guidanceReceipts={guidanceReceipts}
        scrollRootRef={ref}
        initialHistoryFocus={initialHistoryFocus}
      />
    </div>
  );
}

function installScrollIntoViewSpy() {
  const scrollIntoView = vi.fn();
  Object.defineProperty(Element.prototype, "scrollIntoView", {
    configurable: true,
    value: scrollIntoView,
  });
  return { scrollIntoView };
}

const messageOnlyTurn: RawEvent[] = [
  { id: 20, type: "turn.start", data: { turn_id: "t2" } },
  { id: 21, type: "user.message", data: { turn_id: "t2", text: "message only" } },
  { id: 22, type: "message.done", data: { turn_id: "t2", text: "no tool needed", finish_reason: "stop" } },
  { id: 23, type: "turn.end", data: { turn_id: "t2", reason: "completed" } },
];

const webFetchTurn: RawEvent[] = [
  { id: 40, type: "turn.start", data: { turn_id: "t4" } },
  { id: 41, type: "user.message", data: { turn_id: "t4", text: "research affine" } },
  {
    id: 42,
    type: "tool.request",
    data: {
      turn_id: "t4",
      call_id: "fetch-affine",
      tool: "web_fetch",
      args: { url: "https://www.affine.io/" },
      args_truncated: false,
      args_bytes: 32,
      args_omitted_bytes: 0,
      args_cap_bytes: 8192,
    },
  },
  {
    id: 43,
    type: "tool.result",
    data: {
      call_id: "fetch-affine",
      exit_code: 0,
      duration_ms: 40,
      result_summary: "AFFINE subnet 120",
      result: "AFFINE subnet 120",
      result_truncated: false,
      result_bytes: 20,
      result_omitted_bytes: 0,
      result_cap_bytes: 8192,
    },
  },
  { id: 44, type: "message.done", data: { turn_id: "t4", text: "Affine is subnet 120.", finish_reason: "stop" } },
  { id: 45, type: "turn.end", data: { turn_id: "t4", reason: "completed" } },
];

const runningStarted: RawEvent[] = [
  { id: 30, type: "turn.start", data: { turn_id: "t3" } },
  { id: 31, type: "user.message", data: { turn_id: "t3", text: "summarize the repo" } },
];
