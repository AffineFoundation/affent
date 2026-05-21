package projectcontext

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/affinefoundation/affent/internal/textutil"
)

// Files lists user-authored project knowledge files recognized in a
// workspace, in load priority order. When multiple are present, all
// are read and concatenated.
var Files = []string{
	"AGENTS.md",
	"CLAUDE.md",
	"CONVENTIONS.md",
	".cursorrules",
	".clinerules",
	".clinerules.md",
	"GEMINI.md",
}

// MaxBytes caps the total project-context block injected into the
// system prompt. Per-file content beyond this budget is truncated;
// files past the budget are skipped.
const MaxBytes = 32 * 1024

const headerRuleWidth = 46

// Load reads recognized project-context files from workspaceDir and
// returns a system-prompt block. Returns "" when none are present.
// Each file enters under a `## <filename>` header.
func Load(workspaceDir string) string {
	if workspaceDir == "" {
		return ""
	}
	sections, used := loadSections(workspaceDir, MaxBytes)
	if len(sections) == 0 {
		return ""
	}
	sep := strings.Repeat("=", headerRuleWidth)
	header := fmt.Sprintf("PROJECT CONTEXT (user-authored project notes; %d/%d bytes)",
		used, MaxBytes)
	return fmt.Sprintf("%s\n%s\n%s\n\n%s", sep, header, sep, strings.Join(sections, "\n\n"))
}

func loadSections(workspaceDir string, budget int) ([]string, int) {
	var sections []string
	used := 0
	for _, name := range Files {
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
			content = truncateFile(content, bodyRoom)
		}
		section := header + content
		sections = append(sections, section)
		used += len(section)
	}
	return sections, used
}

// truncateFile clips at a UTF-8-safe boundary and appends a
// "[truncated]" marker within the limit.
func truncateFile(content string, limit int) string {
	const marker = "\n... [truncated]"
	if limit <= len(marker) {
		return content[:textutil.AlignBackward(content, limit)]
	}
	cut := textutil.AlignBackward(content, limit-len(marker))
	return content[:cut] + marker
}
