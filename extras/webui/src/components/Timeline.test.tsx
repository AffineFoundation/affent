import { fireEvent, render, screen, within } from "@testing-library/react";
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
  onUseAsDraft?: (content: string) => void,
) {
  const session = reduceRawEvents(raws);
  return render(<Timeline session={session} sessionId={sessionId} onOpenArtifact={onOpenArtifact} onUseAsDraft={onUseAsDraft} />);
}

async function openTimelineTools(user: ReturnType<typeof userEvent.setup>) {
  let toolbar = screen.queryByTestId("timeline-toolbar");
  if (!toolbar) {
    await user.click(screen.getByRole("button", { name: "Find" }));
    toolbar = await screen.findByTestId("timeline-toolbar");
  }
  if (!toolbar.hasAttribute("open")) {
    await user.click(within(toolbar).getByText("Find in chat"));
  }
}

async function openTimelineFilters(user: ReturnType<typeof userEvent.setup>) {
  const advanced = screen.getByTestId("timeline-advanced-filter");
  if (!advanced.hasAttribute("open")) {
    await user.click(within(advanced).getByText("Filter results"));
  }
}

describe("Timeline", () => {
  it("renders the assistant answer with folded completed work details", async () => {
    const user = userEvent.setup();
    renderTimeline(completedTurn);
    expect(screen.getByTestId("msg-assistant")).toHaveTextContent("There are two files.");
    expect(screen.getByTestId("agent-activity")).toHaveTextContent("Agent activity");
    expect(screen.getByTestId("agent-activity-digest")).toHaveTextContent("Action summary");
    expect(screen.getByTestId("agent-activity-digest")).toHaveTextContent("README.md main.go");
    expect(screen.getByTestId("agent-activity").textContent?.match(/README\.md main\.go/g)).toHaveLength(1);
    expect(screen.queryByTestId("agent-activity-brief")).toBeNull();
    expect(screen.getByRole("button", { name: /Agent activity/ })).toHaveAttribute("aria-expanded", "false");
    await user.click(screen.getByRole("button", { name: /Agent activity/ }));
    expect(screen.getByTestId("agent-activity-brief")).toHaveTextContent("Goal");
    expect(screen.getByTestId("agent-activity-brief")).toHaveTextContent("list the files");
    expect(screen.getByTestId("agent-activity-brief")).toHaveTextContent("Result");
    expect(screen.getByTestId("agent-activity-brief")).toHaveTextContent("README.md main.go");
    expect(screen.getByTestId("agent-activity")).not.toHaveTextContent("Working plan");
    expect(screen.getByTestId("agent-activity")).toHaveTextContent("List current directory");
    expect(screen.queryByTestId("execution-tree")).toBeNull();
    await user.click(screen.getByRole("button", { name: /Tool details/ }));
    expect(screen.getByTestId("execution-tree")).toHaveTextContent("List current directory");
    const toolDetails = screen.getByRole("button", { name: /Tool details/ });
    expect(toolDetails).toHaveTextContent("Tool details");
    expect(toolDetails).toHaveTextContent("1 completed call");
    expect(toolDetails).toHaveAccessibleName("Tool details · 1 completed call · List files: . · 12ms");
    expect(toolDetails.textContent?.replace(/\s+/g, " ").trim()).toContain("Tool details · 1 completed call · List files: .");
    expect(screen.getByTestId("turn-head")).toHaveTextContent("138 tokens");
    expect(screen.queryByTestId("turn-runtime-meta")).toBeNull();
    expect(screen.queryByText("Technical trace")).toBeNull();
  });

  it("shows turn summaries for quick multi-turn navigation", () => {
    renderTimeline([...completedTurn, ...messageOnlyTurn]);

    expect(screen.getByTestId("conversation-map")).toBeInTheDocument();
    expect(screen.queryByTestId("timeline-toolbar")).toBeNull();
    const nav = screen.getByTestId("turn-navigator");
    expect(within(nav).getByText("Messages")).toBeInTheDocument();
    expect(within(nav).getByRole("button", { name: "Find" })).toHaveAttribute("aria-pressed", "false");
    expect(within(nav).getByText("2 messages · 2 done · 1 action · 138 tokens")).toBeInTheDocument();
    expect(within(nav).queryByTestId("turn-nav-current")).toBeNull();
    expect(within(nav).getByTestId("turn-nav-progress")).toBeInTheDocument();
    expect(within(nav).getByTestId("turn-nav-glance")).toBeInTheDocument();
    expect(within(nav).getByTestId("turn-nav-glance")).toHaveTextContent("list the files");
    expect(within(nav).getByTestId("turn-nav-glance")).toHaveTextContent("Action summary");
    expect(within(nav).getByTestId("turn-nav-glance")).toHaveTextContent("README.md main.go");
    expect(within(nav).getByTestId("turn-nav-glance")).toHaveTextContent("message only");
    expect(within(nav).getByTestId("turn-nav-glance")).toHaveTextContent("Answer");
    expect(within(nav).getByTestId("turn-nav-glance")).toHaveTextContent("no tool needed");
    expect(within(nav).getAllByText("Current · Done")).toHaveLength(1);
    expect(within(nav).getAllByText("message only").length).toBeGreaterThanOrEqual(1);
    expect(within(nav).getAllByText("Done").length).toBeGreaterThanOrEqual(1);
    expect(within(nav).queryByText("1 action")).toBeNull();
    expect(within(nav).getByRole("link", { name: /Jump to message 2: message only \(current\)/ })).toHaveAttribute("href", "#turn-2");
    expect(within(nav).getByRole("link", { name: /Jump to message 1: list the files/ })).toHaveAttribute("href", "#turn-1");
    expect(within(nav).getByRole("link", { name: /Jump to message 2: message only \(current\)/ })).toHaveAttribute("data-current", "true");
    expect(within(nav).getByRole("link", { name: /Message 1: list the files.*Action summary: README.md main.go/ })).toHaveAttribute("href", "#turn-1");
    expect(within(nav).getByRole("link", { name: /Message 2: message only.*Answer: no tool needed/ })).toHaveAttribute("href", "#turn-2");
    expect(within(nav).getByRole("link", { name: /Message 2: message only.*current/ })).toHaveAttribute("data-current", "true");
    expect(screen.getAllByTestId("turn-title").map((node) => node.textContent)).toEqual([
      "list the files",
      "message only",
    ]);
    expect(screen.queryByTestId("turn-boundary")).toBeNull();
    const heads = screen.getAllByTestId("turn-head");
    expect(heads[0]).toHaveAttribute("data-visible", "true");
    expect(heads[1]).toHaveAttribute("data-visible", "true");
    expect(heads[0]).toHaveTextContent("Message 1");
    expect(heads[0]).toHaveTextContent("list the files");
    expect(heads[0]).toHaveTextContent("Done");
    expect(heads[0]).toHaveTextContent("1 action");
    expect(heads[0]).toHaveTextContent("12ms");
    expect(heads[0]).toHaveTextContent("138 tokens");
    expect(heads[1]).toHaveTextContent("Message 2");
    expect(heads[1]).toHaveTextContent("message only");
    expect(heads[1]).toHaveTextContent("Done");
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
    render(
      <Timeline
        session={reduceRawEvents([])}
        savedChatCount={3}
        latestChat={{ title: "affine research", meta: "sess_9a7...f4b20d · 2026-05-24 17:37 UTC" }}
        onOpenLatestChat={onOpenLatestChat}
      />,
    );

    expect(screen.getByTestId("timeline-empty")).toHaveTextContent("affine research");
    expect(screen.queryByTestId("timeline")).toBeNull();
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
    expect(screen.getByTestId("starter-preview")).toHaveTextContent("Find the failing test or runtime error");
    await user.click(screen.getByRole("button", { name: "Use draft" }));

    expect(onUseAsDraft).toHaveBeenCalledWith(
      "Find the failing test or runtime error, explain the cause, and propose the smallest fix.",
      "starter",
    );
  });

  it("shows a pending turn immediately after a task is submitted", () => {
    const session = reduceRawEvents([]);
    render(<Timeline session={session} pendingMessage={{ text: "summarize the repo", kind: "task" }} />);

    expect(screen.queryByTestId("timeline-empty")).toBeNull();
    expect(screen.getByTestId("pending-turn")).toHaveTextContent("summarize the repo");
    expect(screen.getByTestId("pending-turn")).toHaveAttribute("data-kind", "task");
    expect(screen.getByTestId("turn-title")).toHaveTextContent("summarize the repo");
    expect(screen.getByTestId("pending-turn")).toHaveTextContent("Starting");
    expect(screen.getByTestId("pending-turn")).not.toHaveTextContent("events");
  });

  it("shows a pending follow-up as the next conversation message", () => {
    const session = reduceRawEvents(completedTurn);
    render(<Timeline session={session} pendingMessage={{ text: "explain main.go", kind: "task" }} />);

    expect(screen.getByTestId("pending-turn")).toHaveTextContent("explain main.go");
    expect(screen.getByTestId("pending-turn")).toHaveTextContent("Waiting for the next update in this chat.");
    expect(screen.getByTestId("pending-turn")).not.toHaveTextContent("Preparing the first update.");
    expect(screen.getByTestId("conversation-map")).toHaveTextContent("2 messages");
    expect(screen.queryByTestId("turn-nav-current")).toBeNull();
    const nav = screen.getByTestId("turn-navigator");
    const glance = within(nav).getByTestId("turn-nav-glance");
    expect(glance).toHaveTextContent("explain main.go");
    expect(glance).toHaveTextContent("Waiting");
    expect(glance).toHaveTextContent("Affent will add the next update here.");
    expect(glance).toHaveTextContent("Current · Sending");
    expect(within(nav).getByRole("link", { name: /Jump to pending message 2: explain main.go \(current\)/ })).toHaveAttribute(
      "href",
      "#pending-turn",
    );
    expect(within(nav).getByRole("link", { name: /Message 2: explain main.go.*Waiting: Affent will add the next update here.*current/ })).toHaveAttribute(
      "data-current",
      "true",
    );
  });

  it("shows pending live guidance as intervention instead of a new task", () => {
    const session = reduceRawEvents(runningSubagent);
    render(<Timeline session={session} pendingMessage={{ text: "Guidance for the current work: check tests first", kind: "guidance" }} />);

    expect(screen.getByTestId("pending-turn")).toHaveAttribute("data-kind", "guidance");
    expect(screen.getByTestId("pending-turn")).toHaveTextContent("Guidance");
    expect(screen.getByTestId("pending-turn")).toHaveTextContent("Live guidance");
    expect(screen.getByTestId("pending-turn")).toHaveTextContent("Sending");
    expect(screen.getByLabelText("Guidance message")).toHaveTextContent("check tests first");
    expect(screen.getByTestId("pending-turn")).toHaveTextContent("Adding guidance to the live turn.");
    expect(screen.getByTestId("pending-turn")).not.toHaveTextContent("Preparing the first update.");
  });

  it("keeps submitted live guidance visible as an editable receipt", async () => {
    const user = userEvent.setup();
    const onUseAsDraft = vi.fn();
    const session = reduceRawEvents(runningSubagent);
    render(
      <Timeline
        session={session}
        guidanceReceipts={[{ id: 1, text: "Guidance for the current work: check tests first" }]}
        onUseAsDraft={onUseAsDraft}
      />,
    );

    expect(screen.getByTestId("guidance-receipt")).toHaveTextContent("Guidance sent");
    expect(screen.getByTestId("guidance-receipt")).toHaveTextContent("check tests first");
    expect(screen.getByTestId("guidance-receipt")).toHaveTextContent("Added to the current turn.");
    expect(screen.queryByTestId("pending-turn")).toBeNull();

    await user.click(screen.getByRole("button", { name: "Edit guidance" }));

    expect(onUseAsDraft).toHaveBeenCalledWith("Guidance for the current work: check tests first", "guidance_receipt");
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
    expect(screen.getByTestId("conversation-map")).toHaveAttribute("data-density", "compact");
    expect(screen.queryByTestId("turn-nav-glance")).toBeNull();
    expect(screen.queryByRole("button", { name: "Copy answer" })).toBeNull();
    expect(screen.getByTestId("agent-activity")).toHaveTextContent("Agent activity");
    expect(screen.getByTestId("agent-activity-digest")).toHaveTextContent("Now");
    expect(screen.getByTestId("agent-activity-digest")).toHaveTextContent("Inspect docs for WebUI trace requirements");
    expect(screen.getByTestId("agent-activity-brief")).toHaveTextContent("Current focus");
    expect(screen.getByTestId("agent-activity-brief")).toHaveTextContent("Guidance can still be sent");
    expect(screen.getByTestId("agent-activity-tree")).toHaveTextContent("Delegate");
    expect(screen.getByTestId("agent-activity-tree")).toHaveTextContent("Inspect docs for WebUI trace requirements");
    expect(screen.getByTestId("agent-activity-tree")).toHaveTextContent("running");
    expect(screen.queryByTestId("work-thread")).toBeNull();
    expect(screen.queryByTestId("execution-tree")).toBeNull();
  });

  it("keeps running technical details available when filtering for actions", async () => {
    const user = userEvent.setup();
    renderTimeline(runningSubagent);

    await openTimelineTools(user);
    await openTimelineFilters(user);
    await user.click(screen.getByRole("button", { name: "With actions" }));

    expect(screen.getByTestId("work-thread")).toHaveTextContent("Tool details");
    expect(screen.getByTestId("work-thread")).toHaveTextContent("Inspect docs for WebUI trace requirements");
  });

  it("keeps activity tree text selectable by using a separate disclosure control", async () => {
    const user = userEvent.setup();
    renderTimeline(completedSubagentTree);

    await user.click(screen.getByRole("button", { name: /Agent activity/ }));

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

    await user.click(within(screen.getByTestId("agent-activity-brief")).getByRole("button", { name: "Guide turn" }));

    expect(onUseAsDraft).toHaveBeenCalledWith("Guidance for the current work: ", "tool_guidance");
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

  it("keeps completed reasoning available as one folded readable record", async () => {
    const user = userEvent.setup();
    renderTimeline(completedTurn);

    expect(screen.getByRole("button", { name: /Thinking/ })).toBeInTheDocument();
    expect(screen.queryByText("Technical trace")).toBeNull();
    expect(screen.getByTestId("agent-activity")).not.toHaveTextContent("I should list files.");
    expect(screen.queryByText(/\d+ events/)).not.toBeInTheDocument();
    expect(screen.queryByTestId("event-trace")).toBeNull();

    await user.click(screen.getByRole("button", { name: /Thinking/ }));

    expect(screen.getAllByText("I should list files.").length).toBeGreaterThan(0);
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

  it("hides chat review controls for a simple one-message answer", () => {
    renderTimeline(completedTurn);

    expect(screen.queryByTestId("timeline-toolbar")).toBeNull();
  });

  it("copies the assistant answer from the chat bubble", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    renderTimeline(completedTurn);

    await user.click(screen.getByRole("button", { name: "Copy answer" }));

    expect(writeText).toHaveBeenCalledWith("There are two files.");
    expect(screen.getByRole("button", { name: "Copied" })).toBeInTheDocument();
  });

  it("copies the user's message from the chat bubble", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    renderTimeline(completedTurn, "s1", undefined, vi.fn());

    await user.click(screen.getByRole("button", { name: "Copy message" }));

    expect(writeText).toHaveBeenCalledWith("list the files");
    expect(screen.getByRole("button", { name: "Copied" })).toBeInTheDocument();
  });

  it("turns an assistant answer into a follow-up draft", async () => {
    const user = userEvent.setup();
    const onUseAsDraft = vi.fn();
    renderTimeline(completedTurn, "s1", undefined, onUseAsDraft);

    await user.click(screen.getByRole("button", { name: "Ask follow-up" }));

    expect(onUseAsDraft).toHaveBeenCalledWith("Continue from this answer: There are two files.", "answer");
  });

  it("reuses a previous user message as an editable draft", async () => {
    const user = userEvent.setup();
    const onUseAsDraft = vi.fn();
    renderTimeline(completedTurn, "s1", undefined, onUseAsDraft);

    await user.click(screen.getByRole("button", { name: "Edit prompt" }));

    expect(onUseAsDraft).toHaveBeenCalledWith("list the files", "previous_message");
  });

  it("expands a tool card inline on click", async () => {
    const user = userEvent.setup();
    renderTimeline(completedTurn);
    expect(screen.queryByTestId("tool-details")).toBeNull();
    await user.click(screen.getByRole("button", { name: /Tool details/ }));
    await user.click(within(screen.getByTestId("execution-tree")).getByRole("button", { name: /List current directory/ }));
    const details = screen.getByTestId("tool-details");
    expect(details).toBeInTheDocument();
    expect(screen.getByTestId("action-inspector-summary")).toHaveTextContent("File action");
    expect(screen.getByTestId("action-inspector-summary")).toHaveTextContent("Status done");
    expect(screen.getByTestId("execution-node")).toHaveTextContent("done");
    expect(details).toHaveTextContent("list_files");
    expect(details).toHaveTextContent("Output");
    expect(details).toHaveTextContent("Action details");
    expect(details).toHaveTextContent("Technical details");
    expect(within(details).getByText("Trace")).toBeInTheDocument();
    expect(within(details).getByText(/\d+ records/)).toBeInTheDocument();
    expect(within(details).queryByTestId("event-trace")).toBeNull();

    await user.click(within(details).getByText("Trace"));

    expect(within(details).getByTestId("event-trace")).toBeInTheDocument();
  });

  it("shows an exit-code badge for a failed tool", () => {
    renderTimeline(toolError);
    fireEvent.click(screen.getByRole("button", { name: /Tool details/ }));
    const card = screen.getByTestId("execution-node");
    expect(card).toHaveAttribute("data-status", "error");
    expect(within(card).getByText(/exit 2/)).toBeInTheDocument();
    expect(screen.getByTestId("work-thread")).toHaveTextContent("Tool details");
    expect(screen.getByTestId("work-thread")).toHaveTextContent("1 call · 1 tool issue");
    expect(screen.getByTestId("work-summary")).not.toHaveTextContent("1 tool issue");
    expect(screen.getByTestId("work-summary")).toHaveTextContent("make");
  });

  it("badges a truncated result with an artifact", () => {
    renderTimeline(resultTruncated);
    expect(screen.getByTestId("fallback-answer")).toHaveTextContent("Action output was truncated");
    expect(screen.getByTestId("fallback-answer")).toHaveTextContent("line 1");
    expect(screen.getByTestId("fallback-answer")).toHaveTextContent("Full output is available below.");
    fireEvent.click(screen.getByRole("button", { name: /Tool details/ }));
    const card = screen.getByTestId("execution-node");
    expect(within(card).getByText("truncated")).toBeInTheDocument();
    expect(within(card).getByText("artifact")).toBeInTheDocument();
    expect(screen.getByTestId("work-summary")).toHaveTextContent("1 truncated");
    expect(screen.getByTestId("work-summary")).toHaveTextContent("1 file");
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

  it("shows original versus executed args for repaired tool calls", async () => {
    const user = userEvent.setup();
    renderTimeline(argsRepaired);

    await user.click(screen.getByRole("button", { name: /Tool details/ }));
    await user.click(within(screen.getByTestId("execution-tree")).getByRole("button", { name: /main.go/ }));

    expect(screen.getByTestId("repair-comparison")).toHaveTextContent("model request");
    expect(screen.getByTestId("repair-comparison")).toHaveTextContent("readFile");
    expect(screen.getByTestId("repair-comparison")).toHaveTextContent("\"filename\":\"main.go\"");
    expect(screen.getByTestId("repair-comparison")).toHaveTextContent("executed request");
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

  it("opens and folds flagged work from the tree controls", async () => {
    const user = userEvent.setup();
    renderTimeline(resultTruncated, "s1");

    expect(screen.queryByTestId("tool-details")).toBeNull();
    await user.click(screen.getByRole("button", { name: /Tool details/ }));
    await user.click(screen.getByRole("button", { name: "Show important" }));

    expect(screen.getByTestId("tool-details")).toHaveTextContent("Open artifact");

    await user.click(screen.getByRole("button", { name: "Collapse details" }));
    expect(screen.queryByTestId("tool-details")).toBeNull();
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
    expect(activity).not.toHaveTextContent("Needs attention");
    expect(screen.getByRole("button", { name: /Agent activity/ })).toHaveAttribute("aria-expanded", "false");
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
    expect(screen.getByRole("button", { name: /Agent activity/ })).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByTestId("agent-activity-brief")).toHaveTextContent("Next");
  });

  it("keeps historical subagents folded until the user opens them", async () => {
    const user = userEvent.setup();
    renderTimeline(completedSubagentTree);

    expect(screen.getByRole("button", { name: /Agent activity/ })).toHaveAttribute("aria-expanded", "false");
    expect(screen.getByTestId("agent-activity-digest-evidence")).toHaveTextContent("docs/webui-product-design.md");
    expect(screen.getByTestId("agent-activity-digest-evidence")).toHaveTextContent("webui trace");
    await user.click(screen.getByRole("button", { name: /Agent activity/ }));
    expect(screen.queryByTestId("agent-activity-digest-evidence")).toBeNull();
    const activityTree = screen.getByTestId("agent-activity-tree");
    const activityBrief = screen.getByTestId("agent-activity-brief");
    expect(activityBrief).toHaveTextContent("Goal");
    expect(activityBrief).toHaveTextContent("delegate docs inspection");
    expect(activityBrief).toHaveTextContent("Evidence");
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

    await user.click(screen.getByRole("button", { name: /Tool details/ }));
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
    expect(within(screen.getAllByTestId("tool-details")[0]).getAllByText("subagent_run").length).toBeGreaterThan(0);
    expect(within(screen.getAllByTestId("tool-details")[0]).getByText("Usage")).toBeInTheDocument();
    expect(within(screen.getAllByTestId("tool-details")[0]).getByText("392 tokens (310 in / 82 out)")).toBeInTheDocument();
    expect(screen.getByText("MCP action")).toBeInTheDocument();
    expect(screen.getByText("MCP_search")).toBeInTheDocument();
    expect(screen.getAllByText("subagent_01").length).toBeGreaterThan(0);
  });

  it("turns a processed activity next step into an editable message", async () => {
    const user = userEvent.setup();
    const onUseAsDraft = vi.fn();
    renderTimeline(completedSubagentTree, undefined, undefined, onUseAsDraft);

    await user.click(screen.getByRole("button", { name: /Agent activity/ }));
    await user.click(within(screen.getByTestId("agent-activity-brief")).getByRole("button", { name: "Use next step" }));

    expect(onUseAsDraft).toHaveBeenCalledWith(
      "Continue: Replace result parsing with explicit child trace events when backend exposes them.",
      "tool_guidance",
    );
  });

  it("does not auto-collapse a historical subagent the user opened", async () => {
    const user = userEvent.setup();
    const { rerender } = renderTimeline(completedSubagentTree);
    await user.click(screen.getByRole("button", { name: /Tool details/ }));
    const subagent = within(screen.getByTestId("execution-tree")).getByRole("button", { name: /Find the WebUI trace requirements/ });

    await user.click(subagent);
    rerender(<Timeline session={reduceRawEvents([...completedSubagentTree])} />);

    expect(within(screen.getByTestId("execution-tree")).getByRole("button", { name: /Find the WebUI trace requirements/ })).toHaveAttribute("aria-expanded", "true");
    expect(within(screen.getAllByTestId("tool-details")[0]).getAllByText(/WebUI must render trace details/).length).toBeGreaterThan(0);
  });

  it("filters turns by tool activity without leaving the workflow page", async () => {
    const user = userEvent.setup();
    renderTimeline([...completedTurn, ...messageOnlyTurn]);

    expect(screen.queryByTestId("timeline-match-count")).toBeNull();
    await openTimelineTools(user);
    expect(screen.getByTestId("timeline-match-count")).toHaveTextContent("2/2 messages");
    await openTimelineFilters(user);
    await user.click(screen.getByRole("button", { name: "With actions" }));

    expect(screen.getByTestId("timeline-match-count")).toHaveTextContent("1/2 messages");
    expect(within(screen.getByTestId("timeline")).getAllByText("list the files").length).toBeGreaterThan(0);
    expect(screen.queryByText("message only")).toBeNull();
  });

  it("filters directly to artifact and repair turns", async () => {
    const user = userEvent.setup();
    renderTimeline([
      ...namespaceEvents(completedTurn, "a", 0),
      ...namespaceEvents(resultTruncated, "b", 100),
      ...namespaceEvents(argsRepaired, "c", 200),
    ]);

    await openTimelineTools(user);
    await openTimelineFilters(user);
    expect(screen.getByRole("button", { name: "All" })).toHaveTextContent("3");
    expect(screen.getByRole("button", { name: "Files" })).toHaveTextContent("1");
    expect(screen.getByRole("button", { name: "Runtime fixes" })).toHaveTextContent("1");
    expect(screen.getByRole("button", { name: "Needs attention" })).toHaveTextContent("0");

    await user.click(screen.getByRole("button", { name: "Files" }));
    expect(screen.getByTestId("timeline-match-count")).toHaveTextContent("1/3 messages");
    expect(screen.getByRole("button", { name: "Show all" })).toBeInTheDocument();
    expect(screen.getByTestId("turn-artifacts")).toHaveTextContent("cat big.log");

    await user.click(screen.getByRole("button", { name: "Runtime fixes" }));
    expect(screen.getByTestId("timeline-match-count")).toHaveTextContent("1/3 messages");
    expect(screen.getByTestId("work-summary")).toHaveTextContent("1 repaired");

    await user.click(screen.getByRole("button", { name: "Large output" }));
    expect(screen.getByTestId("timeline-match-count")).toHaveTextContent("1/3 messages");
    expect(screen.getByTestId("work-summary")).toHaveTextContent("1 truncated");
  });

  it("searches event content and highlights visible matches", async () => {
    const user = userEvent.setup();
    renderTimeline(completedSubagentTree);

    expect(screen.queryByTestId("execution-tree")).toBeNull();
    expect(screen.queryByTestId("timeline-toolbar")).toBeNull();
    expect(within(screen.getByTestId("turn-navigator")).getByRole("button", { name: "Find" })).toBeInTheDocument();
    expect(screen.queryByTestId("timeline-match-count")).toBeNull();
    expect(screen.queryByText("Filter results")).toBeNull();
    await openTimelineTools(user);
    expect(within(screen.getByTestId("turn-navigator")).getByRole("button", { name: "Find" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByText("Search messages and outputs")).toBeInTheDocument();
    expect(screen.getByText("Filter results")).toBeVisible();
    await user.type(screen.getByTestId("timeline-search"), "MCP_search");

    expect(screen.getByTestId("timeline-match-count")).toHaveTextContent("1/1 messages");
    expect(screen.getByTestId("work-thread")).toHaveAttribute("data-open", "true");
    expect(screen.getAllByText("MCP_search").length).toBeGreaterThan(0);
    expect(screen.getAllByText("MCP_search").some((node) => node.tagName.toLowerCase() === "mark")).toBe(true);
  });

  it("keeps folded work folded when search only matches the chat answer", async () => {
    const user = userEvent.setup();
    renderTimeline(completedSubagentTree);

    await openTimelineTools(user);
    await user.type(screen.getByTestId("timeline-search"), "delegated checks found");

    expect(screen.getByTestId("timeline-match-count")).toHaveTextContent("1/1 messages");
    expect(screen.getByTestId("work-thread")).toHaveAttribute("data-open", "false");
    expect(screen.queryByTestId("execution-tree")).toBeNull();
  });

  it("shows an empty filtered state when no event content matches", async () => {
    const user = userEvent.setup();
    renderTimeline([...completedTurn, ...messageOnlyTurn]);

    await openTimelineTools(user);
    await user.type(screen.getByTestId("timeline-search"), "definitely-not-present");

    expect(within(screen.getByTestId("conversation-map")).getByTestId("timeline-toolbar")).toBeInTheDocument();
    expect(screen.queryByTestId("turn-navigator")).toBeNull();
    expect(screen.getByTestId("timeline-search")).toHaveValue("definitely-not-present");
    expect(screen.getByTestId("timeline-filter-empty")).toHaveTextContent("No matching messages");
    expect(screen.getByTestId("timeline-filter-empty")).toHaveTextContent("definitely-not-present");
    expect(screen.getByTestId("timeline-match-count")).toHaveTextContent("0/2 messages");
    expect(screen.getByRole("button", { name: "Show all" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Reset filters" })).toBeNull();

    await user.click(screen.getByRole("button", { name: "Show all" }));
    expect(screen.getByTestId("timeline-search")).toHaveValue("");
    expect(screen.getByTestId("timeline-match-count")).toHaveTextContent("2/2 messages");
    expect(screen.queryByTestId("timeline-filter-empty")).toBeNull();
  });

  it("resets search and filters when switching sessions", async () => {
    const user = userEvent.setup();
    const { rerender } = render(
      <Timeline session={reduceRawEvents([...completedTurn, ...messageOnlyTurn])} sessionId="s1" />,
    );

    await openTimelineTools(user);
    await user.type(screen.getByTestId("timeline-search"), "definitely-not-present");
    expect(screen.getByTestId("timeline-filter-empty")).toBeInTheDocument();

    rerender(<Timeline session={reduceRawEvents([
      ...messageOnlyTurn,
      ...namespaceEvents(messageOnlyTurn, "second", 100),
    ])} sessionId="s2" />);

    expect(screen.queryByTestId("timeline-search")).toBeNull();
    expect(screen.queryByTestId("timeline-filter-empty")).toBeNull();
    expect(screen.queryByTestId("timeline-match-count")).toBeNull();
    await openTimelineTools(user);
    expect(screen.getByTestId("timeline-search")).toHaveValue("");
    expect(screen.getByTestId("timeline-match-count")).toHaveTextContent("2/2 messages");
    expect(within(screen.getByTestId("timeline")).getAllByText("message only").length).toBeGreaterThan(0);
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
  });

  it("follows local guidance receipts when already at the latest message", () => {
    const { scrollIntoView } = installScrollIntoViewSpy();
    const { rerender } = render(<ScrollHarness events={completedTurn} />);

    rerender(<ScrollHarness events={completedTurn} guidanceReceipts={[{ id: 1, text: "check tests first" }]} />);

    expect(scrollIntoView).toHaveBeenCalledWith({ behavior: "auto", block: "end" });
  });

  it("anchors running updates to the current turn instead of the bottom spacer", () => {
    const { scrollIntoView } = installScrollIntoViewSpy();
    const { rerender } = render(<ScrollHarness events={runningStarted} />);

    rerender(<ScrollHarness events={runningSubagent} />);

    expect(scrollIntoView).toHaveBeenCalledWith({ behavior: "auto", block: "start" });
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
    expect(screen.queryByRole("button", { name: "Back to latest" })).toBeNull();
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

  it("does not auto-scroll or insert jump chrome while the user is selecting text", () => {
    const { scrollIntoView } = installScrollIntoViewSpy();
    const { rerender } = render(<ScrollHarness events={completedTurn} />);
    const scrollRoot = screen.getByTestId("scroll-root");
    Object.defineProperties(scrollRoot, {
      scrollTop: { configurable: true, writable: true, value: 144 },
    });

    fireEvent.pointerDown(scrollRoot);
    scrollRoot.scrollTop = 260;
    fireEvent.scroll(scrollRoot);
    expect(scrollRoot.scrollTop).toBe(144);

    scrollRoot.scrollTop = 260;
    rerender(<ScrollHarness events={completedTurn} guidanceReceipts={[{ id: 1, text: "check tests first" }]} />);

    expect(scrollIntoView).not.toHaveBeenCalled();
    expect(scrollRoot.scrollTop).toBe(144);
    expect(screen.queryByRole("button", { name: /latest/i })).toBeNull();
  });

  it("offers a quiet return-to-latest control while browsing older messages", async () => {
    const user = userEvent.setup();
    const { scrollIntoView } = installScrollIntoViewSpy();
    render(<ScrollHarness events={completedTurn} />);
    const scrollRoot = screen.getByTestId("scroll-root");
    Object.defineProperties(scrollRoot, {
      clientHeight: { configurable: true, value: 300 },
      scrollHeight: { configurable: true, value: 1200 },
      scrollTop: { configurable: true, value: 0 },
    });

    fireEvent.wheel(scrollRoot);
    fireEvent.scroll(scrollRoot);

    const jump = screen.getByRole("button", { name: "Back to latest" });
    expect(jump).toHaveAttribute("data-new", "false");

    await user.click(jump);

    expect(scrollIntoView).toHaveBeenCalledWith({ behavior: "auto", block: "end" });
    expect(screen.queryByRole("button", { name: "Back to latest" })).toBeNull();
  });

  it("copies structured args from an expanded tool node", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    renderTimeline(completedTurn);

    await user.click(screen.getByRole("button", { name: /Tool details/ }));
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

    await user.click(screen.getByRole("button", { name: /Tool details/ }));
    await user.click(within(screen.getByTestId("execution-tree")).getByRole("button", { name: /List current directory/ }));
    await user.click(screen.getByRole("button", { name: "Copy action record" }));

    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("Action: List current directory"));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("Status: done"));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining('"path": "."'));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("Output:\nREADME.md\nmain.go"));
  });

  it("turns an expanded tool result into a follow-up draft", async () => {
    const user = userEvent.setup();
    const onUseAsDraft = vi.fn();
    renderTimeline(completedTurn, undefined, undefined, onUseAsDraft);

    await user.click(screen.getByRole("button", { name: /Tool details/ }));
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

    await user.click(screen.getByRole("button", { name: /Tool details/ }));
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

    await user.click(screen.getByRole("button", { name: /Tool details/ }));
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

    expect(screen.getByTestId("fallback-answer")).toHaveTextContent("No final answer yet");
    expect(screen.getByTestId("fallback-answer")).toHaveTextContent("before synthesizing the final reply");
    expect(screen.getByTestId("continuation-card")).toHaveTextContent("Final answer not produced");
    expect(screen.getByTestId("continuation-card")).toHaveTextContent("gathered evidence");
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
    expect(digest).toHaveAccessibleName("Handoff · Ran 1 action; message 2 continued the task. · 1 step · 1 action");
    expect(digest.textContent?.replace(/\s+/g, " ").trim()).toContain("Handoff · Ran 1 action; message 2 continued the task. · 1 step · 1 action");
    expect(digest.textContent?.replace(/\s+/g, " ").trim()).not.toContain("HandoffRan");
    expect(digest).not.toHaveTextContent("This message reached the action limit");
    expect(screen.queryByTestId("work-thread")).toBeNull();
  });

  it("keeps historical handoff tool details hidden until search asks for them", async () => {
    const user = userEvent.setup();
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
    await openTimelineTools(user);
    await user.type(screen.getByTestId("timeline-search"), "memory");

    const toolDetails = screen.getByRole("button", { name: /Tool details/ });
    expect(toolDetails).toHaveAccessibleName("Tool details · continued in message 2 · 2 calls · 7ms");
    expect(toolDetails.textContent?.replace(/\s+/g, " ").trim()).toContain("Tool details · continued in message 2 · 2 calls · 7ms");
    expect(toolDetails).not.toHaveTextContent("2 actions");
  });

  it("renders compact web evidence while keeping the full source available", () => {
    renderTimeline(webFetchTurn);

    const evidence = screen.getByTestId("agent-activity-digest-evidence");
    expect(evidence).toHaveTextContent("affine.io");
    expect(evidence).toHaveTextContent("Fetched affine.io");
    expect(screen.getByTestId("agent-activity-digest").textContent?.replace(/\s+/g, " ").trim()).toContain("1 evidence · Fetched affine.io");
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

function namespaceEvents(raws: RawEvent[], suffix: string, idOffset: number): RawEvent[] {
  return raws.map((event) => ({
    ...event,
    id: event.id + idOffset,
    data: namespacePayload(event.data, suffix),
  }));
}

function namespacePayload(data: unknown, suffix: string): unknown {
  if (!data || typeof data !== "object" || Array.isArray(data)) return data;
  const copy: Record<string, unknown> = { ...(data as Record<string, unknown>) };
  if (typeof copy.turn_id === "string") copy.turn_id = `${copy.turn_id}_${suffix}`;
  if (typeof copy.call_id === "string") copy.call_id = `${copy.call_id}_${suffix}`;
  return copy;
}
