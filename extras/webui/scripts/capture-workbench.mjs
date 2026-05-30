import { chromium } from "playwright";

const url = process.env.WEBUI_SCREENSHOT_URL || "http://127.0.0.1:18789/";
const tab = process.env.WEBUI_SCREENSHOT_TAB || "Files";
const screenshot = process.env.WEBUI_SCREENSHOT_PATH || "/tmp/affent-webui-screenshot.png";
const workspacePath = process.env.WEBUI_SCREENSHOT_WORKSPACE_PATH || "";
const hoverSelector = process.env.WEBUI_SCREENSHOT_HOVER_SELECTOR || "";
const clickText = process.env.WEBUI_SCREENSHOT_CLICK_TEXT || "";
const mockMemory = process.env.WEBUI_SCREENSHOT_MOCK_MEMORY || "";
const mockAutomation = process.env.WEBUI_SCREENSHOT_MOCK_AUTOMATION || "";
const mockSkills = process.env.WEBUI_SCREENSHOT_MOCK_SKILLS || "";
const mockTask = process.env.WEBUI_SCREENSHOT_MOCK_TASK || "";
const mockConfig = process.env.WEBUI_SCREENSHOT_MOCK_CONFIG || "";
const skillProbe = process.env.WEBUI_SCREENSHOT_SKILL_PROBE || "";
const openWorkbench = process.env.WEBUI_SCREENSHOT_OPEN_WORKBENCH !== "false";
const openChat = process.env.WEBUI_SCREENSHOT_OPEN_CHAT || "";
const executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE || "/usr/bin/chromium";
const viewportWidth = Number(process.env.WEBUI_SCREENSHOT_WIDTH || 1440);
const viewportHeight = Number(process.env.WEBUI_SCREENSHOT_HEIGHT || 960);

const browser = await chromium.launch({
  executablePath,
  headless: true,
  args: ["--no-sandbox", "--disable-dev-shm-usage"],
});

