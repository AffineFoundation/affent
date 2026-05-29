package agenteval

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/jsonl"
	"github.com/affinefoundation/affent/internal/sse"
	"github.com/affinefoundation/affent/internal/textutil"
)

const (
	DefaultBatchTimeout              = 5 * time.Minute
	DefaultBatchMaxTurnSteps         = 10
	DefaultSetupCommandTimeout       = 30 * time.Second
	DefaultVerifierOutputCapBytes    = 1 * 1024 * 1024
	maxDebugToolRepairExamples       = 5
	maxDebugLoopGuardExamples        = 5
	maxDebugToolTruncationExamples   = 5
	maxDebugMemoryUpdateExamples     = 5
	maxDebugMemorySearchMissExamples = 5
	maxDebugSessionSearchExamples    = 5
	maxDebugPlanExamples             = 5
	maxDebugChildTranscriptRefs      = 20
	maxTraceLineBytes                = jsonl.DefaultMaxRecordBytes
)

type ToolOrderRequirement struct {
	Earlier string
	Later   string
}

type CommandToolOrderRequirement struct {
	Command string
	Tool    string
}

type CommandOrderRequirement struct {
	Earlier string
	Later   string
}

type ToolArgContainsRequirement struct {
	Tool      string
	Arg       string
	Substring string
	// Min is the required number of matching tool calls. Values <=0 default
	// to one so scenarios do not need to spell out the common case.
	Min int
	// Max is the allowed number of matching tool calls for max constraints.
	// Values <=0 default to one so duplicate guards stay compact.
	Max int
}

type LoopDecisionRequirement struct {
	Kind                    string
	Decision                string
	Trigger                 string
	MinTokenBudget          int
	MinObservedInputTokens  int
	MinProjectedInputTokens int
	MinBudgetBytes          int
	// Min is the required number of matching loop.decision events. Values
	// <=0 default to one so scenarios can spell the common case tersely.
	Min int
}

type LoopProtocolFeedRequirement struct {
	Mode                          string
	PlanLabelContains             string
	PlanCurrentStepStatus         string
	PlanCurrentStep               string
	CurrentSituation              string
	LastTurnEndReason             string
	MinLastTurnToolRequests       int
	MinLastTurnToolErrors         int
	MinLastTurnForcedNoTools      int
	MinLastTurnMemoryUpdates      int
	MinLastTurnMemorySearchCalls  int
	MinLastTurnMemorySearchMisses int
	MinLastTurnSessionSearchCalls int
	MinLastTurnLoopGuards         int
	LastDecisionKind              string
	LastDecisionTrigger           string
	LastDecision                  string
	LastDecisionConfidence        string
	LastDecisionReason            string
	LastDecisionAction            string
	MinLastDecisionTokenBudget    int
	MinLastDecisionObservedInput  int
	MinLastDecisionProjectedInput int
	MinLastDecisionBudgetBytes    int
	// Min is the required number of matching loop.protocol_feed events.
	// Values <=0 default to one so scenarios can spell the common case tersely.
	Min int
}

type SourceAccessRequirement struct {
	Status               string
	Tool                 string
	URLContains          string
	RequestedURLContains string
	SourceMethod         string
	JSONPath             string
	// Min is the required number of matching SourceAccess results. Values
	// <=0 default to one so scenarios can spell the common case tersely.
	Min int
}

type SessionSearchRequirement struct {
	QueryContains   string
	SessionID       string
	SnippetContains string
	MatchedTerms    []string
	ContextIncluded bool
	TurnIdx         int
	// Min is the required number of matching session_search hits. Values
	// <=0 default to one so scenarios can spell the common case tersely.
	Min int
}

type RecentSessionSearchRequirement struct {
	QueryContains     string
	SessionID         string
	UserContains      string
	AssistantContains string
	PlanContains      string
	LoopContains      string
	RecoveryContains  string
	MessageContains   string
	// Min is the required number of matching recent-session anchors. Values
	// <=0 default to one so scenarios can spell the common case tersely.
	Min int
}

type BatchScenario struct {
	Name                                           string
	Suites                                         []string
	Domains                                        []string
	Prompt                                         string
	Prompts                                        []string
	SessionID                                      string
	ExecutePlan                                    bool
	EnableMemory                                   bool
	EnableLoopProtocol                             bool
	ExposeLoopProtocolTool                         bool
	RequiredTurnEndReason                          string
	Files                                          map[string]string
	SetupCommands                                  []string
	SourceRepoURL                                  string
	SourceRepoRef                                  string
	SourceRepoDir                                  string
	VerifyCommand                                  string
	VerifierTimeout                                time.Duration
	ExpectedSkill                                  string
	ForbiddenCommands                              []string
	ForbidWorkspaceAbsolutePaths                   bool
	RequiredCommands                               []string
	RequiredCommandCounts                          map[string]int
	RequiredToolCounts                             map[string]int
	RequiredToolFailureKindCounts                  map[string]int
	MaxToolFailureKindCounts                       map[string]int
	RequiredToolStatsAtLeast                       map[string]int
	RequiredTraceEventCounts                       map[string]int
	RequiredUserMessageModes                       map[string]int
	ForbiddenUserMessageModes                      []string
	RequiredTaskStateRequestMode                   string
	RequiredTaskStateRequestSource                 string
	RequiredTaskStateScheduleKind                  string
	RequiredConversationRepairStatsAtLeast         map[string]int
	RequiredConversationRepairKinds                map[string]int
	RequiredLoopDecisionKinds                      map[string]int
	RequiredLoopDecisionResults                    map[string]int
	RequiredLoopDecisionMatches                    []LoopDecisionRequirement
	RequiredMessageRejected                        map[string]int
	RequiredCompletionGuards                       []string
	RequiredLoopProtocolFeeds                      int
	RequiredLoopProtocolCalibrationRequests        int
	RequiredLoopProtocolCalibrations               int
	RequiredLoopProtocolCalibrationRequestStatuses map[string]int
	RequiredLoopProtocolCalibrationStatuses        map[string]int
	RequiredLoopProtocolFeedModes                  map[string]int
	RequiredLoopProtocolFeedMatches                []LoopProtocolFeedRequirement
	RequireLoopProtocolFullAfterCompact            bool
	RequiredLoopProtocolFinalStatus                string
	RequiredSourceAccess                           []SourceAccessRequirement
	RequiredSessionSearch                          []SessionSearchRequirement
	RequiredRecentSessionSearch                    []RecentSessionSearchRequirement
	RequiredContextInjectionSources                map[string]int
	RequiredContextInjectionText                   map[string][]string
	RequiredContextCompactions                     int
	RequiredReactiveCompactions                    int
	RequiredContextCompactionReasons               map[string]int
	RequiredCompactionRemovedMsgs                  int
	RequiredCompactionReducedBytes                 int
	RequiredContextSummaryText                     []string
	RequiredContextLoopProtocolAnchorText          []string
	RequiredCommandBeforeTool                      []CommandToolOrderRequirement
	RequiredCommandAfterTool                       []CommandToolOrderRequirement
	RequiredCommandOrder                           []CommandOrderRequirement
	RequiredTools                                  []string
	ForbiddenTools                                 []string
	RequiredFocusedTaskCounts                      map[string]int
	RequiredFocusedTaskSourceCounts                map[string]int
	RequiredSubagentModeCounts                     map[string]int
	RequiredSubagentSourceCounts                   map[string]int
	RequireNoDelegationErrors                      bool
	RequireNoPlanErrors                            bool
	RequireFinalPlanCompleted                      bool
	RequiredFinalText                              []string
	ForbiddenFinalText                             []string
	RequiredToolResultText                         map[string][]string
	RequiredToolArgContains                        []ToolArgContainsRequirement
	ForbiddenToolArgContains                       []ToolArgContainsRequirement
	MaxToolArgContains                             []ToolArgContainsRequirement
	RequiredTruncatedResults                       []string
	RequiredResultArtifacts                        []string
	RequiredToolOrder                              []ToolOrderRequirement
	ProtectedFiles                                 []string
	RequiredFileSubstrings                         map[string][]string
	ForbiddenFileSubstrings                        map[string][]string
	MaxParentToolCalls                             int
	MaxSuccessfulToolCallsByTool                   map[string]int
	RuntimeMaxTurnInputTokens                      int
	MaxLoopTurnInputTokens                         int
	MaxLoopTurnTotalTokens                         int
	MaxTurns                                       int
	CompactTrigger                                 int
	CompactTriggerInputTokens                      int
	CompactKeepLast                                int
}

type BatchRunner struct {
	RepoRoot                 string
	WorkRoot                 string
	BaseURL                  string
	APIKey                   string
	Model                    string
	Temperature              string
	TopP                     string
	MaxTokens                string
	Seed                     string
	Executor                 string
	RuntimeEvalMode          bool
	RuntimeTools             string
	RuntimeAllTools          bool
	RuntimeMemory            bool
	RuntimeWeb               bool
	RuntimeBrowser           bool
	RuntimeMCPConfig         string
	TraceDeltas              bool
	GoBin                    string
	Timeout                  time.Duration
	VerifierOutputCapBytes   int
	CleanupPassingWorkspaces bool
}

type BatchResult struct {
	BatchScenario                   string
	Workspace                       string
	TraceWorkspace                  string
	ChildTranscriptRoot             string
	TracePath                       string
	DebugManifestPath               string
	TimelinePath                    string
	FinalTextPath                   string
	StdoutPath                      string
	StderrPath                      string
	AffentctlCommand                []string
	RunExitCode                     int
	OK                              bool
	Expectations                    *DebugScenarioExpectations
	Failures                        []string
	Duration                        time.Duration
	FinalText                       string
	TraceSchemaVersion              int
	TraceEvents                     int
	TraceEventTypes                 map[string]int
	TurnEndReason                   string
	ToolCalls                       int
	ToolStats                       ToolRuntimeStats
	RuntimeErrorByKind              map[string]int
	RuntimeErrorExamples            map[string][]RuntimeErrorExample
	ConversationRepairs             []sse.ConversationRepairedPayload
	LoopDecisionStats               LoopDecisionStats
	MessageRejectedStats            MessageRejectedStats
	LoopProtocolFeeds               LoopProtocolFeedStats
	LoopProtocolCalibrationRequests LoopProtocolCalibrationStats
	LoopProtocolCalibrations        LoopProtocolCalibrationStats
	LoopProtocolSetupOverrun        LoopProtocolSetupOverrunStats
	LoopTurnCheckpoints             LoopTurnCheckpointStats
	ContextInjections               ContextInjectionStats
	ContextCompactions              ContextCompactionStats
	ToolRepairExamples              []ToolRepairExample
	ToolFailureExamples             map[string][]ToolFailureExample
	LoopGuardExamples               []LoopGuardExample
	SourceAccessExamples            []SourceAccessExample
	BrowserScrollExamples           []BrowserScrollExample
	BrowserNetworkExamples          []BrowserNetworkSearchExample
	MemoryUpdateExamples            []MemoryUpdateExample
	MemorySearchMissExamples        []MemorySearchMissExample
	SessionSearchExamples           []SessionSearchExample
	PlanExamples                    []PlanExample
	ToolTruncationExamples          []ToolTruncationExample
	ToolTruncation                  ToolTruncationStats
	WorkspacePath                   WorkspacePathStats
	Usage                           Usage
	Verifier                        VerifierResult
	WorkspaceRemoved                bool
	CleanupError                    string
	TraceDeltas                     bool
	ChildTranscripts                []DebugTranscriptRef
	// Delegation aggregates focused-task / subagent calls observed
	// in the trace. Zero-value when the scenario used no delegation
	// tool; HasAny() reports whether the block is worth surfacing.
	Delegation DelegationStats
	// Plan aggregates persisted-plan tool usage. Zero-value when the
	// scenario did not call the plan tool.
	Plan PlanStats
	// Repair aggregates tool-call recovery kinds from tool.request
	// repair_notes. Zero-value when no tool repair/canonicalization
	// occurred.
	Repair ToolRepairStats
	// RuntimeSurface is the latest effective tool/runtime surface observed
	// in the trace. Nil for old traces or runs that failed before turn start.
	RuntimeSurface *sse.RuntimeSurfacePayload
	// TaskState is the derived task-state snapshot used by debug output and
	// eval JSONL to keep long-run progress auditable.
	TaskState TaskStateSnapshot
}

type DebugManifest struct {
	SchemaVersion                          int                               `json:"schema_version"`
	Scenario                               string                            `json:"scenario"`
	OK                                     bool                              `json:"ok"`
	Workspace                              string                            `json:"workspace"`
	TraceWorkspace                         string                            `json:"trace_workspace,omitempty"`
	ChildTranscriptRoot                    string                            `json:"child_transcript_root,omitempty"`
	TracePath                              string                            `json:"trace_path"`
	TimelinePath                           string                            `json:"timeline_path,omitempty"`
	FinalTextPath                          string                            `json:"final_text_path,omitempty"`
	StdoutPath                             string                            `json:"stdout_path,omitempty"`
	StderrPath                             string                            `json:"stderr_path,omitempty"`
	AffentctlCommand                       []string                          `json:"affentctl_command,omitempty"`
	RunExitCode                            int                               `json:"run_exit_code"`
	ConversationDir                        string                            `json:"conversation_dir,omitempty"`
	ArtifactDir                            string                            `json:"artifact_dir,omitempty"`
	TraceDeltas                            bool                              `json:"trace_deltas,omitempty"`
	Prompt                                 string                            `json:"prompt"`
	Prompts                                []string                          `json:"prompts,omitempty"`
	Expectations                           DebugScenarioExpectations         `json:"expectations,omitempty"`
	ExpectationCapabilityNames             []string                          `json:"expectation_capability_names,omitempty"`
	ExpectationCapabilityOutcome           string                            `json:"expectation_capability_outcome,omitempty"`
	ExpectationCapabilityPassedNames       []string                          `json:"expectation_capability_passed_names,omitempty"`
	ExpectationCapabilityFailedNames       []string                          `json:"expectation_capability_failed_names,omitempty"`
	Failures                               []string                          `json:"failures,omitempty"`
	QualityGatesPassed                     *bool                             `json:"quality_gates_passed,omitempty"`
	QualityGateFailures                    []string                          `json:"quality_gate_failures,omitempty"`
	Verifier                               *DebugVerifierResult              `json:"verifier,omitempty"`
	DebugBrief                             *DebugBrief                       `json:"debug_brief,omitempty"`
	RecoveryGuide                          *DebugRecoveryGuide               `json:"recovery_guide,omitempty"`
	ToolRepairExamples                     []ToolRepairExample               `json:"tool_repair_examples,omitempty"`
	ConversationRepairExamples             []sse.ConversationRepairedPayload `json:"conversation_repair_examples,omitempty"`
	MessageRejectedExamples                []MessageRejected                 `json:"message_rejected_examples,omitempty"`
	LoopGuardExamples                      []LoopGuardExample                `json:"loop_guard_examples,omitempty"`
	LoopTurnCheckpointExamples             []LoopTurnCheckpoint              `json:"loop_turn_checkpoint_examples,omitempty"`
	LoopProtocolFeedExamples               []LoopProtocolFeed                `json:"loop_protocol_feed_examples,omitempty"`
	LoopProtocolCalibrationRequestExamples []LoopProtocolCalibration         `json:"loop_protocol_calibration_request_examples,omitempty"`
	LoopProtocolCalibrationExamples        []LoopProtocolCalibration         `json:"loop_protocol_calibration_examples,omitempty"`
	ContextInjectionExamples               []ContextInjection                `json:"context_injection_examples,omitempty"`
	SourceAccessExamples                   []SourceAccessExample             `json:"source_access_examples,omitempty"`
	BrowserScrollExamples                  []BrowserScrollExample            `json:"browser_scroll_examples,omitempty"`
	BrowserNetworkExamples                 []BrowserNetworkSearchExample     `json:"browser_network_examples,omitempty"`
	MemoryUpdateExamples                   []MemoryUpdateExample             `json:"memory_update_examples,omitempty"`
	MemorySearchMissExamples               []MemorySearchMissExample         `json:"memory_search_miss_examples,omitempty"`
	SessionSearchExamples                  []SessionSearchExample            `json:"session_search_examples,omitempty"`
	PlanExamples                           []PlanExample                     `json:"plan_examples,omitempty"`
	ToolTruncationExamples                 []ToolTruncationExample           `json:"tool_truncation_examples,omitempty"`
	ContextCompactionExamples              []ContextCompaction               `json:"context_compaction_examples,omitempty"`
	ChildTranscripts                       []DebugTranscriptRef              `json:"child_transcripts,omitempty"`
	Metrics                                DebugMetrics                      `json:"metrics"`
	RuntimeSurface                         *sse.RuntimeSurfacePayload        `json:"runtime_surface,omitempty"`
	TaskState                              *TaskStateSnapshot                `json:"task_state,omitempty"`
	GeneratedAt                            string                            `json:"generated_at"`
}

type DebugRecoveryGuide struct {
	Summary               string   `json:"summary,omitempty"`
	Inspect               []string `json:"inspect,omitempty"`
	ExactRerunCommand     []string `json:"exact_rerun_command,omitempty"`
	FullTraceRerunCommand []string `json:"full_trace_rerun_command,omitempty"`
	ContinuePrompt        string   `json:"continue_prompt,omitempty"`
}

