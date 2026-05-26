package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/affinefoundation/affent/internal/sourceaccess"
	"github.com/affinefoundation/affent/internal/sse"
	"github.com/affinefoundation/affent/internal/textutil"
	"github.com/affinefoundation/affent/internal/toolfailure"
	"github.com/affinefoundation/affent/internal/toolrepair"
)

func summarizeOriginalToolArgs(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	return textutil.Preview(raw, 512)
}

func toolRequestArgsView(args json.RawMessage) map[string]any {
	return toolRequestArgsEventView(args).Args
}

type toolRequestArgsEvent struct {
	Args         map[string]any
	Truncated    bool
	Bytes        int
	OmittedBytes int
	CapBytes     int
}

func toolRequestArgsEventView(args json.RawMessage) toolRequestArgsEvent {
	view := toolRequestArgsEvent{
		Args:     map[string]any{},
		Bytes:    len(args),
		CapBytes: maxToolRequestArgsEventBytes,
	}
	var obj map[string]any
	if err := json.Unmarshal(args, &obj); err != nil || obj == nil {
		return view
	}
	cappedAny, omitted := capToolRequestArgValue(obj)
	capped, ok := cappedAny.(map[string]any)
	if !ok || capped == nil {
		return view
	}
	raw, err := json.Marshal(capped)
	if err == nil && len(raw) <= maxToolRequestArgsEventBytes {
		view.Args = capped
		view.Truncated = omitted > 0
		view.OmittedBytes = omitted
		return view
	}
	view.Args = map[string]any{
		"__affent_truncated":      fmt.Sprintf("tool request args exceeded %d-byte event cap", maxToolRequestArgsEventBytes),
		"__affent_original_bytes": len(args),
	}
	view.Truncated = true
	view.OmittedBytes = len(args)
	return view
}

func capToolRequestArgValue(v any) (any, int) {
	switch x := v.(type) {
	case string:
		if len(x) <= maxToolRequestArgStringBytes {
			return x, 0
		}
		cut := textutil.AlignBackward(x, maxToolRequestArgStringBytes)
		return x[:cut] + fmt.Sprintf("\n... [%d more bytes truncated from tool.request arg string]", len(x)-cut), len(x) - cut
	case map[string]any:
		out := make(map[string]any, len(x))
		omitted := 0
		for k, v := range x {
			capped, n := capToolRequestArgValue(v)
			out[k] = capped
			omitted += n
		}
		return out, omitted
	case []any:
		out := make([]any, len(x))
		omitted := 0
		for i, v := range x {
			capped, n := capToolRequestArgValue(v)
			out[i] = capped
			omitted += n
		}
		return out, omitted
	default:
		return v, 0
	}
}

func toolRuntimeStatsPtr(stats sse.ToolRuntimeStats) *sse.ToolRuntimeStats {
	if stats.ToolRequests == 0 &&
		stats.ToolNameCanonicalized == 0 &&
		stats.ToolArgsRepaired == 0 &&
		stats.ToolRepairCalls == 0 &&
		stats.ToolRepairSucceeded == 0 &&
		stats.ToolRepairFailed == 0 &&
		stats.ToolRepairNotes == 0 &&
		len(stats.ToolRepairByKind) == 0 &&
		len(stats.ToolFailureByKind) == 0 &&
		stats.ToolErrors == 0 &&
		stats.ToolDurationMS == 0 &&
		stats.LoopGuardInterventions == 0 &&
		stats.ForcedNoTools == 0 &&
		stats.SourceAccessResults == 0 &&
		stats.SourceAccessVerified == 0 &&
		stats.SourceAccessDiscoveryOnly == 0 &&
		stats.SourceAccessNetwork == 0 &&
		stats.SourceAccessDynamicPartial == 0 &&
		stats.MemoryUpdates == 0 &&
		stats.MemoryUpdateAdd == 0 &&
		stats.MemoryUpdateReplace == 0 &&
		stats.MemoryUpdateRemove == 0 &&
		stats.ToolContextTruncated == 0 &&
		stats.ToolContextOmittedBytes == 0 {
		return nil
	}
	return &stats
}

func recordToolContextOmission(stats *sse.ToolRuntimeStats, omitted int) {
	if stats == nil || omitted <= 0 {
		return
	}
	stats.ToolContextTruncated++
	stats.ToolContextOmittedBytes += omitted
}

