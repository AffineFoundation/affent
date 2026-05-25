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

Default container limits are `2g` memory, `2` CPUs, and `512` PIDs where the
target supports all three. Go runtime limits are derived from cgroups so Go
builds and tests respect the same resource envelope.

The production runtime image installs Chromium in addition to the standard
shell/file/web tooling. Browser sessions prefer the system Chromium binary on
`PATH`; if none is present, the underlying browser launcher may download its
own copy at first use, which is slower and can fail in minimal images that lack
Chromium shared libraries.

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
runtime image with durable session state under `/workspace/session-state`. They
enable direct web fetch, the real browser toolset, and a persistent browser
cache at `/workspace/browser-cache` by default, while keeping `web_search`
disabled unless a search backend is explicitly configured. Those paths live
inside `IMAGE_WORKSPACE`, so the server preserves conversation history as long as `IMAGE_WORKSPACE` is the same host path, and it preserves browser cache data
under the same workspace. Deleting a session with
`DELETE /v1/sessions/{id}` intentionally removes that durable state.
`make image-serve-up` refuses to reuse an existing named container when its
runtime image revision differs from the current checkout; run
`make image-serve-restart` to recreate that container after rebuilding.

Use `make image-serve-smoke` for a local persistence check; it creates a
session, verifies the default browser/web tool catalog, restarts the runtime,
and verifies the durable session is still listed.

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

`make image-serve-status` prints the container labels, resource limits, port
mapping, and live `/healthz` JSON so stale images and port mistakes are visible
without inspecting Docker manually.

Test the chat endpoint:

```bash
curl -sS http://127.0.0.1:7777/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}' \
  | jq '{content: .choices[0].message.content, session_id: .affent_session_id}'
```

## Web Retrieval Diagnostics

`web_fetch` starts as a direct HTTP reader, and `web_search` depends on the
configured search backend. `AFFENT_WEB_SEARCH_PROVIDER` accepts `auto`,
`tavily`, or `google`. `auto` preserves the historical Tavily default when
`TAVILY_API_KEY` is present, otherwise uses Google when
`GOOGLE_CSE_API_KEY` and `GOOGLE_CSE_ID` are configured. The Google backend
uses the official Programmable Search JSON API instead of scraping
`google.com/search`, because automated browser sessions from datacenter IPs
often receive anti-abuse challenge pages. When `web_search` is explicitly
enabled without a configured backend, `affentserve` fails at startup instead of silently degrading to fetch-only
mode. When a runtime also enables
`extras/browser`,
`affentserve` wires the session Chromium instance into `web_fetch` as a rendered
fallback: direct-reader trap hosts, anti-bot/challenge responses, and
client-rendered app shells are retried through the browser and returned as
rendered snapshot text. Runtimes without browser support keep the lightweight
HTTP-only behavior. Browser fallback preserves `web_fetch`'s default private
network protection: loopback, RFC1918, link-local, cloud-metadata, and other
internal addresses are refused unless `AllowPrivateNetwork` is explicitly set
for trusted local development. Browser snapshots are formatted with interactive
refs before passive page text, compact adjacent short text blocks, and cap long
dashboard output so small and medium models see the next actionable refs before
context truncation. Search backends can still time out, rate-limit,
or return no usable URLs. The tools surface those cases as structured failures
so the agent can switch source instead of burning turns:

The main agent prompt includes the current UTC date as runtime context. For
current market, news, or trend answers, the model is instructed to treat that as
an access date only when a source lacks its own timestamp, and not to invent
source publication/update dates.

Browser and web tool results also use smaller per-tool context budgets than
generic tools. Full results remain available in trace events, but only the
compact prefix is fed back into the next LLM call. This keeps repeated rendered
page inspections from dominating context on small and medium models.

- `Failure: kind=blocked`: the source refused direct fetch, commonly HTTP 401
  or 403, or returned a successful HTTP response that is only an anti-bot,
  cookie/JavaScript, search-challenge, or social-site error page.
- `Failure: kind=empty_response`: the source returned a successful HTTP
  response with no readable body.
