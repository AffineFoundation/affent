package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/affinefoundation/affent/internal/sse"
	"github.com/rs/zerolog"
)

func runCmd(args []string) int {
	var cf commonFlags
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cf.bind(fs)
	promptFlag := fs.String("prompt", "", "user prompt; '-' for stdin, '@file', or literal")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `usage: affentctl run [flags]

One-shot: feed a single prompt, drive the loop until turn.end, print
the final assistant text on stdout. Designed for training / eval.

Required: --prompt, --model.`)
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

	prompt, err := readMaybeStdin(*promptFlag)
	if err != nil {
		// Distinguish "you forgot --prompt" (which goes to the
		// "use '-' for stdin" hint) from "your @file path is wrong"
		// (which the user needs to see verbatim to fix the typo).
		fmt.Fprintf(os.Stderr, "--prompt: %v\n", err)
		return exitUsage
	}
	if strings.TrimSpace(prompt) == "" {
		fmt.Fprintln(os.Stderr, "--prompt is required (use '-' for stdin)")
		return exitUsage
	}

	b, code := setupLoop(cf)
	if code != 0 {
		return code
	}
	defer b.close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if b.resumed {
		b.log.Info().Str("session_id", b.sessionID).Msg("resumed")
	} else {
		if err := writeTraceMeta(b.trace); err != nil {
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

	finalText, exit := drainBatch(ctx, b.loop, b.events, b.trace, b.log, cf.traceSkipDeltas)
	// When the user routed the trace to stdout (--trace -), printing the
	// final assistant text on stdout too would interleave plain markdown
	// into the JSONL stream and break every batch-eval consumer that
	// pipes stdout through jq. The text is already on stdout via the
	// message.done event in the trace, so we just skip the extra print.
	if finalText != "" && cf.tracePath != "-" {
		fmt.Println(finalText)
	}
	return exit
}

// drainBatch is the run-mode drain: writes every event to trace, returns
// the assistant's last final text and an exit code on turn.end. When
// skipDeltas is true, thinking.delta and message.delta are observed for
// state but not written to trace — the final text remains available
// through message.end. Useful for batch eval where token-level replay
// has no value but trace size matters.
//
// loop is the active *agent.Loop and may be nil only in unit tests. On
// SIGINT (ctx.Done) we MUST call Loop.Cancel — the Loop runs its turn on
// a detached background context so the parent ctx cancelling has no
// effect on in-flight LLM calls or shell-tool processes. Without the
// explicit Cancel a `shell exec sleep 60` survives the SIGKILL on
// affentctl, leaving an orphan in the user's process table.
func drainBatch(ctx context.Context, loop interface{ Cancel() }, events <-chan sse.Event, trace io.Writer, log zerolog.Logger, skipDeltas bool) (string, int) {
	enc := json.NewEncoder(trace)
	enc.SetEscapeHTML(false)

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
					writeTrace := !skipDeltas ||
						(ev.Type != sse.TypeMessageDelta && ev.Type != sse.TypeThinkingDelta)
					if writeTrace {
						if err := enc.Encode(ev); err != nil {
							log.Error().Err(err).Msg("write trace")
						}
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
			writeTrace := !skipDeltas ||
				(ev.Type != sse.TypeMessageDelta && ev.Type != sse.TypeThinkingDelta)
			if writeTrace {
				if err := enc.Encode(ev); err != nil {
					log.Error().Err(err).Msg("write trace")
				}
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
