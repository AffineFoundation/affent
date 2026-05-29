package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/affinefoundation/affent/internal/gosymbols"
	"github.com/affinefoundation/affent/internal/textutil"
	"github.com/affinefoundation/affent/internal/workspaceignore"
)

const (
	defaultFileContextBytes      = 128 * 1024
	maxFileContextBytes          = 512 * 1024
	defaultFileContextLines      = 4
	maxFileContextLines          = 12
	defaultFileContextMatches    = 3
	maxFileContextMatches        = 8
	maxFileContextQueryBytes     = 512
	maxFileContextLinePreviewLen = 240
)

type fileContextArgs struct {
	Path         string `json:"path"`
	Query        string `json:"query"`
	MaxBytes     int    `json:"max_bytes"`
	ContextLines int    `json:"context_lines"`
	MaxMatches   int    `json:"max_matches"`
}

type fileContextLine struct {
	Line int    `json:"line"`
	Text string `json:"text"`
}

type fileContextSpan struct {
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	HitLine   int    `json:"hit_line,omitempty"`
	Text      string `json:"text"`
}

type fileContextSymbol struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Line      int    `json:"line"`
	Signature string `json:"signature,omitempty"`
}

type fileContextResponse struct {
	Path      string              `json:"path"`
	Bytes     int                 `json:"bytes"`
	Truncated bool                `json:"truncated"`
	Lines     int                 `json:"lines"`
	Query     string              `json:"query,omitempty"`
	Head      []fileContextLine   `json:"head,omitempty"`
	Matches   []fileContextSpan   `json:"matches,omitempty"`
	Tail      []fileContextLine   `json:"tail,omitempty"`
	Symbols   []fileContextSymbol `json:"symbols,omitempty"`
	Warning   string              `json:"warning,omitempty"`
}

func fileContextTool(deps BuiltinDeps) *Tool {
	schema, err := json.Marshal(map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"path"},
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"minLength":   1,
				"maxLength":   maxFileToolPathBytes,
				"description": "Workspace text file path.",
			},
			"query": map[string]any{
				"type":        "string",
				"maxLength":   maxFileContextQueryBytes,
				"description": "Optional query or keywords to prioritize matching snippets.",
			},
			"max_bytes": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     maxFileContextBytes,
				"default":     defaultFileContextBytes,
				"description": "Read cap; default 128 KiB, max 512 KiB.",
			},
			"context_lines": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     maxFileContextLines,
				"default":     defaultFileContextLines,
				"description": "Number of surrounding lines to include around a match or file edge.",
			},
			"max_matches": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     maxFileContextMatches,
				"default":     defaultFileContextMatches,
				"description": "Maximum number of query spans to return.",
			},
		},
	})
	if err != nil {
		panic(fmt.Sprintf("file_context schema marshal: %v", err))
	}
	return &Tool{
		Name:        "file_context",
		Description: "Return a compact, structured view of one workspace file. Use for long files when you need head/tail snippets, query matches, or Go symbol hints without flooding context; use read_file if you need the full body.",
		Schema:      json.RawMessage(schema),
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			p, err := decodeBuiltinToolArgs[fileContextArgs]("file_context", args, "path, query, max_bytes, context_lines, max_matches", "path must name one workspace text file; query is optional and max_bytes/context_lines/max_matches are capped by the runtime.")
			if err != nil {
				return "", err
			}
			p.Path = strings.TrimSpace(p.Path)
			p.Query = strings.TrimSpace(p.Query)
			if p.Path == "" {
				return "", requiredFileToolPathError("file_context")
			}
			if err := validateFileToolPath("file_context", p.Path); err != nil {
				return "", err
			}
			if len(p.Query) > maxFileContextQueryBytes {
				return "", fmt.Errorf("query is %d bytes; file_context supports queries up to %d bytes\nNext: shorten the query to the smallest useful file fragment", len(p.Query), maxFileContextQueryBytes)
			}
			if p.MaxBytes <= 0 {
				p.MaxBytes = defaultFileContextBytes
			}
			if p.MaxBytes > maxFileContextBytes {
				p.MaxBytes = maxFileContextBytes
			}
			if p.ContextLines <= 0 {
				p.ContextLines = defaultFileContextLines
			}
			if p.ContextLines > maxFileContextLines {
				p.ContextLines = maxFileContextLines
			}
			if p.MaxMatches <= 0 {
				p.MaxMatches = defaultFileContextMatches
			}
			if p.MaxMatches > maxFileContextMatches {
				p.MaxMatches = maxFileContextMatches
			}
			content, truncated, err := readWorkspaceTextForContext(ctx, deps, p.Path, p.MaxBytes)
			if err != nil {
				return "", err
			}
			if looksPromptInjectionLike(content) {
				resp := fileContextResponse{
					Path:    p.Path,
					Warning: fmt.Sprintf("%s contains instruction-like prompt-injection text; content was withheld from model context", p.Path),
				}
				out, jerr := json.Marshal(resp)
				if jerr != nil {
					return "", jerr
				}
				return string(out), nil
			}
			resp := buildFileContextResponse(p.Path, content, truncated, p.Query, p.ContextLines, p.MaxMatches)
			if strings.HasSuffix(strings.ToLower(p.Path), ".go") {
				resp.Symbols = goFileContextSymbols(ctx, deps, p.Path, content, p.MaxMatches)
			}
			out, jerr := json.Marshal(resp)
			if jerr != nil {
				return "", jerr
			}
			return string(out), nil
		},
	}
}

