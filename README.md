# Affent

Affent is an OpenAI-compatible agent runtime with a CLI, an HTTP server, and a
Go API. It is built for practical tool-using agent sessions: running commands,
editing workspace files, searching prior sessions, preserving durable memory,
and streaming structured events to clients.

Affent owns the runtime concerns around model streaming, tool dispatch,
conversation persistence, cancellation, retries, context compaction, project
context, memory, MCP integration, and optional web/browser tools.

## Features

- OpenAI-compatible streaming chat client with reasoning-channel support.
- Tool loop with bounded per-turn steps, cancellation, retries, watchdogs, and
  capped tool-result context.
- Built-in tools for shell execution and workspace file operations.
- Pluggable executor interface for local, Docker, or custom sandboxes.
- Append-only JSONL conversation persistence with atomic replacement for
  compaction.
- Context compaction using an OpenHands-style rolling summary prompt.
- Project context loading from common agent instruction files.
- Topic-bucketed persistent memory with on-demand lexical retrieval.
- Session search over prior workspace transcripts.
- MCP client support for stdio and streamable HTTP servers.
- Canonical SSE event stream for UI, tracing, replay, and evaluation.
- Optional extras for web search/fetch and real browser automation.
- `affentctl` CLI and `affentserve` OpenAI-compatible HTTP server.

## Project Layout

```text
loop.go              Agent loop, streaming, tool execution, cancellation
llm.go               OpenAI-compatible streaming client
tool.go              Tool definition and registry
builtins.go          Shell and workspace file tools
conversation.go      JSONL conversation log
compaction.go        Rolling context compaction
project_context.go   Project instruction file loader
memory.go            Persistent memory store
memory_tool.go       Memory tool schema and dispatch
session_search.go    Retrieval over prior session logs
executor/            Executor and FileOps implementations
internal/            Runtime internals behind the public root package
mcp/                 MCP client and registry adapter
sse/                 Event types and payloads
cmd/affentctl/       CLI driver
cmd/affentserve/     HTTP server
extras/web/          Optional web_fetch and web_search tools
extras/browser/      Optional Chromium browser tools
```

`cmd/affentserve`, `extras/web`, and `extras/browser` are separate Go
submodules. Importing the root module does not pull their heavier
dependencies.

## Installation

Affent requires Go 1.22 or newer.

Use as a Go package:

```bash
go get github.com/affinefoundation/affent
```

Build the CLI:

```bash
go build -o ./bin/affentctl ./cmd/affentctl
```

Build the HTTP server:

```bash
cd cmd/affentserve
go build -o ../../bin/affentserve .
```

## Quick Start

Run a one-shot task:

```bash
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

List saved sessions:

```bash
./bin/affentctl sessions --workspace ./workspace
```

Conversation logs are stored under:

```text
<workspace>/.affentctl/<session_id>.jsonl
```

Use `--session-id <id>` or `--continue` with `run` and `chat` to resume an
existing conversation.

## Configuration

Both `affentctl` and `affentserve` load `.env` files from the current
directory and from:

```text
~/.config/affent/.env
```

Shell environment variables take precedence over `.env` values.

Common CLI variables:

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
TAVILY_API_KEY
```

`affentctl --config FILE` accepts JSON configuration. CLI flags override file
values.

```json
{
  "workspace": "./workspace",
  "base_url": "https://api.openai.com/v1",
  "model": "gpt-4o-mini",
  "max_turns": 10,
  "project_context": true,
  "memory": {
    "enabled": true,
    "dir": ".affent/memory",
    "user_store": "",
    "max_chars": "2200,1375"
  },
  "compact": {
    "trigger": 240,
    "keep_last": 10
  }
}
```

## Go API

Affent can also be used directly from Go. This is primarily useful for
integrations that need to construct their own runtime, registry, executor, or
event pipeline. A minimal setup creates an LLM client, a tool registry, a
conversation log, and a loop.

