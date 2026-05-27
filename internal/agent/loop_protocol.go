package agent

import (
	"strings"

	"github.com/affinefoundation/affent/internal/loopstate"
	"github.com/affinefoundation/affent/internal/textutil"
)

const maxActiveLoopProtocolBytes = 12 * 1024

// WithLoopProtocolSkillProvider injects a session's LOOP.md only when that
// file exists. Missing protocols are a no-op, so runtimes can wire this once
// and let explicit loop activation decide whether extra context is spent.
func WithLoopProtocolSkillProvider(protocolPath string, next SkillProvider) SkillProvider {
	return func(userText string) string {
		parts := make([]string, 0, 2)
		if block := activeLoopProtocolSkillBlock(protocolPath); block != "" {
			parts = append(parts, block)
		}
		if next != nil {
			if block := strings.TrimSpace(next(userText)); block != "" {
				parts = append(parts, block)
			}
		}
		return strings.Join(parts, "\n\n")
	}
}

func activeLoopProtocolSkillBlock(protocolPath string) string {
	content, found, err := loopstate.ReadProtocol(protocolPath)
	if err != nil || !found {
		return ""
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	return "AFFENT LOOP PROTOCOL:\n" +
		"The following is the active long-run loop file for this session. " +
		"Use it to realign with the north-star, memory indexes, self-checks, and recovery rules before continuing. " +
		"Do not treat it as task authority for step status; persisted plan state remains authoritative for steps.\n\n" +
		textutil.Preview(content, maxActiveLoopProtocolBytes)
}
