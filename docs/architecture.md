# Affent Architecture

Affent is organized around product entry points rather than a public Go SDK.
The supported external surfaces are:

- `affentctl`
- `affentserve`
- `affenteval`
- configuration files and environment variables
- durable state directories
- OpenAI-compatible HTTP request/response shapes
- Affent-native SSE events and JSONL traces

`internal/agent.Loop` is an implementation boundary used by those product
entry points. It is not a stable integration contract for downstream Go
programs.

## System View

Affent has one runtime core and three product drivers:

- `affentctl` runs a single local session for one-shot prompts, interactive
  chat, traces, plans, memory, MCP, and executor selection.
- `affentserve` manages a pool of durable sessions behind HTTP APIs and the
  embedded Workbench.
- `affenteval` runs scenario suites against the same runtime surfaces and
  scores the resulting traces.

The common path is:

1. A user, API client, schedule, or eval sends a turn to a session.
2. The session appends the user message to durable conversation state.
3. `internal/agent.Loop` streams an OpenAI-compatible chat completion.
4. Model tool calls are validated, repaired when possible, governed by runtime
   policies, dispatched through the configured registry, and appended back to
   the conversation with bounded context.
5. The loop publishes structured events for every important boundary:
   user message, runtime surface, assistant output, tool request/result, usage,
   compaction, loop protocol feed, decision, error, and turn end.
6. CLI traces, server SSE streams, the Workbench, evals, and session summaries
   consume the same event shape.

This is the central architectural rule: session state and event records are the
source of truth. UI views, OpenAI-compatible responses, summaries, and eval
metrics are derived projections.

## Runtime Boundary

The agent runtime lives in `internal/agent`. It owns the session execution
cycle:

- LLM streaming and retry handling.
- Tool registry and dispatch.
- Conversation persistence.
- Structured event publication.
- Context compaction.
- Memory wiring.
- Project-context loading.
- Session search wiring.
- Plan, skill, subagent, and focused-task runtime tools.
- Loop protocol injection, calibration, and turn checkpointing.
- Runtime caps and loop guards.
- Pre-dispatch, post-result, and first-tool policies that gate tool calls
  without coupling the loop to feature-specific logic.

The runtime is intentionally tool-agnostic at its core. Tool families are
registered by callers according to configuration and deployment posture.

## Tool Call Governance

The runtime exposes three independent policy types that callers can attach
to a `Loop` or to a single turn:

- `FirstToolPolicy`: forces a named tool to be the first tool call when a
  trigger condition matches the user text. Typical use: ensure delegation
  surfaces are reached before parent-side exploration.
- `PostToolPolicy`: blocks selected follow-up tools after a named tool
  returns. Supports two sub-rules — "block these tools whenever the named
  tool has run this turn" and "block these tools only when the named tool's
  result activates the policy". Used to keep delegated reports authoritative
  instead of re-running the same evidence gathering parent-side.
- `ToolCallPolicy`: pre-dispatch rejection with full access to the call
  arguments, user text, and current tool-call count. Cannot rewrite
  arguments; can reject with a structured reason. Used by features that
  must veto an otherwise valid call before the executor sees it.

Every rejection is tagged with a stable failure kind
(`tool_policy_first_tool`, `tool_policy_repeat`, `tool_policy_active`,
`tool_policy_rejected`) so loop guards, eval checks, and trace consumers can
detect it without parsing prose.

## Supporting Packages

Supporting packages sit behind narrower boundaries:

- `internal/executor`: local and Docker execution backends plus file
  operations.
- `internal/memory`: file-backed persistent memory store with target/topic
  layering, BM25-plus-recency search, atomic writes, and `flock` exclusion.
- `internal/sessionsearch`: cross-session transcript retrieval and ranking.
- `internal/projectcontext`: project instruction file loading
  (AGENTS.md, CONVENTIONS.md, .cursorrules, .clinerules, CLAUDE.md, GEMINI.md).
- `internal/mcp`: MCP client and tool registration, with allow/deny lists,
  initialization timeout, response capping, namespace control, and schema
  normalization.
- `internal/sse`: event contract shared by CLI and HTTP surfaces, including
  the `TraceSchemaVersion` constant and every published payload struct.
- `internal/eventlog`: JSONL event persistence.
- `internal/planstate`: persisted plan state with bounded summary.
- `internal/loopstate`: per-session loop protocol state, sidecar events
  log, and protocol-feed/turn-checkpoint/memory-update/decision recording.
- `internal/sourceaccess`: normalized evidence-quality headers shared by
  web fetch, browser, and search tools.
- `internal/toolfailure`: structured `Failure: kind=` extraction shared by
  the loop, loop guard, eval checks, and trace consumers.
- `internal/toolrepair`: classification of repair notes into stable buckets
  (tool name, malformed JSON, wrapper unwrap, scalar wrap, alias rename,
  enum normalization, type coercion, unknown field drop).
- `internal/websource`: URL/host normalization and direct-reader trap
  classification.
- `internal/netguard`: SSRF guard for outbound network calls.
- `internal/workspaceignore`: shared ignore-rule handling for workspace
  scoped tools.
- `internal/textutil`: shared UTF-8-safe truncation helpers.
- `internal/metrictext`: shared bounded text rendering for metrics.
- `internal/jsonl`: shared JSONL reader.
- `internal/gosymbols`: Go symbol parsing for the `symbol_context` tool.
- `internal/agenteval`: scenario runner, requirement checks, batch
  reporting, and debug recovery guides.
