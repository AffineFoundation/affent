import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "./App";
import { completedTurn } from "./fixtures/completedTurn";
import { cancelledTurn, maxTurns, resultTruncated, runningSubagent, toolError } from "./fixtures/scenarios";

async function openMessageOptions(user: ReturnType<typeof userEvent.setup>, scope = document.body) {
  await user.click(within(scope).getByRole("button", { name: "Message options" }));
}

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

  it("defaults to white even when the device prefers dark colors", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => Promise.reject(new Error("network down"))));
    Object.defineProperty(window, "matchMedia", {
      configurable: true,
      value: vi.fn(() => ({ matches: true })),
    });

    render(<App />);

    await waitFor(() => expect(screen.getByTestId("app-shell")).toHaveAttribute("data-theme", "light"));
    expect(document.documentElement).toHaveAttribute("data-theme", "light");
  });

  it("lets mobile users explicitly hide and restore the top controls", async () => {
    const user = userEvent.setup();
    vi.stubGlobal("fetch", vi.fn(async () => Promise.reject(new Error("network down"))));

    render(<App />);

    await user.click(screen.getByRole("button", { name: "Hide top controls" }));
    expect(screen.getByTestId("app-shell")).toHaveAttribute("data-mobile-topbar", "hidden");
    expect(screen.getByRole("button", { name: "Show top controls" })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Show top controls" }));
    expect(screen.getByTestId("app-shell")).toHaveAttribute("data-mobile-topbar", "visible");
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
              latest_memory_update: {
                action: "add",
                target: "memory",
                topic: "core",
                location: "memory:core",
                preview: "project facts are durable",
              },
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
      if (url === "/v1/sessions/research-1/memory") {
        return jsonResponse({
          session_id: "research-1",
          has_memory: true,
          user: {
            target: "user",
            topic: "user",
            entries: ["prefers concise reports"],
            entry_count: 1,
            chars_used: 23,
            chars_limit: 1375,
            percent: 1,
          },
          core: {
            target: "memory",
            topic: "core",
            entries: ["project facts are durable"],
            entry_count: 1,
            chars_used: 25,
            chars_limit: 2200,
            percent: 1,
          },
          topics: [],
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
    await userEvent.click(screen.getByLabelText("Workbench"));
    expect(await screen.findByTestId("session-memory-panel")).toHaveTextContent("2 entries");
    expect(screen.getByTestId("session-memory-latest")).toHaveTextContent("Latest update");
    expect(screen.getByTestId("session-memory-latest")).toHaveTextContent("memory:core");
    expect(screen.getByTestId("session-memory-panel")).toHaveTextContent("project facts are durable");
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

  it("starts loop activation as a draft before sending the refinement turn", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({ sessions: [], has_more: false });
      }
      if (url === "/v1/sessions" && init?.method === "POST") {
        return jsonResponse({
          session: {
            id: "loop-1",
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
      if (url === "/v1/sessions/loop-1/loop-protocol" && init?.method === "POST") {
        return jsonResponse({
          session_id: "loop-1",
          protocol: "# Loop Protocol\n\n- status: draft",
          summary: { path: ".affent/loops/loop-1/LOOP.md", status: "draft", bytes: 32 },
          state: { version: 1, loop_id: "loop-1", status: "draft", initial_goal_preview: "analyze market data for several days" },
          events: [],
        });
      }
      if (url === "/v1/sessions/loop-1/messages" && init?.method === "POST") {
        return jsonResponse({ session_id: "loop-1", turn_id: "t1" });
      }
      if (url === "/v1/sessions/loop-1/history?after=-1&limit=500") {
        return jsonResponse({ session_id: "loop-1", events: [], next_after: -1, has_more: false, trace_schema_detected: false });
      }
      if (url === "/v1/sessions/loop-1/events") return eventStreamResponse("");
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    const input = await screen.findByPlaceholderText("Message Affent...");
    await user.type(input, "analyze market data for several days");
    await user.click(within(screen.getByTestId("composer-automation")).getByText("Automation"));
    await user.click(within(screen.getByTestId("composer-automation")).getByRole("button", { name: "Set up loop" }));

    await waitFor(() => expect(fetchImpl).toHaveBeenCalledWith("/v1/sessions/loop-1/loop-protocol", expect.objectContaining({ method: "POST" })));
    const loopCall = fetchImpl.mock.calls.find(([url]) => String(url) === "/v1/sessions/loop-1/loop-protocol");
    expect((loopCall?.[1] as RequestInit).body).toBe(JSON.stringify({ activate: true, goal: "analyze market data for several days" }));
    const messageCall = fetchImpl.mock.calls.find(([url]) => String(url) === "/v1/sessions/loop-1/messages");
    const sent = JSON.parse(String((messageCall?.[1] as RequestInit).body)) as { content: string; display_text?: string };
    expect(sent.content).toContain("Loop protocol activation is pending");
    expect(sent.content).toContain("chat or the WebUI");
    expect(sent.content).toContain("loop_protocol action=read");
    expect(sent.content).toContain("Ask exactly one concise calibration question now");
    expect(sent.content).toContain("ask one focused follow-up in a later turn");
    expect(sent.content).toContain("Do not complete activation in the same turn");
    expect(sent.content).toContain("wait for the user's answer");
    expect(sent.content).toContain("complete_activation");
    expect(sent.content).toContain("Current Situation");
    expect(sent.content).toContain("1200 characters");
    expect(await screen.findByTestId("session-list")).toHaveTextContent("Loop draft");
    expect(screen.getByTestId("session-list")).toHaveTextContent("analyze market data");
  });

  it("keeps empty loop setup out of the selected session surface", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({
          sessions: [
            {
              id: "loop-panel",
              active: true,
              durable: true,
              topic_user_message: "long running subnet analysis",
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
      if (url === "/v1/sessions/loop-panel/history?after=-1&limit=500") {
        return jsonResponse({ session_id: "loop-panel", events: [], next_after: -1, has_more: false, trace_schema_detected: false });
      }
      if (url === "/v1/sessions/loop-panel/events") return eventStreamResponse("");
      if (url === "/v1/sessions/loop-panel/loop-protocol" && init?.method === "POST") {
        return jsonResponse({
          session_id: "loop-panel",
          protocol: "# Loop Protocol\n\n- status: draft",
          summary: { path: ".affent/loops/loop-panel/LOOP.md", status: "draft", bytes: 32 },
          state: { version: 1, loop_id: "loop-panel", status: "draft", initial_goal_preview: "long running subnet analysis" },
          events: [],
        });
      }
      if (url === "/v1/sessions/loop-panel/messages" && init?.method === "POST") {
        return jsonResponse({ session_id: "loop-panel", turn_id: "t1" });
      }
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    await waitFor(() => expect(screen.getByTestId("session-list")).toHaveTextContent("long running subnet analysis"));
    expect(screen.queryByTestId("session-loop-panel")).toBeNull();
    await user.type(screen.getByPlaceholderText("Message Affent..."), "long running subnet analysis");
    await user.click(within(screen.getByTestId("composer-automation")).getByText("Automation"));
    await user.click(within(screen.getByTestId("composer-automation")).getByRole("button", { name: "Set up loop" }));

    await waitFor(() => expect(fetchImpl).toHaveBeenCalledWith("/v1/sessions/loop-panel/loop-protocol", expect.objectContaining({ method: "POST" })));
    const loopCall = fetchImpl.mock.calls.find(([url]) => String(url) === "/v1/sessions/loop-panel/loop-protocol");
    expect((loopCall?.[1] as RequestInit).body).toBe(JSON.stringify({ activate: true, goal: "long running subnet analysis" }));
    const messageCall = fetchImpl.mock.calls.find(([url]) => String(url) === "/v1/sessions/loop-panel/messages");
    const sent = JSON.parse(String((messageCall?.[1] as RequestInit).body)) as { content: string };
    expect(sent.content).toContain("Ask exactly one concise calibration question now");
    expect(sent.content).toContain("ask one focused follow-up in a later turn");
    expect(sent.content).toContain("Do not complete activation in the same turn");
    expect(sent.content).toContain("complete_activation");
    expect(sent.content).toContain("1200 characters");
    expect(sent).toMatchObject({ display_text: "Set up loop: long running subnet analysis" });
    expect(screen.getByTestId("pending-turn")).toHaveTextContent("Set up loop: long running subnet analysis");
    expect(screen.getByTestId("pending-turn")).not.toHaveTextContent("complete_activation");
    expect(await screen.findByTestId("session-loop-panel")).toHaveTextContent("Draft");
    expect(screen.getByTestId("session-loop-panel")).toHaveTextContent("Setup pending");
    expect(screen.getByTestId("session-loop-panel")).toHaveTextContent("activate after your answer");
  });

  it("continues draft loop setup from recorded calibration without asking again", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({
          sessions: [
            {
              id: "loop-draft-answer",
              active: true,
              durable: true,
              topic_user_message: "long running subnet analysis",
              has_conversation: true,
              has_events: true,
              has_artifacts: false,
              has_memory: false,
              has_runtime_skills: false,
              has_loop_protocol: true,
              loop_protocol: {
                path: ".affent/loops/loop-draft-answer/LOOP.md",
                status: "draft",
                bytes: 512,
                preview: "Draft loop protocol",
                state: {
                  version: 1,
                  loop_id: "loop-draft-answer",
                  status: "draft",
                  initial_goal_preview: "long running subnet analysis",
                  calibration_answers: 1,
                  last_calibration_answer_preview: "Stop when taostats evidence or source confidence is weak.",
                },
              },
              loop_state: {
                version: 1,
                loop_id: "loop-draft-answer",
                status: "draft",
                initial_goal_preview: "long running subnet analysis",
                calibration_answers: 1,
                last_calibration_answer_preview: "Stop when taostats evidence or source confidence is weak.",
              },
            },
          ],
          has_more: false,
        });
      }
      if (url === "/v1/sessions/loop-draft-answer/history?after=-1&limit=500") {
        return jsonResponse({ session_id: "loop-draft-answer", events: [], next_after: -1, has_more: false, trace_schema_detected: false });
      }
      if (url === "/v1/sessions/loop-draft-answer/events") return eventStreamResponse("");
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    expect(await screen.findByTestId("session-automation-panel")).toHaveTextContent("Loop review");
    const panel = await screen.findByTestId("session-loop-panel");
    expect(panel).toHaveTextContent("Activation review");
    await user.click(within(panel).getByRole("button", { name: "Review in chat" }));

    const draft = (screen.getByPlaceholderText("Message Affent...") as HTMLTextAreaElement).value;
    expect(draft).toContain("A calibration answer is already recorded");
    expect(draft).toContain("Stop when taostats evidence or source confidence is weak.");
    expect(draft).toContain("metadata status: running");
    expect(draft).toContain("loop_protocol action=complete_activation");
    expect(draft).toContain("Current Situation at or below 1200 characters");
    expect(draft).toContain("ask exactly one focused missing-field question");
    expect(draft).not.toContain("Ask one concise calibration question before changing the protocol");
  });

  it("prefills the pending loop calibration question as a direct answer draft", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({
          sessions: [
            {
              id: "loop-draft-question",
              active: true,
              durable: true,
              topic_user_message: "long running subnet analysis",
              has_conversation: true,
              has_events: true,
              has_artifacts: false,
              has_memory: false,
              has_runtime_skills: false,
              has_loop_protocol: true,
              loop_protocol: {
                path: ".affent/loops/loop-draft-question/LOOP.md",
                status: "draft",
                bytes: 512,
                preview: "Draft loop protocol",
                state: {
                  version: 1,
                  loop_id: "loop-draft-question",
                  status: "draft",
                  initial_goal_preview: "long running subnet analysis",
                  calibration_questions: 1,
                  last_calibration_question_preview: "What evidence quality should pause this loop?",
                  calibration_answers: 0,
                },
              },
              loop_state: {
                version: 1,
                loop_id: "loop-draft-question",
                status: "draft",
                initial_goal_preview: "long running subnet analysis",
                calibration_questions: 1,
                last_calibration_question_preview: "What evidence quality should pause this loop?",
                calibration_answers: 0,
              },
            },
          ],
          has_more: false,
        });
      }
      if (url === "/v1/sessions/loop-draft-question/history?after=-1&limit=500") {
        return jsonResponse({ session_id: "loop-draft-question", events: [], next_after: -1, has_more: false, trace_schema_detected: false });
      }
      if (url === "/v1/sessions/loop-draft-question/events") return eventStreamResponse("");
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    expect(await screen.findByTestId("session-automation-panel")).toHaveTextContent("Loop waiting");
    const panel = await screen.findByTestId("session-loop-panel");
    expect(panel).toHaveTextContent("Waiting for your calibration answer");
    await user.click(within(panel).getByRole("button", { name: "Open answer draft" }));

    const draft = (screen.getByPlaceholderText("Message Affent...") as HTMLTextAreaElement).value;
    expect(draft).toContain("Loop calibration answer for: long running subnet analysis");
    expect(draft).toContain("Pending question: What evidence quality should pause this loop?");
    expect(draft).toContain("My answer:");
    expect(draft).not.toContain("Ask one concise calibration question before changing the protocol");
  });

  it("shows and disables the selected session loop protocol", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({
          sessions: [
            {
              id: "loop-control",
              active: true,
              durable: true,
              has_conversation: false,
              has_events: false,
              has_artifacts: false,
              has_memory: false,
              has_runtime_skills: false,
              has_loop_protocol: true,
              loop_protocol: {
                path: ".affent/loops/loop-control/LOOP.md",
                status: "running",
                bytes: 512,
                preview: "Keep market evidence recoverable.",
                state: {
                  version: 1,
                  loop_id: "loop-control",
                  status: "running",
                  initial_goal_preview: "watch market evidence for several days",
                  protocol_updates: 1,
                  protocol_feeds: 2,
                },
              },
            },
          ],
          has_more: false,
        });
      }
      if (url === "/v1/sessions/loop-control/history?after=-1&limit=500") {
        return jsonResponse({ session_id: "loop-control", events: [], next_after: -1, has_more: false });
      }
      if (url === "/v1/sessions/loop-control/events") return eventStreamResponse("");
      if (url === "/v1/sessions/loop-control/loop-protocol" && (!init?.method || init.method === "GET")) {
        return jsonResponse({
          session_id: "loop-control",
          protocol: "# Loop Protocol: loop-control\n\n- status: running\n\n## 1. North Star\n\nwatch market evidence",
          summary: {
            path: ".affent/loops/loop-control/LOOP.md",
            status: "running",
            bytes: 92,
            preview: "watch market evidence",
          },
          state: {
            version: 1,
            loop_id: "loop-control",
            status: "running",
            initial_goal_preview: "watch market evidence for several days",
          },
          events: [],
        });
      }
      if (url === "/v1/sessions/loop-control/loop-protocol" && init?.method === "DELETE") {
        return jsonResponse({
          session_id: "loop-control",
          cleared: true,
          state: {
            version: 1,
            loop_id: "loop-control",
            status: "disabled",
            event_count: 2,
            last_event_summary: "Disabled LOOP.md",
          },
          events: [],
        });
      }
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    expect(await screen.findByTestId("session-automation-panel")).toHaveTextContent("Loop running");
    const panel = await screen.findByTestId("session-loop-panel");
    expect(panel).toHaveTextContent("Running");
    expect(panel).toHaveTextContent("Running protocol");
    expect(panel).toHaveTextContent("watch market evidence for several days");
    expect(panel).toHaveTextContent(".affent/loops/loop-control/LOOP.md");

    await user.click(within(panel).getByRole("button", { name: "View LOOP.md" }));
    expect(await screen.findByTestId("session-loop-protocol")).toHaveTextContent("# Loop Protocol: loop-control");
    await user.click(within(panel).getByRole("button", { name: "Update via chat" }));
    expect((screen.getByPlaceholderText("Message Affent...") as HTMLTextAreaElement).value).toContain("Review and update LOOP.md");

    await user.click(within(panel).getByRole("button", { name: "Disable loop" }));

    await waitFor(() => expect(fetchImpl).toHaveBeenCalledWith("/v1/sessions/loop-control/loop-protocol", expect.objectContaining({ method: "DELETE" })));
    expect(await screen.findByTestId("session-automation-panel")).toHaveTextContent("Loop disabled");
    expect(await screen.findByTestId("session-loop-panel")).toHaveTextContent("Disabled");
    expect(screen.getByTestId("session-list")).toHaveTextContent("Loop disabled");
    expect(screen.queryByRole("button", { name: "Disable loop" })).toBeNull();
  });

  it("schedules a session check-in from the unified automation menu", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({
          sessions: [
            {
              id: "timer-control",
              active: true,
              durable: true,
              topic_user_message: "long running subnet analysis",
              has_conversation: false,
              has_events: false,
              has_artifacts: false,
              has_memory: false,
              has_runtime_skills: false,
              has_schedules: false,
            },
          ],
          has_more: false,
        });
      }
      if (url === "/v1/sessions/timer-control/history?after=-1&limit=500") {
        return jsonResponse({ session_id: "timer-control", events: [], next_after: -1, has_more: false });
      }
      if (url === "/v1/sessions/timer-control/events") return eventStreamResponse("");
      if (url === "/v1/sessions/timer-control/schedules" && init?.method === "POST") {
        return jsonResponse({
          session_id: "timer-control",
          schedules: [
            {
              id: "sched_1",
              kind: "checkin",
              prompt: "Scheduled check-in for session: long running subnet analysis",
              display_text: "Check in 1h: long running subnet analysis",
              enabled: true,
              next_run_at: "2026-05-27T14:30:00Z",
              created_at: "2026-05-27T13:30:00Z",
              updated_at: "2026-05-27T13:30:00Z",
            },
          ],
          summary: {
            count: 1,
            enabled: 1,
            next_run_at: "2026-05-27T14:30:00Z",
            next_schedule_id: "sched_1",
            next_prompt_preview: "Check in 1h: long running subnet analysis",
          },
        });
      }
      if (url === "/v1/sessions/timer-control/loop-protocol" && init?.method === "POST") {
        return jsonResponse({
          session_id: "timer-control",
          protocol: "# Loop Protocol\n\n- status: draft",
          summary: { path: ".affent/loops/timer-control/LOOP.md", status: "draft", bytes: 32 },
          state: { version: 1, loop_id: "timer-control", status: "draft", initial_goal_preview: "Scheduled check-in for long running subnet analysis" },
          events: [],
        });
      }
      if (url === "/v1/sessions/timer-control/messages" && init?.method === "POST") {
        return jsonResponse({ session_id: "timer-control", turn_id: "timer_calibration" });
      }
      if (url === "/v1/sessions/timer-control/schedules/sched_1" && init?.method === "PATCH") {
        const body = JSON.parse(String(init.body)) as { enabled: boolean };
        return jsonResponse({
          session_id: "timer-control",
          schedules: [
            {
              id: "sched_1",
              kind: "checkin",
              prompt: "Scheduled check-in for session: long running subnet analysis",
              display_text: "Check in 1h: long running subnet analysis",
              enabled: body.enabled,
              next_run_at: "2026-05-27T14:30:00Z",
              created_at: "2026-05-27T13:30:00Z",
              updated_at: "2026-05-27T13:40:00Z",
            },
          ],
          summary: body.enabled
            ? {
                count: 1,
                enabled: 1,
                next_run_at: "2026-05-27T14:30:00Z",
                next_schedule_id: "sched_1",
                next_prompt_preview: "Check in 1h: long running subnet analysis",
              }
            : { count: 1, enabled: 0 },
        });
      }
      if (url === "/v1/sessions/timer-control/schedules/sched_1" && init?.method === "DELETE") {
        return jsonResponse({
          session_id: "timer-control",
          schedule_id: "sched_1",
          cleared: true,
          summary: { count: 0, enabled: 0 },
        });
      }
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    await waitFor(() => expect(screen.getByTestId("session-list")).toHaveTextContent("long running subnet analysis"));
    expect(screen.queryByTestId("session-schedule-panel")).toBeNull();
    await user.click(within(screen.getByTestId("composer-automation")).getByText("Automation"));
    await user.click(within(screen.getByTestId("composer-automation")).getByRole("button", { name: "Check in 1h" }));

    await waitFor(() => expect(fetchImpl).toHaveBeenCalledWith("/v1/sessions/timer-control/schedules", expect.objectContaining({ method: "POST" })));
    const scheduleCall = fetchImpl.mock.calls.find(([url]) => String(url) === "/v1/sessions/timer-control/schedules");
    const body = JSON.parse(String((scheduleCall?.[1] as RequestInit).body)) as { kind?: string; prompt: string; display_text?: string; next_run_at: string; enabled: boolean };
    expect(body.kind).toBe("checkin");
    expect(body.prompt).toContain("Scheduled check-in for session: long running subnet analysis");
    expect(body.prompt).toContain("ask the user one concise question");
    expect(body.prompt).toContain("loop_protocol action=read");
    expect(body.prompt).toContain("Current Situation");
    expect(body.prompt).toContain("1200 characters");
    expect(body.display_text).toBe("Check in 1h: long running subnet analysis");
    expect(body.enabled).toBe(true);
    expect(body.next_run_at).toMatch(/Z$/);
    const loopCall = fetchImpl.mock.calls.find(([url]) => String(url) === "/v1/sessions/timer-control/loop-protocol");
    expect((loopCall?.[1] as RequestInit).body).toBe(JSON.stringify({ activate: true, goal: "Scheduled check-in for long running subnet analysis" }));
    const messageCall = fetchImpl.mock.calls.find(([url]) => String(url) === "/v1/sessions/timer-control/messages");
    const sent = JSON.parse(String((messageCall?.[1] as RequestInit).body)) as { content: string; display_text?: string };
    expect(sent.content).toContain("Calibrate scheduled check-in");
    expect(sent.content).toContain("Ask the user one concise question now");
    expect(sent.content).toContain("ask one focused follow-up in a later turn");
    expect(sent.content).toContain("do not claim the timer is operationally calibrated");
    expect(sent.content).toContain("Current Situation at or below 1200 characters");
    expect(sent.display_text).toBe("Calibrate check-in timer: long running subnet analysis");
    expect(screen.getByTestId("pending-turn")).toHaveTextContent("Calibrate check-in timer: long running subnet analysis");
    expect(screen.getByTestId("pending-turn")).not.toHaveTextContent("do not claim the timer is operationally calibrated");
    expect(await screen.findByTestId("session-automation-panel")).toHaveTextContent("1 timer active");
    expect(await screen.findByTestId("session-schedule-panel")).toHaveTextContent("1 active");
    expect(screen.getByTestId("session-schedule-list")).toHaveTextContent("Check in 1h: long running subnet analysis");
    expect(screen.getByTestId("session-schedule-list")).not.toHaveTextContent("ask the user one concise question");
    expect(screen.getByTestId("session-list")).toHaveTextContent("timers");

    await user.click(within(screen.getByTestId("session-schedule-list")).getByRole("button", { name: "Pause" }));
    await waitFor(() => expect(fetchImpl).toHaveBeenCalledWith("/v1/sessions/timer-control/schedules/sched_1", expect.objectContaining({ method: "PATCH" })));
    const pauseCall = fetchImpl.mock.calls.find(([, init]) => (init as RequestInit | undefined)?.method === "PATCH");
    expect((pauseCall?.[1] as RequestInit).body).toBe(JSON.stringify({ enabled: false }));
    expect(await screen.findByTestId("session-schedule-panel")).toHaveTextContent("1 paused");
    expect(screen.getByTestId("session-schedule-list")).toHaveTextContent("Paused");

    await user.click(within(screen.getByTestId("session-schedule-list")).getByRole("button", { name: "Resume" }));
    await waitFor(() => expect(fetchImpl.mock.calls.filter(([, init]) => (init as RequestInit | undefined)?.method === "PATCH")).toHaveLength(2));
    const resumeCall = fetchImpl.mock.calls.filter(([, init]) => (init as RequestInit | undefined)?.method === "PATCH")[1];
    expect((resumeCall[1] as RequestInit).body).toBe(JSON.stringify({ enabled: true }));
    expect(await screen.findByTestId("session-schedule-panel")).toHaveTextContent("1 active");

    await user.click(within(screen.getByTestId("session-schedule-list")).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(fetchImpl).toHaveBeenCalledWith("/v1/sessions/timer-control/schedules/sched_1", expect.objectContaining({ method: "DELETE" })));
    await waitFor(() => expect(screen.queryByTestId("session-schedule-panel")).toBeNull());
  });

  it("schedules a recurring loop tick from the unified automation menu when loop is running", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({
          sessions: [
            {
              id: "loop-timer",
              active: true,
              durable: true,
              topic_user_message: "long running runtime improvement",
              has_conversation: false,
              has_events: false,
              has_artifacts: false,
              has_memory: false,
              has_runtime_skills: false,
              has_loop_protocol: true,
              loop_protocol: {
                path: ".affent/loops/loop-timer/LOOP.md",
                status: "running",
                bytes: 512,
                preview: "Improve Affent long-run reliability.",
                state: {
                  version: 1,
                  loop_id: "loop-timer",
                  status: "running",
                  initial_goal_preview: "long running runtime improvement",
                },
              },
              has_schedules: false,
            },
          ],
          has_more: false,
        });
      }
      if (url === "/v1/sessions/loop-timer/history?after=-1&limit=500") {
        return jsonResponse({ session_id: "loop-timer", events: [], next_after: -1, has_more: false });
      }
      if (url === "/v1/sessions/loop-timer/events") return eventStreamResponse("");
      if (url === "/v1/sessions/loop-timer/schedules" && init?.method === "POST") {
        return jsonResponse({
          session_id: "loop-timer",
          schedules: [
            {
              id: "sched_loop",
              kind: "loop_tick",
              prompt: "Scheduled loop tick for session: long running runtime improvement",
              display_text: "Loop every 30m: long running runtime improvement",
              enabled: true,
              next_run_at: "2026-05-27T14:00:00Z",
              repeat_interval_seconds: 1800,
              created_at: "2026-05-27T13:30:00Z",
              updated_at: "2026-05-27T13:30:00Z",
            },
          ],
          summary: {
            count: 1,
            enabled: 1,
            next_run_at: "2026-05-27T14:00:00Z",
            next_schedule_id: "sched_loop",
            next_prompt_preview: "Loop every 30m: long running runtime improvement",
          },
        });
      }
      if (url === "/v1/sessions/loop-timer/messages" && init?.method === "POST") {
        return jsonResponse({ session_id: "loop-timer", turn_id: "loop_timer_calibration" });
      }
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    await waitFor(() => expect(screen.getByTestId("session-list")).toHaveTextContent("long running runtime improvement"));
    expect(screen.getByTestId("session-automation-panel")).toHaveTextContent("Loop running");
    expect(screen.getByTestId("session-loop-panel")).toHaveTextContent("Running");
    expect(screen.queryByTestId("session-schedule-panel")).toBeNull();
    await user.click(within(screen.getByTestId("composer-automation")).getByText("Automation"));
    await user.click(within(screen.getByTestId("composer-automation")).getByRole("button", { name: "Loop every 30m" }));

    await waitFor(() => expect(fetchImpl).toHaveBeenCalledWith("/v1/sessions/loop-timer/schedules", expect.objectContaining({ method: "POST" })));
    const scheduleCall = fetchImpl.mock.calls.find(([url]) => String(url) === "/v1/sessions/loop-timer/schedules");
    const body = JSON.parse(String((scheduleCall?.[1] as RequestInit).body)) as { kind?: string; prompt: string; display_text?: string; repeat_interval_seconds?: number; enabled: boolean };
    expect(body.kind).toBe("loop_tick");
    expect(body.prompt).toContain("Scheduled loop tick for session: long running runtime improvement");
    expect(body.prompt).toContain("autonomous long-run tick");
    expect(body.prompt).toContain("loop_protocol action=read");
    expect(body.prompt).toContain("advance at most one compact high-value step");
    expect(body.prompt).toContain("Current Situation at or below 1200 characters");
    expect(body.display_text).toBe("Loop every 30m: long running runtime improvement");
    expect(body.repeat_interval_seconds).toBe(1800);
    expect(body.enabled).toBe(true);
    expect(fetchImpl.mock.calls.some(([url]) => String(url) === "/v1/sessions/loop-timer/loop-protocol")).toBe(false);
    const messageCall = fetchImpl.mock.calls.find(([url]) => String(url) === "/v1/sessions/loop-timer/messages");
    const sent = JSON.parse(String((messageCall?.[1] as RequestInit).body)) as { content: string; display_text?: string };
    expect(sent.content).toContain("Calibrate recurring loop tick");
    expect(sent.content).toContain("Read LOOP.md with loop_protocol action=read");
    expect(sent.content).toContain("Ask the user one concise question now");
    expect(sent.content).toContain("ask one focused follow-up in a later turn");
    expect(sent.content).toContain("Current Situation at or below 1200 characters");
    expect(sent.display_text).toBe("Calibrate loop timer: long running runtime improvement");
    expect(screen.getByTestId("pending-turn")).toHaveTextContent("Calibrate loop timer: long running runtime improvement");
    expect(screen.getByTestId("pending-turn")).not.toHaveTextContent("Ask the user one concise question now");
    expect(await screen.findByTestId("session-automation-panel")).toHaveTextContent("Loop running · 1 timer active");
    expect(await screen.findByTestId("session-schedule-panel")).toHaveTextContent("1 active");
    expect(screen.getByTestId("session-schedule-list")).toHaveTextContent("Loop tick");
    expect(screen.getByTestId("session-schedule-list")).toHaveTextContent("Loop every 30m: long running runtime improvement");
    expect(screen.getByTestId("session-schedule-list")).not.toHaveTextContent("autonomous long-run tick");
    expect(screen.getByTestId("session-schedule-list")).toHaveTextContent("Repeats every 30m");
    expect(screen.getByTestId("session-list")).toHaveTextContent("timers");
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

  it("loads runtime diagnostics inside Workbench without changing the top strip", async () => {
    const user = userEvent.setup();
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/v1/sessions?limit=100") {
        return jsonResponse({ sessions: [], has_more: false });
      }
      if (url === "/v1/stats") {
        return jsonResponse({
          model: "qwen-small",
          executor_mode: "local",
          enable_builtins: true,
          enable_web: true,
          enable_browser: true,
          enable_memory: true,
          active_sessions: 2,
          running_turns: 1,
          eval_mode: true,
          eval_tools: "workspace,recall",
          aggregate: {
            blocked_by_type: 0,
            blocked_by_domain: 0,
            cache_hit: 1,
            cache_miss: 1,
            network_fetch: 1,
            input_tokens: 1000,
            output_tokens: 250,
            turns: 3,
            tools: {
              tool_requests: 4,
              tool_errors: 0,
              source_access_results: 3,
              source_access_verified: 2,
              source_access_network: 1,
              session_search_calls: 1,
              session_search_results: 2,
              session_search_context_hits: 1,
              session_search_matched_terms: 3,
            },
            runtime: {
              runtime_errors: 0,
              context_compactions: 1,
              context_compactions_reactive: 1,
              context_compaction_removed_messages: 72,
            },
          },
        });
      }
      if (url === "/v1/skills") {
        return jsonResponse({ skills: [], count: 0, install_enabled: false });
      }
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    expect(await screen.findByTestId("connection-pill")).toHaveTextContent("Connected");
    expect(screen.getByTestId("connection-pill")).not.toHaveTextContent("qwen-small");

    await user.click(screen.getByLabelText("Workbench"));

    expect(screen.queryByLabelText("Settings")).toBeNull();
    expect(screen.getByTestId("workbench-panel")).toHaveTextContent("Current context first");
    const context = screen.getByTestId("workbench-context-panel");
    expect(context).toHaveAttribute("open");
    expect(context).toHaveTextContent("Fresh task");
    expect(screen.getByTestId("runtime-stats-panel")).not.toHaveAttribute("open");
    expect(screen.getByTestId("account-settings-panel")).not.toHaveAttribute("open");
    expect(screen.getByTestId("session-skills-panel")).not.toHaveAttribute("open");
    const runtime = await screen.findByTestId("runtime-stats-panel");
    expect(runtime).toHaveTextContent("qwen-small");
    expect(runtime).toHaveTextContent("2 sessions · 1 running · eval · workspace,recall · executor local");
    expect(screen.getByTestId("runtime-stats-grid")).toHaveTextContent("Evidence2/3 verified · 1 network");
    expect(screen.getByTestId("runtime-stats-grid")).toHaveTextContent("Recall2 hits · 1 context · 3 terms");
    expect(screen.getByTestId("runtime-stats-grid")).toHaveTextContent("Context1 compaction · 1 reactive · -72 msgs");
    expect(screen.getByTestId("connection-pill")).not.toHaveTextContent("qwen-small");
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

  it("refreshes visible memory state after a live memory update", async () => {
    const user = userEvent.setup();
    const live = controllableEventStream();
    let memoryCalls = 0;
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
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
      if (url === "/v1/sessions/s1/events") return live.response;
      if (url === "/v1/skills") {
        return jsonResponse({ session_id: "account", count: 0, install_enabled: true, skills: [] });
      }
      if (url === "/v1/sessions/s1/memory") {
        memoryCalls += 1;
        if (memoryCalls === 1) {
          return jsonResponse({ session_id: "s1", has_memory: false, topics: [] });
        }
        return jsonResponse({
          session_id: "s1",
          has_memory: true,
          topics: [
            {
              target: "memory",
              topic: "markets",
              entries: ["Alpha Coast market reports use marker MEM-STOCK-73."],
              entry_count: 1,
              chars_used: 52,
              chars_limit: 4400,
              percent: 1,
            },
          ],
        });
      }
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    await waitFor(() => expect(fetchImpl).toHaveBeenCalledWith("/v1/sessions/s1/events", expect.anything()));
    await user.click(screen.getByLabelText("Workbench"));
    expect(await screen.findByTestId("session-memory-panel")).toHaveTextContent("No durable memory");

    live.send([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "remember market policy" } },
      {
        id: 3,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "memory",
          args: {
            action: "add",
            target: "memory",
            topic: "markets",
            content: "Alpha Coast market reports use marker MEM-STOCK-73.",
          },
          args_truncated: false,
          args_bytes: 64,
          args_omitted_bytes: 0,
          args_cap_bytes: 8192,
        },
      },
      {
        id: 4,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "c1",
          exit_code: 0,
          result_summary: "{\"ok\":true,\"target\":\"memory\",\"topic\":\"markets\"}",
          result: "{\"ok\":true,\"target\":\"memory\",\"topic\":\"markets\"}",
          result_truncated: false,
          result_bytes: 48,
          result_omitted_bytes: 0,
          result_cap_bytes: 262144,
        },
      },
      { id: 5, type: "turn.end", data: { turn_id: "t1", reason: "completed", tool_stats: { tool_requests: 1, memory_updates: 1, memory_update_add: 1 } } },
    ]);
    live.close();

    expect(await screen.findByTestId("memory-update-strip")).toHaveTextContent("MEM-STOCK-73");
    await waitFor(() => expect(memoryCalls).toBeGreaterThanOrEqual(2));
    expect(screen.getByTestId("session-memory-panel")).toHaveTextContent("Alpha Coast market reports use marker MEM-STOCK-73.");
    const row = screen.getByRole("button", { name: /remember market policy/ });
    expect(within(row).getByTestId("session-chips")).toHaveTextContent("Memory");
  });

  it("refreshes visible plan state after a live plan update", async () => {
    const live = controllableEventStream();
    let planCalls = 0;
    const fetchImpl = vi.fn(async (input: RequestInfo | URL) => {
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
      if (url === "/v1/sessions/s1/events") return live.response;
      if (url === "/v1/sessions/s1/plan") {
        planCalls += 1;
        return jsonResponse({
          session_id: "s1",
          plan: {
            steps: [
              { text: "Collect evidence", status: "in_progress", evidence: ["docs/source.md"], note: "Verify the current behavior." },
              { text: "Write summary", status: "pending" },
            ],
          },
          summary: {
            label: "Plan",
            total_steps: 2,
            completed_steps: 0,
            active: true,
            blocked: false,
            done: false,
            current_step: "Collect evidence",
            current_step_index: 1,
            current_step_status: "in_progress",
            error: false,
          },
        });
      }
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    await waitFor(() => expect(fetchImpl).toHaveBeenCalledWith("/v1/sessions/s1/events", expect.anything()));

    live.send([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "plan the analysis" } },
      {
        id: 3,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "plan",
          args: {
            action: "set",
            steps: [
              { step: "Collect evidence", status: "in_progress" },
              { step: "Write summary", status: "pending" },
            ],
          },
          args_truncated: false,
          args_bytes: 128,
          args_omitted_bytes: 0,
          args_cap_bytes: 8192,
        },
      },
      {
        id: 4,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "c1",
          exit_code: 0,
          result_summary: "{\"ok\":true,\"active\":true}",
          result: "{\"ok\":true,\"active\":true}",
          result_truncated: false,
          result_bytes: 25,
          result_omitted_bytes: 0,
          result_cap_bytes: 262144,
        },
      },
      { id: 5, type: "turn.end", data: { turn_id: "t1", reason: "completed", tool_stats: { tool_requests: 1, plan_calls: 1, plan_by_action: { set: 1 } } } },
    ]);
    live.close();

    await waitFor(() => expect(planCalls).toBe(1));
    expect(await screen.findByTestId("chat-context-details")).toHaveTextContent("Plan 0/2 · step 1 active");
    const planPanel = await screen.findByTestId("session-plan-panel");
    expect(planPanel).toHaveTextContent("0/2 complete");
    expect(planPanel).toHaveTextContent("Collect evidence");
    expect(planPanel).toHaveTextContent("Write summary");
    expect(planPanel).toHaveTextContent("docs/source.md");
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

    await openMessageOptions(user, screen.getByTestId("guidance-receipt"));
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

    await screen.findByText("There are two files.");
    await openMessageOptions(user, screen.getByTestId("msg-assistant"));
    await user.click(screen.getByRole("button", { name: "Ask follow-up" }));

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

    await screen.findByText("There are two files.");
    await openMessageOptions(user, screen.getByTestId("msg-assistant"));
    await user.click(screen.getByRole("button", { name: "Retry from here" }));

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

function controllableEventStream() {
  const encoder = new TextEncoder();
  let controller!: ReadableStreamDefaultController<Uint8Array>;
  const stream = new ReadableStream<Uint8Array>({
    start(c) {
      controller = c;
    },
  });
  return {
    response: new Response(stream, {
      status: 200,
      headers: { "Content-Type": "text/event-stream" },
    }),
    send(events: { id: number; type: string; data: unknown }[]) {
      controller.enqueue(encoder.encode(encodeEvents(events)));
    },
    close() {
      controller.close();
    },
  };
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
