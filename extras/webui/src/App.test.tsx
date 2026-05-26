import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "./App";
import { completedTurn } from "./fixtures/completedTurn";
import { cancelledTurn, maxTurns, resultTruncated, runningSubagent, toolError } from "./fixtures/scenarios";

describe("App", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    window.localStorage.clear();
    document.documentElement.removeAttribute("data-theme");
  });

  it("switches between white and black themes", async () => {
    const user = userEvent.setup();
    vi.stubGlobal("fetch", vi.fn(async () => Promise.reject(new Error("network down"))));

    render(<App />);

    expect(screen.getByTestId("app-shell")).toHaveAttribute("data-theme", "light");
    await user.click(screen.getByRole("button", { name: "Black" }));

    expect(screen.getByTestId("app-shell")).toHaveAttribute("data-theme", "dark");
    expect(document.documentElement).toHaveAttribute("data-theme", "dark");
    expect(window.localStorage.getItem("affent.theme")).toBe("dark");
    expect(screen.getByRole("button", { name: "Black" })).toHaveAttribute("aria-pressed", "true");

    await user.click(screen.getByRole("button", { name: "White" }));
    expect(screen.getByTestId("app-shell")).toHaveAttribute("data-theme", "light");
    expect(window.localStorage.getItem("affent.theme")).toBe("light");
  });

  it("falls back to an offline preview when the API is unreachable", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => Promise.reject(new Error("network down"))));

    render(<App />);

    await waitFor(() => expect(screen.getByTestId("connection-pill")).toHaveTextContent("Preview"));
    expect(screen.getByTestId("connection-pill")).toHaveTextContent("Preview");
    expect(screen.queryByTestId("session-list")).toBeNull();
    expect(screen.queryByRole("button", { name: "New chat" })).toBeNull();
    expect(screen.getByTestId("workspace-shell")).toHaveAttribute("data-compact-nav", "true");
    expect(screen.getByTestId("workspace-shell")).toHaveAttribute("data-session-nav", "hidden");
    expect((await screen.findAllByText("list the files"))[0]).toBeVisible();
    expect(await screen.findByTestId("timeline")).toBeVisible();
    expect(await screen.findByText("There are two files.", {}, { timeout: 5000 })).toBeVisible();
    await waitFor(() => expect(screen.queryByTestId("workflow-status")).toBeNull(), {
      timeout: 5000,
    });
  }, 8000);

  it("opens on a fresh task surface when only saved chats exist, then loads history on selection", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({
          sessions: [
            {
              id: "s1",
              active: false,
              durable: true,
              topic_user_message: "saved research task",
              has_conversation: true,
              has_events: true,
              has_artifacts: false,
              has_memory: false,
              has_runtime_skills: false,
              usage: { input_tokens: 120, output_tokens: 18, turns: 1 },
            },
          ],
          has_more: false,
        });
      }
      if (url === "/v1/sessions/s1/history?after=-1&limit=500") {
        return jsonResponse({
          session_id: "s1",
          events: completedTurn,
          next_after: 11,
          has_more: false,
          trace_schema_version: 1,
          trace_schema_detected: true,
        });
      }
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    await waitFor(() => expect(screen.getByTestId("connection-pill")).toHaveTextContent("Connected"));
    expect(screen.getByTestId("workspace-shell")).toHaveAttribute("data-compact-nav", "false");
    expect(screen.getByTestId("workspace-shell")).toHaveAttribute("data-session-nav", "visible");
    expect(screen.getByTestId("session-list")).toHaveTextContent("saved research task");
    expect(screen.getByTestId("session-list")).not.toHaveTextContent("s1");
    expect(screen.queryByTestId("chat-context-bar")).toBeNull();
    expect(screen.getByTestId("timeline-empty")).toHaveTextContent("What should we work on?");
    expect(screen.getByTestId("timeline-empty")).toHaveTextContent("saved research task");
    expect(screen.getByRole("button", { name: /Open latest chat/ })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Use as draft" }));
    expect(screen.getByPlaceholderText("Message Affent...")).toHaveValue("saved research task");
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Starting from recent chat");
    await user.click(screen.getByRole("button", { name: "Remove" }));
    expect(screen.getByPlaceholderText("Message Affent...")).toHaveValue("");
    expect(fetchImpl).not.toHaveBeenCalledWith("/v1/sessions/s1/history?after=-1&limit=500", expect.anything());
    expect(fetchImpl).not.toHaveBeenCalledWith("/v1/sessions/s1/events", expect.anything());

    await user.click(screen.getByRole("button", { name: /Open latest chat/ }));

    const context = await screen.findByTestId("chat-context-bar");
    expect(context).toHaveTextContent("Result ready");
    expect(context).toHaveTextContent("README.md main.go");
    const details = screen.getByTestId("chat-context-details");
    expect(chatContextMetric(details, "Work 1 action")).toBeVisible();
    expect(within(details).getByLabelText("Session metrics: 1 more metric")).toBeInTheDocument();
    await user.click(within(details).getByLabelText("Session metrics: 1 more metric"));
    expect(chatContextMetric(details, "Tokens 138")).toBeVisible();
    const contextText = context.textContent?.replace(/\s+/g, " ").trim();
    expect(contextText).toContain("Result ready · README.md main.go");
    expect(contextText).not.toContain("Task: list the files");
    expect(context).toHaveAccessibleName("Result ready · README.md main.go");
    expect(screen.queryByText("Metrics")).toBeNull();
    expect(context.querySelector(".chat-context-primary")).toHaveTextContent("README.md main.go");
    expect(context.querySelector(".chat-context-topic")).toBeNull();
    expect(screen.queryByTestId("runtime-capabilities")).toBeNull();
    expect(screen.queryByRole("button", { name: "Profile" })).toBeNull();
    expect(await screen.findByTestId("msg-assistant")).toHaveTextContent("There are two files.");
    expect(screen.getByTestId("composer-intent")).toHaveTextContent("Resume chat");
    expect(screen.getByTestId("composer-intent")).toHaveTextContent("continue this chat");
    expect(screen.getByRole("button", { name: "Resume" })).toBeDisabled();
    expect(screen.queryByTestId("session-strip")).toBeNull();
    expect(fetchImpl).not.toHaveBeenCalledWith("/v1/sessions/s1/events", expect.anything());
  });

  it("keeps internal session ids out of the latest-chat shortcut", async () => {
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({
          sessions: [
            {
              id: "saved-session-abcdef123456",
              active: false,
              durable: true,
              last_used_at: "2026-05-23T18:30:00Z",
              has_conversation: true,
              has_events: true,
              has_artifacts: false,
              has_memory: false,
              has_runtime_skills: false,
            },
          ],
          has_more: false,
        });
      }
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    const empty = await screen.findByTestId("timeline-empty");
    const latest = within(empty).getByTestId("intro-latest-chat");
    expect(latest).toHaveTextContent("Saved chat");
    expect(latest).toHaveTextContent("May 23 18:30 UTC");
    expect(latest).not.toHaveTextContent("saved-se...123456");
    expect(within(latest).queryByRole("button", { name: "Use as draft" })).toBeNull();
    expect(within(latest).getByRole("button", { name: /Open latest chat/ })).toBeInTheDocument();
    expect(screen.getByTestId("session-list")).not.toHaveTextContent("saved-se...123456");
  });

  it("deletes a saved chat from the sidebar", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === "/v1/stats") {
        return jsonResponse({});
      }
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({
          sessions: [
            {
              id: "delete-me",
              active: false,
              durable: true,
              latest_user_message: "delete this stale experiment",
              has_conversation: true,
              has_events: true,
              has_artifacts: false,
              has_memory: false,
              has_runtime_skills: false,
            },
          ],
          has_more: false,
        });
      }
      if (url === "/v1/sessions/delete-me" && init?.method === "DELETE") {
        return new Response(null, { status: 204 });
      }
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    expect(await screen.findByTestId("session-list")).toHaveTextContent("delete this stale experiment");
    await user.click(screen.getByRole("button", { name: "Delete chat" }));
    await user.click(within(screen.getByRole("group", { name: "Confirm delete chat" })).getByRole("button", { name: "Confirm" }));

    await waitFor(() => expect(screen.queryByTestId("session-list")).toBeNull());
    expect(fetchImpl).toHaveBeenCalledWith("/v1/sessions/delete-me", expect.objectContaining({ method: "DELETE" }));
    expect(screen.getByText("What should we work on?")).toBeVisible();
  });

  it("loads every history page before rendering a saved chat", async () => {
    const user = userEvent.setup();
    const firstPage = completedTurn.slice(0, 4);
    const secondPage = completedTurn.slice(4, 8);
    const thirdPage = completedTurn.slice(8);
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({
          sessions: [
            {
              id: "long-history",
              active: false,
              durable: true,
              topic_user_message: "long research task",
              has_conversation: true,
              has_events: true,
              has_artifacts: false,
              has_memory: false,
              has_runtime_skills: false,
            },
          ],
          has_more: false,
        });
      }
      if (url === "/v1/sessions/long-history/history?after=-1&limit=500") {
        return jsonResponse({
          session_id: "long-history",
          events: firstPage,
          next_after: 3,
          has_more: true,
          trace_schema_version: 1,
          trace_schema_detected: true,
        });
      }
      if (url === "/v1/sessions/long-history/history?after=3&limit=500") {
        return jsonResponse({
          session_id: "long-history",
          events: secondPage,
          next_after: 7,
          has_more: true,
          trace_schema_version: 1,
          trace_schema_detected: true,
        });
      }
      if (url === "/v1/sessions/long-history/history?after=7&limit=500") {
        return jsonResponse({
          session_id: "long-history",
          events: thirdPage,
          next_after: 11,
          has_more: false,
          trace_schema_version: 1,
          trace_schema_detected: true,
        });
      }
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    await user.click(await screen.findByRole("button", { name: /Open latest chat/ }));

    expect(await screen.findByTestId("msg-assistant")).toHaveTextContent("There are two files.");
    await waitFor(() => expect(fetchImpl).toHaveBeenCalledWith("/v1/sessions/long-history/history?after=3&limit=500", expect.anything()));
    await waitFor(() => expect(fetchImpl).toHaveBeenCalledWith("/v1/sessions/long-history/history?after=7&limit=500", expect.anything()));
    expect(fetchImpl).not.toHaveBeenCalledWith("/v1/sessions/long-history/events", expect.anything());
  });

  it("surfaces runtime capabilities before a real task is sent", async () => {
    const history = deferred<Response>();
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({
          sessions: [
            {
              id: "research-1",
              active: true,
              durable: true,
              capabilities: {
                eval_mode: false,
                builtins: false,
                skill_install: true,
                plan: false,
                memory: true,
                session_search: false,
                symbol_context: false,
                repo_search: false,
                browser: false,
                browser_screenshot: false,
                web: false,
                web_search: false,
                subagent: true,
                subagent_max_depth: 2,
                focused_tasks: true,
                focused_task_profiles: ["recall", "explore", "verify", "review"],
              },
              has_conversation: true,
              has_events: true,
              has_artifacts: false,
              has_memory: false,
              has_runtime_skills: false,
            },
          ],
          has_more: false,
        });
      }
      if (url === "/v1/sessions/research-1/history?after=-1&limit=500") {
        return history.promise;
      }
      if (url === "/v1/sessions/research-1/tools") {
        return jsonResponse({
          session_id: "research-1",
          count: 3,
          tools: [
            { name: "read_file", group: "Workspace", description: "Read a file from the workspace.", parameters: { type: "object", properties: { path: { type: "string" } } } },
            { name: "web_fetch", group: "Research", description: "Fetch a public URL.", parameters: { type: "object", properties: { url: { type: "string" } } } },
            { name: "taostats_query", group: "MCP", source: "taostats", raw_name: "query", description: "Query MCP data for TAO stats.", parameters: { type: "object", properties: { query: { type: "string" } } } },
          ],
        });
      }
      if (url === "/v1/skills") {
        return jsonResponse({
          session_id: "account",
          count: 1,
          install_enabled: true,
          skills: [
            {
              name: "coding_repair_workflow",
              description: "Repair code by reproducing failures first.",
              source: "embed:internal/agent/builtin_skills/coding_repair_workflow/SKILL.md",
              runtime: false,
              body_preview: "AFFENT ACTIVE SKILL: coding_repair_workflow",
              body_bytes: 128,
            },
          ],
        });
      }
      if (url === "/v1/sessions/research-1/events") return eventStreamResponse("");
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    await waitFor(() => expect(screen.getByTestId("connection-pill")).toHaveTextContent("Loading chat"));
    expect(screen.queryByTestId("timeline-loading")).toBeNull();

    history.resolve(
      jsonResponse({ session_id: "research-1", events: [], next_after: -1, has_more: false, trace_schema_detected: false }),
    );

    expect(screen.queryByTestId("runtime-capabilities")).toBeNull();
    expect(screen.queryByRole("button", { name: "Profile" })).toBeNull();
    const input = screen.getByPlaceholderText("Message Affent...");
    expect(input).toBeVisible();
    await userEvent.type(input, "Analyze Affine recent market trends and Twitter reaction");
    expect(screen.getByTestId("composer-task-hint")).toHaveTextContent("Needs current sources");
    expect(screen.getByTestId("composer-task-hint")).toHaveTextContent("paste URLs, docs, or files");
    await userEvent.clear(input);
    await userEvent.type(input, "help me install a skill from github");
    expect(screen.getByTestId("composer-task-hint")).toHaveTextContent("Skill install ready");
    expect(screen.getByTestId("composer-task-hint")).toHaveTextContent("propose_install");
    expect(screen.queryByTestId("session-tools-panel")).toBeNull();
    await userEvent.click(screen.getByLabelText("Settings"));
    expect(await screen.findByTestId("session-skills-panel")).toHaveTextContent("1 skill");
    expect(screen.getByTestId("session-skills-panel")).toHaveTextContent("coding_repair_workflow");
    expect(screen.queryByTestId("chat-context-bar")).toBeNull();
  });

  it("creates a session from the first submitted task", async () => {
    const user = userEvent.setup();
    const messageResponse = deferred<Response>();
    const fetchImpl = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({ sessions: [], has_more: false });
      }
      if (url === "/v1/sessions" && init?.method === "POST") {
        return jsonResponse({
          session: {
            id: "new-1",
            active: true,
            durable: true,
            has_conversation: false,
            has_events: false,
            has_artifacts: false,
            has_memory: false,
            has_runtime_skills: false,
          },
        });
      }
      if (url === "/v1/sessions/new-1/messages" && init?.method === "POST") {
        return messageResponse.promise;
      }
      if (url === "/v1/sessions/new-1/history?after=-1&limit=500") {
        return jsonResponse({ session_id: "new-1", events: [], next_after: -1, has_more: false, trace_schema_detected: false });
      }
      if (url === "/v1/sessions/new-1/events") {
        return eventStreamResponse("");
      }
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    const input = await screen.findByPlaceholderText("Message Affent...");
    await user.type(input, "summarize the repo");
    await user.click(screen.getByRole("button", { name: "Start anyway" }));

    expect(await screen.findByTestId("pending-turn")).toHaveTextContent("summarize the repo");
    expect(screen.getByTestId("chat-context-bar")).toHaveTextContent("repo");
    expect(screen.getByTestId("chat-context-bar")).toHaveTextContent("Sending");
    expect(screen.getByTestId("chat-context-bar")).toHaveTextContent("Next:");
    expect(screen.getByTestId("chat-context-bar")).not.toHaveTextContent("Status:");
    expect(screen.queryByTestId("workflow-status")).toBeNull();
    messageResponse.resolve(jsonResponse({ session_id: "new-1", turn_id: "t1" }));
    await waitFor(() => expect(fetchImpl).toHaveBeenCalledWith("/v1/sessions/new-1/messages", expect.objectContaining({ method: "POST" })));
    await waitFor(() => expect(screen.getByTestId("connection-pill")).toHaveTextContent("Disconnected"));
    expect(screen.getByTestId("pending-turn")).toHaveTextContent("summarize the repo");
    expect(screen.getByTestId("pending-turn")).toHaveTextContent("Preparing the first update.");
    expect(screen.queryByRole("button", { name: "Working" })).toBeNull();
    expect(screen.getByTestId("workspace-shell")).toHaveAttribute("data-session-nav", "visible");
    expect(screen.getByTestId("session-list")).toHaveTextContent("repo");
    expect(screen.getByTestId("session-list")).not.toHaveTextContent("No messages yet");
    expect(screen.getByTestId("session-list")).not.toHaveTextContent("New live chat");
    expect(screen.getByTestId("session-list")).not.toHaveTextContent("new-1");
    expect(screen.queryByRole("button", { name: "New chat" })).toBeNull();
    expect(screen.getByRole("button", { name: "New" })).toBeInTheDocument();
  });

  it("shows artifact output first in the chat context bar when the latest chat has files", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({
          sessions: [
            {
              id: "artifact-session",
              active: false,
              durable: true,
              topic_user_message: "artifact report task",
              has_conversation: true,
              has_events: true,
              has_artifacts: true,
              has_memory: false,
              has_runtime_skills: false,
              usage: { input_tokens: 120, output_tokens: 18, turns: 1 },
            },
          ],
          has_more: false,
        });
      }
      if (url === "/v1/sessions/artifact-session/history?after=-1&limit=500") {
        return jsonResponse({
          session_id: "artifact-session",
          events: [
            { id: 1, type: "turn.start", data: { turn_id: "t1" } },
            { id: 2, type: "user.message", data: { turn_id: "t1", text: "summarize the repo" } },
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
            { id: 5, type: "message.done", data: { turn_id: "t1", text: "Here is the report." } },
            { id: 6, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
          ],
          next_after: 6,
          has_more: false,
          trace_schema_version: 1,
          trace_schema_detected: true,
        });
      }
      if (url === "/v1/sessions/artifact-session/events") return eventStreamResponse("");
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    await user.click(await screen.findByRole("button", { name: /Open latest chat/ }));

    const context = await screen.findByTestId("chat-context-bar");
    expect(context).toHaveTextContent("Result ready");
    expect(context).toHaveTextContent("Artifact 1 file (8 KiB, 1 MiB omitted)");
    const details = screen.getByTestId("chat-context-details");
    expect(chatContextMetric(details, "Artifact 1 file")).toBeVisible();
    expect(within(details).getByLabelText("Session metrics: 1 more metric")).toBeInTheDocument();
    await user.click(within(details).getByLabelText("Session metrics: 1 more metric"));
    expect(chatContextMetric(details, "Work 1 action · 1 source")).toBeVisible();
    expect(context).toHaveTextContent("Artifact 1 file (8 KiB, 1 MiB omitted)");
  });

  it("shows a pending follow-up as the active chat context", async () => {
    const user = userEvent.setup();
    const messageResponse = deferred<Response>();
    const fetchImpl = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({
          sessions: [
            {
              id: "s1",
              active: true,
              durable: true,
              has_conversation: true,
              has_events: true,
              has_artifacts: false,
              has_memory: false,
              has_runtime_skills: false,
            },
          ],
          has_more: false,
        });
      }
      if (url === "/v1/sessions/s1/history?after=-1&limit=500") {
        return jsonResponse({
          session_id: "s1",
          events: completedTurn,
          next_after: 11,
          has_more: false,
          trace_schema_detected: false,
        });
      }
      if (url === "/v1/sessions/s1/events") return eventStreamResponse("");
      if (url === "/v1/sessions/s1/messages" && init?.method === "POST") return messageResponse.promise;
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    expect(await screen.findByTestId("chat-context-bar")).toHaveTextContent("Result ready");
    expect(screen.getByTestId("chat-context-bar")).toHaveTextContent("README.md main.go");
    const input = screen.getByPlaceholderText("Message Affent...");
    await user.type(input, "explain main.go");
    await user.click(screen.getByRole("button", { name: "Send" }));

    expect(await screen.findByTestId("pending-turn")).toHaveTextContent("explain main.go");
    expect(screen.getByTestId("pending-turn")).toHaveTextContent("Waiting for the next update in this chat.");
    expect(screen.getByTestId("pending-turn")).not.toHaveTextContent("Preparing the first update.");
    expect(screen.queryByTestId("turn-navigator")).toBeNull();
    const context = screen.getByTestId("chat-context-bar");
    expect(context).toHaveTextContent("main.go");
    expect(context).toHaveTextContent("Sending");
    expect(context).toHaveTextContent("Next:");
    expect(context).not.toHaveTextContent("Status:");
    expect(context).toHaveTextContent("next update");
    expect(context).not.toHaveTextContent("Result ready");
    expect(screen.getByTestId("session-list")).toHaveTextContent("list the files");
    expect(screen.getByTestId("session-list")).toHaveTextContent("Sending · main.go");
    expect(screen.getByTestId("session-list")).toHaveTextContent("Waiting for the next update.");
    expect(screen.getByTestId("session-list")).not.toHaveTextContent("Answer · There are two files.");

    messageResponse.resolve(jsonResponse({ session_id: "s1", turn_id: "t2" }));
    await waitFor(() => expect(fetchImpl).toHaveBeenCalledWith("/v1/sessions/s1/messages", expect.objectContaining({ method: "POST" })));
  });

  it("subscribes to live events after resuming a saved chat", async () => {
    const user = userEvent.setup();
    const live = [
      { id: 12, type: "turn.start", data: { turn_id: "t2" } },
      { id: 13, type: "user.message", data: { turn_id: "t2", text: "explain Bittensor" } },
      { id: 14, type: "message.done", data: { turn_id: "t2", text: "Bittensor is a decentralized AI network.", finish_reason: "stop" } },
      { id: 15, type: "turn.end", data: { turn_id: "t2", reason: "completed" } },
    ];
    const fetchImpl = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({
          sessions: [
            {
              id: "saved-1",
              active: false,
              durable: true,
              latest_user_message: "previous task",
              has_conversation: true,
              has_events: true,
              has_artifacts: false,
              has_memory: false,
              has_runtime_skills: false,
            },
          ],
          has_more: false,
        });
      }
      if (url === "/v1/sessions/saved-1/history?after=-1&limit=500") {
        return jsonResponse({
          session_id: "saved-1",
          events: completedTurn,
          next_after: 11,
          has_more: false,
          trace_schema_detected: false,
        });
      }
      if (url === "/v1/sessions/saved-1/messages" && init?.method === "POST") {
        return jsonResponse({ session_id: "saved-1", turn_id: "t2" }, 202);
      }
      if (url === "/v1/sessions/saved-1/events") return eventStreamResponse(encodeEvents(live));
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    const sessionList = await screen.findByTestId("session-list");
    await user.click(within(sessionList).getByRole("button", { name: /previous task/ }));
    await screen.findByText("There are two files.");

    const input = screen.getByPlaceholderText("Message Affent...");
    await user.type(input, "explain Bittensor");
    await user.click(screen.getByRole("button", { name: "Resume" }));

    expect(await within(screen.getByTestId("timeline")).findByText("Bittensor is a decentralized AI network.")).toBeVisible();
    await waitFor(() => expect(fetchImpl).toHaveBeenCalledWith("/v1/sessions/saved-1/events", expect.anything()));
    const eventCall = fetchImpl.mock.calls.find(([url]) => String(url) === "/v1/sessions/saved-1/events");
    const eventHeaders = eventCall?.[1]?.headers as Headers;
    expect(eventHeaders.get("Last-Event-ID")).toBe("11");
  });

  it("refreshes history after sending from a saved chat when no live stream is open yet", async () => {
    const user = userEvent.setup();
    let historyCalls = 0;
    const followUp = [
      ...completedTurn,
      { id: 12, type: "turn.start", data: { turn_id: "t2" } },
      { id: 13, type: "user.message", data: { turn_id: "t2", text: "explain Bittensor" } },
      { id: 14, type: "tool.request", data: {
        turn_id: "t2",
        call_id: "c2",
        tool: "web_fetch",
        args: { url: "https://example.com/bittensor" },
        args_truncated: false,
        args_bytes: 42,
        args_omitted_bytes: 0,
        args_cap_bytes: 65536,
      } },
    ];
    const fetchImpl = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({
          sessions: [
            {
              id: "saved-1",
              active: false,
              durable: true,
              latest_user_message: "previous task",
              has_conversation: true,
              has_events: true,
              has_artifacts: false,
              has_memory: false,
              has_runtime_skills: false,
            },
          ],
          has_more: false,
        });
      }
      if (url === "/v1/sessions/saved-1/history?after=-1&limit=500") {
        historyCalls += 1;
        return jsonResponse({
          session_id: "saved-1",
          events: historyCalls === 1 ? completedTurn : followUp,
          next_after: historyCalls === 1 ? 11 : 14,
          has_more: false,
          trace_schema_detected: false,
        });
      }
      if (url === "/v1/sessions/saved-1/messages" && init?.method === "POST") {
        return jsonResponse({ session_id: "saved-1", turn_id: "t2" }, 202);
      }
      if (url === "/v1/sessions/saved-1/events") return eventStreamResponse("");
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    const sessionList = await screen.findByTestId("session-list");
    await user.click(within(sessionList).getByRole("button", { name: /previous task/ }));
    await screen.findByText("There are two files.");

    await user.type(screen.getByPlaceholderText("Message Affent..."), "explain Bittensor");
    await user.click(screen.getByRole("button", { name: "Resume" }));

    await waitFor(() => {
      const activity = screen.getAllByTestId("agent-activity");
      expect(activity.some((node) => node.textContent?.includes("Fetch example.com/bittensor"))).toBe(true);
    });
    expect(screen.getByTestId("chat-context-bar").querySelector(".chat-context-primary")).toHaveTextContent(
      "Fetch example.com/bittensor",
    );
    expect(screen.getByTestId("chat-context-bar").querySelector(".chat-context-topic")).toBeNull();
    expect(fetchImpl).toHaveBeenCalledWith("/v1/sessions/saved-1/history?after=-1&limit=500", expect.anything());
    expect(historyCalls).toBeGreaterThanOrEqual(2);
  });

  it("keeps the draft when sending a message fails", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({ sessions: [], has_more: false });
      }
      if (url === "/v1/sessions" && init?.method === "POST") {
        return jsonResponse({
          session: {
            id: "new-1",
            active: true,
            durable: true,
            has_conversation: false,
            has_events: false,
            has_artifacts: false,
            has_memory: false,
            has_runtime_skills: false,
          },
        });
      }
      if (url === "/v1/sessions/new-1/messages" && init?.method === "POST") {
        return jsonResponse({ error: { message: "provider unavailable" } }, 503);
      }
      if (url === "/v1/sessions/new-1/history?after=-1&limit=500") {
        return jsonResponse({ session_id: "new-1", events: [], next_after: -1, has_more: false, trace_schema_detected: false });
      }
      if (url === "/v1/sessions/new-1/events") return eventStreamResponse("");
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    const input = await screen.findByPlaceholderText("Message Affent...");
    await user.type(input, "retry-worthy task");
    await user.click(screen.getByRole("button", { name: "Start" }));

    await waitFor(() => expect(screen.getByTestId("connection-pill")).toHaveTextContent("Send failed"));
    expect(input).toHaveValue("retry-worthy task");
    expect(input).toHaveFocus();
    expect(screen.queryByTestId("pending-turn")).toBeNull();
    expect(screen.queryByTestId("session-strip")).toBeNull();
    expect(screen.queryByText("Session details")).toBeNull();
  });

  it("opens a blank chat draft without creating an empty saved chat", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({
          sessions: [
            {
              id: "saved-1",
              active: false,
              durable: true,
              latest_user_message: "existing saved work",
              has_conversation: true,
              has_events: true,
              has_artifacts: false,
              has_memory: false,
              has_runtime_skills: false,
            },
          ],
          has_more: false,
        });
      }
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    const input = await screen.findByPlaceholderText("Message Affent...");
    expect(await screen.findAllByText("existing saved work")).not.toHaveLength(0);
    await user.click(screen.getByRole("button", { name: "New" }));

    await waitFor(() => expect(input).toHaveFocus());
    expect(screen.getByTestId("connection-pill")).toHaveTextContent("Ready");
    expect(screen.getByTestId("connection-pill")).toHaveAttribute("title", "Ready to chat");
    expect(screen.getByTestId("workspace-shell")).toHaveAttribute("data-session-nav", "visible");
    expect(screen.getByTestId("session-list")).toHaveTextContent("existing saved work");
    expect(screen.getByTestId("session-list")).not.toHaveTextContent("New chat");
    expect(fetchImpl).not.toHaveBeenCalledWith("/v1/sessions", expect.objectContaining({ method: "POST" }));
  });

  it("keeps the current chat visible while a switched session history loads", async () => {
    const user = userEvent.setup();
    const s2History = deferred<Response>();
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({
          sessions: [
            {
              id: "s1",
              active: true,
              durable: true,
              has_conversation: true,
              has_events: true,
              has_artifacts: false,
              has_memory: false,
              has_runtime_skills: false,
            },
            {
              id: "s2",
              active: false,
              durable: true,
              has_conversation: false,
              has_events: false,
              has_artifacts: false,
              has_memory: false,
              has_runtime_skills: false,
            },
          ],
          has_more: false,
        });
      }
      if (url === "/v1/sessions/s1/history?after=-1&limit=500") {
        return jsonResponse({
          session_id: "s1",
          events: completedTurn,
          next_after: 11,
          has_more: false,
          trace_schema_detected: false,
        });
      }
      if (url === "/v1/sessions/s2/history?after=-1&limit=500") {
        return s2History.promise;
      }
      if (url === "/v1/sessions/s1/events" || url === "/v1/sessions/s2/events") {
        return eventStreamResponse("");
      }
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    expect(await screen.findByText("There are two files.")).toBeVisible();
    await user.click(screen.getByRole("button", { name: /New chat.*No messages yet/ }));

    expect(screen.getByText("There are two files.")).toBeVisible();
    expect(screen.getByTestId("connection-pill")).toHaveTextContent("Loading chat");
    expect(screen.queryByTestId("timeline-loading")).toBeNull();
    expect(screen.getByTestId("chat-context-bar")).toBeInTheDocument();
    expect(screen.queryByTestId("session-strip")).toBeNull();

    s2History.resolve(jsonResponse({ session_id: "s2", events: [], next_after: -1, has_more: false, trace_schema_detected: false }));
    await waitFor(() => expect(fetchImpl).toHaveBeenCalledWith("/v1/sessions/s2/history?after=-1&limit=500", expect.anything()));
    await waitFor(() => expect(screen.queryByText("There are two files.")).toBeNull());
    expect(screen.queryByTestId("timeline-loading")).toBeNull();
    expect(screen.getByText("What should we work on?")).toBeVisible();
  });

  it("keeps technical server details out of the top status strip", async () => {
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/v1/stats") {
        return jsonResponse({
          listen: "0.0.0.0:7777",
          model: "qwen3.6-35b-a3b",
          executor_mode: "local",
          enable_browser: true,
          enable_web: true,
          enable_web_search: true,
          enable_memory: true,
          enable_builtins: true,
          enable_subagent: true,
          enable_focused_tasks: true,
          active_sessions: 3,
          running_turns: 2,
          shutting_down: false,
          web_search_backend: "tavily",
          browser_cache_dir: "/workspace/browser-cache",
          workspace_root: "/workspace/sessions",
          memory_root: "/workspace/session-state",
          server_time: "2026-05-25T18:55:00Z",
        });
      }
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({ sessions: [], has_more: false });
      }
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    expect(await screen.findByTestId("connection-pill")).toHaveTextContent("Connected");
    expect(screen.queryByTestId("workbench-status-bar")).toBeNull();
    expect(screen.queryByText("Start a task or continue a saved chat")).toBeNull();
    expect(screen.queryByText("qwen3.6-35b-a3b")).toBeNull();
    expect(screen.queryByText("Executor local")).toBeNull();
    expect(screen.queryByText("Browser on")).toBeNull();
    expect(screen.queryByText("Web search on")).toBeNull();
    expect(screen.queryByText("Memory on")).toBeNull();
    expect(screen.queryByText("Listen 0.0.0.0:7777")).toBeNull();
    expect(screen.queryByTestId("profile-dialog")).toBeNull();
  });

  it("keeps the top bar compact when stats polling would fail", async () => {
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/v1/stats") {
        return jsonResponse({ error: { message: "stats offline" } }, 503);
      }
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({ sessions: [], has_more: false });
      }
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    expect(await screen.findByTestId("connection-pill")).toBeVisible();
    expect(screen.queryByTestId("workbench-status-bar")).toBeNull();
    expect(screen.queryByText("Some live status is unavailable")).toBeNull();
  });

  it("shows the selected session title while a titled chat loads", async () => {
    const user = userEvent.setup();
    const s2History = deferred<Response>();
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({
          sessions: [
            {
              id: "s1",
              active: true,
              durable: true,
              topic_user_message: "current work",
              has_conversation: true,
              has_events: true,
              has_artifacts: false,
              has_memory: false,
              has_runtime_skills: false,
            },
            {
              id: "s2",
              active: false,
              durable: true,
              topic_user_message: "affine recent research",
              has_conversation: true,
              has_events: true,
              has_artifacts: false,
              has_memory: false,
              has_runtime_skills: false,
            },
          ],
          has_more: false,
        });
      }
      if (url === "/v1/sessions/s1/history?after=-1&limit=500") {
        return jsonResponse({
          session_id: "s1",
          events: completedTurn,
          next_after: 11,
          has_more: false,
          trace_schema_detected: false,
        });
      }
      if (url === "/v1/sessions/s2/history?after=-1&limit=500") return s2History.promise;
      if (url === "/v1/sessions/s1/events" || url === "/v1/sessions/s2/events") return eventStreamResponse("");
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    expect(await screen.findByText("There are two files.")).toBeVisible();
    await user.click(screen.getByRole("button", { name: /affine recent research/i }));

    expect(screen.getByTestId("connection-pill")).toHaveTextContent("Loading Affine recent research");
    expect(screen.queryByTestId("timeline-loading")).toBeNull();

    s2History.resolve(jsonResponse({ session_id: "s2", events: [], next_after: -1, has_more: false, trace_schema_detected: false }));
    await waitFor(() => expect(fetchImpl).toHaveBeenCalledWith("/v1/sessions/s2/history?after=-1&limit=500", expect.anything()));
    await waitFor(() => expect(screen.queryByTestId("timeline-loading")).toBeNull());
  });

  it("applies live SSE events after persisted history", async () => {
    const live = [
      { id: 12, type: "turn.start", data: { turn_id: "t2" } },
      { id: 13, type: "user.message", data: { turn_id: "t2", text: "say hi" } },
      { id: 14, type: "message.done", data: { turn_id: "t2", text: "hi", finish_reason: "stop" } },
      { id: 15, type: "turn.end", data: { turn_id: "t2", reason: "completed" } },
    ];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url === "/v1/sessions?limit=100") {
          return jsonResponse({
            sessions: [{ id: "s1", active: true, durable: true, has_conversation: true, has_events: true, has_artifacts: false, has_memory: false, has_runtime_skills: false }],
            has_more: false,
          });
        }
        if (url === "/v1/sessions/s1/history?after=-1&limit=500") {
          return jsonResponse({ session_id: "s1", events: [], next_after: -1, has_more: false, trace_schema_detected: false });
        }
        if (url === "/v1/sessions/s1/events") return eventStreamResponse(encodeEvents(live));
        return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
      }),
    );

    render(<App />);

    expect((await screen.findAllByText("say hi"))[0]).toBeVisible();
    expect(screen.getByTestId("msg-assistant")).toHaveTextContent("hi");
  });

  it("moves tool Next guidance into the composer draft", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({
          sessions: [{ id: "s1", active: true, durable: true, has_conversation: true, has_events: true, has_artifacts: false, has_memory: false, has_runtime_skills: false }],
          has_more: false,
        });
      }
      if (url === "/v1/sessions/s1/history?after=-1&limit=500") {
        return jsonResponse({ session_id: "s1", events: toolError, next_after: 6, has_more: false, trace_schema_detected: false });
      }
      if (url === "/v1/sessions/s1/events") return eventStreamResponse("");
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    await user.click(await screen.findByRole("button", { name: /Action details/ }));
    const executionTree = await screen.findByTestId("execution-tree");
    await user.click(within(executionTree).getByRole("button", { name: /make/ }));
    await user.click(screen.getByRole("button", { name: "Use as message" }));

    expect(screen.getByPlaceholderText("Message Affent...")).toHaveValue("Continue: check the Makefile path");
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Using suggested next step");
    expect(screen.getByPlaceholderText("Message Affent...")).toHaveFocus();
  });

  it("moves live activity guidance into the busy composer", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({
          sessions: [{ id: "s1", active: true, durable: true, has_conversation: true, has_events: true, has_artifacts: false, has_memory: false, has_runtime_skills: false }],
          has_more: false,
        });
      }
      if (url === "/v1/sessions/s1/history?after=-1&limit=500") {
        return jsonResponse({ session_id: "s1", events: runningSubagent, next_after: 3, has_more: false, trace_schema_detected: false });
      }
      if (url === "/v1/sessions/s1/events") return eventStreamResponse("");
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    await user.click(await screen.findByRole("button", { name: "Guide run" }));

    expect(screen.getByPlaceholderText("Message Affent...")).toHaveValue("Guidance for current run:");
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Using suggested next step");
    expect(within(screen.getByTestId("composer")).getByRole("button", { name: "Send guidance" })).toBeEnabled();
    expect(screen.getByPlaceholderText("Message Affent...")).toHaveFocus();
  });

  it("labels in-flight live guidance separately from a new task", async () => {
    const user = userEvent.setup();
    const sent = deferred<Response>();
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({
          sessions: [{ id: "s1", active: true, durable: true, has_conversation: true, has_events: true, has_artifacts: false, has_memory: false, has_runtime_skills: false }],
          has_more: false,
        });
      }
      if (url === "/v1/sessions/s1/history?after=-1&limit=500") {
        return jsonResponse({ session_id: "s1", events: runningSubagent, next_after: 3, has_more: false, trace_schema_detected: false });
      }
      if (url === "/v1/sessions/s1/events") return eventStreamResponse("");
      if (url === "/v1/sessions/s1/messages") return sent.promise;
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    const context = await screen.findByTestId("chat-context-bar");
    expect(context).toHaveTextContent("Working");
    expect(context.querySelector(".chat-context-topic")).toBeNull();
    await user.click(await screen.findByRole("button", { name: "Guide run" }));
    await user.type(screen.getByPlaceholderText("Message Affent..."), "check tests first");
    await user.click(within(screen.getByTestId("composer")).getByRole("button", { name: "Send guidance" }));

    expect(screen.getByTestId("pending-turn")).toHaveAttribute("data-kind", "guidance");
    expect(screen.getByTestId("pending-turn")).toHaveTextContent("Guidance");
    expect(screen.getByTestId("chat-context-bar")).toHaveTextContent("Sending guidance");
    expect(screen.getByTestId("chat-context-bar")).toHaveTextContent("Applying your guidance");
    expect(screen.getByTestId("chat-context-bar")).toHaveTextContent("Task:");
    expect(screen.getByTestId("pending-turn")).toHaveTextContent("Applying your guidance to the current run.");
    expect(screen.getByTestId("pending-turn")).not.toHaveTextContent("Preparing the first update.");

    sent.resolve(jsonResponse({ session_id: "s1", turn_id: "t1" }));
    await waitFor(() => expect(screen.queryByTestId("pending-turn")).toBeNull());
    expect(screen.getByTestId("guidance-receipt")).toHaveTextContent("Guidance sent");
    expect(screen.getByTestId("guidance-receipt")).toHaveTextContent("check tests first");
    expect(screen.getByTestId("guidance-receipt")).toHaveTextContent("Affent will use this in the current run.");

    await user.click(screen.getByRole("button", { name: "Edit guidance" }));

    expect(screen.getByPlaceholderText("Message Affent...")).toHaveValue("Guidance for current run:check tests first");
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Editing sent guidance");
  });

  it("shows a stopping state while a running turn is being cancelled", async () => {
    const user = userEvent.setup();
    const cancelled = deferred<Response>();
    let historyCalls = 0;
    const fetchImpl = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({
          sessions: [{ id: "s1", active: true, durable: true, has_conversation: true, has_events: true, has_artifacts: false, has_memory: false, has_runtime_skills: false }],
          has_more: false,
        });
      }
      if (url === "/v1/sessions/s1/history?after=-1&limit=500") {
        historyCalls += 1;
        return jsonResponse({
          session_id: "s1",
          events: historyCalls === 1 ? runningSubagent : cancelledTurn,
          next_after: 3,
          has_more: false,
          trace_schema_detected: false,
        });
      }
      if (url === "/v1/sessions/s1/events") return eventStreamResponse("");
      if (url === "/v1/sessions/s1/cancel" && init?.method === "POST") return cancelled.promise;
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    await screen.findByRole("button", { name: "Stop" });
    await user.click(screen.getByRole("button", { name: "Guide run" }));
    await user.type(screen.getByPlaceholderText("Message Affent..."), "please stop after this");
    await user.click(within(screen.getByTestId("composer")).getByRole("button", { name: "Stop" }));

    expect(screen.getByTestId("composer")).toHaveAttribute("data-cancelling", "true");
    expect(screen.getByTestId("composer-intent")).toHaveTextContent("Stopping run");
    expect(within(screen.getByTestId("composer")).getByRole("button", { name: "Stopping" })).toBeDisabled();
    expect(within(screen.getByTestId("composer")).getByRole("button", { name: "Send guidance" })).toBeDisabled();

    cancelled.resolve(jsonResponse({ session_id: "s1", cancelled: true }));
    await waitFor(() => expect(screen.getByTestId("composer")).toHaveAttribute("data-cancelling", "false"));
    expect(fetchImpl).toHaveBeenCalledWith("/v1/sessions/s1/cancel", expect.objectContaining({ method: "POST" }));
  });

  it("moves an expanded tool result into the composer draft", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({
          sessions: [{ id: "s1", active: true, durable: true, has_conversation: true, has_events: true, has_artifacts: false, has_memory: false, has_runtime_skills: false }],
          has_more: false,
        });
      }
      if (url === "/v1/sessions/s1/history?after=-1&limit=500") {
        return jsonResponse({ session_id: "s1", events: completedTurn, next_after: 11, has_more: false, trace_schema_detected: false });
      }
      if (url === "/v1/sessions/s1/events") return eventStreamResponse("");
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    await user.click(await screen.findByRole("button", { name: /Run summary/ }));
    await user.click(screen.getByRole("button", { name: /List current directory/ }));
    await user.click(screen.getByRole("button", { name: "Use output" }));

    expect(screen.getByPlaceholderText("Message Affent...")).toHaveValue(
      [
        "Use this output in the next step:",
        "Action: List current directory",
        "Tool: list_files",
        "Output:\nREADME.md\nmain.go",
      ].join("\n"),
    );
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Using action output");
    expect(screen.getByPlaceholderText("Message Affent...")).toHaveFocus();
  });

  it("moves a failed tool retry into the composer draft", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({
          sessions: [{ id: "s1", active: true, durable: true, has_conversation: true, has_events: true, has_artifacts: false, has_memory: false, has_runtime_skills: false }],
          has_more: false,
        });
      }
      if (url === "/v1/sessions/s1/history?after=-1&limit=500") {
        return jsonResponse({ session_id: "s1", events: toolError, next_after: 6, has_more: false, trace_schema_detected: false });
      }
      if (url === "/v1/sessions/s1/events") return eventStreamResponse("");
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    await user.click(await screen.findByRole("button", { name: /Action details/ }));
    const executionTree = await screen.findByTestId("execution-tree");
    await user.click(within(executionTree).getByRole("button", { name: /make/ }));
    await user.click(screen.getByRole("button", { name: "Retry action" }));

    expect(screen.getByPlaceholderText("Message Affent...")).toHaveValue(
      [
        "Retry the failed action: make",
        "Tool: shell",
        "Next: check the Makefile path",
        'Args:\n{\n  "command": "make"\n}',
      ].join("\n"),
    );
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Retrying failed action");
    expect(screen.getByPlaceholderText("Message Affent...")).toHaveFocus();
  });

  it("moves max-turn continuation into the composer draft", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({
          sessions: [{ id: "s1", active: true, durable: true, has_conversation: true, has_events: true, has_artifacts: false, has_memory: false, has_runtime_skills: false }],
          has_more: false,
        });
      }
      if (url === "/v1/sessions/s1/history?after=-1&limit=500") {
        return jsonResponse({ session_id: "s1", events: maxTurns, next_after: 3, has_more: false, trace_schema_detected: false });
      }
      if (url === "/v1/sessions/s1/events") return eventStreamResponse("");
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    await user.click(await screen.findByRole("button", { name: "Ask for final answer" }));

    expect(screen.getByPlaceholderText("Message Affent...")).toHaveValue(
      "Do not call more tools. Based only on the evidence already gathered in this chat, produce the final answer.",
    );
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Requesting final answer");
  });

  it("moves a chat artifact into the composer draft", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({
          sessions: [{ id: "s1", active: true, durable: true, has_conversation: true, has_events: true, has_artifacts: true, has_memory: false, has_runtime_skills: false }],
          has_more: false,
        });
      }
      if (url === "/v1/sessions/s1/history?after=-1&limit=500") {
        return jsonResponse({ session_id: "s1", events: resultTruncated, next_after: 3, has_more: false, trace_schema_detected: false });
      }
      if (url === "/v1/sessions/s1/events") return eventStreamResponse("");
      if (url === "/v1/sessions/s1/artifacts/.affent/artifacts/tool-results/000001-c1.txt?offset=0&limit=65536") {
        return new Response("artifact payload", {
          status: 200,
          headers: {
            "X-Affent-Artifact-Path": ".affent/artifacts/tool-results/000001-c1.txt",
            "X-Affent-Artifact-Bytes": "16",
            "X-Affent-Artifact-Offset": "0",
          },
        });
      }
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    await user.click(await screen.findByRole("button", { name: "Use in message" }));

    expect(screen.getByPlaceholderText("Message Affent...")).toHaveValue(
      "Use this file in the next step: .affent/artifacts/tool-results/000001-c1.txt",
    );
    expect(screen.getByTestId("composer-context")).toHaveTextContent("File added to message");

    await user.click(screen.getByRole("button", { name: "Open file" }));
    await user.click(await screen.findByRole("button", { name: "Use text" }));

    expect(screen.getByPlaceholderText("Message Affent...")).toHaveValue(
      [
        "Use this loaded file text in the next step:",
        "File: .affent/artifacts/tool-results/000001-c1.txt",
        "Text:\nartifact payload",
      ].join("\n"),
    );
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Using file text");
  });

  it("moves an assistant answer into a follow-up draft", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({
          sessions: [{ id: "s1", active: true, durable: true, has_conversation: true, has_events: true, has_artifacts: false, has_memory: false, has_runtime_skills: false }],
          has_more: false,
        });
      }
      if (url === "/v1/sessions/s1/history?after=-1&limit=500") {
        return jsonResponse({ session_id: "s1", events: completedTurn, next_after: 11, has_more: false, trace_schema_detected: false });
      }
      if (url === "/v1/sessions/s1/events") return eventStreamResponse("");
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    await user.click(await screen.findByRole("button", { name: "Ask follow-up" }));

    expect(screen.getByPlaceholderText("Message Affent...")).toHaveValue(
      "Continue from this answer: There are two files.",
    );
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Continuing from answer");
    expect(screen.getByPlaceholderText("Message Affent...")).toHaveFocus();
  });

  it("moves an assistant answer into a retry draft", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({
          sessions: [{ id: "s1", active: true, durable: true, has_conversation: true, has_events: true, has_artifacts: false, has_memory: false, has_runtime_skills: false }],
          has_more: false,
        });
      }
      if (url === "/v1/sessions/s1/history?after=-1&limit=500") {
        return jsonResponse({ session_id: "s1", events: completedTurn, next_after: 11, has_more: false, trace_schema_detected: false });
      }
      if (url === "/v1/sessions/s1/events") return eventStreamResponse("");
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    await user.click(await screen.findByRole("button", { name: "Retry from here" }));

    expect(screen.getByPlaceholderText("Message Affent...")).toHaveValue(
      "Retry from this reply:\n\nThere are two files.",
    );
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Retrying from reply");
    expect(screen.getByRole("button", { name: "Retry" })).toBeEnabled();
  });

  it("keeps previous prompt editing out of the composer flow", async () => {
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({
          sessions: [{ id: "s1", active: true, durable: true, has_conversation: true, has_events: true, has_artifacts: false, has_memory: false, has_runtime_skills: false }],
          has_more: false,
        });
      }
      if (url === "/v1/sessions/s1/history?after=-1&limit=500") {
        return jsonResponse({ session_id: "s1", events: completedTurn, next_after: 11, has_more: false, trace_schema_detected: false });
      }
      if (url === "/v1/sessions/s1/events") return eventStreamResponse("");
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    await screen.findByText("There are two files.");

    expect(screen.queryByRole("button", { name: "Edit prompt" })).toBeNull();
    expect(screen.getByPlaceholderText("Message Affent...")).toHaveValue("");
  });
});

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function chatContextMetric(root: HTMLElement, text: string): HTMLElement {
  return within(root).getAllByText((_, element) => element?.textContent?.includes(text) ?? false, { selector: "span" })[0];
}

function eventStreamResponse(body: string): Response {
  return new Response(body, {
    status: 200,
    headers: { "Content-Type": "text/event-stream" },
  });
}

function encodeEvents(events: { id: number; type: string; data: unknown }[]): string {
  return events
    .map((ev) => `event: ${ev.type}\nid: ${ev.id}\ndata: ${JSON.stringify(ev.data)}\n\n`)
    .join("");
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}