```go
package main

import (
	"context"
	"log"

	"github.com/affinefoundation/affent"
	"github.com/affinefoundation/affent/executor"
	"github.com/affinefoundation/affent/sse"
)

func main() {
	ctx := context.Background()
	workspace := "/tmp/affent-workspace"

	llm := affent.NewLLMClient(
		"https://api.openai.com/v1",
		"<api-key>",
		"gpt-4o-mini",
	)

	mem := affent.NewFileMemoryStore(workspace)

	reg := affent.NewRegistry()
	affent.RegisterBuiltins(reg, affent.BuiltinDeps{
		Executor:         executor.NewLocalExecutor("session-1", workspace),
		HostWorkspaceDir: workspace,
		Memory:           mem,
		SessionsDir:      workspace + "/.affentctl",
		SessionID:        "session-1",
	})

	conv, err := affent.NewConversation(workspace, "session-1")
	if err != nil {
		log.Fatal(err)
	}

	events := make(chan sse.Event, 256)
	loop := &affent.Loop{
		LLM:    llm,
		Tools:  reg,
		Conv:   conv,
		Events: events,
		Memory: mem,
		Compactor: &affent.LLMSummaryCompactor{
			LLM:         llm,
			TriggerMsgs: affent.DefaultSummaryTriggerMsgs,
			KeepFirst:   2,
			KeepLast:    affent.DefaultSummaryKeepLast,
		},
	}

	if err := loop.EnsureSystemPrompt(""); err != nil {
		log.Fatal(err)
	}

	turnID, err := loop.SendUser(ctx, "Inspect this workspace.")
	if err != nil {
		log.Fatal(err)
	}

	_ = turnID
	// Consume events until the matching turn.end event.
}
```

The default system prompt assumes a developer workspace with shell and file
tools. Integrations should provide their own prompt when the surrounding product
has different capabilities or policy.

## Architecture

### Loop

`Loop` is the runtime object for one conversation. It appends the user message,
streams model output, dispatches tools, persists messages, publishes SSE events,
and stops when the model returns a final answer or the turn budget is exhausted.

Important defaults:

| setting | default |
| --- | --- |
| `MaxTurnSteps` | `10` |
| `PerCallTimeout` | `3m` |
| `MaxTransientRetries` | `3` |
| `TransientBackoff` | `4s` |
| model-facing tool result cap | `8 KiB` |
| event-preview tool result cap | `4 KiB` |

### Tools

Affent includes:

- `shell`
- `read_file`
- `write_file`
- `edit_file`
- `list_files`
- `memory` when a `MemoryStore` is configured
- `session_search` when a sessions directory is configured

File tools are scoped to the configured workspace. Relative paths are resolved
inside the workspace; absolute paths must also resolve inside it. Symlinks are
checked before file writes. `read_file` refuses binary-looking files using a
NUL-byte heuristic.

`shell` runs through `executor.Executor`. The built-in implementations are:

| executor | isolation | notes |
| --- | --- | --- |
| `LocalExecutor` | none | Runs on the host. Use only when the caller already provides isolation. |
| `DockerExecExecutor` | existing container | Uses `docker exec`; the caller manages container lifecycle. |

Custom sandboxes can implement `executor.Executor` and optionally
`executor.FileOps`.

### Events

Affent emits a canonical SSE event stream. The same event stream is used by the
CLI trace writer, HTTP server, tests, and direct Go integrations.

| event | meaning |
| --- | --- |
| `turn.start` | A user turn was accepted. |
| `user.message` | Echo of the user message. |
| `thinking.delta` | Incremental reasoning content when the provider exposes it. |
| `thinking.done` | Completed reasoning block. |
| `message.delta` | Incremental assistant text. |
| `message.done` | Completed assistant message. |
| `tool.request` | Model requested a tool call. |
| `tool.output` | Optional live tool output stream. |
| `tool.result` | Tool call completed. |
| `file.changed` | Optional file-change notification. |
| `usage` | Token usage for the turn. |
| `turn.end` | Turn completed, failed, was cancelled, or hit max turns. |
| `error` | Recoverable or terminal error event. |

Every event has a monotonically increasing per-loop sequence id.

### Context Compaction

Long conversations can be compacted by attaching a `Compactor`.
`LLMSummaryCompactor` keeps the beginning of the conversation, a rolling summary
of older work, and the most recent messages. It also avoids splitting an
assistant tool call from its corresponding tool result.

Compaction has two activation paths:

- proactive compaction after a configured message threshold
- reactive compaction when the upstream provider reports context overflow

### Project Context

When `Loop.ProjectContextDir` is set, Affent loads recognized project
instruction files and appends them to the system prompt at session start.