- `Failure: kind=dynamic_shell`: the source returned a successful HTML response
  that looks like a client-rendered loading/app shell rather than source
  evidence. The result may include bounded `Discovery preview (not source
  evidence)` text and a few high-signal `Discovery links (not source evidence)`
  extracted from visible shell navigation; use them only to choose a canonical
  API/text/export endpoint, not as verified page evidence. If
  no better source is available, mark the source as dynamic/unverified.
  When the fetched HTML contains URL-relevant embedded app state or JSON, the
  result can instead include an `Embedded data preview (page source evidence)`
  block. That block is bounded and does not make the rendered shell itself
  evidence; use it only when the fields directly match the requested entity or
  route, and prefer canonical API/text/export endpoints when available.
- `Failure: kind=non_text`: the source returned an image, PDF, archive, or
  another body that is not readable page evidence.
- `Failure: kind=timeout`, `network_error`, `rate_limited`, `server_error`,
  `not_found`, or `http_error`: transport or HTTP-class failures.
- `Failure: kind=no_results`: `web_search` returned no results or no usable
  result URLs.
- `Failure: kind=search_error`: the configured search backend failed in a way
  that does not fit a narrower network or HTTP class.
- `Failure: kind=invalid_args`: the model called the tool with missing or
  unsupported arguments.

Every structured web failure also includes a `Next:` line. The runtime prompt
instructs the agent to follow that guidance: use a canonical or alternate
source, use browser tools when they are registered, rely on search snippets only
as weak sentiment when full-page reading is unavailable, or answer with the gap
clearly marked as unverified.

When `web_fetch` can reuse the session browser, recovered results include
`mode=rendered_browser_fallback` and `[rendered browser fallback succeeded: ...]`.
If a dynamic shell or direct-reader trap says `Rendered browser fallback is not
configured`, that runtime is using direct HTTP fetch only; start `affentserve`
with browser support enabled, or use the registered browser tools directly.

The `web_fetch` tool description and external-research prompt intentionally
steer the model toward official/raw/API/text URLs and away from direct-reading
result-list pages, social pages, short links, dynamic dashboards, and likely
bot/challenge shells. Those sources can still be useful for discovery or
sentiment, but they should not consume repeated direct-fetch attempts when a
canonical source is available.

`web_search` may annotate individual results with `Direct-reader caution` or
`Direct-reader warning` when a URL is likely to waste turns in direct HTTP
fetches, such as search-result pages, social/discussion pages, short-link
redirectors, or result titles/snippets that clearly describe a live dashboard,
client-rendered page, or JavaScript-required app. A warning is stronger than a
caution: the agent should not spend a direct page-reading call on that URL in
the current turn. It should prefer the target/canonical URL, an official API or
text endpoint, or treat the snippet as weak sentiment/claim evidence when no
readable page source is available.

When a search result snippet itself mentions directly readable URLs such as
`llms.txt`, markdown docs, API endpoints, JSON, CSV, or feeds, `web_search`
adds `Source hint` lines. These are discovery hints, not evidence by
themselves. They are meant to help small models choose the text/API URL to read
instead of spending turns on a JavaScript dashboard route.

`web_fetch` also preflights a small high-confidence set of direct-reader traps,
including search-result pages, site-local `/search` routes, major social sites,
and market pages that routinely reject plain HTTP readers. With a rendered
fallback configured, those URLs are sent directly to the session browser instead
of wasting an HTTP attempt. Without a rendered fallback, `web_fetch` returns a
structured `blocked` no-evidence result before network dispatch. This keeps
current-research tasks from spending multiple turns on sources that are useful
for discovery or sentiment but are not reliable direct page evidence. Broad
collection routes such as `/coins` or `/subnets` are annotated as weak
direct-reader targets; they may still be readable, but a specific detail page,
API/text/export endpoint, docs page, or source repository is usually better
evidence.