try {
  const page = await browser.newPage({ viewport: { width: viewportWidth, height: viewportHeight }, deviceScaleFactor: 1 });
  const consoleMessages = [];
  const failed = [];

  page.on("console", (message) => {
    consoleMessages.push(`${message.type()}: ${message.text()}`);
  });
  page.on("requestfailed", (request) => {
    failed.push(`${request.method()} ${request.url()} ${request.failure()?.errorText || ""}`.trim());
  });
  page.on("response", (response) => {
    if (response.status() >= 400) failed.push(`${response.status()} ${response.url()}`);
  });

  if (mockMemory) {
    await page.route("**/v1/sessions/*/memory", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(mockMemoryPayload(mockMemory)),
      });
    });
  }

  if (mockAutomation) {
    await page.route("**/v1/sessions?limit=100", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(mockAutomationSessionList(mockAutomation)),
      });
    });
    await page.route("**/v1/sessions/mock-automation/history?**", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(mockAutomationHistory()),
      });
    });
    await page.route("**/v1/sessions/mock-automation/schedules", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(mockAutomationSchedules(mockAutomation)),
      });
    });
    await page.route("**/v1/sessions/mock-automation/loop-protocol", async (route) => {
      if (mockAutomation !== "loop-timers" && mockAutomation !== "loop-draft-ready") {
        await route.fulfill({
          status: 404,
          contentType: "application/json",
          body: JSON.stringify({ error: { message: "loop protocol mock not enabled" } }),
        });
        return;
      }
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(mockAutomationLoopProtocol()),
      });
    });
  }

  if (mockTask) {
    await page.route("**/v1/sessions?limit=100", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(mockTaskSessionList(mockTask)),
      });
    });
    await page.route("**/v1/sessions/mock-task/history?**", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(mockTaskHistory(mockTask)),
      });
    });
  }

  if (mockSkills) {
    await page.route("**/v1/skills", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(mockSkillsPayload(mockSkills)),
      });
    });
  }

  if (mockConfig) {
    await page.route("**/v1/settings/git-check", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(mockGitCheckPayload()),
      });
    });
    await page.route("**/v1/settings", async (route) => {
      if (route.request().method() !== "GET") {
        await route.fallback();
        return;
      }
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(mockConfigPayload(mockConfig)),
      });
    });
  }

  await page.goto(url, { waitUntil: "networkidle" });

  if (openChat) {
    await openRequestedChat(page, openChat);
  }

  if (openWorkbench) {
    const workbenchButton = page.getByRole("button", { name: /^Workbench$/ });
    if (await workbenchButton.count()) await workbenchButton.first().click();
  }

  if (openWorkbench && tab) {
    const tabButton = page.getByRole("button", { name: new RegExp(`^${escapeRegExp(tab)}\\b`) });
    if (await tabButton.count()) await tabButton.first().click();
  }

  if (openWorkbench && workspacePath && tab === "Files") {
    await openWorkspacePath(page, workspacePath);
  }

  if (openWorkbench && tab === "Skills" && skillProbe) {
    try {
      const input = page.getByPlaceholder("e.g. repair failing workspace tests").first();
      await input.waitFor({ state: "visible", timeout: 2500 });
      await input.fill(skillProbe);
      await page.waitForTimeout(500);
    } catch (err) {
      console.warn(`Could not fill skill probe ${skillProbe}: ${err instanceof Error ? err.message : String(err)}`);
    }
  }

  if (hoverSelector) {
    try {
      const hoverTarget = page.locator(hoverSelector).first();
      await hoverTarget.waitFor({ state: "visible", timeout: 2500 });
      await hoverTarget.hover();
      await page.waitForTimeout(250);
    } catch (err) {
      console.warn(`Could not hover selector ${hoverSelector}: ${err instanceof Error ? err.message : String(err)}`);
    }
  }

  if (clickText) {
    try {
      const exactButton = page.getByRole("button", { name: new RegExp(`^${escapeRegExp(clickText)}$`) }).first();
      const fallbackText = page.getByText(clickText, { exact: true }).first();
      const clickTarget = await exactButton.count() ? exactButton : fallbackText;
      await clickTarget.waitFor({ state: "visible", timeout: 2500 });
      await clickTarget.click();
      await page.waitForTimeout(500);
    } catch (err) {
      console.warn(`Could not click text ${clickText}: ${err instanceof Error ? err.message : String(err)}`);
    }
  }

  await page.waitForTimeout(1000);
  await page.screenshot({ path: screenshot, fullPage: true });

  const bodyText = await page.locator("body").innerText();
  const overflow = await page.evaluate(() => {
    const offenders = [];
    for (const element of document.querySelectorAll("button, [class*='session-workspace'], [class*='session-files'], [class*='workbench']")) {
      const rect = element.getBoundingClientRect();
      if (!rect.width || !rect.height) continue;
      if (element.scrollWidth > element.clientWidth + 2 || element.scrollHeight > element.clientHeight + 2) {
        offenders.push({
          className: String(element.className || ""),
          text: (element.textContent || "").trim().slice(0, 100),
          scrollWidth: element.scrollWidth,
          clientWidth: element.clientWidth,
          scrollHeight: element.scrollHeight,
          clientHeight: element.clientHeight,
        });
      }
    }
    return offenders.slice(0, 25);
  });

  console.log(JSON.stringify({
    url,
    tab,
    openWorkbench,
    openChat,
    workspacePath,
    hoverSelector,
    clickText,
    mockMemory,
    mockAutomation,
    mockSkills,
    mockTask,
    mockConfig,
    skillProbe,
    screenshot,
    failed,
    consoleMessages: consoleMessages.slice(-30),
    overflow,
    textSample: bodyText.slice(0, 1600),
  }, null, 2));
} finally {
  await browser.close();
}

function mockConfigPayload(mode) {
  if (mode === "review") {
    return {
      env: [
        { name: "GITHUB_TOKEN", configured: true, updated_at: "2026-05-27T10:00:00Z" },
        { name: "EMPTY_TOKEN", configured: false },
        { name: "GOOGLE_API_KEY", configured: true, updated_at: "2026-05-28T09:20:00Z" },
      ],
      ssh: {
        exists: true,
        public_key: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMockAffentWorkbenchKey affent@example",
        public_key_path: "/workspace/.home/.ssh/id_ed25519.pub",
      },
    };
  }
  return {
    env: [{ name: "GITHUB_TOKEN", configured: true, updated_at: "2026-05-27T10:00:00Z" }],
    ssh: { exists: false },
  };
}

function mockGitCheckPayload() {
  return {
    kind: "host",
    target: "github.com",
    host: "github.com",
    status: "ok",
    exit_code: 1,
    output: "successfully authenticated",
    duration_ms: 42,
    checked_at: "2026-05-30T00:00:00Z",
  };
}

