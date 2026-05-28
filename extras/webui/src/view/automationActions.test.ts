import { describe, expect, it } from "vitest";
import { automationActionLabel, shouldOfferLoopSetupAction } from "./automationActions";

describe("automationActionLabel", () => {
  it("uses one action vocabulary for loop setup and timers", () => {
    expect(automationActionLabel("loop_setup")).toBe("Set up long-running loop");
    expect(automationActionLabel("checkin")).toBe("Schedule 1h check-in");
    expect(automationActionLabel("loop_tick")).toBe("Schedule 30m loop tick");
    expect(automationActionLabel("daily")).toBe("Schedule daily check-in");
  });

  it("keeps transient busy states short", () => {
    expect(automationActionLabel("loop_setup", true)).toBe("Setting up");
    expect(automationActionLabel("loop_tick", true)).toBe("Scheduling");
  });

  it("only offers loop setup for explicit long-running automation intent", () => {
    expect(shouldOfferLoopSetupAction("fix the failing checkout test")).toBe(false);
    expect(shouldOfferLoopSetupAction("keep improving the web workbench")).toBe(true);
    expect(shouldOfferLoopSetupAction("analyze market data for several days")).toBe(true);
    expect(shouldOfferLoopSetupAction("每天检查一次构建状态并提醒我")).toBe(true);
  });
});