func recordToolFailureKind(stats *sse.ToolRuntimeStats, tool, result string, failed bool) {
	if stats == nil {
		return
	}
	kinds := toolfailure.KindsForResult(tool, result, failed)
	if len(kinds) == 0 {
		return
	}
	if stats.ToolFailureByKind == nil {
		stats.ToolFailureByKind = map[string]int{}
	}
	for _, kind := range kinds {
		stats.ToolFailureByKind[kind]++
	}
}

func recordSourceAccessStats(stats *sse.ToolRuntimeStats, result string) {
	if stats == nil {
		return
	}
	info, ok := sourceaccess.FirstInfoFromResult(result)
	if !ok {
		return
	}
	stats.SourceAccessResults++
	if info.IsDiscoveryOnly() {
		stats.SourceAccessDiscoveryOnly++
	} else if info.AccessedURL != "" {
		stats.SourceAccessVerified++
	}
	if info.IsNetworkSource() {
		stats.SourceAccessNetwork++
	}
	if sourceaccess.HasDynamicPartialEvidence(result) {
		stats.SourceAccessDynamicPartial++
	}
}

func recordMemoryUpdateStats(stats *sse.ToolRuntimeStats, tool string, args json.RawMessage, result string, isErr bool) {
	if stats == nil || tool != MemoryToolName || isErr {
		return
	}
	var req struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return
	}
	action := strings.TrimSpace(req.Action)
	if action != memoryActionAdd && action != memoryActionReplace && action != memoryActionRemove {
		return
	}
	var resp struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal([]byte(result), &resp); err != nil || !resp.OK {
		return
	}
	stats.MemoryUpdates++
	switch action {
	case memoryActionAdd:
		stats.MemoryUpdateAdd++
	case memoryActionReplace:
		stats.MemoryUpdateReplace++
	case memoryActionRemove:
		stats.MemoryUpdateRemove++
	}
}

func toolFailureKind(result string) string {
	return toolfailure.Kind(result)
}

func recordToolRepairNotes(stats *sse.ToolRuntimeStats, notes []string) {
	if stats == nil {
		return
	}
	for _, note := range notes {
		kind := toolrepair.Kind(note)
		if kind == "" {
			continue
		}
		stats.ToolRepairNotes++
		if stats.ToolRepairByKind == nil {
			stats.ToolRepairByKind = map[string]int{}
		}
		stats.ToolRepairByKind[kind]++
	}
}

func recordToolRepairOutcome(stats *sse.ToolRuntimeStats, repaired bool, isErr bool) {
	if stats == nil || !repaired {
		return
	}
	stats.ToolRepairCalls++
	if isErr {
		stats.ToolRepairFailed++
		return
	}
	stats.ToolRepairSucceeded++
}

func toolResultEventPayload(callID string, exitCode int, result string) sse.ToolResultPayload {
	cappedResult, truncated, omitted := truncateForEventWithStats(result, MaxToolResultBytesInEvent)
	payload := sse.ToolResultPayload{
		CallID:             callID,
		ExitCode:           exitCode,
		ResultSummary:      textutil.Preview(result, MaxToolResultPreviewInEvent),
		Result:             cappedResult,
		ResultTruncated:    truncated,
		ResultBytes:        len(result),
		ResultOmittedBytes: omitted,
		ResultCapBytes:     MaxToolResultBytesInEvent,
	}
	if exitCode != 0 {
		payload.FailureKind = toolfailure.Kind(result)
		payload.FailureKinds = toolfailure.Kinds(result)
	}
	return payload
}

func toolFailureKindForOutcome(tool, result string, isErr bool) string {
	return toolfailure.KindForResult(tool, result, isErr)
}

func toolFailureKindsForOutcome(tool, result string, isErr bool) []string {
	return toolfailure.KindsForResult(tool, result, isErr)
}

func toolResultEventPayloadForTurn(turnID, callID string, exitCode int, result string) sse.ToolResultPayload {
	payload := toolResultEventPayload(callID, exitCode, result)
	payload.TurnID = turnID
	return payload
}

func toolResultEventPayloadWithDuration(callID string, exitCode int, result string, duration time.Duration) sse.ToolResultPayload {
	payload := toolResultEventPayload(callID, exitCode, result)
	if duration >= time.Millisecond {
		payload.DurationMS = duration.Milliseconds()
	}
	return payload
}

func toolResultEventPayloadWithDurationForTurn(turnID, callID string, exitCode int, result string, duration time.Duration) sse.ToolResultPayload {
	payload := toolResultEventPayloadForTurn(turnID, callID, exitCode, result)
	if duration >= time.Millisecond {
		payload.DurationMS = duration.Milliseconds()
	}
	return payload
}

