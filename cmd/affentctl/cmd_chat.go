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
	"github.com/affinefoundation/affent/internal/loopstate"
	"github.com/affinefoundation/affent/internal/planstate"
	"github.com/affinefoundation/affent/internal/sse"
	"github.com/affinefoundation/affent/internal/textutil"
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
  /plan draft <request>
              create/update a plan without executing other tools
  /plan execute [request]
              execute the confirmed active plan in this session
  /plan clear remove current session plan
  /loop       print current session loop protocol, if any
  /loop on [goal]
              create a draft LOOP.md and ask the agent to complete activation
  /loop off   disable current session LOOP.md
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
	if err := b.writeStartupTraceEvents(); err != nil {
		fmt.Fprintf(os.Stderr, "write startup trace events: %v\n", err)
		return exitRuntime
	}
	printStartupPlanSummary(b)
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

		turnText := line
		turnOpts := agent.TurnOptions{}
		loopActivationAttempt := false
		if strings.HasPrefix(line, "/") {
			prompt, opts, ok, err := chatPlanSlashTurn(line, b)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				continue
			}
			if ok {
				turnText = prompt
				turnOpts = opts
			} else {
				prompt, opts, sendTurn, handled, err := chatLoopSlashTurn(line, b)
				if err != nil {
					fmt.Fprintf(os.Stderr, "%v\n", err)
					continue
				}
				if sendTurn {
					turnText = prompt
					turnOpts = opts
					loopActivationAttempt = true
				} else if handled {
					continue
				} else {
					cont, exit := handleSlash(line, b)
					if !cont {
						return exit
					}
					continue
				}
			}
		}
		if !strings.HasPrefix(line, "/") {
			recordCurrentSessionLoopCalibrationAnswerIfReady(b, turnText)
		}

		// Per-turn cancellation context: Ctrl+C only kills the active
		// turn, not the REPL.
		turnCtx, cancelTurn := signal.NotifyContext(ctx, syscall.SIGINT)

		planBefore := currentSessionPlanSummary(b)
		turnID, err := b.loop.SendUserWithOptions(turnCtx, turnText, turnOpts)
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
		if loopActivationAttempt {
			finalizeCurrentSessionLoopActivation(b)
		}
		emitPlanChange(planBefore, currentSessionPlanSummary(b))
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
				marker := toolResultStatusLabel(p)
				fmt.Fprintln(os.Stderr, dim(fmt.Sprintf("  [tool] -> %s, %d bytes", marker, len(p.ResultSummary))))
				for _, line := range toolResultFailureDetails(p) {
					fmt.Fprintln(os.Stderr, dim("  [tool]    "+line))
				}
			case sse.TypeError:
				var p sse.ErrorPayload
				_ = json.Unmarshal(ev.Data, &p)
				label := p.Code
				if p.FailureKind != "" {
					label = fmt.Sprintf("%s, failure=%s", label, p.FailureKind)
				}
				fmt.Fprintln(os.Stderr, dim(fmt.Sprintf("  [error] %s: %s", label, p.Message)))
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
	// Show one-line preview of the most informative key for common
	// tool families; fall back to JSON when there is no obvious handle.
	for _, k := range []string{"command", "path", "url", "query", "name", "id"} {
		if v, ok := args[k]; ok {
			s := textutil.Preview(fmt.Sprint(v), 77, "...")
			return fmt.Sprintf("%s=%q", k, s)
		}
	}
	raw, _ := json.Marshal(args)
	s := textutil.Preview(string(raw), 77, "...")
	return s
}

func toolResultStatusLabel(p sse.ToolResultPayload) string {
	marker := "ok"
	if p.ExitCode != 0 {
		marker = fmt.Sprintf("exit %d", p.ExitCode)
	} else if p.FailureKind != "" || len(p.FailureKinds) > 0 {
		marker = "no evidence"
	}
	if len(p.FailureKinds) > 0 {
		return fmt.Sprintf("%s, failure=%s", marker, strings.Join(p.FailureKinds, "+"))
	}
	if p.FailureKind != "" {
		return fmt.Sprintf("%s, failure=%s", marker, p.FailureKind)
	}
	return marker
}

