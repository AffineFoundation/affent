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

Active plans are injected back into each turn with a compact `plan:x/y:status`
label, completed step details omitted, the current unfinished step called out
explicitly, and a reminder to update that same step with status, evidence, or
note after progress. This is a bounded execution aid rather than a strict
workflow engine: the runtime steers the model to execute and update the current
step, while loop guards and evals track whether the plan tool was used cleanly.
Execute-plan turns also reject
`plan action=set` and `plan action=clear` so a confirmed plan is updated in
place instead of being silently replaced during execution. Eval debug
manifests, timelines, and JSONL records include bounded `plan_examples` with
the action, affected step, evidence refs, progress, and current step so
long-run recovery failures can be inspected without opening the raw plan file.

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

DashScope-compatible deployments may set `DASHSCOPE_API_KEY` instead of
`AFFENTSERVE_API_KEY`; the runtime accepts both, with `AFFENTSERVE_API_KEY`
remaining the canonical name.

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
`TAVILY_API_KEY` is present, otherwise uses Google when an API key
(`GOOGLE_CSE_API_KEY` or `GOOGLE_API_KEY`) and a search engine ID
(`GOOGLE_CSE_ID` or `GOOGLE_SEARCH_ENGINE_ID`) are configured. The Google backend
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
context truncation. Snapshot extraction pierces open shadow DOM roots for both
visible text and interactive refs, and browser interaction tools resolve those
refs through the same shadow-aware lookup. This lets component-heavy dashboards
expose ordinary facts and controls without dumping raw HTML. `browser_find`
uses the same shadow-aware scan, so targeted searches stay consistent with
snapshots on component-heavy pages. It also propagates page diagnostics such as
empty dynamic metric widgets into its SourceAccess classification, so label-only
matches are marked as partial evidence rather than verified metric values.
Search backends can still time out, rate-limit, or return no usable URLs. The
tools surface those cases as structured failures so the agent can switch source
instead of burning turns:

The main agent prompt includes the current UTC date as runtime context. For
current market, news, or trend answers, the model is instructed to treat that as
an access date only when a source lacks its own timestamp, and not to invent
source publication/update dates.

Browser and web tool results also use smaller per-tool context budgets than
generic tools. Full results remain available in trace events, but only the
compact prefix is fed back into the next LLM call. This keeps repeated rendered
page inspections from dominating context on small and medium models.
The target browser architecture is documented in
[`docs/browser-access-architecture.md`](browser-access-architecture.md):
rendered pages should produce compact observations, diagnostics, source-access
status, and eventually bounded network evidence, not raw HTML dumps or
site-specific fallback scrapers.

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
`loop_guard_repeated_call`. Browser page-text search also has an evidence
guard: after `browser_find` returns no matches on the same rendered page three
times in one turn, the runtime emits `loop_guard_no_new_evidence` and steers
the model toward one snapshot inspection, `browser_network`/`browser_network_read`,
a different source, or a clearly marked gap. Captured network evidence search
uses the same pattern: `browser_network` includes the current rendered page in
its compact output, and repeated no-match searches on that same page trigger
`loop_guard_no_new_evidence` so the agent waits once, interacts with the
relevant tab, switches to a known API/text/source endpoint, or marks hidden
fields unverified instead of cycling through metric synonyms. Per-turn workflow caps emit
`loop_guard_call_cap`. First-tool and post-tool workflow policies emit
`tool_policy_first_tool`, `tool_policy_repeat`, or `tool_policy_active` when
they block a model call before the underlying tool runs. Per-turn stats expose
`tool_repair_by_kind`, `tool_failure_by_kind`, plus `source_access_results`,
`source_access_verified`, `source_access_discovery_only`, and
`source_access_network`. Eval debug manifests, timelines, and JSONL records
also include bounded `tool_repair_examples`, so repeated small-model tool-name,
alias, enum, type-coercion, and unknown-field mistakes can be inspected without
opening raw trace events. They also include bounded `loop_guard_examples`,
showing the blocked tool, compact args/result guidance, structured guard
reason and suggested next step, failure kind, and whether the rejection came
from loop guard logic or tool policy. Source evidence examples include compact
body previews after the `SourceAccess:` header, and memory update examples
include previous/next previews, so live-web facts and long-run memory changes
can be audited without opening the raw trace. Durable
transcript recall is tracked with
`session_search_calls`, `session_search_results`,
`session_search_context_hits`, `session_search_matched_terms`, and matched
terms per call. Eval debug manifests, timelines, and JSONL records also include
bounded `session_search_examples` with the query, matched session, turn,
physical message index when available, session log modification time, matched
terms, context flag, and compact snippet preview. A user-request hit can carry
the adjacent assistant answer so resume/debug runs show the prior outcome, not
just the old question, which lets poor resume/recovery runs be debugged without
opening the full transcript. Each
`tool.result` can expose
`failure_kind` plus `failure_kinds`, so eval runs and UIs can distinguish a
useful recovery path from a run that simply accumulated failed retrievals,
discovery-only pages, empty recall, or policy violations.
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
  `loop_guard_repeated_call` or `tool_policy_repeat`; the parent should use
  the first report as an evidence index instead.

