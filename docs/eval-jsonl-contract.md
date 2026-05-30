# Eval JSONL Contract

`affenteval --jsonl` emits one JSON object per line. Each line carries
`schema_version`; consumers should reject future versions they do not
understand.

The JSONL stream is intended for automation, dashboards, and model/runtime
comparisons. It is derived from the same runtime traces described in
[`event-trace-contract.md`](event-trace-contract.md), but it is not a raw event
log. Scenario records summarize one executed case; summary records aggregate a
batch, including quality gates, capability/domain coverage, retained artifact
paths, and bounded examples that point operators to the first traces worth
opening.

The contract is append-friendly. New optional metrics may appear without a
schema bump. Consumers should ignore fields they do not use and rely on
`schema_version` only for incompatible changes.

## Schema Version 1

Shared metadata fields:

- `schema_version`: integer eval JSONL schema version.
- `type`: `scenario` or `summary`.
- `suite`: optional suite label.
- `model`: optional model id.
- `provider_label`: optional provider label for cross-run comparisons.
- `executor`: eval executor label, such as `local`, `sandbox`, or
  `docker:<container>`.
- `temperature`: sampling temperature passed to `affentctl`.
- `top_p`: optional top-p sampling value passed to `affentctl`.
- `max_tokens`: optional max output token limit passed to `affentctl`.
- `seed`: optional deterministic-sampling seed passed to `affentctl`.
- `runtime_eval_mode`: optional boolean, present when the eval runner passed
  `affentctl run --eval-mode`. This is the default for `affenteval`.
- `runtime_tools`: optional comma-separated tool allowlist passed via
  `--runtime-tools` / `affentctl --eval-tools`.
- `runtime_all_tools`: optional boolean, present when the eval runner passed
  `--runtime-all-tools` / `affentctl --eval-all-tools`.
- `runtime_memory`: optional boolean, present when the eval runner explicitly
  opted memory back into the runtime with `--runtime-memory`.
- `runtime_web`: optional boolean, present when the eval runner explicitly
  enabled web search/runtime web tools with `--runtime-web`.
- `runtime_browser`: optional boolean, present when the eval runner explicitly
  enabled browser runtime tools with `--runtime-browser`.
- `trace_deltas`: optional boolean, present when the eval runner retained
  streaming message delta events with `--trace-deltas`. When omitted, the
  runner passed `affentctl --trace-skip-deltas` to keep traces compact.
- `runtime_mcp`: optional boolean, present when the eval runner passed a runtime
  MCP config. The config path itself is not written to JSONL.
- `timeout_ms`: per-scenario timeout in milliseconds.
- `quality_profile`: optional string, present when the eval runner expanded a
  built-in quality gate profile such as `longrun` or `web-evidence`. Profile
  thresholds are still written as ordinary gate fields, and explicitly supplied
  gate flags override profile defaults.
- `min_pass_rate`, `min_completion_rate`, `min_memory_update_rate`,
  `min_loop_turn_checkpoint_rate`, `min_loop_protocol_feed_rate`,
  `min_loop_protocol_calibration_request_rate`,
  `min_loop_protocol_calibration_rate`, `min_runtime_surface_rate`,
  `min_trace_event_rate`,
  `min_source_network_rate`,
  `min_source_access_verified_rate`,
  `min_expectation_capability_pass_rate`,
  `min_each_expectation_capability_pass_rate`,
  `min_expectation_domain_pass_rate`,
  `min_each_expectation_domain_pass_rate`,
  `min_session_search_context_hit_rate`,
  `min_session_search_matched_terms_per_call`,
  `min_tool_repair_success_rate`, `min_verifier_pass_rate`,
  `max_focused_task_error_rate`, `max_forced_no_tools_rate`,
  `max_loop_guard_intervention_rate`, `max_plan_error_rate`,
  `max_memory_search_miss_rate`,
  `max_source_discovery_only_rate`,
  `max_source_dynamic_partial_rate`, `max_subagent_error_rate`,
  `max_tool_error_rate`,
  `max_tool_context_truncation_rate`, `max_tool_result_truncation_rate`,
  `max_avg_runtime_errors`, `max_avg_context_compactions`,
  `max_avg_reactive_context_compactions`,
  `max_avg_context_removed_messages`, `max_avg_context_summary_bytes`,
  `max_avg_context_summary_missing`, `max_avg_context_summary_empty`,
  `max_avg_context_injections`, `max_avg_context_injection_bytes`,
  `max_avg_context_injection_estimated_tokens`,
  `max_avg_tool_calls`, `max_avg_duration_ms`, `max_avg_total_tokens`, and
  `max_scenario_total_tokens`: optional quality gate thresholds configured for
  the run. Disabled gates are omitted.
- `min_expectation_domain_source_access_verified_rates`,
  `max_expectation_domain_avg_total_tokens`,
  `max_expectation_domain_avg_tool_calls`,
  `max_expectation_domain_avg_runtime_errors`,
  `max_expectation_domain_tool_error_rates`, and
  `max_expectation_domain_loop_guard_intervention_rates`: optional maps from
  workload domain to a domain-specific quality threshold. These preserve
  experiment-specific CI conditions such as "Bittensor research must stay under
  this token budget" or "web evidence must keep verified SourceAccess above
  this rate" without forcing one global threshold across unlike task domains.
- `max_debug_brief_tag_rates`: optional map of `debug_brief` tag to maximum
  scenario rate. This lets profiles fail specific triage patterns, such as
  weak session recall context or dynamic web evidence without network-backed
  reads, without adding a bespoke top-level metric for every future diagnostic
  tag. The built-in `longrun` profile also gates failed, not-run, and abnormal
  verifier tags so code/PR verification regressions remain visible even when
  aggregate pass-rate gates are broad. CLI overrides use
  `--max-debug-brief-tag-rate tag=rate`; `tag=-1` disables a profile default
  for that tag.
- `required_expectation_capabilities`: optional list of expectation capability
  families that must be represented by at least one scenario in the batch.
  This is a coverage guard for CI and model comparisons: it catches a run that
  accidentally skipped, filtered, or misconfigured the scenario family a gate
  was meant to exercise.