Repeated failed `web_fetch` calls are guarded more aggressively than general
tool failures. Repeated no-evidence `web_search` results also count as failures
for loop-guard purposes. After repeated no-evidence retrieval, the guard tells
the model to stop opening/searching result lists one by one and change
strategy. If the model repeats the same failed URL or search query, the guard
blocks that exact input and emits
`Failure: kind=loop_guard_repeated_failed_input`. If a `web_search` result
marks a URL with `Direct-reader warning`, the guard also blocks `web_fetch` to
that same URL for the current turn and emits
`Failure: kind=loop_guard_direct_reader_warning`; this saves a tool call and
pushes the model toward snippets as weak evidence or toward canonical
API/text/source URLs. For site-level failures such
as `blocked`, `rate_limited`, or `private_network_blocked`, repeated failures
from different URLs on the same host also block more fetches to that host for
the current turn, because trying another social/search/challenge URL usually
wastes context rather than adding evidence. Known direct-reader trap hosts are
blocked after the first structured failure from that host; other blocked hosts
still get one distinct-URL retry before host-level blocking. Repeated
`dynamic_shell` results on the same host also block additional dashboard/page
routes, while leaving likely API/text/export paths such as `/api/...` or
`.json`/`.csv` URLs available as fallbacks. Generic identical call repeats emit
`loop_guard_repeated_call`, and per-turn workflow caps emit
`loop_guard_call_cap`. First-tool and post-tool workflow policies emit
`tool_policy_first_tool`, `tool_policy_repeat`, or `tool_policy_active` when
they block a model call before the underlying tool runs. Per-turn stats expose
`tool_failure_by_kind`, and each `tool.result` can expose `failure_kind` plus
`failure_kinds`, so eval runs and UIs can distinguish a useful recovery path
from a run that simply accumulated failed retrievals or policy violations.
Successful-but-no-evidence web results, such as `dynamic_shell`,
`empty_response`, `non_text`, or `no_results`, contribute to
`tool_failure_by_kind` even when their `tool.result.exit_code` is `0`;
`tool_errors` remains reserved for non-zero tool exits.

Runtime LLM errors use the same idea on `error.failure_kind` when the loop can
classify them. Known values include `llm_timeout` for per-call or stream-idle
timeouts, `llm_incomplete_stream` when upstream closes SSE before
`finish_reason`, and `context_overflow` when the provider rejects an oversized
prompt/context window. The human `message` remains detailed; the structured
kind is for eval grouping, WebUI badges, and operator alerts.

`llm_timeout` means Affent's per-call wall-clock timeout or stream-idle
watchdog fired while waiting for `/chat/completions` to produce a usable next
chunk. Common causes are long first-token latency from prefill or scheduler
queueing, reasoning models pausing between chunks, KV-cache stalls, proxy
buffering, or an upstream that keeps HTTP open without useful tokens.
`llm_incomplete_stream` means HTTP/SSE started but the upstream closed before a
terminal `finish_reason`; this is usually an upstream/proxy abort such as an
sglang/vLLM worker crash, KV-cache preemption, reverse-proxy reset, or OOM kill.
Batch eval summaries also normalize older bare messages such as
`context deadline exceeded` and `stream ended without finish` into these
actionable examples when they can be classified.

## Subagent Delegation Diagnostics

`subagent_run` is a lower-level isolated child runtime for broad exploration,
review, and other noisy read-only work. The child returns a compact report plus
metadata, not its full transcript. The parent should normally answer from that
report and avoid repeating the same file reads, commands, or browser steps.

Post-tool policy distinguishes a complete evidence report from a partial
verification index:

- `ok=false` means the child did not complete cleanly, or the runtime detected
  explicit open gaps in the report. The parent may do a small verification pass
  over the missing facts, but should not spawn another broad child for the same
  work.
- `ok=true` with no open-gap section means the report is considered sufficient
  for the parent to answer. Duplicate parent-side exploration tools are blocked
  with `tool_policy_active`.
- A repeated `subagent_run` in the same turn is blocked with
  `tool_policy_repeat`; the parent should use the first report as an evidence
  index instead.

Open gaps are detected conservatively from explicit report sections such as
`Uncertainties`, `Warnings`, `Limitations`, `Open questions`, or `Gaps`
(singular and plural forms are accepted, including inline forms like
`Warnings: source is stale`). Empty markers such as `none`, `N/A`, or
`no known uncertainties` keep the report complete. Definitive absence claims
like "No issues found" or "No matching prior session context was found" are not
open gaps by themselves.

