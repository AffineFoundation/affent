import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { SessionSummary } from "../api/sessions";
import { completedTurn } from "../fixtures/completedTurn";
import { reduceRawEvents } from "../store/reduce";
import { SessionList } from "./SessionList";

describe("SessionList", () => {
  it("shows useful status and feature context without cost noise", () => {
    renderList([
      session({
        id: "s1",
        active: true,
        durable: true,
        has_events: true,
        has_artifacts: true,
        usage: { input_tokens: 400, output_tokens: 100, turns: 2 },
      }),
    ]);

    const row = screen.getByRole("button", { name: /s1/ });
    expect(row).toHaveTextContent("Live chat");
    expect(row).toHaveTextContent("Live");
    expect(row).not.toHaveTextContent("2 messages");
    expect(row).not.toHaveTextContent("tokens");
    expect(row).not.toHaveTextContent("activity");
    expect(row).toHaveTextContent("files");
  });

  it("uses the latest user task as the row title while keeping the id visible", () => {
    renderList([
      session({
        id: "workspace-session-abcdef123456",
        durable: true,
        latest_user_message: "review the WebUI session list behavior",
        last_used_at: "2026-05-23T18:30:00Z",
      }),
    ]);

    const row = screen.getByRole("button", { name: /WebUI session list behavior/ });
    expect(row).not.toHaveTextContent("review the WebUI");
    expect(row).toHaveTextContent("workspac...123456");
    expect(row).toHaveTextContent("2026-05-23 18:30 UTC");
  });

  it("uses the stable chat topic when the latest message is only a continuation", () => {
    renderList([
      session({
        id: "affine-session",
        durable: true,
        latest_user_message: "请继续同一个任务。基于已有证据输出报告",
        topic_user_message: "affine 是 Bittensor 的一个子网，请收集信息",
        last_used_at: "2026-05-24T17:37:00Z",
      }),
    ]);

    const row = screen.getByRole("button", { name: /Affine（Bittensor 子网）/ });
    expect(row).toHaveTextContent("Affine（Bittensor 子网）");
    expect(row).not.toHaveTextContent("请继续同一个任务");
  });

  it("shows the active chat before newer saved chats, then sorts saved chats by recency", () => {
    renderList([
      session({
        id: "older-saved",
        durable: true,
        latest_user_message: "older saved task",
        last_used_at: "2026-05-23T18:30:00Z",
      }),
      session({
        id: "recent-saved",
        durable: true,
        latest_user_message: "recent saved task",
        last_used_at: "2026-05-24T17:37:00Z",
      }),
      session({
        id: "live-stale",
        active: true,
        latest_user_message: "live task",
        last_used_at: "2026-05-22T12:00:00Z",
      }),
    ]);

    const rows = screen.getAllByRole("button").filter((button) => button.classList.contains("session-row"));
    expect(rows.map((row) => row.textContent)).toEqual([
      expect.stringContaining("live task"),
      expect.stringContaining("recent saved task"),
      expect.stringContaining("older saved task"),
    ]);
  });

  it("hides chat filters when there is only one chat", () => {
    renderList([session({ id: "s1", active: true })]);

    expect(screen.queryByTestId("session-tools")).toBeNull();
  });

  it("describes empty chats without internal metrics", () => {
    renderList([session({ id: "new-session" })]);

    const row = screen.getByRole("button", { name: /new-session/ });
    expect(row).toHaveTextContent("New chat");
    expect(row).toHaveTextContent("No messages yet");
    expect(row).not.toHaveTextContent("empty");
  });

  it("keeps multi-chat search visible while filters stay quiet until focus", async () => {
    const user = userEvent.setup();
    renderList([session({ id: "s1", active: true }), session({ id: "s2", durable: true })]);

    const tools = screen.getByTestId("session-tools");
    expect(tools).toHaveAttribute("data-expanded", "false");
    expect(within(tools).getByText("Search chats")).toBeInTheDocument();
    expect(within(tools).queryByText("2/2")).toBeNull();
    expect(within(tools).queryByRole("button", { name: /Saved/ })).toBeNull();

    await user.click(screen.getByTestId("session-search"));
    expect(tools).toHaveAttribute("data-expanded", "true");
    expect(within(tools).getByText("2/2")).toBeInTheDocument();
    expect(within(tools).getByRole("button", { name: /Saved/ })).toBeInTheDocument();
    expect(screen.getByTestId("session-search")).toBeVisible();
  });

  it("offers a compact mobile chat switcher for selected saved chats", async () => {
    const user = userEvent.setup();
    renderList([
      session({ id: "s1", latest_user_message: "current affine research" }),
      session({ id: "s2", latest_user_message: "older project review" }),
    ]);

    const panel = screen.getByLabelText("Chats");
    const toggle = screen.getByRole("button", { name: "Switch chats" });

    expect(panel).toHaveAttribute("data-has-selection", "true");
    expect(panel).toHaveAttribute("data-mobile-open", "false");
    expect(toggle).toHaveTextContent("Affine research");
    expect(toggle).toHaveTextContent("Switch");

    await user.click(toggle);

    expect(panel).toHaveAttribute("data-mobile-open", "true");
    expect(toggle).toHaveAccessibleName("Hide chat list");
    expect(toggle).toHaveTextContent("Hide");
  });

  it("does not show chat filters before any chats exist", () => {
    renderList([]);

    expect(screen.getByRole("button", { name: "New" })).toBeInTheDocument();
    expect(screen.getByText("No chats yet. Type a request to start.")).toBeInTheDocument();
    expect(screen.queryByTestId("session-tools")).toBeNull();
  });

  it("shows the selected session's latest task from the live timeline state", () => {
    render(
      <SessionList
        sessions={[session({ id: "s1", durable: true, has_events: true })]}
        selectedId="s1"
        currentSession={reduceRawEvents(completedTurn)}
        demoActive={false}
        onSelect={vi.fn()}
        onNew={vi.fn()}
      />,
    );

    const row = screen.getByRole("button", { name: /list the files/ });
    expect(row).toHaveTextContent("Done");
    expect(row).not.toHaveTextContent("1 action");
    expect(row).not.toHaveTextContent("No messages yet");
  });

  it("shows the original task topic instead of a continuation prompt", () => {
    render(
      <SessionList
        sessions={[session({ id: "s1", durable: true, has_events: true })]}
        selectedId="s1"
        currentSession={reduceRawEvents([
          { id: 1, type: "turn.start", data: { turn_id: "t1" } },
          { id: 2, type: "user.message", data: { turn_id: "t1", text: "affine 是 Bittensor 的一个子网，请收集信息" } },
          { id: 3, type: "turn.end", data: { turn_id: "t1", reason: "max_turns" } },
          { id: 4, type: "turn.start", data: { turn_id: "t2" } },
          { id: 5, type: "user.message", data: { turn_id: "t2", text: "请继续同一个任务。基于已有证据输出报告" } },
          { id: 6, type: "message.done", data: { turn_id: "t2", text: "阶段性报告如下。" } },
          { id: 7, type: "turn.end", data: { turn_id: "t2", reason: "completed" } },
        ])}
        demoActive={false}
        onSelect={vi.fn()}
        onNew={vi.fn()}
      />,
    );

    const row = screen.getByRole("button", { name: /Affine（Bittensor 子网）/ });
    expect(row).toHaveTextContent("Affine（Bittensor 子网）");
    expect(row).not.toHaveTextContent("请继续同一个任务");
  });

  it("does not color a completed answer as failed when only tool attempts failed", () => {
    render(
      <SessionList
        sessions={[session({ id: "s1", durable: true, has_events: true })]}
        selectedId="s1"
        currentSession={reduceRawEvents([
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
          { id: 5, type: "message.done", data: { turn_id: "t1", text: "I still found enough to answer." } },
          { id: 6, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
        ])}
        demoActive={false}
        onSelect={vi.fn()}
        onNew={vi.fn()}
      />,
    );

    const row = screen.getByRole("button", { name: /Affine/ });
    expect(row).toHaveAttribute("data-tone", "saved");
    expect(row).toHaveTextContent("Done");
    expect(row).not.toHaveTextContent("research affine");
    expect(row).not.toHaveTextContent("1 tool issue");
  });

  it("filters and searches sessions without leaving the sidebar", async () => {
    const user = userEvent.setup();
    renderList([
      session({ id: "live-one", active: true, has_events: true }),
      session({ id: "memory-two", durable: true, has_memory: true }),
      session({ id: "artifact-three", durable: true, has_artifacts: true }),
    ]);

    await user.click(screen.getByTestId("session-search"));
    await user.click(screen.getByRole("button", { name: /Memory/ }));
    expect(screen.getByTestId("session-list")).toHaveTextContent("memory-two");
    expect(screen.getByTestId("session-list")).not.toHaveTextContent("artifact-three");

    await user.clear(screen.getByTestId("session-search"));
    await user.type(screen.getByTestId("session-search"), "artifact");
    expect(screen.getByTestId("session-filter-empty")).toHaveTextContent("No matching chats");

    await user.click(within(screen.getByTestId("session-filter-empty")).getByRole("button", { name: "Reset" }));
    expect(screen.getByTestId("session-list")).toHaveTextContent("artifact-three");
  });

  it("renders the offline preview row without live filters or new-chat actions", async () => {
    const onNew = vi.fn();
    render(
      <SessionList
        sessions={[]}
        selectedId={undefined}
        demoActive
        onSelect={vi.fn()}
        onNew={onNew}
      />,
    );

    expect(screen.getByTestId("demo-session-row")).toHaveTextContent("Offline preview");
    expect(screen.getByTestId("demo-session-row")).toHaveTextContent("Read-only replay");
    expect(screen.queryByTestId("session-tools")).toBeNull();
    expect(screen.queryByRole("button", { name: "New" })).toBeNull();
    expect(onNew).not.toHaveBeenCalled();
  });
});

function renderList(sessions: SessionSummary[]) {
  return render(
    <SessionList
      sessions={sessions}
      selectedId={sessions[0]?.id}
      demoActive={false}
      onSelect={vi.fn()}
      onNew={vi.fn()}
    />,
  );
}

function session(overrides: Partial<SessionSummary>): SessionSummary {
  return {
    id: "s1",
    active: false,
    durable: false,
    has_conversation: false,
    has_events: false,
    has_artifacts: false,
    has_memory: false,
    has_runtime_skills: false,
    ...overrides,
  };
}
