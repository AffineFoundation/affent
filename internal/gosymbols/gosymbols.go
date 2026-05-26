package gosymbols

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/affinefoundation/affent/internal/textutil"
	"github.com/affinefoundation/affent/internal/workspaceignore"
)

const (
	defaultMaxGoFiles           = 128
	defaultMaxPackages          = 48
	defaultMaxSymbolsPerPackage = 8
	defaultMaxSymbols           = 512
	maxGoFileBytes              = 1 << 20
)

// ScanOptions controls the breadth of a workspace symbol scan.
type ScanOptions struct {
	IncludeTests         bool
	MaxGoFiles           int
	MaxPackages          int
	MaxSymbolsPerPackage int
	MaxSymbols           int
}

// SymbolRecord is one top-level Go declaration discovered while
// scanning a workspace.
type SymbolRecord struct {
	RelPath   string
	RelDir    string
	PkgName   string
	File      string
	Name      string
	Kind      string
	Signature string
	Line      int
	IsTest    bool
}

// PackageHint is the compact package summary used by project context.
type PackageHint struct {
	RelDir  string
	PkgName string
	Files   []string
	Symbols []string
}

// ScanStats reports how much of the workspace was visited.
type ScanStats struct {
	ScannedFiles int
	SkippedFiles int
	ParseErrors  int
	Truncated    bool
}

// ScanResult bundles the raw symbol records, the package summaries,
// and scan accounting.
type ScanResult struct {
	Records  []SymbolRecord
	Packages []PackageHint
	Stats    ScanStats
}

// SearchHit is a compact symbol lookup hit.
type SearchHit struct {
	RelPath   string
	Line      int
	PkgName   string
	Name      string
	Kind      string
	Signature string
}

// Search finds likely symbol declarations matching query within a
// previously scanned workspace.
func (r ScanResult) Search(query string, maxHits int) []SearchHit {
	if maxHits <= 0 {
		maxHits = 10
	}
	terms := searchTerms(query)
	if len(terms) == 0 {
		return nil
	}
	type scoredHit struct {
		hit   SearchHit
		score int
	}
	var hits []scoredHit
	for _, rec := range r.Records {
		score := scoreRecord(rec, query, terms)
		if score <= 0 {
			continue
		}
		hits = append(hits, scoredHit{
			hit: SearchHit{
				RelPath:   rec.RelPath,
				Line:      rec.Line,
				PkgName:   rec.PkgName,
				Name:      rec.Name,
				Kind:      rec.Kind,
				Signature: rec.Signature,
			},
			score: score,
		})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		if hits[i].hit.RelPath != hits[j].hit.RelPath {
			return hits[i].hit.RelPath < hits[j].hit.RelPath
		}
		if hits[i].hit.Line != hits[j].hit.Line {
			return hits[i].hit.Line < hits[j].hit.Line
		}
		if hits[i].hit.Name != hits[j].hit.Name {
			return hits[i].hit.Name < hits[j].hit.Name
		}
		return hits[i].hit.Kind < hits[j].hit.Kind
	})
	if len(hits) > maxHits {
		hits = hits[:maxHits]
	}
	out := make([]SearchHit, len(hits))
	for i, hit := range hits {
		out[i] = hit.hit
	}
	return out
}

// ScanWorkspace walks the workspace root and returns the discovered Go
// symbols plus a compact package summary. The scan respects the root
// .gitignore matcher and caps the scan to keep the runtime bounded.
func ScanWorkspace(ctx context.Context, workspaceAbs, scopeAbs string, scopeInfo os.FileInfo, ignore *workspaceignore.Matcher, opts ScanOptions) (ScanResult, error) {
	if workspaceAbs == "" || scopeAbs == "" || scopeInfo == nil {
		return ScanResult{}, nil
	}
	opts = normalizeScanOptions(opts)
	if resolved, err := filepath.EvalSymlinks(workspaceAbs); err == nil && resolved != "" {
		workspaceAbs = resolved
	}
	builder := newWorkspaceSymbolBuilder(workspaceAbs, ignore, opts)
	if scopeInfo.IsDir() {
		if err := builder.walkDir(ctx, scopeAbs); err != nil && err != filepath.SkipAll {
			return ScanResult{}, err
		}
	} else {
		if err := builder.scanFile(ctx, scopeAbs); err != nil && err != filepath.SkipAll {
			return ScanResult{}, err
		}
	}
	return builder.finish(), nil
}

