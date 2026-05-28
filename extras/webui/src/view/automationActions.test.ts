import { describe, expect, it } from "vitest";
import { automationActionLabel } from "./automationActions";

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
});
