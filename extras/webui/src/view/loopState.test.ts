import { describe, expect, it } from "vitest";
import { normalizeEvents } from "../normalize/normalizeEvent";
import { loopStateFromEvents, mergeLoopStateFromEvents } from "./loopState";

describe("loopState view model", () => {
  it("derives draft calibration state from live session events", () => {
    const events = normalizeEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      {
        id: 2,
        type: "loop.protocol_calibration_request",
        data: {
          loop_id: "market-loop",
          status: "draft",
          protocol_path: ".affent/loops/market-loop/LOOP.md",
          calibration_questions: 1,
          last_calibration_question_preview: "What should pause this loop?",
        },
      },
    ]);

    expect(loopStateFromEvents(events)).toEqual({
      version: 1,
      loop_id: "market-loop",
      status: "draft",
      protocol_path: ".affent/loops/market-loop/LOOP.md",
      calibration_questions: 1,
      last_calibration_question_preview: "What should pause this loop?",
    });
  });

  it("merges live loop events over stale session summary without losing stable goal metadata", () => {
    const events = normalizeEvents([
      {
        id: 1,
        type: "loop.protocol_calibration",
        data: {
          loop_id: "market-loop",
          status: "draft",
          calibration_questions: 1,
          calibration_answers: 1,
          last_calibration_answer_preview: "Pause when source evidence is weak.",
          protocol_path: ".affent/loops/market-loop/LOOP.md",
        },
      },
      {
        id: 2,
        type: "loop.protocol_activate",
        data: {
          loop_id: "market-loop",
          status: "running",
          protocol_updates: 2,
          protocol_path: ".affent/loops/market-loop/LOOP.md",
        },
      },
    ]);

    expect(mergeLoopStateFromEvents({
      version: 1,
      status: "draft",
      initial_goal_preview: "Monitor market data.",
      calibration_questions: 1,
      calibration_answers: 0,
    }, events)).toEqual({
      version: 1,
      loop_id: "market-loop",
      status: "running",
      initial_goal_preview: "Monitor market data.",
      calibration_questions: 1,
      calibration_answers: 1,
      last_calibration_answer_preview: "Pause when source evidence is weak.",
      protocol_updates: 2,
      protocol_path: ".affent/loops/market-loop/LOOP.md",
    });
  });
});