Recognized files:

```text
AGENTS.md
CLAUDE.md
CONVENTIONS.md
.cursorrules
.clinerules
.clinerules.md
GEMINI.md
```

These files are read-only from Affent's perspective.

### Persistent Memory

Memory is disabled by default. Configure a `MemoryStore` or pass
`affentctl --memory` to enable it.

The default `FileMemoryStore` uses a topic-bucketed Markdown layout:

```text
<workspace>/.affent/memory/core.md
<workspace>/.affent/memory/topics/general.md
<workspace>/.affent/memory/topics/<topic>.md
$XDG_CONFIG_HOME/affent/USER.md
```

`core.md`, `topics/general.md`, and `USER.md` are injected into the system prompt
at session start. Other topic files are retrieved on demand through the
`memory` tool's search action. This keeps durable facts available without
turning the prompt into a long-term database.

The memory tool supports:

```text
add
replace
remove
search
list
```

Workspace memory is topic-bucketed. User memory is a single cross-workspace
profile.

### Session Search

`session_search` retrieves snippets from prior JSONL conversation logs in the
same workspace. It is intended for transcript recall: previous conclusions,
commands, or discussions. Persistent memory is for durable facts that should
remain available across sessions.

## MCP

Affent supports MCP servers over stdio and streamable HTTP. Tools are registered
into the same `Registry` as built-ins and are namespaced as:

```text
<server>_<tool>
```

Example `affentctl --mcp-config` file:

```json
{
  "servers": [
    {
      "name": "git",
      "command": "uvx",
      "args": ["mcp-server-git", "--repository", "./workspace"]
    },
    {
      "name": "internal",
      "url": "http://localhost:8123/mcp",
      "headers": {
        "X-Auth-Token": "secret"
      }
    }
  ]
}
```

## Optional Extras

### Web

`extras/web` provides:

- `web_fetch`
- `web_search`

`web_fetch` extracts readable page content and converts it to Markdown.
`web_search` uses a pluggable `SearchProvider`; the default provider reads
`TAVILY_API_KEY`.

```go
import affentweb "github.com/affinefoundation/affent/extras/web"

affentweb.RegisterFetch(reg, affentweb.FetchConfig{})
affentweb.RegisterAll(reg, affentweb.Options{})
```

### Browser

`extras/browser` provides Chromium-backed browser tools using the common
snapshot/ref interaction pattern: `browser_snapshot` returns stable element
references, and action tools operate on those references.

```go
import affentbrowser "github.com/affinefoundation/affent/extras/browser"

sess, err := affentbrowser.NewSession(affentbrowser.SessionConfig{
	Headless: true,
})
if err != nil {
	panic(err)
}
defer sess.Close()

affentbrowser.RegisterAll(reg, sess, affentbrowser.Options{})
```

## HTTP Server

`cmd/affentserve` runs Affent behind an HTTP API.

Endpoints:

```text
GET    /healthz
GET    /v1/models
GET    /v1/stats
POST   /v1/chat/completions
GET    /v1/sessions/{id}/events
DELETE /v1/sessions/{id}
```

`/v1/chat/completions` is OpenAI-compatible. It supports streaming responses
and exposes the active session id through `X-Affent-Session-Id` and
`affent_session_id`.

Session id precedence:

1. `X-Affent-Session-Id` request header
2. `affent_session_id` request body field
3. `session_id` request body field
4. new session

Server features are opt-in:

```text
--browser
--browser-screenshot
--web
--web-search
--builtins
--memory
```

`--builtins` enables shell and file tools. It should only be enabled when the
server process is already isolated.

## Security Model

Affent is an agent runtime, not a security sandbox.

The built-in file tools enforce workspace path checks, and the executor
interface makes it straightforward to place shell execution behind Docker or a
custom sandbox. Those protections are defense in depth. If untrusted users or
untrusted model outputs can drive tools, the deployment must provide the real
isolation boundary.

Recommended production practices:

- Run tool execution in an isolated container, VM, or remote sandbox.
- Do not enable `--builtins` on a shared host without isolation.
- Treat browser and web tools as network-capable capabilities.
- Gate `affentserve` with `--auth-token` or an upstream proxy.
- Keep persistent memory scoped to the user or workspace that owns it.

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

## License

This repository does not currently declare a license file.