- `required_expectation_domains`: optional list of realistic task-domain labels
  that must be represented by at least one scenario in the batch. This guards
  against a nominal long-run or web batch passing while accidentally omitting
  key workloads such as market analysis, Bittensor research, code/PR execution,
  web evidence, or long-run recovery.

## Scenario Record

Scenario records describe one eval case:

- `scenario`: scenario id.
- `ok`: whether the scenario passed every check.
- `duration_ms`: wall-clock scenario duration.
- `workspace`: scenario workspace path.
- `trace_path`: trace JSONL path.
- `trace_schema_version`: parsed runtime trace schema version, when available.
- `trace_events`: total parsed trace events, when a trace was parsed.
- `trace_event_types`: per-event-type counts from the parsed trace, such as
  `message.delta`, `tool.request`, `tool.result`, and `runtime.surface`.
- `debug_manifest_path`: retained debug manifest path, when the workspace was
  not removed.
- `timeline_path`: retained human-readable timeline path, when the workspace
  was not removed. It summarizes runtime surface, tools, result previews,
  truncation/artifact pointers, child transcript refs, loop decisions,
  compactions, and errors.
- `final_text_path`, `stdout_path`, `stderr_path`: retained files containing
  the final assistant text, full `affentctl` stdout, and full `affentctl`
  stderr for local debugging.
- `affentctl_command`: redacted command argv used for the scenario run. API
  keys are replaced with `<redacted>`.
- `prompt`: the single prompt, or a compact turn-labeled display for multi-turn
  scenarios.
- `prompts`: optional ordered prompt list for multi-turn scenarios. These
  scenarios must use a stable session id so successive `affentctl run` calls
  append to the same conversation and trace.
- `expectations`: optional structured copy of the scenario's declarative checks,
  including required/forbidden tools, tool counts, source-access requirements,
  direct `session_search` hit requirements, no-hit
  `required_recent_session_search` anchor requirements, loop-decision
  requirements, loop protocol calibration/feed, active plan
  checkpoint/current-situation, and last-turn checkpoint requirements,
  context-compaction requirements, context-injection source requirements such as
  `required_context_injection_sources.final_evidence_digest`, optional
  `required_context_compaction_reasons` checks for threshold, request-pressure,
  input-budget, or overflow compaction paths, optional
  `required_context_loop_protocol_anchor_text` checks for post-compaction
  `LOOP.md` recovery anchors, optional
  `require_loop_protocol_full_after_compaction` sequence checks,
  task-state request provenance plus optional
  `required_task_state_attempted_actions`,
  `required_task_state_changed_files`, and `required_task_state_evidence`
  checks for canonical TaskState reviewability,
  plan/delegation constraints, protected files, optional
  `required_turn_end_reason` for intentional non-completed endings, optional
  `setup_commands` that prepared the workspace before the agent ran, and related
  max-turn/compaction settings such as `compact_trigger` and
  `compact_trigger_input_tokens`. This
  lets batch-analysis scripts inspect why a scenario passed or failed without
  reopening the debug manifest.
  Loop-decision checks can assert route-control events such as
  `evidence_quality=defer/source_access_dynamic_partial` or
  `research_checkpoint=trigger/external_calibration_requested`, which lets
  long-run evals prove that the runtime surfaced a needed external-calibration
  checkpoint instead of relying on an invisible prompt nudge. Live-web
  research checkpoint cases cover both direct parent `web_fetch` evidence and
  delegated `run_task(research)` evidence, so result consumers can distinguish
  source-heavy parent reads from compact child research handoffs. Delegated
  research cases can additionally require `required_focused_task_source_counts`
  or `required_subagent_source_counts` so a child task must return
  source-backed findings/report lines, not just a successful delegation call.
  Scenario expectations may also include `domains`, a curated task-domain list
  such as `market`, `bittensor`, `code_pr`, `web_evidence`, or
  `longrun_recovery`; these labels are not capability inference and are meant
  for comparing realistic workload coverage.
- `expectation_capability_names`, `expectation_capability_outcome`,
  `expectation_capability_passed_names`, and
  `expectation_capability_failed_names`: optional scenario-level derived
  capability families and their scenario outcome. These use the same broad
  capability inference as summary records, so consumers can group individual
  scenario rows without reimplementing expectation parsing. Composite
  long-running recovery cases that require loop protocol feeds, no-hit
  recent-session anchors, and a `session_search` to `memory` recovery sequence
  are tagged as `longrun_recovery`. Scenarios that require
  `required_focused_task_source_counts` or
  `required_subagent_source_counts` are also tagged as
  `delegated_source_evidence`, which lets quality profiles fail unsourced child
  research/review paths separately from generic delegation failures.
- `runtime_surface`: compact copy of the latest effective runtime surface when
  the trace reached turn start. It records sorted tool names, broad
  capabilities such as `web_fetch`, `web_search`, `browser`, `memory`, and
  `subagent`, partial `workspace_tools` when only some workspace tools are
  enabled, per-tool workspace path argument policies, per-tool loop-guard call
  caps for capped registered tools, plus key tool-result limits. Retained debug
  manifests include the fuller `runtime_surface` block with per-tool
  group/source metadata.
- `runtime_surface_refresh_by_reason`: scenario-level counts of every
  `runtime.surface.refresh_reason` observed in the trace. Summary records
  aggregate those counts across scenarios, so long-run suites can distinguish
  ordinary turn-start surfaces from post-compaction refreshes and observed
  compact-window calibration.
- `task_state`: optional derived snapshot of the scenario's objective, status,
  current step, constraints, known facts, next step, open questions,
  verification state, changed files, attempted actions, failed actions with
  structured recovery hints, evidence, and runtime/context sources. Go runtime
  and eval surfaces share the canonical `internal/taskstate.Snapshot` JSON
  contract. Successful shell evidence may include structured handoff sources
  such as `git_commit` and `git_push`. It is derived from trace facts for
  audit/debug output and is not fed back into the agent runtime.
