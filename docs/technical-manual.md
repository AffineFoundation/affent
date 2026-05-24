# Affent Technical Manual

This manual covers the supported operating surfaces for Affent: CLI, HTTP
server, Docker images, configuration, state layout, tools, eval, and security
boundaries. Start with the root README for the project positioning and design
rationale.

Affent is not a public Go SDK. The supported integration surfaces are the CLI,
HTTP API, durable state files, and structured runtime events.

## Operating Model

Affent is built around durable agent sessions. Each session owns conversation
state, streams model output, dispatches configured tools, publishes events,
persists logs, and applies runtime limits. The CLI usually drives one session
per process; the HTTP server manages a pool of sessions.

The same runtime can be driven through:

- `affentctl`: local CLI for one-shot runs, interactive sessions, tracing,
  plans, memory, MCP, and executor selection.
- `affentserve`: HTTP service with OpenAI-compatible chat completions plus
  Affent-native session, event, artifact, transcript, and stats endpoints.
- `affenteval`: scenario runner that checks runtime behavior through traces.

Tool capabilities are opt-in. Shell, file, memory, session search, MCP, web,
browser, subagent, focused-task, and skill tools are registered by configuration
instead of assumed globally.

## Build And Check

Build the CLI through Docker:

```bash
make affentctl
```

Build with the host Go toolchain only when that is intentional:

```bash
make affentctl-local
```

Check local configuration without calling a model:

```bash
./bin/affentctl doctor \
  --workspace ./workspace \
  --base-url https://api.openai.com/v1 \
  --api-key "$OPENAI_API_KEY" \
  --model gpt-4o-mini
```

`doctor` reports the resolved tool surface, executor class, MCP tool filters,
memory settings, project-context settings, retry/timeout settings, and runtime
boundary caps.

## CLI Usage

Run a single prompt:

```bash
./bin/affentctl run \
  --workspace ./workspace \
  --base-url https://api.openai.com/v1 \
  --api-key "$OPENAI_API_KEY" \
  --model gpt-4o-mini \
  --prompt "Inspect this workspace and summarize the project."
```

Read the prompt from stdin or a file:

```bash
./bin/affentctl run --prompt -
./bin/affentctl run --prompt @request.txt
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

Use a stable session id when another command must resume the same state:

```bash
./bin/affentctl run --workspace ./workspace --session-id migration-1 \
  --model gpt-4o-mini \
  --prompt "Inspect the migration plan."
```

### Plans

For work that needs review before execution, create or update a persisted plan
first:

```bash
./bin/affentctl run --workspace ./workspace --session-id migration-1 \
  --model gpt-4o-mini --plan-only \
  --prompt "Plan the config migration."
```

After review, execute the unfinished plan:

```bash
./bin/affentctl run --workspace ./workspace --session-id migration-1 \
  --model gpt-4o-mini --execute-plan
```

Inside `affentctl chat`, use `/plan draft <request>`, `/plan execute [note]`,
`/plan`, and `/plan clear`.

## Docker Paths

The default Docker path keeps Go builds and test runs inside a memory-limited
container while preserving build caches under `.tmp/`.

Common targets:

```bash
make affentctl
make sandbox-start
make image-run IMAGE_COMMAND='affentctl --help'
make image-serve-up
make image-serve-smoke
make eval-container EVAL_ARGS='--list'
make eval-serve-browser-container
make test-container TEST_PACKAGES=./internal/agent
make test-container TEST_DIR=cmd/affentserve TEST_PACKAGES=./...
```

Default container limits are `1g` memory, `2` CPUs, and `512` PIDs where the
target supports all three. Go runtime limits are derived from cgroups so Go
builds and tests respect the same resource envelope.

### Sandbox Executor

Start or reuse the default persistent tool sandbox:

```bash
./bin/affentctl sandbox start
```

Use it for a run:

```bash
./bin/affentctl run \
  --executor sandbox \
  --base-url https://api.openai.com/v1 \
  --api-key "$OPENAI_API_KEY" \
  --model gpt-4o-mini \
  --prompt "Inspect the workspace and report what files exist."
