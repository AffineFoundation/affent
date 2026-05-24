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

Build the CLI in a memory-limited Docker container:

```bash
make affentctl
```

Common make shortcuts wrap the same Docker/runtime commands:

```bash
make affentctl-local
make sandbox-start
make image-run IMAGE_COMMAND='affentctl --help'
make image-serve-up
make eval-container EVAL_ARGS='--list'
make test-container TEST_PACKAGES=./internal/agent
make test-container TEST_DIR=cmd/affentserve TEST_PACKAGES=./...
```

`make affentctl` and `make test-container` run Go inside Docker with a `1g`
memory limit by default and keep Go build/module caches under `.tmp/`. Their Go
runtime limits are derived from the container cgroup limits, so changing
`CONTAINER_MEMORY` or `CONTAINER_CPUS` does not require a second matching Go
knob. Use `make affentctl-local` only when you explicitly want the host Go
toolchain. Set `TEST_DIR` for nested modules such as `cmd/affentserve` or
`extras/web`. The other targets use `affentctl` so the same default persistent
workspaces, image tags, and resource limits apply.

`make image-serve-up` is the one-command local server path: it builds the
runtime image if needed, starts `affentserve` in a named Docker container,
mounts the persistent runtime workspace, waits for `/healthz`, and uses the
default `1g` memory / `2` CPU / `512` PID limits. The default health URL follows
common explicit `SERVE_PUBLISH` mappings such as `7777:7777` and
`127.0.0.1:8888:7777`; set `SERVE_HEALTH_URL` for random or nonstandard Docker
publish specs. If an existing container was created with a different persistent
workspace, port publishing, affentserve workspace/memory root, listen address,
or resource limits, Affent refuses to silently reuse it; run
`make image-serve-restart` to recreate the container with the requested
workspace and limits and wait for `/healthz`.

`make eval-container` builds the full Affent runtime image, runs the checkout's
`cmd/affenteval` inside it with the same Docker memory/CPU defaults, mounts the
checkout at `/workspace`, stores scenario workspaces under `/workspace/.tmp/eval`,
and keeps runtime HOME/caches under `/workspace/.tmp/eval-container`. It
defaults to `EVAL_ARGS='--list'` so it does not call a model unless you request
scenarios explicitly.

The equivalent host build, when you intentionally want to bypass Docker, is:

```bash
go build -o ./bin/affentctl ./cmd/affentctl
```

Check local setup without calling a model:

```bash
./bin/affentctl doctor \
  --workspace ./workspace \
  --base-url https://api.openai.com/v1 \
  --api-key "$OPENAI_API_KEY" \
  --model gpt-4o-mini
```

When `--mcp-config` is set, `doctor` also initializes each configured MCP
server and calls `tools/list` within the configured `init_timeout` budget. The
MCP line reports raw tool count, filtered tool count, final advertised tool
names, namespace mode, and filtered tool reasons so a workflow-specific
allowlist/denylist can be checked before a model sees the tools.
`doctor` also prints the active runtime boundary caps, including prompt/config
input limits, LLM request and stream accumulator caps, tool request/result event
caps, tool result context cap, and MCP result cap. Its capability line
summarizes the tool surface the resolved config will expose, including
shell/file tools, skill install, memory, session search, MCP, subagent,
focused tasks, project context, and executor class.

Run a single prompt:

```bash
./bin/affentctl run \
  --workspace ./workspace \
  --base-url https://api.openai.com/v1 \
  --api-key "$OPENAI_API_KEY" \
  --model gpt-4o-mini \
  --prompt "Inspect this workspace and summarize the project."
```

For work that needs review before execution, create a persistent plan first and
resume the same session after confirmation:

```bash
./bin/affentctl run --workspace ./workspace --session-id migration-1 \
  --model gpt-4o-mini --plan-only \
  --prompt "Plan the config migration."

./bin/affentctl run --workspace ./workspace --session-id migration-1 \
  --model gpt-4o-mini --execute-plan
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

Start a persistent, memory-limited Docker tool container:

```bash
./bin/affentctl sandbox start
```

By default this creates/reuses a container named `affent-sandbox`, limits it to
`1g` memory, runs the project-owned
`affinefoundation/affent-sandbox:latest` image, and mounts a durable workspace under
`$XDG_DATA_HOME/affent/sandbox/workspace` (or
`~/.local/share/affent/sandbox/workspace`) at the same absolute path inside the
container. If neither `XDG_DATA_HOME` nor a usable home directory is available
(common when running as an arbitrary UID in a container), Affent falls back to
`./affent/sandbox/workspace`. That same path stores affentctl sessions and
memory on the host, and is also the shell/file workspace inside the container.

The sandbox runs as your current host UID/GID by default, so files written
through shell and file tools remain editable on the host. Its `HOME`, Go cache,
npm cache, and pip cache also live under the durable workspace. For Go workloads,
`GOMEMLIMIT` and `GOMAXPROCS` are derived from the Docker memory and CPU limits so
test/build commands are less likely to exhaust the container.

The default sandbox image is defined in `docker/sandbox.Dockerfile` and includes
the tools Affent expects for normal coding-agent work: git/ssh, curl/wget, jq,
ripgrep/fd, file/tree/procps, build-essential/make/pkg-config, Python 3, Node.js
npm, Go 1.24, sqlite, archive tools, rsync, and patch/diff utilities. When you
run `sandbox start` from a source checkout, Affent automatically builds this
image if it is not already present locally. To rebuild or pin the image yourself:

```bash
./bin/affentctl sandbox build
./bin/affentctl sandbox start --replace
```

`sandbox build` uses `docker/sandbox.Dockerfile`, tags
`affinefoundation/affent-sandbox:latest`, and applies a `1g` Docker build memory
limit by default. When run from a source checkout subdirectory, Affent resolves
the default Dockerfile and build context back to the checkout root. Use
`--image`, `--dockerfile`, `--context`, or `--memory` when you need an explicit
local or internal build. Docker memory limits below `128m` are rejected because
they are not useful for Affent tool/runtime containers. Use
`sandbox start --user ""` only when you
intentionally want the image default user instead of host UID/GID.

Use `--image your-registry/affent-sandbox:tag --replace` when you publish an
internal image with extra language runtimes or company tooling.

Build a full Affent runtime image when you want the Affent binaries and the
standard toolchain in one container:

```bash
./bin/affentctl image build --image affinefoundation/affent:local
```

That image includes `affentctl`, `affentserve`, `affenteval`, and the same
packages listed in `docker/tool-packages.txt` that the sandbox image installs.
`image build` uses `docker/affent.Dockerfile`, applies a `1g` Docker build
memory limit by default, and tags `affinefoundation/affent:latest` unless you set
`--image`. Its entrypoint still derives `GOMEMLIMIT` and `GOMAXPROCS` from the
container's cgroup limits as a fallback, but `affentctl image run` also passes
those Go runtime limits explicitly from `--memory` and `--cpus`. Run it through
the same CLI so memory/process limits, Go runtime limits, and the persistent
`/workspace` mount are applied by default:

```bash
AFFENTCTL_BASE_URL=https://api.openai.com/v1 \
AFFENTCTL_API_KEY="$OPENAI_API_KEY" \
AFFENTCTL_MODEL=gpt-4o-mini \
./bin/affentctl image run --image affinefoundation/affent:local -- \
  affentctl run --executor local --prompt "Inspect the workspace."