func toolResultFailureDetails(p sse.ToolResultPayload) []string {
	if p.ExitCode == 0 && p.FailureKind == "" && len(p.FailureKinds) == 0 {
		return nil
	}
	summary := strings.TrimSpace(p.ResultSummary)
	if summary == "" {
		return nil
	}
	var out []string
	for _, raw := range strings.Split(summary, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "Failure: kind=") {
			continue
		}
		out = append(out, compactToolResultDetailLine(line))
		if strings.HasPrefix(line, "Next:") || len(out) >= 2 {
			break
		}
	}
	return out
}

func compactToolResultDetailLine(line string) string {
	const max = 180
	if len(line) <= max {
		return line
	}
	return trimUTF8(line, max-3) + "..."
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
  /plan draft <request>
               create/update a plan without executing other tools
  /plan execute [request]
              execute the confirmed active plan in this session
  /plan clear  remove current session plan
  /loop       print current session loop protocol, if any
  /loop on [goal]
              create/use LOOP.md and ask the agent to refine it
  /loop off   disable current session LOOP.md
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
	case "/plan clear":
		clearCurrentSessionPlan(b)
		return true, 0
	case "/loop":
		printCurrentSessionLoopProtocol(b)
		return true, 0
	case "/loop off":
		clearCurrentSessionLoopProtocol(b)
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

func chatPlanSlashTurn(line string, b *loopBundle) (string, agent.TurnOptions, bool, error) {
	sub, arg, ok := parsePlanSlash(line)
	if !ok {
		return "", agent.TurnOptions{}, false, nil
	}
	switch sub {
	case "draft":
		if arg == "" {
			return "", agent.TurnOptions{}, false, fmt.Errorf("/plan draft requires a request")
		}
		if b == nil || b.loop == nil {
			return "", agent.TurnOptions{}, false, fmt.Errorf("agent loop is not initialized")
		}
		opts, err := agent.PlanOnlyTurnOptions(b.loop.Tools, runPlanOnlyMaxToolCalls)
		if err != nil {
			return "", agent.TurnOptions{}, false, err
		}
		return agent.PlanOnlyUserPrompt(arg), opts, true, nil
	case "execute":
		prompt, executePlanStepIndex, err := prepareRunExecutePlan(b, arg)
		if err != nil {
			return "", agent.TurnOptions{}, false, err
		}
		return prompt, agent.ExecutePlanTurnOptionsForStep(executePlanStepIndex), true, nil
	default:
		return "", agent.TurnOptions{}, false, nil
	}
}

func parsePlanSlash(line string) (subcommand, arg string, ok bool) {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < len("/plan") || !strings.EqualFold(trimmed[:len("/plan")], "/plan") {
		return "", "", false
	}
	rest := strings.TrimSpace(trimmed[len("/plan"):])
	if rest == "" {
		return "", "", false
	}
	sub, arg, _ := strings.Cut(rest, " ")
	sub = strings.ToLower(strings.TrimSpace(sub))
	arg = strings.TrimSpace(arg)
	switch sub {
	case "draft", "execute":
		return sub, arg, true
	default:
		return "", "", false
	}
}

func chatLoopSlashTurn(line string, b *loopBundle) (string, agent.TurnOptions, bool, bool, error) {
	sub, arg, ok := parseLoopSlash(line)
	if !ok {
		return "", agent.TurnOptions{}, false, false, nil
	}
	switch sub {
	case "on":
		if b == nil || b.loop == nil {
			return "", agent.TurnOptions{}, false, true, fmt.Errorf("agent loop is not initialized")
		}
		if err := createCurrentSessionLoopDraft(b, arg); err != nil {
			return "", agent.TurnOptions{}, false, true, err
		}
		fmt.Fprintf(os.Stderr, "[loop] draft created at %s; asking agent to complete activation\n", loopstate.ProtocolRelPath(b.sessionID))
		return loopActivationRefinementPrompt(arg, b.sessionID, loopstate.ProtocolRelPath(b.sessionID)), agent.TurnOptions{}, true, true, nil
	default:
		return "", agent.TurnOptions{}, false, false, nil
	}
}

func parseLoopSlash(line string) (subcommand, arg string, ok bool) {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < len("/loop") || !strings.EqualFold(trimmed[:len("/loop")], "/loop") {
		return "", "", false
	}
	rest := strings.TrimSpace(trimmed[len("/loop"):])
	if rest == "" {
		return "", "", false
	}
	sub, arg, _ := strings.Cut(rest, " ")
	sub = strings.ToLower(strings.TrimSpace(sub))
	arg = strings.TrimSpace(arg)
	switch sub {
	case "on":
		return sub, arg, true
	default:
		return "", "", false
	}
}

func createCurrentSessionLoopDraft(b *loopBundle, goal string) error {
	path := b.loopProtocolPath
	if strings.TrimSpace(path) == "" {
		path = loopstate.ProtocolPath(b.workspace, b.sessionID)
		b.loopProtocolPath = path
	}
	_, _, _, err := loopstate.EnsureProtocolTemplate(path, loopstate.ProtocolTemplateOptions{
		LoopID:       b.sessionID,
		OwnerSession: b.sessionID,
		Goal:         goal,
		Workspace:    b.workspace,
		Status:       "draft",
		Plan:         affentctlLoopProtocolCurrentPlanCheckpoint(b.planPath),
	})
	if err != nil {
		return fmt.Errorf("create loop protocol draft: %w", err)
	}
	if b.loop != nil {
		b.loop.LoopProtocolPath = path
		if b.loop.Tools != nil {
			agent.RegisterLoopProtocolOnly(b.loop.Tools, path)
		}
	}
	return nil
}

func finalizeCurrentSessionLoopActivation(b *loopBundle) {
	if b == nil || strings.TrimSpace(b.loopProtocolPath) == "" {
		return
	}
	content, found, err := loopstate.ReadProtocol(b.loopProtocolPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[loop] activation check failed: %v\n", err)
		return
	}
	if !found || markdownLoopStatus(content) != "running" {
		fmt.Fprintln(os.Stderr, "[loop] still pending: LOOP.md must be supplemented and metadata status set to running before active loop feeds start")
		return
	}
	if _, _, err := loopstate.RecordProtocolActivation(b.loopProtocolPath, "agent completed loop activation"); err != nil {
		fmt.Fprintf(os.Stderr, "[loop] activation persist failed: %v\n", err)
		return
	}
	b.loop.LoopProtocolPath = b.loopProtocolPath
	if !b.loopProtocolSkillInstalled {
		b.loop.CompletionGuards = append(b.loop.CompletionGuards, agent.LoopProtocolCompletionGuard(b.loopProtocolPath))
		b.loop.SkillProvider = agent.WithLoopProtocolSkillProviderWithCheckpoint(b.loopProtocolPath, affentctlLoopProtocolPlanCheckpointProvider(b.planPath), b.loop.SkillProvider)
		b.loopProtocolSkillInstalled = true
	}
	fmt.Fprintf(os.Stderr, "[loop] active: %s\n", loopstate.ProtocolRelPath(b.sessionID))
}

func recordCurrentSessionLoopCalibrationAnswerIfReady(b *loopBundle, text string) {
	if b == nil || strings.TrimSpace(text) == "" {
		return
	}
	path := b.loopProtocolPath
	if strings.TrimSpace(path) == "" {
		path = loopstate.ProtocolPath(b.workspace, b.sessionID)
	}
	state, found, err := loopstate.ReadState(filepath.Join(filepath.Dir(path), loopstate.StateFileName))
	if err != nil || !found || state.Status != "draft" {
		return
	}
	if state.CalibrationQuestions > 0 {
		if state.CalibrationAnswers >= state.CalibrationQuestions {
			return
		}
	} else {
		return
	}
	state, _, err = loopstate.RecordProtocolCalibrationAnswer(path, text)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[loop] calibration record failed: %v\n", err)
		return
	}
	publishCurrentSessionLoopCalibrationEvent(b, sse.TypeLoopCalibration, state)
}