- `task_state_status`, `task_state_verification`,
  `task_state_changed_files`, `task_state_attempted_actions`,
  `task_state_failed_actions`, `task_state_evidence`, and
  `task_state_evidence_by_source`: compact task-state columns for dashboards
  that do not need to parse the full `task_state` object. The source map lets
  code/PR suites verify handoff evidence such as `git_commit` and `git_push`
  at summary level.
- `run_exit_code`: `affentctl run` exit code for the scenario.
- `turn_end_reason`: runtime turn end reason, when available. Scenarios default
  to expecting `completed`; set `required_turn_end_reason` when a scenario is
  intentionally validating `max_turns`, `cancelled`, or `error` behavior.
- `tool_calls`: number of tool calls in the trace.
- `tool_errors`: runtime tool error count, limited to non-zero tool exits.
- `tool_repaired`: runtime tool argument repair count.
- `tool_name_canonicalized`: runtime tool name canonicalization count.
- `tool_repair_calls`: count of tool calls that were repaired or
  canonicalized.
- `tool_repair_succeeded`: repaired/canonicalized tool calls with
  `exit_code == 0`.
- `tool_repair_failed`: repaired/canonicalized tool calls with
  `exit_code != 0`.
- `tool_repair_notes`: count of classified runtime repair diagnostics emitted
  on `tool.request` events. Omitted when no repair diagnostics were observed.
- `tool_repair_by_kind`: optional map of repair diagnostic kind to count. Known
  keys include `tool_name`, `malformed_json`, `wrapper_unwrap`,
  `scalar_wrap`, `alias_rename`, `enum_normalization`, `type_coercion`,
  `unknown_field_drop`, and `other`.
  New traces prefer the authoritative `turn.end.tool_stats` repair summary;
  older traces fall back to classifying `tool.request.repair_notes`.
- `tool_repair_examples`: optional bounded examples of repaired or
  canonicalized tool calls. Each sample includes tool index, call id, final
  tool name, original tool name when present, canonicalized/args-repaired
  flags, compact original argument preview, repair notes/kinds, exit code, and
  whether the repaired call succeeded. Summary records include the originating
  scenario for cross-scenario triage.
- `tool_failure_by_kind`: optional map of structured tool failure kind to
  count. New traces prefer `turn.end.tool_stats`; replay of older or partial
  traces may derive counts from per-call `tool.result.failure_kinds`,
  `tool.result.failure_kind`, or structured `Failure: kind=...` result text. A
  single tool result may contribute multiple kinds when the original tool
  failure and a later runtime guard are both present. Web tool failures use
  this field to distinguish blocked pages, empty responses, non-text
  responses, timeouts, search no-results, stale or non-interactable browser
  refs, argument errors, HTTP/network classes, and loop-guard rejections such
  as repeated failed URL/query inputs. Failed shell commands without a more
  specific structured kind are grouped as `command_failed`; other failed tools
  without structured metadata are grouped as `tool_failed`. This field can
  count Web no-evidence results whose `tool.result.exit_code` is `0`; it is
  the right field for retrieval quality diagnostics, while `tool_errors` is the
  right field for non-zero tool exits.
- `tool_failure_hints`: optional map of structured tool failure kind to a short
  operator hint explaining likely cause and next diagnostic action.
- `tool_failure_examples`: optional map of failure kind to bounded sample tool
  failures. Scenario records derive samples from that scenario; summary
  records carry a small cross-scenario sample per kind. Each sample includes
  the tool name, a compact
  `args_summary` such as `url="..."` or `query="..."`, the exit code, and a
  compact `result_summary` containing the failure reason and `Next:` guidance
  when the trace carried it. This is diagnostic context for operators; counts
  in `tool_failure_by_kind` remain the aggregation source of truth. Summary
  records include the originating scenario.
- `runtime_error_by_kind`: optional map of structured `error.failure_kind`
  counts, such as `llm_timeout`, `llm_incomplete_stream`, or
  `context_overflow`. This is separate from `failure_kinds` so recoverable LLM
  retry errors can be tracked without turning an otherwise passing scenario
  into a scenario assertion failure.
- `runtime_error_hints`: optional map of runtime error kind to a short
  operator hint explaining likely cause and next diagnostic action.
- `runtime_error_examples`: optional map of runtime error kind to bounded
  message samples. Scenario records derive samples from that scenario; summary
  records carry a small cross-scenario sample per kind. These preserve the
  specific timeout, endpoint, or incomplete-stream detail from `error` events
  without requiring operators to inspect the full trace. Summary records
  include the originating scenario.
- `debug_brief`: optional machine-readable triage block for failed or
  diagnostic-heavy scenarios. It contains sorted `tags` such as
  `outcome:failed`, `tool_failure:blocked`, `runtime_error:llm_timeout`,
  `source_dynamic_partial`, `source_dynamic_without_network`,
  `source_dynamic_without_decision`, `source_unverified_all`,
  `source_discovery_only_all`,
  `source_network:missing_response_diagnostics`,
  `browser_scroll`, `browser_scroll:boundary`, `browser_scroll:stuck`,
  `browser_scroll:stuck_without_network`,
  `browser_network`, `browser_network:no_matches`,
  `browser_network:unread_refs`, `browser_network:refs`,
  `loop_protocol:fixture`, `loop_protocol:calibration_backlog`,
  `memory_update:replace`, `empty_recall`,
  `empty_recall:no_recent_sessions`,
  `empty_recall:recent_sessions`,
  `research_checkpoint:no_external_evidence`,
  `loop_guard:forced_no_tools`,
  `recall:no_context`, `recall:no_matched_terms`,
  `recall:weak_context`, `recall:weak_matched_terms`,
  `tool_repair:failed`, `tool_repair:type_coercion`,
  `delegation_error:focused_task`, `plan_error`, `plan:unfinished`,
  `context_compaction:reactive`,
  `context_compaction:summary_missing`, `context_compaction:summary_empty`,
  `verifier:failed`, `verifier:not_run`, `verifier:abnormal`,
  `verifier:output_truncated`,
  `truncation`, `truncation:tool_context`, and
  `truncation:missing_artifact`, plus ordered `items` with a severity, message,
  inspect targets, and relevant counts. This is the compact "what to inspect
  first" index for long-run batch analysis.
  Loop-guard items point at `loop_guard_examples` first so operators can see
  the blocked tool, guard reason, and suggested next step before opening the
  full tool timeline.