```

`image run` defaults to `1g` memory, `2` CPUs, `512` PIDs, and
`$XDG_DATA_HOME/affent/runtime/workspace` (or
`~/.local/share/affent/runtime/workspace`) mounted at `/workspace`; with no
usable home directory, it falls back to `./affent/runtime/workspace`. When run
from a source checkout with the default runtime image tag, it builds the image
first if it is missing locally, using the same `1g` build memory limit. It
sets `GOMEMLIMIT` to 75% of the Docker memory limit and `GOMAXPROCS` from the
Docker CPU limit, so Go-based tools inside the runtime image respect the same
resource envelope by default. The command also forwards portable model, auth,
sampling, and feature-toggle env vars such as
`AFFENTCTL_BASE_URL`, `AFFENTSERVE_MODEL`, `AFFENTSERVE_BUILTINS`, and
`TAVILY_API_KEY` when they are set. Host path or executor env vars such as
`AFFENTCTL_WORKSPACE`,
`AFFENTCTL_CONFIG`, `AFFENTCTL_MCP_CONFIG`, `AFFENTCTL_EXECUTOR`,
`AFFENTSERVE_WORKSPACE_ROOT`, and `AFFENTSERVE_MEMORY_ROOT` are not
auto-forwarded because their host values usually do not exist inside the
container; pass container paths explicitly with `--env KEY=VALUE` when needed.
The wrapper timeout defaults to `30m`; use `--timeout 0s` only for an
intentionally unbounded run. Add `--env KEY=VALUE` or `--publish 7777:7777` for
explicit runtime needs. Resource limits, image names, container names, users,
env vars, and published ports are validated before Affent creates workspace
directories or calls Docker; Docker memory limits must be at least `128m`, and
`--pids-limit` must be at least `64`.

`make image-serve` is the shortest production-image smoke path for the HTTP
service. It runs the runtime image through `affentctl image run`, publishes
`127.0.0.1:7777:7777` by default, listens on `0.0.0.0:7777` inside the
container, stores per-session workspaces under `/workspace/sessions`, and stores
durable session state under `/workspace/session-state`. Both paths are inside
the persistent `/workspace` mount. Because this entrypoint is already inside the
memory-limited runtime container, it enables `affentserve --builtins` so the
HTTP service can use shell/file tools; pass `SERVE_ARGS='--builtins=false'` when
you intentionally want a read-only/tool-light service. It also sets
`--timeout 0s` so the wrapper does not stop the service after the one-shot
command default. Override `SERVE_PUBLISH`, `SERVE_LISTEN`,
`SERVE_HEALTH_URL`, `SERVE_WORKSPACE_ROOT`, `SERVE_MEMORY_ROOT`,
`IMAGE_RUN_ARGS`, or `SERVE_ARGS` only when that value is intentionally
different for your deployment; use `SERVE_PUBLISH=7777:7777` only when you
intentionally want Docker to bind on all host interfaces.

For repeatable local use or evals:

```bash
./bin/affentctl run \
  --executor sandbox \
  --base-url https://api.openai.com/v1 \
  --api-key "$OPENAI_API_KEY" \
  --model gpt-4o-mini \
  --prompt "Inspect the workspace and report what files exist."
```

`--executor sandbox` starts or reuses the same default sandbox automatically. If
you prefer shell exports for a whole terminal or eval script, use
`eval "$(./bin/affentctl sandbox start --print-env)"`.

Inspect or stop the sandbox:

```bash
./bin/affentctl sandbox status
./bin/affentctl sandbox stop
./bin/affentctl sandbox stop --remove
```

If you change the sandbox image, workspace, or resource limits for an existing
container name, add `--replace` so the container is recreated with the new
settings.

Build the HTTP server:

```bash
cd cmd/affentserve
go build -o ../../bin/affentserve .
```

Run it. CLI flags and env vars are interchangeable; this example uses
env so the same setup ports cleanly to `docker run -e …` or a
Kubernetes pod spec:

```bash
AFFENTSERVE_BASE_URL=https://api.openai.com/v1 \
AFFENTSERVE_API_KEY="$OPENAI_API_KEY" \
AFFENTSERVE_MODEL=gpt-4o-mini \
./bin/affentserve --listen 127.0.0.1:7777
```

Test the chat endpoint with any OpenAI SDK or curl. `affent_session_id`
in the response pins the session; pass it back as a header or in the
body to continue the same conversation:

```bash
curl -sS http://127.0.0.1:7777/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}' \
  | jq '{content: .choices[0].message.content, session_id: .affent_session_id}'
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
AFFENTCTL_WORKSPACE
AFFENTCTL_CONFIG
AFFENTCTL_MCP_CONFIG
AFFENTCTL_EXECUTOR
AFFENTCTL_SUBAGENT
AFFENTCTL_SUBAGENT_MAX_DEPTH
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