func publishCurrentSessionLoopCalibrationEvent(b *loopBundle, typ string, state loopstate.State) {
	if b == nil {
		return
	}
	loopID := strings.TrimSpace(state.LoopID)
	if loopID == "" {
		loopID = strings.TrimSpace(b.sessionID)
	}
	if loopID == "" {
		loopID = "loop"
	}
	ev, err := sse.NewEvent(typ, sse.LoopProtocolCalibrationPayload{
		LoopID:                  loopID,
		Status:                  state.Status,
		CalibrationQuestions:    state.CalibrationQuestions,
		LastCalibrationQuestion: state.LastCalibrationQuestion,
		CalibrationAnswers:      state.CalibrationAnswers,
		LastCalibrationAnswer:   state.LastCalibrationAnswer,
		ProtocolPath:            loopstate.ProtocolRelPath(loopID),
		EventSeq:                state.EventCount,
	})
	if err != nil {
		return
	}
	if b.events != nil {
		select {
		case b.events <- ev:
			return
		default:
		}
	}
	if b.recorder != nil {
		_ = b.recorder.Write(ev)
	}
}

func markdownLoopStatus(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(key), "status") {
			return strings.ToLower(strings.TrimSpace(value))
		}
	}
	return ""
}

func loopActivationRefinementPrompt(goal, sessionID, relPath string) string {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		goal = "the user's current long-running objective"
	}
	return `Loop protocol activation is pending, not active yet.

Your job is to understand the user's real intention for this long-running loop, ask concise clarification questions if required, and supplement ` + relPath + ` before the loop can be considered active.

Required activation criteria:
1. Understand and summarize the user's underlying intent in the North Star section.
2. Add a compact Current Situation snapshot, practical stop conditions, likely failure modes, recovery anchors, and memory lookup/update rules only as durable rules when needed.
3. Keep task step authority in plan state; do not duplicate a todo list into LOOP.md.
4. Ask the user at least one concise calibration question before activation, even when the initial goal seems clear.
5. If information is still missing, ask at most two specific questions and leave metadata status as draft.
6. Only after the user answers and the protocol is sufficiently supplemented, edit LOOP.md metadata to status: running.

Candidate goal:
` + goal + `

Session: ` + sessionID
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

