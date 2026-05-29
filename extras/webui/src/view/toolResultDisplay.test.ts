import { describe, expect, it } from "vitest";
import { showsChatArtifact, showsResultStorageChrome, showsToolContextChrome } from "./toolResultDisplay";

describe("tool result display policy", () => {
  it("keeps raw source capture storage out of the chat scan path", () => {
    const source = { tool: "web_fetch" };

    expect(showsChatArtifact(source)).toBe(false);
    expect(showsResultStorageChrome(source)).toBe(false);
    expect(showsToolContextChrome(source)).toBe(false);
  });

  it("keeps normal tool output chrome visible", () => {
    const source = { tool: "shell" };

    expect(showsChatArtifact(source)).toBe(true);
    expect(showsResultStorageChrome(source)).toBe(true);
    expect(showsToolContextChrome(source)).toBe(true);
  });
});