type workspaceSymbolBuilder struct {
	workspaceAbs string
	ignore       *workspaceignore.Matcher
	opts         ScanOptions
	stats        ScanStats
	records      []SymbolRecord
	pkgs         map[string]*packageState
}

type packageState struct {
	relDir string
	name   string
	files  []string
	syms   map[string]struct{}
}

func newWorkspaceSymbolBuilder(workspaceAbs string, ignore *workspaceignore.Matcher, opts ScanOptions) *workspaceSymbolBuilder {
	return &workspaceSymbolBuilder{
		workspaceAbs: workspaceAbs,
		ignore:       ignore,
		opts:         opts,
		pkgs:         map[string]*packageState{},
	}
}

func normalizeScanOptions(opts ScanOptions) ScanOptions {
	if opts.MaxGoFiles <= 0 {
		opts.MaxGoFiles = defaultMaxGoFiles
	}
	if opts.MaxPackages <= 0 {
		opts.MaxPackages = defaultMaxPackages
	}
	if opts.MaxSymbolsPerPackage <= 0 {
		opts.MaxSymbolsPerPackage = defaultMaxSymbolsPerPackage
	}
	if opts.MaxSymbols <= 0 {
		opts.MaxSymbols = defaultMaxSymbols
	}
	return opts
}

func (b *workspaceSymbolBuilder) finish() ScanResult {
	var packages []PackageHint
	for _, state := range b.pkgs {
		if len(state.syms) == 0 {
			continue
		}
		syms := make([]string, 0, len(state.syms))
		for sym := range state.syms {
			syms = append(syms, sym)
		}
		sort.Strings(syms)
		files := append([]string(nil), state.files...)
		sort.Strings(files)
		packages = append(packages, PackageHint{
			RelDir:  state.relDir,
			PkgName: state.name,
			Files:   files,
			Symbols: syms,
		})
	}
	sort.Slice(packages, func(i, j int) bool {
		if packages[i].RelDir != packages[j].RelDir {
			return packages[i].RelDir < packages[j].RelDir
		}
		return packages[i].PkgName < packages[j].PkgName
	})
	sort.SliceStable(b.records, func(i, j int) bool {
		if b.records[i].RelPath != b.records[j].RelPath {
			return b.records[i].RelPath < b.records[j].RelPath
		}
		if b.records[i].Line != b.records[j].Line {
			return b.records[i].Line < b.records[j].Line
		}
		if b.records[i].Name != b.records[j].Name {
			return b.records[i].Name < b.records[j].Name
		}
		return b.records[i].Kind < b.records[j].Kind
	})
	return ScanResult{
		Records:  b.records,
		Packages: packages,
		Stats:    b.stats,
	}
}

