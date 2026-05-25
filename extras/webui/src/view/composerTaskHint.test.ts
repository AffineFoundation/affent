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

  it("warns when no capability snapshot exists for the first research task", () => {
    expect(buildComposerTaskHint("check latest market news")).toEqual({
      label: "Web access unknown",
      detail: "This request may need live sources, but this chat has not reported web access yet.",
      tone: "unknown",
    });
  });

  it("warns for natural external information-gathering prompts", () => {
    expect(buildComposerTaskHint("Affine 是 Bittensor 的一个子网，请收集信息向我介绍", runtime("limited"))).toEqual({
      label: "Current web access limited",
      detail: "This may need current sources; search or page browsing is only partially available.",
      tone: "warning",
    });
    expect(buildComposerTaskHint("Gather information from official sources and tweets about Affine", runtime("off"))).toEqual({
      label: "Current web info unavailable",
      detail: "This request needs live sources; results may be incomplete until search or page access is enabled.",
      tone: "warning",
    });
  });

  it("stays quiet for local project work and research-ready runtimes", () => {
    expect(buildComposerTaskHint("recap the project architecture", runtime("off"))).toBeUndefined();
    expect(buildComposerTaskHint("recap the project architecture")).toBeUndefined();
    expect(buildComposerTaskHint("check latest market news", runtime("ready"))).toBeUndefined();
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
