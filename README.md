# affent

Small, embeddable **agent loop core** for any environment that talks to
an OpenAI-compatible chat completions endpoint (OpenAI, vLLM, Chutes,
SGLang, OpenRouter, Anthropic-via-OpenAI-compat, ...).

Drop into different scenarios that share a single loop:

- [`affine-agents`](https://github.com/AffineFoundation/affine-agents) —
  multi-user web platform: chat UI, per-session Docker sandboxes,
  schedules, Postgres + Redis.
- training / RL rigs that need an agent rollout
- batch eval pipelines (SWE-bench-style harness etc.)
- one-off scripts via `affentctl`

Tiny dep graph: `uuid` + `zerolog` + stdlib.

## What you get

**The loop** (`loop.go`)

- streaming LLM responses with first-class **reasoning** channel
  (`reasoning_content`), persisted on `ChatMessage.ReasoningContent`
  and surfaced as separate `thinking.delta` + `thinking.done` SSE
  events. Reasoning is local-only — stripped from outbound requests
  via `wireMessage` since DeepSeek / Kimi / GLM emit it on responses
  but reject it on inbound.
- **cancellation** mid-turn (parent ctx cancel beats any in-flight
  retry; visible content already streamed makes a stream-cut error
  non-retryable to keep the UI's delta accumulator coherent)
- **transient-error retry** for HTTP 408 / 429 / 5xx, network resets,
  mid-stream EOF, per-call timeouts; honors server `Retry-After`
  header (capped at `MaxRespectedRetryAfter = 5m`)
- **stream watchdog**: `StreamIdleTimeout=60s` between chunks,
  `StreamPostFinishTimeout=5s` after `finish_reason` to defend
  against upstream proxies that forget to send `[DONE]`
- **budgets**: `MaxTurnSteps` (assistant↔tool round trips per user
  turn, default 10), `PerCallTimeout` (per chat completion, default
  3m), `MaxTransientRetries` (default 3, exponential backoff)
- **tool result caps**: `MaxToolResultBytesInContext=8KiB` (what the
  model sees), `MaxToolResultPreviewInEvent=4KiB` (what the SSE event
  carries) — full bytes still go to consumers that care
- **monotonic event ids**: every SSE event carries a per-loop
  sequential `id` so trace consumers can detect drops, order events,
  and tell when filtered events were skipped (see `--trace-skip-deltas`)

**Context compaction** (`compaction.go`)

- `Compactor` interface; default `LLMSummaryCompactor` implements
  rolling summarization using OpenHands V1's
  [`summarizing_prompt.j2`](https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-sdk/openhands/sdk/context/condenser/prompts/summarizing_prompt.j2)
  verbatim — structured fields (`USER_CONTEXT` / `TASK_TRACKING` /
  `COMPLETED` / `PENDING` / `CODE_STATE` / `TESTS` / `CHANGES` /
  `DEPS` / `VERSION_CONTROL_STATUS`), example-driven, with hard
  `PRESERVE TASK IDs` semantics.
- two activation paths: **proactive** (msg count > `TriggerMsgs`,
  default 240) and **reactive** (upstream returns context-overflow
  4xx — matched against keyword set covering OpenAI / DeepSeek /
  Kimi / Anthropic phrasings — emergency compact + retry outside the
  transient-retry budget).
- preserves head (`KeepFirst=2`) + rolling summary (single user
  message tagged `[summary of earlier work]`) + tail (`KeepLast=10`),
  with a boundary fixer that refuses to sever an
  `assistant.tool_calls` from its `role=tool` replies.

**Tools**

- builtin: `shell`, `read_file`, `write_file`, `edit_file`,
  `list_files`. File tools are sandboxed by `safeWorkspacePath`:
  relative paths join onto the workspace, absolute paths are taken
  literally and must fall inside the workspace (no sentinel /
  trim-the-leading-slash hacks).
- `shell` runs through an `executor.Executor` interface. Two stock
  implementations ship in `executor/`:
  - `LocalExecutor` — no isolation; commands run on the host. For CLI /
    training rigs / when the caller is already sandboxed.
  - `DockerExecExecutor` — `docker exec` into a pre-existing container
    you started elsewhere (eval harnesses, multi-tenant attach,
    CI jobs). Does NOT manage container lifecycle.
  - your own impl for Firecracker / Kata / remote / etc.
- File tools (`read_file` / `write_file` / `edit_file` / `list_files`)
  default to touching the host workspace dir directly (fast: no exec
  hop, lets the gateway preview/diff). When the active executor also
  implements the optional `executor.FileOps` interface (e.g.
  `DockerExecExecutor` does — it implements file ops via `docker exec`
  internally), the builtins automatically route through it instead.
  That makes file tools work even when the executor has its own
  filesystem view that isn't bind-mounted from the host.
- **MCP**: stdio + streamable-http (spec rev 2025-03-26). Plug in any
  number of MCP servers — their tools surface as `<server>_<tool>`
  alongside the builtins.

**Project context** (`project_context.go`)

- User-authored project knowledge files, auto-loaded from the workspace
  and inlined into the system prompt at session start. Read-only; affent
  never writes to them.
- Recognized filenames (in load order): `AGENTS.md`, `CLAUDE.md`,
  `CONVENTIONS.md`, `.cursorrules`, `.clinerules`, `.clinerules.md`,
  `GEMINI.md`. Multiple files concatenate, each under a `## <filename>`
  header. Total cap `MaxProjectContextBytes = 32 KiB` (per-file
  truncation past the budget).
- Enabled by default; toggle via `affentctl --project-context=false`,
  or set `Loop.ProjectContextDir = ""` when embedding.

**Persistent memory** (`memory.go`)

- Off by default. Opt in via `Loop.Memory = affent.NewFileMemoryStore(workspace)`
  (or `affentctl --memory`).
- Two stores: `MEMORY.md` for agent notes (env, conventions, lessons
  learned — workspace-scoped) and `USER.md` for user profile
  (preferences, communication style — user-scoped, default
  `$XDG_CONFIG_HOME/affent/USER.md`).
- Single `memory` tool with `action ∈ {add, replace, remove}` and
  `target ∈ {memory, user}`. `replace`/`remove` use a short unique
  substring (`old_text`) to identify the entry — no IDs.
- Frozen-snapshot semantics: at session start, `MemoryStore.Snapshot()`
  composes the on-disk state into the system prompt **once**. Mid-
  session writes update on-disk + live tool responses but do NOT
  re-snapshot, keeping the prefix cache stable for the rest of the
  session.
- Char-bounded (default `MEMORY=2200`, `USER=1375`; ~800 / ~500
  tokens). On overflow, the tool returns `ok=false` with `entries`
  listing the current state so the agent can consolidate in the same
  turn.
- Atomic writes (tempfile + rename). Minimal security scan blocks
  invisible/bidi-override unicode, the literal delimiter sequence,
  and `authorized_keys` substrings — not a full prompt-injection
  regex list (those are mostly performative).

**Session search** (`session_search.go`)

- Registered as the `session_search` tool when `BuiltinDeps.SessionsDir`
  is set (affentctl wires it automatically). The agent searches its
  own past conversation logs in the workspace for `did we discuss X`
  / `what was that command` / `last week's conclusion` questions.
- Term-overlap scoring over JSONL session logs; user + assistant
  messages only (system and tool results are skipped). The current
  session is excluded so the agent doesn't match its own in-flight
  turns.
- Memory and session_search are complementary: memory holds compact
  facts always present in the system prompt; session_search returns
  full snippets on demand without paying per-turn token cost.

**Persistence**

- `Conversation`: append-only JSONL chat log on disk, includes system
  prompt + user/assistant/tool messages with `tool_call_id` preserved
  for resume. `Replace()` rewrites atomically (used by the compactor
  after summarizing earlier turns).

**Observability**

- 13 SSE event types streamed on a single channel — see below.
- `affentctl --trace <path>` mirrors every event into a JSONL file
  for replay / regression diffing (`-` for stdout, empty for stderr).
- `--trace-skip-deltas` drops `thinking.delta` / `message.delta` from
  the trace (skipped events still consume sequence ids so consumers
  can tell what was filtered). Useful for batch eval / training where
  token-level replay isn't needed; the final text is in
  `thinking.done` / `message.done` regardless.

## Layout

```
loop.go            agent loop (LLM <-> tools, streaming, reasoning, cancel)
llm.go             OpenAI-compat streaming client (incl. reasoning_content, watchdog, retry classification)
builtins.go        shell, read_file, write_file, edit_file, list_files (workspace-sandboxed)
project_context.go LoadProjectContext: read AGENTS.md / CONVENTIONS.md / .cursorrules / .clinerules / CLAUDE.md / GEMINI.md
memory.go          MemoryStore + FileMemoryStore (workspace MEMORY.md + user USER.md)
memory_tool.go     the single `memory` tool (action × target dispatch)
session_search.go  session_search tool: term-overlap retrieval over past JSONL session logs
compaction.go      Compactor interface + LLMSummaryCompactor (OpenHands V1 prompt)
conversation.go    JSONL-on-disk chat log, append-only + atomic Replace
tool.go            Tool + Registry
executor/          Executor + FileOps interfaces; LocalExecutor + DockerExecExecutor
sse/               canonical event type constants + payload structs
mcp/               stdio + streamable-http MCP client; Registry adapter
cmd/affentctl/     CLI: run / chat / sessions
cmd/affentserve/   HTTP server: OpenAI-compat /v1/chat/completions + native SSE event stream
extras/            opt-in helper packages, separate sub-modules
  web/             web_fetch + web_search (HTML→markdown, Tavily search default)
  browser/         real Chromium tools (navigate / click / type / scroll / snapshot / screenshot)
```

The `extras/` directory holds opt-in helper packages as separate Go
sub-modules. The root `affent` library does not import them; callers
choose which extras to register, and consumers that don't import them
don't pull their transitive deps.

`cmd/affentserve/` is also a separate Go sub-module so the HTTP
server's deps (browser, extras) don't bloat the core library or
`affentctl`.

## SSE events (the wire contract)

Every loop emits these on `Loop.Events`. UIs, trace files, and tests
all consume the same stream.

Naming: **`.done`** means a streaming accumulator is complete (more
events for the same turn may still follow). **`.end`** is reserved for
turn-level boundaries (no more events for that turn).

| type             | when                                              |
|------------------|---------------------------------------------------|
| `turn.start`     | user message accepted, turn starts                |
| `user.message`   | echoes the user's text (so SSE replays are full)  |
| `thinking.delta` | model's reasoning channel, token by token         |
| `thinking.done`  | reasoning accumulation complete (full text)       |
| `message.delta`  | model's visible content, token by token           |
| `message.done`   | assistant message complete (full text)            |
| `tool.request`   | model called a tool — name + args                 |
| `tool.output`    | (gateway-only today) live stdout/stderr stream    |
| `tool.result`    | tool finished — exit code + truncated preview     |
| `file.changed`   | filesystem mutation (gateway watcher hook)        |
| `usage`          | input / output token totals for the turn          |
| `turn.end`       | reason: `completed` / `cancelled` / `error`       |
| `error`          | transient (recoverable=true) or terminal failure  |

## affentctl (the CLI)

```
affentctl run --prompt "..." --workspace ./task        # one-shot
affentctl chat --workspace ./task                       # REPL
affentctl sessions --workspace ./task                   # list past sessions
```

`run` and `chat` accept `--session-id <id>` or `--continue` to resume
an existing conversation. Logs persist as JSONL under
`<workspace>/.affentctl/<session_id>.jsonl`.

### Flags

| flag                    | default                                          | env                    |
|-------------------------|--------------------------------------------------|------------------------|
| `--config`              |                                                  | `AFFENTCTL_CONFIG`     |
| `--workspace`           | `./affent-workspace`                             |                        |
| `--base-url`            |                                                  | `AFFENTCTL_BASE_URL`   |
| `--api-key`             |                                                  | `AFFENTCTL_API_KEY`    |
| `--model`               |                                                  | `AFFENTCTL_MODEL`      |
| `--prompt`              | (run) literal, `-` for stdin, `@file`            |                        |
| `--max-turns`           | 10                                               |                        |
| `--max-call-timeout`    | 3m                                               |                        |
| `--retry-transient`     | 3                                                |                        |
| `--retry-backoff`       | 4s (doubles each attempt)                        |                        |
| `--trace`               | stderr; `-` stdout, `<path>` JSONL file          |                        |
| `--trace-skip-deltas`   | false (set true for batch eval — drop deltas)    |                        |
| `--system-prompt`       | builtin (dev-box flavored); `-` / file / literal |                        |
| `--quiet`               | false                                            |                        |
| `--project-context`     | true (auto-loads AGENTS.md / CLAUDE.md / etc.)   |                        |
| `--memory`              | false (opt in)                                   |                        |
| `--memory-only`         | false (implies `--memory`, forces `--project-context=false`, rejects `--mcp-config`) | |
| `--memory-workspace-store` | `<workspace>/.affent/MEMORY.md`               |                        |
| `--memory-user-store`   | `$XDG_CONFIG_HOME/affent/USER.md`                |                        |
| `--memory-max-chars`    | `2200,1375` (MEM,USER)                           |                        |
| `--session-id`          | new session                                      |                        |
| `--continue`            | resume newest session under `--workspace`        |                        |
| `--mcp-config`          |                                                  | `AFFENTCTL_MCP_CONFIG` |
| `--compact-trigger`     | 240 (matches OpenHands V1 max_size); 0 disables  |                        |
| `--compact-keep-last`   | 10                                               |                        |

### Project context

`affentctl` auto-loads recognized project knowledge files from
`--workspace` and inlines them into the system prompt at session
start. Files are user-authored and read-only; affent never writes
to them. Recognized names (concatenated in this order if multiple
exist):

```
AGENTS.md
CLAUDE.md
CONVENTIONS.md
.cursorrules
.clinerules
.clinerules.md
GEMINI.md
```

Default on. Disable with `affentctl --project-context=false` for
runs that need a clean baseline.

### Persistent memory

Memory is **off by default** so existing one-shot and dev-box workflows
keep their current tool surface until the caller opts in.

```bash
# Real user: memory on. Agent persists notes in <workspace>/.affent/MEMORY.md
# and user profile in $XDG_CONFIG_HOME/affent/USER.md.
affentctl chat --workspace ./project --memory

# Controlled memory run: only the `memory` tool, no shell/file/MCP escape hatches.
affentctl run --memory-only --prompt @question.txt
```

Two stores: `memory` holds the agent's own notes (environment facts,
project conventions, lessons learned) and travels with the workspace.
`user` holds what the agent knows about the user (preferences,
communication style) and crosses workspaces.

Each store has a character cap (default `2200` / `1375`, ~`800` /
`500` tokens). On overflow the tool returns the current entries so
the agent can consolidate without an extra read. The frozen snapshot
goes into the system prompt at session start; mid-session writes
don't re-snapshot, so the prefix cache stays stable.

### Config file

`--config FILE` loads JSON configuration before building the loop. CLI
flags override values from the config file.

```json
{
  "workspace": "./task",
  "base_url": "https://api.openai.com/v1",
  "model": "gpt-4o-mini",
  "max_turns": 8,
  "trace_skip_deltas": true,
  "project_context": true,
  "memory": {
    "enabled": true,
    "only": false,
    "workspace_store": ".affent/MEMORY.md",
    "user_store": "",
    "max_chars": "2200,1375"
  },
  "compact": {
    "trigger": 240,
    "keep_last": 10
  }
}
```

### MCP config

`--mcp-config FILE` plugs in any number of MCP servers; their tools
are exposed alongside the builtins, namespaced `<server>_<tool>`.
Each server picks its transport from whichever field is set: `URL`
for streamable-http, `command` for stdio. Setting both is an error.

```json
{
  "servers": [
    {
      "name": "fs",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp/task"]
    },
    {
      "name": "git",
      "command": "uvx",
      "args": ["mcp-server-git", "--repository", "/tmp/task"]
    },
    {
      "name": "verify",
      "url": "http://host.docker.internal:8123/mcp",
      "headers": {"X-Auth-Token": "secret"}
    }
  ]
}
```

`Headers` (HTTP only) layers extra HTTP headers onto every request —
useful for auth tokens, version pinning. Sessions are tracked via
`Mcp-Session-Id` automatically.

## Quickstart

```bash
go build -o /tmp/affentctl ./cmd/affentctl

/tmp/affentctl run \
  --workspace /tmp/task \
  --base-url  https://api.openai.com/v1 \
  --api-key   "$OPENAI_API_KEY" \
  --model     gpt-4o-mini \
  --prompt    "list files in /tmp/task and summarize what's there"
```

## Embedding affent in your own program

```go
import (
    "github.com/affinefoundation/affent"
    "github.com/affinefoundation/affent/executor"
    "github.com/affinefoundation/affent/sse"
)

// Optional persistent memory. nil = disabled (default).
mem := affent.NewFileMemoryStore("/tmp/task")

reg := affent.NewRegistry()
affent.RegisterBuiltins(reg, affent.BuiltinDeps{
    Executor:         executor.NewLocalExecutor("session-1", "/tmp/task"),
    HostWorkspaceDir: "/tmp/task",
    Memory:           mem, // registers the `memory` tool too
})

conv, _ := affent.NewConversation("/tmp/task", "session-1")
events := make(chan sse.Event, 256)

llm := affent.NewLLMClient(baseURL, apiKey, model)
loop := &affent.Loop{
    LLM:    llm,
    Tools:  reg,
    Conv:   conv,
    Events: events,
    Memory: mem, // composes MEMORY.md / USER.md into the system prompt at session start
    // Optional: shrink history when it grows beyond TriggerMsgs.
    Compactor: &affent.LLMSummaryCompactor{
        LLM:         llm,
        TriggerMsgs: 240,
        KeepFirst:   2,
        KeepLast:    10,
    },
    // MaxTurnSteps, PerCallTimeout, MaxTransientRetries are all
    // optional — zero falls back to the documented defaults.
}
_ = loop.EnsureSystemPrompt("")  // "" = use DefaultSystemPrompt

turnID, err := loop.SendUser(ctx, "list files and summarize")
// drain events until you see turn.end with matching turn_id
```

The default system prompt assumes a "dev box" environment (a
`/home/agent` + `/workspace` bind-mounted into a container) and
mentions `schedule_*` tools that the gateway registers. If you're
embedding affent outside that environment, pass your own prompt to
`EnsureSystemPrompt`.

## Using extras/web

`extras/web` ships `web_fetch` and `web_search` as opt-in tools. It's a
separate Go sub-module — `go get github.com/affinefoundation/affent`
won't pull any HTML-processing or search-backend deps unless you also
`go get .../extras/web`.

`web_fetch` runs the standard reader pipeline:
[go-shiori/go-readability](https://github.com/go-shiori/go-readability)
(Mozilla Readability Go port — extracts the article main content,
drops nav/header/footer/sidebar) → [JohannesKaufmann/html-to-markdown](https://github.com/JohannesKaufmann/html-to-markdown)
(commonmark-spec converter — handles bold/italic/lists/code/tables/
links/images). We don't roll our own HTML processing.

`web_search` ships a `SearchProvider` interface with a Tavily-backed
default; swap to Brave / SearXNG / an internal index by implementing
the interface.

```go
import (
    "github.com/affinefoundation/affent"
    affentweb "github.com/affinefoundation/affent/extras/web"
)

reg := affent.NewRegistry()
affent.RegisterBuiltins(reg, deps)

// Just the fetch tool — no external API key needed.
affentweb.RegisterFetch(reg, affentweb.FetchConfig{})

// Both fetch + search; default Tavily backend reads TAVILY_API_KEY.
affentweb.RegisterAll(reg, affentweb.Options{})

// Custom search provider (Brave, SearXNG, internal index, …):
tool, _ := affentweb.SearchTool(affentweb.SearchConfig{Provider: myProvider})
reg.Add(tool)
```

`SearchProvider` is the seam — implement `Search(ctx, query, n) ([]SearchResult, error)`
and pass it via `Options.SearchProvider` or `SearchConfig.Provider`.

## Using extras/browser

`extras/browser` ships real browser tools driven by Chromium via
go-rod. Six tools follow the Playwright MCP / Browser Use convention
of "snapshot + ref": each `browser_snapshot` returns a list of
interactive elements with stable integer ref ids, and action tools
(`browser_click`, `browser_type`, `browser_scroll`) take a ref rather
than a CSS selector. Refs let the model reason about the page it just
saw instead of inventing query syntax.

```go
import (
    "github.com/affinefoundation/affent"
    affentbrowser "github.com/affinefoundation/affent/extras/browser"
)

sess, err := affentbrowser.NewSession(affentbrowser.SessionConfig{
    Headless: true,
})
if err != nil { log.Fatal(err) }
defer sess.Close()

reg := affent.NewRegistry()
affent.RegisterBuiltins(reg, deps)
affentbrowser.RegisterAll(reg, sess, affentbrowser.Options{})
```

One `Session` corresponds to one isolated Chromium profile; most
embedders create one Session per agent Loop and close it when the
conversation ends. The package is a separate Go sub-module so the
root affent library does not pull go-rod or its Chromium runtime
unless you opt in.

## affentserve (HTTP server)

`cmd/affentserve` runs affent behind an OpenAI-compatible HTTP API
so any OpenAI SDK (or generic eval harness) can drive it:

```
GET    /healthz
GET    /v1/models
POST   /v1/chat/completions          — OpenAI-compat, streaming via SSE
GET    /v1/sessions/{id}/events      — affent's native 13-type event stream
DELETE /v1/sessions/{id}             — close + remove
```

A `SessionPool` keeps per-session state in-process with LRU eviction
when above `--max-sessions`, idle-TTL GC, and graceful shutdown that
closes browser instances and removes workspaces. Optional features
opt in via flags / JSON config: `--enable-browser`, `--enable-web`,
`--enable-web-search`, `--enable-builtins` (shell + file tools —
disabled by default for remote-driven servers), `--enable-memory`.
`--auth-token` gates every endpoint except `/healthz` with a
`Bearer` token.

`cmd/affentserve` is also a separate Go sub-module — the server's
deps (browser, web, MCP) stay out of the root module's transitive
graph. Build it with `go build ./cmd/affentserve` from that
sub-module's directory.

## Embedding non-local executors

`executor.Executor` is the seam between affent's `shell` tool and the
backing isolation boundary. Two stock implementations ship:

| executor | isolation | use case |
|---|---|---|
| `LocalExecutor` | none — runs on host | CLI, training rigs, when the caller is already sandboxed |
| `DockerExecExecutor` | `docker exec` into a pre-existing container | eval harnesses, attach-mode runs, CI jobs |

`DockerExecExecutor` does NOT manage container lifecycle — the caller
starts and stops the container. It implements the optional
`executor.FileOps` interface so the builtin `read_file` / `write_file`
/ `edit_file` / `list_files` tools automatically route through
`docker exec` instead of touching the host filesystem. This makes
file tools work even when the container's filesystem view isn't
bind-mounted from the host. Write paths use base64-over-stdin to
side-step shell quoting hazards with arbitrary content.

`affentctl --executor docker:<container_id>` wires this up
end-to-end; embedders that need Firecracker / Kata / remote / etc.
implement their own `Executor` (and optionally `FileOps`) and pass it
into `BuiltinDeps`.

## Status

**Working**:

- native loop with reasoning streaming (`thinking.delta` + `thinking.done`)
  + cancel; transient-error retry with `Retry-After` honor + stream
  watchdog
- multi-turn REPL + session resume
- MCP stdio + streamable-http (spec rev 2025-03-26)
- workspace path sandboxing in builtin file tools (relative + absolute,
  no sentinel hacks)
- context compaction (OpenHands V1 LLMSummarizingCondenser prompt
  verbatim, rolling summary with `[summary of earlier work]` marker,
  proactive + reactive paths, tool-call boundary safety)
- persistent memory: two-store `FileMemoryStore` (workspace MEMORY.md
  + user USER.md), single `memory` tool, frozen-snapshot system-
  prompt injection, char-bounded with overflow consolidation
- monotonic event ids + `--trace-skip-deltas` for batch-eval traces
- `wireMessage` strips `reasoning_content` from outbound requests
  (DeepSeek / Kimi / GLM compat)

**Out of scope** (intentional):

- TodoWrite / structured task tracking — Claude Code-style policy,
  belongs in the embedder. affent provides the `Registry` mechanism;
  embedders register their own todo tool with whatever state-machine
  they prefer.
- IDE integration / GUI — affent is server-side / batch / training
  shaped, not IDE-shaped.
- OpenTelemetry traces — wrap externally if needed; the SSE event
  stream is the in-tree observability.
