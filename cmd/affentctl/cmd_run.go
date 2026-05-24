package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/eventlog"
	"github.com/affinefoundation/affent/internal/planstate"
	"github.com/affinefoundation/affent/internal/sse"
	"github.com/rs/zerolog"
)

const runPlanOnlyMaxToolCalls = 2

func runCmd(args []string) int {
	var cf commonFlags
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cf.bind(fs)
	promptFlag := fs.String("prompt", "", "user prompt; '-' for stdin, '@file', or literal")
	planOnly := fs.Bool("plan-only", false, "create or update the persisted task plan and stop before executing tools")
	executePlan := fs.Bool("execute-plan", false, "execute the selected session's persisted plan after user confirmation; requires --session-id or --continue and an unfinished non-blocked plan")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `usage: affentctl run [flags]

One-shot: feed a single prompt, drive the loop until turn.end, print
the final assistant text on stdout. Designed for training / eval.

Required: --model. --prompt is required unless --execute-plan is set.`)
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr, "\nExit codes: 0 completed, 2 max_turns, 3 error, 130 cancelled.")
	}
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if err := applyConfig(&cf, fs); err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return exitUsage
	}
	if err := validateRunModeFlags(cf, *planOnly, *executePlan); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}

	prompt, err := readMaybeStdin(*promptFlag)
	if err != nil {
		// Distinguish "you forgot --prompt" (which goes to the
		// "use '-' for stdin" hint) from "your @file path is wrong"
		// (which the user needs to see verbatim to fix the typo).
		fmt.Fprintf(os.Stderr, "--prompt: %v\n", err)
		return exitUsage
	}
	if strings.TrimSpace(prompt) == "" && !*executePlan {
		fmt.Fprintln(os.Stderr, "--prompt is required (use '-' for stdin)")
		return exitUsage
	}

	b, code := setupLoop(cf)
	if code != 0 {
		return code
	}
	defer b.close()
	if *planOnly {
		if err := enableRunPlanOnly(b); err != nil {
			fmt.Fprintf(os.Stderr, "plan-only: %v\n", err)
			return exitUsage
		}
		prompt = agent.PlanOnlyUserPrompt(prompt)
	} else if *executePlan {
		var err error
		prompt, err = prepareRunExecutePlan(b, prompt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "execute-plan: %v\n", err)
			return exitUsage
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if b.resumed {
		b.log.Info().Str("session_id", b.sessionID).Msg("resumed")
	} else {
		if err := b.recorder.WriteMeta(); err != nil {
			b.log.Error().Err(err).Msg("write trace metadata")
			return exitRuntime
		}
		b.log.Info().Str("session_id", b.sessionID).Msg("new session")
	}

	turnID, err := b.loop.SendUser(ctx, prompt)
	if err != nil {
		b.log.Error().Err(err).Msg("send user")
		return exitRuntime
	}
	b.log.Info().Str("turn_id", turnID).Msg("turn started")

	finalText, exit := drainBatch(ctx, b.loop, b.events, b.recorder, b.log)
	// When the user routed the trace to stdout (--trace -), printing the
	// final assistant text on stdout too would interleave plain markdown
	// into the JSONL stream and break every batch-eval consumer that
	// pipes stdout through jq. The text is already on stdout via the
	// message.done event in the trace, so we just skip the extra print.
	if finalText != "" && cf.tracePath != "-" {
		fmt.Println(finalText)
	}
	if *planOnly && exit == 0 {
		if line := runPlanOnlyNextStepLine(b); line != "" {
			fmt.Fprintln(os.Stderr, line)
		}
	}
	return exit
}

func enableRunPlanOnly(b *loopBundle) error {
	if b == nil || b.loop == nil || b.loop.Tools == nil {
		return fmt.Errorf("agent loop is not initialized")
	}
	opts, err := agent.PlanOnlyTurnOptions(b.loop.Tools, runPlanOnlyMaxToolCalls)
	if err != nil {
		return err
	}
	b.loop.Tools = opts.Tools
	b.loop.FirstToolPolicy = opts.FirstToolPolicy
	b.loop.MaxToolCalls = opts.MaxToolCalls
	b.loop.FinalNoToolsOnMaxTurns = opts.FinalNoToolsOnMaxTurns
	return nil
}

func validateRunModeFlags(c commonFlags, planOnly, executePlan bool) error {
	if planOnly && executePlan {
		return fmt.Errorf("--plan-only and --execute-plan cannot be used together")
	}
	if executePlan && strings.TrimSpace(c.sessionID) == "" && !c.continueLast {
		return fmt.Errorf("--execute-plan requires --session-id or --continue so execution resumes the confirmed plan session")
	}
	return nil
}

func prepareRunExecutePlan(b *loopBundle, prompt string) (string, error) {
	if b == nil || strings.TrimSpace(b.workspace) == "" || strings.TrimSpace(b.sessionID) == "" {
		return "", fmt.Errorf("session plan is not available")
	}
	if !loopHasPlanTool(b) {
		return "", fmt.Errorf("plan tool is not available")
	}
	summary := runSessionPlanSummary(b)
	switch {
	case summary.Error:
		return "", fmt.Errorf("session %q has an unreadable plan; inspect or clear it with affentctl sessions --plan/--clear-plan", b.sessionID)
	case summary.Label == planstate.LabelMissing:
		return "", fmt.Errorf("session %q has no persisted plan; create one with --plan-only first", b.sessionID)
	case summary.Label == planstate.LabelEmpty:
		return "", fmt.Errorf("session %q has an empty plan; create a concrete plan with --plan-only first", b.sessionID)
	case summary.Done:
		return "", fmt.Errorf("session %q plan is already done; clear it or create a new plan", b.sessionID)
	case summary.Blocked:
		return "", fmt.Errorf("session %q plan is blocked at step %d; resolve the blocker before executing", b.sessionID, summary.CurrentStepIndex)
	case summary.TotalSteps == 0:
		return "", fmt.Errorf("session %q has no executable plan steps", b.sessionID)
	}
	if strings.TrimSpace(prompt) == "" {
		prompt = "Proceed with the active persisted plan."
	}
	return runExecutePlanPrompt(prompt, summary.Label), nil
}

