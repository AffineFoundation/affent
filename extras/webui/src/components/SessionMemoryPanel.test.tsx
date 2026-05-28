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
    expect(screen.getByTestId("session-memory-dashboard")).toHaveTextContent("Shared user + session");
    expect(screen.getByTestId("session-memory-dashboard")).toHaveTextContent("104/7975 chars");
    expect(screen.getByTestId("session-memory-dashboard")).toHaveTextContent("Draft only");
    expect(screen.getByTestId("session-memory-toolbar")).toHaveTextContent("Searchable durable memory");
    await user.click(within(screen.getByTestId("session-memory-toolbar")).getByRole("button", { name: "Copy snapshot" }));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("Memory snapshot evidence"));
    await user.click(within(screen.getByTestId("session-memory-toolbar")).getByRole("button", { name: "Suggest from chat" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("suggest durable memory entries"), "memory");
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Current memory: 4 entries"), "memory");
    await user.click(within(screen.getByTestId("session-memory-toolbar")).getByRole("button", { name: "Review snapshot" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("durable memory snapshot"), "memory");
    expect(screen.getByTestId("session-memory-latest")).toHaveTextContent("Latest update");
    expect(screen.getByTestId("session-memory-latest")).toHaveTextContent("Replaced");
    expect(screen.getByTestId("session-memory-latest")).toHaveTextContent("memory:research");
    expect(screen.getByTestId("session-memory-latest")).toHaveTextContent("taostats pages require browser network evidence");
    expect(screen.getByTestId("session-memory-focus")).toHaveTextContent("research");
    expect(screen.getByTestId("session-memory-focus")).toHaveTextContent("Topic memory");
    expect(screen.getByTestId("session-memory-focus")).toHaveTextContent("taostats pages are dynamic");
    expect(screen.getByTestId("session-memory-focus")).toHaveTextContent("CoinGecko has API fallback");
    await user.click(within(screen.getByTestId("session-memory-latest")).getByRole("button", { name: "Copy update evidence" }));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("Memory update evidence"));
    await user.click(within(screen.getByTestId("session-memory-latest")).getByRole("button", { name: "Review update" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Review this memory update"), "memory");
    expect(screen.getByTestId("session-memory-list")).toHaveTextContent("User");
    expect(screen.getByTestId("session-memory-list")).toHaveTextContent("Core");
    expect(screen.getByTestId("session-memory-list")).toHaveTextContent("research");
    expect(screen.getByTestId("memory-bucket-preview-memory-research")).toHaveTextContent("taostats pages are dynamic");

    await user.type(screen.getByPlaceholderText("Search entries or topics"), "taostats");

    const list = screen.getByTestId("session-memory-list");
    expect(screen.getByTestId("session-memory-search-count")).toHaveTextContent("1 bucket · 1 entry");
    expect(list).toHaveTextContent("research");
    expect(list).not.toHaveTextContent("Core");
    expect(list).toHaveTextContent("1 matched");
    expect(list).toHaveTextContent("taostats pages are dynamic");
    expect(list).not.toHaveTextContent("CoinGecko has API fallback");
    expect(screen.getByTestId("session-memory-focus")).toHaveTextContent("taostats pages are dynamic");

    await user.click(within(list).getByRole("button", { name: "Copy details" }));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("Memory bucket evidence for research"));
    await user.click(within(list).getByRole("button", { name: "Start from memory" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Use this memory evidence"), "memory");
    await user.click(screen.getByRole("button", { name: "Clear" }));
    expect(screen.getByTestId("session-memory-list")).toHaveTextContent("Core");
    expect(screen.queryByTestId("session-memory-search-count")).toBeNull();

    await user.click(screen.getByRole("button", { name: /User\s+1/ }));
    expect(screen.getByTestId("session-memory-search-count")).toHaveTextContent("1 bucket");
    expect(screen.getByTestId("session-memory-list")).toHaveTextContent("prefers concise reports");
    expect(screen.getByTestId("session-memory-list")).not.toHaveTextContent("project runs in containers");
    await user.click(screen.getByRole("button", { name: "Clear" }));
    await user.click(screen.getByRole("button", { name: /Session\s+2/ }));
    expect(screen.getByTestId("session-memory-list")).toHaveTextContent("Core");
    expect(screen.getByTestId("session-memory-list")).toHaveTextContent("research");
    expect(screen.getByTestId("session-memory-list")).not.toHaveTextContent("prefers concise reports");
    await user.click(screen.getByRole("button", { name: "Clear" }));

    await user.type(within(screen.getByTestId("session-memory-form")).getByLabelText("Topic"), "research");
    await user.type(within(screen.getByTestId("session-memory-form")).getByLabelText("Content"), "CoinGecko pages require a browser fallback.");
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
  });

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

    await user.click(within(screen.getByTestId("session-memory-list")).getByText("research"));
    const obsoleteEntry = within(screen.getByTestId("session-memory-list")).getAllByText("obsolete browser fallback rule").find((node) => node.closest("li"));
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
    const staleEntry = within(screen.getByTestId("session-memory-list")).getAllByText("stale browser fallback rule").find((node) => node.closest("li"));
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
    expect(panel).toHaveTextContent("No user, core, or topic entries saved.");
    expect(screen.getByTestId("session-memory-dashboard")).toHaveTextContent("Session scoped");
    expect(screen.getByTestId("session-memory-dashboard")).toHaveTextContent("Draft only");
    expect(screen.getByTestId("session-memory-list")).toHaveTextContent("No durable memory saved");
    expect(screen.queryByPlaceholderText("Search entries or topics")).toBeNull();
    expect(screen.getByTestId("session-memory-form")).toBeInTheDocument();
    expect(screen.getByTestId("session-memory-toolbar")).toHaveTextContent("Suggest from chat");
    screen.getByRole("button", { name: "Suggest from chat" }).click();
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Do not save memory yet"), "memory");
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