type DebugScenarioExpectations struct {
	CheckNames                                     []string                              `json:"check_names,omitempty"`
	Suites                                         []string                              `json:"suites,omitempty"`
	Domains                                        []string                              `json:"domains,omitempty"`
	SessionID                                      string                                `json:"session_id,omitempty"`
	ExecutePlan                                    bool                                  `json:"execute_plan,omitempty"`
	EnableMemory                                   bool                                  `json:"enable_memory,omitempty"`
	EnableLoopProtocol                             bool                                  `json:"enable_loop_protocol,omitempty"`
	ExposeLoopProtocolTool                         bool                                  `json:"expose_loop_protocol_tool,omitempty"`
	RequiredTurnEndReason                          string                                `json:"required_turn_end_reason,omitempty"`
	VerifyCommand                                  string                                `json:"verify_command,omitempty"`
	SetupCommands                                  []string                              `json:"setup_commands,omitempty"`
	SourceRepoURL                                  string                                `json:"source_repo_url,omitempty"`
	SourceRepoRef                                  string                                `json:"source_repo_ref,omitempty"`
	SourceRepoDir                                  string                                `json:"source_repo_dir,omitempty"`
	ExpectedSkill                                  string                                `json:"expected_skill,omitempty"`
	RequiredTools                                  []string                              `json:"required_tools,omitempty"`
	ForbiddenTools                                 []string                              `json:"forbidden_tools,omitempty"`
	RequiredCommands                               []string                              `json:"required_commands,omitempty"`
	ForbiddenCommands                              []string                              `json:"forbidden_commands,omitempty"`
	ForbidWorkspaceAbsolutePaths                   bool                                  `json:"forbid_workspace_absolute_paths,omitempty"`
	RequiredCommandCounts                          map[string]int                        `json:"required_command_counts,omitempty"`
	RequiredCommandOrder                           []DebugCommandOrderRequirement        `json:"required_command_order,omitempty"`
	RequiredToolCounts                             map[string]int                        `json:"required_tool_counts,omitempty"`
	RequiredToolFailureKindCounts                  map[string]int                        `json:"required_tool_failure_kind_counts,omitempty"`
	MaxToolFailureKindCounts                       map[string]int                        `json:"max_tool_failure_kind_counts,omitempty"`
	RequiredToolStatsAtLeast                       map[string]int                        `json:"required_tool_stats_at_least,omitempty"`
	RequiredTraceEventCounts                       map[string]int                        `json:"required_trace_event_counts,omitempty"`
	RequiredUserMessageModes                       map[string]int                        `json:"required_user_message_modes,omitempty"`
	ForbiddenUserMessageModes                      []string                              `json:"forbidden_user_message_modes,omitempty"`
	RequiredTaskStateRequestMode                   string                                `json:"required_task_state_request_mode,omitempty"`
	RequiredTaskStateRequestSource                 string                                `json:"required_task_state_request_source,omitempty"`
	RequiredTaskStateScheduleKind                  string                                `json:"required_task_state_schedule_kind,omitempty"`
	RequiredConversationRepairStatsAtLeast         map[string]int                        `json:"required_conversation_repair_stats_at_least,omitempty"`
	RequiredConversationRepairKinds                map[string]int                        `json:"required_conversation_repair_kinds,omitempty"`
	RequiredLoopDecisionKinds                      map[string]int                        `json:"required_loop_decision_kinds,omitempty"`
	RequiredLoopDecisionResults                    map[string]int                        `json:"required_loop_decision_results,omitempty"`
	RequiredLoopDecisionMatches                    []DebugLoopDecisionRequirement        `json:"required_loop_decision_matches,omitempty"`
	RequiredMessageRejected                        map[string]int                        `json:"required_message_rejected,omitempty"`
	RequiredCompletionGuards                       []string                              `json:"required_completion_guards,omitempty"`
	RequiredLoopProtocolFeeds                      int                                   `json:"required_loop_protocol_feeds,omitempty"`
	RequiredLoopProtocolCalibrationRequests        int                                   `json:"required_loop_protocol_calibration_requests,omitempty"`
	RequiredLoopProtocolCalibrations               int                                   `json:"required_loop_protocol_calibrations,omitempty"`
	RequiredLoopProtocolCalibrationRequestStatuses map[string]int                        `json:"required_loop_protocol_calibration_request_statuses,omitempty"`
	RequiredLoopProtocolCalibrationStatuses        map[string]int                        `json:"required_loop_protocol_calibration_statuses,omitempty"`
	RequiredLoopProtocolFeedModes                  map[string]int                        `json:"required_loop_protocol_feed_modes,omitempty"`
	RequiredLoopProtocolFeedMatches                []DebugLoopProtocolFeedRequirement    `json:"required_loop_protocol_feed_matches,omitempty"`
	RequireLoopProtocolFullAfterCompact            bool                                  `json:"require_loop_protocol_full_after_compaction,omitempty"`
	RequiredLoopProtocolFinalStatus                string                                `json:"required_loop_protocol_final_status,omitempty"`
	RequiredToolResultText                         map[string][]string                   `json:"required_tool_result_text,omitempty"`
	RequiredToolArgContains                        []DebugToolArgContainsRequirement     `json:"required_tool_arg_contains,omitempty"`
	ForbiddenToolArgContains                       []DebugToolArgContainsRequirement     `json:"forbidden_tool_arg_contains,omitempty"`
	MaxToolArgContains                             []DebugToolArgContainsRequirement     `json:"max_tool_arg_contains,omitempty"`
	RequiredSourceAccess                           []DebugSourceAccessRequirement        `json:"required_source_access,omitempty"`
	RequiredSessionSearch                          []DebugSessionSearchRequirement       `json:"required_session_search,omitempty"`
	RequiredRecentSessionSearch                    []DebugRecentSessionSearchRequirement `json:"required_recent_session_search,omitempty"`
	RequiredContextInjectionSources                map[string]int                        `json:"required_context_injection_sources,omitempty"`
	RequiredContextInjectionText                   map[string][]string                   `json:"required_context_injection_text,omitempty"`
	RequiredCommandBeforeTool                      []DebugCommandToolOrderRequirement    `json:"required_command_before_tool,omitempty"`
	RequiredCommandAfterTool                       []DebugCommandToolOrderRequirement    `json:"required_command_after_tool,omitempty"`
	RequiredToolOrder                              []DebugToolOrderRequirement           `json:"required_tool_order,omitempty"`
	RequiredFocusedTaskCounts                      map[string]int                        `json:"required_focused_task_counts,omitempty"`
	RequiredFocusedTaskSourceCounts                map[string]int                        `json:"required_focused_task_source_counts,omitempty"`
	RequiredSubagentModeCounts                     map[string]int                        `json:"required_subagent_mode_counts,omitempty"`
	RequiredSubagentSourceCounts                   map[string]int                        `json:"required_subagent_source_counts,omitempty"`
	RequireNoDelegationErrors                      bool                                  `json:"require_no_delegation_errors,omitempty"`
	RequireNoPlanErrors                            bool                                  `json:"require_no_plan_errors,omitempty"`
	RequireFinalPlanCompleted                      bool                                  `json:"require_final_plan_completed,omitempty"`
	RequiredFinalText                              []string                              `json:"required_final_text,omitempty"`
	ForbiddenFinalText                             []string                              `json:"forbidden_final_text,omitempty"`
	RequiredTruncatedResults                       []string                              `json:"required_truncated_results,omitempty"`
	RequiredResultArtifacts                        []string                              `json:"required_result_artifacts,omitempty"`
	RequiredContextCompactions                     int                                   `json:"required_context_compactions,omitempty"`
	RequiredReactiveCompactions                    int                                   `json:"required_reactive_context_compactions,omitempty"`
	RequiredContextCompactionReasons               map[string]int                        `json:"required_context_compaction_reasons,omitempty"`
	RequiredCompactionRemovedMsgs                  int                                   `json:"required_compaction_removed_messages,omitempty"`
	RequiredCompactionReducedBytes                 int                                   `json:"required_compaction_reduced_bytes,omitempty"`
	RequiredContextSummaryText                     []string                              `json:"required_context_summary_text,omitempty"`
	RequiredContextLoopProtocolAnchorText          []string                              `json:"required_context_loop_protocol_anchor_text,omitempty"`
	ProtectedFiles                                 []string                              `json:"protected_files,omitempty"`
	RequiredFileSubstrings                         map[string][]string                   `json:"required_file_substrings,omitempty"`
	ForbiddenFileSubstrings                        map[string][]string                   `json:"forbidden_file_substrings,omitempty"`
	MaxParentToolCalls                             int                                   `json:"max_parent_tool_calls,omitempty"`
	MaxSuccessfulToolCallsByTool                   map[string]int                        `json:"max_successful_tool_calls_by_tool,omitempty"`
	RuntimeMaxTurnInputTokens                      int                                   `json:"runtime_max_turn_input_tokens,omitempty"`
	MaxLoopTurnInputTokens                         int                                   `json:"max_loop_turn_input_tokens,omitempty"`
	MaxLoopTurnTotalTokens                         int                                   `json:"max_loop_turn_total_tokens,omitempty"`
	MaxTurns                                       int                                   `json:"max_turns,omitempty"`
	CompactTrigger                                 int                                   `json:"compact_trigger,omitempty"`
	CompactTriggerInputTokens                      int                                   `json:"compact_trigger_input_tokens,omitempty"`
	CompactKeepLast                                int                                   `json:"compact_keep_last,omitempty"`
}

// ExpectationCapabilityNames derives broad capability families from a
// scenario's declarative expectations. The labels intentionally match
// affenteval summary fields so scenario rows, debug manifests, timelines, and
// batch summaries group regressions the same way.
func ExpectationCapabilityNames(exp DebugScenarioExpectations) []string {
	caps := map[string]bool{}
	if strings.TrimSpace(exp.SessionID) != "" {
		caps["session"] = true
	}
	if len(exp.RequiredUserMessageModes) > 0 {
		caps["session"] = true
		for mode := range exp.RequiredUserMessageModes {
			switch mode {
			case agent.UserModePlanOnly, agent.UserModeExecutePlan:
				caps["plan"] = true
			case "loop_setup":
				caps["loop_protocol"] = true
			}
		}
	}
	if strings.TrimSpace(exp.RequiredTaskStateRequestMode) != "" || strings.TrimSpace(exp.RequiredTaskStateRequestSource) != "" || strings.TrimSpace(exp.RequiredTaskStateScheduleKind) != "" {
		caps["session"] = true
		caps["trace"] = true
	}
	if exp.ExecutePlan || exp.RequireNoPlanErrors || exp.RequireFinalPlanCompleted {
		caps["plan"] = true
	}
	if exp.EnableMemory {
		caps["memory"] = true
	}
	if exp.ExposeLoopProtocolTool {
		caps["loop_protocol"] = true
	}
	if exp.RuntimeMaxTurnInputTokens > 0 {
		caps["input_budget"] = true
		caps["trace"] = true
	}
	if strings.TrimSpace(exp.ExpectedSkill) != "" {
		caps["skill"] = true
	}
	if expectationRequiresSkillInstall(exp) {
		caps["skill"] = true
		caps["skill_install"] = true
	}
	if strings.TrimSpace(exp.SourceRepoURL) != "" {
		caps["source_repo"] = true
		caps["workspace"] = true
	}
	if exp.VerifyCommand != "" {
		caps["verifier"] = true
	}
	if len(exp.RequiredSourceAccess) > 0 {
		caps["source_access"] = true
	}
	if len(exp.RequiredSessionSearch) > 0 || len(exp.RequiredRecentSessionSearch) > 0 {
		caps["session_search"] = true
	}
	if len(exp.RequiredContextInjectionSources) > 0 {
		caps["context_injection"] = true
	}
	if len(exp.RequiredContextInjectionText) > 0 {
		caps["context_injection"] = true
	}
	for _, req := range exp.RequiredSourceAccess {
		addExpectationToolCapabilities(caps, req.Tool)
	}
	if exp.RequiredContextCompactions > 0 ||
		exp.RequiredReactiveCompactions > 0 ||
		len(exp.RequiredContextCompactionReasons) > 0 ||
		exp.RequiredCompactionRemovedMsgs > 0 ||
		exp.RequiredCompactionReducedBytes > 0 ||
		len(exp.RequiredContextSummaryText) > 0 ||
		len(exp.RequiredContextLoopProtocolAnchorText) > 0 {
		caps["context_compaction"] = true
	}
	if exp.RequiredLoopProtocolFeeds > 0 ||
		exp.RequiredLoopProtocolCalibrationRequests > 0 ||
		exp.RequiredLoopProtocolCalibrations > 0 ||
		len(exp.RequiredLoopProtocolCalibrationRequestStatuses) > 0 ||
		len(exp.RequiredLoopProtocolCalibrationStatuses) > 0 ||
		len(exp.RequiredLoopProtocolFeedModes) > 0 ||
		len(exp.RequiredLoopProtocolFeedMatches) > 0 ||
		exp.RequireLoopProtocolFullAfterCompact ||
		strings.TrimSpace(exp.RequiredLoopProtocolFinalStatus) != "" {
		caps["loop_protocol"] = true
		for _, req := range exp.RequiredLoopProtocolFeedMatches {
			if req.PlanLabelContains != "" || req.PlanCurrentStepStatus != "" || req.PlanCurrentStep != "" {
				caps["plan"] = true
				break
			}
		}
	}
	if exp.RequiredLoopProtocolFeeds > 0 &&
		len(exp.RequiredRecentSessionSearch) > 0 &&
		expectationRequiresToolOrder(exp, agent.SessionSearchToolName, agent.MemoryToolName) {
		caps["longrun_recovery"] = true
	}
	if len(exp.RequiredMessageRejected) > 0 {
		caps["trace"] = true
		for trigger := range exp.RequiredMessageRejected {
			switch {
			case strings.Contains(trigger, "loop_protocol"):
				caps["loop_protocol"] = true
			case strings.Contains(trigger, "plan"):
				caps["plan"] = true
			}
		}
	}
	if len(exp.RequiredCompletionGuards) > 0 {
		caps["trace"] = true
		for _, guard := range exp.RequiredCompletionGuards {
			switch {
			case strings.Contains(guard, "loop_protocol"):
				caps["loop_protocol"] = true
			case strings.Contains(guard, "plan"):
				caps["plan"] = true
			}
		}
	}
	if expectationRequiresResearchCheckpoint(exp) {
		caps["research_checkpoint"] = true
	}
	if expectationRequiresLoopDecisionKind(exp, "input_budget") {
		caps["input_budget"] = true
		caps["trace"] = true
	}
	if expectationRequiresDelegatedSourceEvidence(exp) {
		caps["delegated_source_evidence"] = true
	}
	if len(exp.RequiredFocusedTaskCounts) > 0 ||
		len(exp.RequiredFocusedTaskSourceCounts) > 0 ||
		len(exp.RequiredSubagentModeCounts) > 0 ||
		len(exp.RequiredSubagentSourceCounts) > 0 ||
		exp.RequireNoDelegationErrors {
		caps["delegation"] = true
	}
	for _, tool := range expectationRequiredToolNames(exp) {
		addExpectationToolCapabilities(caps, tool)
	}
	for stat := range exp.RequiredToolStatsAtLeast {
		addExpectationStatCapabilities(caps, stat)
	}
	if len(exp.RequiredTraceEventCounts) > 0 || len(exp.RequiredUserMessageModes) > 0 || len(exp.ForbiddenUserMessageModes) > 0 || strings.TrimSpace(exp.RequiredTaskStateRequestMode) != "" || strings.TrimSpace(exp.RequiredTaskStateRequestSource) != "" || strings.TrimSpace(exp.RequiredTaskStateScheduleKind) != "" {
		caps["trace"] = true
	}
	if len(exp.RequiredConversationRepairStatsAtLeast) > 0 || len(exp.RequiredConversationRepairKinds) > 0 {
		caps["trace"] = true
		caps["session"] = true
	}
	for range exp.RequiredCommandBeforeTool {
		caps["workspace"] = true
	}
	for range exp.RequiredCommandAfterTool {
		caps["workspace"] = true
	}
	if len(exp.RequiredCommandOrder) > 0 || len(exp.RequiredCommands) > 0 || len(exp.RequiredCommandCounts) > 0 {
		caps["workspace"] = true
	}
	if exp.ForbidWorkspaceAbsolutePaths {
		caps["workspace"] = true
	}
	if len(exp.RequiredFileSubstrings) > 0 || len(exp.ForbiddenFileSubstrings) > 0 || len(exp.ProtectedFiles) > 0 {
		caps["workspace"] = true
	}
	if len(caps) == 0 {
		return nil
	}
	names := make([]string, 0, len(caps))
	for cap := range caps {
		names = append(names, cap)
	}
	sort.Strings(names)
	return names
}

// ScenarioExpectationCapabilityNames returns the broad capability families a
// scenario is expected to exercise, using the same derivation as debug
// manifests and batch summaries.
func ScenarioExpectationCapabilityNames(s BatchScenario) []string {
	return ExpectationCapabilityNames(debugScenarioExpectations(s))
}

// ScenarioExpectationDomains returns the task-domain labels a scenario
// declares for coverage and quality-gate accounting.
func ScenarioExpectationDomains(s BatchScenario) []string {
	seen := map[string]bool{}
	for _, domain := range s.Domains {
		domain = strings.TrimSpace(domain)
		if domain != "" {
			seen[domain] = true
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for domain := range seen {
		out = append(out, domain)
	}
	sort.Strings(out)
	return out
}

func expectationRequiresDelegatedSourceEvidence(exp DebugScenarioExpectations) bool {
	return len(exp.RequiredFocusedTaskSourceCounts) > 0 || len(exp.RequiredSubagentSourceCounts) > 0
}

func expectationRequiresSkillInstall(exp DebugScenarioExpectations) bool {
	for _, req := range exp.RequiredToolArgContains {
		if req.Tool != agent.SkillToolName || req.Arg != "action" {
			continue
		}
		switch strings.TrimSpace(req.Substring) {
		case "install", "propose_url", "propose_install", "confirm_install":
			return true
		}
	}
	return false
}

func expectationRequiresResearchCheckpoint(exp DebugScenarioExpectations) bool {
	return expectationRequiresLoopDecisionKind(exp, "research_checkpoint")
}

func expectationRequiresLoopDecisionKind(exp DebugScenarioExpectations, kind string) bool {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return false
	}
	if exp.RequiredLoopDecisionKinds[kind] > 0 {
		return true
	}
	for _, req := range exp.RequiredLoopDecisionMatches {
		if req.Kind == kind {
			return true
		}
	}
	return false
}

func expectationRequiresToolOrder(exp DebugScenarioExpectations, earlier, later string) bool {
	for _, req := range exp.RequiredToolOrder {
		if req.Earlier == earlier && req.Later == later {
			return true
		}
	}
	return false
}

func ExpectationCapabilityOutcome(ok bool, names []string) string {
	if len(names) == 0 {
		return ""
	}
	if ok {
		return "passed"
	}
	return "failed"
}

func ExpectationCapabilityPassedNames(ok bool, names []string) []string {
	if len(names) == 0 || !ok {
		return nil
	}
	return append([]string(nil), names...)
}

func ExpectationCapabilityFailedNames(ok bool, names []string) []string {
	if len(names) == 0 || ok {
		return nil
	}
	return append([]string(nil), names...)
}

func expectationRequiredToolNames(exp DebugScenarioExpectations) []string {
	tools := map[string]bool{}
	add := func(tool string) {
		tool = strings.TrimSpace(tool)
		if tool != "" {
			tools[tool] = true
		}
	}
	for _, tool := range exp.RequiredTools {
		add(tool)
	}
	for tool := range exp.RequiredToolCounts {
		add(tool)
	}
	for tool := range exp.RequiredToolResultText {
		add(tool)
	}
	for _, req := range exp.RequiredSourceAccess {
		add(req.Tool)
	}
	if len(exp.RequiredSessionSearch) > 0 || len(exp.RequiredRecentSessionSearch) > 0 {
		add(agent.SessionSearchToolName)
	}
	for _, req := range exp.RequiredToolArgContains {
		add(req.Tool)
	}
	for _, req := range exp.ForbiddenToolArgContains {
		add(req.Tool)
	}
	for _, req := range exp.MaxToolArgContains {
		add(req.Tool)
	}
	for _, req := range exp.RequiredToolOrder {
		add(req.Earlier)
		add(req.Later)
	}
	for _, req := range exp.RequiredCommandBeforeTool {
		add(req.Tool)
	}
	for _, req := range exp.RequiredCommandAfterTool {
		add(req.Tool)
	}
	if len(exp.RequiredCommands) > 0 ||
		len(exp.RequiredCommandCounts) > 0 ||
		len(exp.RequiredCommandOrder) > 0 ||
		len(exp.RequiredCommandBeforeTool) > 0 ||
		len(exp.RequiredCommandAfterTool) > 0 {
		add("shell")
	}
	if len(exp.RequiredFocusedTaskCounts) > 0 || len(exp.RequiredFocusedTaskSourceCounts) > 0 {
		add(agent.FocusedTaskToolName)
	}
	if len(exp.RequiredSubagentModeCounts) > 0 || len(exp.RequiredSubagentSourceCounts) > 0 {
		add(agent.SubagentToolName)
	}
	for _, tool := range exp.RequiredTruncatedResults {
		add(tool)
	}
	for _, tool := range exp.RequiredResultArtifacts {
		add(tool)
	}
	out := make([]string, 0, len(tools))
	for tool := range tools {
		out = append(out, tool)
	}
	sort.Strings(out)
	return out
}

func addExpectationToolCapabilities(caps map[string]bool, tool string) {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return
	}
	switch {
	case tool == agent.MemoryToolName:
		caps["memory"] = true
	case tool == agent.SessionSearchToolName:
		caps["session_search"] = true
	case tool == agent.PlanToolName:
		caps["plan"] = true
	case tool == agent.SkillToolName:
		caps["skill"] = true
	case tool == agent.SubagentToolName || tool == agent.FocusedTaskToolName:
		caps["delegation"] = true
	case tool == "web_fetch" || tool == "web_search":
		caps["web"] = true
		caps["source_access"] = true
	case strings.HasPrefix(tool, "browser_"):
		caps["browser"] = true
		caps["source_access"] = true
	case tool == "mcp":
		caps["mcp"] = true
	default:
		if isExpectationWorkspaceTool(tool) {
			caps["workspace"] = true
		}
	}
}

func addExpectationStatCapabilities(caps map[string]bool, stat string) {
	switch {
	case strings.HasPrefix(stat, "memory_"):
		caps["memory"] = true
	case strings.HasPrefix(stat, "session_search_"):
		caps["session_search"] = true
	case strings.HasPrefix(stat, "source_access_"):
		caps["source_access"] = true
	case strings.Contains(stat, "focused_task") || strings.Contains(stat, "subagent"):
		caps["delegation"] = true
	case strings.Contains(stat, "plan"):
		caps["plan"] = true
	case strings.Contains(stat, "context_compaction"):
		caps["context_compaction"] = true
	}
}

func isExpectationWorkspaceTool(tool string) bool {
	switch tool {
	case "shell", "read_file", "file_context", "write_file", "edit_file", "list_files", "symbol_context", "repo_search":
		return true
	default:
		return false
	}
}

type DebugToolArgContainsRequirement struct {
	Tool      string `json:"tool"`
	Arg       string `json:"arg"`
	Substring string `json:"substring"`
	Min       int    `json:"min,omitempty"`
	Max       int    `json:"max,omitempty"`
}

type DebugToolOrderRequirement struct {
	Earlier string `json:"earlier,omitempty"`
	Later   string `json:"later,omitempty"`
}

type DebugCommandToolOrderRequirement struct {
	Command string `json:"command,omitempty"`
	Tool    string `json:"tool,omitempty"`
}

type DebugCommandOrderRequirement struct {
	Earlier string `json:"earlier,omitempty"`
	Later   string `json:"later,omitempty"`
}

