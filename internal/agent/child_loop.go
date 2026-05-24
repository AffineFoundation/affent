package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/affinefoundation/affent/internal/memory"
	"github.com/affinefoundation/affent/internal/sse"
	"github.com/rs/zerolog"
)

// childRunSpec configures one bounded child Loop run. It captures every
// piece of state that differs between child-loop callers (subagent_run,
// run_task) so the conversation/Loop construction/drain plumbing is
// written exactly once in runChildLoop.
//
// Caller responsibilities (not done here):
//   - Build the child Tools registry (per-mode or per-task-type).
//   - Register any extra session-scoped tools and arrange their cleanup.
//   - Generate ChildID with a stable prefix so transcripts and trace
//     metadata can identify the surface that launched the child.
//   - Shape the returned childRunResult into the surface's structured
//     response payload (subagentResponse vs FocusedTaskResult).
type childRunSpec struct {
	// ChildID is the unique identifier surfaced as "child_session_id"
	// and used to name the JSONL transcript file. Should be prefixed by
	// the launching surface, e.g. "subagent_<uuid>" or "focused_<uuid>".
	ChildID string
	// LogComponent is the value attached to the child Loop's log
	// "component" field for trace filtering, e.g. "subagent".
	LogComponent string
	// TranscriptDir is the root for the child JSONL transcript. When
	// empty, runChildLoop falls back to a temp dir that is removed after
	// the run.
	TranscriptDir string

	LLM   *LLMClient
	Tools *Registry

	// MaxTurns is the assistant<->tool round-trip budget. It is also
	// used as the per-turn tool-call cap because a child run is a single
	// user turn and treating them as the same budget is the conservative
	// choice for context-isolated work.
	MaxTurns                    int
	ToolResultMaxBytesInContext int
	PerCallTimeout              time.Duration
	Memory                      memory.MemoryStore
	ProjectContextDir           string
	Log                         zerolog.Logger

	SystemPrompt string
	UserPrompt   string

	FirstToolPolicy *FirstToolPolicy
	PostToolPolicy  *PostToolPolicy
}

// childRunResult is the raw output of a child Loop run. The caller is
// responsible for sanitizing the report and wrapping it in a surface-
// specific structured response.
type childRunResult struct {
	Report        string             // last message.done text from the child
	TurnEndReason string             // sse.TurnEnd reason
	ToolCalls     []subagentToolCall // accumulated child tool requests + per-call result summaries
	Usage         subagentUsage      // summed token usage across child LLM calls
	LoopErrors    []string           // recoverable child loop errors that did not abort the turn
	Err           error              // first hard error (drain error, ctx cancel, system-prompt failure, etc.)
}

// runChildLoop drives a single child Loop end-to-end: open the child
// conversation, build the Loop, send the user prompt, drain events, and
// return the raw result. It deliberately does not shape the result —
// different surfaces (free-form subagent reports vs structured focused-
// task JSON) need different post-processing.
func runChildLoop(ctx context.Context, spec childRunSpec) childRunResult {
	convPath, cleanup, err := subagentConversationPath(spec.TranscriptDir, spec.ChildID)
	if err != nil {
		return childRunResult{Err: err}
	}
	defer cleanup()

	conv, err := OpenConversationAt(convPath)
	if err != nil {
		return childRunResult{Err: fmt.Errorf("child conversation: %w", err)}
	}

	events := make(chan sse.Event, 128)
	loop := &Loop{
		LLM:                         spec.LLM,
		Tools:                       spec.Tools,
		Conv:                        conv,
		Events:                      events,
		Log:                         spec.Log.With().Str("component", spec.LogComponent).Logger(),
		MaxTurnSteps:                spec.MaxTurns,
		MaxToolCalls:                spec.MaxTurns,
		ToolResultMaxBytesInContext: spec.ToolResultMaxBytesInContext,
		PerCallTimeout:              spec.PerCallTimeout,
		FinalNoToolsOnMaxTurns:      true,
		Memory:                      spec.Memory,
		ProjectContextDir:           spec.ProjectContextDir,
		SkillProvider:               SkillProviderForTools(nil, spec.Tools),
		FirstToolPolicy:             spec.FirstToolPolicy,
		PostToolPolicy:              spec.PostToolPolicy,
	}

	if err := loop.EnsureSystemPrompt(spec.SystemPrompt); err != nil {
		return childRunResult{Err: fmt.Errorf("child system prompt: %w", err)}
	}
	turnID, err := loop.SendUser(ctx, spec.UserPrompt)
	if err != nil {
		return childRunResult{Err: err}
	}
	report, reason, calls, usage, errMsgs, drainErr := drainSubagent(ctx, events, turnID)
	if drainErr != nil {
		loop.Cancel()
	}
	return childRunResult{
		Report:        report,
		TurnEndReason: reason,
		ToolCalls:     calls,
		Usage:         usage,
		LoopErrors:    errMsgs,
		Err:           drainErr,
	}
}
