package agent

import "strings"

const SessionScheduleSystemGuidance = `Session scheduling:
- Use session_schedule for future turns, reminders, timers, recurring checks, and scheduled follow-ups. It is runtime-owned durable state and does not require LOOP.md.
- Use loop_protocol only for durable long-running task state that must preserve a north star, operating rules, recovery anchors, and completion semantics across turns.
- If both tools are available, ordinary timers and recurring checks should create or update a session_schedule with an RFC3339 next_run_at and repeat_interval_seconds. Use kind=loop_tick only when a scheduled turn is intentionally nudging an already-running LOOP.md.`

func WithSessionScheduleSystemGuidance(prompt string) string {
	if strings.TrimSpace(prompt) == "" {
		prompt = DefaultSystemPrompt
	}
	if strings.Contains(prompt, "Session scheduling:") {
		return prompt
	}
	return prompt + "\n\n" + SessionScheduleSystemGuidance
}