`affentctl --config` accepts JSON using the same names as the CLI flags, with
nested objects for grouped settings. For persistent memory, set explicit caps
when you expect many topic buckets so disk state stays bounded:

```json
{
  "workspace": "./workspace",
  "memory": {
    "enabled": true,
    "dir": ".affent/memory",
    "max_chars": "2200,1375",
    "topic_max_chars": 4400,
    "max_topics": 32
  }
}
```

## State And Memory

Affent keeps session state in JSONL conversation logs so runs can be resumed,
replayed, or inspected. `affentctl` stores logs under the workspace by default.
`affentserve` stores each session's durable state under its session state root:
`conversation.jsonl`, `events.jsonl`, `plan.json`, runtime-installed skills,
and memory files all survive container restarts when that root is backed by a
host volume.
The `make image-serve-up` and `make image-serve-restart` targets keep that root
at `/workspace/session-state` inside the container by default, with `/workspace`
mounted from `IMAGE_WORKSPACE`. Recreating or restarting the container therefore
preserves conversation history as long as `IMAGE_WORKSPACE` is the same host
path. Changing `IMAGE_WORKSPACE` or `SERVE_MEMORY_ROOT` intentionally points the
server at a different session-state tree; `image-serve-up` refuses to reuse an
old container in that case so the mismatch is visible.
Clients resume by sending the same `X-Affent-Session-Id` header or
`affent_session_id` / `session_id` request field. `DELETE /v1/sessions/{id}`
intentionally removes that durable state.
Use `GET /v1/sessions?limit=100&after=<session_id>` to list active and durable
sessions without reading full logs. `POST /v1/sessions` creates or reopens a
session explicitly; pass `{"session_id":"..."}` to choose a stable id, or an
empty body to generate one. `GET /v1/sessions/{id}` returns the merged active
and durable status for one session.
Use `GET /v1/sessions/{id}/history?after=-1&limit=100` to page through the
persisted event log. The `after` cursor is a JSONL line number (`next_after`
from the previous response), not an event id, so replay remains correct across
server restarts where runtime event ids can start over.
The live `GET /v1/sessions/{id}/events` stream uses the same durable line
cursor for its SSE `id:` field; reconnect with `Last-Event-ID` to replay
persisted events after that cursor before continuing with live events. If the
server restarted and the session exists only on disk, the events endpoint
reopens that durable session before replaying.
Use `GET /v1/sessions/{id}/plan` to read the persisted `plan.json` snapshot
without reopening an inactive session.

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

For local CLI use, `--executor sandbox` is the built-in Docker path. It starts
or reuses a long-lived container with default memory/process limits. The
explicit `affentctl sandbox start` command is available when you want to start
the container ahead of time, inspect/stop it, or print `AFFENTCTL_EXECUTOR` /
`AFFENTCTL_WORKSPACE` exports for a whole shell session.

MCP servers can be registered over stdio or streamable HTTP. Their tools are
namespaced by default and become part of the same registry as Affent's built-in
tools:

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

MCP config files are capped at 1 MiB and reject unknown fields so typos do not
silently produce unused configuration. `init_timeout` is optional and defaults
to `30s`; raise it only for slow stdio servers that need extra cold-start time.
Use `allow_tools` or `deny_tools` with raw MCP tool names when a server exposes
more tools than the current workflow should show the model. Empty, duplicate,
overlapping, or unknown filter entries are rejected so a misspelled tool filter
does not silently widen the tool surface. Affent also rejects a server that
would expose zero usable tools after filtering; remove that server from the MCP
config instead of keeping a no-op entry.
With the default namespace behavior, a server tool such as `poi_search` is
advertised to the model as `AMap_poi_search`. For models fine-tuned around raw
MCP tool names, set `"namespace": false` on that server. When prefixes are
disabled, Affent rejects duplicate final tool names at startup instead of
silently overwriting a tool.