Eval delegation metrics count runtime failures and unresolved child reports:
`subagent_run` `ok:false` counts as a subagent error, and `run_task` `ok:false`
counts as a focused-task error for non-`verify` task types even when the tool
transport exit code is zero. `verify` may use `ok:false` for a valid
"claim falsified" result. Eval JSONL/text summaries also expose
`focused_task_incomplete`, `subagent_incomplete`, and
`delegation_incomplete=...` so operators can distinguish child reports that
finished with unresolved gaps from transport/runtime failures.

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
  configured runtime boundaries. When `web_search` is enabled,
  `web_search_backend` reports the non-secret active backend name
  (`tavily`, `google`, or `html` for the default public-search-page
  fallback chain). `runtime.turn_end_by_reason.max_turns`
  indicates the agent exhausted its per-turn action budget before a final
  answer; `runtime.runtime_error_by_kind` tracks non-tool failures such as
  `llm_timeout` and `llm_incomplete_stream`. Runtime stats also expose
  `context_compactions`, `context_compactions_reactive`,
  `context_compaction_removed_messages`, and
  `context_compaction_summary_bytes`,
  `context_compaction_summary_missing`, and
  `context_compaction_summary_empty` so long-run operators can see context
  pressure and weak compaction summaries without opening the raw trace. Browser stats expose
  `blocked_by_type`, `blocked_by_domain`, and `domain_relaxations` so
  operators can see when the runtime had to temporarily widen the default
  domain blocklist to recover a page that was otherwise healthy but depended
  on a blocked third-party script or bootstrap URL.
When `browser_navigate` recovers from `net::ERR_BLOCKED_BY_CLIENT`, the tool
result is prefixed with a short recovery note before the usual
`SourceAccess:` block. That keeps the recovery visible to both the model and
operators without changing the snapshot format itself.
If the browser lands on a 404 or "page not found" page, the snapshot and
`browser_find` output are still returned for navigation discovery, but the
`SourceAccess:` line marks them as `not_found_page_discovery_only` so the
model does not treat the body as verified evidence.
Browser sessions also keep a bounded same-site XHR/fetch evidence log.
`browser_network` searches captured JSON/text responses and returns compact
refs with the current rendered page context; WebUI activity summaries surface
that page, query, and match/no-match status so operators can see when a long
run is cycling through network-evidence searches. `browser_network_read` reads a selected ref with
`SourceAccess: browser_network_url=...; ref=...; status=200;
content_type=application/json; source_method=network_xhr_fetch`.
Large JSON/text responses are accepted up to the browser response-cache cap and
then truncated into the evidence log, so a dashboard API response is not
dropped merely because it is too large to feed back in full. Use this path for
dynamic dashboards whose rendered text exposes labels but not the underlying
metric values.
When a tool result is too large for either the `tool.result` event payload or
the next model-context message, the runtime can persist the complete redacted
output as a tool-result artifact and include a workspace-relative read hint in
the truncated context. This keeps long runs recoverable without feeding every
large result back to the model in full.

Session endpoints:

- `GET /v1/sessions`
- `POST /v1/sessions`
- `GET /v1/sessions/{id}`
- `DELETE /v1/sessions/{id}`
- `GET /v1/sessions/{id}/events`
- `GET /v1/sessions/{id}/history`
- `GET /v1/sessions/{id}/plan`
- `DELETE /v1/sessions/{id}/plan`
- `GET /v1/sessions/{id}/loop-protocol`
- `POST /v1/sessions/{id}/loop-protocol`
- `DELETE /v1/sessions/{id}/loop-protocol`
- `GET /v1/sessions/{id}/schedules`
- `POST /v1/sessions/{id}/schedules`
- `PATCH /v1/sessions/{id}/schedules/{schedule_id}`
- `DELETE /v1/sessions/{id}/schedules/{schedule_id}`
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
AFFENTSERVE_SHARED_USER_MEMORY
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
GOOGLE_API_KEY
GOOGLE_CSE_ID
GOOGLE_SEARCH_ENGINE_ID
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

