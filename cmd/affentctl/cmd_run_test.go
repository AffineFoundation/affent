package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/loopstate"
)

func TestEnableRunPlanOnlyInstallsPlanPolicyAndBudget(t *testing.T) {
	reg := agent.NewRegistry()
	reg.Add(&agent.Tool{
		Name: agent.PlanToolName,
		Execute: func(context.Context, json.RawMessage) (string, error) {
			return "{}", nil
		},
	})
	reg.Add(&agent.Tool{
		Name: "shell",
		Execute: func(context.Context, json.RawMessage) (string, error) {
			t.Fatal("plan-only must not keep executable non-plan tools")
			return "", nil
		},
	})
	b := &loopBundle{loop: &agent.Loop{Tools: reg}}

	if err := enableRunPlanOnly(b); err != nil {
		t.Fatalf("enableRunPlanOnly: %v", err)
	}
	if b.loop.FirstToolPolicy == nil || b.loop.FirstToolPolicy.ToolName != agent.PlanToolName {
		t.Fatalf("first tool policy = %+v, want plan", b.loop.FirstToolPolicy)
	}
	if b.loop.MaxToolCalls != runPlanOnlyMaxToolCalls {
		t.Fatalf("MaxToolCalls = %d, want %d", b.loop.MaxToolCalls, runPlanOnlyMaxToolCalls)
	}
	if !b.loop.FinalNoToolsOnMaxTurns {
		t.Fatal("FinalNoToolsOnMaxTurns should be enabled for plan-only")
	}
	defs := b.loop.Tools.Defs()
	if len(defs) != 1 || defs[0].Function.Name != agent.PlanToolName {
		t.Fatalf("plan-only tool defs = %+v, want only plan", defs)
	}
	if _, ok := b.loop.Tools.Get("shell"); ok {
		t.Fatal("plan-only should remove non-plan tools from the run registry")
	}
}

func TestEnableRunPlanOnlyRequiresPlanTool(t *testing.T) {
	err := enableRunPlanOnly(&loopBundle{loop: &agent.Loop{Tools: agent.NewRegistry()}})
	if err == nil || !strings.Contains(err.Error(), "plan tool is not available") {
		t.Fatalf("enableRunPlanOnly error = %v, want missing plan tool", err)
	}
}

func TestPlanOnlyTurnOptionsNarrowsToolSurface(t *testing.T) {
	reg := testPlanRegistry(t)
	reg.Add(&agent.Tool{
		Name: "shell",
		Execute: func(context.Context, json.RawMessage) (string, error) {
			t.Fatal("plan-only turn options must not keep shell")
			return "", nil
		},
	})
	opts, err := agent.PlanOnlyTurnOptions(reg, 2)
	if err != nil {
		t.Fatalf("PlanOnlyTurnOptions: %v", err)
	}
	if opts.FirstToolPolicy == nil || opts.FirstToolPolicy.ToolName != agent.PlanToolName {
		t.Fatalf("first tool policy = %+v, want plan", opts.FirstToolPolicy)
	}
	if opts.MaxToolCalls != 2 || !opts.FinalNoToolsOnMaxTurns {
		t.Fatalf("options = %+v, want bounded plan-only turn", opts)
	}
	if opts.UserMode != agent.UserModePlanOnly {
		t.Fatalf("UserMode = %q, want %q", opts.UserMode, agent.UserModePlanOnly)
	}
	defs := opts.Tools.Defs()
	if len(defs) != 1 || defs[0].Function.Name != agent.PlanToolName {
		t.Fatalf("plan-only defs = %+v, want only plan", defs)
	}
}