Runtime skills are installed through the `skill` tool into the durable
session/workspace skill directory. The installed body is also upserted into the
active in-memory registry, so matching future turns can use it without
restarting `affentctl` or `affentserve`. `skill action:"list"` returns catalog
metadata plus activation rules, not skill bodies, so the agent can inspect when
installed skills will fire without dumping every workflow into context. For
searched or remote candidates, the agent should call `skill` with
`action:"propose_install"`, show the returned `proposal_id`, and include a
non-empty `source` URL/path/provenance string so the user can review where the
candidate came from. Only then call
`action:"confirm_install"` after the user's latest reply explicitly confirms
that exact id. Direct `action:"install"` is reserved for an exact skill body the
user already provided. Runtime skill manifests preserve the reviewed `source`
across session restart; when no source is supplied for a direct install, Affent
falls back to the local persisted `SKILL.md` path.

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
- Recursive `subagent_run` is bounded by `subagent_max_depth`; set the
  depth to `1` for single-layer delegation.
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

Registration:

- `affentctl`: subagent and memory are on by default. Disable subagent
  with `--subagent=false`,
  `AFFENTCTL_SUBAGENT=false`, or `"subagent": false` /
  `"enable_subagent": false` in the config file. `--memory-only` also
  disables it because that mode strips every non-memory tool. Disable
  memory with `--memory=false`.
- `affentserve`: subagent and memory are on by default. Disable with
  `--subagent=false`, `--memory=false`, `AFFENTSERVE_SUBAGENT=false`,
  `AFFENTSERVE_MEMORY=false`, or the matching config keys. Independent
  of `--builtins` — the parent can have subagent without exposing host
  shell, and conversely host shell doesn't pull subagent in.

Pass `mode` (`explore`, `review`, `test`, or `research`) and `task` to
invoke; `max_turns` defaults to 6 with a hard cap of 12.
`subagent_max_depth` defaults to 2 and is hard-capped at 4.

## Focused Tasks

The `run_task` tool is the productized delegation surface. Where
`subagent_run` is the lower-level "give me an isolated child Loop"
primitive, `run_task` constrains the call to one of five well-defined
task types, gives each a dedicated system prompt and tool whitelist,
and requires the child to return a single structured JSON object that
the parent agent can act on without re-reading the child's transcript.

Task types (`task_type` field, schema enum):

- `recall` — search durable memory and prior sessions for facts that
  constrain the current task. Read-only memory + session_search.
- `explore` — locate files / symbols / modules in the current
  workspace and form a small map. read_file + list_files + read-only
  shell + session_search.
- `research` — look up external facts via web_fetch / web_search and
  return cited results. Web tools only (no workspace access).
- `verify` — verify a specific claim with the smallest necessary
  check. Same tools as explore but the prompt forbids speculation and
  requires pass/fail evidence.
- `review` — review a named change/file/claim for risks, missing
  tests, and unhandled edge cases. Each finding carries an explicit
  severity.

Each call must pass `task_type` and `objective`. `max_turns` is
optional and defaults to the profile's recommended budget (4–6);
hard-capped at 12.

Structured output (always returned to the parent, even on partial
failure):

```json
{
  "task_type":      "recall",
  "ok":             true,
  "summary":        "found 1 relevant prior decision",
  "findings":       [
    {
      "claim":      "user prefers terse responses",
      "evidence":   "don't summarize at the end",
      "source":     "session:abc123",
      "confidence": "high"
    }
  ],
  "not_found":      [],
  "warnings":       [],
  "suggested_next": [],
  "objective":      "find user response-format preferences",
  "child_session_id": "focused_…",
  "turn_end_reason": "completed"
}
```

Design contract enforced in code (`internal/agent/focusedtask.go` +
tests):

- Per-profile tool whitelist. The child registry contains only the
  capabilities the profile declares AND the deps satisfy.
- No `write_file` / `edit_file` / `run_task` / `subagent_run` in any
  focused-task child. Children cannot recursively delegate.