- `conversation.jsonl`: conversation records used to resume sessions. On load,
  Affent repairs missing tool-result pairs left by a mid-turn crash with a
  structured `resume_missing_tool_result` placeholder, keeping strict
  OpenAI-compatible histories valid while telling the model not to assume the
  missing tool succeeded.
- `events.jsonl`: runtime event records for replay and SSE recovery.
- `plan.json`: persisted plan state.
- `.affent/loops/<session_id>/LOOP.md`: optional per-session loop protocol.
  `affentctl --loop-protocol`, `affentctl chat` `/loop on [goal]`, and
  `POST /v1/sessions/{id}/loop-protocol` with `{"activate":true}` initialize a
  draft protocol template when the file is missing; existing files are honored
  without rewriting them. `affentserve` does not create this draft for ordinary
  chat messages; chat-driven setup must go through the `loop_protocol`
  `start_setup` action so loop state only appears after explicit user intent.
  A draft protocol is not treated as an active loop and is not fed into ordinary
  turns, even if its sidecar `state.json` is missing after manual file edits or
  partial recovery. The activation turn must make the model understand the user's intent,
  supplement the protocol with a compact current situation snapshot, stop
  conditions, failure modes, recovery anchors, and any durable memory
  lookup/update rules that belong in the rules section, then set metadata
  `status: running`; only then does the runtime record
  `loop.protocol_activate` and start active loop feeds. Active protocols use a
  low-noise feed policy: the first three feeds and every sixth feed use a
  bounded full copy, while intervening feeds use a smaller digest focused on
  metadata, north-star, current situation, rules, self-checks, stop/recovery, and
  plan/step anchors. Activation, running-protocol maintenance, and successful
  context compaction mark the loop state so the next feed is forced back to full
  even if the normal cadence would have used a digest.
  Feed metadata also includes compact runtime checkpoints from `state.json`,
  including the latest calibration answer, turn end, memory update, and loop
  decision, so the model can recover recent durable changes without replaying
  the full trace.
  When an active loop turn asks for high-impact runtime, protocol, memory,
  browser, eval, or agent-design changes and the request also asks for
  mainstream/frontier/external calibration, the runtime adds a compact
  `AFFENT RESEARCH CHECKPOINT` reminder before the user message and emits a
  visible `loop.decision` with `kind=research_checkpoint`. This is a bounded
  route-check signal, not a second controller agent: the model should use the
  available focused-task, web, or browser surface for a narrow external
  comparison, or explicitly record that external research tools are unavailable,
  then close the loop through plan/rule/protocol/eval changes only when the
  evidence changes direction.
  When `affentserve` loop protocol support is enabled, the model also sees the
  narrow `loop_protocol` tool. It can initialize a chat-driven draft with
  `start_setup`, read the draft, write bounded draft updates, or call
  `complete_activation` with the full supplemented protocol;
  `complete_activation` requires metadata `status: running` and records
  `loop.protocol_activate`. This lets ordinary chat requests and WebUI buttons
  share the same calibration-first setup path, and avoids asking the model to
  edit server-managed session state through ordinary workspace file tools.
  `affentctl` resolves the file under the configured workspace and, when a
  persisted `.affentctl/<session_id>.plan.json` exists, includes the current
  plan checkpoint in the feed metadata. Session list/detail responses expose
  its summary for server-backed sessions. Use
  `POST /v1/sessions/{id}/loop-protocol` with `{"protocol":"..."}` to create
  or replace it without reopening the session; use `DELETE` to disable it.
