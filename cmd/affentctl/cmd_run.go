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

	"github.com/affinefoundation/affent/sse"
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
		return 64
	}

	prompt, err := readMaybeStdin(*promptFlag)
	if err != nil || strings.TrimSpace(prompt) == "" {
		fmt.Fprintln(os.Stderr, "--prompt is required (use '-' for stdin)")
		return 64
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
		b.log.Info().Str("session_id", b.sessionID).Msg("new session")
	}

	turnID, err := b.loop.SendUser(ctx, prompt)
	if err != nil {
		b.log.Error().Err(err).Msg("send user")
		return 3
	}
	b.log.Info().Str("turn_id", turnID).Msg("turn started")

	finalText, exit := drainBatch(ctx, b.events, b.trace, b.log, cf.traceSkipDeltas)
	if finalText != "" {
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
func drainBatch(ctx context.Context, events <-chan sse.Event, trace io.Writer, log zerolog.Logger, skipDeltas bool) (string, int) {
	enc := json.NewEncoder(trace)
	enc.SetEscapeHTML(false)

	var finalText string
	exit := 0
	turnEnded := false

	for {
		select {
		case <-ctx.Done():
			log.Warn().Msg("interrupted")
			return finalText, 130
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
			case sse.TypeMessageEnd:
				var p sse.MessageEndPayload
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
				default:
					if p.Reason == "max_turns" || p.Reason == "length" {
						exit = 2
					} else if exit == 0 {
						exit = 3
					}
				}
				return finalText, exit
			}
		}
	}
}