- JSON-preferred + text-fallback parser: if the child emits anything
  that isn't a parseable JSON object, the runtime wraps the raw
  output in `summary` and adds `structured_output_parse_failed` to
  `warnings` so the parent agent can still consume the result with
  an explicit downgrade signal.
- Per-turn cap of 3 `run_task` calls per parent turn (loop guard). A
  4th call is rejected with an over-delegation message.
- Evidence is sanitized byte-by-byte for C0 control characters and
  ANSI escapes before the parent agent sees it, so a child that read
  a malicious file can't hijack the trace UI or downstream string
  handling.
- Field ordering in the response JSON keeps `task_type` / `ok` /
  `summary` / `findings` at the front, so the parent Loop's 8 KiB
  per-tool-result truncation never clips the load-bearing content.
- Each tool.request / tool.result trace event for `run_task` carries
  a `delegation: {kind:"focused_task", task_type:…}` block, so trace
  UIs and eval pipelines can filter or group without parsing tool
  args.

Registration:

- `affentctl`: focused tasks on by default. Disable with
  `--focused-tasks=false`, `AFFENTCTL_FOCUSED_TASKS=false`, or
  `"focused_tasks": false` / `"enable_focused_tasks": false` in the
  config file. Independent of `--subagent` — disabling one does not
  disable the other.
- `affentserve`: focused tasks on by default. Disable with
  `--focused-tasks=false`, `AFFENTSERVE_FOCUSED_TASKS=false`, or
  `"enable_focused_tasks": false`. The `research` profile is filtered
  out of the schema enum when `--web` is off, so models never see a
  task_type they can't fulfill.

The full design and implementation notes — task-type selection guidance,
prompt contracts, output schema details, eval strategy, open design questions —
are in [docs/focused-tasks.md](docs/focused-tasks.md).

## Evaluation

Affent includes an internal evaluation runner for real agent scenarios. It
creates temporary workspaces, runs `affentctl` against a configured model, and
checks both the final outcome and the trace-level process quality: whether the
agent completed the turn cleanly, reproduced failures, avoided broad filesystem
scans, preserved test exit codes, kept tests unchanged, and ran a final
verification.

```bash
go run ./cmd/affenteval --list
go run ./cmd/affenteval --list-suites
go run ./cmd/affenteval --list --suite small-model-tools
go run ./cmd/affenteval --suite small-model-tools --temperature 0
go run ./cmd/affenteval --scenario coding-python-slug --temperature 0
go run ./cmd/affenteval --suite small-model-tools --jsonl > eval.jsonl
go run ./cmd/affenteval --scenario coding-python-slug --executor sandbox
make eval-container EVAL_ARGS='--suite small-model-tools --temperature 0'
```

The runner is intentionally small and scenario-driven. It is meant to turn
observed failures from real models into repeatable regression checks before the
same lesson becomes a prompt, skill, guard, or tool-policy change. Text output
includes per-scenario and summary metrics for tool calls, tool errors, argument
repairs, measured tool time, request/result truncation counts and omitted byte
totals, complete result artifact counts, token usage, per-scenario turn end
reason, and summary end-reason/failure-kind distribution. Use `--jsonl` when
comparing models or storing CI artifacts; it emits one `scenario` record per
run plus a final `summary` record with the same metrics. Passing scenario
workspaces are removed by default to avoid filling the machine during repeated
evals; failing workspaces are kept for debugging. Pass `--keep-workspaces` when
you need every workspace and trace left on disk.

Eval JSONL records include `schema_version=1` plus run metadata such as suite,
model, optional `provider_label`, executor, temperature, and timeout so stored
artifacts can be compared without guessing which runtime configuration produced
them. Scenario records also include the parsed `trace_schema_version`, and the
summary includes `trace_schema_versions` counts across the run. Set
`--provider-label` when multiple OpenAI-compatible providers serve the same
model id. Failing scenario records include both the raw `failures` strings and a
structured `failure_kinds` count map for stable aggregation.