- `.affent/loops/<session_id>/state.json`: machine-readable loop lifecycle
  state. It records owner, status, the initial goal preview and initial plan
  label when available, protocol update count, calibration answer count and
  latest calibration preview, protocol feed count, latest feed mode, context
  compaction count, whether the next protocol feed must be full, the latest
  active-plan checkpoint observed during a feed, and the latest turn checkpoint:
  turn id, end reason, token usage, tool/error counts, loop-guard interventions,
  forced no-tool recoveries, memory updates, and session-search calls. Confirmed
  memory mutations are also mirrored as a
  latest memory-update checkpoint with action, target, topic, location,
  previous/next previews, and a compact display preview. It also records
  loop-decision count and the latest gate decision, including kind, trigger,
  decision, confidence, reason, and required action. The latest loop event is
  mirrored so restart/resume code and WebUI do not have to parse Markdown or
  replay the full trace for common status panels. WebUI session rows surface
  recent calibration answers, memory updates, loop decisions, and last-turn
  checkpoint state from these fields. Feed count is durable, so
  reopening a session continues the
  full/digest cadence instead of restarting from the first full feeds. Session
  list/detail responses expose this as `loop_state`, including after `LOOP.md`
  is disabled.
- `.affent/loops/<session_id>/events.jsonl`: bounded loop audit events such as
  protocol feeds, memory updates, loop decisions, turn checkpoints, compaction
  marks, updates, and deletes. `GET /v1/sessions/{id}/loop-protocol` returns
  recent events alongside the protocol for operator visibility.
- `schedules.json`: per-session scheduled prompts. Each schedule stores the
  model-facing `prompt` and optional human-facing `display_text`; WebUI lists,
  session summaries, and scheduled `user.message` events prefer `display_text`
  so long internal loop/timer control prompts do not become the visible task
  title.
- Runtime skill files: installed skill bodies and manifests.
- Memory files: topic-bucketed workspace or user memory.
- Transcript files: child-task and subagent conversations.
- Artifact files: full tool outputs when event payloads are capped.

`affentctl` stores session state under the configured workspace by default.
`affentserve` stores per-session durable state under its session state root so
state survives container restart when backed by a host volume.
By default `affentserve` scopes both project memory and `target=user` memory to
the session id, which is the safer default for shared servers. For local
single-user WebUI deployments, set `--shared-user-memory` or
`AFFENTSERVE_SHARED_USER_MEMORY=true` to store `target=user` in
`MemoryRoot/USER.md` and share stable user preferences across sessions while
keeping project/task memory per session.

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
- Focused tasks: typed delegation surface for recall, explore, web_extract,
  research, verify, and review tasks.

Runtime skill installs use a proposal/confirmation path for remote candidates.
Direct install is reserved for an exact skill body the user already supplied.
Skill manifests can declare `required_tools`; those skills stay installed and
visible in the catalog, but Affent only auto-injects them when the current
runtime actually registered the required tools.

Focused tasks and subagents return structured reports to the parent session
without injecting their full intermediate work into the parent conversation.
They are bounded by task size, turn count, depth, output caps, and read-only
tool policies where applicable. Focused-task findings must include both
`source` and `evidence`; if a child claims success but every finding is omitted
for missing evidence, the runtime downgrades the result to `ok:false` so the
parent treats it as a gap instead of a verified report.
When rolling compaction later summarizes the session, `run_task` and
`subagent_run` tool results are rendered as compact delegation summaries
(`summary`/`findings` or `report` plus bounded metadata and tool-call names)
instead of raw JSON, so long sessions preserve the evidence the parent acted on
without paying to re-summarize child transcripts or bulky response metadata.
Compacted plan tool results also include the same `plan:x/y:status` label so
post-compaction recovery can identify current progress even if natural-language
step text was shortened.
If the compacted span included an active `LOOP.md` feed, the rolling summary
also receives a deterministic `LOOP_PROTOCOL:` anchor with the protocol path,
feed mode/count, loop id/status, and active plan checkpoint. This keeps
post-compaction recovery pointed at the per-session protocol even when the
summarizer model omits that detail from its natural-language summary. The
runtime also mirrors that line into the `context.compacted`
`loop_protocol_anchor` field so trace, WebUI, and eval tooling can display it
without relying on the bounded summary preview.
Long-run eval scenarios can assert required anchor substrings through
`required_context_loop_protocol_anchor_text`, which catches regressions where
ordinary compaction still fires but loses the per-session `LOOP.md` recovery
pointer.
They can also require `require_loop_protocol_full_after_compaction`, which
uses trace event order to verify that a full protocol feed happened after a
`context.compacted` event instead of merely occurring somewhere in the same
run.
Eval debug manifests index the retained child transcript paths and sizes under
`child_transcripts`, and timelines include a `Child Transcripts` section, so
operators can jump to isolated child work without pushing transcript contents
back into the parent model context.
`web_extract` is the focused-task variant for page-level reading: use it when
one page or a small bounded set of pages contains too much raw text for the
parent turn, so the child keeps the evidence compact and the parent only sees
findings, warnings, and suggested_next.
For workspace code discovery, prefer the built-in `symbol_context` tool when
you already know the likely symbol or declaration, use `file_context` before
`read_file` on long or noisy files, then use `repo_search` before broad shell
scans; these return compact file:line evidence and keep the parent context
smaller.