func TestRunRecordsLoopCalibrationAnswerBeforeTurn(t *testing.T) {
	workspace := t.TempDir()
	sessionID := "run-loop-answer"
	protocolPath := loopstate.ProtocolPath(workspace, sessionID)
	if _, _, _, err := loopstate.EnsureProtocolTemplate(protocolPath, loopstate.ProtocolTemplateOptions{
		LoopID:       sessionID,
		OwnerSession: sessionID,
		Goal:         "keep a long-running market evidence loop aligned",
		Workspace:    workspace,
		Status:       "draft",
	}); err != nil {
		t.Fatalf("EnsureProtocolTemplate: %v", err)
	}
	conv, err := agent.OpenConversationAt(filepath.Join(workspace, ".affentctl", sessionID+".jsonl"))
	if err != nil {
		t.Fatalf("OpenConversationAt: %v", err)
	}
	if err := conv.Append(agent.ChatMessage{Role: "assistant", Content: "For this loop, what stop condition should pause work?"}); err != nil {
		t.Fatalf("append assistant question: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"Recorded the calibration answer.\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprintf(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	tracePath := filepath.Join(workspace, "trace.jsonl")
	out := captureStdout(t, func() {
		code := runCmd([]string{
			"--workspace", workspace,
			"--session-id", sessionID,
			"--model", "fake-model",
			"--base-url", srv.URL,
			"--prompt", "Pause if source quality is weak or the market report is complete.",
			"--trace", tracePath,
			"--trace-skip-deltas",
			"--quiet",
			"--max-turns", "1",
		})
		if code != 0 {
			t.Fatalf("runCmd exit = %d", code)
		}
	})
	if !strings.Contains(out, "Recorded the calibration answer.") {
		t.Fatalf("runCmd stdout = %q", out)
	}
	state, found, err := loopstate.ReadState(loopstate.StatePath(workspace, sessionID))
	if err != nil || !found {
		t.Fatalf("ReadState found=%v err=%v", found, err)
	}
	if state.CalibrationQuestions != 1 ||
		state.CalibrationAnswers != 1 ||
		!strings.Contains(state.LastCalibrationQuestion, "stop condition") ||
		!strings.Contains(state.LastCalibrationAnswer, "Pause if source quality") {
		t.Fatalf("calibration state = %+v", state)
	}
	trace, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	for _, want := range []string{
		`"type":"loop.protocol_calibration_request"`,
		`"type":"loop.protocol_calibration"`,
		`"last_calibration_answer_preview":"Pause if source quality is weak or the market report is complete."`,
	} {
		if !strings.Contains(string(trace), want) {
			t.Fatalf("trace missing %q:\n%s", want, trace)
		}
	}
}

func TestRunLoopProtocolInitialTurnRecordsLoopSetupMode(t *testing.T) {
	workspace := t.TempDir()
	sessionID := "run-loop-setup"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"What pause condition should stop this loop?\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprintf(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	tracePath := filepath.Join(workspace, "trace.jsonl")
	out := captureStdout(t, func() {
		code := runCmd([]string{
			"--workspace", workspace,
			"--session-id", sessionID,
			"--model", "fake-model",
			"--base-url", srv.URL,
			"--prompt", "Start a long-running loop for repo reliability.",
			"--loop-protocol",
			"--trace", tracePath,
			"--trace-skip-deltas",
			"--quiet",
			"--max-turns", "1",
		})
		if code != 0 {
			t.Fatalf("runCmd exit = %d", code)
		}
	})
	if !strings.Contains(out, "pause condition") {
		t.Fatalf("runCmd stdout = %q", out)
	}
	trace, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	for _, want := range []string{
		`"type":"user.message"`,
		`"mode":"loop_setup"`,
		`Start a long-running loop for repo reliability.`,
	} {
		if !strings.Contains(string(trace), want) {
			t.Fatalf("trace missing %q:\n%s", want, trace)
		}
	}
}

func TestValidateRunModeFlags(t *testing.T) {
	for _, tc := range []struct {
		name        string
		flags       commonFlags
		planOnly    bool
		executePlan bool
		wantErr     string
	}{
		{name: "normal run", flags: commonFlags{}, wantErr: ""},
		{name: "plan only", flags: commonFlags{}, planOnly: true, wantErr: ""},
		{name: "execute explicit session", flags: commonFlags{sessionID: "planned"}, executePlan: true, wantErr: ""},
		{name: "execute continue", flags: commonFlags{continueLast: true}, executePlan: true, wantErr: ""},
		{name: "conflict", flags: commonFlags{sessionID: "planned"}, planOnly: true, executePlan: true, wantErr: "cannot be used together"},
		{name: "execute needs selected session", flags: commonFlags{}, executePlan: true, wantErr: "requires --session-id or --continue"},
		{name: "blank session id still needs selection", flags: commonFlags{sessionID: " \t "}, executePlan: true, wantErr: "requires --session-id or --continue"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRunModeFlags(tc.flags, tc.planOnly, tc.executePlan)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("validateRunModeFlags error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("validateRunModeFlags error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestPrepareRunExecutePlanUsesPersistedUnfinishedPlan(t *testing.T) {
	workspace := t.TempDir()
	convDir := filepath.Join(workspace, ".affentctl")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localSessionPlanPath(convDir, "planned"), []byte(`{"version":1,"steps":[{"text":"inspect","status":"completed"},{"text":"ship","status":"pending"}]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := &loopBundle{loop: &agent.Loop{Tools: testPlanRegistry(t)}, workspace: workspace, sessionID: "planned"}

	got, stepIndex, err := prepareRunExecutePlan(b, "")
	if err != nil {
		t.Fatalf("prepareRunExecutePlan: %v", err)
	}
	if stepIndex != 2 {
		t.Fatalf("execute plan step index = %d, want 2", stepIndex)
	}
	for _, want := range []string{
		"Execute-plan mode is enabled.",
		"The user has confirmed execution",
		"plan:1/2",
		"Execute only the current unfinished step first",
		"call plan with action=update for that same step",
		"Proceed with the active persisted plan.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("execute-plan prompt missing %q:\n%s", want, got)
		}
	}
}

func TestPrepareRunExecutePlanRejectsNonExecutablePlans(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{name: "missing", want: "has no persisted plan"},
		{name: "empty", raw: `{"version":1,"steps":[]}`, want: "empty plan"},
		{name: "done", raw: `{"version":1,"steps":[{"text":"done","status":"completed"}]}`, want: "already done"},
		{name: "blocked", raw: `{"version":1,"steps":[{"text":"need input","status":"blocked"}]}`, want: "plan is blocked"},
		{name: "bad", raw: `{`, want: "unreadable plan"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			convDir := filepath.Join(workspace, ".affentctl")
			if err := os.MkdirAll(convDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.raw != "" {
				if err := os.WriteFile(localSessionPlanPath(convDir, "planned"), []byte(tc.raw), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			_, _, err := prepareRunExecutePlan(&loopBundle{loop: &agent.Loop{Tools: testPlanRegistry(t)}, workspace: workspace, sessionID: "planned"}, "continue")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("prepareRunExecutePlan error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestPrepareRunExecutePlanRequiresPlanTool(t *testing.T) {
	workspace := t.TempDir()
	convDir := filepath.Join(workspace, ".affentctl")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localSessionPlanPath(convDir, "planned"), []byte(`{"version":1,"steps":[{"text":"ship","status":"pending"}]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := prepareRunExecutePlan(&loopBundle{loop: &agent.Loop{Tools: agent.NewRegistry()}, workspace: workspace, sessionID: "planned"}, "")
	if err == nil || !strings.Contains(err.Error(), "plan tool is not available") {
		t.Fatalf("prepareRunExecutePlan error = %v, want missing plan tool", err)
	}
}

func TestRunPlanOnlyNextStepLinePrintsExecutablePlanCommand(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "work dir's")
	convDir := filepath.Join(workspace, ".affentctl")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localSessionPlanPath(convDir, "planned"), []byte(`{"version":1,"steps":[{"text":"ship","status":"pending"}]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := runPlanOnlyNextStepLine(&loopBundle{workspace: workspace, sessionID: "planned"})
	for _, want := range []string{
		`[plan] saved for session "planned"`,
		"affentctl run",
		"--execute-plan",
		"--workspace " + shellQuoteForEnv(workspace),
		"--session-id 'planned'",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("next-step line missing %q:\n%s", want, got)
		}
	}
}

func testPlanRegistry(t *testing.T) *agent.Registry {
	t.Helper()
	reg := agent.NewRegistry()
	reg.Add(&agent.Tool{
		Name: agent.PlanToolName,
		Execute: func(context.Context, json.RawMessage) (string, error) {
			return "{}", nil
		},
	})
	return reg
}

func TestRunPlanOnlyNextStepLineSkipsNonExecutablePlans(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{name: "missing"},
		{name: "empty", raw: `{"version":1,"steps":[]}`},
		{name: "done", raw: `{"version":1,"steps":[{"text":"done","status":"completed"}]}`},
		{name: "blocked", raw: `{"version":1,"steps":[{"text":"need input","status":"blocked"}]}`},
		{name: "bad", raw: `{`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			convDir := filepath.Join(workspace, ".affentctl")
			if err := os.MkdirAll(convDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.raw != "" {
				if err := os.WriteFile(localSessionPlanPath(convDir, "planned"), []byte(tc.raw), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if got := runPlanOnlyNextStepLine(&loopBundle{workspace: workspace, sessionID: "planned"}); got != "" {
				t.Fatalf("next-step line = %q, want empty", got)
			}
		})
	}
}
