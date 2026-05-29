import { expect, test, type Page } from "@playwright/test";
import type { RawEvent } from "../src/api/events";
import { completedSubagentTree, runningSubagent, toolError } from "../src/fixtures/scenarios";

const artifactTurn = [
  { id: 20, type: "turn.start", data: { turn_id: "t2" } },
  { id: 21, type: "user.message", data: { turn_id: "t2", text: "show a large artifact" } },
  {
    id: 22,
    type: "tool.request",
    data: {
      turn_id: "t2",
      call_id: "artifact-call",
      tool: "shell",
      args: { command: "cat big.log" },
      args_truncated: false,
      args_bytes: 24,
      args_omitted_bytes: 0,
      args_cap_bytes: 8192,
    },
  },
  {
    id: 23,
    type: "tool.result",
    data: {
      call_id: "artifact-call",
      exit_code: 0,
      duration_ms: 88,
      result_summary: "large result preview",
      result: "large result preview",
      result_truncated: true,
      result_bytes: 120,
      result_omitted_bytes: 96,
      result_cap_bytes: 24,
      result_artifact_path: ".affent/artifacts/tool-results/000001-artifact-call.txt",
    },
  },
  { id: 25, type: "turn.end", data: { turn_id: "t2", reason: "completed", tool_stats: { tool_requests: 1, tool_duration_ms: 88 } } },
];

const streamingAnswer = [
  { id: 0, type: "trace.meta", data: { schema_version: 1 } },
  { id: 1, type: "turn.start", data: { turn_id: "t1" } },
  { id: 2, type: "user.message", data: { turn_id: "t1", text: "summarize this project" } },
  {
    id: 3,
    type: "message.delta",
    data: { turn_id: "t1", delta: "I am reading the project structure and checking the important files." },
  },
  {
    id: 4,
    type: "message.delta",
    data: { turn_id: "t1", delta: " The summary will stay in this chat." },
  },
];

const liveGapHistory: RawEvent[] = [
  { id: 0, type: "trace.meta", data: { schema_version: 1 } },
  { id: 1, type: "turn.start", data: { turn_id: "t1" } },
  { id: 2, type: "user.message", data: { turn_id: "t1", text: "affine 是 bittensor 的一个子网，请收集信息" } },
  {
    id: 3,
    type: "tool.request",
    data: {
      turn_id: "t1",
      call_id: "fetch-affine",
      tool: "web_fetch",
      args: { url: "https://www.coingecko.com/en/coins/affine" },
      args_truncated: false,
      args_bytes: 52,
      args_omitted_bytes: 0,
      args_cap_bytes: 8192,
    },
  },
];

const liveGapReplay: RawEvent[] = [
  {
    id: 4,
    type: "tool.result",
    data: {
      call_id: "fetch-affine",
      exit_code: 0,
      duration_ms: 1240,
      result_summary: "Affine price page fetched from CoinGecko.",
      result: "Affine price page fetched from CoinGecko.",
      result_truncated: false,
      result_bytes: 43,
      result_omitted_bytes: 0,
      result_cap_bytes: 8192,
    },
  },
  { id: 5, type: "message.done", data: { turn_id: "t1", text: "Affine data fetched.", finish_reason: "stop" } },
  { id: 6, type: "turn.end", data: { turn_id: "t1", reason: "completed", tool_stats: { tool_requests: 1, tool_duration_ms: 1240 } } },
];

const failedWebFetch: RawEvent[] = [
  { id: 0, type: "trace.meta", data: { schema_version: 1 } },
  { id: 1, type: "turn.start", data: { turn_id: "t1" } },
  { id: 2, type: "user.message", data: { turn_id: "t1", text: "收集 affine 的相关信息" } },
  {
    id: 3,
    type: "tool.request",
    data: {
      turn_id: "t1",
      call_id: "fetch-affine-failed",
      tool: "web_fetch",
      args: { url: "https://www.coingecko.com/en/coins/affine" },
      args_truncated: false,
      args_bytes: 52,
      args_omitted_bytes: 0,
      args_cap_bytes: 8192,
    },
  },
  {
    id: 4,
    type: "tool.result",
    data: {
      call_id: "fetch-affine-failed",
      exit_code: 1,
      duration_ms: 860,
      result_summary: "GET https://www.coingecko.com/en/coins/affine returned 403",
      result: "GET https://www.coingecko.com/en/coins/affine returned 403 Forbidden. Retry with another source.",
      result_truncated: false,
      result_bytes: 94,
      result_omitted_bytes: 0,
      result_cap_bytes: 8192,
    },
  },
  { id: 5, type: "message.done", data: { turn_id: "t1", text: "CoinGecko fetch failed; try another source.", finish_reason: "stop" } },
  { id: 6, type: "turn.end", data: { turn_id: "t1", reason: "completed", tool_stats: { tool_requests: 1, tool_errors: 1, tool_duration_ms: 860 } } },
];

function encodeSSE(events: readonly RawEvent[]): string {
  return events.map((event) => `id: ${event.id}\nevent: ${event.type}\ndata: ${JSON.stringify(event.data)}\n\n`).join("");
}

async function openFindInChat(page: Page) {
  const toolbar = page.getByTestId("timeline-toolbar");
  if ((await toolbar.count()) === 0) {
    await page.getByTestId("turn-navigator").getByRole("button", { name: "Find in chat" }).click();
  }
  await expect(toolbar).toBeVisible();
  if ((await toolbar.getAttribute("open")) === null) {
    await toolbar.locator("summary").click();
  }
}

async function installClipboardSpy(page: Page) {
  await page.addInitScript(() => {
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: {
        writeText: async (text: string) => {
          (window as unknown as { __copiedText?: string }).__copiedText = text;
        },
      },
    });
  });
}

async function copiedText(page: Page): Promise<string> {
  return page.evaluate(() => (window as unknown as { __copiedText?: string }).__copiedText ?? "");
}