## Evaluation

List built-in scenarios:

```bash
go run ./cmd/affenteval --list
go run ./cmd/affenteval --list-suites
```

Current built-in suites:

- `small-model-tools`: weak-model tool calling, recovery, and compact-context behavior.
- `hard-agent`: harder local agent tasks such as coding, planning, and subagent workflows.
- `long-run`: deterministic complex tasks for longer practical runs, currently
  covering stock synthesis, Bittensor subnet research, code implementation with
  PR-style reporting, focused-task recovery, research-checkpoint visibility,
  and multi-session task recovery.
  The stock and subnet scenarios require reading the explicit evidence files,
  so a run cannot pass by answering only from prompt wording or stale archive
  files. The PR-style coding scenario requires reading the implementation file
  before editing it and naming the changed file in the final PR summary. A
  combined recovery scenario requires joining persistent memory with prior
  session history, covering the shared-memory plus session-search path.
- `live-web`: non-CI live web regressions for JavaScript-heavy pages,
  direct-reader recovery, and browser network evidence quality. These scenarios
  intentionally depend on public sites and should be run with web/browser tools
  enabled. Browser network evidence scenarios require final answers to preserve
  `browser_network_url`, `requested_url`, `ref=...`, `status=...`, and
  `content_type=...`, so operators can distinguish the response actually read
  from the user-facing page being verified and audit response quality. They
  require `browser_network_read` but do not require an extra `browser_network`
  search when a rendered snapshot already exposes the relevant captured network
  ref. One network-discovery scenario does require `browser_network` before
  `browser_network_read`, covering the case where a JavaScript page exposes only
  shell text and the agent must first find the most relevant captured response.

Run scenarios:

```bash
go run ./cmd/affenteval --suite small-model-tools --runtime-tools workspace,recall,plan,skill,delegation --temperature 0
go run ./cmd/affenteval --suite long-run --runtime-tools workspace,recall,plan,delegation --temperature 0
go run ./cmd/affenteval --suite live-web --runtime-web --runtime-browser --temperature 0 --keep-workspaces
go run ./cmd/affenteval --scenario coding-python-slug --runtime-tools workspace --temperature 0
go run ./cmd/affenteval --suite small-model-tools --runtime-tools workspace,recall,plan,skill,delegation --jsonl > eval.jsonl
go run ./cmd/affenteval --list-quality-profiles
go run ./cmd/affenteval --suite long-run --runtime-tools workspace,recall,plan,delegation --quality-profile longrun --temperature 0
go run ./cmd/affenteval --suite live-web --runtime-web --runtime-browser --quality-profile web-evidence --temperature 0 --keep-workspaces
```

