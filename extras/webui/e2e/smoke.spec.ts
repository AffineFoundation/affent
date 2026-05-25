import { expect, test, type Page } from "@playwright/test";
import { completedSubagentTree, runningSubagent } from "../src/fixtures/scenarios";

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

async function openFindInChat(page: Page) {
  const toolbar = page.getByTestId("timeline-toolbar");
  if ((await toolbar.count()) === 0) {
    await page.getByTestId("turn-navigator").getByRole("button", { name: "Search" }).click();
  }
  await expect(toolbar).toBeVisible();
  if ((await toolbar.getAttribute("open")) === null) {
    await toolbar.locator("summary").click();
  }
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
  await page.route("**/v1/sessions/new-ui/messages", async (route) => {
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
  await expect(page.getByTestId("session-list")).toContainText("new-ui");
  await expect(page.getByTestId("session-tools")).toHaveCount(0);
  await page.getByPlaceholder("Message Affent...").fill("send should survive failure");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.getByTestId("connection-pill")).toContainText("Send failed");
  await expect(page.getByPlaceholder("Message Affent...")).toHaveValue("send should survive failure");
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

  await expect(page.getByTestId("runtime-capabilities")).toContainText("Local project mode");
  await expect(page.getByTestId("runtime-capabilities")).toContainText("Research");
  await expect(page.getByTestId("runtime-capabilities")).toContainText("Offline");
  await expect(page.getByTestId("runtime-capabilities")).toContainText("No live web tools for current outside information.");
  await expect(page.getByTestId("runtime-capabilities")).toContainText("Workers");
  await expect(page.getByTestId("runtime-capabilities")).toContainText("Delegation on");
  await page.getByPlaceholder("Message Affent...").fill("Analyze Affine recent market trends and Twitter reaction");
  await expect(page.getByTestId("composer-task-hint")).toContainText("Current web info unavailable");
  await expect(page.getByTestId("composer-task-hint")).toContainText("results may be incomplete");
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
  await expect(page.getByRole("button", { name: "Working" })).toBeDisabled();
  await page.screenshot({
    path: testInfo.outputPath(`streaming-answer-${testInfo.project.name}.png`),
    fullPage: true,
  });

  expect(errors, `console errors: ${errors.join(" | ")}`).toEqual([]);
});