test("empty chat opens as a low-noise conversation starter", async ({ page }) => {
  await page.route("**/v1/sessions?limit=100", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ sessions: [], has_more: false }),
    });
  });
  await page.route("**/v1/sessions", async (route) => {
    if (route.request().method() !== "POST") return route.fallback();
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        session: {
          id: "new-ui",
          active: false,
          durable: true,
          has_conversation: false,
          has_events: false,
          has_artifacts: false,
          has_memory: false,
          has_runtime_skills: false,
        },
      }),
    });
  });
  await page.route("**/v1/sessions/new-ui/history?after=-1&limit=500", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        session_id: "new-ui",
        events: [],
        next_after: -1,
        has_more: false,
        trace_schema_detected: false,
      }),
    });
  });
  await page.route("**/v1/sessions/new-ui/events", async (route) => {
    await route.fulfill({
      contentType: "text/event-stream",
      body: "",
    });
  });
  let releaseMessageResponse: (() => void) | undefined;
  const messageResponseGate = new Promise<void>((resolve) => {
    releaseMessageResponse = resolve;
  });
  await page.route("**/v1/sessions/new-ui/messages", async (route) => {
    await messageResponseGate;
    await route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({ error: { message: "provider unavailable" } }),
    });
  });

  await page.goto("/");

  await expect(page.getByTestId("timeline-empty")).toContainText("What should we work on?");
  await expect(page.getByTestId("timeline-empty")).toContainText("start from a draft");
  await expect(page.getByRole("button", { name: /Inspect project/ })).toBeVisible();
  await expect(page.getByTestId("starter-preview")).toContainText("Inspect this project and summarize");
  await page.getByRole("button", { name: /Investigate issue/ }).hover();
  await expect(page.getByTestId("starter-preview")).toContainText("Investigate the current issue");
  await expect(page.getByTestId("timeline-empty")).not.toContainText("Message");
  await expect(page.getByTestId("session-list")).toHaveCount(0);
  await expect(page.getByTestId("workspace-shell")).toHaveAttribute("data-session-nav", "hidden");
  await expect(page.getByTestId("chat-context-bar")).toHaveCount(0);
  await expect(page.getByTestId("session-tools")).toHaveCount(0);
  await expect(page.getByTestId("session-strip")).toHaveCount(0);

  const viewport = page.viewportSize();
  const introBox = await page.getByTestId("timeline-empty").boundingBox();
  const composerBox = await page.getByTestId("composer").boundingBox();
  expect(introBox?.y ?? 0).toBeGreaterThan((viewport?.height ?? 0) * 0.14);
  expect(introBox?.y ?? 0).toBeLessThan((viewport?.height ?? 0) * 0.76);
  expect((introBox?.y ?? 0) + (introBox?.height ?? 0)).toBeLessThanOrEqual((composerBox?.y ?? 0) + 1);
  await page.getByRole("button", { name: /Inspect project/ }).click();
  await expect(page.getByPlaceholder("Message Affent...")).toHaveValue("");
  await page.getByRole("button", { name: "Use draft" }).click();
  await expect(page.getByPlaceholder("Message Affent...")).toHaveValue("Inspect this project and summarize the important files, risks, and next steps.");
  await expect(page.getByTestId("composer-context")).toContainText("Starting from draft");
  await expect(page.getByRole("button", { name: "Start" })).toBeEnabled();
  await page.getByRole("button", { name: "Remove" }).click();
  await expect(page.getByPlaceholder("Message Affent...")).toHaveValue("");
  await page.getByRole("button", { name: "New chat" }).click();
  await expect(page.getByPlaceholder("Message Affent...")).toBeFocused();
  await expect(page.getByTestId("workspace-shell")).toHaveAttribute("data-session-nav", "visible");
  await expect(page.getByTestId("session-list")).toContainText("New chat");
  await expect(page.getByTestId("session-list")).toContainText("No messages yet");
  await expect(page.getByTestId("session-list")).not.toContainText("new-ui");
  await expect(page.getByTestId("session-tools")).toHaveCount(0);
  await page.getByPlaceholder("Message Affent...").fill("send should survive failure");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.getByTestId("timeline")).toHaveAttribute("data-pending-first", "true");
  await expect(page.getByTestId("pending-turn")).toContainText("send should survive failure");
  await expect(page.getByTestId("pending-turn")).toContainText("Preparing the first update.");
  await expect(page.getByTestId("pending-turn")).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth)).toBe(false);
  releaseMessageResponse?.();
  await expect(page.getByTestId("connection-pill")).toContainText("Send failed");
  await expect(page.getByPlaceholder("Message Affent...")).toHaveValue("send should survive failure");
});

test("saved chats do not push the mobile composer out of reach", async ({ page }, testInfo) => {
  await page.route("**/v1/sessions?limit=100", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        sessions: [
          {
            id: "saved-affine",
            active: false,
            durable: true,
            topic_user_message: "Affine research notes",
            has_conversation: true,
            has_events: true,
            has_artifacts: false,
            has_memory: false,
            has_runtime_skills: false,
            usage: { input_tokens: 1200, output_tokens: 450, turns: 3 },
            tools: { tool_requests: 5, tool_errors: 1, tool_repair_succeeded: 2, tool_repair_failed: 0 },
            last_used_at: "2026-05-25T10:38:00Z",
          },
          {
            id: "saved-project",
            active: false,
            durable: true,
            topic_user_message: "Project review",
            has_conversation: true,
            has_events: true,
            has_artifacts: false,
            has_memory: false,
            has_runtime_skills: false,
            last_used_at: "2026-05-25T09:12:00Z",
          },
        ],
        has_more: false,
      }),
    });
  });

  await page.goto("/");

  await expect(page.getByTestId("timeline-empty")).toContainText("What should we work on?");
  await expect(page.getByPlaceholder("Message Affent...")).toBeVisible();
  await expect(page.getByTestId("composer").getByRole("button", { name: "Start" })).toBeVisible();
  await page.getByTestId("intro-latest-chat").getByRole("button", { name: "Use title as draft" }).click();
  await expect(page.getByPlaceholder("Message Affent...")).toHaveValue("Affine research notes");
  await expect(page.getByTestId("composer-context")).toContainText("Starting from recent chat");
  await page.getByRole("button", { name: "Remove" }).click();
  await expect(page.getByPlaceholder("Message Affent...")).toHaveValue("");
  await expect(page.getByTestId("workspace-shell")).toHaveAttribute("data-session-nav", "visible");
  if (testInfo.project.name === "mobile") {
    await expect(page.getByRole("complementary", { name: "Chats" })).toHaveAttribute("data-mobile-open", "false");
    await expect(page.getByTestId("session-list")).not.toBeInViewport();
    await expect(page.getByRole("button", { name: "Open chats" })).toContainText("2");
    const composerBox = await page.getByTestId("composer").boundingBox();
    const viewport = page.viewportSize();
    expect((composerBox?.y ?? Number.POSITIVE_INFINITY) + (composerBox?.height ?? 0)).toBeLessThanOrEqual((viewport?.height ?? 0) + 1);

    await page.getByRole("button", { name: "Open chats" }).click();
    await expect(page.getByTestId("session-list")).toBeVisible();
    await expect(page.getByRole("button", { name: "Close chats" }).first()).toContainText("2");
    await expect(page.getByTestId("session-list")).toContainText("Affine research notes");
    await expect(page.getByTestId("session-list")).toContainText("1 issue");
  } else {
    await expect(page.getByTestId("session-list")).toBeVisible();
    await expect(page.getByTestId("session-list")).toContainText("Affine research notes");
    await expect(page.getByTestId("session-list")).toContainText("3 messages · 5 actions · 1 issue");
  }
});