function mockAutomationSessionList(mode) {
  const summary = mockAutomationSchedules(mode).summary;
  const hasLoop = mode === "loop-timers" || mode === "loop-draft-ready";
  const loopProtocol = hasLoop ? mockAutomationLoopProtocol(mode) : undefined;
  return {
    sessions: [
      {
        id: "mock-automation",
        active: false,
        durable: true,
        topic_user_message: mode === "loop-draft-ready" ? "Prepare WebUI automation loop" : hasLoop ? "Maintain WebUI automation reliability" : mode === "timers" ? "Keep WebUI automation checks running" : "Mock automation",
        has_conversation: true,
        has_events: true,
        has_artifacts: false,
        has_memory: false,
        has_runtime_skills: false,
        has_loop_protocol: hasLoop,
        loop_protocol: loopProtocol?.summary,
        has_loop_state: hasLoop,
        loop_state: loopProtocol?.state,
        has_schedules: summary.count > 0,
        schedules: summary,
        usage: { input_tokens: 4800, output_tokens: 900, turns: 3 },
      },
    ],
    has_more: false,
  };
}

function mockTaskSessionList(mode) {
  const failed = mode === "failure";
  return {
    sessions: [
      {
        id: "mock-task",
        active: false,
        durable: true,
        topic_user_message: failed ? "Harden Workbench Task actions" : "Continue Workbench polishing",
        has_conversation: true,
        has_events: true,
        has_artifacts: false,
        has_memory: false,
        has_runtime_skills: false,
        usage: { input_tokens: 12000, output_tokens: 2400, turns: 4 },
        context: {
          message_count: 96,
          compact_trigger: 120,
          compact_percent: 80,
          messages_until_compact: 24,
          estimated_conversation_tokens: 9000,
          estimated_tool_schema_tokens: 2400,
          tool_schema_budget_tokens: 3000,
          model_context_window_tokens: 100000,
          model_context_window_source: "provider",
          reserved_output_tokens: 30000,
          compact_trigger_input_tokens: 70000,
          compact_trigger_input_percent: 80,
          request_input_tokens_until_compact: 4200,
        },
        context_compactions: {
          count: 3,
          reactive: 2,
          removed_messages: 64,
          latest_reason: "context_overflow",
          latest_model_context_window_tokens: 100000,
          latest_model_context_window_source: "provider",
          latest_trigger_input_tokens: 70000,
          latest_trigger_input_percent: 80,
          latest_reserved_output_tokens: 30000,
        },
        task_state: failed ? {
          objective: "Harden Workbench Task actions",
          status: "running",
          request_mode: "execute_plan",
          current_step: "Wire Task next actions into composer drafts",
          next_step: "Rerun focused Task tests and capture a screenshot",
          changed_files: [{ path: "extras/webui/src/components/WorkbenchContextPanel.tsx", action: "edit" }],
          attempted_actions: [{ tool: "shell", summary: "npm test -- WorkbenchContextPanel.test.tsx" }],
          failed_actions: [{
            tool: "shell",
            summary: "WorkbenchContextPanel.test.tsx failed",
            kinds: ["command_failed"],
            next: "Fix the assertion and rerun the focused test",
          }],
          evidence: [{ source: "shell", summary: "npm test -- WorkbenchContextPanel.test.tsx" }],
          verification_state: "failed",
        } : {
          objective: "Continue Workbench polishing",
          status: "running",
          current_step: "Review Task surface",
          next_step: "Tighten the next action and rerun the screenshot",
        },
      },
    ],
    has_more: false,
  };
}

