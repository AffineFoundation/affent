package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/affinefoundation/affent/internal/gosymbols"
	"github.com/affinefoundation/affent/internal/textutil"
	"github.com/affinefoundation/affent/internal/workspaceignore"
)

const (
	maxSymbolContextQueryBytes  = 1024
	maxSymbolContextPathBytes   = maxFileToolPathBytes
	defaultSymbolContextMaxHits = 12
	maxSymbolContextMaxHits     = 50
)

type symbolContextArgs struct {
	Query      string `json:"query"`
	Path       string `json:"path"`
	MaxResults int    `json:"max_results"`
}

// SymbolContextToolName is the registry name for the Go symbol lookup helper.
const SymbolContextToolName = "symbol_context"

func symbolContextTool(deps BuiltinDeps) *Tool {
	schema := json.RawMessage(fmt.Sprintf(`{
        "type": "object",
        "additionalProperties": false,
        "required": ["query"],
        "properties": {
            "query": {"type": "string", "minLength": 1, "maxLength": %d, "description": "Symbol, package, file, or declaration fragment to locate."},
            "path": {"type": "string", "maxLength": %d, "description": "Workspace-relative file or directory to search within; default workspace root."},
            "max_results": {"type": "integer", "minimum": 1, "maximum": %d, "default": %d, "description": "Maximum number of symbol hits to return."}
        }
    }`, maxSymbolContextQueryBytes, maxSymbolContextPathBytes, maxSymbolContextMaxHits, defaultSymbolContextMaxHits))
	return &Tool{
		Name:        SymbolContextToolName,
		Description: "Search workspace Go symbols and return concise declaration matches with file, line, package, and signature. Use it when you know a symbol, package, or declaration shape and want the exact definition before broader repo_search or file reads.",
		Schema:      schema,
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			p, err := decodeBuiltinToolArgs[symbolContextArgs](SymbolContextToolName, args, "query, path, max_results", "query is required; path and max_results are optional.")
			if err != nil {
				return "", err
			}
			p.Query = strings.TrimSpace(p.Query)
			if p.Query == "" {
				return "", errors.New("query is required\nNext: retry symbol_context with a symbol, package, file, or declaration fragment")
			}
			if len(p.Query) > maxSymbolContextQueryBytes {
				return "", fmt.Errorf("query is %d bytes; symbol_context supports queries up to %d bytes\nNext: shorten the query to the smallest useful symbol fragment", len(p.Query), maxSymbolContextQueryBytes)
			}
			p.Path = strings.TrimSpace(p.Path)
			if p.Path == "" {
				p.Path = "."
			}
			if len(p.Path) > maxSymbolContextPathBytes {
				return "", fmt.Errorf("path is %d bytes; symbol_context supports paths up to %d bytes\nNext: retry with a shorter workspace-relative path, or search from the workspace root", len(p.Path), maxSymbolContextPathBytes)
			}
			if p.MaxResults <= 0 {
				p.MaxResults = defaultSymbolContextMaxHits
			}
			if p.MaxResults > maxSymbolContextMaxHits {
				p.MaxResults = maxSymbolContextMaxHits
			}
			if deps.HostWorkspaceDir == "" {
				return "", errors.New("workspace is not configured; symbol_context requires HostWorkspaceDir\nNext: restart affent with a workspace root before retrying symbol_context")
			}
			root, err := safeWorkspacePath(deps, p.Path)
			if err != nil {
				return "", err
			}
			info, err := os.Stat(root)
			if err != nil {
				if os.IsNotExist(err) {
					return "", fileNotFoundToolError(deps, SymbolContextToolName, p.Path)
				}
				return "", err
			}
			wsAbs := deps.HostWorkspaceDir
			if resolved, err := filepath.EvalSymlinks(wsAbs); err == nil && resolved != "" {
				wsAbs = resolved
			}
			ignore, err := workspaceignore.LoadGitignore(wsAbs)
			if err != nil {
				return "", err
			}
			scan, err := gosymbols.ScanWorkspace(ctx, wsAbs, root, info, ignore, gosymbols.ScanOptions{
				IncludeTests:         true,
				MaxGoFiles:           160,
				MaxPackages:          64,
				MaxSymbolsPerPackage: 12,
				MaxSymbols:           600,
			})
			if err != nil {
				return "", err
			}
			hits := scan.Search(p.Query, p.MaxResults)
			if len(hits) == 0 {
				var b strings.Builder
				fmt.Fprintf(&b, "no symbol matches for %q under %s", p.Query, p.Path)
				if scan.Stats.ScannedFiles > 0 {
					fmt.Fprintf(&b, "\nscanned %d Go file(s)", scan.Stats.ScannedFiles)
				}
				if scan.Stats.SkippedFiles > 0 || scan.Stats.ParseErrors > 0 {
					fmt.Fprintf(&b, "\nskipped %d file(s), parse errors %d", scan.Stats.SkippedFiles, scan.Stats.ParseErrors)
				}
				b.WriteString("\nNext: broaden the query, use repo_search for text discovery, or narrow the path to the package you expect.")
				return b.String(), nil
			}
			var b strings.Builder
			fmt.Fprintf(&b, "found %d symbol hit(s)", len(hits))
			if scan.Stats.ScannedFiles > 0 {
				fmt.Fprintf(&b, " in %d file(s)", scan.Stats.ScannedFiles)
			}
			if scan.Stats.SkippedFiles > 0 || scan.Stats.ParseErrors > 0 {
				fmt.Fprintf(&b, " (skipped %d file(s), parse errors %d)", scan.Stats.SkippedFiles, scan.Stats.ParseErrors)
			}
			b.WriteString("\n")
			for _, hit := range hits {
				sig := strings.TrimSpace(hit.Signature)
				if sig == "" {
					sig = hit.Kind + " " + hit.Name
				}
				sig = textutil.Preview(textutil.CompactWhitespace(sig), 180)
				if hit.Line > 0 {
					if hit.PkgName != "" {
						fmt.Fprintf(&b, "%s:%d [%s] %s\n", hit.RelPath, hit.Line, hit.PkgName, sig)
					} else {
						fmt.Fprintf(&b, "%s:%d %s\n", hit.RelPath, hit.Line, sig)
					}
					continue
				}
				if hit.PkgName != "" {
					fmt.Fprintf(&b, "%s [%s] %s\n", hit.RelPath, hit.PkgName, sig)
					continue
				}
				fmt.Fprintf(&b, "%s %s\n", hit.RelPath, sig)
			}
			if scan.Stats.Truncated {
				b.WriteString("... [more symbols truncated]\n")
			}
			return strings.TrimSpace(b.String()), nil
		},
	}
}
