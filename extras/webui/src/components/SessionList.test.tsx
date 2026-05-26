import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { SessionSummary } from "../api/sessions";
import { completedTurn } from "../fixtures/completedTurn";
import { reduceRawEvents } from "../store/reduce";
import { SessionList } from "./SessionList";

describe("SessionList", () => {
  it("shows useful status context without cost or feature noise", () => {
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

    const row = screen.getByRole("button", { name: /Live chat/ });
    expect(row).toHaveTextContent("Live chat");
    expect(row).toHaveTextContent("Live");
    expect(row).not.toHaveTextContent("s1");
    expect(row).not.toHaveTextContent("2 messages");
    expect(row).not.toHaveTextContent("tokens");
    expect(row).not.toHaveTextContent("activity");
    expect(row).not.toHaveTextContent("files");
  });

  it("shows tool work in the row stats when the API summary includes it", () => {
    renderList([
      session({
        id: "tools-session",
        durable: true,
        latest_user_message: "review the WebUI timeline",
        usage: { input_tokens: 1200, output_tokens: 450, turns: 3 },
        tools: { tool_requests: 5, tool_errors: 1, tool_repair_succeeded: 2, tool_repair_failed: 0 },
      }),
    ]);

    const row = screen.getByRole("button", { name: /WebUI timeline/ });
    expect(within(row).getByTestId("session-stats")).toHaveTextContent("3 messages · 5 actions · 1 issue");
    expect(row).toHaveTextContent("3 messages · 5 actions · 1 issue");
  });

  it("uses the latest user task as the row title while keeping the id out of the scan path", () => {
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
    expect(row).not.toHaveTextContent("workspac...123456");
    expect(row).not.toHaveTextContent("Saved");
    expect(row).toHaveTextContent("May 23 18:30 UTC");
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
    expect(row).toHaveTextContent("Latest · 基于已有证据输出报告");
    expect(row).not.toHaveTextContent("请继续同一个任务");
    expect(row).not.toHaveTextContent("Saved");
    expect(row).toHaveAccessibleDescription("Latest · 基于已有证据输出报告");
  });

  it("shows a generated chat title instead of the first user message", () => {
    renderList([
      session({
        id: "affine-session",
        durable: true,
        title: "Affine market research",
        latest_user_message: "affine 是 Bittensor 的一个子网，请收集信息并向我介绍",
        last_used_at: "2026-05-24T17:37:00Z",
      }),
    ]);

    const row = screen.getByRole("button", { name: /Affine market research/ });
    expect(row).toHaveTextContent("Affine market research");
    expect(row).not.toHaveTextContent("affine 是 Bittensor");
    expect(row).toHaveTextContent("May 24 17:37 UTC");
  });

  it("does not expose a raw prompt when the provided title is unsummarized", () => {
    renderList([
      session({
        id: "raw-title",
        durable: true,
        title: "会话的标题最好是经过总结的，而不是把第一句话的输入当做标题",
        latest_user_message: "会话的标题最好是经过总结的，而不是把第一句话的输入当做标题",
      }),
    ]);

    const row = screen.getByRole("button", { name: /会话标题摘要/ });
    expect(row).toHaveTextContent("会话标题摘要");
    expect(row).not.toHaveTextContent("第一句话");
  });

  it("describes unresolved chat issues in the row preview", () => {
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
          { id: 5, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
        ])}
        demoActive={false}
        onSelect={vi.fn()}
        onNew={vi.fn()}
      />,
    );

    const row = screen.getByRole("button", { name: /Affine/ });
    expect(row).toHaveTextContent("Blocked");
    expect(row).toHaveAttribute("data-preview", "pinned");
    expect(row).toHaveAccessibleDescription("Issue · DNS failed");
    expect(within(row).getByTestId("session-preview")).toHaveTextContent("Issue · DNS failed");
  });

  it("keeps the selected chat's resolved preview visible without hover", () => {
    renderList([session({ id: "s1", durable: true, has_events: true })], {
      currentSession: reduceRawEvents(completedTurn),
    });

    const row = screen.getByRole("button", { name: /list the files/ });
    expect(row).toHaveAttribute("data-preview", "pinned");
    expect(row).toHaveAccessibleDescription("Answer · There are two files.");
    expect(within(row).getByTestId("session-preview")).toHaveTextContent("Answer · There are two files.");
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

    const row = screen.getByRole("button", { name: /New chat/ });
    expect(row).toHaveTextContent("New chat");
    expect(row).toHaveTextContent("No messages yet");
    expect(row).not.toHaveTextContent("new-session");
    expect(row).not.toHaveTextContent("empty");
  });

  it("keeps multi-chat search quiet until the user asks for it", async () => {
    const user = userEvent.setup();
    renderList([session({ id: "s1", active: true }), session({ id: "s2", durable: true })]);

    const tools = screen.getByTestId("session-tools");
    expect(tools).toHaveAttribute("data-expanded", "false");
    expect(within(tools).getByRole("button", { name: "Search chats" })).toBeInTheDocument();
    expect(within(tools).getByText("Filters")).toBeInTheDocument();
    expect(screen.queryByTestId("session-search")).toBeNull();
    expect(within(tools).queryByText("2/2")).toBeNull();
    expect(within(tools).queryByRole("button", { name: /Saved/ })).toBeNull();

    await user.click(within(tools).getByRole("button", { name: "Search chats" }));
    expect(tools).toHaveAttribute("data-expanded", "true");
    expect(within(tools).getByText("Search chats")).toBeInTheDocument();
    expect(within(tools).getByText("2/2")).toBeInTheDocument();
    expect(within(tools).getByRole("button", { name: /Saved/ })).toBeInTheDocument();
    expect(screen.getByTestId("session-search")).toBeVisible();
    expect(screen.getByTestId("session-search")).toHaveFocus();
  });

  it("asks for confirmation before deleting a chat", async () => {
    const user = userEvent.setup();
    const onDelete = vi.fn();
    renderList([session({ id: "s1", durable: true, latest_user_message: "clean up stale chat" })], { onDelete });

    await user.click(screen.getByRole("button", { name: "Delete chat" }));
    const confirm = screen.getByRole("group", { name: "Confirm delete chat" });

    expect(confirm).toHaveTextContent("Delete this chat?");
    expect(onDelete).not.toHaveBeenCalled();
    await user.click(within(confirm).getByRole("button", { name: "Delete" }));
    expect(onDelete).toHaveBeenCalledWith("s1");
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
    expect(toggle).not.toHaveTextContent("Current chat");
    expect(toggle).toHaveTextContent("Affine research");
    expect(toggle).toHaveTextContent("Switch");

    await user.click(toggle);

    expect(panel).toHaveAttribute("data-mobile-open", "true");
    expect(toggle).toHaveAccessibleName("Hide chat list");
    expect(toggle).toHaveTextContent("Hide");
  });

  it("shows the selected chat meta in the mobile switcher when there is no detail", () => {
    renderList([
      session({ id: "saved-empty", durable: true, has_conversation: true, has_events: true, last_used_at: "2026-05-24T17:37:00Z" }),
      session({ id: "saved-recent", durable: true, latest_user_message: "older project review", last_used_at: "2026-05-23T18:30:00Z" }),
    ]);

    const toggle = screen.getByRole("button", { name: "Switch chats" });
    expect(toggle).toHaveTextContent("Saved chat");
    expect(toggle).toHaveTextContent("May 24 17:37 UTC");
    expect(toggle).not.toHaveTextContent("saved-empty");
    expect(toggle).not.toHaveTextContent("Current chat");
  });

  it("prefers the resolved preview over the original request in the mobile switcher", () => {
    renderList([session({ id: "s1", durable: true, has_events: true })], {
      currentSession: reduceRawEvents(completedTurn),
    });

    const toggle = screen.getByRole("button", { name: "Switch chats" });
    expect(toggle).toHaveTextContent("list the files");
    expect(toggle).toHaveTextContent("Answer · There are two files.");
    expect(toggle).not.toHaveTextContent("Latest · list the files");
  });

  it("uses plain chat counts instead of internal session metrics", () => {
    render(
      <SessionList
        sessions={[
          session({ id: "s1", latest_user_message: "current affine research" }),
          session({ id: "s2", latest_user_message: "older project review" }),
        ]}
        selectedId={undefined}
        demoActive={false}
        onSelect={vi.fn()}
        onNew={vi.fn()}
      />,
    );

    expect(screen.getAllByText("2 chats")).toHaveLength(2);
    expect(screen.getByRole("button", { name: "Switch chats" })).toHaveTextContent("2 chats");
    expect(screen.getByLabelText("Chats")).toHaveAttribute("data-mobile-open", "false");
    expect(screen.queryByText(/messages|actions|issues|continued|ephemeral/i)).toBeNull();
  });

  it("does not show chat filters before any chats exist", () => {
    renderList([]);

    expect(screen.getByRole("button", { name: "New" })).toBeInTheDocument();
    expect(screen.getByText("No chats yet. Type a request to start.")).toBeInTheDocument();
    expect(screen.queryByTestId("session-tools")).toBeNull();
  });

  it("shows the selected session's latest task from the live timeline state", () => {
    renderList([session({ id: "s1", durable: true, has_events: true })], {
      currentSession: reduceRawEvents(completedTurn),
    });

    const row = screen.getByRole("button", { name: /list the files/ });
    expect(row).toHaveTextContent("Done");
    expect(row).toHaveAccessibleDescription("Answer · There are two files.");
    expect(within(row).getByTestId("session-preview")).toHaveTextContent("Answer · There are two files.");
    expect(within(row).getByTestId("session-stats")).toHaveTextContent("1 message · 1 action");
    expect(row).not.toHaveTextContent("No messages yet");
  });

  it("shows feature chips for the selected chat without repeating artifact counts", () => {
    render(
      <SessionList
        sessions={[
          session({
            id: "s1",
            durable: true,
            latest_user_message: "review the repo",
            has_memory: true,
            has_runtime_skills: true,
            has_artifacts: true,
          }),
        ]}
        selectedId="s1"
        demoActive={false}
        onSelect={vi.fn()}
        onNew={vi.fn()}
      />,
    );

    const row = screen.getByRole("button", { name: /repo/ });
    expect(within(row).getByTestId("session-chips")).toHaveTextContent("Memory · Skills");
    expect(within(row).getByTestId("session-chips")).not.toHaveTextContent("Files");
  });

  it("shows artifact size in the mobile switcher when the selected chat has output files", () => {
    renderList([session({ id: "s1", durable: true, has_events: true })], {
      currentSession: reduceRawEvents([
        { id: 1, type: "turn.start", data: { turn_id: "t1" } },
        {
          id: 2,
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
          id: 3,
          type: "tool.result",
          data: {
            turn_id: "t1",
            call_id: "c1",
            exit_code: 0,
            duration_ms: 42,
            result_summary: "saved output",
            result: "saved output",
            result_truncated: true,
            result_bytes: 8192,
            result_omitted_bytes: 1048576,
            result_cap_bytes: 262144,
            result_artifact_path: ".affent/artifacts/tool-results/000001-c1.txt",
          },
        },
        { id: 4, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
      ]),
    });

    const toggle = screen.getByRole("button", { name: "Switch chats" });
    expect(toggle).toHaveTextContent("1 message · 1 action · 1 file (8 KiB, 1 MiB omitted)");
    expect(toggle).not.toHaveTextContent("May");
  });

  it("shows a pending follow-up in the selected chat row immediately", () => {
    renderList([session({ id: "s1", active: true, durable: true, latest_user_message: "list the files" })], {
      currentSession: reduceRawEvents(completedTurn),
      pendingTask: "explain main.go",
    });

    const row = screen.getByRole("button", { name: /list the files/ });
    expect(row).toHaveTextContent("Sending · main.go");
    expect(row).toHaveTextContent("Waiting for the next update.");
    expect(row).toHaveTextContent("Live");
    expect(row).not.toHaveTextContent("There are two files.");
    expect(screen.getByRole("button", { name: "Switch chats" })).toHaveTextContent("Sending · main.go");
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
    expect(row).toHaveTextContent("Latest · 基于已有证据输出报告");
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

    await user.click(screen.getByRole("button", { name: "Search chats" }));
    await user.click(within(screen.getByRole("group", { name: "Session filter" })).getByRole("button", { name: /Memory/ }));
    expect(screen.getByTestId("session-list")).toHaveTextContent("Memory chat");
    expect(screen.getByTestId("session-list")).not.toHaveTextContent("Files chat");

    await user.clear(screen.getByTestId("session-search"));
    await user.type(screen.getByTestId("session-search"), "artifact");
    expect(screen.getByTestId("session-filter-empty")).toHaveTextContent("No matching chats");

    await user.click(within(screen.getByTestId("session-filter-empty")).getByRole("button", { name: "Reset" }));
    expect(screen.getByTestId("session-list")).toHaveTextContent("Files chat");
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

function renderList(
  sessions: SessionSummary[],
  opts: { currentSession?: ReturnType<typeof reduceRawEvents>; pendingTask?: string; onDelete?: (id: string) => void } = {},
) {
  return render(
    <SessionList
      sessions={sessions}
      selectedId={sessions[0]?.id}
      currentSession={opts.currentSession}
      pendingTask={opts.pendingTask}
      demoActive={false}
      onSelect={vi.fn()}
      onNew={vi.fn()}
      onDelete={opts.onDelete}
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
