import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { SessionMemoryPanel } from "./SessionMemoryPanel";

describe("SessionMemoryPanel", () => {
  it("renders durable memory buckets and filters entries", async () => {
    const user = userEvent.setup();
    const onUseAsDraft = vi.fn();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    render(
      <SessionMemoryPanel
        defaultOpen
        onUseAsDraft={onUseAsDraft}
        latestUpdate={{
          action: "replace",
          target: "memory",
          topic: "research",
          location: "memory:research",
          preview: "taostats pages require browser network evidence",
        }}
        memory={{
          session_id: "s1",
          has_memory: true,
          shared_user_memory: true,
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
          ],
        }}
      />,
    );

    expect(screen.getByTestId("session-memory-panel")).toHaveTextContent("4 entries");
    expect(screen.getByTestId("session-memory-panel")).toHaveTextContent("shared user");
    expect(screen.queryByTestId("session-memory-dashboard")).toBeNull();
    expect(screen.queryByTestId("session-memory-maintenance")).toBeNull();
    expect(screen.getByTestId("session-memory-toolbar")).toHaveTextContent("Ready · 4 entries in 3 buckets · 104/7975 chars");
    await user.click(within(screen.getByTestId("session-memory-toolbar")).getByRole("button", { name: "Copy snapshot" }));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("Memory snapshot evidence"));
    expect(within(screen.getByTestId("session-memory-toolbar")).queryByRole("button", { name: "Find candidates" })).toBeNull();
    expect(within(screen.getByTestId("session-memory-toolbar")).queryByRole("button", { name: "Review snapshot" })).toBeNull();
    expect(screen.getByTestId("session-memory-latest")).toHaveTextContent("Latest write");
    expect(screen.getByTestId("session-memory-latest")).toHaveTextContent("Replaced");
    expect(screen.getByTestId("session-memory-latest")).toHaveTextContent("taostats pages require browser network evidence");
    expect(screen.getByTestId("session-memory-focus")).toHaveTextContent("research");
    expect(screen.getByTestId("session-memory-focus")).toHaveTextContent("Topic memory");
    expect(screen.getByTestId("session-memory-focus")).toHaveTextContent("Scope");
    expect(screen.getByTestId("session-memory-focus")).toHaveTextContent("Session topic");
    expect(screen.getByTestId("session-memory-focus")).toHaveTextContent("Capacity");
    expect(screen.getByTestId("session-memory-focus")).toHaveTextContent("55/4400 chars · 1%");
    expect(screen.getByTestId("session-memory-focus")).toHaveTextContent("taostats pages are dynamic");
    expect(screen.getByTestId("session-memory-focus")).toHaveTextContent("CoinGecko has API fallback");
    expect(within(screen.getByTestId("session-memory-latest")).queryByRole("button", { name: "Copy update evidence" })).toBeNull();
    expect(within(screen.getByTestId("session-memory-latest")).queryByRole("button", { name: "Review update" })).toBeNull();
    expect(screen.getByTestId("session-memory-list")).toHaveTextContent("User");
    expect(screen.getByTestId("session-memory-list")).toHaveTextContent("Core");
    expect(screen.getByTestId("session-memory-list")).toHaveTextContent("research");
    expect(screen.getByTestId("memory-bucket-preview-memory-research")).toHaveTextContent("taostats pages are dynamic");
    const researchBucket = screen.getByTestId("memory-bucket-preview-memory-research").closest("button");
    expect(researchBucket).not.toBeNull();
    expect(researchBucket!).toHaveTextContent("topic");
    expect(researchBucket!).toHaveTextContent("2 entries");
    expect(researchBucket!).toHaveTextContent("1%");
    expect(researchBucket!).not.toHaveTextContent("55/4400 chars");

    await user.type(screen.getByPlaceholderText("Search entries or topics"), "taostats");

    const list = screen.getByTestId("session-memory-list");
    expect(screen.getByTestId("session-memory-search-count")).toHaveTextContent("1 bucket · 1 entry");
    expect(list).toHaveTextContent("research");
    expect(list).not.toHaveTextContent("Core");
    expect(list).toHaveTextContent("1 matched");
    expect(list).toHaveTextContent("taostats pages are dynamic");
    expect(list).not.toHaveTextContent("CoinGecko has API fallback");
    expect(screen.getByTestId("session-memory-focus")).toHaveTextContent("taostats pages are dynamic");

    expect(within(list).queryByRole("button", { name: "Copy details" })).toBeNull();
    expect(within(list).queryByRole("button", { name: "Start from memory" })).toBeNull();
    await user.click(within(screen.getByTestId("session-memory-focus")).getByRole("button", { name: "Copy entries" }));
    expect(writeText).toHaveBeenCalledWith("taostats pages are dynamic\n\nCoinGecko has API fallback");
    await user.click(screen.getByRole("button", { name: "Clear" }));
    expect(screen.getByTestId("session-memory-list")).toHaveTextContent("Core");
    expect(screen.queryByTestId("session-memory-search-count")).toBeNull();

    const filters = screen.getByRole("group", { name: "Filter memory buckets" });
    await user.click(within(filters).getByRole("button", { name: /User\s+1/ }));
    expect(screen.getByTestId("session-memory-search-count")).toHaveTextContent("1 bucket");
    expect(screen.getByTestId("session-memory-list")).toHaveTextContent("prefers concise reports");
    expect(screen.getByTestId("session-memory-list")).not.toHaveTextContent("project runs in containers");
    await user.click(screen.getByRole("button", { name: "Clear" }));
    await user.click(within(filters).getByRole("button", { name: /Session\s+2/ }));
    expect(screen.getByTestId("session-memory-list")).toHaveTextContent("Core");
    expect(screen.getByTestId("session-memory-list")).toHaveTextContent("research");
    expect(screen.getByTestId("session-memory-list")).not.toHaveTextContent("prefers concise reports");
    await user.click(screen.getByRole("button", { name: "Clear" }));

    await user.type(within(screen.getByTestId("session-memory-form")).getByLabelText("Topic"), "research");
    expect(screen.getByTestId("session-memory-editor-meta")).toHaveTextContent("memory:research");
    await user.type(within(screen.getByTestId("session-memory-form")).getByLabelText("Content"), "CoinGecko pages require a browser fallback.");
    expect(screen.getByTestId("session-memory-editor-meta")).toHaveTextContent("1 line");
    expect(screen.getByTestId("session-memory-editor-meta")).toHaveTextContent("43 chars");
    await user.click(within(screen.getByTestId("session-memory-form")).getByRole("button", { name: "Prepare memory draft" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(
      [
        "Add or update durable memory if this is useful, accurate, and non-secret:",
        "Target: memory",
        "Topic: research",
        "Content:",
        "CoinGecko pages require a browser fallback.",
      ].join("\n"),
      "memory",
    );
  }, 10_000);

  it("saves memory directly when the runtime supports memory writes", async () => {
    const user = userEvent.setup();
    const onAddMemory = vi.fn(async () => ({
      session_id: "s1",
      has_memory: true,
      topics: [
        {
          target: "memory",
          topic: "research",
          entries: ["CoinGecko pages require a browser fallback."],
          entry_count: 1,
          chars_used: 42,
          chars_limit: 4400,
          percent: 1,
        },
      ],
    }));
    render(<SessionMemoryPanel defaultOpen memory={{ session_id: "s1", has_memory: false, topics: [] }} onAddMemory={onAddMemory} />);

    await user.type(within(screen.getByTestId("session-memory-form")).getByLabelText("Topic"), "research");
    await user.type(within(screen.getByTestId("session-memory-form")).getByLabelText("Content"), "CoinGecko pages require a browser fallback.");
    await user.click(within(screen.getByTestId("session-memory-form")).getByRole("button", { name: "Save memory" }));

    expect(onAddMemory).toHaveBeenCalledWith({
      target: "memory",
      topic: "research",
      content: "CoinGecko pages require a browser fallback.",
    });
    expect(await screen.findByText("Memory saved.")).toBeInTheDocument();
    expect(within(screen.getByTestId("session-memory-form")).getByLabelText("Content")).toHaveValue("");
  });

  it("opens add memory with the selected bucket as the write target", async () => {
    const user = userEvent.setup();
    const onAddMemory = vi.fn(async () => ({ session_id: "s1", has_memory: true, topics: [] }));
    render(
      <SessionMemoryPanel
        defaultOpen
        onAddMemory={onAddMemory}
        memory={{
          session_id: "s1",
          has_memory: true,
          topics: [
            {
              target: "memory",
              topic: "project",
              entries: ["Use Vite for WebUI development."],
              entry_count: 1,
              chars_used: 30,
              chars_limit: 4400,
              percent: 1,
            },
            {
              target: "memory",
              topic: "research",
              entries: ["CoinGecko pages require browser fallback."],
              entry_count: 1,
              chars_used: 40,
              chars_limit: 4400,
              percent: 1,
            },
          ],
        }}
      />,
    );

    await user.click(within(screen.getByTestId("session-memory-list")).getByText("project"));
    await user.click(screen.getByText("Add to project"));
    expect(within(screen.getByTestId("session-memory-form")).getByLabelText("Topic")).toHaveValue("project");
    expect(within(screen.getByTestId("session-memory-form")).getByRole("button", { name: /Session/ })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByTestId("session-memory-editor-meta")).toHaveTextContent("memory:project");
    expect(screen.getByTestId("session-memory-editor-meta")).toHaveTextContent("0 lines · 0 chars");

    await user.type(within(screen.getByTestId("session-memory-form")).getByLabelText("Content"), "Use containerized Vite for WebUI screenshots.");
    expect(screen.getByTestId("session-memory-editor-meta")).toHaveTextContent("1 line");
    await user.click(within(screen.getByTestId("session-memory-form")).getByRole("button", { name: "Save memory" }));

    expect(onAddMemory).toHaveBeenCalledWith({
      target: "memory",
      topic: "project",
      content: "Use containerized Vite for WebUI screenshots.",
    });
  });

  it("shows session-derived memory candidates and loads them into the write form", async () => {
    const user = userEvent.setup();
    const onAddMemory = vi.fn(async () => ({ session_id: "s1", has_memory: true, topics: [] }));
    render(
      <SessionMemoryPanel
        defaultOpen
        memory={{ session_id: "s1", has_memory: false, topics: [] }}
        onAddMemory={onAddMemory}
        candidates={[
          {
            id: "project-goal",
            target: "memory",
            topic: "project",
            content: "Project goal: Build a Python CLI 2048 game.",
            source: "Loop goal",
            reason: "Useful for resuming this project without rereading the whole chat.",
          },
        ]}
      />,
    );

    const candidates = screen.getByTestId("session-memory-candidates");
    expect(candidates).toHaveTextContent("Candidate facts");
    expect(candidates).toHaveTextContent("Project goal: Build a Python CLI 2048 game.");
    const maintenance = screen.getByTestId("session-memory-maintenance");
    expect(maintenance).toHaveTextContent("Save candidates");
    expect(maintenance).toHaveTextContent("1 candidate fact");
    expect(within(maintenance).queryByRole("article")).toBeNull();
    expect(screen.getByTestId("session-memory-form")).not.toBeVisible();
    await user.click(within(candidates).getByRole("button", { name: "Edit before save" }));
    expect(within(screen.getByTestId("session-memory-form")).getByLabelText("Topic")).toHaveValue("project");
    expect(within(screen.getByTestId("session-memory-form")).getByLabelText("Content")).toHaveValue("Project goal: Build a Python CLI 2048 game.");
    expect(screen.getByText("Candidate loaded. Review before saving.")).toBeInTheDocument();

    await user.click(within(candidates).getByRole("button", { name: "Save" }));
    expect(onAddMemory).toHaveBeenCalledWith({
      target: "memory",
      topic: "project",
      content: "Project goal: Build a Python CLI 2048 game.",
    });
    expect(await screen.findByText("Memory candidate saved.")).toBeInTheDocument();
  });

  it("removes memory entries with confirmation when the runtime supports edits", async () => {
    const user = userEvent.setup();
    const onRemoveMemory = vi.fn(async () => ({
      session_id: "s1",
      has_memory: true,
      topics: [
        {
          target: "memory",
          topic: "research",
          entries: ["keep current evidence rule"],
          entry_count: 1,
          chars_used: 26,
          chars_limit: 4400,
          percent: 1,
        },
      ],
    }));
    render(
      <SessionMemoryPanel
        defaultOpen
        onRemoveMemory={onRemoveMemory}
        memory={{
          session_id: "s1",
          has_memory: true,
          topics: [
            {
              target: "memory",
              topic: "research",
              entries: ["obsolete browser fallback rule", "keep current evidence rule"],
              entry_count: 2,
              chars_used: 58,
              chars_limit: 4400,
              percent: 1,
            },
          ],
        }}
      />,
    );

    const filterGroup = screen.getByRole("group", { name: "Filter memory buckets" });
    expect(within(filterGroup).queryByRole("button", { name: /User\s+0/ })).toBeNull();
    expect(within(filterGroup).queryByRole("button", { name: /Needs review\s+0/ })).toBeNull();

    await user.click(within(screen.getByTestId("session-memory-list")).getByText("research"));
    const obsoleteEntry = within(screen.getByTestId("session-memory-focus")).getAllByText("obsolete browser fallback rule").find((node) => node.closest("li"));
    expect(obsoleteEntry).toBeDefined();
    await user.click(within(obsoleteEntry!.closest("li")!).getByRole("button", { name: "Remove" }));
    expect(onRemoveMemory).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Confirm remove" }));

    expect(onRemoveMemory).toHaveBeenCalledWith({
      action: "remove",
      target: "memory",
      topic: "research",
      old_text: "obsolete browser fallback rule",
    });
    expect(await screen.findByText("Memory removed.")).toBeInTheDocument();
  });

  it("edits memory entries inline when the runtime supports replace", async () => {
    const user = userEvent.setup();
    const onReplaceMemory = vi.fn(async () => ({
      session_id: "s1",
      has_memory: true,
      topics: [
        {
          target: "memory",
          topic: "research",
          entries: ["current browser fallback rule"],
          entry_count: 1,
          chars_used: 29,
          chars_limit: 4400,
          percent: 1,
        },
      ],
    }));
    render(
      <SessionMemoryPanel
        defaultOpen
        onReplaceMemory={onReplaceMemory}
        memory={{
          session_id: "s1",
          has_memory: true,
          topics: [
            {
              target: "memory",
              topic: "research",
              entries: ["stale browser fallback rule"],
              entry_count: 1,
              chars_used: 27,
              chars_limit: 4400,
              percent: 1,
            },
          ],
        }}
      />,
    );

    await user.click(within(screen.getByTestId("session-memory-list")).getByText("research"));
    const staleEntry = within(screen.getByTestId("session-memory-focus")).getAllByText("stale browser fallback rule").find((node) => node.closest("li"));
    expect(staleEntry).toBeDefined();
    await user.click(within(staleEntry!.closest("li")!).getByRole("button", { name: "Edit" }));
    const editBox = screen.getByLabelText("Edit memory 1");
    expect(editBox).toHaveValue("stale browser fallback rule");
    await user.clear(editBox);
    await user.type(editBox, "current browser fallback rule");
    await user.click(screen.getByRole("button", { name: "Save edit" }));

    expect(onReplaceMemory).toHaveBeenCalledWith({
      action: "replace",
      target: "memory",
      topic: "research",
      old_text: "stale browser fallback rule",
      new_content: "current browser fallback rule",
    });
    expect(await screen.findByText("Memory updated.")).toBeInTheDocument();
  });

  it("surfaces memory maintenance findings and filters affected buckets", async () => {
    const user = userEvent.setup();
    const scrollIntoView = vi.fn();
    Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: scrollIntoView });
    render(
      <SessionMemoryPanel
        defaultOpen
        onReplaceMemory={vi.fn()}
        onRemoveMemory={vi.fn()}
        memory={{
          session_id: "s1",
          has_memory: true,
          user: {
            target: "user",
            topic: "user",
            entries: ["access_token=ghp_example should not be stored"],
            entry_count: 1,
            chars_used: 44,
          },
          topics: [
            {
              target: "memory",
              topic: "project",
              entries: ["Use Vite for WebUI development.", "Use Vite for WebUI development."],
              entry_count: 2,
              chars_used: 62,
              chars_limit: 70,
              percent: 89,
            },
            {
              target: "memory",
              topic: "research",
              entries: ["CoinGecko pages require browser fallback."],
              entry_count: 1,
              chars_used: 40,
            },
          ],
        }}
      />,
    );

    expect(screen.queryByTestId("session-memory-dashboard")).toBeNull();
    expect(screen.getByTestId("session-memory-maintenance")).toHaveTextContent("Remove secrets");
    expect(screen.getByTestId("session-memory-maintenance")).toHaveTextContent("1 entry · User");
    expect(screen.getByTestId("session-memory-maintenance")).toHaveTextContent("Deduplicate");
    expect(screen.getByTestId("session-memory-maintenance")).toHaveTextContent("2 duplicate entries · project");
    expect(screen.getByTestId("session-memory-maintenance")).toHaveTextContent("Reduce pressure");
    expect(screen.getByTestId("session-memory-maintenance")).toHaveTextContent("1 issue · project");
    expect(screen.getByTestId("session-memory-toolbar")).toHaveTextContent("Review · 1 secret entry · 2 duplicate entries · 1 pressure issue · session");
    expect(screen.getByTestId("session-memory-maintenance")).not.toHaveTextContent("Scan for memory entries");
    expect(within(screen.getByTestId("session-memory-maintenance")).queryByRole("article")).toBeNull();
    expect(screen.queryByTestId("session-memory-review")).toBeNull();
    expect(screen.getByTestId("session-memory-list")).toHaveTextContent("Secret");
    expect(screen.getByTestId("session-memory-list")).toHaveTextContent("Duplicate");
    expect(screen.getByTestId("session-memory-list")).toHaveTextContent("Pressure");
    expect(screen.getByTestId("session-memory-list")).toHaveTextContent("access_token=[redacted] should not be stored");
    expect(screen.getByTestId("session-memory-list")).not.toHaveTextContent("ghp_example");
    expect(screen.getByTestId("session-memory-focus")).toHaveTextContent("User");
    expect(screen.getByTestId("session-memory-focus-review")).toHaveTextContent("Secret");

    await user.click(within(screen.getByTestId("session-memory-maintenance")).getByRole("button", { name: /Remove secrets/ }));
    expect(screen.getByTestId("session-memory-search-count")).toHaveTextContent("2 buckets");
    expect(screen.getByTestId("session-memory-list")).toHaveTextContent("User");
    expect(screen.getByTestId("session-memory-list")).toHaveTextContent("project");
    expect(screen.getByTestId("session-memory-list")).not.toHaveTextContent("research");
    expect(screen.getByTestId("session-memory-focus")).toHaveTextContent("User");
    expect(screen.getByTestId("session-memory-focus-review")).toHaveTextContent("Secret");
    expect(screen.getByTestId("session-memory-focus-review")).toHaveTextContent("possible secret or credential");
    expect(screen.getByTestId("session-memory-focus-review")).toHaveTextContent("access_token=[redacted] should not be stored");
    const secretRows = screen.getByTestId("session-memory-focus").querySelectorAll('[data-review-kind="sensitive"]');
    expect(secretRows).toHaveLength(1);
    expect(secretRows[0]).toHaveTextContent("access_token=[redacted] should not be stored");

    await user.click(within(screen.getByTestId("session-memory-focus")).getByRole("button", { name: "Reveal" }));
    expect(screen.getByTestId("session-memory-focus")).toHaveTextContent("access_token=ghp_example should not be stored");

    await user.click(within(screen.getByTestId("session-memory-maintenance")).getByRole("button", { name: /Deduplicate/ }));
    expect(screen.getByTestId("session-memory-focus")).toHaveTextContent("project");
    expect(screen.getByTestId("session-memory-focus-review")).toHaveTextContent("Duplicate");
    expect(screen.getByTestId("session-memory-focus-review")).toHaveTextContent("Capacity");
    expect(screen.getByTestId("session-memory-focus").querySelectorAll('[data-review-kind="duplicate"]')).toHaveLength(2);
    expect(scrollIntoView).toHaveBeenCalledWith({ block: "nearest" });
  });

  it("shows an empty selected-chat state", () => {
    render(<SessionMemoryPanel defaultOpen noSession />);

    expect(screen.getByTestId("session-memory-panel")).toHaveTextContent("Session memory unavailable");
    expect(screen.getByTestId("session-memory-panel")).toHaveTextContent("Open a saved chat before inspecting session memory.");
    expect(screen.getByTestId("session-memory-panel")).toHaveTextContent("Open a saved chat to inspect stored memory buckets.");
  });

  it("keeps empty memory state factual and avoids unusable search", () => {
    const onUseAsDraft = vi.fn();
    render(<SessionMemoryPanel defaultOpen memory={{ session_id: "s1", has_memory: false, topics: [] }} onUseAsDraft={onUseAsDraft} />);

    const panel = screen.getByTestId("session-memory-panel");
    expect(panel).toHaveTextContent("No durable memory");
    expect(panel).not.toHaveTextContent("No user, core, or topic entries saved.");
    expect(screen.queryByTestId("session-memory-dashboard")).toBeNull();
    expect(screen.queryByTestId("session-memory-list")).toBeNull();
    expect(panel).not.toHaveTextContent("No durable memory saved");
    expect(screen.queryByTestId("session-memory-maintenance")).toBeNull();
    expect(screen.queryByPlaceholderText("Search entries or topics")).toBeNull();
    expect(screen.getByTestId("session-memory-form")).toBeInTheDocument();
    expect(screen.queryByTestId("session-memory-toolbar")).toBeNull();
    expect(onUseAsDraft).not.toHaveBeenCalled();
    expect(panel).not.toHaveTextContent("No matching memory.");
  });

  it("keeps the last loaded memory visible when a later API action fails", () => {
    render(
      <SessionMemoryPanel
        defaultOpen
        error="memory storage is read-only"
        memory={{
          session_id: "s1",
          has_memory: true,
          topics: [
            {
              target: "memory",
              topic: "research",
              entries: ["keep current evidence rule"],
              entry_count: 1,
              chars_used: 26,
              chars_limit: 4400,
              percent: 1,
            },
          ],
        }}
        onAddMemory={vi.fn()}
      />,
    );

    expect(screen.getByTestId("session-memory-panel")).toHaveTextContent("1 entry");
    expect(screen.getByTestId("session-memory-panel")).toHaveTextContent("Memory API failed: memory storage is read-only · showing last loaded memory");
    expect(screen.getByRole("alert")).toHaveTextContent("memory storage is read-only");
    expect(screen.getByTestId("session-memory-list")).toHaveTextContent("keep current evidence rule");
    expect(screen.getByTestId("session-memory-form")).toBeInTheDocument();
    expect(screen.queryByTestId("session-memory-fallback")).toBeNull();
  });

  it("surfaces a compact API diagnostic in the collapsed summary", async () => {
    const onUseAsDraft = vi.fn();
    const diagnostic = "API route /v1/sessions/s1/memory returned the WebUI app shell. The affentserve build may not expose this route. Use the current affentserve build.";
    render(<SessionMemoryPanel error={diagnostic} onUseAsDraft={onUseAsDraft} />);

    const summary = within(screen.getByTestId("session-memory-panel")).getByText("Memory unavailable").closest("summary");
    expect(summary).toHaveTextContent("Memory API failed: API route /v1/sessions/s1/memory returned the WebUI app shell.");
    expect(summary).not.toHaveTextContent("Use the current affentserve build");

    await userEvent.click(screen.getByText("Memory unavailable"));
    expect(screen.getByRole("alert")).toHaveTextContent(diagnostic);
    expect(screen.getByTestId("session-memory-fallback")).toHaveTextContent("Memory can still be prepared");
    await userEvent.type(within(screen.getByTestId("session-memory-form")).getByLabelText("Content"), "Remember that this repo uses Vite.");
    await userEvent.click(within(screen.getByTestId("session-memory-form")).getByRole("button", { name: "Prepare memory draft" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Remember that this repo uses Vite."), "memory");
  });
});
