import { describe, expect, it } from "vitest";
import { buildWorkspaceFileView, parentWorkspacePath, workspaceFileDraft } from "./workspaceFile";

describe("workspaceFile", () => {
  it("normalizes a workspace directory response for the Files panel", () => {
    const view = buildWorkspaceFileView({
      session_id: "s1",
      path: ".",
      kind: "directory",
      has_more: false,
      entries: [
        { name: "src", path: "src", kind: "directory" },
        { name: "README.md", path: "README.md", kind: "file", bytes: 2048 },
      ],
    });

    expect(view).toMatchObject({
      path: ".",
      kind: "directory",
      title: "Workspace root",
      detail: "1 directory · 1 file",
      entries: [
        { name: "src", path: "src", kind: "directory" },
        { name: "README.md", path: "README.md", kind: "file", size: "2 KiB" },
      ],
    });
    expect(workspaceFileDraft(view)).toContain("- dir src");
    expect(workspaceFileDraft(view)).toContain("- file README.md (2 KiB)");
  });

  it("normalizes file content and parent paths", () => {
    const view = buildWorkspaceFileView({
      session_id: "s1",
      path: "src/main.go",
      kind: "file",
      bytes: 26,
      text: "package main\nfunc main() {}\n",
      has_more: true,
    });

    expect(view).toMatchObject({
      path: "src/main.go",
      kind: "file",
      title: "main.go",
      detail: "26 B · preview truncated",
      lines: ["package main", "func main() {}", ""],
      hasMore: true,
    });
    expect(parentWorkspacePath("src/main.go")).toBe("src");
    expect(parentWorkspacePath("src")).toBe(".");
    expect(parentWorkspacePath(".")).toBeUndefined();
    expect(workspaceFileDraft(view)).toContain("Snapshot is truncated");
  });
});