- Retained debug manifests include `recovery_guide`, an optional
  machine-readable next-action block. It includes a concise summary, ordered
  inspect targets
  spanning timeline/manifest/trace/final text/stderr/artifacts/child
  transcripts plus debug-brief sections, the exact redacted `affentctl` rerun
  command, a full-trace rerun command when the compact trace skipped streaming
  deltas, and a copyable continuation prompt for handing the failure back to an
  agent. If the debug brief reports `browser_network:unread_refs`, the prompt
  explicitly routes the next agent to `browser_network_examples`,
  `source_evidence`, and `browser_network_read`, because refs/checks from
  `browser_network` are leads rather than citable `SourceAccess:` evidence.
  Long-run recall and context-pressure tags add bounded next actions too:
  summary-missing/empty context compactions route recovery through persisted
  `LOOP.md`, plan state, session search, memory, or authoritative files;
  recent-session and weak-recall tags route retry through
  `session_search_examples`; memory misses without topic anchors route retry
  through `memory_search_miss_examples` and explicit target/topic discovery;
  loop protocol fixture tags route operators to repair the per-session
  `LOOP.md` or `state.json` lifecycle state before rerunning model evals.
  Research checkpoint evidence-gap tags route operators to
  `loop_decision_examples`, `source_evidence`, and `child_transcripts` before
  treating a route change as externally calibrated. Source evidence satisfies
  this tag only when it is verified or network-backed; discovery-only and
  dynamic-partial page evidence remain weak leads. Delegated evidence only
  satisfies this tag when it came from focused `research`/`web_extract` tasks
  or a `research` subagent; local explore/review delegation remains an internal
  review signal.
  JSONL scenario records expose the manifest location through
  `debug_manifest_path`; they do not inline this block.
- `loop_guard_interventions`: runtime loop guard intervention count.
- `forced_no_tools`: count of forced no-tool follow-up requests after repeated
  blocking loop guard interventions. Soft warnings such as
  `loop_guard_repeated_failures` still count under `loop_guard_interventions`
  but do not by themselves force a no-tool follow-up, so API/text fallback
  tools can still run after a recoverable web failure pattern.
- `loop_guard_examples`: optional bounded examples of loop-guard and tool
  policy rejections. Each sample includes the failure kind, category
  (`loop_guard` or `tool_policy`), tool index, call id, tool name, compact
  argument/result previews, structured `guard_summary` and
  `suggested_next_step` fields when the tool result carried `loop_guard:` /
  `Next:` guidance, and exit code. Use this when
  `loop_guard_interventions` or `forced_no_tools` spike and the generic
  failure-kind counts do not show which attempted call was blocked. Summary
  records include the originating scenario.
- `loop_turn_checkpoints`: optional count of durable per-turn LOOP sidecar
  checkpoint writes observed in trace for this scenario.
- `loop_turn_checkpoint_examples`: optional bounded checkpoint samples with
  turn id, loop id/status, protocol path, event sequence, persisted checkpoint
  count, turn end reason, token counts, tool/error/guard counts, memory update
  and recall counts. These prove the run persisted recovery state before the
  trace claimed a long-run turn finished.
- `loop_protocol_feeds`: optional count of loop protocol injections into model
  context for this scenario.
- `loop_protocol_feed_by_mode`: optional map of feed mode to count. Known modes
  are `full` and `digest`; future modes should be treated as diagnostic labels.
- `loop_protocol_feed_examples`: optional bounded examples of protocol feed
  events. Each sample includes turn id, loop id, loop status, feed mode,
  sequential feed number, persisted protocol feed count, optional calibration
  answer count/latest answer preview, protocol path, optional
  `current_situation_preview` extracted from the LOOP.md Current Situation
  section, and optional active plan checkpoint fields (`plan_label`,
  `plan_current_step_index`, `plan_current_step_status`, and
  `plan_current_step`). Samples may also include the latest turn checkpoint
  fields (`last_turn_id`, `last_turn_end_reason`, `last_turn_tool_requests`,
  `last_turn_tool_errors`, `last_turn_forced_no_tools`,
  `last_turn_memory_updates`, `last_turn_memory_search_calls`,
  `last_turn_memory_search_misses`, `last_turn_session_search_calls`, and
  `last_turn_loop_guards`) plus latest loop decision fields
  (`last_decision_kind`, `last_decision_trigger`, `last_decision`,
  `last_decision_confidence`, `last_decision_reason`, and
  `last_decision_required_action`). Summary records include the originating scenario.
  Use these fields to verify that long-running loop sessions actually refreshed
  `LOOP.md` without overfeeding the model context, and that each feed preserved
  pointers back to authoritative plan and recall state.
- `loop.turn_checkpoint` trace events are counted in `trace_event_types` and
  can be asserted with `required_trace_event_counts`. They are emitted only
  after the loop sidecar checkpoint write succeeds, so they are a direct
  long-run durability signal rather than just another `turn.end` summary.
- `loop_protocol_calibration_requests`: optional count of assistant calibration
  questions asked for draft `LOOP.md` activation.
- `loop_protocol_calibration_request_examples`: optional bounded examples of
  mirrored calibration-question events. Each sample includes loop id, status,
  calibration question count/latest question preview, protocol path, and the
  sidecar loop event sequence.
- `loop_protocol_calibrations`: optional count of accepted user calibration
  answers for draft `LOOP.md` activation.
- `loop_protocol_calibration_examples`: optional bounded examples of mirrored
  calibration-answer events. Each sample includes loop id, status, calibration
  question/answer counts, latest previews, protocol path, and the sidecar loop
  event sequence. Use these fields to verify that setup progressed before
  waiting for a later protocol feed.
- Scenario debug manifests can require these setup events with
  `required_loop_protocol_calibration_requests` and
  `required_loop_protocol_calibrations`, which is useful for proving that a
  loop asked for calibration and then recorded user input before activation.
  They can also require status-specific setup evidence with
  `required_loop_protocol_calibration_request_statuses` and
  `required_loop_protocol_calibration_statuses`; draft setup scenarios use
  these fields to prove calibration did not accidentally activate the loop.
  Calibration-only setup scenarios may start from a draft protocol; active
  `LOOP.md` fixture preflight is reserved for feed requirements.