```

Inspect or stop the sandbox:

```bash
./bin/affentctl sandbox status
./bin/affentctl sandbox stop
./bin/affentctl sandbox stop --remove
```

The default sandbox mounts a durable workspace under
`$XDG_DATA_HOME/affent/sandbox/workspace`, or
`~/.local/share/affent/sandbox/workspace`, with a local fallback under
`./affent/sandbox/workspace` when no usable home directory exists. It runs as
the current host UID/GID by default so generated files remain editable.

### Runtime Image

Build the full runtime image:

```bash
./bin/affentctl image build --image affinefoundation/affent:local
```

Run a command inside it:

```bash
AFFENTCTL_BASE_URL=https://api.openai.com/v1 \
AFFENTCTL_API_KEY="$OPENAI_API_KEY" \
AFFENTCTL_MODEL=gpt-4o-mini \
./bin/affentctl image run --image affinefoundation/affent:local -- \
  affentctl run --executor local --prompt "Inspect the workspace."
```

`image run` mounts a persistent `/workspace`, forwards portable model/auth
environment variables, and intentionally does not forward host-path variables
such as `AFFENTCTL_WORKSPACE` or `AFFENTSERVE_WORKSPACE_ROOT` unless passed
explicitly with `--env`.

`make image-serve-up` and `make image-serve-restart` run `affentserve` in the
runtime image with durable session state under `/workspace/session-state`. That
path lives inside `IMAGE_WORKSPACE`, so the server preserves conversation history as long as `IMAGE_WORKSPACE` is the same host path. Deleting a session with
`DELETE /v1/sessions/{id}` intentionally removes that durable state.

Use `make image-serve-smoke` for a local persistence check; it creates a
session, restarts the runtime, and verifies the durable session is still listed.

## HTTP Server

Build the server:

```bash
cd cmd/affentserve
go build -o ../../bin/affentserve .
```

Run it directly:

```bash
AFFENTSERVE_BASE_URL=https://api.openai.com/v1 \
AFFENTSERVE_API_KEY="$OPENAI_API_KEY" \
AFFENTSERVE_MODEL=gpt-4o-mini \
./bin/affentserve --listen 127.0.0.1:7777
```

Or start the production-style image path:

```bash
make image-serve-up
```

Test the chat endpoint:

```bash
curl -sS http://127.0.0.1:7777/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}' \
  | jq '{content: .choices[0].message.content, session_id: .affent_session_id}'
