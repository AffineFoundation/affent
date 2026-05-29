package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/affinefoundation/affent/internal/textutil"
	"github.com/affinefoundation/affent/internal/workspaceignore"
)

const (
	maxRepoSearchQueryBytes   = 1024
	maxRepoSearchPathBytes    = maxFileToolPathBytes
	defaultRepoSearchMaxHits  = 20
	maxRepoSearchMaxHits      = 50
	maxRepoSearchFileBytes    = 1 << 20
	maxRepoSearchScannerBytes = 1 << 20
	repoSearchBinaryProbe     = 8192
)

type repoSearchArgs struct {
	Query      string `json:"query"`
	Path       string `json:"path"`
	MaxResults int    `json:"max_results"`
}

type repoSearchTerm struct {
	Raw     string
	Compact string
}

type repoSearchHit struct {
	Path    string `json:"path"`
	Line    int    `json:"line,omitempty"`
	Snippet string `json:"snippet"`
}

type repoSearchStats struct {
	ScannedFiles  int
	SkippedFiles  int
	BinaryFiles   int
	OversizedFile int
	Truncated     bool
}

// repoSearchTool searches workspace text files for a query and returns
// concise path/line/snippet hits. It is intentionally lightweight: the
// goal is to help the model find likely files quickly without forcing
// it to shell out to rg/find or read whole files just to orient itself.
func repoSearchTool(deps BuiltinDeps) *Tool {
	schema := json.RawMessage(fmt.Sprintf(`{
        "type": "object",
        "additionalProperties": false,
        "required": ["query"],
        "properties": {
            "query": {"type": "string", "minLength": 1, "maxLength": %d, "description": "Search terms or phrase; all whitespace-separated terms must appear in a matched line or path, and symbol separators such as '_' or '-' are ignored for matching."},
            "path": {"type": "string", "maxLength": %d, "description": "Workspace-relative file or directory to search from; default workspace root."},
            "max_results": {"type": "integer", "minimum": 1, "maximum": %d, "default": %d, "description": "Maximum number of hits to return."}
        }
    }`, maxRepoSearchQueryBytes, maxRepoSearchPathBytes, maxRepoSearchMaxHits, defaultRepoSearchMaxHits))
	return &Tool{
		Name:        "repo_search",
		Description: "Search workspace text files for a query and return file:line snippets. Use for code, docs, symbol, TODO, and configuration discovery when you know the general area but not the exact file.",
		Schema:      schema,
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			p, err := decodeBuiltinToolArgs[repoSearchArgs]("repo_search", args, "query, path, max_results", "query is required; path and max_results are optional.")
			if err != nil {
				return "", err
			}
			p.Query = strings.TrimSpace(p.Query)
			if p.Query == "" {
				return "", errors.New("query is required\nNext: retry repo_search with a concrete term or phrase, or use list_files to find candidate paths first")
			}
			if len(p.Query) > maxRepoSearchQueryBytes {
				return "", fmt.Errorf("query is %d bytes; repo_search supports queries up to %d bytes\nNext: shorten the query to the smallest useful set of terms", len(p.Query), maxRepoSearchQueryBytes)
			}
			p.Path = strings.TrimSpace(p.Path)
			if p.Path == "" {
				p.Path = "."
			}
			if len(p.Path) > maxRepoSearchPathBytes {
				return "", fmt.Errorf("path is %d bytes; repo_search supports paths up to %d bytes\nNext: retry with a shorter workspace-relative path, or search from the workspace root", len(p.Path), maxRepoSearchPathBytes)
			}
			if p.MaxResults <= 0 {
				p.MaxResults = defaultRepoSearchMaxHits
			}
			if p.MaxResults > maxRepoSearchMaxHits {
				p.MaxResults = maxRepoSearchMaxHits
			}
			if deps.HostWorkspaceDir == "" {
				return "", errors.New("workspace is not configured; repo_search requires HostWorkspaceDir\nNext: restart affent with a workspace root before retrying repo_search")
			}
			root, err := safeWorkspacePath(deps, p.Path)
			if err != nil {
				return "", err
			}
			info, err := os.Stat(root)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return "", fileNotFoundToolError(deps, "repo_search", p.Path)
				}
				return "", err
			}
			terms := repoSearchTerms(p.Query)
			if len(terms) == 0 {
				return "", errors.New("query did not contain searchable terms\nNext: retry repo_search with at least one alphanumeric term")
			}
			wsAbs := deps.HostWorkspaceDir
			if resolved, err := filepath.EvalSymlinks(deps.HostWorkspaceDir); err == nil && resolved != "" {
				wsAbs = resolved
			}
			ignore, err := workspaceignore.LoadGitignore(wsAbs)
			if err != nil {
				return "", err
			}
			hits, stats, err := searchRepoTree(ctx, wsAbs, root, info, terms, p.MaxResults, ignore)
			if err != nil {
				return "", err
			}
			if len(hits) == 0 {
				var b strings.Builder
				fmt.Fprintf(&b, "no matches for %q under %s", p.Query, p.Path)
				if stats.ScannedFiles > 0 {
					fmt.Fprintf(&b, "\nsearched %d file(s)", stats.ScannedFiles)
				}
				if stats.BinaryFiles > 0 || stats.OversizedFile > 0 || stats.SkippedFiles > 0 {
					fmt.Fprintf(&b, "\nskipped %d binary, %d oversized, %d filtered file(s)", stats.BinaryFiles, stats.OversizedFile, stats.SkippedFiles)
				}
				b.WriteString("\nNext: broaden the query, use a different path, or fall back to shell rg/find if you need a custom search shape.")
				return b.String(), nil
			}
			var b strings.Builder
			fmt.Fprintf(&b, "found %d hit(s)", len(hits))
			if stats.ScannedFiles > 0 {
				fmt.Fprintf(&b, " in %d file(s)", stats.ScannedFiles)
			}
			if stats.BinaryFiles > 0 || stats.OversizedFile > 0 || stats.SkippedFiles > 0 {
				fmt.Fprintf(&b, " (skipped %d binary, %d oversized, %d filtered file(s))", stats.BinaryFiles, stats.OversizedFile, stats.SkippedFiles)
			}
			b.WriteString("\n")
			for _, hit := range hits {
				if hit.Line > 0 {
					fmt.Fprintf(&b, "%s:%d: %s\n", hit.Path, hit.Line, hit.Snippet)
				} else {
					fmt.Fprintf(&b, "%s: %s\n", hit.Path, hit.Snippet)
				}
			}
			if stats.Truncated {
				b.WriteString("... [more hits truncated]\n")
			}
			return strings.TrimSpace(b.String()), nil
		},
	}
}