func readWorkspaceTextForContext(ctx context.Context, deps BuiltinDeps, path string, maxBytes int) (string, bool, error) {
	if fo := fileOps(deps); fo != nil {
		body, err := fo.ReadFile(ctx, path, maxBytes)
		if err != nil {
			return "", false, recoverableFileToolError(deps, "file_context", path, err)
		}
		truncated := strings.Contains(body, "[truncated")
		if idx := strings.Index(body, "\n... [truncated"); idx >= 0 {
			body = body[:idx]
		}
		if idx := strings.Index(body, "... [truncated"); idx >= 0 {
			body = body[:idx]
		}
		return strings.TrimSpace(body), truncated, nil
	}
	full, err := safeWorkspacePath(deps, path)
	if err != nil {
		return "", false, err
	}
	f, err := os.Open(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, fmt.Errorf("%s not found\nNext: call list_files on %s or the workspace root to find the correct path, then retry file_context", displayFileToolPath(deps, path), parentForToolPath(deps, path))
		}
		return "", false, err
	}
	defer f.Close()
	buf, err := io.ReadAll(io.LimitReader(f, int64(maxBytes)+1))
	if err != nil {
		return "", false, err
	}
	if looksBinary(buf) {
		displayPath := displayFileToolPath(deps, path)
		return "", false, recoverableFileToolError(deps, "file_context", path, fmt.Errorf("%s appears to be binary (contains null bytes); use shell with file/xxd/base64 to inspect", displayPath))
	}
	truncated := len(buf) > maxBytes
	if truncated {
		cut := textutil.AlignBackward(string(buf), maxBytes)
		buf = buf[:cut]
	}
	return strings.TrimSpace(string(buf)), truncated, nil
}

func buildFileContextResponse(path, content string, truncated bool, query string, contextLines, maxMatches int) fileContextResponse {
	lines := splitLines(content)
	resp := fileContextResponse{
		Path:      path,
		Bytes:     len(content),
		Truncated: truncated,
		Lines:     len(lines),
		Query:     strings.TrimSpace(query),
	}
	if len(lines) == 0 {
		return resp
	}
	headCount := min(contextLines, len(lines))
	resp.Head = make([]fileContextLine, 0, headCount)
	for i := 0; i < headCount; i++ {
		resp.Head = append(resp.Head, fileContextLine{Line: i + 1, Text: previewLine(lines[i])})
	}
	if truncated || len(lines) > headCount*2 {
		tailCount := min(contextLines, len(lines))
		start := len(lines) - tailCount
		if start < headCount {
			start = headCount
		}
		if start < len(lines) {
			resp.Tail = make([]fileContextLine, 0, len(lines)-start)
			for i := start; i < len(lines); i++ {
				resp.Tail = append(resp.Tail, fileContextLine{Line: i + 1, Text: previewLine(lines[i])})
			}
		}
	}
	if terms := fileContextTerms(query); len(terms) > 0 {
		resp.Matches = fileContextMatches(lines, terms, contextLines, maxMatches)
	}
	return resp
}