func truncateForEvent(s string, max int) string {
	out, _, _ := truncateForEventWithStats(s, max)
	return out
}

func truncateForEventWithStats(s string, max int) (string, bool, int) {
	out, omitted := textutil.TruncateWithMarker(s, max, func(omitted int) string {
		return fmt.Sprintf("\n... [%d more bytes truncated from tool.result event payload]", omitted)
	})
	return out, omitted > 0, omitted
}

func truncateForContext(s string, max int) string {
	head, omitted := textutil.PreviewHead(s, max)
	if omitted == 0 {
		return head
	}
	return head + defaultContextTruncationMarker(omitted)
}

func truncateToolResultForContext(toolName, result string, max int, artifactPath string) string {
	head, omitted := textutil.PreviewHead(result, max)
	if omitted == 0 {
		return head
	}
	return head + toolResultContextTruncationMarker(toolName, omitted, artifactPath)
}

type toolResultContextBudget struct {
	remaining       int
	browserPageURLs map[string]int
}

func newToolResultContextBudget(max int) *toolResultContextBudget {
	if max <= 0 {
		return nil
	}
	return &toolResultContextBudget{remaining: max}
}

func (b *toolResultContextBudget) exhausted() bool {
	return b != nil && b.remaining <= 0
}

func (b *toolResultContextBudget) truncateToolResult(toolName, result string, perToolMax int, artifactPath string) (string, int) {
	if result == "" {
		return "", 0
	}
	if perToolMax <= 0 {
		perToolMax = MaxToolResultBytesInContext
	}
	if b == nil {
		return truncateToolResultForContext(toolName, result, perToolMax, artifactPath), toolResultContextOmittedBytes(result, perToolMax)
	}
	if repeatedBrowserPage := b.recordBrowserPageResult(toolName, result); repeatedBrowserPage {
		return b.truncateRepeatedBrowserPageResult(toolName, result, perToolMax)
	}
	if b.remaining <= 0 {
		return toolResultContextBudgetExhaustedResult(toolName, result, artifactPath)
	}

	max := perToolMax
	budgetLimited := false
	if b.remaining < max {
		max = b.remaining
		budgetLimited = true
	}
	if len(result) <= max {
		b.remaining -= len(result)
		return result, 0
	}

	cut := textutil.AlignBackward(result, max)
	b.remaining -= cut
	omitted := len(result) - cut
	marker := toolResultContextTruncationMarker(toolName, omitted, artifactPath)
	if budgetLimited {
		marker = toolResultContextBudgetTruncationMarker(toolName, omitted, artifactPath)
	}
	return result[:cut] + marker, omitted
}

func (b *toolResultContextBudget) recordBrowserPageResult(toolName, result string) bool {
	if b == nil || !isBrowserPageSnapshotTool(toolName) {
		return false
	}
	u := toolResultBrowserURL(result)
	if u == "" {
		return false
	}
	if b.browserPageURLs == nil {
		b.browserPageURLs = map[string]int{}
	}
	seen := b.browserPageURLs[u]
	b.browserPageURLs[u] = seen + 1
	return seen > 0
}

func (b *toolResultContextBudget) truncateRepeatedBrowserPageResult(toolName, result string, perToolMax int) (string, int) {
	if b == nil || b.remaining <= 0 {
		return toolResultContextBudgetExhaustedResult(toolName, result, "")
	}
	headMax := min(perToolMax, 768)
	headMax = min(headMax, b.remaining)
	head := toolResultContextEvidenceHead(result, headMax)
	if head == "" || len(result) <= len(head) {
		b.remaining -= min(len(result), b.remaining)
		return result, 0
	}
	b.remaining -= len(head)
	omitted := len(result) - len(head)
	return head + "\n\n" + repeatedBrowserPageContextMarker(toolName, omitted), omitted
}

func isBrowserPageSnapshotTool(toolName string) bool {
	switch toolName {
	case "browser_navigate", "browser_snapshot", "browser_scroll", "browser_wait", "browser_click", "browser_type":
		return true
	default:
		return false
	}
}

func toolResultBrowserURL(result string) string {
	return sourceaccess.AccessedURLFromResult(result)
}

func toolResultContextBudgetExhaustedResult(toolName, result string, artifactPath string) (string, int) {
	head := toolResultContextEvidenceHead(result, 512)
	if head == "" {
		return toolResultContextBudgetExhaustedMarker(toolName, len(result), artifactPath), len(result)
	}
	omitted := max(0, len(result)-len(head))
	if omitted == 0 {
		return head, 0
	}
	return head + "\n\n" + toolResultContextBudgetExhaustedMarker(toolName, omitted, artifactPath), omitted
}