func (b *workspaceSymbolBuilder) walkDir(ctx context.Context, scopeAbs string) error {
	return filepath.WalkDir(scopeAbs, func(full string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		name := d.Name()
		if d.IsDir() {
			if full != scopeAbs && shouldSkipSymbolDir(name) {
				b.stats.SkippedFiles++
				return filepath.SkipDir
			}
			relDir, relErr := filepath.Rel(b.workspaceAbs, full)
			if relErr == nil {
				relDir = filepath.ToSlash(relDir)
				if relDir != "." && b.ignore != nil && b.ignore.Ignored(relDir, true) {
					b.stats.SkippedFiles++
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") {
			return nil
		}
		if !b.opts.IncludeTests && strings.HasSuffix(name, "_test.go") {
			b.stats.SkippedFiles++
			return nil
		}
		if b.stats.ScannedFiles >= b.opts.MaxGoFiles || len(b.records) >= b.opts.MaxSymbols {
			b.stats.Truncated = true
			return filepath.SkipAll
		}
		return b.scanFile(ctx, full)
	})
}

func (b *workspaceSymbolBuilder) scanFile(ctx context.Context, full string) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	fi, err := os.Stat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if fi.IsDir() {
		return nil
	}
	if fi.Size() > maxGoFileBytes {
		b.stats.SkippedFiles++
		return nil
	}
	relPath, err := filepath.Rel(b.workspaceAbs, full)
	if err != nil {
		return nil
	}
	relPath = filepath.ToSlash(relPath)
	if !b.opts.IncludeTests && strings.HasSuffix(filepath.Base(relPath), "_test.go") {
		b.stats.SkippedFiles++
		return nil
	}
	if b.ignore != nil && b.ignore.Ignored(relPath, false) {
		b.stats.SkippedFiles++
		return nil
	}
	b.stats.ScannedFiles++
	if b.stats.ScannedFiles > b.opts.MaxGoFiles || len(b.records) >= b.opts.MaxSymbols {
		b.stats.Truncated = true
		return filepath.SkipAll
	}
	fset := token.NewFileSet()
	file, perr := parser.ParseFile(fset, full, nil, parser.SkipObjectResolution)
	if perr != nil || file == nil {
		b.stats.ParseErrors++
		return nil
	}
	relDir := filepath.ToSlash(filepath.Dir(relPath))
	if relDir == "." {
		relDir = "."
	}
	pkg := file.Name.Name
	state := b.pkgState(relDir, pkg)
	isTest := strings.HasSuffix(filepath.Base(relPath), "_test.go")
	if len(state.files) < 3 {
		state.files = appendUniqueSorted(state.files, filepath.Base(relPath))
	}
	for _, decl := range file.Decls {
		if len(b.records) >= b.opts.MaxSymbols {
			b.stats.Truncated = true
			return filepath.SkipAll
		}
		switch d := decl.(type) {
		case *ast.FuncDecl:
			b.addSymbol(state, SymbolRecord{
				RelPath:   relPath,
				RelDir:    relDir,
				PkgName:   pkg,
				File:      filepath.Base(relPath),
				Name:      d.Name.Name,
				Kind:      "func",
				Signature: declSummary(fset, d),
				Line:      fset.Position(d.Pos()).Line,
				IsTest:    isTest,
			})
			if len(b.records) >= b.opts.MaxSymbols {
				b.stats.Truncated = true
				return filepath.SkipAll
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					b.addSymbol(state, SymbolRecord{
						RelPath:   relPath,
						RelDir:    relDir,
						PkgName:   pkg,
						File:      filepath.Base(relPath),
						Name:      s.Name.Name,
						Kind:      "type",
						Signature: declSummary(fset, d),
						Line:      fset.Position(s.Pos()).Line,
						IsTest:    isTest,
					})
					if len(b.records) >= b.opts.MaxSymbols {
						b.stats.Truncated = true
						return filepath.SkipAll
					}
				case *ast.ValueSpec:
					kind := strings.ToLower(d.Tok.String())
					for _, name := range s.Names {
						b.addSymbol(state, SymbolRecord{
							RelPath:   relPath,
							RelDir:    relDir,
							PkgName:   pkg,
							File:      filepath.Base(relPath),
							Name:      name.Name,
							Kind:      kind,
							Signature: declSummary(fset, d),
							Line:      fset.Position(name.Pos()).Line,
							IsTest:    isTest,
						})
						if len(b.records) >= b.opts.MaxSymbols {
							b.stats.Truncated = true
							return filepath.SkipAll
						}
					}
				}
			}
		}
	}
	return nil
}

func (b *workspaceSymbolBuilder) pkgState(relDir, pkg string) *packageState {
	key := relDir + "\x00" + pkg
	state := b.pkgs[key]
	if state != nil {
		return state
	}
	if len(b.pkgs) >= b.opts.MaxPackages {
		b.stats.Truncated = true
		return &packageState{relDir: relDir, name: pkg, syms: map[string]struct{}{}}
	}
	state = &packageState{relDir: relDir, name: pkg, syms: map[string]struct{}{}}
	b.pkgs[key] = state
	return state
}