func repoSearchTerms(query string) []repoSearchTerm {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil
	}
	seen := map[string]struct{}{}
	var out []repoSearchTerm
	for _, term := range strings.Fields(query) {
		if len(term) < 2 {
			continue
		}
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		out = append(out, repoSearchTerm{Raw: term, Compact: normalizeRepoSearchText(term)})
	}
	return out
}

func searchRepoTree(ctx context.Context, workspaceAbs, root string, info os.FileInfo, terms []repoSearchTerm, maxHits int, ignore *workspaceignore.Matcher) ([]repoSearchHit, repoSearchStats, error) {
	var stats repoSearchStats
	if maxHits <= 0 {
		maxHits = defaultRepoSearchMaxHits
	}
	results := make([]repoSearchHit, 0, maxHits)
	addPathHit := func(displayPath string) {
		if len(results) >= maxHits {
			return
		}
		results = append(results, repoSearchHit{
			Path:    displayPath,
			Snippet: "path match",
		})
	}
	if !info.IsDir() {
		display := displayRepoSearchPath(workspaceAbs, root)
		if ignore != nil && ignore.Ignored(display, false) {
			stats.SkippedFiles++
			return results, stats, nil
		}
		if matchesQuery(strings.ToLower(display), normalizeRepoSearchText(display), terms) {
			addPathHit(display)
		}
		if len(results) < maxHits {
			adds, st, err := searchRepoFile(ctx, root, display, terms, maxHits-len(results), ignore)
			stats.ScannedFiles += st.ScannedFiles
			stats.SkippedFiles += st.SkippedFiles
			stats.BinaryFiles += st.BinaryFiles
			stats.OversizedFile += st.OversizedFile
			stats.Truncated = stats.Truncated || st.Truncated
			if err != nil {
				return nil, stats, err
			}
			results = append(results, adds...)
		}
		return results, stats, nil
	}
	err := filepath.WalkDir(root, func(full string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if full != root && d.IsDir() && shouldSkipRepoSearchDir(d.Name()) {
			stats.SkippedFiles++
			return filepath.SkipDir
		}
		display := displayRepoSearchPath(workspaceAbs, full)
		if ignore != nil && ignore.Ignored(display, d.IsDir()) {
			stats.SkippedFiles++
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if len(results) >= maxHits {
			stats.Truncated = true
			return filepath.SkipAll
		}
		if matchesQuery(strings.ToLower(display), normalizeRepoSearchText(display), terms) {
			addPathHit(display)
			return nil
		}
		adds, st, ferr := searchRepoFile(ctx, full, display, terms, maxHits-len(results), ignore)
		stats.ScannedFiles += st.ScannedFiles
		stats.SkippedFiles += st.SkippedFiles
		stats.BinaryFiles += st.BinaryFiles
		stats.OversizedFile += st.OversizedFile
		stats.Truncated = stats.Truncated || st.Truncated
		if ferr != nil {
			return ferr
		}
		results = append(results, adds...)
		if len(results) >= maxHits {
			stats.Truncated = true
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil && err != filepath.SkipAll {
		return nil, stats, err
	}
	return results, stats, nil
}

func searchRepoFile(ctx context.Context, fullPath, displayPath string, terms []repoSearchTerm, maxHits int, ignore *workspaceignore.Matcher) ([]repoSearchHit, repoSearchStats, error) {
	var stats repoSearchStats
	if maxHits <= 0 {
		return nil, stats, nil
	}
	fi, err := os.Stat(fullPath)
	if err != nil {
		return nil, stats, err
	}
	if fi.IsDir() {
		return nil, stats, nil
	}
	stats.ScannedFiles = 1
	if ignore != nil && ignore.Ignored(displayPath, false) {
		stats.SkippedFiles = 1
		return nil, stats, nil
	}
	if fi.Size() > maxRepoSearchFileBytes {
		stats.OversizedFile = 1
		return nil, stats, nil
	}
	f, err := os.Open(fullPath)
	if err != nil {
		return nil, stats, err
	}
	defer f.Close()
	probe := make([]byte, repoSearchBinaryProbe)
	n, _ := f.Read(probe)
	if looksBinary(probe[:n]) {
		stats.BinaryFiles = 1
		return nil, stats, nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, stats, err
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxRepoSearchScannerBytes)
	lineNo := 0
	hits := make([]repoSearchHit, 0, maxHits)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return nil, stats, ctx.Err()
		}
		lineNo++
		line := scanner.Text()
		if !matchesQuery(strings.ToLower(line), normalizeRepoSearchText(line), terms) {
			continue
		}
		hits = append(hits, repoSearchHit{
			Path:    displayPath,
			Line:    lineNo,
			Snippet: textutil.Preview(strings.TrimSpace(line), 220, "…"),
		})
		if len(hits) >= maxHits {
			stats.Truncated = true
			break
		}
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			stats.Truncated = true
			return hits, stats, nil
		}
		return nil, stats, err
	}
	return hits, stats, nil
}

func matchesQuery(text, compactText string, terms []repoSearchTerm) bool {
	if len(terms) == 0 {
		return false
	}
	for _, term := range terms {
		if !strings.Contains(text, term.Raw) && !strings.Contains(compactText, term.Compact) {
			return false
		}
	}
	return true
}

func normalizeRepoSearchText(text string) string {
	if text == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

func displayRepoSearchPath(workspaceAbs, full string) string {
	if workspaceAbs == "" {
		return filepath.ToSlash(full)
	}
	rel, err := filepath.Rel(workspaceAbs, full)
	if err != nil {
		return filepath.ToSlash(full)
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		return "."
	}
	return rel
}

func shouldSkipRepoSearchDir(name string) bool {
	if name == ".git" || name == "node_modules" || name == "dist" || name == "build" || name == "coverage" || name == "vendor" {
		return true
	}
	return strings.HasPrefix(name, ".")
}
