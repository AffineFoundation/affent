# Affent Agent Runtime Maturity Calibration

Date: 2026-05-29

This draft calibrates Affent's next stage against concrete evidence from the
current codebase, a real long-run eval, OpenAI Codex's public implementation,
and Anthropic Claude Code's public documentation. It is not a product pitch and
does not propose copying either system.

## Current Evidence

The latest realistic `longrun-code-clone-modify-push-local-remote` run now
passes after eval hygiene fixes:

```text
go run ./cmd/affenteval --scenario longrun-code-clone-modify-push-local-remote --runtime-all-tools --temperature 0 --timeout 120s --keep-workspaces
```

Result:

- Scenario passed.
- Verifier proved clone, test, fix, clean git status, commit, push, and fresh remote clone.
- Previous false negatives from Go toolchain environment mismatch and non-loop checkpoint expectations are gone.
- The run still had two tool failures caused by the model guessing `/workspace` and then trying an unbounded `find /`.

This is the important signal: Affent can complete the end-to-end coding task,
but the runtime still does not make the canonical workspace/task state obvious
enough to prevent early path drift.

Committed stage:

- `d62512d8 Stabilize long-run eval checks`

## Codex Implementation Lessons

Reference inspected:

- `openai/codex` cloned at `42c80385cd6e2c8efc8b85c39bb9bde5edf01fc3`.
- Local path: `/tmp/affent-research-codex`.

Codex has moved beyond a simple CLI loop. Its app-server protocol treats agent
work as durable threads and turns:

- `thread/start`, `thread/resume`, `thread/fork`, `thread/compact/start`,
  `thread/rollback`, `thread/turns/list`, and `thread/turns/items/list` are
  explicit protocol surfaces.
- `turn/start`, `turn/steer`, and `turn/interrupt` are separate operations.
- Thread and turn params carry `cwd`, `runtime_workspace_roots`,
  permission/sandbox/profile settings, model settings, and additional context.
- Resume/fork responses return instruction sources, runtime workspace roots,
  approval policy, sandbox policy, and permission profile provenance.

This is relevant to Affent because Workbench cannot become a real control
surface if all state is hidden behind a chat turn. Affent should keep its
file-backed design, but the WebUI/API should converge toward explicit
thread/session/turn operations: start, steer, interrupt, compact, resume,
fork, rollback, list turns, list turn items.

Codex also makes context and compaction configurable at runtime:

- Config includes model context window, auto-compaction threshold, compaction
  scope, compact prompt, project doc size limits, instruction toggles, and
  inclusion controls for permissions/apps/collaboration/skills/environment
  instructions.
- Tests show compacted history is token-limited, appends summary, refreshes
  developer messages, and reinjects initial context.

Affent should not add more prompt text to solve state drift. It should make
context assembly auditable and structured: task state, instruction sources,
memory summaries, skills, workspace roots, and verification evidence should be
separate context inputs with traceable provenance.

Codex's project instruction model is also conservative:

- `AGENTS.md` is treated as user instructions and has explicit max-byte
  handling and fallback filenames.
- Skills/plugins are capability bundles with manifests, enabled/disabled
  state, roots, descriptions, and tool permissions.

Affent already has project context and skills. The missing part is less about
format support and more about governance: why a skill was activated, which
task-state condition triggered it, what tools it requires, and which eval proves
it helps.

## Claude Code Lessons

Reference inspected:

- Claude Code official memory docs:
  https://code.claude.com/docs/en/memory
- Claude Code official subagent docs:
  https://code.claude.com/docs/en/sub-agents
- Claude Code official hooks docs:
  https://code.claude.com/docs/en/hooks
- Claude Code official workflows docs:
  https://code.claude.com/docs/en/common-workflows
- Claude Code official settings docs:
  https://code.claude.com/docs/en/settings

Claude Code separates behavioral guidance from enforced configuration. Its
docs explicitly say CLAUDE.md and auto memory are context, not enforcement;
actions that must be blocked should be implemented through settings or hooks.
Affent should follow the same separation:

- Memory and project docs should guide.
- Tool policy and permissions should enforce.
- Workbench should display both, so users know whether a behavior is guidance
  or a hard rule.

Claude Code's auto memory design is especially relevant:

- Auto memory is on by default.
- Claude decides what is worth remembering.
- The memory directory is plain markdown and machine-local.
- `MEMORY.md` is a concise index; only the first 200 lines or 25KB are loaded
  at startup.
- Detailed topic files are read on demand.
- `/memory` lets users audit and edit what was loaded or saved.

Affent should not hardcode when to write memory. The agent should decide based
on a schema, while runtime enforces scope, size, dedupe, provenance, and audit.
Long-term memory must not store current task progress. Task progress belongs in
session task state.

Claude Code's subagents are also pragmatic:

- They preserve main-context space.
- They can be tool-restricted.
- Built-in Explore and Plan are read-only.
- Delegation is selected by description and task fit, not by a giant workflow
  script.

Affent's focused tasks/subagents are directionally aligned. The next maturity
step is to connect delegation results into canonical task state and Workbench
evidence, not to create more subagent types.

Claude Code's hooks show what mature lifecycle control looks like: SessionStart,
InstructionsLoaded, UserPromptSubmit, PreToolUse, PostToolUse,
PostToolUseFailure, PostToolBatch, Stop, PreCompact, PostCompact, SessionEnd,
and more. Affent does not need to expose all of these now, but it should define
its own lifecycle event contract around the same principle: external automation
and WebUI should observe and influence lifecycle points without patching prompts.