test("active session surfaces the current tool catalog", async ({ page }) => {
  await page.route("**/v1/sessions?limit=100", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        sessions: [
          {
            id: "research-1",
            active: true,
            durable: true,
            capabilities: {
              eval_mode: false,
              builtins: true,
              skill_install: false,
              plan: false,
              memory: true,
              session_search: true,
              symbol_context: false,
              repo_search: true,
              browser: true,
              browser_screenshot: false,
              web: true,
              web_search: true,
              subagent: true,
              subagent_max_depth: 2,
              focused_tasks: true,
              focused_task_profiles: ["recall", "explore"],
            },
            topic_user_message: "Affine research notes",
            has_conversation: true,
            has_events: true,
            has_artifacts: false,
            has_memory: false,
            has_runtime_skills: false,
            usage: { input_tokens: 1200, output_tokens: 450, turns: 3 },
            tools: { tool_requests: 5, tool_errors: 1, tool_repair_succeeded: 2, tool_repair_failed: 0 },
            last_used_at: "2026-05-25T10:38:00Z",
          },
        ],
        has_more: false,
      }),
    });
  });
  await page.route("**/v1/sessions/research-1/history?after=-1&limit=500", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        session_id: "research-1",
        events: [],
        next_after: -1,
        has_more: false,
        trace_schema_detected: false,
      }),
    });
  });
  await page.route("**/v1/sessions/research-1/tools", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        session_id: "research-1",
        count: 3,
        tools: [
          { name: "read_file", group: "Workspace", description: "Read a file from the workspace.", parameters: { type: "object", properties: { path: { type: "string" } } } },
          { name: "web_fetch", group: "Research", description: "Fetch a public URL.", parameters: { type: "object", properties: { url: { type: "string" } } } },
          { name: "taostats_query", group: "MCP", source: "taostats", raw_name: "query", description: "Query MCP data for TAO stats.", parameters: { type: "object", properties: { query: { type: "string" } } } },
        ],
        surface: {
          headline: "Visible tool surface",
          detail: "These tools are the subset currently exposed to this session.",
          tone: "ready",
          status: "allowed",
          disabled_reasons: [],
          warnings: [],
        },
      }),
    });
  });
  await page.route("**/v1/sessions/research-1/events", async (route) => {
    await route.fulfill({
      contentType: "text/event-stream",
      body: "",
    });
  });

  await installClipboardSpy(page);
  await page.goto("/");

  const panel = page.getByTestId("session-tools-panel");
  await expect(panel).toContainText("3 tools available");
  await expect(panel).toContainText("Workspace 1 · Research 1 · MCP · taostats 1");
  await expect(panel).toContainText("Allow / filter status");
  await expect(panel).toContainText("Visible tool surface");
  await expect(panel).toContainText("These tools are the subset currently exposed to this session.");
  await expect(panel).toContainText("Allowed");
  await panel.getByText("3 tools available").click();
  await expect(panel.getByRole("tab", { name: "All 3" })).toBeVisible();
  await expect(panel.getByRole("tab", { name: "Workspace 1" })).toBeVisible();
  await expect(panel.getByRole("tab", { name: "Research 1" })).toBeVisible();
  await expect(panel.getByRole("tab", { name: "MCP · taostats 1" })).toBeVisible();
  await panel.getByRole("tab", { name: "Research 1" }).click();
  await expect(panel).toContainText("Research · 1 visible group");
  await expect(panel.getByTestId("session-tools-list")).toContainText("web_fetch");
  await expect(panel.getByTestId("session-tools-list")).not.toContainText("read_file");
  await expect(panel.getByTestId("session-tools-list")).not.toContainText("taostats_query");
  await panel.getByText("web_fetch").click();
  await expect(page.getByTestId("session-tools-list")).toContainText("Schema 1 field");
  await expect(page.getByTestId("session-tools-list")).toContainText("Description 4 words");
  await expect(panel.getByRole("button", { name: "Copy diagnostic" })).toBeVisible();
  await expect(panel.getByRole("button", { name: "Copy filtered catalog" })).toBeVisible();
  await expect(panel.getByRole("button", { name: "Copy names" })).toBeVisible();
  await panel.getByRole("button", { name: "Copy diagnostic" }).click();
  await expect(copiedText(page)).resolves.toContain("Tool diagnostic");
  await expect(copiedText(page)).resolves.toContain("Visible tool surface");
  await expect(copiedText(page)).resolves.toContain("Filter: Research");
  await panel.getByRole("button", { name: "Copy filtered catalog" }).click();
  await expect(copiedText(page)).resolves.toContain("Tool catalog");
  await expect(copiedText(page)).resolves.toContain("Research (1 tool)");
  await expect(copiedText(page)).resolves.toContain("web_fetch — Fetch a public URL.");
  await panel.getByRole("button", { name: "Copy names" }).click();
  await expect(copiedText(page)).resolves.toContain("Tool names");
  await expect(copiedText(page)).resolves.toContain("- web_fetch");
  await expect(copiedText(page)).resolves.not.toContain("read_file");
  await expect(copiedText(page)).resolves.not.toContain("taostats_query");
  await page.getByTestId("session-tools-search").fill("missing");
  await expect(page.getByRole("button", { name: "Clear" })).toBeVisible();
  await expect(page.getByRole("tab", { name: "All 0" })).toBeVisible();
  await expect(page.getByTestId("session-tools-list")).toContainText("No matching tools.");
  await expect(panel).toContainText("Research · no tools match this filter");
  await expect(panel).toContainText("Research · no matching tools");
  await page.getByRole("button", { name: "Clear" }).click();
  await expect(page.getByRole("button", { name: "Clear" })).toHaveCount(0);
  await expect(panel).toContainText("Research · 1 visible group");
  await panel.getByRole("tab", { name: "All 1" }).click();
  await expect(panel.getByRole("tab", { name: "All 3", selected: true })).toBeVisible();
  await expect(page.getByTestId("session-tools-search")).toBeVisible();
  await expect(panel).toContainText("Workspace");
  await expect(panel).toContainText("Research");
  await expect(panel).toContainText("MCP · taostats");
  await panel.getByRole("button", { name: /Workspace 1 tool/i }).click();
  await expect(page.getByTestId("session-tools-list")).not.toContainText("read_file");
  await panel.getByRole("button", { name: "Collapse all" }).click();
  await expect(page.getByTestId("session-tools-list")).not.toContainText("web_fetch");
  await panel.getByRole("button", { name: "Expand all" }).click();
  await expect(page.getByTestId("session-tools-list")).toContainText("read_file");
  await expect(panel).toContainText("Raw name: query");
  await page.getByTestId("session-tools-search").fill("taostats");
  await expect(page.getByTestId("session-tools-list")).toContainText("taostats_query");
  await expect(page.getByTestId("session-tools-list")).toContainText("Search matches");
});

