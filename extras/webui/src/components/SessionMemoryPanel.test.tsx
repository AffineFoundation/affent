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
              entries: ["taostats pages are dynamic"],
              entry_count: 1,
              chars_used: 27,
              chars_limit: 4400,
              percent: 1,
              newest_at: "2026-05-26T10:00:00Z",
            },
          ],
        }}
      />,
    );

    expect(screen.getByTestId("session-memory-panel")).toHaveTextContent("3 entries");
    expect(screen.getByTestId("session-memory-panel")).toHaveTextContent("shared user");
    expect(screen.getByTestId("session-memory-latest")).toHaveTextContent("Latest update");
    expect(screen.getByTestId("session-memory-latest")).toHaveTextContent("Replaced");
    expect(screen.getByTestId("session-memory-latest")).toHaveTextContent("memory:research");
    expect(screen.getByTestId("session-memory-latest")).toHaveTextContent("taostats pages require browser network evidence");
    await user.click(within(screen.getByTestId("session-memory-latest")).getByRole("button", { name: "Copy update evidence" }));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("Memory update evidence"));
    await user.click(within(screen.getByTestId("session-memory-latest")).getByRole("button", { name: "Use update as draft" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Review this memory update"), "memory");
    expect(screen.getByTestId("session-memory-list")).toHaveTextContent("User");
    expect(screen.getByTestId("session-memory-list")).toHaveTextContent("Core");
    expect(screen.getByTestId("session-memory-list")).toHaveTextContent("research");

    await user.type(screen.getByPlaceholderText("Search entries or topics"), "taostats");

    const list = screen.getByTestId("session-memory-list");
    expect(list).toHaveTextContent("research");
    expect(list).not.toHaveTextContent("Core");

    await user.click(within(list).getByText("research"));
    expect(list).toHaveTextContent("taostats pages are dynamic");

    await user.click(within(list).getByRole("button", { name: "Copy evidence" }));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("Memory bucket evidence for research"));
    await user.click(within(list).getByRole("button", { name: "Use memory as draft" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Use this memory evidence"), "memory");
  });

  it("shows an empty selected-chat state", () => {
    render(<SessionMemoryPanel defaultOpen noSession />);

    expect(screen.getByTestId("session-memory-panel")).toHaveTextContent("Session memory unavailable");
    expect(screen.getByTestId("session-memory-panel")).toHaveTextContent("Open a saved chat before inspecting session memory.");
    expect(screen.getByTestId("session-memory-panel")).toHaveTextContent("Open a saved chat to inspect stored memory buckets.");
  });

  it("keeps empty memory state factual and avoids unusable search", () => {
    render(<SessionMemoryPanel defaultOpen memory={{ session_id: "s1", has_memory: false, topics: [] }} />);

    const panel = screen.getByTestId("session-memory-panel");
    expect(panel).toHaveTextContent("No durable memory");
    expect(panel).toHaveTextContent("No user, core, or topic entries saved.");
    expect(screen.getByTestId("session-memory-list")).toHaveTextContent("No memory buckets.");
    expect(screen.queryByPlaceholderText("Search entries or topics")).toBeNull();
    expect(panel).not.toHaveTextContent("No matching memory.");
  });

  it("surfaces a compact API diagnostic in the collapsed summary", async () => {
    const diagnostic = "API route /v1/sessions/s1/memory returned the WebUI app shell. The affentserve build may not expose this route. Use the current affentserve build.";
    render(<SessionMemoryPanel error={diagnostic} />);

    const summary = within(screen.getByTestId("session-memory-panel")).getByText("Memory unavailable").closest("summary");
    expect(summary).toHaveTextContent("Memory API failed: API route /v1/sessions/s1/memory returned the WebUI app shell.");
    expect(summary).not.toHaveTextContent("Use the current affentserve build");

    await userEvent.click(screen.getByText("Memory unavailable"));
    expect(screen.getByRole("alert")).toHaveTextContent(diagnostic);
  });
});