func previewLine(line string) string {
	line = strings.TrimRight(line, "\r")
	if line == "" {
		return ""
	}
	return textutil.Preview(line, maxFileContextLinePreviewLen)
}

func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	return strings.Split(content, "\n")
}

func fileContextTerms(query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil
	}
	var raw []string
	var cur strings.Builder
	flush := func() {
		t := strings.TrimSpace(cur.String())
		cur.Reset()
		if len(t) >= 2 {
			raw = append(raw, t)
		}
	}
	for _, r := range query {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	out := raw[:0]
	seen := map[string]bool{}
	for _, term := range raw {
		if seen[term] {
			continue
		}
		seen[term] = true
		out = append(out, term)
	}
	return out
}

type fileContextHit struct {
	line  int
	score float64
}

func fileContextMatches(lines []string, terms []string, contextLines, maxMatches int) []fileContextSpan {
	if len(lines) == 0 || len(terms) == 0 || maxMatches <= 0 {
		return nil
	}
	lowerQuery := strings.Join(terms, " ")
	var hits []fileContextHit
	for idx, line := range lines {
		lower := strings.ToLower(line)
		score := 0.0
		if strings.Contains(lower, lowerQuery) {
			score += 2
		}
		for _, term := range terms {
			if strings.Contains(lower, term) {
				score++
			}
		}
		if score > 0 {
			hits = append(hits, fileContextHit{line: idx, score: score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].line < hits[j].line
	})
	if len(hits) > maxMatches {
		hits = hits[:maxMatches]
	}
	var out []fileContextSpan
	seen := map[int]bool{}
	for _, hit := range hits {
		start := hit.line - contextLines
		if start < 0 {
			start = 0
		}
		end := hit.line + contextLines + 1
		if end > len(lines) {
			end = len(lines)
		}
		if seen[start] && seen[end] {
			continue
		}
		seen[start] = true
		seen[end] = true
		out = append(out, fileContextSpan{
			StartLine: start + 1,
			EndLine:   end,
			HitLine:   hit.line + 1,
			Text:      formatContextSpan(lines, start, end),
		})
	}
	return out
}

func formatContextSpan(lines []string, start, end int) string {
	var b strings.Builder
	for i := start; i < end; i++ {
		if i > start {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%d: %s", i+1, previewLine(lines[i]))
	}
	return b.String()
}

func goFileContextSymbols(ctx context.Context, deps BuiltinDeps, path, content string, maxMatches int) []fileContextSymbol {
	if maxMatches <= 0 {
		return nil
	}
	fo := fileOps(deps)
	if fo != nil {
		// No special container-specific work is needed; the symbol scan
		// uses the workspace path and the contents we already read.
		_ = fo
	}
	workspace := strings.TrimSpace(deps.HostWorkspaceDir)
	if workspace == "" {
		return nil
	}
	full, err := safeWorkspacePath(deps, path)
	if err != nil {
		return nil
	}
	info, err := os.Stat(full)
	if err != nil {
		return nil
	}
	ignore, _ := workspaceignore.LoadGitignore(workspace)
	scan, err := gosymbols.ScanWorkspace(ctx, workspace, full, info, ignore, gosymbols.ScanOptions{
		IncludeTests:         true,
		MaxGoFiles:           1,
		MaxPackages:          1,
		MaxSymbolsPerPackage: maxMatches,
		MaxSymbols:           maxMatches,
	})
	if err != nil || len(scan.Records) == 0 {
		return nil
	}
	symbols := make([]fileContextSymbol, 0, min(maxMatches, len(scan.Records)))
	for _, rec := range scan.Records {
		if filepath.Clean(rec.RelPath) != filepath.Clean(path) {
			continue
		}
		symbols = append(symbols, fileContextSymbol{
			Name:      rec.Name,
			Kind:      rec.Kind,
			Line:      rec.Line,
			Signature: rec.Signature,
		})
		if len(symbols) >= maxMatches {
			break
		}
	}
	return symbols
}