type DebugLoopDecisionRequirement struct {
	Kind                    string `json:"kind,omitempty"`
	Decision                string `json:"decision,omitempty"`
	Trigger                 string `json:"trigger,omitempty"`
	MinTokenBudget          int    `json:"min_token_budget,omitempty"`
	MinObservedInputTokens  int    `json:"min_observed_input_tokens,omitempty"`
	MinProjectedInputTokens int    `json:"min_projected_input_tokens,omitempty"`
	MinBudgetBytes          int    `json:"min_budget_bytes,omitempty"`
	Min                     int    `json:"min,omitempty"`
}

type DebugLoopProtocolFeedRequirement struct {
	Mode                          string `json:"mode,omitempty"`
	PlanLabelContains             string `json:"plan_label_contains,omitempty"`
	PlanCurrentStepStatus         string `json:"plan_current_step_status,omitempty"`
	PlanCurrentStep               string `json:"plan_current_step,omitempty"`
	CurrentSituation              string `json:"current_situation,omitempty"`
	LastTurnEndReason             string `json:"last_turn_end_reason,omitempty"`
	MinLastTurnToolRequests       int    `json:"min_last_turn_tool_requests,omitempty"`
	MinLastTurnToolErrors         int    `json:"min_last_turn_tool_errors,omitempty"`
	MinLastTurnForcedNoTools      int    `json:"min_last_turn_forced_no_tools,omitempty"`
	MinLastTurnMemoryUpdates      int    `json:"min_last_turn_memory_updates,omitempty"`
	MinLastTurnMemorySearchCalls  int    `json:"min_last_turn_memory_search_calls,omitempty"`
	MinLastTurnMemorySearchMisses int    `json:"min_last_turn_memory_search_misses,omitempty"`
	MinLastTurnSessionSearchCalls int    `json:"min_last_turn_session_search_calls,omitempty"`
	MinLastTurnLoopGuards         int    `json:"min_last_turn_loop_guards,omitempty"`
	LastDecisionKind              string `json:"last_decision_kind,omitempty"`
	LastDecisionTrigger           string `json:"last_decision_trigger,omitempty"`
	LastDecision                  string `json:"last_decision,omitempty"`
	LastDecisionConfidence        string `json:"last_decision_confidence,omitempty"`
	LastDecisionReason            string `json:"last_decision_reason,omitempty"`
	LastDecisionAction            string `json:"last_decision_required_action,omitempty"`
	MinLastDecisionTokenBudget    int    `json:"min_last_decision_token_budget,omitempty"`
	MinLastDecisionObservedInput  int    `json:"min_last_decision_observed_input_tokens,omitempty"`
	MinLastDecisionProjectedInput int    `json:"min_last_decision_projected_input_tokens,omitempty"`
	MinLastDecisionBudgetBytes    int    `json:"min_last_decision_budget_bytes,omitempty"`
	Min                           int    `json:"min,omitempty"`
}

type DebugSourceAccessRequirement struct {
	Status               string `json:"status,omitempty"`
	Tool                 string `json:"tool,omitempty"`
	URLContains          string `json:"url_contains,omitempty"`
	RequestedURLContains string `json:"requested_url_contains,omitempty"`
	SourceMethod         string `json:"source_method,omitempty"`
	JSONPath             string `json:"json_path,omitempty"`
	Min                  int    `json:"min,omitempty"`
}

type DebugSessionSearchRequirement struct {
	QueryContains   string   `json:"query_contains,omitempty"`
	SessionID       string   `json:"session_id,omitempty"`
	SnippetContains string   `json:"snippet_contains,omitempty"`
	MatchedTerms    []string `json:"matched_terms,omitempty"`
	ContextIncluded bool     `json:"context_included,omitempty"`
	TurnIdx         int      `json:"turn_idx,omitempty"`
	Min             int      `json:"min,omitempty"`
}

type DebugRecentSessionSearchRequirement struct {
	QueryContains     string `json:"query_contains,omitempty"`
	SessionID         string `json:"session_id,omitempty"`
	UserContains      string `json:"user_contains,omitempty"`
	AssistantContains string `json:"assistant_contains,omitempty"`
	PlanContains      string `json:"plan_contains,omitempty"`
	LoopContains      string `json:"loop_contains,omitempty"`
	RecoveryContains  string `json:"recovery_contains,omitempty"`
	MessageContains   string `json:"message_contains,omitempty"`
	Min               int    `json:"min,omitempty"`
}

type DebugTranscriptRef struct {
	Kind  string `json:"kind"`
	Path  string `json:"path"`
	Bytes int64  `json:"bytes,omitempty"`
}

type DebugMetrics struct {
	TurnEndReason                   string         `json:"turn_end_reason,omitempty"`
	ToolCalls                       int            `json:"tool_calls"`
	ToolRequests                    int            `json:"tool_requests,omitempty"`
	ToolRequestsAdmitted            int            `json:"tool_requests_admitted,omitempty"`
	ToolRequestsSkipped             int            `json:"tool_requests_skipped,omitempty"`
	ToolErrors                      int            `json:"tool_errors"`
	ToolArgsRepaired                int            `json:"tool_args_repaired"`
	ToolNameCanonicalized           int            `json:"tool_name_canonicalized"`
	ToolRepairCalls                 int            `json:"tool_repair_calls,omitempty"`
	ToolRepairSucceeded             int            `json:"tool_repair_succeeded,omitempty"`
	ToolRepairFailed                int            `json:"tool_repair_failed,omitempty"`
	ToolRepairNotes                 int            `json:"tool_repair_notes,omitempty"`
	ToolRepairByKind                map[string]int `json:"tool_repair_by_kind,omitempty"`
	ToolFailureByKind               map[string]int `json:"tool_failure_by_kind,omitempty"`
	ToolUnclassifiedErrors          int            `json:"tool_unclassified_errors,omitempty"`
	FocusedTaskCalls                int            `json:"focused_task_calls,omitempty"`
	FocusedTaskByType               map[string]int `json:"focused_task_by_type,omitempty"`
	FocusedTaskSources              map[string]int `json:"focused_task_sources,omitempty"`
	FocusedTaskErrors               int            `json:"focused_task_errors,omitempty"`
	FocusedTaskIncomplete           int            `json:"focused_task_incomplete,omitempty"`
	SubagentCalls                   int            `json:"subagent_calls,omitempty"`
	SubagentByMode                  map[string]int `json:"subagent_by_mode,omitempty"`
	SubagentSources                 map[string]int `json:"subagent_sources,omitempty"`
	SubagentErrors                  int            `json:"subagent_errors,omitempty"`
	SubagentIncomplete              int            `json:"subagent_incomplete,omitempty"`
	PlanCalls                       int            `json:"plan_calls,omitempty"`
	PlanByAction                    map[string]int `json:"plan_by_action,omitempty"`
	PlanErrors                      int            `json:"plan_errors,omitempty"`
	MessageRejected                 int            `json:"message_rejected,omitempty"`
	MessageRejectedByTrigger        map[string]int `json:"message_rejected_by_trigger,omitempty"`
	LoopGuardInterventions          int            `json:"loop_guard_interventions"`
	ForcedNoTools                   int            `json:"forced_no_tools"`
	LoopTurnCheckpoints             int            `json:"loop_turn_checkpoints,omitempty"`
	LoopProtocolFeeds               int            `json:"loop_protocol_feeds,omitempty"`
	LoopProtocolFeedByMode          map[string]int `json:"loop_protocol_feed_by_mode,omitempty"`
	LatestLoopProtocolFeedNumber    int            `json:"latest_loop_protocol_feed_number,omitempty"`
	LatestLoopProtocolFeedMode      string         `json:"latest_loop_protocol_feed_mode,omitempty"`
	LoopProtocolCalibrationRequests int            `json:"loop_protocol_calibration_requests,omitempty"`
	LoopProtocolCalibrations        int            `json:"loop_protocol_calibrations,omitempty"`
	ContextInjections               int            `json:"context_injections,omitempty"`
	ContextInjectionBySource        map[string]int `json:"context_injection_by_source,omitempty"`
	ContextInjectionBytes           int            `json:"context_injection_bytes,omitempty"`
	ContextInjectionEstimatedTokens int            `json:"context_injection_estimated_tokens,omitempty"`
	SourceAccessResults             int            `json:"source_access_results"`
	SourceAccessVerified            int            `json:"source_access_verified"`
	SourceAccessDiscoveryOnly       int            `json:"source_access_discovery_only"`
	SourceAccessNetwork             int            `json:"source_access_network"`
	SourceAccessDynamicPartial      int            `json:"source_access_dynamic_partial"`
	MemoryUpdates                   int            `json:"memory_updates"`
	MemoryUpdateAdd                 int            `json:"memory_update_add,omitempty"`
	MemoryUpdateReplace             int            `json:"memory_update_replace,omitempty"`
	MemoryUpdateRemove              int            `json:"memory_update_remove,omitempty"`
	MemorySearchCalls               int            `json:"memory_search_calls,omitempty"`
	MemorySearchMisses              int            `json:"memory_search_misses,omitempty"`
	SessionSearchCalls              int            `json:"session_search_calls,omitempty"`
	SessionSearchResults            int            `json:"session_search_results,omitempty"`
	SessionSearchContextHits        int            `json:"session_search_context_hits,omitempty"`
	SessionSearchMatchedTerms       int            `json:"session_search_matched_terms,omitempty"`
	SessionSearchRecent             int            `json:"session_search_recent_sessions,omitempty"`
	ContextCompactions              int            `json:"context_compactions"`
	ReactiveContextCompactions      int            `json:"reactive_context_compactions"`
	ContextCompactionRemoved        int            `json:"context_compaction_removed_messages"`
	ContextCompactionReducedBytes   int            `json:"context_compaction_reduced_bytes,omitempty"`
	ContextCompactionSummary        int            `json:"context_compaction_summary_bytes,omitempty"`
	ContextCompactionMissing        int            `json:"context_compaction_summary_missing,omitempty"`
	ContextCompactionEmpty          int            `json:"context_compaction_summary_empty,omitempty"`
	ToolContextTruncated            int            `json:"tool_context_truncated,omitempty"`
	ToolContextOmittedBytes         int            `json:"tool_context_omitted_bytes,omitempty"`
	InputTokens                     int            `json:"input_tokens"`
	OutputTokens                    int            `json:"output_tokens"`
	TraceEvents                     int            `json:"trace_events,omitempty"`
	TraceEventTypes                 map[string]int `json:"trace_event_types,omitempty"`
	TaskStateStatus                 string         `json:"task_state_status,omitempty"`
	TaskStateVerification           string         `json:"task_state_verification,omitempty"`
	TaskStateChangedFiles           int            `json:"task_state_changed_files,omitempty"`
	TaskStateFailedActions          int            `json:"task_state_failed_actions,omitempty"`
	TaskStateEvidence               int            `json:"task_state_evidence,omitempty"`
}

type VerifierResult struct {
	Command            string
	Ran                bool
	OK                 bool
	ExitCode           int
	Duration           time.Duration
	OutputBytes        int
	OutputTruncated    bool
	OutputOmittedBytes int
	OutputCapBytes     int
}

type DebugVerifierResult struct {
	Command            string `json:"command,omitempty"`
	Ran                bool   `json:"ran,omitempty"`
	OK                 bool   `json:"ok,omitempty"`
	ExitCode           int    `json:"exit_code,omitempty"`
	DurationMS         int64  `json:"duration_ms,omitempty"`
	OutputBytes        int    `json:"output_bytes,omitempty"`
	OutputTruncated    bool   `json:"output_truncated,omitempty"`
	OutputOmittedBytes int    `json:"output_omitted_bytes,omitempty"`
	OutputCapBytes     int    `json:"output_cap_bytes,omitempty"`
}

func BuiltinBatchScenarios() []BatchScenario {
	return []BatchScenario{
		goMedianScenario(),
		goConfigPrecedenceScenario(),
		pythonSlugScenario(),
		goRedactionScenario(),
		pythonConfigParserScenario(),
		promptInjectionFactsScenario(),
		focusedTaskProjectFactsScenario(),
		subagentProjectFactsScenario(),
		subagentNoisyFactsScenario(),
		subagentNestedFactsScenario(),
		smallToolBadJSONReadScenario(),
		smallToolWrongFieldReadScenario(),
		smallToolWrongToolNameScenario(),
		defaultRuntimeSymbolContextScenario(),
		defaultRuntimeSymbolContextRuntimeCapabilitiesScenario(),
		defaultRuntimeSymbolContextThenReadFileScenario(),
		defaultRuntimeFileContextScenario(),
		defaultRuntimeRepoSearchScenario(),
		skillToolReadScenario(),
		skillRemoteInstallGuardScenario(),
		skillReviewedInstallActivationScenario(),
		planCodingRepairScenario(),
		planNotForSimpleReadScenario(),
		planResumeCurrentStepScenario(),
		memoryCrossSessionRecallScenario(),
		sessionHistoryRecallScenario(),
		longRunMemorySessionJoinScenario(),
		longRunMultiTaskSessionRecoveryScenario(),
		longRunRecentSessionAnchorRecoveryScenario(),
		longRunLoopMemoryAnchorRecoveryScenario(),
		longRunCrashMissingToolResultResumeScenario(),
		longRunCrashDuplicateToolResultResumeScenario(),
		longRunContextCompactionRetentionScenario(),
		longRunRequestInputPressureCompactionScenario(),
		longRunInputBudgetPressureScenario(),
		longRunLoopSetupNormalTextScenario(),
		longRunLoopActivationCalibrationScenario(),
		longRunLoopActivationCompletedDraftScenario(),
		longRunResearchCheckpointScenario(),
		memoryConfirmedWriteStatsScenario(),
		memoryAutonomousDurableRuleScenario(),
		memoryIgnoreTransientTaskStatusScenario(),
		smallToolRepeatedReadScenario(),
		smallToolEditRecoveryScenario(),
		smallToolShellFailureScenario(),
		smallToolWorkspaceRelativeShellScenario(),
		oversizedToolResultScenario(),
		longRunStockAnalysisScenario(),
		longRunBittensorSubnetScenario(),
		longRunCodePRScenario(),
		longRunCodeCommitPushScenario(),
		longRunCodeCloneCommitPushScenario(),
		longRunCodeSourceRepoCommitPushScenario(),
		longRunScratchProjectLoopPushScenario(),
		longRunLoopFinalClosureGuardScenario(),
		longRunActivePlanFinalClosureGuardScenario(),
		longRunPlanLoopFinalClosureGuardScenario(),
		longRunScratchProjectIterativeLoopPushScenario(),
		longRunIntegratedMemoryRecoveryScenario(),
		longRunFocusedTaskRecoveryScenario(),
		liveWebSkillURLInstallActivationScenario(),
		liveWebResearchCheckpointEvidenceScenario(),
		liveWebResearchCheckpointDelegatedEvidenceScenario(),
		liveWebTaostatsDynamicEvidenceScenario(),
		liveWebTaostatsWebFetchRecoveryScenario(),
		liveWebTaostatsScrollNetworkRecoveryScenario(),
		liveWebTaostatsNetworkSearchReadScenario(),
	}
}

func BatchScenarioNames() []string {
	scenarios := BuiltinBatchScenarios()
	names := make([]string, 0, len(scenarios))
	for _, s := range scenarios {
		names = append(names, s.Name)
	}
	sort.Strings(names)
	return names
}