## Affent Maturity Gap

Affent's current architecture is strong in three areas:

- File-backed durable state.
- Trace/eval observability.
- Tool governance and recovery signals.

The main maturity gap is now state unification.

Today plan, LOOP.md, memory, compaction, session search, skill, project context,
and tool traces can all imply "what to do next". That is not sustainable for
long-running work. The system needs a canonical task-state view that each
mechanism can read from or contribute to without competing for authority.

This should not become a giant mutable JSON object. A better design is:

- Keep event logs and sidecar files as sources of truth.
- Derive a canonical `TaskState` view at runtime.
- Let tools update specific facts through typed events or typed state patches.
- Let Workbench display the same derived view.
- Let compaction and resume inject this view instead of ad hoc summaries.

Minimum `TaskState` fields:

- Objective.
- Current step.
- Constraints.
- Known facts.
- Changed files.
- Attempted actions.
- Failed actions.
- Evidence.
- Verification state.
- Open questions.
- Next step.

The recent eval path failure maps cleanly to this: workspace root and available
workspace entries should be visible as task/runtime state, not guessed by the
model and then corrected by failed tools.

## Next Product Direction

### 1. Make TaskState The Control Plane

Short-term deliverable:

- Add a derived `TaskState` model from existing plan, loop state, trace, tool
  results, git/workspace signals, and verification state.
- Start read-only: expose it in trace/debug/WebUI before allowing writes.
- Use evals to prove it reduces path drift, repeated failed actions, and
  post-compaction loss of current step.

Non-goal:

- Do not replace all existing mechanisms at once.
- Do not make every module mutate one central object.

### 2. Fix Workspace Semantics Systemically

The model repeatedly guessed `/workspace` even though the workspace root is
already the default. This should be solved by runtime state and tool UX:

- Runtime surface should expose "workspace root is the current default" as a
  structured field for UI and context assembly.
- Tool errors for absolute workspace guesses should be classified more
  specifically, for example `workspace_path_escape`, not generic `tool_failed`.
- Workbench should show last cwd, workspace roots, known top-level entries, and
  failed path attempts.
- Evals should track whether the model used absolute workspace paths, not just
  whether the final task passed.

### 3. Make Memory Default, Small, And Auditable

Affent should use a file-backed memory layout closer to Claude Code's practical
shape:

- A concise index loaded by default.
- Topic files read on demand.
- Agent-decided writes through a structured memory tool.
- Runtime-enforced dedupe, size limits, source metadata, confidence, and scope.
- Workbench UI for loaded memory, recalled memory, write proposals, accepted
  writes, rejected writes, and edits.

Memory layers:

- User preferences.
- Project facts.
- Design decisions.
- Failure lessons.
- Current task state.

Only the first four belong in durable memory. Current task state belongs in
session-local state and should not pollute long-term memory.

### 4. Treat Skills As Tested Capabilities, Not Prompt Packs

Default built-in skills should target known failure modes:

- Repo inspect.
- Code repair.
- Test failure diagnosis.
- Verify before final.
- Memory hygiene.
- Web evidence.
- Skill install/review.

Each built-in skill should have:

- Trigger conditions tied to task state or failure kind.
- Required tools.
- Expected state/evidence outputs.
- Evals proving reduced failure rate or reduced token/tool cost.
- Workbench visibility: why it activated, what it changed, and what tools it
  used.

User-installed skills should remain possible, but the core product should not
depend on a plugin marketplace to be reliable.

### 5. Make Workbench A Session Operating Surface

Workbench should not be a terminal clone or a side panel collection. It should
be the visual operating surface for the same session truth:

- Task: objective, current step, constraints, open questions.
- Evidence: files, commands, sources, tool outputs, artifacts.
- Changes: diff, touched files, generated files.
- Verification: tests, build, verifier, status, last failure.
- Context: active project docs, memory, skills, loop, compaction summary.
- Control: start, steer, interrupt, compact, resume, fork, rollback.
- Trace: raw events and derived timeline.

Codex's thread/turn API is a useful direction here. Affent can keep its simpler
Go/file architecture, but the WebUI should expose the same category of controls
instead of treating one user message as one opaque operation.

### 6. Make Documentation Stronger By Splitting Roles

Affent docs should become stronger by being clearer, not longer:

- README: product identity, quick start, Docker one-command start, high-level
  architecture, links.
- Technical manual: CLI/server/config/tools/state/eval operations.
- Architecture docs: session state, TaskState, memory, skills, context,
  compaction, WebUI data model.
- Eval cookbook: how to add realistic scenarios and interpret failures.
- Workbench design: information architecture and session truth mapping.

The README should remain polished and short. Detailed operational material
belongs in docs, not in the README.

## Immediate Next Steps

1. Add a read-only `TaskState` derivation path using current trace/plan/loop
   evidence.
2. Add a realistic eval that specifically fails on unnecessary `/workspace`
   guesses and broad root scans even when the task eventually succeeds.
3. Improve tool failure classification for workspace path escapes.
4. Expose workspace root/default cwd/top-level entries in runtime surface and
   Workbench context.
5. Convert memory design into a concrete file layout and eval plan before
   changing write behavior.
6. Keep using the clone-modify-test-commit-push scenario as the first regression
   gate for agent maturity.
