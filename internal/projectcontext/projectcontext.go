package projectcontext

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/affinefoundation/affent/internal/gosymbols"
	"github.com/affinefoundation/affent/internal/textutil"
	"github.com/affinefoundation/affent/internal/workspaceignore"
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
const repoMapChildLimit = 6
const goSymbolFileLimit = 48
const goSymbolPackageLimit = 24
const goSymbolPerPackageLimit = 8

// Load reads recognized project-context files from workspaceDir and
// returns a system-prompt block. Returns "" when none are present.
// Each file enters under a `## <filename>` header.
func Load(workspaceDir string) string {
	if workspaceDir == "" {
		return ""
	}
	ignore, _ := workspaceignore.LoadGitignore(workspaceDir)
	sections, used := loadSections(workspaceDir, MaxBytes)
	if repoMap, repoUsed := loadRepoMap(workspaceDir, MaxBytes-used, ignore); repoMap != "" {
		sections = append(sections, repoMap)
		used += repoUsed
	}
	if codeHints, hintUsed := loadGoSymbolHints(workspaceDir, MaxBytes-used, ignore); codeHints != "" {
		sections = append(sections, codeHints)
		used += hintUsed
	}
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
		header := fmt.Sprintf("## %s\n\n", name)
		// Reserve header bytes from the budget so the truncated body
		// plus header still fits.
		bodyRoom := budget - used - len(header)
		if bodyRoom < 64 {
			break
		}
		content, truncated, err := readContextFile(path, bodyRoom)
		if err != nil {
			continue
		}
		if content == "" {
			continue
		}
		if truncated || len(content) > bodyRoom {
			content = truncateFile(content, bodyRoom, true)
		}
		section := header + content
		sections = append(sections, section)
		used += len(section)
	}
	return sections, used
}

func loadRepoMap(workspaceDir string, budget int, ignore *workspaceignore.Matcher) (string, int) {
	if workspaceDir == "" || budget <= 0 {
		return "", 0
	}
	entries, err := os.ReadDir(workspaceDir)
	if err != nil {
		return "", 0
	}
	var dirs []string
	var files []string
	for _, ent := range entries {
		name := ent.Name()
		if shouldSkipRepoMapEntry(name) || (ignore != nil && ignore.Ignored(name, ent.IsDir())) {
			continue
		}
		if ent.IsDir() {
			dirs = append(dirs, name)
			continue
		}
		if ent.Type().IsRegular() {
			files = append(files, name)
		}
	}
	if len(dirs) == 0 && len(files) == 0 {
		return "", 0
	}
	var b strings.Builder
	b.WriteString("## REPO MAP\n\n")
	if len(dirs) > 0 {
		b.WriteString("Top-level directories:\n")
		for _, name := range dirs {
			childSummary := summarizeRepoChildren(filepath.Join(workspaceDir, name), repoMapChildLimit, ignore, name)
			if childSummary != "" {
				fmt.Fprintf(&b, "- %s/ (%s)\n", name, childSummary)
				continue
			}
			fmt.Fprintf(&b, "- %s/\n", name)
		}
		b.WriteString("\n")
	}
	if len(files) > 0 {
		b.WriteString("Top-level files:\n")
		for _, name := range files {
			fmt.Fprintf(&b, "- %s\n", name)
		}
		b.WriteString("\n")
	}
	out := strings.TrimSpace(b.String())
	if len(out) > budget {
		out = truncateFile(out, budget, true)
	}
	return out, len(out)
}

func loadGoSymbolHints(workspaceDir string, budget int, ignore *workspaceignore.Matcher) (string, int) {
	if workspaceDir == "" || budget <= 0 {
		return "", 0
	}
	info, err := os.Stat(workspaceDir)
	if err != nil || info == nil {
		return "", 0
	}
	scan, err := gosymbols.ScanWorkspace(context.Background(), workspaceDir, workspaceDir, info, ignore, gosymbols.ScanOptions{
		IncludeTests:         false,
		MaxGoFiles:           goSymbolFileLimit,
		MaxPackages:          goSymbolPackageLimit,
		MaxSymbolsPerPackage: goSymbolPerPackageLimit,
		MaxSymbols:           goSymbolPackageLimit * goSymbolPerPackageLimit,
	})
	if err != nil || len(scan.Packages) == 0 {
		return "", 0
	}
	var b strings.Builder
	b.WriteString("## GO SYMBOL HINTS\n\n")
	for _, pkg := range scan.Packages {
		if pkg.RelDir == "" || pkg.PkgName == "" {
			continue
		}
		if len(pkg.Symbols) == 0 {
			continue
		}
		files := ""
		if len(pkg.Files) > 0 {
			files = " (" + strings.Join(pkg.Files, ", ") + ")"
		}
		fmt.Fprintf(&b, "- %s%s [%s]: %s\n", pkg.RelDir, files, pkg.PkgName, strings.Join(pkg.Symbols, ", "))
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "", 0
	}
	if len(out) > budget {
		out = truncateFile(out, budget, true)
	}
	return out, len(out)
}

func summarizeRepoChildren(dir string, limit int, ignore *workspaceignore.Matcher, parentRel string) string {
	entries, err := os.ReadDir(dir)
	if err != nil || limit <= 0 {
		return ""
	}
	var names []string
	more := 0
	for _, ent := range entries {
		name := ent.Name()
		rel := filepath.ToSlash(filepath.Join(parentRel, name))
		if shouldSkipRepoMapEntry(name) || (ignore != nil && ignore.Ignored(rel, ent.IsDir())) {
			continue
		}
		if len(names) < limit {
			names = append(names, name)
			continue
		}
		more++
	}
	if len(names) == 0 {
		return ""
	}
	if more > 0 {
		return strings.Join(names, ", ") + fmt.Sprintf(", ... (+%d more)", more)
	}
	return strings.Join(names, ", ")
}

func shouldSkipRepoMapEntry(name string) bool {
	for _, ctxName := range Files {
		if name == ctxName {
			return true
		}
	}
	if name == ".git" || name == "node_modules" || name == "dist" || name == "build" || name == "coverage" || name == "vendor" {
		return true
	}
	return strings.HasPrefix(name, ".")
}

func readContextFile(path string, limit int) (string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return "", false, err
	}
	truncated := len(raw) > limit
	if truncated {
		s := string(raw)
		raw = []byte(s[:textutil.AlignBackward(s, limit)])
	}
	return strings.TrimSpace(string(raw)), truncated, nil
}

// truncateFile clips at a UTF-8-safe boundary and appends a
// "[truncated]" marker within the limit.
func truncateFile(content string, limit int, forceMarker bool) string {
	const marker = "\n... [truncated]"
	if !forceMarker {
		head, _ := textutil.TruncateWithMarker(content, limit, func(_ int) string { return marker })
		return head
	}
	if limit <= len(marker) {
		return content[:textutil.AlignBackward(content, limit)]
	}
	cut := textutil.AlignBackward(content, limit-len(marker))
	return content[:cut] + marker
}