- `tool_duration_ms`: total runtime tool dispatch duration.
- `source_access_results`: count of tool results with a normalized
  `SourceAccess:` evidence header.
- `source_access_verified`: count of `SourceAccess:` results with an accessed
  URL that were not discovery-only.
- `source_access_discovery_only`: count of search-result, not-found, or
  rendered fallback `SourceAccess:` results that are navigation aids rather
  than factual evidence.
- `source_access_network`: count of `SourceAccess:` results read from captured
  browser XHR/fetch evidence.
- `source_access_examples`: optional bounded examples of normalized
  `SourceAccess:` evidence. Each sample includes tool index, call id, tool
  name, evidence status (`verified`, `network`, `dynamic_partial`, or
  `discovery_only`), accessed/requested URLs, URL field, source method, HTTP
  status, content type, and JSON path when present. Browser network reads also
  include the captured response `ref` when the `SourceAccess:` header carried
  one. Samples include a compact `result_preview` after the `SourceAccess:`
  provenance header so operators can see the actual evidence body (for example
  a JSON value or rendered page text) without opening the full trace. Batch
  summary examples also include the originating scenario name so a failed
  live-web evidence chain can be traced directly.
- `browser_network_examples`: optional bounded examples of `browser_network`
  searches. These are diagnostic refs/checks, not citable factual evidence.
  Each sample includes tool index, call id, current page URL, query, status
  (`matches`, `no_matches`, or `unknown`), bounded network refs, compact
  `preview:` snippets from matched responses, the tool-level
  `refs_only_not_citable`/`read_required=true` status when present, whether the
  refs require `browser_network_read`, and the suggested next step. Batch
  summary examples also include the originating scenario name. Treat these as
  leads for dynamic pages: cite hidden JSON/text values only after a matching
  `browser_network_read` result produces network `SourceAccess:` evidence.
- `browser_scroll_examples`: optional bounded examples of `browser_scroll`
  telemetry. These are page-position diagnostics, not citable factual
  evidence. Each sample includes tool index, call id, rendered URL, direction,
  before/after/max scroll positions, movement, boundary, status (`moved`,
  `stuck`, `boundary`, or `unknown`), result preview, and the suggested next
  step. Use these samples to detect dynamic dashboards where scrolling reached
  a boundary or did not move; cite hidden values only after a matching
  `browser_network_read` result produces network `SourceAccess:` evidence.
- `session_search_calls`: count of dispatched `session_search` tool calls.
- `session_search_results`: total prior-session hits reported by parsed
  `session_search` JSON responses.
- `session_search_context_hits`: count of `session_search` hits that included
  adjacent transcript context, a compact persisted plan anchor, or a compact
  loop-protocol or task-state anchor.
- `session_search_matched_terms`: count of unique matched query terms reported
  across parsed `session_search` responses.
- `session_search_recent_sessions`: count of recent-session recovery anchors
  returned by parsed no-hit `session_search` responses. Recent-session anchors
  may include canonical `task_state` previews derived from `events.jsonl`.
- `session_search_examples`: optional bounded examples of parsed
  `session_search` responses. Each sample includes tool index, call id, query,
  total result count, matched session id, logical turn index, physical
  `message_idx` when available, role, score, matched terms, context-included
  flag, and a compact snippet/message preview. User-request hits can include
  the adjacent assistant answer so recovery evals can inspect the prior
  outcome. No-hit examples can include recent-session user, assistant, plan,
  and loop anchor previews for the retry path. Summary records include the
  originating scenario.
- `memory_update_examples`: optional bounded per-scenario examples of confirmed
  durable memory mutations. Each sample includes the tool index, call id,
  action, target/topic location, and compact previous/next previews when
  available. These examples make long-run memory drift auditable without
  opening the full trace or markdown timeline. Summary records include the
  originating scenario.
- `memory_search_miss_examples`: optional bounded per-scenario examples of
  successful memory searches that returned no direct hits. Each sample includes
  tool index, call id, target/topic, query, topic count, compact topic names
  when topic anchors are available, and the recovery message. Summary records
  include the originating scenario.
- `memory_search_calls`: count of dispatched memory search attempts.
- `memory_search_misses`: count of successful memory search calls that returned
  no direct hits. These counters are emitted in `tool_stats`, scenario JSONL,
  and summary JSONL so long-run recall miss rates can be trended without
  opening traces; `memory_search_miss_examples` provide the bounded recovery
  details.
- `context_compaction_examples`: optional bounded examples of context
  compaction events. Each sample includes the turn id, before/after message
  counts, removed message count, reactive/proactive flag, reason, and summary
  byte size. Samples include `summary_present_known` when the source trace
  explicitly carried `summary_present`, so older traces are not misclassified
  as missing summaries. Samples may also include `loop_protocol_anchor` when
  compaction preserved a recoverable `LOOP.md` pointer. Summary records include
  the originating scenario.
- `context_compactions`, `context_compactions_reactive`,
  `context_compaction_removed_messages`, `context_compaction_summary_bytes`,
  `context_compaction_summary_missing`, and
  `context_compaction_summary_empty`: optional context pressure counters
  collected from context compaction trace events. Scenario expectations can use
  `required_context_compaction_reasons` to require specific reason counts from
  the same trace data.
- `context_injections`: optional count of hidden system-context blocks injected
  into model context.
- `context_injection_by_source`: optional map of injected context source to
  count. Sources include account access hints, active plan state, active skill
  state, and other runtime-injected context blocks.
- `context_injection_bytes` and `context_injection_estimated_tokens`: optional
  total size/cost counters for injected context.
- `context_injection_examples`: optional bounded examples of injected context
  blocks. Each sample includes turn id, source, title, compact summary/preview,
  byte size, and estimated tokens. Summary records include the originating
  scenario.