Quality gate flags are optional and disabled by default. They return exit code
`1` after the full batch finishes if the aggregate summary violates configured
thresholds. Text summaries print a `QUALITY_GATES` line when any gate is
enabled, including failed gate names and thresholds; JSONL records copy the
enabled thresholds into metadata so result files preserve their pass/fail
conditions. Use `--quality-profile longrun` for general long-run regression
runs; it includes minimum trace-event, memory-update, loop-protocol feed,
session-search context-hit, scenario-level session-recall debug tag, and missing
truncation-artifact gates, plus a scenario-level failed tool-repair gate, so
observability, shared memory, tool recovery, loop-guard no-tool fallback, and
cross-session recovery regressions fail the batch. Use
`--quality-profile web-evidence` for live/current web evidence runs; it also
fails scenario-level debug brief tags such as dynamic source evidence without
network-backed reads, browser network refs that were not followed by
`browser_network_read`, or dynamic gaps without an explicit evidence-quality
defer decision.
Explicit `--max-debug-brief-tag-rate tag=rate` flags merge with profile
defaults and can disable one profile tag with `tag=-1`. Other explicit gate
flags override the profile defaults. JSONL summary records also
include `quality_gates_passed` when any gate is enabled and
`quality_gate_failures` when a gate failed, so stored eval artifacts can explain
CI or model-comparison failures without stderr.
The default text summary also prints the key normalized rates used for long-run
debugging, including pass/completion, average scenario duration, memory update
coverage, loop-protocol feed coverage, runtime-surface coverage, tool errors,
focused-task/subagent errors, plan errors, repair success, verifier pass rate,
verified evidence, network/discovery/dynamic-partial source ratios, average
context compactions, reactive context compactions, aggregate and per-family
expected-capability pass rates, messages removed by compaction, compaction
summary size, missing/empty compaction summaries, session-search matched terms
per call, average tool calls, and tool-context truncation.
Use `--min-pass-rate`, `--min-completion-rate`,
`--min-memory-update-rate`, `--min-loop-protocol-feed-rate`,
`--min-runtime-surface-rate`, `--min-trace-event-rate`,
`--min-source-network-rate`,
`--min-source-access-verified-rate`,
`--min-expectation-capability-pass-rate`,
`--min-each-expectation-capability-pass-rate`,
`--min-session-search-context-hit-rate`,
`--min-session-search-matched-terms-per-call`,
`--min-tool-repair-success-rate`,
`--min-verifier-pass-rate`, `--max-tool-error-rate`,
`--max-focused-task-error-rate`, `--max-subagent-error-rate`,
`--max-forced-no-tools-rate`,
`--max-loop-guard-intervention-rate`,
`--max-plan-error-rate`,
`--max-source-discovery-only-rate`,
`--max-source-dynamic-partial-rate`,
`--max-tool-context-truncation-rate`,
`--max-tool-result-truncation-rate`, `--max-avg-runtime-errors`,
`--max-avg-context-compactions`, `--max-avg-reactive-context-compactions`,
`--max-avg-context-removed-messages`, `--max-avg-context-summary-bytes`,
`--max-avg-context-summary-missing`, `--max-avg-context-summary-empty`,
`--max-avg-tool-calls`, `--max-avg-duration-ms`, and `--max-avg-total-tokens`
for CI or model/provider comparison runs.

Run a one-off prompt through the same batch harness:

```bash
go run ./cmd/affenteval --prompt "Analyze the current project and report the risky parts." --name project-audit --max-turns 12 --keep-workspaces
go run ./cmd/affenteval --prompt-file request.md --runtime-web --name web-research --max-turns 20 --keep-workspaces
go run ./cmd/affenteval --prompt-file request.md --runtime-web --runtime-browser --name rendered-web-debug --max-turns 20 --keep-workspaces
go run ./cmd/affenteval --prompt-file request.md --runtime-web --runtime-browser --trace-deltas --name full-trace-debug --max-turns 20 --keep-workspaces
```