test("opening a saved chat shows a loading state, not the empty-chat fallback, until history arrives", async ({ page }, testInfo) => {
  await page.route("**/v1/sessions?limit=100", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        sessions: [
          {
            id: "slow-history",
            active: false,
            durable: true,
            topic_user_message: "affine research",
            has_conversation: true,
            has_events: true,
            usage: { input_tokens: 0, output_tokens: 0, turns: 0 },
            tools: { tool_requests: 0, tool_errors: 0, tool_repair_succeeded: 0, tool_repair_failed: 0 },
            last_used_at: "2026-05-25T10:38:00Z",
          },
        ],
        has_more: false,
      }),
    });
  });

  // Stall the history request so the "loading chat" window is observable.
  // We resolve it explicitly later to confirm the loaded-but-empty fallback
  // takes over only after the fetch completes.
  let releaseHistory: (() => void) | undefined;
  const historyHeld = new Promise<void>((resolve) => {
    releaseHistory = resolve;
  });
  await page.route("**/v1/sessions/slow-history/history?after=-1&limit=500", async (route) => {
    await historyHeld;
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        session_id: "slow-history",
        events: [],
        next_after: -1,
        has_more: false,
        trace_schema_detected: false,
      }),
    });
  });
  await page.route("**/v1/sessions/slow-history/events", async (route) => {
    await route.fulfill({ contentType: "text/event-stream", body: "" });
  });

  await page.goto("/?sessionId=slow-history");

  // While history is in flight, the loading row replaces the empty-chat
  // fallback so we never paint a misleading "No messages loaded" page.
  const loading = page.getByTestId("timeline-loading-session");
  await expect(loading).toBeVisible();
  await expect(loading).toHaveAttribute("aria-busy", "true");
  await expect(loading).toContainText("Loading chat");
  await expect(loading).toContainText(/affine research/i);
  await expect(page.getByTestId("timeline-empty-session")).toHaveCount(0);
  await expect(page.getByText("No messages loaded")).toHaveCount(0);
  await page.screenshot({
    path: testInfo.outputPath(`timeline-loading-${testInfo.project.name}.png`),
    fullPage: true,
  });

  releaseHistory?.();

  // After the empty-history fetch resolves, the genuine loaded-but-empty
  // page takes over — proving the loading branch is scoped to the in-flight
  // window only.
  await expect(page.getByTestId("timeline-empty-session")).toContainText("No messages loaded");
  await expect(page.getByTestId("timeline-loading-session")).toHaveCount(0);
});

test("offline preview keeps creation controls read-only while the API is unreachable", async ({ page }) => {
  await page.route("**/v1/sessions?limit=100", async (route) => {
    await route.abort("failed");
  });

  await page.goto("/");

  await expect(page.getByTestId("demo-session-row")).toHaveCount(0);
  await expect(page.getByTestId("session-list")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "New chat" })).toHaveCount(0);
  await expect(page.getByTestId("composer")).toHaveAttribute("data-readonly", "true");
  await expect(page.getByTestId("composer")).toContainText("Preview replay");
  await expect(page.getByTestId("composer")).toContainText("Connect affentserve to send messages.");
  await expect(page.getByTestId("composer").getByRole("textbox")).toHaveCount(0);
  await expect(page.getByTestId("composer").getByRole("button")).toHaveCount(0);
  await expect(page.getByTestId("workspace-shell")).toHaveAttribute("data-compact-nav", "true");
  await expect(page.getByTestId("workspace-shell")).toHaveAttribute("data-session-nav", "hidden");
  await expect(page.getByTestId("chat-context-bar")).toHaveCount(0);
  const viewport = page.viewportSize();
  const timelineBox = await page.locator(".timeline-surface").boundingBox();
  await expect(page.locator(".session-panel")).toHaveCount(0);
  if ((viewport?.width ?? 0) >= 1180) {
    const timelineCenter = (timelineBox?.x ?? 0) + (timelineBox?.width ?? 0) / 2;
    expect(Math.abs(timelineCenter - (viewport?.width ?? 0) / 2)).toBeLessThan(8);
  }
});

test("composer warns before current-web tasks when web access is unavailable", async ({ page }) => {
  await page.route("**/v1/sessions?limit=100", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        sessions: [
          {
            id: "local-research",
            active: true,
            durable: true,
            capabilities: {
              eval_mode: false,
              builtins: true,
              skill_install: false,
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
              focused_task_profiles: ["recall", "explore"],
            },
            has_conversation: true,
            has_events: true,
            has_artifacts: false,
            has_memory: false,
            has_runtime_skills: false,
          },
        ],
        has_more: false,
      }),
    });
  });
  await page.route("**/v1/sessions/local-research/history?after=-1&limit=500", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        session_id: "local-research",
        events: [],
        next_after: -1,
        has_more: false,
        trace_schema_detected: false,
      }),
    });
  });
  await page.route("**/v1/sessions/local-research/events", async (route) => {
    await route.fulfill({
      contentType: "text/event-stream",
      body: "",
    });
  });

  await page.goto("/");

  await expect(page.getByTestId("runtime-capabilities")).toContainText("Project work ready");
  await expect(page.getByTestId("runtime-capabilities")).toContainText("Web");
  await expect(page.getByTestId("runtime-capabilities")).toContainText("Not available");
  await expect(page.getByTestId("runtime-capabilities")).toContainText("Current outside information may be incomplete.");
  await expect(page.getByTestId("runtime-capabilities")).toContainText("Agents");
  await expect(page.getByTestId("runtime-capabilities")).toContainText("Subtasks available");
  await page.getByPlaceholder("Message Affent...").fill("Analyze Affine recent market trends and Twitter reaction");
  await expect(page.getByTestId("composer-task-hint")).toContainText("Needs current sources");
  await expect(page.getByTestId("composer-task-hint")).toContainText("unless you provide sources");
});