func loopHasPlanTool(b *loopBundle) bool {
	if b == nil || b.loop == nil || b.loop.Tools == nil {
		return false
	}
	_, ok := b.loop.Tools.Get(agent.PlanToolName)
	return ok
}

func runSessionPlanSummary(b *loopBundle) planstate.Summary {
	if b == nil {
		return planstate.ErrorSummary()
	}
	convDir := filepath.Join(b.workspace, ".affentctl")
	return localSessionPlanSummary(convDir, b.sessionID)
}

func runPlanOnlyNextStepLine(b *loopBundle) string {
	if b == nil || strings.TrimSpace(b.workspace) == "" || strings.TrimSpace(b.sessionID) == "" {
		return ""
	}
	summary := runSessionPlanSummary(b)
	if summary.Error || summary.Label == planstate.LabelMissing || summary.Label == planstate.LabelEmpty || summary.Done || summary.Blocked || summary.TotalSteps == 0 {
		return ""
	}
	return fmt.Sprintf("[plan] saved for session %q; after confirmation run: affentctl run --workspace %s --session-id %s --execute-plan", b.sessionID, shellQuoteForEnv(b.workspace), shellQuoteForEnv(b.sessionID))
}

func runExecutePlanPrompt(request, label string) string {
	request = strings.TrimSpace(request)
	if request == "" {
		request = "Proceed with the active persisted plan."
	}
	return `Execute-plan mode is enabled.

The user has confirmed execution of this session's persisted task plan (` + strings.TrimSpace(label) + `). Continue from AFFENT ACTIVE PLAN, execute the next concrete step, update the plan as progress changes, and do not restart planning unless the persisted plan is stale or impossible to execute.

User confirmation/request:
` + request
}

// drainBatch is the run-mode drain: records every event via rec,
// returns the assistant's last final text and an exit code on turn.end.
// When rec was built with SkipDeltas, thinking.delta and message.delta
// are still observed for state but not written — the final text remains
// available through message.done. Useful for batch eval where
// token-level replay has no value but trace size matters.
//
// loop is the active *agent.Loop and may be nil only in unit tests. On
// SIGINT (ctx.Done) we MUST call Loop.Cancel — the Loop runs its turn on
// a detached background context so the parent ctx cancelling has no
// effect on in-flight LLM calls or shell-tool processes. Without the
// explicit Cancel a `shell exec sleep 60` survives the SIGKILL on
// affentctl, leaving an orphan in the user's process table.
func drainBatch(ctx context.Context, loop interface{ Cancel() }, events <-chan sse.Event, rec *eventlog.Recorder, log zerolog.Logger) (string, int) {
	var finalText string
	exit := 0
	turnEnded := false

	for {
		select {
		case <-ctx.Done():
			// SIGINT here means "abort this run". The Loop runs the turn
			// on a detached background ctx so we MUST call Cancel — only
			// then does the loop's turnCtx fire, exec.CommandContext kills
			// the running shell process, and runTurn emits turn.end with
			// reason=cancelled. After Cancel we still wait briefly for
			// runTurn to flush turn.end into the trace; otherwise the
			// trace truncates mid-turn and replay tooling breaks.
			log.Warn().Msg("interrupted; cancelling turn and draining")
			if loop != nil {
				loop.Cancel()
			}
			deadline := time.After(turnCancelDrainTimeout)
			for {
				select {
				case ev, ok := <-events:
					if !ok {
						return finalText, 130
					}
					if err := rec.Write(ev); err != nil {
						log.Error().Err(err).Msg("write trace")
					}
					if ev.Type == sse.TypeMessageDone {
						var p sse.MessageDonePayload
						_ = json.Unmarshal(ev.Data, &p)
						finalText = p.Text
					}
					if ev.Type == sse.TypeTurnEnd {
						return finalText, 130
					}
				case <-deadline:
					log.Warn().Msg("turn.end never arrived after cancel; trace may be incomplete")
					return finalText, 130
				}
			}
		case ev, ok := <-events:
			if !ok {
				if !turnEnded {
					log.Warn().Msg("event stream closed before turn.end")
					return finalText, 3
				}
				return finalText, exit
			}
			if err := rec.Write(ev); err != nil {
				log.Error().Err(err).Msg("write trace")
			}
			switch ev.Type {
			case sse.TypeMessageDone:
				var p sse.MessageDonePayload
				_ = json.Unmarshal(ev.Data, &p)
				finalText = p.Text
			case sse.TypeError:
				exit = 3
			case sse.TypeTurnEnd:
				var p sse.TurnEndPayload
				_ = json.Unmarshal(ev.Data, &p)
				log.Info().Str("reason", p.Reason).Msg("turn ended")
				turnEnded = true
				switch p.Reason {
				case sse.TurnEndCompleted:
					exit = 0
				case sse.TurnEndCancelled:
					exit = 130
				case sse.TurnEndMaxTurns:
					exit = 2
				default:
					if exit == 0 {
						exit = 3
					}
				}
				return finalText, exit
			}
		}
	}
}
