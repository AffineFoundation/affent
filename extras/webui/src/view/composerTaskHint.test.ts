import { describe, expect, it } from "vitest";
import { buildComposerTaskHint } from "./composerTaskHint";
import type { RuntimeCapabilityView } from "./runtimeCapabilities";

describe("buildComposerTaskHint", () => {
  it("warns when a current research task is typed into a local runtime", () => {
    expect(buildComposerTaskHint("Analyze Affine recent market trends and Twitter reaction", runtime("off"))).toEqual({
      label: "Current web info unavailable",
      detail: "This request needs live sources; results may be incomplete until search or page access is enabled.",
      tone: "warning",
    });
  });

  it("surfaces unknown research capability for saved chats", () => {
    expect(buildComposerTaskHint("分析 affine 最近的发展趋势和币价", runtime("unknown"))).toEqual({
      label: "Web access unknown",
      detail: "Send once to refresh this chat's web access before relying on current information.",
      tone: "unknown",
    });
  });

  it("stays quiet for local project work and research-ready runtimes", () => {
    expect(buildComposerTaskHint("recap the project architecture", runtime("off"))).toBeUndefined();
    expect(buildComposerTaskHint("check latest market news", runtime("ready"))).toBeUndefined();
    expect(buildComposerTaskHint("check latest market news")).toBeUndefined();
  });
});

function runtime(research: RuntimeCapabilityView["research"]): RuntimeCapabilityView {
  return {
    headline: research,
    detail: research,
    tone: research === "ready" ? "ready" : research === "unknown" ? "unknown" : "warning",
    research,
    chips: [],
  };
}