test("streaming answers show a live writing state", async ({ page }, testInfo) => {
  const errors: string[] = [];
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(m.text());
  });
  page.on("pageerror", (e) => errors.push(String(e)));

  await page.route("**/v1/sessions?limit=100", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        sessions: [
          {
            id: "streaming-1",
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
      }),
    });
  });
  await page.route("**/v1/sessions/streaming-1/history?after=-1&limit=500", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        session_id: "streaming-1",
        events: streamingAnswer,
        next_after: streamingAnswer.length - 1,
        has_more: false,
        trace_schema_version: 1,
        trace_schema_detected: true,
      }),
    });
  });
  await page.route("**/v1/sessions/streaming-1/events", async (route) => {
    await route.fulfill({
      contentType: "text/event-stream",
      body: "",
    });
  });

  await page.goto("/");

  await expect(page.getByTestId("msg-assistant")).toContainText("I am reading the project structure");
  await expect(page.getByTestId("msg-assistant")).toHaveAttribute("data-streaming", "true");
  await expect(page.getByTestId("msg-assistant").getByRole("status")).toContainText("Writing");
  await expect(page.getByRole("button", { name: "Ask follow-up" })).toHaveCount(0);
  await expect(page.getByTestId("timeline-toolbar")).toHaveCount(0);
  await expect(page.getByTestId("composer").getByRole("button", { name: "Send guidance" })).toBeDisabled();
  await page.screenshot({
    path: testInfo.outputPath(`streaming-answer-${testInfo.project.name}.png`),
    fullPage: true,
  });

  expect(errors, `console errors: ${errors.join(" | ")}`).toEqual([]);
});

test("live event stream replays the gap after history loading", async ({ page }) => {
  let lastEventID = "";
  await page.route("**/v1/sessions?limit=100", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        sessions: [
          {
            id: "live-gap",
            active: true,
            durable: true,
            latest_user_message: "affine 是 bittensor 的一个子网，请收集信息",
            has_conversation: true,
            has_events: true,
            has_artifacts: false,
            has_memory: false,
            has_runtime_skills: false,
          },
        ],
        has_more: false,
      }),
    });
  });
  await page.route("**/v1/sessions/live-gap/history?after=-1&limit=500", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        session_id: "live-gap",
        events: liveGapHistory,
        next_after: 3,
        has_more: false,
        trace_schema_version: 1,
        trace_schema_detected: true,
      }),
    });
  });
  await page.route("**/v1/sessions/live-gap/events", async (route) => {
    lastEventID = route.request().headers()["last-event-id"] ?? "";
    await route.fulfill({
      contentType: "text/event-stream",
      body: encodeSSE(liveGapReplay),
    });
  });

  await page.goto("/");

  await expect(page.getByTestId("msg-assistant")).toContainText("Affine data fetched.");
  await expect(page.getByTestId("msg-assistant")).toHaveAttribute("data-streaming", "false");
  await expect(page.getByRole("button", { name: "Ask follow-up" })).toBeVisible();
  await expect(page.getByTestId("pending-turn")).toHaveCount(0);
  expect(lastEventID).toBe("3");
});

test("web-fetch failure details can be copied as issues, activity, and full work records", async ({ page }) => {
  await installClipboardSpy(page);
  await page.route("**/v1/sessions?limit=100", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        sessions: [
          {
            id: "copy-failure",
            active: true,
            durable: true,
            latest_user_message: "收集 affine 的相关信息",
            has_conversation: true,
            has_events: true,
            has_artifacts: false,
            has_memory: false,
            has_runtime_skills: false,
          },
        ],
        has_more: false,
      }),
    });
  });
  await page.route("**/v1/sessions/copy-failure/history?after=-1&limit=500", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        session_id: "copy-failure",
        events: failedWebFetch,
        next_after: failedWebFetch.length - 1,
        has_more: false,
        trace_schema_version: 1,
        trace_schema_detected: true,
      }),
    });
  });
  await page.route("**/v1/sessions/copy-failure/events", async (route) => {
    await route.fulfill({ contentType: "text/event-stream", body: "" });
  });

  await page.goto("/");

  const activity = page.getByTestId("agent-activity").first();
  await expect(activity).toContainText("Answered after working around 1 issue");
  await expect(activity).not.toContainText("https://www.coingecko.com/en/coins/affine");
  await activity.getByRole("button", { name: "Copy issues" }).click();
  let copied = await copiedText(page);
  expect(copied).toContain("https://www.coingecko.com/en/coins/affine");
  expect(copied).toContain("403 Forbidden");

  await activity.getByRole("button", { name: /What Affent did/ }).click();
  await expect(activity).toContainText("coingecko.com/en/coins/affine");
  await activity.getByRole("button", { name: "Copy activity" }).click();
  copied = await copiedText(page);
  expect(copied).toContain("web_fetch");
  expect(copied).toContain("fetch-affine-failed");
  expect(copied).toContain("Retry with another source");

  await page.getByRole("button", { name: /Action details/ }).click();
  const executionTree = page.getByTestId("execution-tree");
  await executionTree.getByRole("button", { name: "Copy issues" }).click();
  copied = await copiedText(page);
  expect(copied).toContain("GET https://www.coingecko.com/en/coins/affine returned 403");

  await executionTree.getByRole("button", { name: "Copy details" }).click();
  copied = await copiedText(page);
  expect(copied).toContain("Action: Fetch coingecko.com/en/coins");
  expect(copied).toContain('"url": "https://www.coingecko.com/en/coins/affine"');
  expect(copied).toContain("Retry with another source");
});

test("mobile workbench inspector does not push the composer below the viewport", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== "mobile", "mobile layout regression coverage");

  await page.route("**/v1/sessions?limit=100", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        sessions: [
          {
            id: "mobile-workbench-failure",
            active: true,
            durable: true,
            latest_user_message: "build it",
            has_conversation: true,
            has_events: true,
            has_artifacts: false,
            has_memory: false,
            has_runtime_skills: false,
          },
        ],
        has_more: false,
      }),
    });
  });
  await page.route("**/v1/sessions/mobile-workbench-failure/history?after=-1&limit=500", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        session_id: "mobile-workbench-failure",
        events: toolError,
        next_after: toolError.length - 1,
        has_more: false,
        trace_schema_version: 1,
        trace_schema_detected: true,
      }),
    });
  });
  await page.route("**/v1/sessions/mobile-workbench-failure/events", async (route) => {
    await route.fulfill({ contentType: "text/event-stream", body: "" });
  });

  await page.goto("/");
  await page.getByRole("button", { name: "Workbench" }).click();
  await page.getByTestId("workbench-panel").getByRole("button", { name: /Run/ }).click();

  await expect(page.getByTestId("workbench-inspector")).toContainText("Run");
  await expect(page.getByTestId("composer")).toBeHidden();

  const viewport = page.viewportSize();
  const inspectorBox = await page.getByTestId("workbench-inspector").boundingBox();
  const surfaceBox = await page.locator(".timeline-surface").boundingBox();
  expect(inspectorBox?.y ?? Number.POSITIVE_INFINITY).toBeLessThan(viewport?.height ?? 0);
  expect((inspectorBox?.y ?? 0) + (inspectorBox?.height ?? 0)).toBeLessThanOrEqual((viewport?.height ?? 0) + 2);
  expect((surfaceBox?.y ?? 0) + (surfaceBox?.height ?? 0)).toBeLessThanOrEqual((viewport?.height ?? 0) + 2);

  await page.getByTestId("session-run-list").getByRole("button", { name: "Rerun as draft" }).click();
  await expect(page.getByTestId("workbench-inspector")).toHaveCount(0);
  await expect(page.getByTestId("composer")).toBeVisible();
  await expect(page.getByTestId("composer-context")).toContainText("Using command");
  await expect(page.getByPlaceholder("Message Affent...")).toHaveValue(/Run evidence for make/);
});