func (b *workspaceSymbolBuilder) addSymbol(state *packageState, rec SymbolRecord) {
	if state == nil || rec.Name == "" {
		return
	}
	if strings.HasPrefix(rec.Name, "_") {
		return
	}
	if !b.opts.IncludeTests && rec.IsTest {
		return
	}
	if len(state.syms) >= b.opts.MaxSymbolsPerPackage {
		return
	}
	if _, ok := state.syms[rec.Name]; ok {
		return
	}
	state.syms[rec.Name] = struct{}{}
	b.records = append(b.records, rec)
}

func declSummary(fset *token.FileSet, node ast.Node) string {
	if node == nil {
		return ""
	}
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, node); err != nil {
		return ""
	}
	summary := textutil.CompactWhitespace(buf.String())
	summary = strings.TrimSpace(summary)
	return textutil.Preview(summary, 180)
}

func searchTerms(query string) []searchTerm {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil
	}
	seen := map[string]struct{}{}
	var out []searchTerm
	for _, raw := range strings.Fields(query) {
		if len(raw) < 2 {
			continue
		}
		if _, ok := seen[raw]; ok {
			continue
		}
		seen[raw] = struct{}{}
		out = append(out, searchTerm{Raw: raw, Compact: compactSearchText(raw)})
	}
	return out
}

type searchTerm struct {
	Raw     string
	Compact string
}

func scoreRecord(rec SymbolRecord, query string, terms []searchTerm) int {
	name := strings.ToLower(rec.Name)
	kind := strings.ToLower(rec.Kind)
	pkg := strings.ToLower(rec.PkgName)
	relPath := strings.ToLower(rec.RelPath)
	file := strings.ToLower(rec.File)
	sig := strings.ToLower(rec.Signature)
	compactName := compactSearchText(name)
	compactPath := compactSearchText(relPath)
	compactFile := compactSearchText(file)
	compactPkg := compactSearchText(pkg)
	compactSig := compactSearchText(sig)
	compactQuery := compactSearchText(query)

	score := 0
	switch {
	case compactQuery != "" && compactQuery == compactName:
		score += 120
	case compactQuery != "" && strings.Contains(compactName, compactQuery):
		score += 100
	case compactQuery != "" && strings.Contains(compactPath, compactQuery):
		score += 80
	case compactQuery != "" && strings.Contains(compactSig, compactQuery):
		score += 60
	}
	if query != "" && strings.Contains(name, query) {
		score += 40
	}
	if query != "" && strings.Contains(relPath, query) {
		score += 30
	}
	if query != "" && strings.Contains(sig, query) {
		score += 20
	}
	for _, term := range terms {
		matched := false
		if strings.Contains(compactName, term.Compact) {
			score += 50
			matched = true
		}
		if strings.Contains(compactPath, term.Compact) {
			score += 35
			matched = true
		}
		if strings.Contains(compactFile, term.Compact) {
			score += 30
			matched = true
		}
		if strings.Contains(compactPkg, term.Compact) {
			score += 20
			matched = true
		}
		if strings.Contains(compactSig, term.Compact) {
			score += 15
			matched = true
		}
		if strings.Contains(kind, term.Raw) {
			score += 5
			matched = true
		}
		if !matched && strings.Contains(name, term.Raw) {
			score += 10
		}
	}
	if rec.IsTest {
		score -= 10
	}
	return score
}

func compactSearchText(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func shouldSkipSymbolDir(name string) bool {
	switch name {
	case ".git", "node_modules", "dist", "build", "coverage", "vendor":
		return true
	default:
		return strings.HasPrefix(name, ".")
	}
}

func appendUniqueSorted(existing []string, next string) []string {
	for _, cur := range existing {
		if cur == next {
			return existing
		}
	}
	return append(existing, next)
}