Each run writes a trace JSONL plus retained debug files in the scenario
workspace: `affenteval-debug.json`, `affenteval-timeline.md`,
`affenteval-final.txt`, `affenteval-stdout.txt`, and
`affenteval-stderr.txt`. Failed workspaces are kept automatically. When quality
gates are enabled, passing workspaces are kept until the batch gate result is
known; if any gate fails they remain for trace/timeline inspection, otherwise
they are cleaned unless `--keep-workspaces` is set. `--keep-workspaces` also
keeps passing runs for local inspection. The timeline is the human-readable
index for debugging: it links the raw trace, starts with a `Recovery Guide` and
`Debug Brief` for failed or diagnostic-heavy runs, shows the redacted
`affentctl` command argv, trace event type counts, effective runtime surface,
tool calls with args/result previews, child transcript refs,
truncation/artifact pointers, loop protocol feeds, loop decisions, context
compactions, and runtime errors. Browser network searches are listed separately from `SourceAccess`
evidence: `browser_network` entries are refs/checks only, include compact
matched-response previews when available, and the timeline requires
`browser_network_read` source evidence before treating hidden JSON/text values
as citable. By default, eval
traces pass `affentctl --trace-skip-deltas` so token
streaming deltas do not bury the tool timeline; use `--trace-deltas` only for
deep provider/stream debugging when the raw `message.delta` sequence matters.
The trace emits a `runtime.surface` event at turn start, and the debug
manifest copies the latest surface into `runtime_surface`, including the
effective tool names, broad capability flags, partial workspace tool lists, and
key tool-result limits. The manifest includes `recovery_guide`, which orders
the files and sections to inspect, keeps the exact redacted rerun command, adds
a full-trace rerun command when the compact trace skipped streaming deltas, and
provides a continuation prompt for handing the failure back to an agent. The
manifest and JSONL scenario record also include a machine-readable
`debug_brief` with stable tags and inspection hints; loop guard hints route
first to `loop_guard_examples` so the blocked call, guard reason, and suggested
next step are visible before opening full traces. The debug manifest, timeline,
and JSONL scenario record also include structured
`expectations` plus derived expected capability names/outcome, so
batch-analysis scripts can group failures by required tools, evidence checks,
loop protocol feed checkpoints, plan/delegation constraints, and
context-compaction requirements without reimplementing capability inference.
Text and JSONL summary records aggregate those declarations as expectation
coverage counters, including suites, required tools, required source-access
statuses, and broad capabilities such as memory, browser, delegation, plan,
loop protocol, and context compaction. They also split expected capabilities
into passed and failed counts so long-run reports can show whether regressions
cluster around memory, browser/web evidence, delegation, plan, loop protocol,
or context compaction. Text and
JSONL summaries also include an aggregate expected-capability pass rate for CI
and model/provider comparison dashboards, plus bounded failed-scenario examples
per failure kind and expected capability so operators can jump from a
turn-end/verifier/browser/memory/plan regression to the retained trace or
timeline. The per-family expectation gate can fail a run when one capability
family regresses even if the aggregate expectation pass rate is still
acceptable. JSONL summary records also aggregate debug brief tags as
`debug_brief_by_tag` for batch triage. Tool result truncation and model-context
trimming are split by tags such as `truncation:missing_artifact` and
`truncation:tool_context`, so long-run regressions can separate lost raw output
from output that existed but was shortened before re-entering the model.
JSONL scenario records also include a compact `runtime_surface` summary so
batch analysis can group outcomes by actual tool/capability surface. JSONL
summary records include per-scenario counts for runtime tools and capabilities
seen across the batch.

Run through Docker:

```bash
make eval-container EVAL_RUNTIME_TOOLS=workspace,recall,plan,skill,delegation EVAL_ARGS='--suite small-model-tools --temperature 0'
make eval-container EVAL_RUNTIME_TOOLS=workspace,recall,plan,delegation EVAL_ARGS='--suite long-run --temperature 0'
make eval-agent-container EVAL_RUNTIME_TOOLS=workspace EVAL_ARGS='--scenario coding-python-slug --temperature 0'
make eval-agent-container EVAL_RUNTIME_TOOLS=readonly_workspace EVAL_ARGS='--scenario repo-inspection --temperature 0'
make eval-agent-container EVAL_RUNTIME_MEMORY=true EVAL_ARGS='--scenario your-memory-scenario --temperature 0'
make eval-agent-container EVAL_RUNTIME_WEB=true EVAL_RUNTIME_BROWSER=true EVAL_ARGS='--prompt-file request.md --name rendered-web-debug --max-turns 20'
make eval-agent-container EVAL_RUNTIME_WEB=true EVAL_RUNTIME_BROWSER=true EVAL_TRACE_DELTAS=true EVAL_ARGS='--prompt-file request.md --name full-trace-debug --max-turns 20'
make eval-agent-container EVAL_RUNTIME_MCP_CONFIG=/workspace/config/mcp.json EVAL_ARGS='--scenario your-mcp-scenario --temperature 0'
```

Use explicit `affenteval` sampling flags such as `--temperature`, `--top-p`,
`--max-tokens`, and `--seed` for reproducible runs. The eval container does not
forward host `AFFENTCTL_TEMPERATURE`, `AFFENTCTL_TOP_P`,
`AFFENTCTL_MAX_TOKENS`, or `AFFENTCTL_SEED`; pass those values through
`EVAL_ARGS` so they are also recorded in JSONL metadata. Use
`EVAL_DOCKER_ARGS` only for deliberate extra container environment.

