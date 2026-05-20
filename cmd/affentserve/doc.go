// Command affentserve runs affent behind an HTTP API.
//
// The server speaks OpenAI-compatible chat completions on
// /v1/chat/completions so any OpenAI SDK / generic eval harness can
// drive it. A second SSE endpoint, /v1/sessions/{id}/events, exposes
// affent's native 13-type event stream for clients that want richer
// trace data (reasoning, tool requests, tool results, etc.) than the
// OpenAI delta protocol carries.
//
// Why a separate binary
//
//   - cmd/affentctl is shaped for one-shot prompts + interactive
//     REPL. The server has different lifecycle needs: per-session
//     state across requests, idle GC, graceful shutdown of long-
//     lived browser instances. Mixing the two would muddy both.
//   - affent's design ethos keeps the core library tiny. The server
//     and the extras it wires (browser, web, mcp) all opt in via
//     this command's flags.
//
// What this binary is NOT
//
//   - Not multi-tenant. One DASHSCOPE_API_KEY / one Tavily key per
//     process. Use a reverse proxy + isolated affentserve instances
//     if you need per-tenant credentials.
//   - Not horizontally scalable on its own — sessions are
//     in-process. Stick a sticky load balancer in front and you can
//     scale out, but the server itself doesn't try.
//   - Not an evaluation harness. Eval-specific concerns (question
//     templating, GT collection, scoring) live in the consumer
//     (e.g. liveweb-arena's Python EvalHarness).
package main