- `internal/e2e`: cross-package end-to-end coverage.
- `internal/architecture`: CI-enforced layering test. The internal/*
  dependency graph is declared as data and verified by `go test`; drift
  fails the build.

Optional tool families live outside the core runtime:

- `extras/web`: web fetch and search tools.
- `extras/browser`: browser automation tools.
- `extras/webui`: React-based Workbench used by `affentserve` for default
  chat plus on-demand surfaces (Context, Workspace, Changes, Files, Run,
  Memory, Skills, Automation, Config, Trace).

## Entry Points

`cmd/affentctl` is the local CLI. It drives one-shot runs, interactive sessions,
session resume, tracing, plans, memory, MCP, executor selection, and the
default sandbox lifecycle.

`cmd/affentserve` is the HTTP runtime. It exposes OpenAI-compatible chat
completions plus native session lifecycle, event, history, tool, artifact,
transcript, schedule, loop-protocol, skill, memory, Workbench command,
account-settings, health, model, and stats endpoints.

`cmd/affenteval` is the scenario runner. It executes bounded agent tasks,
collects runtime traces, and emits text or JSONL summaries for comparison.

The HTTP server intentionally lives in its own command module
(`cmd/affentserve/go.mod`) while path-replacing the root module during in-tree
development. That keeps server-only dependencies and embed behavior separate
from the root CLI/runtime build without making `internal/agent` a public SDK.

## State Model

Affent treats state as inspectable operational data, not as hidden process
memory. Durable state is stored as ordinary files:

- Conversations and events use JSONL. In `affentserve`, `events.jsonl` line
  numbers are also the durable SSE reconnect cursors.
- Plans use JSON (`.affentctl/<session>.plan.json` for CLI; durable session
  trees for server).
- Memory uses bounded topic files under target directories.
- Loop protocol uses three files per loop under `.affent/loops/<loop_id>/`:
  the human-readable `LOOP.md`, the runtime authoritative `state.json`, and
  an append-only `events.jsonl` sidecar.
- `affentserve` keeps project/task memory per session by default; local
  single-user deployments can opt into shared `target=user` memory for stable
  preferences across sessions.
- Runtime skills are persisted as manifests and skill bodies; user-installed
  skills go through a propose/confirm/install lifecycle with atomic publish.
- Oversized tool outputs are stored as artifacts under a configurable prefix
  and referenced through `tool.result.result_artifact_path`.
- Child delegation work (focused tasks and subagents) is stored as
  transcripts under the parent session tree.

This keeps local debugging simple and lets the HTTP server survive container
restart when the state root is backed by a host volume.

In server mode, the configured workspace root is the user/project-owned file
space where work begins. It is not owned by an individual session and is not
removed when a session closes. Each session also has an active workspace,
initially the root, that can move into an existing project directory through the
runtime-owned `session_workspace` tool. Shell, file, repo search, symbol search,
focused-task, and subagent surfaces read that active workspace dynamically, so a
session can clone or create a project once and then operate from that project
without repeating long absolute paths.

Each session separately has a stable session directory for durable identity:
conversation log, event log, memory, plan, loop protocol state, runtime skills,
child transcripts, and artifacts. LRU eviction may drop an in-memory session,
but the stable session directory lets the same session id be reopened while the
workspace remains the shared engineering surface.

## Event Model

The event stream is the main observability contract. Both CLI traces and server
SSE streams use the same event shape for turn boundaries, assistant output,
reasoning output, tool requests, tool results, usage, errors, delegation
metadata, runtime surface, context injection, context compaction, and the
loop-protocol family (feed, calibration, calibration-request, decision).

Event consumers should rely on the documented event envelope and schema version,
not on internal runtime structs. See [event-trace-contract.md](event-trace-contract.md).

## Design Rules

- Keep product interfaces explicit: CLI flags, server config, HTTP routes,
  state layout, and event contracts matter more than preserving a root package.
- Keep root clean: implementation belongs in packages with clear ownership.
- Prefer internal packages until there is a concrete external consumer that
  justifies a stable Go API.
- Keep tools opt-in. Shell, file, browser, web, memory, MCP, skills, subagents,
  and focused tasks are registered by configuration.
- Bound every runtime surface that can grow: prompt input, tool arguments, tool
  results, events, memory, plans, child tasks, retries, and loops.
- Treat storage as recoverable. Operators should be able to inspect, replay,
  back up, or remove durable state without a database service.
- Keep eval and production paths close enough that trace-level lessons from one
  can improve the other.
- Encode dependency layering as data and verify it with a test, not with
  reviewer vigilance. `internal/architecture` is the authoritative graph.

## Non-Goals

- Affent is not a complete security sandbox. Tool execution still needs an
  external isolation boundary for untrusted workloads.
- Affent is not a multi-tenant control plane. Use isolated processes, state
  roots, credentials, and network boundaries when tenants differ.
- Affent is not a general-purpose Go framework. Its public contract is the
  product surface, not internal packages.
- Affent is not an open-ended multi-agent orchestration system. Focused tasks
  and subagents are bounded delegation surfaces with finite types, capped
  budgets, and structured returns; they are not a planner/executor mesh.