Subagent prompts require an `Uncertainties` section and ask the child to write
`- none` when there are no residual gaps. This keeps smaller models from
leaving blank sections that are hard for the parent to interpret.

`affent_session_id` pins follow-up turns. Pass it back through
`X-Affent-Session-Id`, `affent_session_id`, or `session_id`.

### HTTP Endpoints

OpenAI-compatible endpoints:

- `POST /v1/chat/completions`
- `GET /v1/models`

Operational endpoints:

- `GET /healthz` - unauthenticated readiness JSON with `status`,
  `build_revision`, and `build_date`.
- `GET /v1/stats` - authenticated runtime stats, including build metadata,
  session/tool/browser counters, runtime turn-end/error counters, and
  configured runtime boundaries. `runtime.turn_end_by_reason.max_turns`
  indicates the agent exhausted its per-turn action budget before a final
  answer; `runtime.runtime_error_by_kind` tracks non-tool failures such as
  `llm_timeout` and `llm_incomplete_stream`.

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
AFFENT_WEB_SEARCH_PROVIDER
TAVILY_API_KEY
GOOGLE_CSE_API_KEY
GOOGLE_CSE_ID
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
Skill manifests can declare `required_tools`; those skills stay installed and
visible in the catalog, but Affent only auto-injects them when the current
runtime actually registered the required tools.

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

Use explicit `affenteval` sampling flags such as `--temperature`, `--top-p`,
`--max-tokens`, and `--seed` for reproducible runs. The eval container does not
forward host `AFFENTCTL_TEMPERATURE`, `AFFENTCTL_TOP_P`,
`AFFENTCTL_MAX_TOKENS`, or `AFFENTCTL_SEED`; pass those values through
`EVAL_ARGS` so they are also recorded in JSONL metadata. Use
`EVAL_DOCKER_ARGS` only for deliberate extra container environment.

Use runtime eval mode when Affent itself is the benchmark agent and the model
should not receive extra product affordances. In eval mode, dynamic workflow
features such as skills, runtime skill install, subagents, focused tasks, MCP,
project context, session search, and memory are disabled by default; shell/file
tools remain available when built-ins are available. Opt memory or MCP back in
only for suites that explicitly measure those capabilities. The eval container
does not forward host `AFFENTCTL_EVAL_MODE`, `AFFENTCTL_SUBAGENT`,
`AFFENTCTL_FOCUSED_TASKS`, or `AFFENTCTL_PROJECT_CONTEXT`; use the
`EVAL_RUNTIME_*` knobs above.

For external OpenAI-compatible eval harnesses, run `affentserve` in eval mode:

```bash
make eval-serve-container
make eval-serve-browser-container
```

Use `SERVE_EVAL_PERMISSIONS` to opt specific environment capabilities back in,
for example `SERVE_EVAL_PERMISSIONS='browser'` for LiveWeb-style rendered-page
tasks, or `SERVE_EVAL_PERMISSIONS='web web-search memory'` for direct HTTP
retrieval plus memory. Keep this list narrow: enabling `web-search` implies
`web` and requires a configured search backend such as `TAVILY_API_KEY` or
`AFFENT_WEB_SEARCH_PROVIDER=google` with Google CSE credentials, while
browser-only evals should not need web/search permissions.

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
- Web and browser tools refuse private/internal network targets by default,
  including loopback, RFC1918, link-local, and cloud-metadata addresses. Enable
  private network access only for trusted local development or isolated test
  environments.
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

Optional live web smoke tests require network access and are guarded by the
`liveweb` build tag so ordinary unit tests stay deterministic:

```bash
(cd extras/web && go test -tags liveweb . -run TestFetchTool_LiveTaoStatsSubnetEmbeddedData -count=1)
```

Run the containerized root suite:

```bash
make test-container TEST_PACKAGES=./...
```

Browser smoke tests require a local Chromium binary and are guarded by the
`browser_smoke` build tag. The official runtime image provides that binary.
