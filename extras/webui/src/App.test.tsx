import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "./App";
import { completedTurn } from "./fixtures/completedTurn";
import { cancelledTurn, maxTurns, resultTruncated, runningSubagent, toolError } from "./fixtures/scenarios";

describe("App", () => {
  afterEach(() => {
    vi.restoreAllMocks();
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
    expect(chatContextMetric(details, "Actions 1")).toBeVisible();
    expect(chatContextMetric(details, "Tokens 138")).toBeVisible();
    const contextText = context.textContent?.replace(/\s+/g, " ").trim();
    expect(contextText).toContain("Result ready · README.md main.go");
    expect(contextText).not.toContain("Task: list the files");
    expect(context).toHaveAccessibleName("Result ready · README.md main.go");
    expect(screen.queryByText("Metrics")).toBeNull();
    expect(context.querySelector(".chat-context-primary")).toHaveTextContent("README.md main.go");
    expect(context.querySelector(".chat-context-topic")).toBeNull();
    const runtime = await screen.findByTestId("runtime-capabilities");
    expect(runtime).toHaveTextContent("Capability snapshot unknown");
    expect(runtime).toHaveTextContent("This saved chat has not loaded a capability snapshot yet.");
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
    expect(screen.getByTestId("session-list")).toHaveTextContent("saved-se...123456");
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
                skill_install: false,
                plan: false,
                memory: true,
                session_search: false,
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
        return jsonResponse({ session_id: "research-1", events: [], next_after: -1, has_more: false, trace_schema_detected: false });
      }
      if (url === "/v1/sessions/research-1/events") return eventStreamResponse("");
      return jsonResponse({ error: { message: `unexpected ${url}` } }, 404);
    });
    vi.stubGlobal("fetch", fetchImpl);

    render(<App />);

    const runtime = await screen.findByTestId("runtime-capabilities");
    expect(runtime).toHaveTextContent("This chat");
    expect(runtime).toHaveTextContent("Chat-only mode");
    expect(runtime).toHaveTextContent("Files, commands, and live sources are unavailable here.");
    expect(runtime).toHaveTextContent("Research");
    expect(runtime).toHaveTextContent("No live sources");
    expect(runtime).toHaveTextContent("Current outside information may be incomplete.");
    expect(runtime).toHaveTextContent("Files");
    expect(runtime).toHaveTextContent("Unavailable");
    expect(runtime).toHaveTextContent("Subtasks");
    expect(runtime).toHaveTextContent("Nested work");
    expect(runtime).toHaveTextContent("Can delegate focused work (2 levels, 4 focused task types).");
    expect(runtime).toHaveTextContent("Context");
    expect(runtime).toHaveTextContent("Saved memory");
    expect(runtime).toHaveTextContent("Can use saved memory.");
    const input = screen.getByPlaceholderText("Message Affent...");
    expect(input).toBeVisible();
    await userEvent.type(input, "Analyze Affine recent market trends and Twitter reaction");
    expect(screen.getByTestId("composer-task-hint")).toHaveTextContent("Needs current sources");
    expect(screen.getByTestId("composer-task-hint")).toHaveTextContent("unless you provide sources");
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
    await user.click(screen.getByRole("button", { name: "Start" }));

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
    expect(screen.queryByTestId("turn-nav-current")).toBeNull();
    const nav = screen.getByTestId("turn-navigator");
    expect(within(nav).getByTestId("turn-nav-glance")).toHaveTextContent("explain main.go");
    expect(within(nav).getByTestId("turn-nav-glance")).toHaveTextContent("Current · Sending");
    expect(within(nav).getByRole("link", { name: /Message 2: explain main.go.*Waiting.*current/ })).toHaveAttribute(
      "href",
      "#pending-turn",
    );
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

  it("focuses the composer after creating a new chat", async () => {
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
            active: false,
            durable: true,
            has_conversation: false,
            has_events: false,
            has_artifacts: false,
            has_memory: false,
            has_runtime_skills: false,
          },
        });
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
    await user.click(screen.getByRole("button", { name: "New chat" }));

    await waitFor(() => expect(input).toHaveFocus());
    expect(screen.getByTestId("workspace-shell")).toHaveAttribute("data-session-nav", "visible");
    expect(screen.getByTestId("session-list")).toHaveTextContent("New chat");
    expect(screen.getByTestId("session-list")).toHaveTextContent("No messages yet");
    expect(screen.getByTestId("session-list")).not.toHaveTextContent("new-1");
  });

  it("clears the current surface immediately when switching sessions", async () => {
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

    expect(screen.queryByText("There are two files.")).toBeNull();
    expect(screen.getByTestId("timeline-empty")).toHaveTextContent("What should we work on?");
    expect(screen.queryByTestId("chat-context-bar")).toBeNull();
    expect(screen.queryByTestId("session-strip")).toBeNull();

    s2History.resolve(jsonResponse({ session_id: "s2", events: [], next_after: -1, has_more: false, trace_schema_detected: false }));
    await waitFor(() => expect(fetchImpl).toHaveBeenCalledWith("/v1/sessions/s2/history?after=-1&limit=500", expect.anything()));
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

    await user.click(await screen.findByRole("button", { name: /Action details/ }));
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

  it("moves a previous user message into the composer draft", async () => {
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

    await user.click(await screen.findByRole("button", { name: "Edit prompt" }));

    expect(screen.getByPlaceholderText("Message Affent...")).toHaveValue("list the files");
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Editing previous message");
    expect(screen.getByPlaceholderText("Message Affent...")).toHaveFocus();
  });
});

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function chatContextMetric(root: HTMLElement, text: string): HTMLElement {
  return within(root).getByText((_, element) => element?.textContent === text);
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