- `tool_truncation_examples`: optional bounded examples of tool calls whose
  request args, event result, result artifact, or model-context insertion were
  truncated. Each sample includes the tool index, call id, tool name, omitted
  byte counts, cap byte counts, compact `result_summary` preview when present,
  artifact path when present, and context bytes omitted before the tool result
  was fed back to the model. Summary records include the originating scenario.
- `tool_context_truncated`: count of tool results shortened before being fed
  back into the model conversation.
- `tool_context_omitted_bytes`: total bytes omitted from tool results before
  model-context insertion.
- `tool_args_truncated`: count of event-capped tool requests.
- `tool_args_omitted_bytes`: omitted request bytes across tool events.
- `tool_results_truncated`: count of event-capped tool results.
- `tool_results_omitted_bytes`: omitted result bytes across tool events.
- `tool_result_artifacts`: count of tool result artifact references.
- `tool_result_missing_artifacts`: event-capped tool results without a saved
  full-output artifact.
- `tool_context_artifacts`: model-context-truncated tool results with a saved
  full-output artifact.
- `tool_context_missing_artifacts`: model-context-truncated tool results without
  a saved full-output artifact.
- `focused_task_calls`: optional number of delegated `run_task` calls.
- `focused_task_by_type`: optional map of focused task `task_type` to call
  count.
- `focused_task_sources`: optional map of focused task `task_type` to sourced
  finding count from structured `run_task` results.
- `focused_task_errors`: optional count of focused task calls whose runtime
  tool exit code was non-zero.
- `subagent_calls`: optional number of delegated `subagent_run` calls.
- `subagent_by_mode`: optional map of subagent `mode` to call count.
- `subagent_sources`: optional map of subagent `mode` to source-bearing report
  line count from structured `subagent_run` results.
- `subagent_errors`: optional count of subagent calls whose runtime tool exit
  code was non-zero.
- `plan_calls`: optional number of persisted-plan tool calls.
- `plan_by_action`: optional map of plan action (`view`, `set`, `update`,
  `clear`, or `unknown`) to call count.
- `plan_errors`: optional count of plan tool calls whose runtime tool exit code
  was non-zero.
- `plan_examples`: optional bounded examples of persisted-plan tool activity.
  Each sample includes tool index, call id, action, updated step index/status,
  compact step text, evidence refs, note preview, result progress/current step,
  and a compact error/result message when relevant. Summary records include the
  originating scenario.
- `verifier_command`: verifier shell command, when configured.
- `verifier_ran`: whether a verifier command ran.
- `verifier_ok`: whether the verifier exited successfully.
- `verifier_exit_code`: verifier process exit code, or `-1` for abnormal
  termination.
- `verifier_duration_ms`: verifier duration in milliseconds.
- `verifier_output_bytes`: verifier stdout/stderr byte count before capping.
- `verifier_output_truncated`: whether verifier output hit the cap.
- `verifier_output_omitted_bytes`: verifier output bytes omitted by the cap.
- `verifier_output_cap_bytes`: verifier output cap used for the run.
- `input_tokens`: summed provider input tokens.
- `output_tokens`: summed provider output tokens.
- `workspace_removed`: whether a passing workspace was cleaned up. Quality-gated
  runs retain passing workspaces when a batch gate fails so scenario artifacts
  remain inspectable.
- `cleanup_error`: workspace cleanup error, when cleanup failed.
- `failures`: failure details for failed scenarios.
- `failure_kinds`: grouped failure counters for summaries. Fixture and
  pre-run authoring failures use stable kinds such as `loop_protocol_fixture`
  so they can be separated from model/runtime regressions.

Verifier output text is not written to JSONL. Failure reports may include a
bounded preview in `failures`; the structured verifier fields are the stable
surface for trend analysis.

Retained debug artifacts expose the same verifier status as a compact
`verifier` object in `affenteval-debug.json`, with command, ran/ok booleans,
exit code, duration milliseconds, and output byte/truncation counters. The
human-readable `affenteval-timeline.md` also includes a `Verifier` section so
code/PR scenarios can be triaged from retained artifacts without opening the
raw JSONL first.

## Summary Record

Summary records aggregate all scenario records from the same process:

- `scenarios`, `passed`, `failed`, `duration_ms`, `avg_duration_ms`.
- Normalized comparison metrics: `pass_rate`, `completion_rate`,
  `memory_update_rate`, `loop_turn_checkpoint_rate`,
  `loop_protocol_feed_rate`, `runtime_surface_rate`, `task_state_rate`,
  `loop_protocol_calibration_request_rate`,
  `loop_protocol_calibration_rate`, `trace_event_rate`,
  `tool_error_rate`, `forced_no_tools_rate`, and
  `loop_guard_intervention_rate` when tool calls were observed,
  `focused_task_error_rate` when focused-task calls were observed,
  `subagent_error_rate` when subagent calls were observed,
  `plan_error_rate` when plan calls were observed,
  `tool_repair_success_rate` when repaired/canonicalized tool calls were
  observed, `verifier_pass_rate` when verifier commands ran,
  `memory_search_miss_rate` when memory search was used,
  `source_access_verified_rate` when source evidence was observed,
  `source_network_rate` when source evidence was observed,
  `source_discovery_only_rate` when source evidence was observed,
  `source_dynamic_partial_rate` when source evidence was observed,
  `session_search_context_hit_rate` when session search returned prior-session
  hits, `session_search_matched_terms_per_call` when session search was used,
  `avg_runtime_errors`, `avg_context_compactions`,
  `avg_reactive_context_compactions`, `avg_context_removed_messages`,
  `avg_context_summary_bytes`, `avg_context_summary_missing`,
  `avg_context_summary_empty`, `avg_context_injections`,
  `avg_context_injection_bytes`, `avg_context_injection_estimated_tokens`,
  `avg_tool_calls`,
  `tool_context_truncation_rate` and
  `tool_result_truncation_rate` when tool calls were observed,
  `avg_input_tokens`, `avg_output_tokens`, `avg_total_tokens`,
  `max_scenario_total_tokens`, and `max_scenario_token_scenario`.