func BatchSuiteNames() []string {
	seen := map[string]bool{}
	for _, s := range BuiltinBatchScenarios() {
		for _, suite := range s.Suites {
			if strings.TrimSpace(suite) != "" {
				seen[suite] = true
			}
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func SelectBatchScenarios(names []string) ([]BatchScenario, error) {
	return SelectBatchScenariosForSuite("", names)
}

func SelectBatchScenariosForSuite(suite string, names []string) ([]BatchScenario, error) {
	all := BuiltinBatchScenarios()
	if suite != "" {
		filtered := all[:0]
		for _, s := range all {
			if scenarioInSuite(s, suite) {
				filtered = append(filtered, s)
			}
		}
		all = filtered
		if len(all) == 0 {
			return nil, fmt.Errorf("unknown suite %q (valid: %s)", suite, strings.Join(BatchSuiteNames(), ", "))
		}
	}
	if len(names) == 0 {
		return all, nil
	}
	byName := map[string]BatchScenario{}
	for _, s := range all {
		byName[s.Name] = s
	}
	var selected []BatchScenario
	for _, name := range names {
		s, ok := byName[name]
		if !ok {
			valid := make([]string, 0, len(all))
			for _, s := range all {
				valid = append(valid, s.Name)
			}
			sort.Strings(valid)
			return nil, fmt.Errorf("unknown scenario %q (valid: %s)", name, strings.Join(valid, ", "))
		}
		selected = append(selected, s)
	}
	return selected, nil
}

func scenarioInSuite(s BatchScenario, suite string) bool {
	for _, candidate := range s.Suites {
		if candidate == suite {
			return true
		}
	}
	return false
}

func (r BatchRunner) Run(ctx context.Context, scenario BatchScenario) BatchResult {
	start := time.Now()
	res := BatchResult{BatchScenario: scenario.Name, TraceDeltas: r.TraceDeltas}
	if r.Timeout <= 0 {
		r.Timeout = DefaultBatchTimeout
	}
	if scenario.MaxTurns <= 0 {
		scenario.MaxTurns = DefaultBatchMaxTurnSteps
	}
	if len(batchScenarioPrompts(scenario)) > 1 && strings.TrimSpace(scenario.SessionID) == "" {
		return res.fail("multi-turn batch scenario %q requires SessionID", scenario.Name)
	}
	expectations := debugScenarioExpectations(scenario)
	res.Expectations = &expectations
	if strings.TrimSpace(r.RepoRoot) == "" {
		r.RepoRoot = "."
	}
	repoRoot, err := filepath.Abs(r.RepoRoot)
	if err != nil {
		return res.fail("resolve repo root: %v", err)
	}
	workRoot := r.WorkRoot
	if strings.TrimSpace(workRoot) == "" {
		workRoot = filepath.Join(os.TempDir(), "affent-eval")
	}
	if err := os.MkdirAll(workRoot, 0o755); err != nil {
		return res.fail("create work root: %v", err)
	}
	workspace, err := os.MkdirTemp(workRoot, scenario.Name+"-*")
	if err != nil {
		return res.fail("create scenario workspace: %v", err)
	}
	res.Workspace = workspace
	if err := writeScenarioFiles(workspace, scenario.Files); err != nil {
		return res.fail("write scenario files: %v", err)
	}
	if err := verifyScenarioLoopProtocolState(workspace, scenario); err != nil {
		return res.fail("%v", err)
	}
	for _, command := range scenario.SetupCommands {
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}
		setupCtx, setupCancel := context.WithTimeout(ctx, DefaultSetupCommandTimeout)
		setup := r.runVerifier(setupCtx, workspace, repoRoot, command)
		setupCancel()
		if setup.Err != nil {
			return res.fail("setup command failed: %s: %v\n%s", command, setup.Err, trimOneLine(setup.Output, 1200))
		}
	}
	if err := r.prepareScenarioSourceRepo(ctx, workspace, repoRoot, scenario); err != nil {
		return res.fail("%v", err)
	}
	protected, err := readProtectedFiles(workspace, scenario.ProtectedFiles)
	if err != nil {
		return res.fail("snapshot protected files: %v", err)
	}
	tracePath := filepath.Join(workspace, "trace.jsonl")
	res.TracePath = tracePath
	runCtx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	stdout, stderr, exitCode, command, err := r.runAffentctl(runCtx, repoRoot, workspace, tracePath, scenario)
	res.AffentctlCommand = command
	res.FinalText = strings.TrimSpace(stdout)
	res.RunExitCode = exitCode
	if err != nil {
		res.Failures = append(res.Failures, fmt.Sprintf("affentctl run failed: exit=%d err=%v stderr=%s", exitCode, err, trimOneLine(stderr, 800)))
	}
	if err := verifyProtectedFiles(workspace, protected, scenario); err != nil {
		res.Failures = append(res.Failures, err.Error())
	}
	if err := verifyRequiredLoopProtocolFinalStatus(workspace, scenario); err != nil {
		res.Failures = append(res.Failures, err.Error())
	}
	if err := verifyRequiredFileSubstrings(workspace, scenario.RequiredFileSubstrings); err != nil {
		res.Failures = append(res.Failures, err.Error())
	}
	if err := verifyForbiddenFileSubstrings(workspace, scenario.ForbiddenFileSubstrings); err != nil {
		res.Failures = append(res.Failures, err.Error())
	}
	if scenario.VerifyCommand != "" {
		verifierCtx := runCtx
		var verifierCancel context.CancelFunc
		if scenario.VerifierTimeout > 0 {
			verifierCtx, verifierCancel = context.WithTimeout(runCtx, scenario.VerifierTimeout)
		}
		verifier := r.runVerifier(verifierCtx, workspace, repoRoot, scenario.VerifyCommand)
		if verifierCancel != nil {
			verifierCancel()
		}
		res.Verifier = verifier.Result
		if verifier.Err != nil {
			res.Failures = append(res.Failures, fmt.Sprintf("verify command failed: %s: %v\n%s", scenario.VerifyCommand, verifier.Err, trimOneLine(verifier.Output, 1200)))
		}
	}
	var parsedTrace *Trace
	trace, err := ParseTraceFile(tracePath)
	if err != nil {
		res.Failures = append(res.Failures, fmt.Sprintf("parse trace: %v", err))
	} else {
		parsedTrace = &trace
		trace.WorkspaceDir = workspace
		trace.ChildTranscripts = collectDebugChildTranscripts(workspace, maxDebugChildTranscriptRefs)
		populateBatchResultFromTrace(&res, trace)
		res.Failures = append(res.Failures, CheckBatchTrace(trace, scenario)...)
	}
	if scenario.ExpectedSkill != "" {
		if err := checkConversationSkill(workspace, scenario.ExpectedSkill); err != nil {
			res.Failures = append(res.Failures, err.Error())
		}
	}
	mergeRuntimeDiagnosticsFromFailures(&res, 2)
	res.Duration = time.Since(start)
	res.OK = len(res.Failures) == 0
	if err := writeScenarioDebugArtifacts(&res, scenario, stdout, stderr, parsedTrace); err != nil {
		res.Failures = append(res.Failures, fmt.Sprintf("write debug manifest: %v", err))
		res.OK = false
	}
	r.cleanupPassingWorkspace(&res, workspace)
	return res
}

func writeScenarioDebugArtifacts(res *BatchResult, scenario BatchScenario, stdout, stderr string, trace *Trace) error {
	if res == nil || strings.TrimSpace(res.Workspace) == "" {
		return nil
	}
	expectations := debugScenarioExpectations(scenario)
	res.Expectations = &expectations
	if trace != nil && len(res.TraceEventTypes) == 0 {
		res.TraceEventTypes = cloneStringIntMap(trace.RawTypes)
		res.TraceEvents = sumStringIntMap(trace.RawTypes)
	}
	if trace != nil && len(res.ToolRepairExamples) == 0 {
		res.ToolRepairExamples = trace.ToolRepairExamples(maxDebugToolRepairExamples)
	}
	if trace != nil && len(res.LoopGuardExamples) == 0 {
		res.LoopGuardExamples = trace.LoopGuardExamples(maxDebugLoopGuardExamples)
	}
	if trace != nil && len(res.MemoryUpdateExamples) == 0 {
		res.MemoryUpdateExamples = trace.MemoryUpdateExamples(maxDebugMemoryUpdateExamples)
	}
	if trace != nil && len(res.MemorySearchMissExamples) == 0 {
		res.MemorySearchMissExamples = memorySearchMissExamplesForDebug(*trace)
	}
	if trace != nil && len(res.SourceAccessExamples) == 0 {
		res.SourceAccessExamples = sourceAccessExamplesForDebug(*trace)
	}
	if trace != nil && len(res.BrowserScrollExamples) == 0 {
		res.BrowserScrollExamples = browserScrollExamplesForDebug(*trace)
	}
	if trace != nil && len(res.BrowserNetworkExamples) == 0 {
		res.BrowserNetworkExamples = browserNetworkExamplesForDebug(*trace)
	}
	if trace != nil && len(res.SessionSearchExamples) == 0 {
		res.SessionSearchExamples = trace.SessionSearchExamples(maxDebugSessionSearchExamples)
	}
	if trace != nil && len(res.PlanExamples) == 0 {
		res.PlanExamples = trace.PlanExamples(maxDebugPlanExamples)
	}
	if trace != nil && len(res.ToolTruncationExamples) == 0 {
		res.ToolTruncationExamples = trace.ToolTruncationExamples(maxDebugToolTruncationExamples)
	}
	if trace != nil && res.ContextInjections.Count == 0 {
		res.ContextInjections = trace.ContextInjectionStats(2)
	}
	if trace != nil && res.ContextCompactions.Count == 0 {
		res.ContextCompactions = trace.ContextCompactionStats(2)
	}
	if trace != nil {
		if strings.TrimSpace(res.TraceWorkspace) == "" {
			res.TraceWorkspace = strings.TrimSpace(trace.WorkspaceDir)
		}
		if strings.TrimSpace(res.ChildTranscriptRoot) == "" {
			res.ChildTranscriptRoot = strings.TrimSpace(trace.ChildTranscriptRootDir)
		}
	}
	if len(res.ChildTranscripts) == 0 {
		res.ChildTranscripts = collectDebugChildTranscripts(res.Workspace, maxDebugChildTranscriptRefs)
	}
	finalTextPath := filepath.Join(res.Workspace, "affenteval-final.txt")
	if err := os.WriteFile(finalTextPath, []byte(res.FinalText), 0o644); err != nil {
		return err
	}
	res.FinalTextPath = finalTextPath
	stdoutPath := filepath.Join(res.Workspace, "affenteval-stdout.txt")
	if err := os.WriteFile(stdoutPath, []byte(stdout), 0o644); err != nil {
		return err
	}
	res.StdoutPath = stdoutPath
	stderrPath := filepath.Join(res.Workspace, "affenteval-stderr.txt")
	if err := os.WriteFile(stderrPath, []byte(stderr), 0o644); err != nil {
		return err
	}
	res.StderrPath = stderrPath
	manifestPath := filepath.Join(res.Workspace, "affenteval-debug.json")
	timelinePath := filepath.Join(res.Workspace, "affenteval-timeline.md")
	res.DebugManifestPath = manifestPath
	res.TimelinePath = timelinePath
	if err := os.WriteFile(timelinePath, []byte(renderDebugTimeline(*res, scenario, trace)), 0o644); err != nil {
		return err
	}

	expectationCapabilities := ExpectationCapabilityNames(expectations)
	manifest := DebugManifest{
		SchemaVersion:                          1,
		Scenario:                               res.BatchScenario,
		OK:                                     res.OK,
		Workspace:                              res.Workspace,
		TraceWorkspace:                         traceWorkspaceForManifest(*res),
		ChildTranscriptRoot:                    childTranscriptRootForManifest(*res),
		TracePath:                              res.TracePath,
		TimelinePath:                           timelinePath,
		FinalTextPath:                          finalTextPath,
		StdoutPath:                             stdoutPath,
		StderrPath:                             stderrPath,
		AffentctlCommand:                       append([]string(nil), res.AffentctlCommand...),
		RunExitCode:                            res.RunExitCode,
		ConversationDir:                        filepath.Join(res.Workspace, ".affentctl"),
		ArtifactDir:                            filepath.Join(res.Workspace, ".affent", "artifacts"),
		TraceDeltas:                            res.TraceDeltas,
		Prompt:                                 batchScenarioPromptDisplay(scenario),
		Prompts:                                append([]string(nil), scenario.Prompts...),
		Expectations:                           expectations,
		ExpectationCapabilityNames:             expectationCapabilities,
		ExpectationCapabilityOutcome:           ExpectationCapabilityOutcome(res.OK, expectationCapabilities),
		ExpectationCapabilityPassedNames:       ExpectationCapabilityPassedNames(res.OK, expectationCapabilities),
		ExpectationCapabilityFailedNames:       ExpectationCapabilityFailedNames(res.OK, expectationCapabilities),
		Failures:                               append([]string(nil), res.Failures...),
		Verifier:                               debugVerifierResult(res.Verifier),
		DebugBrief:                             BuildDebugBrief(*res),
		RecoveryGuide:                          BuildDebugRecoveryGuide(*res),
		ToolRepairExamples:                     append([]ToolRepairExample(nil), res.ToolRepairExamples...),
		ConversationRepairExamples:             append([]sse.ConversationRepairedPayload(nil), res.ConversationRepairs...),
		MessageRejectedExamples:                append([]MessageRejected(nil), res.MessageRejectedStats.Examples...),
		LoopGuardExamples:                      append([]LoopGuardExample(nil), res.LoopGuardExamples...),
		LoopTurnCheckpointExamples:             append([]LoopTurnCheckpoint(nil), res.LoopTurnCheckpoints.Examples...),
		LoopProtocolFeedExamples:               append([]LoopProtocolFeed(nil), res.LoopProtocolFeeds.Examples...),
		LoopProtocolCalibrationRequestExamples: append([]LoopProtocolCalibration(nil), res.LoopProtocolCalibrationRequests.Examples...),
		LoopProtocolCalibrationExamples:        append([]LoopProtocolCalibration(nil), res.LoopProtocolCalibrations.Examples...),
		ContextInjectionExamples:               append([]ContextInjection(nil), res.ContextInjections.Examples...),
		SourceAccessExamples:                   append([]SourceAccessExample(nil), res.SourceAccessExamples...),
		BrowserScrollExamples:                  append([]BrowserScrollExample(nil), res.BrowserScrollExamples...),
		BrowserNetworkExamples:                 append([]BrowserNetworkSearchExample(nil), res.BrowserNetworkExamples...),
		MemoryUpdateExamples:                   append([]MemoryUpdateExample(nil), res.MemoryUpdateExamples...),
		MemorySearchMissExamples:               cloneMemorySearchMissExamples(res.MemorySearchMissExamples),
		SessionSearchExamples:                  append([]SessionSearchExample(nil), res.SessionSearchExamples...),
		PlanExamples:                           append([]PlanExample(nil), res.PlanExamples...),
		ToolTruncationExamples:                 append([]ToolTruncationExample(nil), res.ToolTruncationExamples...),
		ContextCompactionExamples:              append([]ContextCompaction(nil), res.ContextCompactions.Examples...),
		ChildTranscripts:                       append([]DebugTranscriptRef(nil), res.ChildTranscripts...),
		RuntimeSurface:                         cloneRuntimeSurface(res.RuntimeSurface),
		TaskState:                              CloneTaskStateSnapshotPtr(res.TaskState),
		Metrics: DebugMetrics{
			TurnEndReason:                   res.TurnEndReason,
			ToolCalls:                       res.ToolCalls,
			ToolRequests:                    res.ToolStats.ToolRequests,
			ToolRequestsAdmitted:            res.ToolStats.ToolRequestsAdmitted,
			ToolRequestsSkipped:             res.ToolStats.ToolRequestsSkipped,
			ToolErrors:                      res.ToolStats.ToolErrors,
			ToolArgsRepaired:                res.ToolStats.ToolArgsRepaired,
			ToolNameCanonicalized:           res.ToolStats.ToolNameCanonicalized,
			ToolRepairCalls:                 res.Repair.Calls,
			ToolRepairSucceeded:             res.Repair.SucceededCalls,
			ToolRepairFailed:                res.Repair.FailedCalls,
			ToolRepairNotes:                 res.Repair.Notes,
			ToolRepairByKind:                cloneStringIntMap(res.Repair.ByKind),
			ToolFailureByKind:               cloneStringIntMap(res.ToolStats.ToolFailureByKind),
			ToolUnclassifiedErrors:          res.ToolStats.ToolUnclassifiedErrors,
			FocusedTaskCalls:                res.Delegation.FocusedTaskCalls,
			FocusedTaskByType:               cloneStringIntMap(res.Delegation.FocusedTaskByType),
			FocusedTaskSources:              cloneStringIntMap(res.Delegation.FocusedTaskSourceFindingsByType),
			FocusedTaskErrors:               res.Delegation.FocusedTaskErrors,
			FocusedTaskIncomplete:           res.Delegation.FocusedTaskIncomplete,
			SubagentCalls:                   res.Delegation.SubagentCalls,
			SubagentByMode:                  cloneStringIntMap(res.Delegation.SubagentByMode),
			SubagentSources:                 cloneStringIntMap(res.Delegation.SubagentSourceEvidenceByMode),
			SubagentErrors:                  res.Delegation.SubagentErrors,
			SubagentIncomplete:              res.Delegation.SubagentIncomplete,
			PlanCalls:                       res.Plan.Calls,
			PlanByAction:                    cloneStringIntMap(res.Plan.ByAction),
			PlanErrors:                      res.Plan.Errors,
			MessageRejected:                 res.MessageRejectedStats.Count,
			MessageRejectedByTrigger:        cloneStringIntMap(res.MessageRejectedStats.ByTrigger),
			LoopGuardInterventions:          res.ToolStats.LoopGuardInterventions,
			ForcedNoTools:                   res.ToolStats.ForcedNoTools,
			LoopTurnCheckpoints:             res.LoopTurnCheckpoints.Count,
			LoopProtocolFeeds:               res.LoopProtocolFeeds.Count,
			LoopProtocolFeedByMode:          cloneStringIntMap(res.LoopProtocolFeeds.ByMode),
			LatestLoopProtocolFeedNumber:    res.LoopProtocolFeeds.Latest.FeedNumber,
			LatestLoopProtocolFeedMode:      res.LoopProtocolFeeds.Latest.Mode,
			LoopProtocolCalibrationRequests: res.LoopProtocolCalibrationRequests.Count,
			LoopProtocolCalibrations:        res.LoopProtocolCalibrations.Count,
			ContextInjections:               res.ContextInjections.Count,
			ContextInjectionBySource:        cloneStringIntMap(res.ContextInjections.BySource),
			ContextInjectionBytes:           res.ContextInjections.Bytes,
			ContextInjectionEstimatedTokens: res.ContextInjections.EstimatedTokens,
			SourceAccessResults:             res.ToolStats.SourceAccessResults,
			SourceAccessVerified:            res.ToolStats.SourceAccessVerified,
			SourceAccessDiscoveryOnly:       res.ToolStats.SourceAccessDiscoveryOnly,
			SourceAccessNetwork:             res.ToolStats.SourceAccessNetwork,
			SourceAccessDynamicPartial:      res.ToolStats.SourceAccessDynamicPartial,
			MemoryUpdates:                   res.ToolStats.MemoryUpdates,
			MemoryUpdateAdd:                 res.ToolStats.MemoryUpdateAdd,
			MemoryUpdateReplace:             res.ToolStats.MemoryUpdateReplace,
			MemoryUpdateRemove:              res.ToolStats.MemoryUpdateRemove,
			MemorySearchCalls:               res.ToolStats.MemorySearchCalls,
			MemorySearchMisses:              res.ToolStats.MemorySearchMisses,
			SessionSearchCalls:              res.ToolStats.SessionSearchCalls,
			SessionSearchResults:            res.ToolStats.SessionSearchResults,
			SessionSearchContextHits:        res.ToolStats.SessionSearchContextHits,
			SessionSearchMatchedTerms:       res.ToolStats.SessionSearchMatchedTerms,
			SessionSearchRecent:             res.ToolStats.SessionSearchRecent,
			ContextCompactions:              res.ContextCompactions.Count,
			ReactiveContextCompactions:      res.ContextCompactions.Reactive,
			ContextCompactionRemoved:        res.ContextCompactions.RemovedMessages,
			ContextCompactionReducedBytes:   res.ContextCompactions.ReducedBytes,
			ContextCompactionSummary:        res.ContextCompactions.SummaryBytes,
			ContextCompactionMissing:        res.ContextCompactions.SummaryMissing,
			ContextCompactionEmpty:          res.ContextCompactions.SummaryEmpty,
			ToolContextTruncated:            res.ToolStats.ToolContextTruncated,
			ToolContextOmittedBytes:         res.ToolStats.ToolContextOmittedBytes,
			InputTokens:                     res.Usage.InputTokens,
			OutputTokens:                    res.Usage.OutputTokens,
			TraceEvents:                     res.TraceEvents,
			TraceEventTypes:                 cloneStringIntMap(res.TraceEventTypes),
			TaskStateStatus:                 res.TaskState.Status,
			TaskStateVerification:           res.TaskState.VerificationState,
			TaskStateChangedFiles:           len(res.TaskState.ChangedFiles),
			TaskStateFailedActions:          len(res.TaskState.FailedActions),
			TaskStateEvidence:               len(res.TaskState.Evidence),
		},
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(manifestPath, raw, 0o644); err != nil {
		return err
	}
	return nil
}

func traceWorkspaceForManifest(res BatchResult) string {
	workspace := strings.TrimSpace(res.TraceWorkspace)
	if workspace == "" || sameCleanPath(workspace, res.Workspace) {
		return ""
	}
	return workspace
}

func childTranscriptRootForManifest(res BatchResult) string {
	if len(res.ChildTranscripts) == 0 {
		return ""
	}
	root := strings.TrimSpace(res.ChildTranscriptRoot)
	if root == "" || sameCleanPath(root, res.Workspace) {
		return ""
	}
	return root
}

func sameCleanPath(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func populateBatchResultFromTrace(res *BatchResult, trace Trace) {
	if res == nil {
		return
	}
	if strings.TrimSpace(res.FinalText) == "" {
		res.FinalText = strings.TrimSpace(trace.FinalText)
	}
	res.TraceSchemaVersion = trace.SchemaVersion
	res.TraceEventTypes = cloneStringIntMap(trace.RawTypes)
	res.TraceEvents = sumStringIntMap(trace.RawTypes)
	res.TurnEndReason = trace.TurnEndReason
	res.ToolCalls = len(trace.Tools)
	res.ToolStats = trace.ToolStats
	res.ToolStats.ToolFailureByKind = trace.ToolFailureKindCounts()
	res.ToolStats.ToolUnclassifiedErrors = trace.UnclassifiedToolErrorCount()
	res.RuntimeErrorByKind = trace.LoopErrorKindCounts()
	res.RuntimeErrorExamples = trace.RuntimeErrorExamples(2)
	res.ConversationRepairs = append([]sse.ConversationRepairedPayload(nil), trace.ConversationRepairs...)
	res.LoopDecisionStats = trace.LoopDecisionStats(2)
	res.MessageRejectedStats = trace.MessageRejectedStats(2)
	res.LoopProtocolFeeds = trace.LoopProtocolFeedStats(2)
	res.LoopProtocolCalibrationRequests = trace.LoopProtocolCalibrationRequestStats(2)
	res.LoopProtocolCalibrations = trace.LoopProtocolCalibrationStats(2)
	res.LoopProtocolSetupOverrun = trace.LoopProtocolSetupOverrunStats(3)
	res.LoopTurnCheckpoints = trace.LoopTurnCheckpointStats(2)
	res.ContextInjections = trace.ContextInjectionStats(2)
	res.ContextCompactions = trace.ContextCompactionStats(2)
	res.ToolRepairExamples = trace.ToolRepairExamples(maxDebugToolRepairExamples)
	res.ToolFailureExamples = trace.ToolFailureExamples(2)
	res.LoopGuardExamples = trace.LoopGuardExamples(maxDebugLoopGuardExamples)
	res.SourceAccessExamples = sourceAccessExamplesForDebug(trace)
	res.BrowserScrollExamples = browserScrollExamplesForDebug(trace)
	res.BrowserNetworkExamples = browserNetworkExamplesForDebug(trace)
	res.MemoryUpdateExamples = trace.MemoryUpdateExamples(maxDebugMemoryUpdateExamples)
	res.MemorySearchMissExamples = memorySearchMissExamplesForDebug(trace)
	res.SessionSearchExamples = trace.SessionSearchExamples(maxDebugSessionSearchExamples)
	res.PlanExamples = trace.PlanExamples(maxDebugPlanExamples)
	res.ToolTruncationExamples = trace.ToolTruncationExamples(maxDebugToolTruncationExamples)
	res.ToolTruncation = SummarizeToolTruncation(trace)
	res.WorkspacePath = TraceWorkspaceAbsolutePathStats(trace, 3)
	res.Usage = trace.Usage
	res.Delegation = trace.DelegationStats()
	res.Plan = trace.PlanStats()
	res.Repair = trace.RepairStats()
	res.RuntimeSurface = latestRuntimeSurface(trace.RuntimeSurfaces)
	res.TaskState = trace.TaskState
	res.ChildTranscripts = append([]DebugTranscriptRef(nil), trace.ChildTranscripts...)
	res.TraceWorkspace = strings.TrimSpace(trace.WorkspaceDir)
	res.ChildTranscriptRoot = strings.TrimSpace(trace.ChildTranscriptRootDir)
}

func sourceAccessExamplesForDebug(trace Trace) []SourceAccessExample {
	return trace.SourceAccessExamples(len(trace.Tools))
}

func browserScrollExamplesForDebug(trace Trace) []BrowserScrollExample {
	return trace.BrowserScrollExamples(len(trace.Tools))
}

func browserNetworkExamplesForDebug(trace Trace) []BrowserNetworkSearchExample {
	return trace.BrowserNetworkSearchExamples(len(trace.Tools))
}

func memorySearchMissExamplesForDebug(trace Trace) []MemorySearchMissExample {
	return trace.MemorySearchMissExamples(len(trace.Tools))
}

func BuildDebugRecoveryGuide(res BatchResult) *DebugRecoveryGuide {
	if strings.TrimSpace(res.Workspace) == "" && len(res.AffentctlCommand) == 0 && len(res.Failures) == 0 {
		return nil
	}
	brief := BuildDebugBrief(res)
	if res.OK && res.RunExitCode == 0 && (brief == nil || len(brief.Items) == 0) {
		return nil
	}
	guide := &DebugRecoveryGuide{
		Summary:               debugRecoverySummary(res),
		Inspect:               debugRecoveryInspect(res, brief),
		ExactRerunCommand:     append([]string(nil), res.AffentctlCommand...),
		FullTraceRerunCommand: debugRecoveryFullTraceCommand(res),
		ContinuePrompt:        debugRecoveryContinuePrompt(res, brief),
	}
	if guide.Summary == "" && len(guide.Inspect) == 0 && len(guide.ExactRerunCommand) == 0 && len(guide.FullTraceRerunCommand) == 0 && guide.ContinuePrompt == "" {
		return nil
	}
	return guide
}

func debugVerifierResult(v VerifierResult) *DebugVerifierResult {
	if !v.Ran && strings.TrimSpace(v.Command) == "" {
		return nil
	}
	return &DebugVerifierResult{
		Command:            v.Command,
		Ran:                v.Ran,
		OK:                 v.OK,
		ExitCode:           v.ExitCode,
		DurationMS:         v.Duration.Milliseconds(),
		OutputBytes:        v.OutputBytes,
		OutputTruncated:    v.OutputTruncated,
		OutputOmittedBytes: v.OutputOmittedBytes,
		OutputCapBytes:     v.OutputCapBytes,
	}
}

func debugRecoverySummary(res BatchResult) string {
	if res.OK {
		return "scenario passed; keep the debug artifacts as baseline evidence before changing related runtime behavior"
	}
	if res.TurnEndReason != "" && res.TurnEndReason != "completed" {
		return fmt.Sprintf("scenario failed after turn ended with %q; inspect timeline, debug brief, and trace before rerunning", res.TurnEndReason)
	}
	return "scenario failed; inspect the ordered artifacts below before trusting final text or rerunning"
}

func debugRecoveryInspect(res BatchResult, brief *DebugBrief) []string {
	var inspect []string
	addPath := func(path string) {
		path = strings.TrimSpace(path)
		if path != "" && !containsString(inspect, path) {
			inspect = append(inspect, path)
		}
	}
	addLabel := func(label string, include bool) {
		label = strings.TrimSpace(label)
		if include && label != "" && !containsString(inspect, label) {
			inspect = append(inspect, label)
		}
	}
	addPath(res.TimelinePath)
	addPath(res.DebugManifestPath)
	addPath(res.TracePath)
	addPath(res.FinalTextPath)
	if !res.OK || res.RunExitCode != 0 {
		addPath(res.StderrPath)
		addPath(res.StdoutPath)
	}
	addLabel(filepath.Join(res.Workspace, ".affent", "artifacts"), res.ToolTruncation.ResultArtifacts > 0 || res.ToolTruncation.ContextArtifacts > 0)
	addLabel(filepath.Join(res.Workspace, ".affentctl"), len(res.ChildTranscripts) > 0 || res.Delegation.HasAny())
	if brief != nil {
		for _, item := range brief.Items {
			for _, target := range item.Inspect {
				addLabel(target, true)
			}
		}
	}
	return inspect
}

func debugRecoveryFullTraceCommand(res BatchResult) []string {
	if len(res.AffentctlCommand) == 0 || res.TraceDeltas {
		return nil
	}
	out := make([]string, 0, len(res.AffentctlCommand))
	for _, arg := range res.AffentctlCommand {
		if arg == "--trace-skip-deltas" {
			continue
		}
		out = append(out, arg)
	}
	if len(out) == len(res.AffentctlCommand) {
		return nil
	}
	return out
}

func debugRecoveryContinuePrompt(res BatchResult, brief *DebugBrief) string {
	var parts []string
	if res.OK {
		parts = append(parts, "Use this passing eval as baseline evidence.")
	} else {
		parts = append(parts, "Investigate this Affent eval failure using the retained debug artifacts before changing code.")
	}
	if res.TimelinePath != "" {
		parts = append(parts, "Start from "+res.TimelinePath+".")
	}
	if res.DebugManifestPath != "" {
		parts = append(parts, "Use "+res.DebugManifestPath+" for structured failures, debug tags, examples, and rerun commands.")
	}
	if len(res.Failures) > 0 {
		if preview := debugRecoveryFailurePreview(res.Failures); preview != "" {
			parts = append(parts, "First failure: "+preview+".")
		}
		parts = append(parts, "Explain which explicit expectation failed and make the smallest runtime change that improves the real long-run scenario.")
	}
	if tags := debugRecoveryPriorityTags(brief); len(tags) > 0 {
		parts = append(parts, "Priority debug tags: "+strings.Join(tags, ", ")+".")
		if action := debugRecoveryPriorityAction(tags); action != "" {
			parts = append(parts, action)
		}
	}
	return strings.Join(parts, " ")
}

func debugRecoveryFailurePreview(failures []string) string {
	for _, failure := range failures {
		failure = textutil.CompactWhitespace(strings.TrimSpace(failure))
		if failure != "" {
			return textutil.Preview(failure, 240)
		}
	}
	return ""
}

func debugRecoveryPriorityAction(tags []string) string {
	var actions []string
	add := func(action string) {
		if action != "" && len(actions) < 4 {
			actions = append(actions, action)
		}
	}
	if containsString(tags, "tool_repair:failed") {
		add("For tool_repair:failed, inspect tool_repair_examples and the tool timeline before rerunning; decide whether the fix belongs in tool aliasing, argument repair, or model guidance.")
	}
	if containsString(tags, "verifier:failed") {
		add("For verifier:failed, inspect the Verifier section, failures, and retained workspace diff, then rerun the exact verifier command in the workspace before changing runtime behavior.")
	}
	if containsString(tags, "verifier:not_run") {
		add("For verifier:not_run, inspect runtime exit state and setup commands before trusting the code-task outcome; the scenario did not reach its configured verification step.")
	}
	if containsString(tags, "verifier:abnormal") {
		add("For verifier:abnormal, inspect verifier timeout/cancel symptoms and the command itself before treating the implementation as semantically wrong.")
	}
	if containsString(tags, "verifier:output_truncated") {
		add("For verifier:output_truncated, rerun the verifier in the retained workspace or raise --verifier-output-cap before inferring the exact failing assertion from the bounded preview.")
	}
	if containsString(tags, "durable_completion") {
		add("For durable_completion, inspect final_text, runtime_surface, message_rejected_examples, loop_turn_checkpoint_examples, and plan_calls; fix the completion guard or durable close/update path before trusting the final answer.")
	}
	if containsString(tags, "loop_protocol:fixture") {
		add("For loop_protocol:fixture, fix the per-session .affent/loops/<session_id>/LOOP.md fixture and state.json lifecycle status before rerunning; this is scenario setup, not model behavior.")
	}
	if containsString(tags, "loop_protocol:calibration_backlog") {
		add("For loop_protocol:calibration_backlog, inspect calibration request/answer examples and trace events before continuing; the setup loop is spending budget without closing the protocol handshake.")
	}
	if containsString(tags, "loop_protocol:setup_tool_overrun") {
		add("For loop_protocol:setup_tool_overrun, inspect tool_timeline and loop protocol calibration events; the runtime should stop tool work after draft setup until a calibration question is recorded.")
	}
	if containsString(tags, "tool_failure:loop_protocol_activation_status") || containsString(tags, "tool_failure:loop_protocol_activation_invalid") {
		add("For loop protocol activation failures, inspect tool_failure_examples, saved LOOP.md status, and calibration events; recover through patch_draft for compact draft fixes and complete_activation for the draft-to-running transition.")
	}
	if containsString(tags, "tool_failure:unclassified") {
		add("For tool_failure:unclassified, inspect tool_timeline and trace_events; add structured Failure: kind metadata at the runtime/tool boundary before tuning model behavior or quality thresholds.")
	}
	if containsString(tags, "memory_update:missing") {
		add("For memory_update:missing, inspect memory tool calls and result metadata; either fix the autonomous write decision or make the scenario expectation explicit about why no durable update should occur.")
	}
	if containsString(tags, "memory_update:available_unused") {
		add("For memory_update:available_unused, inspect runtime_surface and tool_timeline; memory was available but never called, so diagnose recall/write decision triggers before changing storage semantics.")
	}
	if containsString(tags, "recall:session_search_available_unused") {
		add("For recall:session_search_available_unused, inspect runtime_surface, tool_timeline, and loop checkpoints; session_search was available but never called, so diagnose resume/recovery decision triggers before changing session storage.")
	}
	if containsString(tags, "memory_update:absent_longrun") {
		add("For memory_update:absent_longrun, inspect tool_timeline, loop checkpoints, and stable verified decisions; add memory only for durable future-use facts, not transient task state.")
	}
	if containsString(tags, "tool_budget:turn_overrun") {
		add("For tool_budget:turn_overrun, inspect runtime_surface and loop_turn_checkpoint_examples; align runtime MaxToolCalls/MaxTurnSteps before trusting long-run token cost.")
	}
	if containsString(tags, "research_checkpoint:no_external_evidence") {
		add("For research_checkpoint:no_external_evidence, inspect loop_decision_examples and verify whether source_evidence or child_transcripts are available; if not, treat conclusions as internal review rather than externally calibrated route changes.")
	}
	if containsString(tags, "loop_guard:forced_no_tools") {
		add("For loop_guard:forced_no_tools, inspect loop_guard_examples and the previous successful evidence before retrying tools; change the tool sequence or finish with a marked gap instead of repeating the blocked call.")
	}
	if containsString(tags, "tool_failure:loop_guard_call_cap") || containsString(tags, "plan_error") {
		add("For plan/tool-call-cap failures, inspect plan_examples and loop_guard_examples; continue from the current persisted plan instead of rewriting it or spending more planning calls.")
	}
	if containsString(tags, "tool_failure:invalid_args") {
		add("For tool_failure:invalid_args, inspect tool_failure_examples and the exact tool schema; fix malformed arguments or schema guidance before treating the task result as model reasoning failure.")
	}
	if containsString(tags, "tool_failure:command_failed") {
		add("For tool_failure:command_failed, inspect tool_failure_examples and the exact shell command, cwd, exit code, and output before changing prompts or retrying the same command.")
	}
	if containsString(tags, "tool_failure:tool_failed") {
		add("For tool_failure:tool_failed, inspect tool_failure_examples and add a more specific structured Failure/Next classification at the tool boundary if recovery remains ambiguous.")
	}
	if containsString(tags, "tool_failure:loop_guard_no_budget") {
		add("For tool_failure:loop_guard_no_budget, inspect the requested-but-unrun tools and rerun the scenario with a smaller tool sequence or a higher turn budget before trusting the final synthesis.")
	}
	if containsString(tags, "tool_failure:blocked") {
		add("For tool_failure:blocked, switch to a canonical source, rendered browser path, API/text endpoint, or explicitly mark the source gap instead of retrying the blocked URL.")
	}
	if containsString(tags, "delegation_error") {
		add("For delegation_error, inspect child_transcripts and delegation examples; decide whether the parent should use direct evidence, a narrower child task, or a larger child budget.")
	}
	if containsString(tags, "browser_network:unread_refs") {
		add("For browser_network:unread_refs, inspect browser_network_examples and source_evidence, then call browser_network_read on the listed ref before citing hidden dynamic values; browser_network refs/checks are not citable SourceAccess evidence.")
	}
	if containsString(tags, "context_compaction:summary_missing") || containsString(tags, "context_compaction:summary_empty") {
		add("For context_compaction summary gaps, inspect context_compaction_examples and recover from persisted LOOP.md, plan state, session_search, memory, or authoritative files before trusting compressed context.")
	}
	if containsString(tags, "empty_recall:recent_sessions") {
		add("For empty_recall:recent_sessions, inspect session_search_examples and retry from recent_sessions plan, loop, or recovery anchors before saying prior history is missing.")
	}
	if containsString(tags, "recall:no_context") ||
		containsString(tags, "recall:no_matched_terms") ||
		containsString(tags, "recall:weak_context") ||
		containsString(tags, "recall:weak_matched_terms") {
		add("For degraded session recall, inspect session_search_examples and rerun with narrower identifiers, adjacent context, plan anchors, or loop anchors before trusting stale transcript hits.")
	}
	if containsString(tags, "recall:memory_no_topic_anchors") {
		add("For recall:memory_no_topic_anchors, inspect memory_search_miss_examples; retry with target/topic discovery or confirm the memory bucket is empty before treating durable recall as absent.")
	}
	return strings.Join(actions, " ")
}

func debugRecoveryPriorityTags(brief *DebugBrief) []string {
	if brief == nil || len(brief.Tags) == 0 {
		return nil
	}
	priority := []string{
		"outcome:failed",
		"turn_end:max_turns",
		"turn_end:error",
		"runtime_error",
		"conversation_repair",
		"tool_repair:failed",
		"verifier:failed",
		"verifier:not_run",
		"verifier:abnormal",
		"verifier:output_truncated",
		"durable_completion",
		"loop_protocol:fixture",
		"loop_protocol:calibration_backlog",
		"loop_protocol:setup_tool_overrun",
		"tool_failure:loop_protocol_activation_status",
		"tool_failure:loop_protocol_activation_invalid",
		"tool_failure:unclassified",
		"memory_update:missing",
		"memory_update:available_unused",
		"recall:session_search_available_unused",
		"memory_update:absent_longrun",
		"tool_budget:turn_overrun",
		"research_checkpoint:no_external_evidence",
		"loop_guard:forced_no_tools",
		"tool_failure:loop_guard_call_cap",
		"tool_failure:loop_guard_no_budget",
		"tool_failure:invalid_args",
		"tool_failure:command_failed",
		"tool_failure:tool_failed",
		"tool_failure:blocked",
		"plan_error",
		"delegation_error",
		"source_dynamic_without_network",
		"source_dynamic_without_decision",
		"browser_network:unread_refs",
		"browser_scroll:stuck_without_network",
		"source_network:missing_response_diagnostics",
		"source_network:partial_read",
		"context_compaction:summary_missing",
		"context_compaction:summary_empty",
		"truncation:missing_artifact",
		"empty_recall:recent_sessions",
		"recall:no_context",
		"recall:no_matched_terms",
		"recall:weak_context",
		"recall:weak_matched_terms",
		"recall:memory_no_topic_anchors",
	}
	seen := map[string]bool{}
	out := make([]string, 0, 6)
	for _, tag := range priority {
		if containsString(brief.Tags, tag) {
			out = append(out, tag)
			seen[tag] = true
			if len(out) >= 6 {
				return out
			}
		}
	}
	for _, tag := range brief.Tags {
		if tag == "" || seen[tag] {
			continue
		}
		out = append(out, tag)
		if len(out) >= 6 {
			return out
		}
	}
	return out
}

func debugScenarioExpectations(s BatchScenario) DebugScenarioExpectations {
	reqArgs := make([]DebugToolArgContainsRequirement, 0, len(s.RequiredToolArgContains))
	for _, req := range s.RequiredToolArgContains {
		reqArgs = append(reqArgs, DebugToolArgContainsRequirement{
			Tool:      req.Tool,
			Arg:       req.Arg,
			Substring: req.Substring,
			Min:       req.Min,
			Max:       req.Max,
		})
	}
	forbiddenArgs := make([]DebugToolArgContainsRequirement, 0, len(s.ForbiddenToolArgContains))
	for _, req := range s.ForbiddenToolArgContains {
		forbiddenArgs = append(forbiddenArgs, DebugToolArgContainsRequirement{
			Tool:      req.Tool,
			Arg:       req.Arg,
			Substring: req.Substring,
			Min:       req.Min,
			Max:       req.Max,
		})
	}
	maxArgs := make([]DebugToolArgContainsRequirement, 0, len(s.MaxToolArgContains))
	for _, req := range s.MaxToolArgContains {
		maxArgs = append(maxArgs, DebugToolArgContainsRequirement{
			Tool:      req.Tool,
			Arg:       req.Arg,
			Substring: req.Substring,
			Min:       req.Min,
			Max:       req.Max,
		})
	}
	sourceReqs := make([]DebugSourceAccessRequirement, 0, len(s.RequiredSourceAccess))
	for _, req := range s.RequiredSourceAccess {
		sourceReqs = append(sourceReqs, DebugSourceAccessRequirement{
			Status:               req.Status,
			Tool:                 req.Tool,
			URLContains:          req.URLContains,
			RequestedURLContains: req.RequestedURLContains,
			SourceMethod:         req.SourceMethod,
			JSONPath:             req.JSONPath,
			Min:                  req.Min,
		})
	}
	sessionSearchReqs := make([]DebugSessionSearchRequirement, 0, len(s.RequiredSessionSearch))
	for _, req := range s.RequiredSessionSearch {
		sessionSearchReqs = append(sessionSearchReqs, DebugSessionSearchRequirement{
			QueryContains:   req.QueryContains,
			SessionID:       req.SessionID,
			SnippetContains: req.SnippetContains,
			MatchedTerms:    append([]string(nil), req.MatchedTerms...),
			ContextIncluded: req.ContextIncluded,
			TurnIdx:         req.TurnIdx,
			Min:             req.Min,
		})
	}
	recentSessionSearchReqs := make([]DebugRecentSessionSearchRequirement, 0, len(s.RequiredRecentSessionSearch))
	for _, req := range s.RequiredRecentSessionSearch {
		recentSessionSearchReqs = append(recentSessionSearchReqs, DebugRecentSessionSearchRequirement{
			QueryContains:     req.QueryContains,
			SessionID:         req.SessionID,
			UserContains:      req.UserContains,
			AssistantContains: req.AssistantContains,
			PlanContains:      req.PlanContains,
			LoopContains:      req.LoopContains,
			RecoveryContains:  req.RecoveryContains,
			MessageContains:   req.MessageContains,
			Min:               req.Min,
		})
	}
	loopReqs := make([]DebugLoopDecisionRequirement, 0, len(s.RequiredLoopDecisionMatches))
	for _, req := range s.RequiredLoopDecisionMatches {
		loopReqs = append(loopReqs, DebugLoopDecisionRequirement{
			Kind:                    req.Kind,
			Decision:                req.Decision,
			Trigger:                 req.Trigger,
			MinTokenBudget:          req.MinTokenBudget,
			MinObservedInputTokens:  req.MinObservedInputTokens,
			MinProjectedInputTokens: req.MinProjectedInputTokens,
			MinBudgetBytes:          req.MinBudgetBytes,
			Min:                     req.Min,
		})
	}
	loopFeedReqs := make([]DebugLoopProtocolFeedRequirement, 0, len(s.RequiredLoopProtocolFeedMatches))
	for _, req := range s.RequiredLoopProtocolFeedMatches {
		loopFeedReqs = append(loopFeedReqs, DebugLoopProtocolFeedRequirement{
			Mode:                          req.Mode,
			PlanLabelContains:             req.PlanLabelContains,
			PlanCurrentStepStatus:         req.PlanCurrentStepStatus,
			PlanCurrentStep:               req.PlanCurrentStep,
			CurrentSituation:              req.CurrentSituation,
			LastTurnEndReason:             req.LastTurnEndReason,
			MinLastTurnToolRequests:       req.MinLastTurnToolRequests,
			MinLastTurnToolErrors:         req.MinLastTurnToolErrors,
			MinLastTurnForcedNoTools:      req.MinLastTurnForcedNoTools,
			MinLastTurnMemoryUpdates:      req.MinLastTurnMemoryUpdates,
			MinLastTurnMemorySearchCalls:  req.MinLastTurnMemorySearchCalls,
			MinLastTurnMemorySearchMisses: req.MinLastTurnMemorySearchMisses,
			MinLastTurnSessionSearchCalls: req.MinLastTurnSessionSearchCalls,
			MinLastTurnLoopGuards:         req.MinLastTurnLoopGuards,
			LastDecisionKind:              req.LastDecisionKind,
			LastDecisionTrigger:           req.LastDecisionTrigger,
			LastDecision:                  req.LastDecision,
			LastDecisionConfidence:        req.LastDecisionConfidence,
			LastDecisionReason:            req.LastDecisionReason,
			LastDecisionAction:            req.LastDecisionAction,
			MinLastDecisionTokenBudget:    req.MinLastDecisionTokenBudget,
			MinLastDecisionObservedInput:  req.MinLastDecisionObservedInput,
			MinLastDecisionProjectedInput: req.MinLastDecisionProjectedInput,
			MinLastDecisionBudgetBytes:    req.MinLastDecisionBudgetBytes,
			Min:                           req.Min,
		})
	}
	toolOrders := make([]DebugToolOrderRequirement, 0, len(s.RequiredToolOrder))
	for _, req := range s.RequiredToolOrder {
		toolOrders = append(toolOrders, DebugToolOrderRequirement{
			Earlier: req.Earlier,
			Later:   req.Later,
		})
	}
	commandBeforeTool := make([]DebugCommandToolOrderRequirement, 0, len(s.RequiredCommandBeforeTool))
	for _, req := range s.RequiredCommandBeforeTool {
		commandBeforeTool = append(commandBeforeTool, DebugCommandToolOrderRequirement{
			Command: req.Command,
			Tool:    req.Tool,
		})
	}
	commandAfterTool := make([]DebugCommandToolOrderRequirement, 0, len(s.RequiredCommandAfterTool))
	for _, req := range s.RequiredCommandAfterTool {
		commandAfterTool = append(commandAfterTool, DebugCommandToolOrderRequirement{
			Command: req.Command,
			Tool:    req.Tool,
		})
	}
	commandOrders := make([]DebugCommandOrderRequirement, 0, len(s.RequiredCommandOrder))
	for _, req := range s.RequiredCommandOrder {
		commandOrders = append(commandOrders, DebugCommandOrderRequirement{
			Earlier: req.Earlier,
			Later:   req.Later,
		})
	}
	checks := BatchScenarioChecks(s)
	checkNames := make([]string, 0, len(checks))
	for _, check := range checks {
		if strings.TrimSpace(check.Name) != "" {
			checkNames = append(checkNames, check.Name)
		}
	}
	return DebugScenarioExpectations{
		CheckNames:                                     checkNames,
		Suites:                                         append([]string(nil), s.Suites...),
		Domains:                                        append([]string(nil), s.Domains...),
		SessionID:                                      strings.TrimSpace(s.SessionID),
		ExecutePlan:                                    s.ExecutePlan,
		EnableMemory:                                   s.EnableMemory,
		EnableLoopProtocol:                             s.EnableLoopProtocol,
		ExposeLoopProtocolTool:                         s.ExposeLoopProtocolTool,
		RequiredTurnEndReason:                          strings.TrimSpace(s.RequiredTurnEndReason),
		VerifyCommand:                                  strings.TrimSpace(s.VerifyCommand),
		SetupCommands:                                  compactNonEmptyStrings(s.SetupCommands),
		SourceRepoURL:                                  strings.TrimSpace(s.SourceRepoURL),
		SourceRepoRef:                                  strings.TrimSpace(s.SourceRepoRef),
		SourceRepoDir:                                  strings.TrimSpace(s.SourceRepoDir),
		ExpectedSkill:                                  strings.TrimSpace(s.ExpectedSkill),
		RequiredTools:                                  append([]string(nil), s.RequiredTools...),
		ForbiddenTools:                                 append([]string(nil), s.ForbiddenTools...),
		RequiredCommands:                               append([]string(nil), s.RequiredCommands...),
		ForbiddenCommands:                              append([]string(nil), s.ForbiddenCommands...),
		ForbidWorkspaceAbsolutePaths:                   s.ForbidWorkspaceAbsolutePaths,
		RequiredCommandCounts:                          cloneStringIntMap(s.RequiredCommandCounts),
		RequiredToolCounts:                             cloneStringIntMap(s.RequiredToolCounts),
		RequiredToolFailureKindCounts:                  cloneStringIntMap(s.RequiredToolFailureKindCounts),
		MaxToolFailureKindCounts:                       cloneStringIntMap(s.MaxToolFailureKindCounts),
		RequiredToolStatsAtLeast:                       cloneStringIntMap(s.RequiredToolStatsAtLeast),
		RequiredTraceEventCounts:                       cloneStringIntMap(s.RequiredTraceEventCounts),
		RequiredUserMessageModes:                       cloneStringIntMap(s.RequiredUserMessageModes),
		ForbiddenUserMessageModes:                      append([]string(nil), s.ForbiddenUserMessageModes...),
		RequiredTaskStateRequestMode:                   strings.TrimSpace(s.RequiredTaskStateRequestMode),
		RequiredTaskStateRequestSource:                 strings.TrimSpace(s.RequiredTaskStateRequestSource),
		RequiredTaskStateScheduleKind:                  strings.TrimSpace(s.RequiredTaskStateScheduleKind),
		RequiredConversationRepairStatsAtLeast:         cloneStringIntMap(s.RequiredConversationRepairStatsAtLeast),
		RequiredConversationRepairKinds:                cloneStringIntMap(s.RequiredConversationRepairKinds),
		RequiredLoopDecisionKinds:                      cloneStringIntMap(s.RequiredLoopDecisionKinds),
		RequiredLoopDecisionResults:                    cloneStringIntMap(s.RequiredLoopDecisionResults),
		RequiredLoopDecisionMatches:                    loopReqs,
		RequiredMessageRejected:                        cloneStringIntMap(s.RequiredMessageRejected),
		RequiredCompletionGuards:                       append([]string(nil), s.RequiredCompletionGuards...),
		RequiredLoopProtocolFeeds:                      s.RequiredLoopProtocolFeeds,
		RequiredLoopProtocolCalibrationRequests:        s.RequiredLoopProtocolCalibrationRequests,
		RequiredLoopProtocolCalibrations:               s.RequiredLoopProtocolCalibrations,
		RequiredLoopProtocolCalibrationRequestStatuses: cloneStringIntMap(s.RequiredLoopProtocolCalibrationRequestStatuses),
		RequiredLoopProtocolCalibrationStatuses:        cloneStringIntMap(s.RequiredLoopProtocolCalibrationStatuses),
		RequiredLoopProtocolFeedModes:                  cloneStringIntMap(s.RequiredLoopProtocolFeedModes),
		RequiredLoopProtocolFeedMatches:                loopFeedReqs,
		RequireLoopProtocolFullAfterCompact:            s.RequireLoopProtocolFullAfterCompact,
		RequiredLoopProtocolFinalStatus:                strings.TrimSpace(s.RequiredLoopProtocolFinalStatus),
		RequiredToolResultText:                         cloneStringSliceMap(s.RequiredToolResultText),
		RequiredToolArgContains:                        reqArgs,
		ForbiddenToolArgContains:                       forbiddenArgs,
		MaxToolArgContains:                             maxArgs,
		RequiredSourceAccess:                           sourceReqs,
		RequiredSessionSearch:                          sessionSearchReqs,
		RequiredRecentSessionSearch:                    recentSessionSearchReqs,
		RequiredContextInjectionSources:                cloneStringIntMap(s.RequiredContextInjectionSources),
		RequiredContextInjectionText:                   cloneStringSliceMap(s.RequiredContextInjectionText),
		RequiredCommandBeforeTool:                      commandBeforeTool,
		RequiredCommandAfterTool:                       commandAfterTool,
		RequiredCommandOrder:                           commandOrders,
		RequiredToolOrder:                              toolOrders,
		RequiredFocusedTaskCounts:                      cloneStringIntMap(s.RequiredFocusedTaskCounts),
		RequiredFocusedTaskSourceCounts:                cloneStringIntMap(s.RequiredFocusedTaskSourceCounts),
		RequiredSubagentModeCounts:                     cloneStringIntMap(s.RequiredSubagentModeCounts),
		RequiredSubagentSourceCounts:                   cloneStringIntMap(s.RequiredSubagentSourceCounts),
		RequireNoDelegationErrors:                      s.RequireNoDelegationErrors,
		RequireNoPlanErrors:                            s.RequireNoPlanErrors,
		RequireFinalPlanCompleted:                      s.RequireFinalPlanCompleted,
		RequiredFinalText:                              append([]string(nil), s.RequiredFinalText...),
		ForbiddenFinalText:                             append([]string(nil), s.ForbiddenFinalText...),
		RequiredTruncatedResults:                       append([]string(nil), s.RequiredTruncatedResults...),
		RequiredResultArtifacts:                        append([]string(nil), s.RequiredResultArtifacts...),
		RequiredContextCompactions:                     s.RequiredContextCompactions,
		RequiredReactiveCompactions:                    s.RequiredReactiveCompactions,
		RequiredContextCompactionReasons:               cloneStringIntMap(s.RequiredContextCompactionReasons),
		RequiredCompactionRemovedMsgs:                  s.RequiredCompactionRemovedMsgs,
		RequiredCompactionReducedBytes:                 s.RequiredCompactionReducedBytes,
		RequiredContextSummaryText:                     append([]string(nil), s.RequiredContextSummaryText...),
		RequiredContextLoopProtocolAnchorText:          append([]string(nil), s.RequiredContextLoopProtocolAnchorText...),
		ProtectedFiles:                                 append([]string(nil), s.ProtectedFiles...),
		RequiredFileSubstrings:                         cloneStringSliceMap(s.RequiredFileSubstrings),
		ForbiddenFileSubstrings:                        cloneStringSliceMap(s.ForbiddenFileSubstrings),
		MaxParentToolCalls:                             s.MaxParentToolCalls,
		MaxSuccessfulToolCallsByTool:                   cloneStringIntMap(s.MaxSuccessfulToolCallsByTool),
		RuntimeMaxTurnInputTokens:                      s.RuntimeMaxTurnInputTokens,
		MaxLoopTurnInputTokens:                         s.MaxLoopTurnInputTokens,
		MaxLoopTurnTotalTokens:                         s.MaxLoopTurnTotalTokens,
		MaxTurns:                                       s.MaxTurns,
		CompactTrigger:                                 s.CompactTrigger,
		CompactTriggerInputTokens:                      s.CompactTriggerInputTokens,
		CompactKeepLast:                                s.CompactKeepLast,
	}
}

func collectDebugChildTranscripts(workspace string, maxRefs int) []DebugTranscriptRef {
	return collectDebugChildTranscriptsFromRoots(workspace, maxRefs, []debugChildTranscriptRoot{
		{kind: "focused_task", path: filepath.Join(workspace, ".affentctl", "focused-tasks")},
		{kind: "subagent", path: filepath.Join(workspace, ".affentctl", "subagents")},
	})
}

func collectDurableSessionChildTranscripts(sessionDir string, maxRefs int) []DebugTranscriptRef {
	return collectDebugChildTranscriptsFromRoots(sessionDir, maxRefs, []debugChildTranscriptRoot{
		{kind: "focused_task", path: filepath.Join(sessionDir, "focused-tasks")},
		{kind: "subagent", path: filepath.Join(sessionDir, "subagents")},
	})
}

type debugChildTranscriptRoot struct {
	kind string
	path string
}

func collectDebugChildTranscriptsFromRoots(rootDir string, maxRefs int, roots []debugChildTranscriptRoot) []DebugTranscriptRef {
	if strings.TrimSpace(rootDir) == "" || maxRefs <= 0 {
		return nil
	}
	var refs []DebugTranscriptRef
	for _, root := range roots {
		_ = filepath.WalkDir(root.path, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d == nil {
				return nil
			}
			if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			rel, err := filepath.Rel(rootDir, path)
			if err != nil {
				rel = path
			}
			refs = append(refs, DebugTranscriptRef{
				Kind:  root.kind,
				Path:  filepath.ToSlash(rel),
				Bytes: info.Size(),
			})
			return nil
		})
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Kind != refs[j].Kind {
			return refs[i].Kind < refs[j].Kind
		}
		return refs[i].Path < refs[j].Path
	})
	if len(refs) > maxRefs {
		return refs[:maxRefs]
	}
	return refs
}

func latestRuntimeSurface(surfaces []sse.RuntimeSurfacePayload) *sse.RuntimeSurfacePayload {
	if len(surfaces) == 0 {
		return nil
	}
	return cloneRuntimeSurface(&surfaces[len(surfaces)-1])
}

func cloneRuntimeSurface(surface *sse.RuntimeSurfacePayload) *sse.RuntimeSurfacePayload {
	if surface == nil {
		return nil
	}
	out := *surface
	out.Tools = append([]sse.RuntimeSurfaceTool(nil), surface.Tools...)
	out.ToolCallCaps = append([]sse.RuntimeToolCallCap(nil), surface.ToolCallCaps...)
	out.CompletionGuards = append([]string(nil), surface.CompletionGuards...)
	return &out
}

func mergeRuntimeDiagnosticsFromFailures(res *BatchResult, maxExamplesPerKind int) {
	if res == nil {
		return
	}
	counts, examples := RuntimeErrorDiagnosticsFromFailures(res.Failures, maxExamplesPerKind)
	for kind, count := range counts {
		if count <= 0 {
			continue
		}
		if res.RuntimeErrorByKind == nil {
			res.RuntimeErrorByKind = map[string]int{}
		}
		if res.RuntimeErrorByKind[kind] == 0 {
			res.RuntimeErrorByKind[kind] = count
		}
	}
	for kind, newExamples := range examples {
		if len(newExamples) == 0 {
			continue
		}
		if res.RuntimeErrorExamples == nil {
			res.RuntimeErrorExamples = map[string][]RuntimeErrorExample{}
		}
		if len(res.RuntimeErrorExamples[kind]) == 0 {
			res.RuntimeErrorExamples[kind] = append([]RuntimeErrorExample(nil), newExamples...)
		}
	}
}

func (r BatchRunner) cleanupPassingWorkspace(res *BatchResult, workspace string) {
	if res == nil || !res.OK || !r.CleanupPassingWorkspaces {
		return
	}
	if err := os.RemoveAll(workspace); err != nil {
		res.CleanupError = err.Error()
		return
	}
	res.WorkspaceRemoved = true
}

func (r BatchResult) fail(format string, args ...any) BatchResult {
	r.Failures = append(r.Failures, fmt.Sprintf(format, args...))
	r.OK = false
	return r
}

func (r BatchRunner) runAffentctl(ctx context.Context, repoRoot, workspace, tracePath string, scenario BatchScenario) (string, string, int, []string, error) {
	if strings.TrimSpace(r.BaseURL) == "" {
		r.BaseURL = os.Getenv("AFFENTCTL_BASE_URL")
	}
	if strings.TrimSpace(r.APIKey) == "" {
		r.APIKey = os.Getenv("AFFENTCTL_API_KEY")
	}
	if strings.TrimSpace(r.Model) == "" {
		r.Model = os.Getenv("AFFENTCTL_MODEL")
	}
	if strings.TrimSpace(r.BaseURL) == "" || strings.TrimSpace(r.Model) == "" {
		return "", "", 64, nil, errors.New("base URL and model are required (flags or AFFENTCTL_BASE_URL/AFFENTCTL_MODEL)")
	}
	goBin := r.GoBin
	if goBin == "" {
		goBin = findGo(repoRoot)
	}
	var stdout, stderr bytes.Buffer
	var redactedCommand []string
	var lastExit int
	for idx, prompt := range batchScenarioPrompts(scenario) {
		args := r.affentctlRunArgs(workspace, tracePath, scenario, prompt)
		if idx == 0 {
			redactedCommand = redactedCommandArgv(goBin, args)
		}
		cmd := exec.CommandContext(ctx, goBin, args...)
		cmd.Dir = repoRoot
		cmd.Env = evalCommandEnv(repoRoot)
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := runEvalCommand(ctx, cmd)
		lastExit = exitCodeFromError(err)
		if err != nil {
			return stdout.String(), stderr.String(), lastExit, redactedCommand, err
		}
	}
	return stdout.String(), stderr.String(), lastExit, redactedCommand, nil
}

func (r BatchRunner) affentctlRunArgs(workspace, tracePath string, scenario BatchScenario, prompt string) []string {
	executor := strings.TrimSpace(r.Executor)
	if executor == "" {
		executor = "local"
	}
	args := []string{
		"run", "./cmd/affentctl", "run",
		"--workspace", workspace,
		"--executor", executor,
		"--base-url", r.BaseURL,
		"--model", r.Model,
		"--max-turns", fmt.Sprint(scenario.MaxTurns),
		"--trace", tracePath,
		"--prompt", prompt,
	}
	if scenario.CompactTrigger > 0 {
		args = append(args, "--compact-trigger", fmt.Sprint(scenario.CompactTrigger))
	}
	if scenario.CompactTriggerInputTokens != 0 {
		args = append(args, "--compact-trigger-input-tokens", fmt.Sprint(scenario.CompactTriggerInputTokens))
	}
	if scenario.CompactKeepLast > 0 {
		args = append(args, "--compact-keep-last", fmt.Sprint(scenario.CompactKeepLast))
	}
	if scenario.RuntimeMaxTurnInputTokens > 0 {
		args = append(args, "--max-turn-input-tokens", fmt.Sprint(scenario.RuntimeMaxTurnInputTokens))
	}
	if !r.TraceDeltas {
		args = append(args, "--trace-skip-deltas")
	}
	if strings.TrimSpace(scenario.SessionID) != "" {
		args = append(args, "--session-id", strings.TrimSpace(scenario.SessionID))
	}
	if scenario.ExecutePlan {
		args = append(args, "--execute-plan")
	}
	if r.APIKey != "" {
		args = append(args, "--api-key", r.APIKey)
	}
	args = appendStringFlag(args, "--temperature", r.Temperature)
	args = appendStringFlag(args, "--top-p", r.TopP)
	args = appendStringFlag(args, "--max-tokens", r.MaxTokens)
	args = appendStringFlag(args, "--seed", r.Seed)
	runtimeEvalMode := r.RuntimeEvalMode || strings.TrimSpace(r.RuntimeTools) != "" || r.RuntimeAllTools || scenario.ExposeLoopProtocolTool
	if runtimeEvalMode {
		args = append(args, "--eval-mode")
	}
	if r.RuntimeAllTools {
		args = append(args, "--eval-all-tools")
	}
	evalTools := r.RuntimeTools
	if scenario.ExposeLoopProtocolTool && !r.RuntimeAllTools {
		evalTools = appendCSVValue(evalTools, agent.LoopProtocolToolName)
	}
	args = appendStringFlag(args, "--eval-tools", evalTools)
	if r.RuntimeMemory || scenario.EnableMemory {
		args = append(args, "--memory=true")
	}
	if scenario.EnableLoopProtocol {
		args = append(args, "--loop-protocol")
	}
	if r.RuntimeWeb {
		args = append(args, "--web=true", "--web-search=true")
	}
	if r.RuntimeBrowser {
		args = append(args, "--browser=true")
	}
	args = appendStringFlag(args, "--mcp-config", r.RuntimeMCPConfig)
	return args
}

func appendStringFlag(args []string, flagName, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return args
	}
	return append(args, flagName, value)
}

func appendCSVValue(csv, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return strings.TrimSpace(csv)
	}
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return value
	}
	for _, part := range strings.FieldsFunc(csv, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t' }) {
		if strings.TrimSpace(part) == value {
			return csv
		}
	}
	return csv + "," + value
}