```

`affent_session_id` pins follow-up turns. Pass it back through
`X-Affent-Session-Id`, `affent_session_id`, or `session_id`.

### HTTP Endpoints

OpenAI-compatible endpoints:

- `POST /v1/chat/completions`
- `GET /v1/models`

Operational endpoints:

- `GET /healthz`
- `GET /v1/stats`

Session endpoints:

- `GET /v1/sessions`
- `POST /v1/sessions`
- `GET /v1/sessions/{id}`
- `DELETE /v1/sessions/{id}`
- `GET /v1/sessions/{id}/events`
- `GET /v1/sessions/{id}/history`
- `GET /v1/sessions/{id}/plan`
- `DELETE /v1/sessions/{id}/plan`
- `GET /v1/sessions/{id}/tools`
- `GET /v1/sessions/{id}/transcripts`
- `GET /v1/sessions/{id}/transcripts/{path}`
- `GET /v1/sessions/{id}/artifacts`
- `GET /v1/sessions/{id}/artifacts/{path}`
- `POST /v1/sessions/{id}/messages`
- `POST /v1/sessions/{id}/cancel`

Use `GET /v1/sessions/{id}/events` for live SSE. Reconnect with
`Last-Event-ID` to replay persisted events before live events continue. Use
`GET /v1/sessions/{id}/history?after=-1&limit=100` for paged replay from the
durable event log.

## Configuration

Affent resolves configuration from CLI flags, environment variables, and JSON
config files. CLI flags override environment variables; environment variables
override config files; config files override built-in defaults.

Both `affentctl` and `affentserve` load `.env` files from the current directory
and from:

```text
~/.config/affent/.env
```

Common CLI variables:

```text
AFFENTCTL_BASE_URL
AFFENTCTL_API_KEY
AFFENTCTL_MODEL
AFFENTCTL_WORKSPACE
AFFENTCTL_CONFIG
AFFENTCTL_MCP_CONFIG
AFFENTCTL_EXECUTOR
AFFENTCTL_MEMORY
AFFENTCTL_SUBAGENT
AFFENTCTL_SUBAGENT_MAX_DEPTH
AFFENTCTL_FOCUSED_TASKS
```

Common server variables:

```text
AFFENTSERVE_BASE_URL
AFFENTSERVE_API_KEY
AFFENTSERVE_MODEL
AFFENTSERVE_AUTH_TOKEN
AFFENTSERVE_WORKSPACE_ROOT
AFFENTSERVE_MEMORY_ROOT
AFFENTSERVE_BROWSER
AFFENTSERVE_BROWSER_SCREENSHOT
AFFENTSERVE_WEB
AFFENTSERVE_WEB_SEARCH
AFFENTSERVE_MEMORY
AFFENTSERVE_BUILTINS
AFFENTSERVE_SUBAGENT
AFFENTSERVE_SUBAGENT_MAX_DEPTH
AFFENTSERVE_FOCUSED_TASKS
AFFENTSERVE_SESSION_RETENTION
AFFENTSERVE_TEMPERATURE
AFFENTSERVE_TOP_P
AFFENTSERVE_MAX_TOKENS
TAVILY_API_KEY
```

Example CLI config:

```json
{
  "workspace": "./workspace",
  "base_url": "https://api.openai.com/v1",
  "model": "gpt-4o-mini",
  "memory": {
    "enabled": true,
    "dir": ".affent/memory",
    "max_chars": "2200,1375",
    "topic_max_chars": 4400,
    "max_topics": 32
  }
}
```

Example MCP config:

```json
{
  "servers": [
    {
      "name": "AMap",
      "command": "python",
      "args": ["amap_server.py"],
      "init_timeout": "30s",
      "allow_tools": ["poi_search", "route_plan"]
    }
  ]
}
```

MCP tool names are namespaced by default. Use `allow_tools` or `deny_tools` to
keep the model-facing tool surface narrow. Unknown, duplicate, overlapping, or
empty filters are rejected so a typo does not silently widen access.

## State Layout

Affent stores durable state as inspectable files:

- `conversation.jsonl`: conversation records used to resume sessions.
- `events.jsonl`: runtime event records for replay and SSE recovery.
- `plan.json`: persisted plan state.
- Runtime skill files: installed skill bodies and manifests.
- Memory files: topic-bucketed workspace or user memory.
- Transcript files: child-task and subagent conversations.
- Artifact files: full tool outputs when event payloads are capped.

`affentctl` stores session state under the configured workspace by default.
`affentserve` stores per-session durable state under its session state root so
state survives container restart when backed by a host volume.

Session search and persistent memory are separate features:

- Session search recalls snippets from previous conversation logs.
- Memory stores facts that should survive across sessions.

## Tools And Capabilities

Built-in workspace tools include file operations and shell execution. File tools
are scoped to the configured workspace. Shell execution always goes through an
executor boundary.

Optional capabilities:

- Memory: topic-bucketed persistent facts with read/search/update tools.
- Session search: retrieval over prior workspace transcripts.
- MCP: stdio or streamable HTTP tool registration.
- Web: fetch and optional search.
- Browser: real browser automation.
- Skills: runtime-installable workflow instructions.
- Subagent: bounded isolated child runtime for exploration or review.
- Focused tasks: typed delegation surface for recall, explore, research,
  verify, and review tasks.

Runtime skill installs use a proposal/confirmation path for remote candidates.
Direct install is reserved for an exact skill body the user already supplied.

Focused tasks and subagents return structured reports to the parent session
without injecting their full intermediate work into the parent conversation.
They are bounded by task size, turn count, depth, output caps, and read-only
tool policies where applicable.

## Evaluation

List built-in scenarios:

```bash
go run ./cmd/affenteval --list
go run ./cmd/affenteval --list-suites
```

Run scenarios:

```bash
go run ./cmd/affenteval --suite small-model-tools --temperature 0
go run ./cmd/affenteval --scenario coding-python-slug --temperature 0
go run ./cmd/affenteval --suite small-model-tools --jsonl > eval.jsonl
```

Run through Docker:

```bash
make eval-container EVAL_ARGS='--suite small-model-tools --temperature 0'
make eval-agent-container EVAL_ARGS='--scenario coding-python-slug --temperature 0'
make eval-agent-container EVAL_RUNTIME_MEMORY=true EVAL_ARGS='--scenario your-memory-scenario --temperature 0'
make eval-agent-container EVAL_RUNTIME_MCP_CONFIG=/workspace/config/mcp.json EVAL_ARGS='--scenario your-mcp-scenario --temperature 0'
```

Use runtime eval mode when Affent itself is the benchmark agent and the model
should not receive extra product affordances. In eval mode, dynamic workflow
features such as skills, runtime skill install, subagents, focused tasks, MCP,
project context, session search, and memory are disabled by default; shell/file
tools remain available when built-ins are available. Opt memory or MCP back in
only for suites that explicitly measure those capabilities. The eval container
does not forward host `AFFENTCTL_EVAL_MODE`, `AFFENTCTL_SUBAGENT`,
`AFFENTCTL_FOCUSED_TASKS`, or `AFFENTCTL_PROJECT_CONTEXT`; use the
`EVAL_RUNTIME_*` knobs above, or pass deliberate extra environment through
`EVAL_DOCKER_ARGS`.

For external OpenAI-compatible eval harnesses, run `affentserve` in eval mode:

```bash
make eval-serve-container
make eval-serve-browser-container
```

Use `SERVE_EVAL_PERMISSIONS` to opt specific environment capabilities back in,
for example `SERVE_EVAL_PERMISSIONS='web-search memory'`.

The JSONL output contract is documented in
[eval-jsonl-contract.md](eval-jsonl-contract.md). Runtime traces are documented
in [event-trace-contract.md](event-trace-contract.md).

## Observability

Affent emits structured events for turn starts, user messages, assistant
deltas, reasoning deltas, tool requests, tool results, usage, errors, and turn
endings. Tool request/result events include repair, truncation, artifact, and
delegation metadata where relevant.

Trace JSONL files start with a `trace.meta` record carrying the schema version.
See [event-trace-contract.md](event-trace-contract.md) for the event envelope
and compatibility rules.

## Security Model

Affent is an agent runtime, not a complete security boundary.

- File tools are path-scoped to the configured workspace.
- Shell tools run through an executor boundary.
- Docker sandbox mode is useful for local isolation but is not a hardened
  multi-tenant sandbox by itself.
- Browser, web, MCP, memory, skills, and built-in tools should be enabled only
  when the deployment needs them.
- Gate `affentserve` with `--auth-token` or an upstream proxy unless it is on a
  trusted network.
- Use isolated processes or state roots when tenants need distinct credentials
  or data boundaries.

Production deployments that let untrusted users or model outputs drive tools
should provide an isolation boundary outside Affent: a hardened container, VM,
remote execution service, or comparable sandbox.

## Development

Run the root module tests:

```bash
go test ./...
```

Run nested module tests separately:

```bash
(cd cmd/affentserve && go test ./...)
(cd extras/web && go test ./...)
(cd extras/browser && go test ./...)
```

Run the containerized root suite:

```bash
make test-container TEST_PACKAGES=./...
```

Browser smoke tests require a local Chromium binary and are guarded by the
`browser_smoke` build tag.
