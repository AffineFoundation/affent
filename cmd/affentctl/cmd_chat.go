package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/eventlog"
	"github.com/affinefoundation/affent/internal/sse"
)

// turnCancelDrainTimeout caps how long drainInteractive blocks after a
// SIGINT cancel waiting for the Loop's `turn.end{cancelled}` event.
// affent's Cancel() signals the loop but the in-flight LLM call /
// tool dispatch needs a beat to wind down before publishing turn.end.
// Without this wait the REPL's next SendUser races the still-active
// turn and gets ErrTurnInFlight.
const turnCancelDrainTimeout = 5 * time.Second

// chatCmd is the REPL: read a line, drive one turn, stream the
// assistant text + tool activity to stderr, then read the next line.
// All turns share one *agent.Loop so the conversation accumulates and
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
  /plan       print current session plan, if any
  /usage      running token totals for this session (input/output/total)
  /exit       quit (Ctrl+D also works)
  /cancel     interrupt a background turn (e.g. a cron-fired one). To
              cancel the turn you just kicked off from this prompt,
              use Ctrl+C — the REPL is busy streaming events and
              can't read a slash command while a turn is in flight.`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if err := applyConfig(&cf, fs); err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return exitUsage
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
		if err := b.recorder.WriteMeta(); err != nil {
			fmt.Fprintf(os.Stderr, "write trace metadata: %v\n", err)
			return exitRuntime
		}
		fmt.Fprintf(os.Stderr, "new session %s (workspace %s)\n", b.sessionID, b.workspace)
	}
	fmt.Fprintln(os.Stderr, "type your message; '/help' for commands, '/exit' or Ctrl+D to quit.")

	// Top-level signal handling: Ctrl+C cancels the in-flight turn (if
	// any) and falls back to "stop the REPL" if pressed while idle.
	ctx, stopAll := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stopAll()

	reader := bufio.NewReader(os.Stdin)

	// onEOF is the post-EOF cleanup: print a newline so the next shell
	// prompt isn't glued onto our last "> ", then exit 0. We DON'T exit
	// here when bufio returned a partial last line — that pattern fires
	// for `echo -n "hi" | affentctl chat`, where dropping the line
	// would silently swallow the user's actual message.
	onEOF := func() int {
		fmt.Fprintln(os.Stderr)
		return 0
	}

	for {
		fmt.Fprint(os.Stderr, "\n> ")
		line, err := reader.ReadString('\n')
		eofWithData := errors.Is(err, io.EOF) && line != ""
		if errors.Is(err, io.EOF) && !eofWithData {
			return onEOF()
		}
		if err != nil && !eofWithData {
			fmt.Fprintf(os.Stderr, "read input: %v\n", err)
			return exitRuntime
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
			if errors.Is(err, agent.ErrTurnInFlight) {
				fmt.Fprintln(os.Stderr, "(a turn is still running; type /cancel to interrupt)")
				continue
			}
			fmt.Fprintf(os.Stderr, "send user: %v\n", err)
			return exitRuntime
		}
		_ = turnID

		inTok, outTok := drainInteractive(turnCtx, b.loop, b.events, b.recorder)
		b.inputTokens += inTok
		b.outputTokens += outTok
		if inTok > 0 || outTok > 0 {
			b.turnsSeen++
		}
		cancelTurn()
	}
}