- Tool totals: `tool_calls`, `tool_errors`, `tool_repaired`,
  `tool_name_canonicalized`, `tool_repair_calls`, `tool_repair_succeeded`,
  `tool_repair_failed`, `tool_repair_notes`, `tool_repair_by_kind`,
  `tool_repair_examples`,
  `tool_failure_by_kind`, `loop_guard_interventions`, `forced_no_tools`,
  `loop_guard_examples`,
  `source_access_results`, `source_access_verified`,
  `source_access_discovery_only`, `source_access_network`,
  `source_access_examples`, `browser_scroll_examples`,
  `browser_network_examples`,
  `memory_updates`, `memory_update_add`, `memory_update_replace`,
  `memory_update_remove`, `memory_update_examples`,
  `memory_search_calls`, `memory_search_misses`,
  `memory_search_miss_examples`,
  `session_search_calls`, `session_search_results`,
  `session_search_context_hits`, `session_search_matched_terms`,
  `session_search_recent_sessions`, `session_search_examples`,
  `tool_duration_ms`,
  `tool_context_truncated`, `tool_context_omitted_bytes`.
- Truncation totals: `tool_args_truncated`, `tool_args_omitted_bytes`,
  `tool_results_truncated`, `tool_results_omitted_bytes`,
  `tool_result_artifacts`, `tool_result_missing_artifacts`,
  `tool_context_artifacts`, `tool_context_missing_artifacts`, plus
  `tool_truncation_examples`, the first bounded samples across the batch.
- Delegation totals: `focused_task_calls`, `focused_task_by_type`,
  `focused_task_sources`, `focused_task_errors`, `focused_task_error_rate`,
  `subagent_calls`, `subagent_by_mode`, `subagent_sources`,
  `subagent_errors`, and `subagent_error_rate`. These fields are omitted when
  no delegated tool calls were observed.
- Plan totals: `plan_calls`, `plan_by_action`, `plan_errors`,
  `plan_examples`, and `plan_error_rate`. These fields are omitted when no plan
  tool calls were observed.
- Verifier totals: `verifier_runs`, `verifier_passed`, `verifier_failed`,
  `verifier_output_truncated`, `verifier_output_omitted_bytes`.
- Trace versions: `trace_schema_versions`.
- Token totals: `input_tokens`, `output_tokens`.
- Turn end totals: `end_completed`, `end_max_turns`, `end_errors`,
  `end_cancelled`, `end_unknown`.
- Failure totals and examples: `failure_kinds`, plus `failure_examples`, a
  bounded per-kind sample containing scenario name, compact failure text, and
  retained trace/timeline/debug-manifest paths when available. Loop protocol
  fixture failures are grouped as `loop_protocol_fixture`.
- Tool failure totals and diagnostics: `tool_failure_by_kind`,
  `tool_failure_hints`, `tool_failure_examples`.
- Runtime error totals and diagnostics: `runtime_error_by_kind`,
  `runtime_error_hints`, `runtime_error_examples`.
- Bounded diagnostic examples: summary-level `tool_repair_examples`,
  `tool_failure_examples`, `loop_guard_examples`, `runtime_error_examples`,
  `source_access_examples`, `browser_scroll_examples`,
  `browser_network_examples`, `memory_update_examples`,
  `memory_search_miss_examples`,
  `session_search_examples`, `tool_truncation_examples`,
  `context_compaction_examples`, `context_injection_examples`,
  `loop_decision_examples`, `loop_turn_checkpoint_examples`,
  `loop_protocol_feed_examples`, `loop_protocol_calibration_request_examples`,
  `loop_protocol_calibration_examples`, and `plan_examples` include their
  originating scenario so long-run batch failures can be routed directly to the
  right trace/timeline. Context compaction examples may include
  `loop_protocol_anchor` when the compacted summary preserved a per-session
  `LOOP.md` recovery pointer.
- Loop protocol feed totals: `loop_protocol_feed_scenarios`,
  `loop_protocol_feeds`, `loop_protocol_feed_by_mode`, and
  `loop_protocol_feed_examples`, useful for checking whether a long-run
  scenario kept its per-session protocol state in attention after compaction or
  multi-turn drift. `loop_protocol_feed_rate` is scenario coverage, not raw
  feed count divided by scenario count, so repeated feeds in one scenario do
  not hide scenarios that never received their protocol.
- Loop turn checkpoint totals: `loop_turn_checkpoint_scenarios`,
  `loop_turn_checkpoints`, and `loop_turn_checkpoint_examples`, useful for
  checking whether long-run turns persist their recovery sidecar before
  ending. `loop_turn_checkpoint_rate` is scenario coverage, not raw checkpoint
  count divided by scenario count.
- Loop protocol calibration totals:
  `loop_protocol_calibration_request_scenarios`,
  `loop_protocol_calibration_requests`,
  `loop_protocol_calibration_scenarios`,
  `loop_protocol_calibrations`, and their bounded example arrays. The summary
  rates `loop_protocol_calibration_request_rate` and
  `loop_protocol_calibration_rate` are scenario coverage rates, so one noisy
  loop cannot mask that other loop-activation scenarios never asked for or
  recorded calibration.
- Context pressure totals: `context_compactions`,
  `context_compactions_reactive`, `context_compaction_removed_messages`,
  `context_compaction_summary_bytes`, `context_compaction_summary_missing`, and
  `context_compaction_summary_empty`.
- Context compaction examples: `context_compaction_examples`, the first bounded
  samples across the batch.
- Context injection totals: `context_injections`,
  `context_injection_by_source`, `context_injection_bytes`, and
  `context_injection_estimated_tokens`, plus bounded
  `context_injection_examples`. No-tool finalization recovery may emit
  `source=final_evidence_digest` when the runtime appended a compact digest of
  prior citable tool evidence before forcing the model to answer without more
  tools. Scenarios can assert this hidden recovery path with
  `expectations.required_context_injection_sources`, which maps a source name to
  the minimum required injection count.
- Debug brief tag totals: `debug_brief_by_tag`, counting how many scenarios
  emitted each machine-readable triage tag. Verifier tags such as
  `verifier:failed`, `verifier:not_run`, `verifier:abnormal`, and
  `verifier:output_truncated` let code/PR batches separate implementation or
  test failures from browser, memory, plan, context, or tool-call regressions.
  `debug_brief_tag_examples` is a bounded map from tag to scenarios and
  retained trace/timeline/debug-manifest paths, so tag-rate gate failures can
  jump directly to representative artifacts.