func clearCurrentSessionPlan(b *loopBundle) {
	convDir := filepath.Join(b.workspace, ".affentctl")
	removed, err := clearLocalSessionPlan(convDir, b.sessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "clear plan: %v\n", err)
		return
	}
	if !removed {
		fmt.Fprintf(os.Stderr, "no active plan for session %s\n", b.sessionID)
		return
	}
	fmt.Fprintf(os.Stderr, "cleared plan for session %s\n", b.sessionID)
}

func printCurrentSessionLoopProtocol(b *loopBundle) {
	if b == nil {
		return
	}
	path := b.loopProtocolPath
	if strings.TrimSpace(path) == "" {
		path = loopstate.ProtocolPath(b.workspace, b.sessionID)
	}
	content, found, err := loopstate.ReadProtocol(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read loop protocol: %v\n", err)
		return
	}
	if !found {
		fmt.Fprintf(os.Stderr, "no loop protocol for session %s\n", b.sessionID)
		return
	}
	fmt.Fprintln(os.Stderr, content)
}

func clearCurrentSessionLoopProtocol(b *loopBundle) {
	if b == nil {
		return
	}
	path := b.loopProtocolPath
	if strings.TrimSpace(path) == "" {
		path = loopstate.ProtocolPath(b.workspace, b.sessionID)
	}
	removed, err := loopstate.RemoveProtocol(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "clear loop protocol: %v\n", err)
		return
	}
	b.loopProtocolSkillInstalled = false
	if b.loop != nil {
		b.loop.LoopProtocolPath = ""
	}
	if !removed {
		fmt.Fprintf(os.Stderr, "no loop protocol for session %s\n", b.sessionID)
		return
	}
	fmt.Fprintf(os.Stderr, "disabled loop protocol for session %s\n", b.sessionID)
}

func currentSessionPlanSummary(b *loopBundle) planstate.Summary {
	convDir := filepath.Join(b.workspace, ".affentctl")
	return localSessionPlanSummary(convDir, b.sessionID)
}

func emitPlanChange(before, after planstate.Summary) {
	if before == after {
		return
	}
	if line := formatPlanChangeLine(after); line != "" {
		fmt.Fprintln(os.Stderr, line)
	}
}

func printStartupPlanSummary(b *loopBundle) {
	if line := formatExistingPlanLine(currentSessionPlanSummary(b)); line != "" {
		fmt.Fprintln(os.Stderr, line)
	}
}

func formatPlanChangeLine(summary planstate.Summary) string {
	if summary.Label == planstate.LabelMissing {
		return "[plan] cleared"
	}
	return formatExistingPlanLine(summary)
}

func formatExistingPlanLine(summary planstate.Summary) string {
	switch summary.Label {
	case "":
		return ""
	case planstate.LabelMissing:
		return ""
	case planstate.LabelEmpty, planstate.LabelError:
		return "[plan] " + summary.Label
	}
	line := "[plan] " + summary.Label
	if summary.CurrentStepIndex > 0 {
		line += fmt.Sprintf(" - step %d", summary.CurrentStepIndex)
		if strings.TrimSpace(summary.CurrentStep) != "" {
			line += ": " + oneLine(summary.CurrentStep, 120)
		}
	}
	return line
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