func batchScenarioPrompts(s BatchScenario) []string {
	if len(s.Prompts) > 0 {
		return append([]string(nil), s.Prompts...)
	}
	return []string{s.Prompt}
}

func batchScenarioPromptDisplay(s BatchScenario) string {
	prompts := batchScenarioPrompts(s)
	if len(prompts) == 1 {
		return prompts[0]
	}
	var b strings.Builder
	for i, prompt := range prompts {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "Turn %d:\n%s", i+1, prompt)
	}
	return b.String()
}

func redactedCommandArgv(bin string, args []string) []string {
	bin = strings.TrimSpace(bin)
	if bin == "" {
		bin = "go"
	}
	out := make([]string, 0, len(args)+1)
	out = append(out, bin)
	nextReplacement := ""
	for _, arg := range args {
		if nextReplacement != "" {
			out = append(out, nextReplacement)
			nextReplacement = ""
			continue
		}
		if arg == "--api-key" {
			out = append(out, arg)
			nextReplacement = "<redacted>"
			continue
		}
		if arg == "--prompt" {
			out = append(out, arg)
			nextReplacement = "<prompt>"
			continue
		}
		if strings.HasPrefix(arg, "--api-key=") {
			out = append(out, "--api-key=<redacted>")
			continue
		}
		if strings.HasPrefix(arg, "--prompt=") {
			out = append(out, "--prompt=<prompt>")
			continue
		}
		out = append(out, arg)
	}
	return out
}

