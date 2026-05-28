import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { SessionChangesPanel } from "./SessionChangesPanel";
import type { SessionChangesView } from "../view/sessionChanges";

describe("SessionChangesPanel", () => {
  it("renders changed files as evidence and creates an adjustment draft", async () => {
    const user = userEvent.setup();
    const onOpenArtifact = vi.fn();
    const onUseAsDraft = vi.fn();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    render(<SessionChangesPanel defaultOpen changes={changes} onOpenArtifact={onOpenArtifact} onUseAsDraft={onUseAsDraft} />);

    const panel = screen.getByTestId("session-changes-panel");
    expect(panel).toHaveAttribute("open");
    expect(panel).toHaveTextContent("2 changed files");
    expect(screen.getByTestId("session-changes-list")).toHaveTextContent("src/payments.ts");
    expect(screen.getByTestId("session-changes-list")).toHaveTextContent("Edit · changed · +2 -1 · turn 2");
    expect(screen.getByTestId("session-changes-list")).toHaveTextContent("Updated payment route");
    expect(screen.getByTestId("session-changes-list")).toHaveTextContent("Evidence artifact: .affent/artifacts/tool-results/edit.txt");
    expect(screen.getByTestId("session-change-diff")).toHaveAccessibleName("Diff preview for src/payments.ts");
    expect(screen.getByTestId("session-change-diff")).toHaveTextContent("@@ -1,3 +1,4 @@");
    expect(screen.getByTestId("session-change-diff")).toHaveTextContent(/\+\s+return enabled;/);

    await user.click(within(screen.getByTestId("session-changes-list")).getAllByRole("button", { name: "Copy path" })[0]);
    expect(writeText).toHaveBeenCalledWith("src/payments.ts");

    await user.click(within(screen.getByTestId("session-changes-list")).getByRole("button", { name: "Copy diff" }));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("Diff for src/payments.ts"));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("+  return enabled;"));

    await user.click(within(screen.getByTestId("session-changes-list")).getByRole("button", { name: "Open evidence" }));
    expect(onOpenArtifact).toHaveBeenCalledWith(".affent/artifacts/tool-results/edit.txt");

    await user.click(within(screen.getByTestId("session-changes-list")).getAllByRole("button", { name: "Adjust" })[0]);

    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Path: src/payments.ts"), "changed_file");
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("+  return enabled;"), "changed_file");
  });

  it("keeps the panel folded by default", () => {
    render(<SessionChangesPanel changes={changes} />);

    expect(screen.getByTestId("session-changes-panel")).not.toHaveAttribute("open");
  });
});

const changes: SessionChangesView = {
  summary: "2 changed files",
  detail: "2 changed",
  files: [
    {
      path: "src/payments.ts",
      operation: "edit",
      status: "changed",
      turnNumber: 2,
      actionCount: 1,
      additions: 2,
      deletions: 1,
      detail: "Updated payment route",
      artifactPath: ".affent/artifacts/tool-results/edit.txt",
      diffPreview: [
        { kind: "meta", text: "diff --git a/src/payments.ts b/src/payments.ts" },
        { kind: "meta", text: "--- a/src/payments.ts" },
        { kind: "meta", text: "+++ b/src/payments.ts" },
        { kind: "hunk", text: "@@ -1,3 +1,4 @@" },
        { kind: "context", text: " export function pay() {" },
        { kind: "remove", text: "-  return false;" },
        { kind: "add", text: "+  const enabled = true;" },
        { kind: "add", text: "+  return enabled;" },
        { kind: "context", text: " }" },
      ],
    },
    {
      path: "tests/payments.spec.ts",
      operation: "write",
      status: "changed",
      turnNumber: 2,
      actionCount: 1,
    },
  ],
};