function mockTaskHistory(mode) {
  if (mode === "source") {
    return {
      session_id: "mock-task",
      events: [
        { id: 1, type: "turn.start", data: { turn_id: "t1" } },
        { id: 2, type: "user.message", data: { turn_id: "t1", text: "Verify current market data from live sources" } },
        { id: 3, type: "tool.request", data: { turn_id: "t1", call_id: "network", tool: "browser_network_read", args: { ref: "n1", json_path: "$.price" } } },
        {
          id: 4,
          type: "tool.result",
          data: {
            turn_id: "t1",
            call_id: "network",
            exit_code: 0,
            result_summary: "SourceAccess: browser_network_url=https://taostats.io/api/subnets/120; requested_url=https://taostats.io/subnets/120; ref=n1; status=200; content_type=application/json; source_method=network_xhr_fetch",
            result: "SourceAccess: browser_network_url=https://taostats.io/api/subnets/120; requested_url=https://taostats.io/subnets/120; ref=n1; status=200; content_type=application/json; source_method=network_xhr_fetch\n{\"price\":\"0.06342 T\",\"market_cap\":\"201.04K T\"}",
          },
        },
        { id: 5, type: "message.done", data: { turn_id: "t1", text: "Network source evidence is available." } },
      ],
      next_after: 5,
      has_more: false,
      trace_schema_detected: true,
      trace_schema_version: 1,
    };
  }

  return {
    session_id: "mock-task",
    events: [
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "Harden Workbench Task actions" } },
      { id: 3, type: "tool.request", data: { turn_id: "t1", call_id: "source", tool: "web_fetch", args: { url: "https://example.test/workbench-trace" } } },
      {
        id: 4,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "source",
          exit_code: 0,
          result_summary: "SourceAccess: fetched_url=https://example.test/workbench-trace; requested_url=https://example.test/workbench-trace; status=200; content_type=text/html",
          result: "SourceAccess: fetched_url=https://example.test/workbench-trace; requested_url=https://example.test/workbench-trace; status=200; content_type=text/html\nTrace evidence source.",
        },
      },
      { id: 5, type: "tool.request", data: { turn_id: "t1", call_id: "shell-fail", tool: "shell", args: { command: "npm test -- WorkbenchContextPanel.test.tsx" } } },
      {
        id: 6,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "shell-fail",
          exit_code: 1,
          failure_kind: "command_failed",
          result_summary: "WorkbenchContextPanel.test.tsx failed\nNext: Fix the assertion and rerun the focused test\nFailure: kind=command_failed",
          result: "WorkbenchContextPanel.test.tsx failed\nNext: Fix the assertion and rerun the focused test\nFailure: kind=command_failed",
        },
      },
      { id: 7, type: "message.done", data: { turn_id: "t1", text: "Task action draft controls are being wired." } },
      { id: 8, type: "thinking.done", data: { turn_id: "t1", text: "Inspect the failed assertion and compare the Workbench usage surface against the intended replacement." } },
      { id: 9, type: "usage", data: { turn_id: "t1", input_tokens: 12000, output_tokens: 2400 } },
    ],
    next_after: 9,
    has_more: false,
    trace_schema_detected: true,
    trace_schema_version: 1,
  };
}

function mockAutomationHistory() {
  return {
    session_id: "mock-automation",
    events: [
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "Keep WebUI automation checks running" } },
      { id: 3, type: "message.done", data: { turn_id: "t1", text: "Automation timers are configured." } },
    ],
    next_after: 3,
    has_more: false,
    trace_schema_detected: true,
    trace_schema_version: 1,
  };
}

