package affent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// projectContextFiles lists user-authored project knowledge files
// recognized in a workspace, in load priority order. When multiple
// are present, all are read and concatenated.
var projectContextFiles = []string{
	"AGENTS.md",
	"CLAUDE.md",
	"CONVENTIONS.md",
	".cursorrules",
	".clinerules",
	".clinerules.md",
	"GEMINI.md",
}

// MaxProjectContextBytes caps the total project-context block injected
// into the system prompt. Per-file content beyond this budget is
// truncated; files past the budget are skipped.
const MaxProjectContextBytes = 32 * 1024

// LoadProjectContext reads recognized project-context files from
// workspaceDir and returns a system-prompt block. Returns "" when
// none are present. Each file enters under a `## <filename>` header.
func LoadProjectContext(workspaceDir string) string {
	if workspaceDir == "" {
		return ""
	}
	sections, used := loadProjectContextSections(workspaceDir, MaxProjectContextBytes)
	if len(sections) == 0 {
		return ""
	}
	sep := strings.Repeat("=", memoryHeaderRuleWidth)
	header := fmt.Sprintf("PROJECT CONTEXT (user-authored project notes; %d/%d bytes)",
		used, MaxProjectContextBytes)
	return fmt.Sprintf("%s\n%s\n%s\n\n%s", sep, header, sep, strings.Join(sections, "\n\n"))
}

func loadProjectContextSections(workspaceDir string, budget int) ([]string, int) {
	var sections []string
	used := 0
	for _, name := range projectContextFiles {
		path := filepath.Join(workspaceDir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := strings.TrimSpace(string(raw))
		if content == "" {
			continue
		}
		if used >= budget {
			break
		}
		remaining := budget - used
		header := fmt.Sprintf("## %s\n\n", name)
		// Reserve header bytes from the budget so the truncated body
		// plus header still fits.
		bodyRoom := remaining - len(header)
		if bodyRoom < 64 {
			break
		}
		if len(content) > bodyRoom {
			content = truncateProjectFile(content, bodyRoom)
		}
		section := header + content
		sections = append(sections, section)
		used += len(section)
	}
	return sections, used
}

// truncateProjectFile clips at a UTF-8-safe boundary and appends a
// "[truncated]" marker within the limit.
func truncateProjectFile(content string, limit int) string {
	const marker = "\n... [truncated]"
	if limit <= len(marker) {
		return content[:utf8AlignBackward(content, limit)]
	}
	cut := utf8AlignBackward(content, limit-len(marker))
	return content[:cut] + marker
}