type verifierRun struct {
	Result VerifierResult
	Output string
	Err    error
}

func (r BatchRunner) runVerifier(ctx context.Context, workspace, repoRoot, command string) verifierRun {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = workspace
	cmd.Env = evalCommandEnv(repoRoot)
	out := newVerifierOutputBuffer(r.VerifierOutputCapBytes)
	cmd.Stdout = out
	cmd.Stderr = out
	start := time.Now()
	err := runEvalCommand(ctx, cmd)
	output := out.String()
	stats := out.Stats()
	result := VerifierResult{
		Command:            command,
		Ran:                true,
		OK:                 err == nil,
		ExitCode:           exitCodeFromError(err),
		Duration:           time.Since(start),
		OutputBytes:        stats.Bytes,
		OutputTruncated:    stats.Truncated,
		OutputOmittedBytes: stats.OmittedBytes,
		OutputCapBytes:     stats.CapBytes,
	}
	return verifierRun{Result: result, Output: output, Err: err}
}

func (r BatchRunner) prepareScenarioSourceRepo(ctx context.Context, workspace, repoRoot string, scenario BatchScenario) error {
	url := strings.TrimSpace(scenario.SourceRepoURL)
	if url == "" {
		return nil
	}
	targetRel, err := cleanScenarioSourceRepoDir(scenario.SourceRepoDir)
	if err != nil {
		return err
	}
	targetPath := filepath.Join(workspace, filepath.FromSlash(targetRel))
	if _, err := os.Stat(targetPath); err == nil {
		return fmt.Errorf("source repo target %s already exists", targetRel)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat source repo target %s: %w", targetRel, err)
	}
	cloneCtx, cloneCancel := context.WithTimeout(ctx, DefaultSetupCommandTimeout)
	clone := r.runGit(cloneCtx, workspace, repoRoot, "clone", "--", url, targetRel)
	cloneCancel()
	if clone.Err != nil {
		return fmt.Errorf("source repo clone failed: git clone %s %s: %v\n%s", url, targetRel, clone.Err, trimOneLine(clone.Output, 1200))
	}
	if ref := strings.TrimSpace(scenario.SourceRepoRef); ref != "" {
		checkoutCtx, checkoutCancel := context.WithTimeout(ctx, DefaultSetupCommandTimeout)
		checkout := r.runGit(checkoutCtx, targetPath, repoRoot, "checkout", ref)
		checkoutCancel()
		if checkout.Err != nil {
			return fmt.Errorf("source repo checkout failed: git -C %s checkout %s: %v\n%s", targetRel, ref, checkout.Err, trimOneLine(checkout.Output, 1200))
		}
	}
	return nil
}

