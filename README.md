# Affent

Affent is an OpenAI-compatible agent runtime for practical tool-using sessions.
It can run as a CLI for local work, as an HTTP service for clients that already
speak the OpenAI API shape, and with optional tool families for web, browser,
memory, files, shell, and MCP enabled as needed.

Affent focuses on the runtime layer of an agent: model streaming, tool
execution, conversation state, cancellation, retries, context management,
durable memory, session recall, MCP integration, and structured event output.

## Why Affent

Modern agent products need more than a chat-completions wrapper. They need a
runtime that can keep a session alive, execute tools safely within a workspace,
recover from provider and network failures, keep long conversations usable, and
expose enough event data for a UI, trace, or evaluation harness to understand
what happened.

Affent is built around those concerns. It keeps the model-facing loop explicit,
the tool surface configurable, and the persistent state inspectable on disk.

## Capabilities

- OpenAI-compatible streaming model calls, including providers that expose a
  separate reasoning channel.
- Tool execution with per-turn budgets, cancellation, transient retry, stream
  watchdogs, and bounded tool output in model context.
- Workspace tools for shell commands and file operations.
- Executor abstraction for local execution, Docker attach mode, or a custom
  sandbox.
- JSONL conversation persistence with resumable sessions.
- Context compaction for long-running conversations.
- Project-context loading from common instruction files such as `AGENTS.md`.
- Topic-bucketed persistent memory with search.
- Session search over prior workspace transcripts.
- MCP stdio and streamable HTTP tool registration.
- Structured SSE events for UI rendering, tracing, replay, and evaluation.
- Optional web fetch/search and real browser automation packages.

## Run Modes

### CLI

`affentctl` is the local and batch driver. It supports one-shot runs,
interactive sessions, session resume, JSONL tracing, project context, memory,
MCP, and local or Docker-backed tool execution.

### HTTP Server

`affentserve` exposes Affent through an OpenAI-compatible HTTP surface. It is
useful for frontends, SDK-based clients, eval systems, and service-style
deployments that want session pinning and access to Affent's native event
stream.

## Quick Start

Build the CLI and run a one-shot task:

```bash
go build -o ./bin/affentctl ./cmd/affentctl

./bin/affentctl run \
  --workspace ./workspace \
  --base-url https://api.openai.com/v1 \
  --api-key "$OPENAI_API_KEY" \
  --model gpt-4o-mini \
  --prompt "Inspect this workspace and summarize the project."
```

Start an interactive session:

```bash
./bin/affentctl chat \
  --workspace ./workspace \
  --base-url https://api.openai.com/v1 \
  --api-key "$OPENAI_API_KEY" \
  --model gpt-4o-mini
```

Resume the latest session in a workspace:

```bash
./bin/affentctl chat --workspace ./workspace --continue
```

Build the HTTP server:

```bash
cd cmd/affentserve
go build -o ../../bin/affentserve .
```

## Configuration

Affent uses CLI flags, JSON config files, and environment variables. Both
`affentctl` and `affentserve` load `.env` files from the current directory and
from:

```text
~/.config/affent/.env
```

The most commonly used variables are:

```text
AFFENTCTL_BASE_URL
AFFENTCTL_API_KEY
AFFENTCTL_MODEL
AFFENTCTL_CONFIG
AFFENTCTL_MCP_CONFIG
AFFENTCTL_EXECUTOR
```

Common server variables:

```text
AFFENTSERVE_BASE_URL
AFFENTSERVE_API_KEY
AFFENTSERVE_AUTH_TOKEN
AFFENTSERVE_WORKSPACE_ROOT
AFFENTSERVE_MEMORY_ROOT
AFFENTSERVE_SESSION_RETENTION
TAVILY_API_KEY
```

## State And Memory

Affent keeps session state in JSONL conversation logs so runs can be resumed,
replayed, or inspected. `affentctl` stores logs under the workspace by default.
`affentserve` keeps per-session state in its session pool and exposes the active
session id to clients.

Persistent memory is opt-in. Workspace memory is topic-bucketed and can be
searched on demand. User memory is a separate cross-workspace profile. The
system prompt receives only compact always-relevant memory plus an index of
retrievable topics, so memory can grow without turning the prompt into a
database dump.