function mockAutomationLoopProtocol(mode = "") {
  const draft = mode === "loop-draft-ready";
  return {
    session_id: "mock-automation",
    protocol: [
      "# LOOP.md",
      "",
      draft ? "Goal: Prepare WebUI automation loop." : "Goal: Maintain WebUI automation reliability.",
      draft ? "Current Situation: Calibration answer is recorded; activation is waiting." : "Current Situation: Watch the Workbench Automation tab, timer health, and screenshot regressions.",
      "Stop Conditions: Stop if the user disables automation or no WebUI work remains.",
      "Recovery Anchors: Check focused tests, typecheck, and latest screenshots before each update.",
    ].join("\n"),
    summary: {
      path: ".affent/loops/mock-automation/LOOP.md",
      status: draft ? "draft" : "running",
      bytes: 324,
      preview: draft ? "Prepare WebUI automation loop after recorded calibration." : "Maintain WebUI automation reliability with focused checks and screenshot review.",
      state: {
        version: 1,
        loop_id: "mock-automation",
        status: draft ? "draft" : "running",
        initial_goal_preview: draft ? "Prepare WebUI automation loop" : "Maintain WebUI automation reliability",
        protocol_path: ".affent/loops/mock-automation/LOOP.md",
        calibration_questions: 1,
        calibration_answers: draft ? 1 : 0,
        last_calibration_answer_preview: draft ? "Pause when screenshots or focused tests show regressions." : undefined,
        protocol_feeds: 4,
        protocol_updates: 2,
        last_decision_kind: "continue",
        last_decision: "Run the next timer tick only after checking focused UI screenshots.",
        last_event_summary: "Fed LOOP.md into scheduled timer turn",
        last_memory_update_action: "replace",
        last_memory_update_preview: "Automation tab requires list + inspector cleanup",
        context_compactions: 1,
        last_context_compaction_reason: "context_overflow",
      },
    },
    state: {
      version: 1,
      loop_id: "mock-automation",
      status: draft ? "draft" : "running",
      initial_goal_preview: draft ? "Prepare WebUI automation loop" : "Maintain WebUI automation reliability",
      protocol_path: ".affent/loops/mock-automation/LOOP.md",
      calibration_questions: 1,
      calibration_answers: draft ? 1 : 0,
      last_calibration_answer_preview: draft ? "Pause when screenshots or focused tests show regressions." : undefined,
      protocol_feeds: 4,
      protocol_updates: 2,
      last_decision_kind: "continue",
      last_decision: "Run the next timer tick only after checking focused UI screenshots.",
      last_event_summary: "Fed LOOP.md into scheduled timer turn",
      last_memory_update_action: "replace",
      last_memory_update_preview: "Automation tab requires list + inspector cleanup",
      context_compactions: 1,
      last_context_compaction_reason: "context_overflow",
    },
    events: [
      {
        seq: 1,
        time: "2026-05-29T20:00:00Z",
        type: "loop.protocol_feed",
        summary: "Fed LOOP.md into scheduled timer turn",
        feed_number: 4,
      },
      {
        seq: 2,
        time: "2026-05-29T20:15:00Z",
        type: "loop.protocol_update",
        summary: "Updated recovery anchors after screenshot review",
        sections_changed: ["Recovery Anchors"],
      },
    ],
  };
}

function mockAutomationSchedules(mode) {
  if (mode === "loop-draft-ready") {
    return {
      session_id: "mock-automation",
      summary: { count: 0, enabled: 0 },
      schedules: [],
    };
  }
  if (mode === "loop-timers") {
    return {
      session_id: "mock-automation",
      summary: {
        count: 2,
        enabled: 2,
        next_run_at: "2026-05-29T22:00:00Z",
        next_schedule_id: "sched_loop",
        next_schedule_kind: "loop_tick",
        next_prompt_preview: "Loop every 30m: WebUI automation reliability",
      },
      schedules: [
        {
          id: "sched_loop",
          kind: "loop_tick",
          prompt: "Scheduled loop tick for session: WebUI automation reliability",
          display_text: "Loop every 30m: WebUI automation reliability",
          enabled: true,
          next_run_at: "2026-05-29T22:00:00Z",
          repeat_interval_seconds: 1800,
          created_at: "2026-05-29T20:00:00Z",
          updated_at: "2026-05-29T21:00:00Z",
        },
        {
          id: "sched_daily",
          kind: "daily_checkin",
          prompt: "Daily Workbench automation review",
          display_text: "Daily check-in: Workbench automation review",
          enabled: true,
          next_run_at: "2026-05-30T15:00:00Z",
          repeat_interval_seconds: 86400,
          created_at: "2026-05-29T20:00:00Z",
          updated_at: "2026-05-29T21:30:00Z",
        },
      ],
    };
  }
  return {
    session_id: "mock-automation",
    summary: {
      count: 2,
      enabled: 1,
      next_run_at: "2026-05-29T22:00:00Z",
      next_schedule_id: "sched_checkin",
      next_schedule_kind: "checkin",
      next_prompt_preview: "Check WebUI automation health",
    },
    schedules: [
      {
        id: "sched_checkin",
        kind: "checkin",
        prompt: "Check WebUI automation health",
        display_text: "Check in 1h: WebUI automation health",
        enabled: true,
        next_run_at: "2026-05-29T22:00:00Z",
        created_at: "2026-05-29T21:00:00Z",
        updated_at: "2026-05-29T21:00:00Z",
      },
      {
        id: "sched_daily",
        kind: "daily_checkin",
        prompt: "Daily Workbench review",
        display_text: "Daily check-in: Workbench review",
        enabled: false,
        next_run_at: "2026-05-30T15:00:00Z",
        repeat_interval_seconds: 86400,
        created_at: "2026-05-29T21:00:00Z",
        updated_at: "2026-05-29T21:30:00Z",
      },
    ],
  };
}

