package affent

import "github.com/affinefoundation/affent/internal/projectcontext"

// MaxProjectContextBytes caps the total project-context block injected
// into the system prompt. Per-file content beyond this budget is
// truncated; files past the budget are skipped.
const MaxProjectContextBytes = projectcontext.MaxBytes

// LoadProjectContext reads recognized project-context files from
// workspaceDir and returns a system-prompt block. Returns "" when
// none are present. Each file enters under a `## <filename>` header.
func LoadProjectContext(workspaceDir string) string {
	return projectcontext.Load(workspaceDir)
}
