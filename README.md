# Affent

Affent is a browser-first agent runtime for durable, auditable software work.

It turns an OpenAI-compatible model into an engineering operator that can plan a
task, inspect a workspace, use tools, change files, verify results, recover
after interruption, and explain its work from the same persistent session
record. The browser Workbench is the primary operating surface; the CLI, HTTP
API, traces, memory, plans, skills, and artifacts all read from the same
file-backed state.

## What Affent Provides

| Area | Capability |
| --- | --- |
| Workbench | Embedded React UI for sessions, timeline, tools, files, changes, run details, memory, skills, schedules, config, artifacts, and trace replay. |
| Agent runtime | Internal model/tool loop with streaming, retries, context compaction, loop guards, tool policies, structured events, and recovery checkpoints. |
| Durable state | Conversations, events, plans, loop protocol state, child transcripts, memory, runtime skills, and tool artifacts are stored as ordinary inspectable files. |
| Tool surface | Opt-in workspace, shell, memory, session search, MCP, web fetch/search, browser automation, focused-task, subagent, plan, and skill tools. |
| Integration | OpenAI-compatible `/v1/chat/completions`, Affent-native session APIs and SSE events, local CLI, Docker runtime image, and JSONL eval output. |
| Evaluation | Scenario runner and trace-derived quality gates for tool use, recovery, source evidence, memory, delegation, context compaction, and long-run behavior. |

## Quick Start

Start the full Docker runtime and embedded Workbench:

```bash
AFFENTCTL_BASE_URL=https://api.openai.com/v1 \
AFFENTCTL_API_KEY="$OPENAI_API_KEY" \
AFFENTCTL_MODEL=gpt-4o-mini \
make image-serve-up
```

Then open:

```text
http://127.0.0.1:7777
```

This builds the runtime image, starts `affentserve`, serves the embedded
Workbench, and persists session state under `.tmp/runtime-workspace/`.

Useful runtime commands:

```bash
make image-serve-status
make image-serve-logs
make image-serve-restart
make image-serve-stop
make image-serve-smoke
```

Use `AFFENTSERVE_*` variables when server-specific settings should override the
shared `AFFENTCTL_*` defaults:

```bash
AFFENTSERVE_BASE_URL=http://host.docker.internal:8000/v1 \
AFFENTSERVE_API_KEY=local \
AFFENTSERVE_MODEL=qwen3-coder \
make image-serve-up
```

See the [technical manual](docs/technical-manual.md) for direct CLI use, Docker
paths, HTTP APIs, configuration, tools, eval, and security boundaries.

## Entry Points

- `cmd/affentserve`: HTTP runtime and embedded Workbench. It exposes
  OpenAI-compatible chat completions plus Affent-native session, event,
  history, artifact, transcript, plan, loop-protocol, schedule, skill, memory,
  Workbench command, account-setting, health, model, and stats endpoints.
- `cmd/affentctl`: local CLI for one-shot runs, interactive chat, persisted
  plans, memory, MCP, tracing, session resume, executor selection, and the
  default Docker sandbox lifecycle.
- `cmd/affenteval`: scenario runner for trace-based regression testing and
  JSONL summaries.
- `extras/webui`: React Workbench used by `affentserve`.
- `extras/web` and `extras/browser`: optional web retrieval and rendered browser
  tool families.
- `internal/agent`: implementation boundary for the runtime loop. Affent's
  public contract is the product surface, not a stable Go SDK.

## Runtime Model

Affent treats session truth as operational data, not hidden process memory.
Important runtime state is persisted in the workspace or server state root:

- `conversation.jsonl` and `events.jsonl` for replayable history and trace.
- `plan.json` for persisted task plans.
- `.affent/loops/<id>/` for long-running loop protocol state.
- Topic-based memory files for project and user facts.
- Runtime skill manifests and bodies.
- Tool-result artifacts for outputs too large for model context or SSE payloads.
- Child transcripts for focused tasks and subagents.

This design lets a server restart, a browser reconnect, or an eval harness read
the same durable record instead of reconstructing state from a terminal
transcript.

## Documentation

| Document | Purpose |
| --- | --- |
| [Architecture](docs/architecture.md) | Runtime boundaries, package ownership, state model, event model, and non-goals. |
| [Technical Manual](docs/technical-manual.md) | Build, CLI, Docker, HTTP server, configuration, tools, eval, and security operations. |
| [Event Trace Contract](docs/event-trace-contract.md) | SSE/JSONL event envelope, stable event types, payload fields, and compatibility rules. |
| [Eval JSONL Contract](docs/eval-jsonl-contract.md) | `affenteval --jsonl` scenario and summary records for automation and dashboards. |
| [Focused Tasks](docs/focused-tasks.md) | Bounded typed delegation through `run_task` and its tool/output contracts. |
| [Browser Access Architecture](docs/browser-access-architecture.md) | Source-evidence model for direct web fetch, rendered browser access, and network evidence. |

## Design Posture

- Keep external contracts explicit: CLI flags, HTTP routes, config, durable
  state, SSE events, and JSONL traces.
- Keep tools opt-in and deployment-aware. Shell, browser, web, memory, MCP,
  skills, subagents, and focused tasks are registered by configuration.
- Bound growing surfaces: prompt input, tool arguments, tool results, events,
  memory, plans, retries, loops, child work, and artifacts.
- Prefer inspectable files over a required database for local development,
  debugging, backup, and recovery.
- Keep production behavior and evaluation behavior close enough that trace
  failures can become runtime fixes.

## Current Status

Affent is under active development. The focus is runtime reliability:
long-running sessions, Workbench observability, durable state, recoverable tool
use, memory, skills, focused delegation, browser/web evidence, and eval
coverage. It is not a complete security sandbox, a multi-tenant control plane,
or a general-purpose Go framework.