test("markdown tables and code actions stay usable inside the chat surface", async ({ page }) => {
  await installClipboardSpy(page);
  await page.route("**/v1/sessions?limit=100", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        sessions: [
          {
            id: "table-session",
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
      }),
    });
  });
  await page.route("**/v1/sessions/table-session/history?after=-1&limit=500", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        session_id: "table-session",
        events: [
          { id: 1, type: "turn.start", data: { turn_id: "t1" } },
          { id: 2, type: "user.message", data: { turn_id: "t1", text: "summarize market risks" } },
          {
            id: 3,
            type: "message.done",
            data: {
              turn_id: "t1",
              finish_reason: "stop",
              text: [
                "| Risk category | What to watch |",
                "| --- | --- |",
                "| Winner-take-all incentive design | Long uninterrupted leadership can make new participant marginal returns approach zero and should be monitored carefully. |",
                "| Market-data limits | CoinMarketCap and TaoStats can render dynamic shells, so quote freshness needs explicit source timestamps. |",
                "",
                "```bash",
                "npm test -- --run src/components/MarkdownText.test.tsx",
                "npm run build",
                "```",
              ].join("\n"),
            },
          },
          { id: 4, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
        ],
        next_after: 4,
        has_more: false,
        trace_schema_detected: true,
      }),
    });
  });
  await page.route("**/v1/sessions/table-session/events", async (route) => {
    await route.fulfill({ contentType: "text/event-stream", body: "" });
  });

  await page.goto("/");
  await expect(page.getByRole("table")).toBeVisible();
  await page.getByRole("button", { name: "Copy table" }).click();
  await expect(page.locator(".markdown-table-copy", { hasText: "Copied" })).toBeVisible();
  expect(await copiedText(page)).toContain("Risk category\tWhat to watch");
  expect(await copiedText(page)).toContain("Market-data limits\tCoinMarketCap and TaoStats");
  await expect(page.locator(".markdown-code-head").getByText("Shell", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Copy shell code" }).click();
  await expect(page.locator(".markdown-code-copy", { hasText: "Copied" })).toBeVisible();
  expect(await copiedText(page)).toBe("npm test -- --run src/components/MarkdownText.test.tsx\nnpm run build");

  if ((page.viewportSize()?.width ?? 0) <= 768) {
    const boxes = await page.evaluate(() => {
      const table = document.querySelector(".markdown-text table")?.getBoundingClientRect();
      const wrapper = document.querySelector(".markdown-table-scroll")?.getBoundingClientRect();
      const surface = document.querySelector(".timeline-surface")?.getBoundingClientRect();
      return {
        tableRight: table?.right ?? 0,
        wrapperRight: wrapper?.right ?? 0,
        surfaceRight: surface?.right ?? 0,
        scrollWidth: document.documentElement.scrollWidth,
        viewportWidth: document.documentElement.clientWidth,
      };
    });
    expect(boxes.tableRight).toBeLessThanOrEqual(boxes.wrapperRight + 1);
    expect(boxes.wrapperRight).toBeLessThanOrEqual(boxes.surfaceRight + 1);
    expect(boxes.scrollWidth).toBe(boxes.viewportWidth);
  }
});

test("assistant answers expose markdown and plain-text copy actions", async ({ page }) => {
  await installClipboardSpy(page);
  await page.route("**/v1/sessions?limit=100", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        sessions: [
          {
            id: "copy-answer",
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
      }),
    });
  });
  await page.route("**/v1/sessions/copy-answer/history?after=-1&limit=500", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        session_id: "copy-answer",
        events: [
          { id: 1, type: "turn.start", data: { turn_id: "t1" } },
          { id: 2, type: "user.message", data: { turn_id: "t1", text: "list files" } },
          {
            id: 3,
            type: "message.done",
            data: {
              turn_id: "t1",
              finish_reason: "stop",
              text: "Use `fd` and keep a short plan.\n\n```bash\nfd -t f\n```",
            },
          },
          { id: 4, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
        ],
        next_after: 4,
        has_more: false,
        trace_schema_detected: true,
      }),
    });
  });
  await page.route("**/v1/sessions/copy-answer/events", async (route) => {
    await route.fulfill({ contentType: "text/event-stream", body: "" });
  });

  await page.goto("/");

  await page.getByRole("button", { name: "Copy answer" }).click();
  await expect(page.getByRole("button", { name: "Copy markdown" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Copy plain text" })).toBeVisible();
  await page.getByRole("button", { name: "Copy plain text" }).click();
  expect(await copiedText(page)).toBe("Use fd and keep a short plan.\n\nfd -t f");
});

test("running work reads like an Affent chat update before drill-down", async ({ page }, testInfo) => {
  const errors: string[] = [];
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(m.text());
  });
  page.on("pageerror", (e) => errors.push(String(e)));

  await page.route("**/v1/sessions?limit=100", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        sessions: [
          {
            id: "running-1",
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
      }),
    });
  });
  await page.route("**/v1/sessions/running-1/history?after=-1&limit=500", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        session_id: "running-1",
        events: runningSubagent,
        next_after: runningSubagent.length - 1,
        has_more: false,
        trace_schema_version: 1,
        trace_schema_detected: true,
      }),
    });
  });
  await page.route("**/v1/sessions/running-1/events", async (route) => {
    await route.fulfill({
      contentType: "text/event-stream",
      body: "",
    });
  });
  const guidanceRequests: string[] = [];
  await page.route("**/v1/sessions/running-1/messages", async (route) => {
    if (route.request().method() !== "POST") return route.fallback();
    const body = JSON.parse(route.request().postData() ?? "{}") as { content?: string };
    guidanceRequests.push(body.content ?? "");
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ turn_id: "guidance-1" }),
    });
  });

  await page.goto("/");

  await expect(page.getByTestId("running-answer")).toContainText("Working on this");
  await expect(page.getByTestId("running-answer")).toContainText("Inspect docs for WebUI trace requirements");
  if (testInfo.project.name === "mobile") {
    await page.getByRole("button", { name: "Switch chats" }).click();
  }
  const runningSessionRow = page.getByTestId("session-list").getByRole("button", { name: /use a subagent to inspect docs/ });
  await expect(runningSessionRow.locator(".session-preview")).toBeVisible();
  await expect(runningSessionRow.locator(".session-preview")).toContainText("Now ·");
  await expect(runningSessionRow.locator(".session-preview")).toContainText("Inspect docs for WebUI trace requirements");
  await expect(page.getByTestId("workflow-status")).toHaveCount(0);
  await expect(page.getByTestId("turn-boundary")).toHaveCount(0);
  await expect(page.getByRole("button", { name: /Work details/ })).toHaveCount(0);
  await expect(page.getByTestId("work-thread")).toHaveCount(0);
  await expect(page.getByTestId("agent-activity")).toHaveAttribute("data-open", "true");
  await expect(page.getByTestId("agent-activity-tree")).toContainText("Inspect docs for WebUI trace requirements");
  await expect(page.getByTestId("agent-activity-tree")).toContainText("running");
  await expect(page.getByTestId("execution-tree")).toHaveCount(0);
  await expect(page.getByTestId("composer")).toBeVisible();
  const scrollBox = await page.getByTestId("conversation-scroll").boundingBox();
  const runningBox = await page.getByTestId("running-answer").boundingBox();
  expect(runningBox?.y ?? Number.POSITIVE_INFINITY).toBeLessThan((scrollBox?.y ?? 0) + 220);
  await expect(page.getByTestId("timeline-toolbar")).toHaveCount(0);
  await expect(page.getByTestId("turn-navigator")).toHaveCount(0);
  await expect(page.getByText("Filters")).not.toBeVisible();
  await expect(page.getByTestId("timeline-match-count")).toHaveCount(0);
  await expect(page.getByTestId("session-strip")).toHaveCount(0);
  await expect(page.getByTestId("composer").getByRole("button", { name: "Send guidance" })).toBeDisabled();
  await page.getByPlaceholder("Message Affent...").fill("prioritize the product flow");
  await expect(page.getByTestId("composer-intent")).toContainText("Guidance ready");
  await expect(page.getByTestId("composer-intent")).toContainText("Sends into this run, not a new chat");
  const composerSendGuidance = page.getByTestId("composer").getByRole("button", { name: "Send guidance" });
  await expect(composerSendGuidance).toBeEnabled();
  await composerSendGuidance.click();
  expect(guidanceRequests).toEqual(["prioritize the product flow"]);
  await expect(page.getByPlaceholder("Message Affent...")).toHaveValue("");
  await page.screenshot({
    path: testInfo.outputPath(`workflow-running-${testInfo.project.name}.png`),
    fullPage: true,
  });

  expect(errors, `console errors: ${errors.join(" | ")}`).toEqual([]);
});