function mockMemoryPayload(mode) {
  if (mode === "review") {
    return {
      session_id: "mock-session",
      has_memory: true,
      shared_user_memory: true,
      user: {
        target: "user",
        topic: "user",
        entries: ["prefers concise reports", "access_token=ghp_example should not be stored"],
        entry_count: 2,
        chars_used: 72,
        chars_limit: 1375,
        percent: 5,
      },
      core: {
        target: "memory",
        topic: "core",
        entries: ["project runs in containers"],
        entry_count: 1,
        chars_used: 26,
        chars_limit: 2200,
        percent: 1,
      },
      topics: [
        {
          target: "memory",
          topic: "research",
          entries: ["taostats pages are dynamic", "CoinGecko has API fallback"],
          entry_count: 2,
          chars_used: 55,
          chars_limit: 4400,
          percent: 1,
          newest_at: "2026-05-26T10:00:00Z",
        },
        {
          target: "memory",
          topic: "project",
          entries: ["Use Vite for WebUI development.", "Use Vite for WebUI development."],
          entry_count: 2,
          chars_used: 62,
          chars_limit: 70,
          percent: 89,
        },
      ],
    };
  }
  return {
    session_id: "mock-session",
    has_memory: true,
    topics: [
      {
        target: "memory",
        topic: "research",
        entries: ["taostats pages are dynamic", "CoinGecko has API fallback"],
        entry_count: 2,
        chars_used: 55,
        chars_limit: 4400,
        percent: 1,
        newest_at: "2026-05-26T10:00:00Z",
      },
    ],
  };
}

function mockSkillsPayload(mode) {
  return {
    session_id: "account",
    count: mode === "review" ? 3 : 2,
    install_enabled: true,
    skills: [
      {
        name: "coding_repair_workflow",
        description: "Reproduce failures before editing code.",
        source: "embed:internal/agent/builtin_skills/coding_repair_workflow/SKILL.md",
        runtime: false,
        triggers: ["fix", "repair", "test failure"],
        required_tools: ["workspace"],
        body_preview: "AFFENT ACTIVE SKILL: coding_repair_workflow\nReproduce first.",
        body_bytes: 96,
      },
      {
        name: "trace_debugging",
        description: "Inspect event traces and isolate tool failures.",
        source: "file:///account-skills/trace_debugging/SKILL.md",
        runtime: true,
        triggers: ["trace", "tool failed"],
        required_tools: ["workspace"],
        body_preview: "AFFENT ACTIVE SKILL: trace_debugging\nUse Trace before retrying.",
        body_bytes: 112,
      },
      ...(mode === "review" ? [{
        name: "manual_workflow",
        description: "",
        source: "file:///account-skills/manual_workflow/SKILL.md",
        runtime: true,
        triggers: [],
        required_tools: [],
        body_preview: "AFFENT ACTIVE SKILL: manual_workflow\nDocument the workflow.",
        body_bytes: 88,
      }] : []),
    ],
  };
}

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

async function openWorkspacePath(page, path) {
  for (const part of path.split("/").filter(Boolean)) {
    const escaped = escapeRegExp(part);
    const candidate = page.getByRole("button", { name: new RegExp(`(^|\\s)${escaped}(\\s|$)`) }).first();
    try {
      await candidate.waitFor({ state: "visible", timeout: 2500 });
      await candidate.click();
      await page.waitForTimeout(400);
    } catch {
      console.warn(`Could not open workspace path part: ${part}`);
      return;
    }
  }
}

async function openRequestedChat(page, mode) {
  if (mode === "latest") {
    const latest = page.getByRole("button", { name: /Open latest chat/i }).first();
    try {
      await latest.waitFor({ state: "visible", timeout: 2500 });
      await latest.click();
      await page.waitForLoadState("networkidle").catch(() => {});
      await page.waitForTimeout(600);
      return;
    } catch {
      console.warn("Could not open latest chat");
    }
  }
  if (mode === "first" || mode === "latest") {
    const firstChat = page.locator("[data-testid='session-list'] button").first();
    try {
      await firstChat.waitFor({ state: "visible", timeout: 2500 });
      await firstChat.click();
      await page.waitForLoadState("networkidle").catch(() => {});
      await page.waitForTimeout(600);
    } catch {
      console.warn("Could not open first chat");
    }
  }
}
