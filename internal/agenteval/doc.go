// Package agenteval is Affent's internal experiment-driven capability
// loop. It runs scenarios — concrete bounded tasks the agent should
// solve — against a real agent.Loop, captures the resulting event
// stream into a Trace, and applies trace-quality Checks to that
// Trace to surface pass/fail and detailed diagnostics.
//
// The point is NOT a public eval framework. It is an internal
// substrate that turns "did the agent answer correctly" into
// "did the agent reproduce before editing, refuse broad scans,
// avoid editing tests, and run the final verification command".
// Every mechanism change (a new guard, a new skill clause, a new
// tool schema constraint) is supposed to be diffable through this
// rig so the team can see whether the change moved the needle on
// trace quality, not just final-answer accuracy.
//
// Scope on purpose:
//
//   - Go-literal scenarios first. Declarative file formats can come
//     later, after the shape is stable.
//   - The Runner only knows how to drive a single Loop and drain its
//     events. Parent/child / multi-agent eval can layer on top
//     without changing this package.
//   - BatchRunner shells through affentctl for black-box real-model
//     regression checks that should leave full JSONL traces on disk.
//   - Checks operate on a frozen Trace, not on the live event channel.
//     This decouples "what we measure" from "how we run" and keeps
//     checks deterministic across reruns.
//
// What lives here:
//
//	types.go    — Scenario, Check, CheckResult, Trace, ToolCall, Outcome
//	runner.go   — Runner.Run(scenario): orchestrates one full evaluation
//	checks.go   — The small starter library of generic checks
//	eval.go     — BatchRunner: affentctl-driven scenarios with trace files
package agenteval