// Real-browser gate: the workflow surface must render, inline disclosures
// must work, no console errors should fire, and each viewport gets a
// screenshot for visual review.
test("workflow timeline renders with inline drill-down", async ({ page }, testInfo) => {
  const errors: string[] = [];
  page.on("console", (m) => {
    if (m.type() === "error") errors.push(m.text());
  });
  page.on("pageerror", (e) => errors.push(String(e)));

  await page.route("**/v1/sessions?limit=100", async (route) => {
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
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
            usage: { input_tokens: 120, output_tokens: 18, turns: 1 },
          },
        ],
        has_more: false,
      }),
    });
  });
  await page.route("**/v1/sessions/s1/history?after=-1&limit=500", async (route) => {
    const events = [...completedSubagentTree, ...artifactTurn];
    await route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({
        session_id: "s1",
        events,
        next_after: events.length - 1,
        has_more: false,
        trace_schema_version: 1,
        trace_schema_detected: true,
      }),
    });
  });
  await page.route("**/v1/sessions/s1/artifacts/.affent/artifacts/tool-results/000001-artifact-call.txt?offset=0&limit=65536", async (route) => {
    await route.fulfill({
      contentType: "application/octet-stream",
      headers: {
        "X-Affent-Artifact-Path": ".affent/artifacts/tool-results/000001-artifact-call.txt",
        "X-Affent-Artifact-Bytes": "58",
        "X-Affent-Artifact-Offset": "0",
      },
      body: "artifact payload with MCP_search marker",
    });
  });
  await page.route("**/v1/sessions/s1/artifacts/.affent/artifacts/tool-results/000001-artifact-call.txt?offset=39&limit=65536", async (route) => {
    await route.fulfill({
      contentType: "application/octet-stream",
      headers: {
        "X-Affent-Artifact-Path": ".affent/artifacts/tool-results/000001-artifact-call.txt",
        "X-Affent-Artifact-Bytes": "58",
        "X-Affent-Artifact-Offset": "39",
      },
      body: " and trailing chunk",
    });
  });
  await page.route("**/v1/sessions/s1/events", async (route) => {
    await route.fulfill({
      contentType: "text/event-stream",
      body: "",
    });
  });

  await page.goto("/");
  await page.evaluate(() => {
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: async () => undefined },
    });
  });

  await expect(page.getByTestId("app-shell")).toBeVisible();
  await expect(page.getByTestId("session-list")).toContainText("show a large artifact");
  await expect(page.getByTestId("session-list")).not.toContainText("s1");
  await expect(page.getByTestId("workspace-shell")).toHaveAttribute("data-session-nav", "visible");
  await expect(page.getByTestId("chat-context-bar")).toContainText("large result preview");
  await expect(page.getByTestId("chat-context-bar")).not.toContainText("Artifact 1 file");
  await expect(page.getByTestId("timeline")).toBeVisible();
  await expect(page.getByTestId("composer")).toBeVisible();
  const viewport = page.viewportSize();
  const composerBox = await page.getByTestId("composer").boundingBox();
  const scrollBox = await page.getByTestId("conversation-scroll").boundingBox();
  const workspaceBox = await page.getByTestId("workspace-shell").boundingBox();
  expect(composerBox?.y ?? Number.POSITIVE_INFINITY).toBeLessThan(viewport?.height ?? 0);
  expect((composerBox?.y ?? 0) + (composerBox?.height ?? 0)).toBeGreaterThan(0);
  expect((scrollBox?.y ?? 0) + (scrollBox?.height ?? 0)).toBeLessThanOrEqual((composerBox?.y ?? 0) + 1);
  if ((viewport?.width ?? 0) <= 768) {
    const sessionBox = await page.locator(".session-panel").boundingBox();
    const surfaceBox = await page.locator(".timeline-surface").boundingBox();
    expect(sessionBox?.y ?? Number.POSITIVE_INFINITY).toBeLessThan((surfaceBox?.y ?? 0) + 1);
    expect(sessionBox?.y ?? Number.POSITIVE_INFINITY).toBeLessThan((composerBox?.y ?? 0) + 1);
  } else {
    const surfaceBox = await page.locator(".timeline-surface").boundingBox();
    expect(surfaceBox?.y ?? 0).toBeLessThanOrEqual((workspaceBox?.y ?? 0) + 1);
  }
  await expect(page.getByTestId("workflow-status")).toHaveCount(0);
  await expect(page.getByTestId("turn-card")).toHaveCount(2);
  await expect(page.getByTestId("conversation-map")).toHaveCount(0);
  await expect(page.getByTestId("turn-navigator")).toHaveCount(0);
  await expect(page.getByTestId("timeline-toolbar")).toHaveCount(0);
  await expect(page.getByTestId("turn-title").nth(0)).toContainText("delegate docs inspection");
  await expect(page.getByTestId("turn-title").nth(1)).toContainText("show a large artifact");
  await expect(page.getByTestId("turn-boundary")).toHaveCount(0);
  await expect(page.getByTestId("turn-head").nth(0)).toContainText("delegate docs inspection");
  await expect(page.getByTestId("turn-head").nth(0)).toContainText("Done");
  await expect(page.getByTestId("turn-head").nth(1)).toContainText("show a large artifact");
  await expect(page.getByTestId("turn-head").nth(1)).toContainText("cat big.log");
  await expect(page.getByTestId("agent-activity").first()).toContainText("WebUI must render trace details as expandable agent structure.");
  await expect(page.getByTestId("agent-activity-digest").first()).toContainText("Result");
  await expect(page.getByTestId("agent-activity-digest").first()).toContainText("2 delegated tasks");
  await expect(page.getByTestId("agent-activity-digest").first()).toContainText("4 evidence");
  await expect(page.getByTestId("agent-activity-digest-evidence").first()).toContainText("Read docs/webui-product-design.md");
  await page.getByTestId("agent-activity").first().getByRole("button", { name: /What Affent did/ }).click();
  await expect(page.getByTestId("agent-activity").first()).toContainText("Read");
  await expect(page.getByTestId("agent-activity").first()).toContainText("docs/webui-product-design.md");
  await expect(page.getByTestId("agent-activity").first()).toContainText("392 tokens");
  await expect(page.getByTestId("agent-activity").first()).toContainText("MCP");
  await expect(page.getByTestId("agent-activity").first()).toContainText("webui trace");
  await expect(page.getByTestId("msg-assistant").first()).toContainText("The delegated checks found");
  await expect(page.getByTestId("turn-artifacts")).toHaveCount(0);
  await expect(page.getByTestId("fallback-answer")).toContainText("Action output was truncated");
  await expect(page.getByTestId("fallback-answer")).toContainText("large result preview");
  await page.getByTestId("fallback-answer").getByRole("button", { name: "Ask follow-up" }).click();
  await expect(page.getByPlaceholder("Message Affent...")).toHaveValue(
    "Continue from this output: large result preview Full output is available in Workbench.",
  );
  await page.getByRole("button", { name: "Workbench" }).click();
  await page.locator(".workbench-nav").getByRole("button", { name: /Artifacts/ }).click();
  await page.getByTestId("session-artifacts-focus").getByRole("button", { name: "Open artifact" }).click();
  await expect(page.getByTestId("artifact-viewer")).toContainText("artifact payload");
  await page.getByTestId("artifact-viewer").getByRole("button", { name: "Use text" }).click();
  await expect(page.getByTestId("composer-context")).toContainText("Using file text");
  await expect(page.getByPlaceholder("Message Affent...")).toHaveValue(/File: \.affent\/artifacts\/tool-results\/000001-artifact-call\.txt/);
  await expect(page.getByPlaceholder("Message Affent...")).toHaveValue(/artifact payload/);
  await page.getByRole("button", { name: "Remove" }).click();
  await expect(page.getByPlaceholder("Message Affent...")).toHaveValue("");
  await page.getByRole("button", { name: "Close Workbench" }).click();
  await expect(page.locator(".raw-disclosure")).toHaveCount(0);

  await expect(page.getByTestId("tool-details")).toHaveCount(0);
  await expect(page.getByTestId("agent-activity").first()).toContainText("Work");
  await expect(page.getByTestId("agent-activity-tree").first().getByRole("button", { name: /Find the WebUI trace requirements/ })).toHaveAttribute("aria-expanded", "false");
  await expect(page.getByTestId("execution-tree")).toHaveCount(0);
  await page.screenshot({
    path: testInfo.outputPath(`workflow-closed-${testInfo.project.name}.png`),
    fullPage: true,
  });
  await page.getByRole("button", { name: "Workbench" }).click();
  await page.locator(".workbench-nav").getByRole("button", { name: /Artifacts/ }).click();
  await page.getByTestId("session-artifacts-focus").getByRole("button", { name: "Open artifact" }).click();
  await expect(page.getByTestId("artifact-viewer")).toContainText("artifact payload");
  await page.getByRole("button", { name: "Load more" }).click();
  await expect(page.getByTestId("artifact-viewer")).toContainText("trailing chunk");
  await page.getByTestId("artifact-search").fill("MCP_search");
  await expect(page.getByTestId("artifact-content").locator("mark", { hasText: "MCP_search" })).toBeVisible();
  await expect(page.getByTestId("artifact-viewer")).toContainText("1 match");
  await expect(page.getByTestId("artifact-match-list")).toContainText("Line 1");
  await expect(page.getByTestId("artifact-match-list")).toContainText("MCP_search");
  await page.getByTestId("artifact-match-list").getByRole("button", { name: "Copy matches" }).click();
  await expect(page.getByTestId("artifact-match-list").getByRole("button", { name: "Copied" })).toBeVisible();
  await page.getByTestId("artifact-match-list").getByRole("button", { name: "Use matches" }).click();
  await expect(page.getByTestId("composer-context")).toContainText("Using evidence");
  await expect(page.getByPlaceholder("Message Affent...")).toHaveValue(/Use this artifact evidence in the next step:\nFile: \.affent\/artifacts/);
  await expect(page.getByTestId("artifact-viewer")).toContainText("100% loaded");
  await page.getByTestId("artifact-viewer").getByRole("button", { name: "Use text" }).click();
  await expect(page.getByTestId("composer-context")).toContainText("Using file text");
  await expect(page.getByPlaceholder("Message Affent...")).toHaveValue(/Use this loaded file text in the next step:\nFile: \.affent\/artifacts/);
  await expect(page.getByTestId("session-strip")).toHaveCount(0);
  await page.screenshot({
    path: testInfo.outputPath(`workflow-expanded-${testInfo.project.name}.png`),
    fullPage: true,
  });

  expect(errors, `console errors: ${errors.join(" | ")}`).toEqual([]);
});
