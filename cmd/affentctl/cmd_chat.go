package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/affinefoundation/affent"
	"github.com/affinefoundation/affent/sse"
)

// chatCmd is the REPL: read a line, drive one turn, stream the
// assistant text + tool activity to stderr, then read the next line.
// All turns share one *affent.Loop so the conversation accumulates and
// is persisted to .affentctl/<sid>.jsonl across runs.
func chatCmd(args []string) int {
	var cf commonFlags
	fs := flag.NewFlagSet("chat", flag.ExitOnError)
	cf.bind(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, `usage: affentctl chat [flags]

REPL: type a message, see the agent's reply stream back, then type
the next message. Conversation persists under
<workspace>/.affentctl/<session_id>.jsonl; use --continue or
--session-id to resume.

Slash commands inside the REPL:
  /help       show commands
  /sid        print current session id
  /exit       quit (Ctrl+D also works)
  /cancel     interrupt the current turn (Ctrl+C also works)`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 64
	}

	// Chat is interactive; the JSONL trace would dump every event
	// inline with the prompt. Default it to /dev/null unless the user
	// explicitly asks for a path. They can pass --trace - to mingle.
	if cf.tracePath == "" {
		cf.tracePath = os.DevNull
	}

	b, code := setupLoop(cf)
	if code != 0 {
		return code
	}
	defer b.close()

	if b.resumed {
		fmt.Fprintf(os.Stderr, "resumed session %s (workspace %s)\n", b.sessionID, b.workspace)
	} else {
		fmt.Fprintf(os.Stderr, "new session %s (workspace %s)\n", b.sessionID, b.workspace)
	}
	fmt.Fprintln(os.Stderr, "type your message; '/help' for commands, '/exit' or Ctrl+D to quit.")

	// Top-level signal handling: Ctrl+C cancels the in-flight turn (if
	// any) and falls back to "stop the REPL" if pressed while idle.
	ctx, stopAll := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stopAll()

	reader := bufio.NewReader(os.Stdin)

	traceEnc := json.NewEncoder(b.trace)
	traceEnc.SetEscapeHTML(false)

	for {
		fmt.Fprint(os.Stderr, "\n> ")
		line, err := reader.ReadString('\n')
		if err == io.EOF {
			fmt.Fprintln(os.Stderr)
			return 0
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "read input: %v\n", err)
			return 3
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "/") {
			cont, exit := handleSlash(line, b)
			if !cont {
				return exit
			}
			continue
		}

		// Per-turn cancellation context: Ctrl+C only kills the active
		// turn, not the REPL.
		turnCtx, cancelTurn := signal.NotifyContext(ctx, syscall.SIGINT)

		turnID, err := b.loop.SendUser(turnCtx, line)
		if err != nil {
			cancelTurn()
			if errors.Is(err, affent.ErrTurnInFlight) {
				fmt.Fprintln(os.Stderr, "(a turn is still running; type /cancel to interrupt)")
				continue
			}
			fmt.Fprintf(os.Stderr, "send user: %v\n", err)
			return 3
		}
		_ = turnID

		drainInteractive(turnCtx, b.events, traceEnc, cf.traceSkipDeltas)
		cancelTurn()
	}
}

