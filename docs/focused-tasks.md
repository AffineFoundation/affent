# Focused Tasks

Focused Tasks are Affent's bounded delegation surface for short, typed
auxiliary work. The parent agent can ask the runtime to run a task such as
recall, exploration, research, verification, or review in an isolated child
session. The child gets a task-specific prompt, a restricted tool set, and a
small turn budget. Only a structured result is returned to the parent context.

The feature is exposed through the `run_task` tool.

## Goals

Focused Tasks are designed to:

- Keep recall, exploration, research, verification, and review work out of the
  parent conversation unless the final result is useful.
- Give each auxiliary task a narrow prompt, tool set, and output contract.
- Improve reliability for smaller or less consistent models.
- Preserve full child traces for replay, debugging, UI inspection, and eval.
- Return compact, structured, evidence-carrying results to the parent agent.

Focused Tasks are not an open-ended multi-agent orchestration system. Task
types are finite, recursive delegation is disabled, and child tools are
whitelisted by profile.

## Task Types

### `recall`

Search durable memory and prior session transcripts for context relevant to the
current objective.

Allowed tools:

- Memory `search` and `list`.
- `session_search`.

Disallowed tools:

- Shell.
- File writes or edits.
- Web and browser tools.
- MCP tools.

Expected output:

- Relevant remembered facts.
- Source references.
- Confidence.
- Explicit `not_found` entries when no useful context exists.

### `explore`

Inspect the current workspace to locate files, symbols, modules, or project
structure relevant to the objective.

Allowed tools:

- `list_files`.
- `read_file`.
- Read-only shell where available.
- Optional `session_search`.

Disallowed tools:

- File writes or edits.
- Destructive or broad shell commands.
- Web and browser tools.

Expected output:

- Relevant files and modules.
- Local structure or dependency notes.
- Suggested next inspection points.
- Unchecked but likely relevant areas.

### `research`

Fetch external facts needed for the objective.

Allowed tools:

- `web_fetch`.
- `web_search`, when configured.

Disallowed tools:

- Workspace tools.
- Shell.
- Memory mutation.

Expected output:

- Factual conclusions.
- Source URLs or page references.
- Date or freshness notes for time-sensitive facts.
- Conflicts or uncertainty across sources.

### `verify`

Check a specific claim, assumption, file state, or change with the smallest
reasonable amount of evidence.

Allowed tools:

- `list_files`.
- `read_file`.
- Read-only shell checks where available.
- Optional `session_search`.

Disallowed tools:

- File writes or edits.
- Broad unrelated exploration.

Expected output:

- Pass/fail or supported/unsupported result.
- The checks that were performed.
- Key evidence.
- Remaining risk.
- Minimal diagnostics when verification fails.

### `review`

Inspect a change, file, plan, or claim for bugs, edge cases, security risks, and
missing tests.

Allowed tools:

- `list_files`.
- `read_file`.
- Optional `session_search`.
- Read-only shell checks where available.

Disallowed tools:

- File writes or edits.

Expected output:

- Findings ordered by severity.
- File or source references where applicable.
- Evidence for each finding.
- Open questions.
- Test gaps and residual risk.

## Tool Contract

`run_task` accepts:

```json
{
  "task_type": "recall",
  "objective": "Find previous preferences relevant to README wording.",
  "max_turns": 4
}
```

Fields:

- `task_type`: one of `recall`, `explore`, `research`, `verify`, or `review`.
- `objective`: a specific task objective.
- `max_turns`: optional child turn budget. The runtime applies profile defaults
  and hard caps.

The runtime may remove task types from the advertised schema when a deployment
does not provide the required tools. For example, `research` is not advertised
when web tools are unavailable.

## Output Contract

Focused Tasks return a structured JSON object to the parent agent:

```json
{
  "task_type": "recall",
  "ok": true,
  "summary": "Found one relevant preference.",
  "findings": [
    {
      "claim": "README should be a project doorway, not a technical manual.",
      "evidence": "User explicitly requested high-level README positioning.",
      "source": "session:...",
      "confidence": "high"
    }
  ],
  "not_found": [],
  "warnings": [],
  "suggested_next": []
}
```

Required behavior:

- The child should return only information relevant to the objective.
- Findings should carry evidence and source references whenever possible.
- Missing information should be reported through `not_found`, not invented.
- Uncertainty, stale sources, partial failures, and parse fallbacks should be
  reported through `warnings`.
- The parent conversation receives the structured result, not the full child
  conversation.

If the child emits non-JSON text, Affent wraps the text in a structured fallback
result and adds a warning so the parent can still consume it with an explicit
downgrade signal.

Parent-side post-task policy treats `warnings` as an open-gap signal and allows
small verification after `run_task`. A definitive `not_found` entry is still a
valid task result, so it does not by itself reopen broad parent-side
exploration.

## Context Isolation

Focused Tasks isolate intermediate work from the parent conversation:

- The child has its own conversation and transcript.
- The child cannot recursively call `run_task` or `subagent_run`.
- The parent receives only the bounded structured result.
- Child tool outputs are capped before they can affect parent context.
- The parent may cite returned sources but must not imply it inspected hidden
  child steps.

This design keeps exploration and retrieval noise from accumulating in the main
model context while preserving observability through traces.

## Trace And Observability

Focused Tasks are visible in runtime events:

- `tool.request` for `run_task` includes
  `delegation: {"kind":"focused_task","task_type":"..."}`.
- Matching `tool.result` events mirror the same delegation metadata.
- Child transcripts are persisted under the session state tree.
- Eval JSONL aggregation can count focused-task calls by type and error status.

Trace consumers can group delegated work without parsing tool arguments.

## Registration

`affentctl` registers Focused Tasks by default. Disable them with:

```bash
--focused-tasks=false
AFFENTCTL_FOCUSED_TASKS=false
```

Config aliases:

```json
{
  "focused_tasks": false,
  "enable_focused_tasks": false
}
```

`affentserve` also registers Focused Tasks by default. Disable them with:

```bash
--focused-tasks=false
AFFENTSERVE_FOCUSED_TASKS=false
```

Focused Tasks are independent of the lower-level `subagent_run` surface. A
deployment may expose one, both, or neither depending on its tool policy.

## Runtime Limits

The current implementation enforces:

- Finite task-type enum.
- Per-profile tool whitelist.
- No file write or edit tools in focused-task children.
- No recursive `run_task` or `subagent_run` inside focused-task children.
- Hard maximum child turn budget.
- Per-turn cap on parent `run_task` calls.
- Output caps before results are returned to the parent context.
- Evidence sanitization for control characters and ANSI escapes.
- Read-only wrappers for shell and memory tools where applicable.

These limits keep Focused Tasks bounded and observable rather than turning them
into an uncontrolled agent network.

## Relationship To `subagent_run`

`subagent_run` is the lower-level isolated child runtime primitive. It accepts a
mode and task description and is useful for broader exploration or review.

`run_task` is the productized, typed delegation surface:

- Task types are finite.
- Prompts are task-specific.
- Tool sets are profile-specific.
- Output schema is structured.
- Trace metadata identifies the task type.

Both surfaces may share child-loop implementation details, but they serve
different product roles.

## Current Status

Implemented:

- `run_task` tool.
- Task types: `recall`, `explore`, `research`, `verify`, and `review`.
- Per-profile prompts, default turn budgets, and tool whitelists.
- Schema filtering when a deployment lacks required tools.
- Structured result envelope with fallback handling.
- Child transcript persistence.
- Delegation metadata on `tool.request` and `tool.result`.
- Eval JSONL aggregation for focused-task and subagent usage.
- Parent-side policies and loop guards to reduce ignored delegation requests,
  repeated exploration after success, and excessive delegation in one turn.

Not currently implemented:

- UI controls that manually trigger Focused Tasks.
- Automatic runtime policy that starts Focused Tasks without model tool calls.
- User-defined task profiles in config files.
- Separate artifact storage for oversized focused-task results.

## Evaluation Targets

Focused Tasks should be evaluated through trace-visible behavior:

- `recall`: relevant memory/session hits, irrelevant recall rate, source
  presence, and confidence accuracy.
- `explore`: relevant file discovery, unnecessary file reads, and bounded tool
  calls.
- `research`: citation presence, unsupported claim count, and source freshness.
- `verify`: pass/fail accuracy, command or evidence presence, and residual risk
  reporting.
- `review`: finding relevance, severity accuracy, evidence quality, and test gap
  coverage.