- Expectation coverage totals:
  `expectation_scenarios` counts scenarios that carried declarative
  expectations; `expectation_suites` counts suite markers;
  `expectation_domains` counts task-domain markers such as `market`,
  `bittensor`, `code_pr`, `web_evidence`, and `longrun_recovery`;
  `expectation_required_tools`
  counts tools mentioned or implied by scenario requirements, including shell
  command, session-search, and delegation requirements; `expectation_source_access`
  counts declared source-access statuses such as `network` or `verified`; and
  `expectation_capabilities` counts broad required capability families such as
  `workspace`, `memory`, `session_search`, `source_access`, `web`, `browser`,
  `delegation`, `delegated_source_evidence`, `plan`, `loop_protocol`,
  `context_compaction`, `verifier`, `research_checkpoint`,
  `longrun_recovery`, and `mcp`.
  `expectation_capability_passed`, `expectation_capability_failed`, and
  `expectation_capability_pass_rate` split those declared capability families
  by scenario outcome. These are declaration/outcome counters, not observed
  runtime behavior. Use them with runtime-surface fields to see which
  capability classes a batch actually exercised and where failures cluster.
  `expectation_capability_total`,
  `expectation_capability_passed_total`,
  `expectation_capability_failed_total`, and
  `expectation_capability_pass_rate_total` aggregate the same declared
  capability instances into one batch-level rate for CI dashboards and quality
  gates.
  `expectation_capability_failure_examples` is a bounded map from capability
  family to failed scenario examples with failure kinds, debug-brief tags, and
  retained trace/timeline/debug-manifest paths when available. It is intended
  as the direct "open this failed case first" index for long-run triage.
  `min_each_expectation_capability_pass_rate` gates each family in
  `expectation_capability_pass_rate` independently, so a memory or browser
  regression cannot be hidden by unrelated passing capabilities.
  `expectation_domain_passed`, `expectation_domain_failed`, and
  `expectation_domain_pass_rate` split declared workload-domain markers by
  scenario outcome. `expectation_domain_total`,
  `expectation_domain_passed_total`, `expectation_domain_failed_total`, and
  `expectation_domain_pass_rate_total` aggregate those domain instances across
  the batch. `expectation_domain_failure_examples` is the domain-level triage
  index for failed market, Bittensor, code/PR, web-evidence, and long-run
  recovery workloads. Use `min_expectation_domain_pass_rate` and
  `min_each_expectation_domain_pass_rate` when a profile must prove not only
  that realistic domains were present, but that each domain stayed above a
  minimum outcome bar.
  `expectation_domain_metrics` is a map from domain to outcome, cost,
  stability, and evidence-quality aggregates scoped to that workload domain:
  scenario counts, pass rate, average duration, average tool calls, average
  runtime errors, average total tokens, memory-update rate, tool error rate,
  loop-guard intervention rate, SourceAccess verified/network/discovery-only/
  dynamic-partial rates, and the raw supporting counts. When a scenario
  declares multiple domains, its runtime costs and evidence counts are
  intentionally attributed to each declared domain so dashboards can answer
  which realistic workload is expensive, unstable, or producing weak source
  evidence without parsing scenario names.
  Domain-specific quality gates can be enabled with
  `--min-expectation-domain-source-access-verified-rate DOMAIN=RATE`,
  `--max-expectation-domain-avg-total-tokens DOMAIN=TOKENS`,
  `--max-expectation-domain-avg-tool-calls DOMAIN=COUNT`,
  `--max-expectation-domain-avg-runtime-errors DOMAIN=COUNT`,
  `--max-expectation-domain-tool-error-rate DOMAIN=RATE`, and
  `--max-expectation-domain-loop-guard-intervention-rate DOMAIN=RATE`.
  The built-in `web-evidence` profile enables these gates for the
  `web_evidence` domain so mixed live-web batches cannot hide weak current-web
  evidence, tool errors, loop-guard churn, runtime errors, or runaway cost
  behind unrelated passing domains. The same profile also requires the
  `delegated_source_evidence` expectation capability and gates delegated child
  error rates so focused web research remains an explicit, source-backed path
  rather than an unobserved convenience tool.
  `--require-expectation-domain` gates declared workload-domain coverage
  independently from capability coverage, so CI can require at least one
  realistic market, Bittensor, code/PR, web-evidence, or long-run recovery
  workload before interpreting aggregate pass rates.
- Quality gate outcome: `quality_profile` identifies any built-in profile used,
  `quality_gates_passed` is present on summary records when at least one quality
  gate threshold was configured, and
  `quality_gate_failures` lists failed gate comparisons when any thresholds
  were violated. `debug_brief_tag_rate[tag]` failures use total scenarios as
  the denominator; pair them with `debug_brief_tag_examples` to jump to
  representative retained artifacts for the failing tag.
- Runtime surface and trace coverage totals: `runtime_surface_rate`,
  `runtime_surface_scenarios`, `runtime_surface_tools`,
  `runtime_surface_capabilities`, `runtime_surface_refresh_by_reason`,
  `trace_event_rate`, and
  `trace_event_scenarios`. Counts are
  per-scenario surface presence, not model call counts; use them to group pass
  rates and failures by the actual tool/capability surface exposed during the
  run. Partial workspace surfaces are counted as `workspace_partial`.
- Task-state coverage totals: `task_state_rate`, `task_state_scenarios`,
  `task_state_by_status`, `task_state_by_verification`,
  `task_state_changed_files`, `task_state_attempted_actions`,
  `task_state_failed_actions`, `task_state_evidence`, and
  `task_state_evidence_by_source`. These summarize derived task-state snapshots so
  long-run dashboards can verify scenario traces are reviewable at the task
  level, not only as low-level tool timelines.
- Cleanup totals: `removed_workspaces`, `cleanup_errors`.

## Compatibility

Adding optional fields is backward compatible. Removing or renaming fields, or
changing the meaning of an existing field, requires incrementing
`schema_version`.