`--executor` is forwarded to the `affentctl run` process under test. It
defaults to `local`. Use `--executor sandbox` for one selected scenario when you
want Affent to auto-start its default memory-limited sandbox. For suites,
pre-start one sandbox mounted over an explicit absolute eval work root and pass
it explicitly so every scenario workspace lives under the same container mount:

```bash
./bin/affentctl sandbox start --name affent-eval-sandbox --workspace /tmp/affent-eval --replace
go run ./cmd/affenteval --work-root /tmp/affent-eval --suite small-model-tools --executor docker:affent-eval-sandbox
```

## Events And Observability

Affent emits a structured SSE event stream covering turn boundaries, model
output, reasoning output when available, tool requests, tool results, usage,
and errors. The same event model supports CLI traces, HTTP clients, UIs, and
evaluation harnesses.

Trace JSONL files start with a `trace.meta` record carrying
`schema_version=1`; see [docs/event-trace-contract.md](docs/event-trace-contract.md)
for the stable event envelope, payload fields, and compatibility rules.

`tool.result` carries `turn_id` for per-turn filtering, keeps `result` bounded
for event transport, and includes `result_truncated`, `result_bytes`,
`result_omitted_bytes`, and `result_cap_bytes` so UIs and evals can detect
event-level truncation without parsing the human-readable marker appended to
oversized results. When a runtime workspace is configured, truncated tool
results also include
`result_artifact_path`, a workspace-relative path to the complete output.
For `affentserve`, tool-result artifacts are stored under the durable session
state root and can be listed with `GET /v1/sessions/{id}/artifacts` or read in
bounded chunks with `GET /v1/sessions/{id}/artifacts/{result_artifact_path}`.
`tool.request` similarly includes `args_truncated`, `args_bytes`,
`args_omitted_bytes`, and `args_cap_bytes` for capped argument payloads.

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

Native session endpoints:

- `GET /v1/sessions?limit=100&after=<session_id>` lists sessions from the
  in-memory pool plus durable session directories. Durable summaries include
  `has_conversation`, a bounded `latest_user_message` preview from the tail of
  `conversation.jsonl`, `has_events`, `has_plan`, `has_artifacts`, `has_memory`,
  `has_runtime_skills`, and `runtime_skill_names`; active summaries also
  include the actual registered capabilities (`builtins`, `skill_install`,
  `plan`, `memory`, `session_search`, `subagent`, `focused_tasks`, web/browser
  flags).
- `POST /v1/sessions` creates or reopens a session and returns the same active
  capability summary.
- `GET /v1/sessions/{id}` returns session status and, when active, capability
  summary.
- `GET /v1/sessions/{id}/events` streams SSE events; reconnect with
  `Last-Event-ID` to replay missed persisted events before live events.
- `GET /v1/sessions/{id}/history?after=-1&limit=100` pages persisted events.
- `GET /v1/sessions/{id}/plan` returns the persisted plan snapshot without
  reopening an inactive session.
- `GET /v1/sessions/{id}/tools` lists the active session's actual tool
  catalog, including JSON schemas.
- `GET /v1/sessions/{id}/transcripts` lists durable child-loop transcripts
  from `run_task` and `subagent_run`.
- `GET /v1/sessions/{id}/transcripts/{path}` reads a bounded transcript chunk.
- `GET /v1/sessions/{id}/artifacts` lists durable tool-result artifacts.
- `GET /v1/sessions/{id}/artifacts/{path}` reads a bounded artifact chunk.
- `POST /v1/sessions/{id}/messages` starts an async user turn and returns the
  `turn_id`; consume output through events or history. The request body is
  `{"content":"..."}` by default. Pass `{"mode":"plan_only","content":"..."}`
  to create or update the persisted plan while exposing only the `plan` tool
  for that turn. Pass `{"mode":"execute_plan","content":"optional note"}` to
  execute an existing unfinished, unblocked plan after user confirmation; this
  mode may omit `content`.
- `POST /v1/sessions/{id}/cancel` asks an active session to stop its current
  turn.
- `DELETE /v1/sessions/{id}` closes and purges the session.

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