Session search and persistent memory serve different roles:

- Session search recalls prior conversation snippets.
- Memory stores durable facts that should survive across sessions.

## Tools And Integrations

Affent ships with shell and file tools, optional memory and session-search
tools, MCP registration, and optional web/browser packages. Tool availability is
chosen by the runtime configuration rather than assumed globally.

File tools are scoped to the configured workspace. Shell execution goes through
an executor boundary. Production deployments should run tools inside a real
sandbox such as a container, VM, or remote execution environment.

MCP servers can be registered over stdio or streamable HTTP. Their tools are
namespaced and become part of the same registry as Affent's built-in tools.

## Subagent

The optional `subagent_run` tool lets the main agent hand a bounded
exploration or review task to a fresh isolated Loop with its own
conversation, its own (read-only) tool set, and a step budget. Only the
structured report comes back; the child's individual tool calls,
file reads, and reasoning never enter the parent's conversation.

Use it for tasks that would otherwise pollute the main context with
noise — codebase exploration, multi-file inspection, log triage, code
review pre-pass.

Design contract enforced in code (`internal/agent/subagent.go` +
`subagent_test.go`):

- No `write_file` / `edit_file` in the child's registry.
- No `subagent_run` in the child's registry — recursion is impossible.
- Optional deps gate their tools: no Executor → no shell, no Memory →
  no memory tool, no SessionsDir → no session_search.
- The child has its own conversation file under `TranscriptDir`; the
  parent's conversation only ever sees the structured response.
- The shell tool inside a subagent is wrapped to reject mutating
  commands (rm/mv/sed -i/git checkout/pip install/output redirection)
  and reads into the audit-transcript path.
- The memory tool inside a subagent is wrapped to allow only
  `search` / `list` actions.
- The structured response leads with `report` so the parent Loop's
  8 KiB tool-result truncation never decapitates the conclusion.
- Per-child token usage (`input_tokens` / `output_tokens`) and any
  recoverable LLM-side errors the child fought through are surfaced
  back to the parent for budget / debug visibility.

CLI registers it automatically when `--memory-only` is off; HTTP
server registers it through the same path on each new session. Pass
`mode` (`explore` or `review`) and `task` to invoke; `max_turns`
defaults to 6 with a hard cap of 12.

## Events And Observability

Affent emits a structured SSE event stream covering turn boundaries, model
output, reasoning output when available, tool requests, tool results, usage,
and errors. The same event model supports CLI traces, HTTP clients, UIs, and
evaluation harnesses.

Trace output can include token-level deltas for replay or omit them for smaller
batch-evaluation artifacts.

## Architecture

Affent's external product surfaces are the CLI, HTTP server, configuration,
state layout, and event contracts. The repository root is a project doorway,
not an importable Go package. Architecture notes live in
`docs/architecture.md`.

## HTTP API

`affentserve` exposes OpenAI-compatible chat completions, model listing,
health/stats endpoints, session lifecycle operations, and a native session
event stream. Clients can pin a session through `X-Affent-Session-Id`,
`affent_session_id`, or `session_id`.

## Security Model

Affent is an agent runtime, not a security sandbox.

Workspace path checks, output caps, binary-file refusal, and executor
abstractions are defense-in-depth measures. They do not replace process,
filesystem, or network isolation. If untrusted users or model outputs can drive
tools, the deployment must provide the isolation boundary.

Recommended production practices:

- Run tool execution in an isolated container, VM, or remote sandbox.
- Do not enable shell/file built-ins on a shared host without isolation.
- Treat browser and web tools as network-capable capabilities.
- Gate `affentserve` with `--auth-token` or an upstream proxy.
- Keep memory scoped to the user, workspace, or session that owns it.

## Development

Run the root module tests:

```bash
go test ./...
```

Run submodule tests separately:

```bash
cd extras/web && go test ./...
cd ../browser && go test ./...
cd ../../cmd/affentserve && go test ./...
```

Browser smoke tests are behind the `browser_smoke` build tag because they need a
local Chromium binary.