test("markdown tables stay inside the chat surface on narrow screens", async ({ page }) => {
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
  await expect(page.getByTestId("turn-navigator").getByRole("button", { name: "Search" })).toHaveAttribute("aria-pressed", "false");
  await expect(page.getByText("Filters")).not.toBeVisible();
  await expect(page.getByTestId("timeline-match-count")).toHaveCount(0);
  await expect(page.getByTestId("session-strip")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Working" })).toBeDisabled();
  await page.getByPlaceholder("Message Affent...").fill("prioritize the product flow");
  await expect(page.getByTestId("composer-intent")).toContainText("Ready to guide this run");
  await expect(page.getByTestId("composer-intent")).toContainText("Sends to the current run, not a new chat");
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
  await expect(page.getByTestId("chat-context-bar")).toContainText("show a large artifact");
  await expect(page.getByTestId("chat-context-bar")).toContainText("large result preview");
  await expect(page.getByTestId("chat-context-bar")).toContainText("Actions 1");
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
  await expect(page.getByTestId("conversation-map")).toContainText("show a large artifact");
  await expect(page.getByTestId("conversation-map")).toContainText("Messages");
  await expect(page.getByTestId("conversation-map")).toContainText("2 messages · Result · large result preview");
  await expect(page.getByTestId("turn-nav-glance")).toContainText("delegate docs inspection");
  await expect(page.getByTestId("turn-nav-glance")).toContainText("WebUI must render trace details as expandable runtime structure.");
  await expect(page.getByTestId("conversation-map")).toContainText("Current · Result");
  await expect(page.getByTestId("turn-nav-current")).toHaveCount(0);
  await expect(page.getByTestId("turn-nav-progress")).toBeVisible();
  await expect(page.getByRole("link", { name: /Jump to message 2: show a large artifact \(current\)/ })).toHaveAttribute("href", "#turn-2");
  await expect(page.getByRole("link", { name: /Jump to message 2: show a large artifact \(current\)/ })).toHaveAttribute("data-current", "true");
  await expect(page.getByRole("link", { name: /Message 2: show a large artifact.*Result: large result preview.*current/ })).toHaveAttribute(
    "data-current",
    "true",
  );
  await expect(page.getByTestId("timeline-toolbar")).toHaveCount(0);
  await expect(page.getByTestId("turn-navigator").getByRole("button", { name: "Search" })).toHaveAttribute("aria-pressed", "false");
  await expect(page.getByTestId("turn-title").nth(0)).toContainText("delegate docs inspection");
  await expect(page.getByTestId("turn-title").nth(1)).toContainText("show a large artifact");
  await expect(page.getByTestId("turn-boundary")).toHaveCount(0);
  await expect(page.getByTestId("turn-head").nth(0)).toHaveAttribute("data-visible", "true");
  await expect(page.getByTestId("turn-head").nth(0)).toContainText("Message 1");
  await expect(page.getByTestId("turn-head").nth(0)).toContainText("delegate docs inspection");
  await expect(page.getByTestId("turn-head").nth(0)).toContainText("Done");
  await expect(page.getByTestId("turn-head").nth(0)).toContainText("2 actions");
  await expect(page.getByTestId("turn-head").nth(1)).toHaveAttribute("data-visible", "true");
  await expect(page.getByTestId("turn-head").nth(1)).toContainText("Message 2");
  await expect(page.getByTestId("turn-head").nth(1)).toContainText("show a large artifact");
  await expect(page.getByTestId("turn-head").nth(1)).toContainText("1 action");
  await expect(page.getByTestId("agent-activity").first()).toContainText("WebUI must render trace details as expandable runtime structure.");
  await expect(page.getByTestId("agent-activity-digest").first()).toContainText("Result");
  await expect(page.getByTestId("agent-activity-digest").first()).toContainText("2 delegated tasks");
  await expect(page.getByTestId("agent-activity-digest").first()).toContainText("4 evidence");
  await page.getByTestId("agent-activity-digest-evidence").first().getByRole("button", { name: "Use evidence" }).click();
  await expect(page.getByTestId("composer-context")).toContainText("Using evidence");
  await expect(page.getByPlaceholder("Message Affent...")).toHaveValue(
    [
      "Use this evidence in the next step:",
      "- Listed docs",
      "- Read docs/webui-product-design.md",
      "- MCP webui trace",
      "- Read docs/focused-tasks.md",
    ].join("\n"),
  );
  await page.getByRole("button", { name: "Remove" }).click();
  await expect(page.getByPlaceholder("Message Affent...")).toHaveValue("");
  await page.getByTestId("agent-activity").first().getByRole("button", { name: /What Affent did/ }).click();
  await expect(page.getByTestId("agent-activity").first()).toContainText("Read");
  await expect(page.getByTestId("agent-activity").first()).toContainText("docs/webui-product-design.md");
  await expect(page.getByTestId("agent-activity").first()).toContainText("392 tokens");
  await expect(page.getByTestId("agent-activity").first()).toContainText("MCP");
  await expect(page.getByTestId("agent-activity").first()).toContainText("webui trace");
  await page.getByTestId("agent-activity").first().getByRole("button", { name: "Use evidence" }).click();
  await expect(page.getByTestId("composer-context")).toContainText("Using evidence");
  await expect(page.getByPlaceholder("Message Affent...")).toHaveValue(
    [
      "Use this evidence in the next step:",
      "- Listed docs",
      "- Read docs/webui-product-design.md",
      "- MCP webui trace",
      "- Read docs/focused-tasks.md",
    ].join("\n"),
  );
  await page.getByRole("button", { name: "Remove" }).click();
  await expect(page.getByPlaceholder("Message Affent...")).toHaveValue("");
  await page.getByRole("button", { name: "Edit prompt" }).first().click();
  await expect(page.getByPlaceholder("Message Affent...")).toHaveValue("delegate docs inspection");
  await expect(page.getByTestId("composer-context")).toContainText("Editing previous message");
  await expect(page.getByTestId("composer-context")).toContainText("Replaced");
  await expect(page.getByRole("button", { name: "Send edited" })).toBeEnabled();
  await expect(page.getByRole("button", { name: "Clear" })).toHaveCount(0);
  await page.getByRole("button", { name: "Remove" }).click();
  await expect(page.getByPlaceholder("Message Affent...")).toHaveValue("");
  await expect(page.getByTestId("composer-context")).toHaveCount(0);
  await expect(page.getByTestId("msg-assistant").first()).toContainText("The delegated checks found");
  await page.getByRole("button", { name: "Ask follow-up" }).first().click();
  await expect(page.getByPlaceholder("Message Affent...")).toHaveValue(
    "Continue from this answer: The delegated checks found the WebUI trace requirements.",
  );
  await expect(page.getByTestId("composer-context")).toContainText("Continuing from answer");
  await expect(page.getByTestId("turn-artifacts")).toContainText("Full output");
  await expect(page.getByTestId("turn-artifacts")).toContainText("000001-artifact-call.txt");
  await expect(page.getByTestId("turn-artifacts")).toContainText("cat big.log");
  await expect(page.getByTestId("fallback-answer")).toContainText("Action output was truncated");
  await expect(page.getByTestId("fallback-answer")).toContainText("large result preview");
  await page.getByTestId("fallback-answer").getByRole("button", { name: "Ask follow-up" }).click();
  await expect(page.getByPlaceholder("Message Affent...")).toHaveValue(
    "Continue from this output: large result preview Full output is available below.",
  );
  await page.getByTestId("turn-artifacts").getByRole("button", { name: "Use in message" }).click();
  await expect(page.getByPlaceholder("Message Affent...")).toHaveValue(
    "Use this file in the next step: .affent/artifacts/tool-results/000001-artifact-call.txt",
  );
  await expect(page.locator(".raw-disclosure")).toHaveCount(0);

  await expect(page.getByTestId("tool-details")).toHaveCount(0);
  await expect(page.getByTestId("agent-activity").first()).toContainText("What Affent did");
  await expect(page.getByTestId("agent-activity-tree").first().getByRole("button", { name: /Find the WebUI trace requirements/ })).toHaveAttribute("aria-expanded", "false");
  await expect(page.getByTestId("execution-tree")).toHaveCount(0);
  await page.screenshot({
    path: testInfo.outputPath(`workflow-closed-${testInfo.project.name}.png`),
    fullPage: true,
  });
  await page.getByRole("button", { name: "Copy activity summary" }).first().click();
  await expect(page.getByTestId("agent-activity").first().getByRole("button", { name: "Copied" })).toBeVisible();
  await page.getByRole("button", { name: "Use this next step" }).first().click();
  await expect(page.getByPlaceholder("Message Affent...")).toHaveValue(/Continue: Replace result parsing with explicit child trace events/);

  await page.getByTestId("turn-navigator").getByRole("button", { name: "Search" }).click();
  await page.getByText("Filters").click();
  await page.getByRole("button", { name: "Actions" }).click();
  await expect(page.getByTestId("work-thread").first()).toBeVisible();
  await page.getByRole("button", { name: /Work details/ }).first().click();
  const executionTree = page.getByTestId("execution-tree");
  await expect(executionTree.getByRole("button", { name: /Find the WebUI trace requirements/ }).first()).toHaveAttribute("aria-expanded", "false");
  await executionTree.getByRole("button", { name: /Find the WebUI trace requirements/ }).first().click();
  await expect(page.getByTestId("tool-details")).toBeVisible();
  await expect(page.getByTestId("tool-details").first()).toContainText("Output");
  await expect(page.getByTestId("tool-details").first()).toContainText("Run summary");
  await expect(page.getByTestId("tool-details").first()).toContainText("action type");
  await expect(page.getByTestId("tool-details").first()).toContainText("Action record");
  await expect(page.getByTestId("tool-details").first()).toContainText(/input \+ \d+ event records/);
  await expect(page.getByTestId("tool-details").first()).toContainText("request input");
  await expect(page.getByTestId("action-inspector-summary").first()).toContainText("Status");
  await expect(page.getByTestId("action-inspector-summary").first()).toContainText("done");
  await expect(page.getByTestId("action-inspector-summary").first()).toContainText("Usage");
  await expect(page.getByTestId("action-inspector-summary").first()).toContainText("392 tokens (310 in / 82 out)");
  await expect(page.getByText("MCP action")).toBeVisible();
  await expect(page.locator(".node-subtitle", { hasText: "External MCP service" })).toBeVisible();
  await expect(page.getByText("subagent_01").first()).toBeVisible();
  await page.getByRole("button", { name: "Copy action record" }).first().click();
  await expect(page.getByTestId("tool-details").first().getByRole("button", { name: "Copied" }).first()).toBeVisible();
  await page.getByRole("button", { name: "Copy input" }).first().click();
  await expect(page.getByTestId("tool-details").first().getByRole("button", { name: "Copied" }).first()).toBeVisible();
  await page.getByTestId("tool-details").first().getByRole("button", { name: "Use output" }).click();
  await expect(page.getByPlaceholder("Message Affent...")).toHaveValue(/Use this output in the next step:\nAction: Find the WebUI trace requirements/);
  await page.getByTestId("turn-artifacts").getByRole("button", { name: "Open file" }).click();
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
  await openFindInChat(page);
  await page.getByTestId("timeline-search").fill("External MCP service");
  await expect(page.getByTestId("timeline-match-count")).toContainText("1/2 messages");
  await expect(page.locator(".execution-tree mark", { hasText: "External MCP service" }).first()).toBeVisible();
  const firstToolDetails = page.getByTestId("tool-details").first();
  const toolTraceSummary = firstToolDetails.locator(".nested-raw > summary");
  await expect(toolTraceSummary).toContainText("Event records");
  await expect(toolTraceSummary).toContainText(/\d+ records/);
  await expect(firstToolDetails.getByText(/\d+ events/)).toHaveCount(0);
  await toolTraceSummary.click();
  await expect(firstToolDetails.locator(".nested-raw .subtle-count")).toContainText(/\d+ records/);
  await expect(firstToolDetails.getByTestId("event-trace")).toBeVisible();
  await expect(page.getByTestId("session-strip")).toHaveCount(0);
  await page.screenshot({
    path: testInfo.outputPath(`workflow-expanded-${testInfo.project.name}.png`),
    fullPage: true,
  });

  expect(errors, `console errors: ${errors.join(" | ")}`).toEqual([]);
});