// drainInteractive reads events for one turn, printing assistant text
// live to stdout, tool activity compactly to stderr. trace gets every
// event in JSONL — except thinking/message deltas when skipDeltas is on.
func drainInteractive(ctx context.Context, events <-chan sse.Event, trace *json.Encoder, skipDeltas bool) {
	const (
		ansiDim   = "\x1b[2m"
		ansiReset = "\x1b[0m"
	)
	useColor := isTTY(os.Stderr)
	dim := func(s string) string {
		if !useColor {
			return s
		}
		return ansiDim + s + ansiReset
	}

	thinkingShown := false
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(os.Stderr, dim("\n[interrupted]"))
			// Drain any straggler events so the next turn starts on a
			// clean channel; bail out as soon as we see turn.end.
			for {
				select {
				case ev, ok := <-events:
					if !ok {
						return
					}
					if !skipDeltas || (ev.Type != sse.TypeMessageDelta && ev.Type != sse.TypeThinkingDelta) {
						_ = trace.Encode(ev)
					}
					if ev.Type == sse.TypeTurnEnd {
						return
					}
				default:
					return
				}
			}
		case ev, ok := <-events:
			if !ok {
				return
			}
			if !skipDeltas || (ev.Type != sse.TypeMessageDelta && ev.Type != sse.TypeThinkingDelta) {
				_ = trace.Encode(ev)
			}
			switch ev.Type {
			case sse.TypeThinkingDelta:
				if !thinkingShown {
					fmt.Fprint(os.Stderr, dim("[thinking...] "))
					thinkingShown = true
				}
			case sse.TypeMessageDelta:
				var p sse.MessageDeltaPayload
				_ = json.Unmarshal(ev.Data, &p)
				if thinkingShown {
					fmt.Fprintln(os.Stderr)
					thinkingShown = false
				}
				fmt.Fprint(os.Stdout, p.Delta)
			case sse.TypeMessageDone:
				fmt.Fprintln(os.Stdout)
			case sse.TypeToolRequest:
				var p sse.ToolRequestPayload
				_ = json.Unmarshal(ev.Data, &p)
				fmt.Fprintln(os.Stderr, dim(fmt.Sprintf("  [tool] %s %s", p.Tool, summarizeArgs(p.Args))))
			case sse.TypeToolResult:
				var p sse.ToolResultPayload
				_ = json.Unmarshal(ev.Data, &p)
				marker := "ok"
				if p.ExitCode != 0 {
					marker = fmt.Sprintf("exit %d", p.ExitCode)
				}
				fmt.Fprintln(os.Stderr, dim(fmt.Sprintf("  [tool] -> %s, %d bytes", marker, len(p.ResultSummary))))
			case sse.TypeError:
				var p sse.ErrorPayload
				_ = json.Unmarshal(ev.Data, &p)
				fmt.Fprintln(os.Stderr, dim(fmt.Sprintf("  [error] %s: %s", p.Code, p.Message)))
			case sse.TypeUsage:
				var p sse.UsagePayload
				_ = json.Unmarshal(ev.Data, &p)
				fmt.Fprintln(os.Stderr, dim(fmt.Sprintf("  [usage] in=%d out=%d", p.InputTokens, p.OutputTokens)))
			case sse.TypeTurnEnd:
				var p sse.TurnEndPayload
				_ = json.Unmarshal(ev.Data, &p)
				if p.Reason != sse.TurnEndCompleted {
					fmt.Fprintln(os.Stderr, dim(fmt.Sprintf("  [turn end: %s]", p.Reason)))
				}
				return
			}
		}
	}
}

func summarizeArgs(args map[string]any) string {
	if len(args) == 0 {
		return "{}"
	}
	// Show one-line preview of the most informative key. shell.command
	// and read_file.path are the typical cases; fall back to JSON.
	for _, k := range []string{"command", "path", "name", "id"} {
		if v, ok := args[k]; ok {
			s := fmt.Sprint(v)
			if len(s) > 80 {
				s = s[:77] + "..."
			}
			return fmt.Sprintf("%s=%q", k, s)
		}
	}
	raw, _ := json.Marshal(args)
	s := string(raw)
	if len(s) > 80 {
		s = s[:77] + "..."
	}
	return s
}

// handleSlash returns (continueLoop, exitCode). When continueLoop is
// false the REPL exits with the given code.
func handleSlash(line string, b *loopBundle) (bool, int) {
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "/exit", "/quit", "/q":
		return false, 0
	case "/help", "/h", "/?":
		fmt.Fprintln(os.Stderr, `commands:
  /help        show this
  /sid         print current session id
  /cancel      interrupt the current turn (or use Ctrl+C)
  /exit        quit`)
		return true, 0
	case "/sid":
		fmt.Fprintln(os.Stderr, b.sessionID)
		return true, 0
	case "/cancel":
		b.loop.Cancel()
		return true, 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", line)
		return true, 0
	}
}

func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
