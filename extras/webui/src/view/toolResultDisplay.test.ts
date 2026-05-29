import { describe, expect, it } from "vitest";
import { showsChatArtifact, showsResultStorageChrome, showsToolContextChrome, showsWorkbenchArtifact } from "./toolResultDisplay";

describe("tool result display policy", () => {
  it("keeps raw source capture storage out of the chat scan path", () => {
    for (const tool of ["web_fetch", "browser_navigate", "browser_snapshot", "browser_network_read"]) {
      const source = { tool };

      expect(showsChatArtifact(source)).toBe(false);
      expect(showsWorkbenchArtifact(source)).toBe(false);
      expect(showsResultStorageChrome(source)).toBe(false);
      expect(showsToolContextChrome(source)).toBe(false);
    }
  });

  it("recognizes raw source capture artifacts when only the display source is available", () => {
    const source = { source: "browser_navigate" };

    expect(showsChatArtifact(source)).toBe(false);
    expect(showsWorkbenchArtifact(source)).toBe(false);
    expect(showsResultStorageChrome(source)).toBe(false);
    expect(showsToolContextChrome(source)).toBe(false);
  });

  it("keeps normal tool output chrome visible", () => {
    const source = { tool: "shell" };

    expect(showsChatArtifact(source)).toBe(true);
    expect(showsWorkbenchArtifact(source)).toBe(true);
    expect(showsResultStorageChrome(source)).toBe(true);
    expect(showsToolContextChrome(source)).toBe(true);
  });
});