// drainInteractive reads events for one turn, printing assistant text
// live to stdout, tool activity compactly to stderr. rec records every
// event as JSONL — minus thinking/message deltas when the recorder was
// built with SkipDeltas. Returns the per-turn token totals seen on
// TypeUsage so the REPL's loopBundle can accumulate session-lifetime
// spend for /usage.
//
// loop is the active *agent.Loop. On SIGINT (ctx.Done) we MUST call
// Loop.Cancel — the Loop runs the turn on a detached background ctx
// so cancelling the parent ctx alone leaves in-flight LLM calls and
// shell-tool processes alive (e.g. `shell exec sleep 60` orphans).
func drainInteractive(ctx context.Context, loop interface{ Cancel() }, events <-chan sse.Event, rec *eventlog.Recorder) (inputTokens, outputTokens int) {
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
			// Loop runs on a detached ctx so signalling alone leaves
			// the in-flight turn (and any shell process) alive. Cancel
			// explicitly, then block until turn.end so the REPL's next
			// SendUser sees a clean Loop state. Without both, the next
			// message gets ErrTurnInFlight, forcing the user to retry or run
			// /cancel again. Bounded so a hung loop doesn't pin the
			// REPL.
			if loop != nil {
				loop.Cancel()
			}
			deadline := time.After(turnCancelDrainTimeout)
			for {
				select {
				case ev, ok := <-events:
					if !ok {
						return
					}
					_ = rec.Write(ev)
					if ev.Type == sse.TypeTurnEnd {
						return
					}
				case <-deadline:
					return
				}
			}
		case ev, ok := <-events:
			if !ok {
				return
			}
			_ = rec.Write(ev)
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
				inputTokens += p.InputTokens
				outputTokens += p.OutputTokens
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
				s = trimUTF8(s, 77) + "..."
			}
			return fmt.Sprintf("%s=%q", k, s)
		}
	}
	raw, _ := json.Marshal(args)
	s := string(raw)
	if len(s) > 80 {
		s = trimUTF8(s, 77) + "..."
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
  /plan        print current session plan, if any
  /usage       running token totals (input/output) for this session
  /cancel      interrupt a background (cron-fired) turn; use Ctrl+C
               to cancel a turn you typed from this prompt
  /exit        quit`)
		return true, 0
	case "/sid":
		fmt.Fprintln(os.Stderr, b.sessionID)
		return true, 0
	case "/plan":
		printCurrentSessionPlan(b)
		return true, 0
	case "/usage":
		fmt.Fprintf(os.Stderr, "session %s — %d turn(s), input=%d output=%d total=%d tokens\n",
			b.sessionID, b.turnsSeen, b.inputTokens, b.outputTokens, b.inputTokens+b.outputTokens)
		return true, 0
	case "/cancel":
		b.loop.Cancel()
		return true, 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", line)
		return true, 0
	}
}

func printCurrentSessionPlan(b *loopBundle) {
	convDir := filepath.Join(b.workspace, ".affentctl")
	plan, found, err := readLocalSessionPlan(convDir, b.sessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read plan: %v\n", err)
		return
	}
	if !found {
		fmt.Fprintf(os.Stderr, "no active plan for session %s\n", b.sessionID)
		return
	}
	fmt.Fprintln(os.Stderr, formatSessionPlanForChat(b.sessionID, plan))
}

type chatPlanState struct {
	UpdatedAt string         `json:"updated_at"`
	Steps     []chatPlanStep `json:"steps"`
}

type chatPlanStep struct {
	Text     string   `json:"text"`
	Status   string   `json:"status"`
	Evidence []string `json:"evidence"`
	Note     string   `json:"note"`
}

func formatSessionPlanForChat(sessionID string, raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	var st chatPlanState
	if err := json.Unmarshal(raw, &st); err != nil || len(st.Steps) == 0 {
		return string(raw)
	}
	var out strings.Builder
	fmt.Fprintf(&out, "plan for session %s", sessionID)
	if strings.TrimSpace(st.UpdatedAt) != "" {
		fmt.Fprintf(&out, " (updated %s)", strings.TrimSpace(st.UpdatedAt))
	}
	out.WriteByte('\n')
	for i, step := range st.Steps {
		status := strings.TrimSpace(step.Status)
		if status == "" {
			status = "pending"
		}
		text := strings.TrimSpace(step.Text)
		if text == "" {
			text = "(untitled)"
		}
		fmt.Fprintf(&out, "%d. [%s] %s\n", i+1, status, oneLine(text, 160))
		if evidence := compactPlanEvidence(step.Evidence); evidence != "" {
			fmt.Fprintf(&out, "   evidence: %s\n", evidence)
		}
		if note := strings.TrimSpace(step.Note); note != "" {
			fmt.Fprintf(&out, "   note: %s\n", oneLine(note, 160))
		}
	}
	return strings.TrimRight(out.String(), "\n")
}

func compactPlanEvidence(evidence []string) string {
	refs := make([]string, 0, len(evidence))
	for _, ref := range evidence {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		refs = append(refs, oneLine(ref, 120))
	}
	return strings.Join(refs, ", ")
}

func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