`affenteval` runs `affentctl` in runtime eval mode by default so benchmark
tasks start from a no-tool surface and prompts only describe registered
capabilities. Opt capabilities back in only for suites that explicitly measure
them: `--runtime-tools read_file,shell`, `--runtime-tools readonly_workspace,web`,
`--runtime-tools recall` for durable memory plus prior-session search,
`--runtime-memory`, `--runtime-web`, `--runtime-browser`, `--runtime-mcp-config`,
or the matching lower-level `affentctl --eval-tools` flags, which imply eval
mode. Use `--eval-all-tools` / `--runtime-all-tools` only for smoke/debug runs
that intentionally exercise the full surface. Before running, `affenteval`
also validates scenario-declared tool dependencies from required tool counts,
orders, argument checks, source-access checks, session recall checks, and
delegation checks, including command-before/after-tool ordering checks, against
the selected runtime surface. The eval container does not forward host
`AFFENTCTL_EVAL_MODE`,
`AFFENTCTL_EVAL_TOOLS`, `AFFENTCTL_EVAL_ALL_TOOLS`, `AFFENTCTL_SUBAGENT`,
`AFFENTCTL_FOCUSED_TASKS`, or `AFFENTCTL_PROJECT_CONTEXT`; use the
`EVAL_RUNTIME_*` knobs above. Use `--runtime-web` when a scenario explicitly
measures direct web retrieval; use `--runtime-browser` for rendered-page debug
runs that need `browser_navigate`, `browser_snapshot`, `browser_find`, or
captured network evidence. When both runtime flags are set, `web_fetch` can
fall back through the same session browser for JavaScript-heavy pages.
Otherwise keep these surfaces off so evals stay on the minimal surface they
intend to measure.
Scenario files can seed `.affent/loops/<session_id>/LOOP.md`; even in runtime
eval mode, `affentctl` injects that protocol when present and emits
`loop.protocol_feed` with the active plan checkpoint and any recorded
calibration answer count/latest preview. This lets long-run evals assert that
the loop protocol was actually fed, rather than only checking that the file
existed in the workspace.
Batch scenarios can also define multiple ordered prompts. The harness reruns
`affentctl run` with the same workspace, trace, and explicit session id, so the
second and later turns exercise real persisted conversation state instead of a
synthetic fixture. This is used for post-compaction recovery checks such as
requiring a full `LOOP.md` feed after `context.compacted`.

When project context is enabled in normal runtime mode, Affent also injects a
small auto-generated repository map alongside user-authored project notes. The
repo map summarizes the top-level workspace structure, skips hidden/build
directories, and excludes the project-context files themselves so the model can
orient itself without reading the whole tree.
Affent also adds a shallow Go symbol hint block for visible Go packages so the
model can see package names, entry files, and a few top-level declarations
before it starts reading code.
The `symbol_context` workspace-discovery tool uses the same Gitignore-aware
symbol scan and returns exact declaration matches with file, line, package,
and short signature snippets for code-orienting lookups.
The `file_context` workspace-discovery tool returns a compact structured view
of one file with head/tail snippets, query matches, and Go symbol hints when
applicable, so models can inspect long files without flooding the parent turn.
The `repo_search` workspace-discovery tool follows the same top-level ignore
policy, including root `.gitignore` entries, so generated files and local
cache/output trees stay out of the default search surface.
The ignore matcher is intentionally lightweight but directory-aware: common
patterns such as `build/`, `dist/`, `node_modules/`, and `*.jsonl` are skipped
across nested paths, not only at the workspace root.

For external OpenAI-compatible eval harnesses, run `affentserve` in eval mode:

```bash
make eval-serve-container
make eval-serve-browser-container
```

Use `SERVE_EVAL_PERMISSIONS` to opt specific environment capabilities back in,
for example `SERVE_EVAL_PERMISSIONS='browser'` for LiveWeb-style rendered-page
tasks, or `SERVE_EVAL_PERMISSIONS='web web-search recall'` for direct HTTP
retrieval plus durable recall. Keep this list narrow: enabling `web-search` implies
`web` and requires a configured search backend such as `TAVILY_API_KEY` or
`AFFENT_WEB_SEARCH_PROVIDER=google` with Google CSE credentials, while
browser-only evals should not need web/search permissions.

The JSONL output contract is documented in
[eval-jsonl-contract.md](eval-jsonl-contract.md). Runtime traces are documented
in [event-trace-contract.md](event-trace-contract.md).

## Observability

Affent emits structured events for turn starts, user messages, assistant
deltas, reasoning deltas, tool requests, tool results, loop protocol feeds,
usage, errors, and turn endings. Tool request/result events include repair,
truncation, artifact, and delegation metadata where relevant.

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