func cleanScenarioSourceRepoDir(dir string) (string, error) {
	dir = strings.TrimSpace(filepath.ToSlash(dir))
	if dir == "" {
		dir = "repo"
	}
	if strings.ContainsRune(dir, 0) {
		return "", errors.New("source repo dir contains NUL")
	}
	if strings.HasPrefix(dir, "/") {
		return "", fmt.Errorf("source repo dir %q must be relative", dir)
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(dir)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("source repo dir %q must stay inside the scenario workspace", dir)
	}
	return clean, nil
}

func (r BatchRunner) runGit(ctx context.Context, dir, repoRoot string, args ...string) verifierRun {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = evalCommandEnv(repoRoot)
	out := newVerifierOutputBuffer(r.VerifierOutputCapBytes)
	cmd.Stdout = out
	cmd.Stderr = out
	start := time.Now()
	err := runEvalCommand(ctx, cmd)
	output := out.String()
	stats := out.Stats()
	command := "git"
	if len(args) > 0 {
		command += " " + strings.Join(args, " ")
	}
	result := VerifierResult{
		Command:            command,
		Ran:                true,
		OK:                 err == nil,
		ExitCode:           exitCodeFromError(err),
		Duration:           time.Since(start),
		OutputBytes:        stats.Bytes,
		OutputTruncated:    stats.Truncated,
		OutputOmittedBytes: stats.OmittedBytes,
		OutputCapBytes:     stats.CapBytes,
	}
	return verifierRun{Result: result, Output: output, Err: err}
}

func exitCodeFromError(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

type verifierOutputStats struct {
	Bytes        int
	Truncated    bool
	OmittedBytes int
	CapBytes     int
}

type verifierOutputBuffer struct {
	mu        sync.Mutex
	buf       []byte
	cap       int
	bytes     int
	omitted   int
	truncated bool
}

func newVerifierOutputBuffer(capBytes int) *verifierOutputBuffer {
	if capBytes <= 0 {
		capBytes = DefaultVerifierOutputCapBytes
	}
	return &verifierOutputBuffer{cap: capBytes}
}

func (b *verifierOutputBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.bytes += len(p)
	room := b.cap - len(b.buf)
	switch {
	case room <= 0:
		b.omitted += len(p)
		b.truncated = true
	case len(p) > room:
		b.buf = append(b.buf, p[:room]...)
		b.omitted += len(p) - room
		b.truncated = true
	default:
		b.buf = append(b.buf, p...)
	}
	return len(p), nil
}

func (b *verifierOutputBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.truncated {
		return string(b.buf)
	}
	return string(b.buf) + fmt.Sprintf("\n[... %d more bytes truncated from verifier output; %d-byte cap.]", b.omitted, b.cap)
}

func (b *verifierOutputBuffer) Stats() verifierOutputStats {
	b.mu.Lock()
	defer b.mu.Unlock()

	return verifierOutputStats{
		Bytes:        b.bytes,
		Truncated:    b.truncated,
		OmittedBytes: b.omitted,
		CapBytes:     b.cap,
	}
}

func runEvalCommand(ctx context.Context, cmd *exec.Cmd) error {
	if err := startEvalCommand(cmd); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		killEvalCommandGroup(cmd)
		<-done
		return ctx.Err()
	}
}

func writeScenarioFiles(root string, files map[string]string) error {
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func verifyScenarioLoopProtocolState(root string, scenario BatchScenario) error {
	if !scenarioRequiresActiveLoopProtocol(scenario) {
		return nil
	}
	sessionID := strings.TrimSpace(scenario.SessionID)
	if sessionID == "" {
		return fmt.Errorf("scenario %q requires loop protocol feeds but has no SessionID", scenario.Name)
	}
	name := filepath.ToSlash(filepath.Join(".affent", "loops", sessionID, "LOOP.md"))
	path := filepath.Join(root, filepath.FromSlash(name))
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("scenario %q requires loop protocol feeds but active protocol file %s is missing", scenario.Name, name)
		}
		return fmt.Errorf("stat loop protocol file %s: %w", name, err)
	}
	if info.IsDir() {
		return fmt.Errorf("scenario %q requires loop protocol feeds but active protocol path %s is a directory", scenario.Name, name)
	}
	if status := evalLoopProtocolStatusFromFile(path); status != "" && status != "running" {
		return fmt.Errorf("scenario %q requires loop protocol feeds but active protocol file %s has status %q, want running", scenario.Name, name, status)
	}
	stateStatus, found, err := evalLoopProtocolStateStatus(filepath.Join(filepath.Dir(path), "state.json"))
	if err != nil {
		return fmt.Errorf("read loop protocol state for %s: %w", name, err)
	}
	if found {
		if stateStatus != "" && stateStatus != "running" {
			return fmt.Errorf("scenario %q requires loop protocol feeds but state for %s has status %q, want running", scenario.Name, name, stateStatus)
		}
	}
	return nil
}

func evalLoopProtocolStatusFromFile(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return evalLoopProtocolStatus(string(raw))
}

func evalLoopProtocolStatus(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.ToLower(strings.TrimSpace(key)) != "status" {
			continue
		}
		return evalLoopProtocolKnownStatus(value)
	}
	return ""
}

func evalLoopProtocolStateStatus(path string) (string, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	var state struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return "", true, err
	}
	return evalLoopProtocolKnownStatus(state.Status), true, nil
}

func evalLoopProtocolKnownStatus(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "draft", "running", "paused", "stopping", "completed", "blocked", "disabled":
		return s
	default:
		return ""
	}
}

func scenarioRequiresActiveLoopProtocol(scenario BatchScenario) bool {
	return scenario.RequiredLoopProtocolFeeds > 0 ||
		len(scenario.RequiredLoopProtocolFeedModes) > 0 ||
		len(scenario.RequiredLoopProtocolFeedMatches) > 0 ||
		scenario.RequireLoopProtocolFullAfterCompact
}

func readProtectedFiles(root string, names []string) (map[string]string, error) {
	out := map[string]string{}
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return nil, err
		}
		out[name] = string(raw)
	}
	return out, nil
}

func verifyProtectedFiles(root string, protected map[string]string, scenario BatchScenario) error {
	for name, want := range protected {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return fmt.Errorf("protected file %s missing: %w", name, err)
		}
		got := string(raw)
		if got != want {
			if protectedLoopProtocolStatusOnlyChangeAllowed(name, want, got, scenario) {
				continue
			}
			return fmt.Errorf("protected file changed: %s", name)
		}
	}
	return nil
}

func verifyRequiredLoopProtocolFinalStatus(root string, scenario BatchScenario) error {
	if strings.TrimSpace(scenario.RequiredLoopProtocolFinalStatus) == "" {
		return nil
	}
	want := evalLoopProtocolKnownStatus(scenario.RequiredLoopProtocolFinalStatus)
	if want == "" {
		return fmt.Errorf("scenario %q requires unknown loop protocol final status %q", scenario.Name, scenario.RequiredLoopProtocolFinalStatus)
	}
	sessionID := strings.TrimSpace(scenario.SessionID)
	if sessionID == "" {
		return fmt.Errorf("scenario %q requires loop protocol final status %q but has no SessionID", scenario.Name, want)
	}
	name := filepath.ToSlash(filepath.Join(".affent", "loops", sessionID, "LOOP.md"))
	path := filepath.Join(root, filepath.FromSlash(name))
	got := evalLoopProtocolStatusFromFile(path)
	if got != want {
		return fmt.Errorf("scenario %q requires loop protocol final status %q but %s has status %q", scenario.Name, want, name, got)
	}
	stateStatus, found, err := evalLoopProtocolStateStatus(filepath.Join(filepath.Dir(path), "state.json"))
	if err != nil {
		return fmt.Errorf("read loop protocol state for %s: %w", name, err)
	}
	if found && stateStatus != "" && stateStatus != want {
		return fmt.Errorf("scenario %q requires loop protocol final status %q but state for %s has status %q", scenario.Name, want, name, stateStatus)
	}
	return nil
}

func protectedLoopProtocolStatusOnlyChangeAllowed(name, before, after string, scenario BatchScenario) bool {
	wantStatus := evalLoopProtocolKnownStatus(scenario.RequiredLoopProtocolFinalStatus)
	if wantStatus == "" || !protectedLoopProtocolPathMatchesScenario(name, scenario) {
		return false
	}
	beforeStatus := evalLoopProtocolStatus(before)
	afterStatus := evalLoopProtocolStatus(after)
	if beforeStatus == "" || afterStatus != wantStatus {
		return false
	}
	beforeComparable, beforeOK := normalizeLoopProtocolMetadataStatus(before, wantStatus)
	afterComparable, afterOK := normalizeLoopProtocolMetadataStatus(after, wantStatus)
	return beforeOK && afterOK && beforeComparable == afterComparable
}

func protectedLoopProtocolPathMatchesScenario(name string, scenario BatchScenario) bool {
	sessionID := strings.TrimSpace(scenario.SessionID)
	if sessionID == "" {
		return false
	}
	cleanName := filepath.ToSlash(filepath.Clean(name))
	wantName := filepath.ToSlash(filepath.Join(".affent", "loops", sessionID, "LOOP.md"))
	return cleanName == wantName
}

func normalizeLoopProtocolMetadataStatus(content, status string) (string, bool) {
	lines := strings.Split(content, "\n")
	inMetadata := false
	metadataLevel := 0
	for i, line := range lines {
		if level, title, ok := markdownHeading(line); ok {
			if inMetadata && level <= metadataLevel {
				inMetadata = false
			}
			if level == 2 && normalizeNumberedHeadingTitle(title) == "metadata" {
				inMetadata = true
				metadataLevel = level
			}
			continue
		}
		if !inMetadata {
			continue
		}
		prefix, _, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(prefix), "-"))
		if strings.EqualFold(key, "status") {
			lines[i] = prefix + ": " + status
			return strings.Join(lines, "\n"), true
		}
	}
	return content, false
}

func markdownHeading(line string) (int, string, bool) {
	trimmed := strings.TrimSpace(line)
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level == 0 || level == len(trimmed) || trimmed[level] != ' ' {
		return 0, "", false
	}
	return level, strings.TrimSpace(trimmed[level+1:]), true
}

func normalizeNumberedHeadingTitle(title string) string {
	title = strings.TrimSpace(title)
	if i := strings.Index(title, "."); i > 0 {
		number := strings.TrimSpace(title[:i])
		allDigits := true
		for _, r := range number {
			if r < '0' || r > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			title = strings.TrimSpace(title[i+1:])
		}
	}
	return strings.ToLower(title)
}

func verifyRequiredFileSubstrings(root string, required map[string][]string) error {
	for name, substrings := range required {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return fmt.Errorf("required-content file %s missing: %w", name, err)
		}
		body := string(raw)
		for _, substr := range substrings {
			if substr == "" {
				continue
			}
			if !strings.Contains(body, substr) {
				return fmt.Errorf("required content %q missing from %s", substr, name)
			}
		}
	}
	return nil
}

func verifyForbiddenFileSubstrings(root string, forbidden map[string][]string) error {
	for name, substrings := range forbidden {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return fmt.Errorf("forbidden-content file %s missing: %w", name, err)
		}
		body := string(raw)
		for _, substr := range substrings {
			if substr == "" {
				continue
			}
			if strings.Contains(body, substr) {
				return fmt.Errorf("forbidden content %q found in %s", substr, name)
			}
		}
	}
	return nil
}

// ParseTraceFile reads a JSONL trace file emitted by affentctl (or any
// SSE-event-shaped log) and returns the unified Trace the in-memory
// Runner also produces. One trace type, one check library — the
// BatchRunner path used to ship its own BatchTrace/BatchToolRequest
// twins which forced every check to be written twice.
//
// The file format is one JSON object per line with `{"type":"...",
// "data":{...}}`; new traces start with trace.meta carrying the schema
// version. Unknown event types are counted into RawTypes but otherwise
// ignored.
func ParseTraceFile(path string) (Trace, error) {
	f, err := os.Open(path)
	if err != nil {
		return Trace{}, err
	}
	defer f.Close()
	trace := Trace{RawTypes: map[string]int{}}
	pending := map[string]int{}
	r := bufio.NewReaderSize(f, 64*1024)
	lineNo := 0
	for {
		line, overLimit, err := jsonl.ReadBoundedLine(r, maxTraceLineBytes)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return trace, err
		}
		lineNo++
		if overLimit {
			return trace, fmt.Errorf("trace %s line %d exceeds max JSONL record size %d bytes", path, lineNo, maxTraceLineBytes)
		}
		var ev struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(line, &ev); err != nil {
			return trace, fmt.Errorf("trace %s line %d: %w", path, lineNo, err)
		}
		trace.RawTypes[ev.Type]++
		if _, err := applyTraceEvent(&trace, pending, ev.Type, ev.Data, ""); err != nil {
			return trace, fmt.Errorf("trace %s line %d: %w", path, lineNo, err)
		}
		appendTraceEventRef(&trace, ev.Type, ev.Data, "")
	}
	trace.TaskState = DeriveTaskState(trace)
	return trace, nil
}

type TraceDebugOptions struct {
	TracePath              string
	OutputDir              string
	Name                   string
	Prompt                 string
	Stdout                 string
	Stderr                 string
	Scenario               *BatchScenario
	WorkspaceDir           string
	ChildTranscriptRootDir string
}

// WriteTraceDebugArtifacts parses an existing affent trace/events JSONL file
// and writes the same debug manifest, timeline, and final-text artifacts that
// normal eval runs produce. This lets real long-run sessions feed the eval and
// Workbench diagnostics without rerunning the model.
func WriteTraceDebugArtifacts(opts TraceDebugOptions) (BatchResult, error) {
	tracePath := strings.TrimSpace(opts.TracePath)
	if tracePath == "" {
		return BatchResult{}, fmt.Errorf("trace path is required")
	}
	trace, err := ParseTraceFile(tracePath)
	if err != nil {
		return BatchResult{}, err
	}
	outputDir := strings.TrimSpace(opts.OutputDir)
	if outputDir == "" {
		outputDir = filepath.Join(filepath.Dir(tracePath), "affenteval-debug")
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return BatchResult{}, fmt.Errorf("create debug output dir: %w", err)
	}
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = "trace-debug"
	}
	scenario := BatchScenario{
		Name:   name,
		Prompt: opts.Prompt,
	}
	if opts.Scenario != nil {
		scenario = *opts.Scenario
		if strings.TrimSpace(name) != "" && name != "trace-debug" {
			scenario.Name = name
		}
		if strings.TrimSpace(opts.Prompt) != "" {
			scenario.Prompt = opts.Prompt
		}
		if strings.TrimSpace(scenario.Name) == "" {
			scenario.Name = name
		}
	}
	checkWorkspace := strings.TrimSpace(opts.WorkspaceDir)
	if checkWorkspace == "" {
		checkWorkspace = outputDir
	}
	trace.WorkspaceDir = checkWorkspace
	childTranscriptRoot := strings.TrimSpace(opts.ChildTranscriptRootDir)
	if childTranscriptRoot == "" {
		childTranscriptRoot = filepath.Dir(tracePath)
	}
	trace.ChildTranscriptRootDir = childTranscriptRoot
	trace.ChildTranscripts = collectDurableSessionChildTranscripts(childTranscriptRoot, maxDebugChildTranscriptRefs)
	res := BatchResult{
		BatchScenario: scenario.Name,
		Workspace:     outputDir,
		TracePath:     tracePath,
		FinalText:     strings.TrimSpace(trace.FinalText),
		RunExitCode:   0,
	}
	populateBatchResultFromTrace(&res, trace)
	res.Failures = append(res.Failures, CheckBatchTrace(trace, scenario)...)
	res.OK = len(res.Failures) == 0
	if err := writeScenarioDebugArtifacts(&res, scenario, opts.Stdout, opts.Stderr, &trace); err != nil {
		res.Failures = append(res.Failures, fmt.Sprintf("write debug manifest: %v", err))
		res.OK = false
		return res, err
	}
	return res, nil
}

func UpdateDebugManifestQualityGates(path string, passed *bool, failures []string) error {
	path = strings.TrimSpace(path)
	if path == "" || (passed == nil && len(failures) == 0) {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read debug manifest: %w", err)
	}
	var manifest DebugManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return fmt.Errorf("decode debug manifest: %w", err)
	}
	manifest.QualityGatesPassed = cloneBoolPtr(passed)
	manifest.QualityGateFailures = append([]string(nil), failures...)
	reconcileRecoveryGuideQualityGates(&manifest, passed, failures)
	updated, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode debug manifest: %w", err)
	}
	updated = append(updated, '\n')
	if err := os.WriteFile(path, updated, 0o644); err != nil {
		return fmt.Errorf("write debug manifest: %w", err)
	}
	if err := updateDebugTimelineQualityGateRecovery(manifest.TimelinePath, manifest.RecoveryGuide); err != nil {
		return err
	}
	return nil
}

func reconcileRecoveryGuideQualityGates(manifest *DebugManifest, passed *bool, failures []string) {
	if manifest == nil || passed == nil || *passed || len(failures) == 0 {
		return
	}
	if manifest.RecoveryGuide == nil {
		manifest.RecoveryGuide = &DebugRecoveryGuide{}
	}
	summary := "scenario failed quality gates; inspect quality_gate_failures before treating the run as baseline evidence"
	promptPrefix := "Quality gates failed; inspect quality_gate_failures before treating this run as baseline evidence."
	if manifest.OK {
		summary = "scenario task checks passed but quality gates failed; do not use this run as baseline evidence until gate failures are understood"
		promptPrefix = "Task checks passed, but quality gates failed; inspect quality_gate_failures before treating this run as baseline evidence."
	} else {
		summary = "scenario task checks and quality gates failed; inspect failures and quality_gate_failures before changing runtime behavior"
		promptPrefix = "Task checks and quality gates failed; inspect failures and quality_gate_failures before changing runtime behavior."
	}
	manifest.RecoveryGuide.Summary = summary
	if !containsString(manifest.RecoveryGuide.Inspect, "quality_gate_failures") {
		manifest.RecoveryGuide.Inspect = append(manifest.RecoveryGuide.Inspect, "quality_gate_failures")
	}
	current := strings.TrimSpace(manifest.RecoveryGuide.ContinuePrompt)
	switch {
	case current == "":
		manifest.RecoveryGuide.ContinuePrompt = promptPrefix
	case strings.HasPrefix(current, promptPrefix):
		manifest.RecoveryGuide.ContinuePrompt = current
	case strings.HasPrefix(current, "Use this passing eval as baseline evidence."):
		manifest.RecoveryGuide.ContinuePrompt = promptPrefix + strings.TrimPrefix(current, "Use this passing eval as baseline evidence.")
	default:
		manifest.RecoveryGuide.ContinuePrompt = promptPrefix + " " + current
	}
}