func toolResultContextEvidenceHead(result string, maxBytes int) string {
	head, _ := textutil.PreviewHead(result, maxBytes)
	return head
}

func repeatedBrowserPageContextMarker(toolName string, omitted int) string {
	return fmt.Sprintf(
		"[... %d bytes omitted from repeated %s output for a browser page already read this turn. Use the earlier snapshot, browser_find for targeted text, navigate to a different source URL, or answer from verified evidence already collected.]",
		omitted, toolName,
	)
}

func toolResultContextOmittedBytes(result string, max int) int {
	_, omitted := textutil.PreviewHead(result, max)
	return omitted
}

func defaultContextTruncationMarker(omitted int) string {
	return fmt.Sprintf(
		"\n\n[... %d more bytes truncated. Re-run the command piping through head/tail/grep/sed, or save the output to a file inside the configured workspace and read it in chunks, if you need more.]",
		omitted,
	)
}

func toolResultContextTruncationMarker(toolName string, omitted int, artifactPath string) string {
	switch toolName {
	case "browser_navigate", "browser_snapshot", "browser_scroll", "browser_wait", "browser_click", "browser_type":
		return fmt.Sprintf(
			"\n\n[... %d more bytes truncated from %s before model context. Use browser_find for targeted visible text, navigate to a more specific URL/section, or answer from the verified visible evidence instead of repeating broad page snapshots.]",
			omitted, toolName,
		) + artifactReadHint(artifactPath)
	case "web_fetch":
		return fmt.Sprintf(
			"\n\n[... %d more bytes truncated from web_fetch before model context. Use a more specific API/text/source URL, fetch a narrower canonical page, or answer with clearly marked gaps from the verified evidence already visible.]",
			omitted,
		) + artifactReadHint(artifactPath)
	default:
		return defaultContextTruncationMarker(omitted) + artifactReadHint(artifactPath)
	}
}

func toolResultContextBudgetTruncationMarker(toolName string, omitted int, artifactPath string) string {
	switch toolName {
	case "browser_navigate", "browser_snapshot", "browser_scroll", "browser_wait", "browser_click", "browser_type":
		return fmt.Sprintf(
			"\n\n[... %d more bytes omitted from %s before model context because the per-turn tool-result context budget is nearly exhausted. Use browser_find, navigate to a narrower page/section, or answer from verified evidence already collected.]",
			omitted, toolName,
		) + artifactReadHint(artifactPath)
	case "web_fetch":
		return fmt.Sprintf(
			"\n\n[... %d more bytes omitted from web_fetch before model context because the per-turn tool-result context budget is nearly exhausted. Use a narrower canonical/API/text source, or answer with clearly marked gaps from verified evidence already collected.]",
			omitted,
		) + artifactReadHint(artifactPath)
	default:
		return fmt.Sprintf(
			"\n\n[... %d more bytes omitted before model context because the per-turn tool-result context budget is nearly exhausted. Use narrower tool calls or answer from verified evidence already collected.]",
			omitted,
		) + artifactReadHint(artifactPath)
	}
}

func toolResultContextBudgetExhaustedMarker(toolName string, omitted int, artifactPath string) string {
	switch toolName {
	case "browser_navigate", "browser_snapshot", "browser_scroll", "browser_wait", "browser_click", "browser_type":
		return fmt.Sprintf(
			"[tool result omitted from model context: %d bytes from %s exceeded the per-turn tool-result context budget. Use browser_find, navigate to a narrower page/section, or answer from verified evidence already collected.]",
			omitted, toolName,
		) + artifactReadHint(artifactPath)
	case "web_fetch":
		return fmt.Sprintf(
			"[tool result omitted from model context: %d bytes from web_fetch exceeded the per-turn tool-result context budget. Use a narrower canonical/API/text source, or answer with clearly marked gaps from verified evidence already collected.]",
			omitted,
		) + artifactReadHint(artifactPath)
	default:
		return fmt.Sprintf(
			"[tool result omitted from model context: %d bytes from %s exceeded the per-turn tool-result context budget. Use narrower tool calls or answer from verified evidence already collected.]",
			omitted, toolName,
		) + artifactReadHint(artifactPath)
	}
}

func artifactReadHint(artifactPath string) string {
	artifactPath = strings.TrimSpace(artifactPath)
	if artifactPath == "" {
		return ""
	}
	return "\nUse the saved artifact with read_file if you need the complete output: " + artifactPath
}