func updateDebugTimelineQualityGateRecovery(path string, guide *DebugRecoveryGuide) error {
	path = strings.TrimSpace(path)
	if path == "" || guide == nil {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read debug timeline: %w", err)
	}
	lines := strings.Split(string(raw), "\n")
	changed := false
	if strings.TrimSpace(guide.Summary) != "" {
		changed = replaceTimelineLine(lines, "- summary:", "- summary: "+timelineInline(guide.Summary, timelineErrorPreviewBytes)) || changed
	}
	if strings.TrimSpace(guide.ContinuePrompt) != "" {
		changed = replaceTimelineLine(lines, "- continue_prompt:", "- continue_prompt: "+timelineInline(guide.ContinuePrompt, timelineResultPreviewBytes)) || changed
	}
	if !changed {
		return nil
	}
	updated := strings.Join(lines, "\n")
	if !strings.HasSuffix(updated, "\n") {
		updated += "\n"
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("write debug timeline: %w", err)
	}
	return nil
}

func replaceTimelineLine(lines []string, prefix, replacement string) bool {
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			lines[i] = replacement
			return true
		}
	}
	return false
}

func cloneBoolPtr(value *bool) *bool {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

// BatchScenarioChecks returns the Check slice derived from the
// declarative fields of a BatchScenario: RequiredCommands become
// ShellCommandMatching checks, ForbiddenCommands become
// ShellCommandLacksUnguarded checks, ProtectedFiles become
// FileNotEdited checks. Lets one Check library cover both pipelines.
func BatchScenarioChecks(scenario BatchScenario) []Check {
	checks := []Check{batchScenarioTurnEndCheck(scenario)}
	for _, tool := range scenario.RequiredTools {
		checks = append(checks, ToolCalled(tool, nil))
	}
	for _, tool := range scenario.ForbiddenTools {
		checks = append(checks, ToolNotCalled(tool, nil))
	}
	for _, substr := range scenario.RequiredFinalText {
		checks = append(checks, FinalTextContainsFold(substr))
	}
	for _, substr := range scenario.ForbiddenFinalText {
		checks = append(checks, FinalTextLacks(substr))
	}
	for _, tool := range sortedStringMapKeys(scenario.RequiredToolResultText) {
		substrings := scenario.RequiredToolResultText[tool]
		for _, substr := range substrings {
			checks = append(checks, ToolResultContains(tool, substr))
		}
	}
	for _, req := range scenario.RequiredToolArgContains {
		min := req.Min
		if min <= 0 {
			min = 1
		}
		checks = append(checks, ToolArgContainsAtLeast(req.Tool, req.Arg, req.Substring, min))
	}
	for _, req := range scenario.ForbiddenToolArgContains {
		checks = append(checks, ToolArgLacksSubstring(req.Tool, req.Arg, req.Substring))
	}
	for _, req := range scenario.MaxToolArgContains {
		max := req.Max
		if max <= 0 {
			max = 1
		}
		checks = append(checks, ToolArgContainsAtMost(req.Tool, req.Arg, req.Substring, max))
	}
	for _, tool := range scenario.RequiredTruncatedResults {
		checks = append(checks, ToolResultTruncated(tool))
	}
	for _, tool := range scenario.RequiredResultArtifacts {
		checks = append(checks, ToolResultArtifact(tool))
	}
	for _, order := range scenario.RequiredToolOrder {
		checks = append(checks, ToolCalledBefore(order.Earlier, order.Later))
	}
	for _, tool := range sortedStringMapKeys(scenario.RequiredToolCounts) {
		checks = append(checks, ToolCalledAtLeast(tool, scenario.RequiredToolCounts[tool]))
	}
	for _, kind := range sortedStringMapKeys(scenario.RequiredToolFailureKindCounts) {
		checks = append(checks, ToolFailureKindAtLeast(kind, scenario.RequiredToolFailureKindCounts[kind]))
	}
	for _, kind := range sortedStringMapKeys(scenario.MaxToolFailureKindCounts) {
		checks = append(checks, ToolFailureKindAtMost(kind, scenario.MaxToolFailureKindCounts[kind]))
	}
	for _, field := range sortedStringMapKeys(scenario.RequiredToolStatsAtLeast) {
		checks = append(checks, ToolStatsAtLeast(field, scenario.RequiredToolStatsAtLeast[field]))
	}
	if min := requiredMemoryUpdateMetadataMin(scenario.RequiredToolStatsAtLeast); min > 0 {
		checks = append(checks, MemoryUpdateMetadataAtLeast(min))
	}
	for _, eventType := range sortedStringMapKeys(scenario.RequiredTraceEventCounts) {
		checks = append(checks, TraceEventCountAtLeast(eventType, scenario.RequiredTraceEventCounts[eventType]))
	}
	for _, mode := range sortedStringMapKeys(scenario.RequiredUserMessageModes) {
		checks = append(checks, UserMessageModeAtLeast(mode, scenario.RequiredUserMessageModes[mode]))
	}
	for _, mode := range scenario.ForbiddenUserMessageModes {
		if strings.TrimSpace(mode) != "" {
			checks = append(checks, UserMessageModeAtMost(mode, 0))
		}
	}
	if mode := strings.TrimSpace(scenario.RequiredTaskStateRequestMode); mode != "" {
		checks = append(checks, TaskStateRequestModeIs(mode))
	}
	if source := strings.TrimSpace(scenario.RequiredTaskStateRequestSource); source != "" {
		checks = append(checks, TaskStateRequestSourceIs(source))
	}
	if kind := strings.TrimSpace(scenario.RequiredTaskStateScheduleKind); kind != "" {
		checks = append(checks, TaskStateScheduleKindIs(kind))
	}
	for _, field := range sortedStringMapKeys(scenario.RequiredConversationRepairStatsAtLeast) {
		checks = append(checks, ConversationRepairStatsAtLeast(field, scenario.RequiredConversationRepairStatsAtLeast[field]))
	}
	for _, kind := range sortedStringMapKeys(scenario.RequiredConversationRepairKinds) {
		checks = append(checks, ConversationRepairKindAtLeast(kind, scenario.RequiredConversationRepairKinds[kind]))
	}
	for _, kind := range sortedStringMapKeys(scenario.RequiredLoopDecisionKinds) {
		checks = append(checks, LoopDecisionKindAtLeast(kind, scenario.RequiredLoopDecisionKinds[kind]))
	}
	for _, decision := range sortedStringMapKeys(scenario.RequiredLoopDecisionResults) {
		checks = append(checks, LoopDecisionResultAtLeast(decision, scenario.RequiredLoopDecisionResults[decision]))
	}
	for _, req := range scenario.RequiredLoopDecisionMatches {
		checks = append(checks, LoopDecisionRequirementAtLeast(req))
	}
	if scenario.RequiredLoopProtocolFeeds > 0 {
		checks = append(checks, LoopProtocolFeedsAtLeast(scenario.RequiredLoopProtocolFeeds))
	}
	if scenario.RequiredLoopProtocolCalibrationRequests > 0 {
		checks = append(checks, LoopProtocolCalibrationRequestsAtLeast(scenario.RequiredLoopProtocolCalibrationRequests))
	}
	if scenario.RequiredLoopProtocolCalibrations > 0 {
		checks = append(checks, LoopProtocolCalibrationsAtLeast(scenario.RequiredLoopProtocolCalibrations))
	}
	for _, status := range sortedStringMapKeys(scenario.RequiredLoopProtocolCalibrationRequestStatuses) {
		checks = append(checks, LoopProtocolCalibrationRequestStatusAtLeast(status, scenario.RequiredLoopProtocolCalibrationRequestStatuses[status]))
	}
	for _, status := range sortedStringMapKeys(scenario.RequiredLoopProtocolCalibrationStatuses) {
		checks = append(checks, LoopProtocolCalibrationStatusAtLeast(status, scenario.RequiredLoopProtocolCalibrationStatuses[status]))
	}
	for _, mode := range sortedStringMapKeys(scenario.RequiredLoopProtocolFeedModes) {
		checks = append(checks, LoopProtocolFeedModeAtLeast(mode, scenario.RequiredLoopProtocolFeedModes[mode]))
	}
	for _, req := range scenario.RequiredLoopProtocolFeedMatches {
		checks = append(checks, LoopProtocolFeedRequirementAtLeast(req))
	}
	if scenario.RequireLoopProtocolFullAfterCompact {
		checks = append(checks, LoopProtocolFullFeedAfterCompaction())
	}
	for _, req := range scenario.RequiredSourceAccess {
		min := req.Min
		if min <= 0 {
			min = 1
		}
		checks = append(checks, SourceAccessMatchWithRequestedAtLeast(req.Status, req.Tool, req.URLContains, req.RequestedURLContains, req.SourceMethod, req.JSONPath, min))
	}
	for _, req := range scenario.RequiredSessionSearch {
		min := req.Min
		if min <= 0 {
			min = 1
		}
		checks = append(checks, SessionSearchMatchAtLeast(req.QueryContains, req.SessionID, req.SnippetContains, req.MatchedTerms, req.ContextIncluded, req.TurnIdx, min))
	}
	for _, req := range scenario.RequiredRecentSessionSearch {
		min := req.Min
		if min <= 0 {
			min = 1
		}
		checks = append(checks, RecentSessionSearchAnchorAtLeast(req.QueryContains, req.SessionID, req.UserContains, req.AssistantContains, req.PlanContains, req.LoopContains, req.RecoveryContains, req.MessageContains, min))
	}
	for _, source := range sortedStringMapKeys(scenario.RequiredContextInjectionSources) {
		checks = append(checks, ContextInjectionSourceAtLeast(source, scenario.RequiredContextInjectionSources[source]))
	}
	for _, source := range sortedStringMapKeys(scenario.RequiredContextInjectionText) {
		for _, substr := range scenario.RequiredContextInjectionText[source] {
			checks = append(checks, ContextInjectionTextAtLeast(source, substr, 1))
		}
	}
	if scenario.RequiredContextCompactions > 0 {
		checks = append(checks, ContextCompactionsAtLeast(scenario.RequiredContextCompactions))
	}
	if scenario.RequiredReactiveCompactions > 0 {
		checks = append(checks, ReactiveContextCompactionsAtLeast(scenario.RequiredReactiveCompactions))
	}
	for _, reason := range sortedStringMapKeys(scenario.RequiredContextCompactionReasons) {
		checks = append(checks, ContextCompactionReasonAtLeast(reason, scenario.RequiredContextCompactionReasons[reason]))
	}
	if scenario.RequiredCompactionRemovedMsgs > 0 {
		checks = append(checks, ContextCompactionRemovedMessagesAtLeast(scenario.RequiredCompactionRemovedMsgs))
	}
	if scenario.RequiredCompactionReducedBytes > 0 {
		checks = append(checks, ContextCompactionReducedBytesAtLeast(scenario.RequiredCompactionReducedBytes))
	}
	for _, substr := range scenario.RequiredContextSummaryText {
		checks = append(checks, ContextCompactionSummaryContains(substr))
	}
	for _, substr := range scenario.RequiredContextLoopProtocolAnchorText {
		checks = append(checks, ContextCompactionLoopProtocolAnchorContains(substr))
	}
	for _, taskType := range sortedStringMapKeys(scenario.RequiredFocusedTaskCounts) {
		checks = append(checks, FocusedTaskCalledAtLeast(taskType, scenario.RequiredFocusedTaskCounts[taskType]))
	}
	for _, taskType := range sortedStringMapKeys(scenario.RequiredFocusedTaskSourceCounts) {
		checks = append(checks, FocusedTaskSourceFindingsAtLeast(taskType, scenario.RequiredFocusedTaskSourceCounts[taskType]))
	}
	for _, mode := range sortedStringMapKeys(scenario.RequiredSubagentModeCounts) {
		checks = append(checks, SubagentCalledAtLeast(mode, scenario.RequiredSubagentModeCounts[mode]))
	}
	for _, mode := range sortedStringMapKeys(scenario.RequiredSubagentSourceCounts) {
		checks = append(checks, SubagentSourceEvidenceAtLeast(mode, scenario.RequiredSubagentSourceCounts[mode]))
	}
	for _, trigger := range sortedStringMapKeys(scenario.RequiredMessageRejected) {
		checks = append(checks, MessageRejectedAtLeast(trigger, scenario.RequiredMessageRejected[trigger]))
	}
	for _, guard := range scenario.RequiredCompletionGuards {
		checks = append(checks, RuntimeSurfaceCompletionGuard(guard))
	}
	if scenario.RuntimeMaxTurnInputTokens > 0 {
		checks = append(checks, RuntimeSurfaceMaxTurnInputTokens(scenario.RuntimeMaxTurnInputTokens))
	}
	if scenario.RequireNoDelegationErrors {
		checks = append(checks, NoDelegationErrors())
	}
	if scenario.RequireNoPlanErrors {
		checks = append(checks, NoPlanErrors())
	}
	if scenario.RequireFinalPlanCompleted {
		checks = append(checks, FinalPlanCompleted())
	}
	if scenario.MaxParentToolCalls > 0 {
		checks = append(checks, MaxSuccessfulToolCalls(scenario.MaxParentToolCalls))
	}
	for _, tool := range sortedStringMapKeys(scenario.MaxSuccessfulToolCallsByTool) {
		checks = append(checks, MaxSuccessfulToolCallsForTool(tool, scenario.MaxSuccessfulToolCallsByTool[tool]))
	}
	if scenarioRequiresLoopTurnCheckpoints(scenario) && scenario.MaxLoopTurnInputTokens > 0 {
		checks = append(checks, MaxLoopTurnInputTokens(scenario.MaxLoopTurnInputTokens))
	}
	if scenarioRequiresLoopTurnCheckpoints(scenario) && scenario.MaxLoopTurnTotalTokens > 0 {
		checks = append(checks, MaxLoopTurnTotalTokens(scenario.MaxLoopTurnTotalTokens))
	}
	for _, want := range scenario.RequiredCommands {
		checks = append(checks, ShellCommandMatching(want))
	}
	for _, pattern := range sortedStringMapKeys(scenario.RequiredCommandCounts) {
		checks = append(checks, ShellCommandMatchingAtLeast(pattern, scenario.RequiredCommandCounts[pattern]))
	}
	for _, order := range scenario.RequiredCommandBeforeTool {
		checks = append(checks, ShellCommandMatchingBeforeTool(order.Command, order.Tool))
	}
	for _, order := range scenario.RequiredCommandAfterTool {
		checks = append(checks, ShellCommandMatchingAfterTool(order.Command, order.Tool))
	}
	for _, order := range scenario.RequiredCommandOrder {
		checks = append(checks, ShellCommandMatchingBeforeCommand(order.Earlier, order.Later))
	}
	for _, forbidden := range scenario.ForbiddenCommands {
		checks = append(checks, ShellCommandLacksUnguarded(forbidden))
	}
	if scenario.ForbidWorkspaceAbsolutePaths {
		checks = append(checks, ShellCommandLacksWorkspaceAbsolutePath())
	}
	if len(scenario.ProtectedFiles) > 0 {
		checks = append(checks, FileNotEdited(scenario.ProtectedFiles))
	}
	return checks
}

func scenarioRequiresLoopTurnCheckpoints(s BatchScenario) bool {
	return s.EnableLoopProtocol ||
		s.RequiredLoopProtocolFeeds > 0 ||
		s.RequiredLoopProtocolCalibrationRequests > 0 ||
		s.RequiredLoopProtocolCalibrations > 0 ||
		len(s.RequiredLoopProtocolCalibrationRequestStatuses) > 0 ||
		len(s.RequiredLoopProtocolCalibrationStatuses) > 0 ||
		len(s.RequiredLoopProtocolFeedModes) > 0 ||
		len(s.RequiredLoopProtocolFeedMatches) > 0 ||
		s.RequireLoopProtocolFullAfterCompact ||
		strings.TrimSpace(s.RequiredLoopProtocolFinalStatus) != ""
}

func sortedStringMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func requiredMemoryUpdateMetadataMin(stats map[string]int) int {
	if len(stats) == 0 {
		return 0
	}
	min := stats["memory_updates"]
	for _, field := range []string{"memory_update_add", "memory_update_replace", "memory_update_remove"} {
		if stats[field] > min {
			min = stats[field]
		}
	}
	return min
}

func compactNonEmptyStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func SummarizeToolTruncation(trace Trace) ToolTruncationStats {
	var stats ToolTruncationStats
	for _, tool := range trace.Tools {
		if tool.ArgsTruncated {
			stats.ArgsTruncated++
		}
		stats.ArgsOmittedBytes += tool.ArgsOmittedBytes
		if tool.ResultTruncated {
			stats.ResultsTruncated++
			if tool.ResultArtifactPath == "" {
				stats.ResultMissingArtifacts++
			}
		}
		stats.ResultsOmittedBytes += tool.ResultOmittedBytes
		if tool.ResultArtifactPath != "" {
			stats.ResultArtifacts++
		}
		if tool.ContextOmittedBytes > 0 {
			stats.ContextTruncated++
			stats.ContextOmittedBytes += tool.ContextOmittedBytes
			if tool.ResultArtifactPath != "" {
				stats.ContextArtifacts++
			} else {
				stats.ContextMissingArtifacts++
			}
		}
	}
	return stats
}

// CheckBatchTrace runs BatchScenarioChecks against the trace and
// returns failure detail strings — the legacy signature BatchRunner.Run
// expects. New code should compose Check slices directly and read
// Outcome.FailedChecks() / Outcome.Results.
func CheckBatchTrace(trace Trace, scenario BatchScenario) []string {
	results := evaluateChecks(trace, BatchScenarioChecks(scenario))
	var failures []string
	for _, r := range results {
		if !r.Pass {
			failures = append(failures, r.Detail)
		}
	}
	return failures
}

func checkConversationSkill(workspace, skill string) error {
	root := filepath.Join(workspace, ".affentctl")
	found := false
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(raw), skill) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("search conversation skill: %w", err)
	}
	if !found {
		return fmt.Errorf("expected active skill %q not found in conversation log", skill)
	}
	return nil
}

func evalPath(repoRoot string) string {
	parts := []string{
		filepath.Join(repoRoot, ".tmp", "toolchains", "go", "bin"),
		filepath.Join(os.Getenv("HOME"), ".local", "go-toolchain", "go", "bin"),
		filepath.Join(os.Getenv("HOME"), ".local", "bin"),
		filepath.Join(os.Getenv("HOME"), "go", "bin"),
		"/usr/local/go/bin",
		"/snap/bin",
	}
	if path := os.Getenv("PATH"); path != "" {
		parts = append(parts, path)
	}
	return strings.Join(dedupeNonEmpty(parts), string(os.PathListSeparator))
}

func evalCommandEnv(repoRoot string, extra ...string) []string {
	base := os.Environ()
	out := make([]string, 0, len(base)+len(extra)+1)
	for _, kv := range base {
		key, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		switch key {
		case "GOROOT", "PATH":
			continue
		default:
			out = append(out, kv)
		}
	}
	out = append(out, "PATH="+evalPath(repoRoot))
	out = append(out, extra...)
	return out
}

func findGo(repoRoot string) string {
	for _, candidate := range []string{
		filepath.Join(repoRoot, ".tmp", "toolchains", "go", "bin", "go"),
		filepath.Join(os.Getenv("HOME"), ".local", "go-toolchain", "go", "bin", "go"),
		"/usr/local/go/bin/go",
		"go",
	} {
		if path, err := exec.LookPath(candidate); err == nil {
			if goCommandUsableForRepo(path, repoRoot) {
				return path
			}
			continue
		}
		if filepath.IsAbs(candidate) {
			if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
				if goCommandUsableForRepo(candidate, repoRoot) {
					return candidate
				}
			}
		}
	}
	return "go"
}

func goCommandUsableForRepo(goBin, repoRoot string) bool {
	if strings.TrimSpace(goBin) == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, goBin, "list", "-m")
	cmd.Dir = repoRoot
	cmd.Env = evalCommandEnv(repoRoot, "GOTOOLCHAIN=local")
	return cmd.Run() == nil
}

func dedupeNonEmpty(parts []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, part := range parts {
		if strings.TrimSpace(part) == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

func trimOneLine(s string, n int) string {
	s = textutil.CompactWhitespace(s)
	if len(s) <= n {
		return s
	}
	return textutil.Preview(s, n, "...")
}
