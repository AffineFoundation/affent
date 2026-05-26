package workspaceignore

import (
	"bufio"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Matcher applies a small, root-level .gitignore subset over
// workspace-relative paths. It is intentionally pragmatic: enough to
// keep generated files, caches, and local build outputs out of the
// default discovery paths without pulling in a full gitignore engine.
type Matcher struct {
	rules []rule
}

type rule struct {
	pattern      string
	negated      bool
	dirOnly      bool
	anchored     bool
	segments     []string
	hasSlash     bool
	originalLine string
}

// LoadGitignore reads root/.gitignore and returns a matcher. Missing
// files are not an error.
func LoadGitignore(root string) (*Matcher, error) {
	raw, err := os.Open(filepath.Join(root, ".gitignore"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer raw.Close()

	m := &Matcher{}
	sc := bufio.NewScanner(raw)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, `\#`) || strings.HasPrefix(line, `\!`) {
			line = line[1:]
		}
		negated := strings.HasPrefix(line, "!")
		if negated {
			line = strings.TrimSpace(strings.TrimPrefix(line, "!"))
		}
		if line == "" {
			continue
		}
		dirOnly := strings.HasSuffix(line, "/")
		if dirOnly {
			line = strings.TrimRight(line, "/")
		}
		anchored := strings.HasPrefix(line, "/")
		if anchored {
			line = strings.TrimPrefix(line, "/")
		}
		line = path.Clean(strings.ReplaceAll(line, "\\", "/"))
		if line == "." {
			continue
		}
		r := rule{
			pattern:      line,
			negated:      negated,
			dirOnly:      dirOnly,
			anchored:     anchored,
			segments:     strings.Split(line, "/"),
			hasSlash:     strings.Contains(line, "/"),
			originalLine: sc.Text(),
		}
		if len(r.segments) == 0 {
			continue
		}
		m.rules = append(m.rules, r)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(m.rules) == 0 {
		return nil, nil
	}
	return m, nil
}

// Ignored reports whether relPath should be skipped. relPath must be
// workspace-relative and use slash separators.
func (m *Matcher) Ignored(relPath string, isDir bool) bool {
	if m == nil || len(m.rules) == 0 {
		return false
	}
	relPath = normalizeRelPath(relPath)
	if relPath == "" || relPath == "." {
		return false
	}
	ignored := false
	for _, r := range m.rules {
		if r.matches(relPath, isDir) {
			ignored = !r.negated
		}
	}
	return ignored
}

func (r rule) matches(relPath string, isDir bool) bool {
	if relPath == "" {
		return false
	}
	if r.dirOnly {
		return matchPatternSegmentsAnywhere(r.segments, strings.Split(relPath, "/"), true)
	}
	if r.anchored {
		return matchPatternSegmentsFrom(r.segments, strings.Split(relPath, "/"), 0, 0)
	}
	if r.hasSlash {
		return matchPatternSegmentsAnywhere(r.segments, strings.Split(relPath, "/"), false)
	}
	parts := strings.Split(relPath, "/")
	for _, part := range parts {
		if ok, _ := path.Match(r.pattern, part); ok {
			return true
		}
	}
	return false
}

func matchPatternSegments(pattern, pathSegs []string) bool {
	return matchPatternSegmentsFrom(pattern, pathSegs, 0, 0)
}

func matchPatternSegmentsAnywhere(pattern, pathSegs []string, allowPrefix bool) bool {
	for start := 0; start <= len(pathSegs); start++ {
		if allowPrefix {
			if matchPatternSegmentsPrefixFrom(pattern, pathSegs, 0, start) {
				return true
			}
			continue
		}
		if matchPatternSegmentsFrom(pattern, pathSegs, 0, start) {
			return true
		}
	}
	return false
}

func matchPatternSegmentsFrom(pattern, pathSegs []string, pi, si int) bool {
	for pi < len(pattern) {
		if pattern[pi] == "**" {
			for skip := si; skip <= len(pathSegs); skip++ {
				if matchPatternSegmentsFrom(pattern, pathSegs, pi+1, skip) {
					return true
				}
			}
			return false
		}
		if si >= len(pathSegs) {
			return false
		}
		ok, err := path.Match(pattern[pi], pathSegs[si])
		if err != nil || !ok {
			return false
		}
		pi++
		si++
	}
	return si == len(pathSegs)
}

func matchPatternSegmentsPrefixFrom(pattern, pathSegs []string, pi, si int) bool {
	for pi < len(pattern) {
		if pattern[pi] == "**" {
			for skip := si; skip <= len(pathSegs); skip++ {
				if matchPatternSegmentsPrefixFrom(pattern, pathSegs, pi+1, skip) {
					return true
				}
			}
			return false
		}
		if si >= len(pathSegs) {
			return false
		}
		ok, err := path.Match(pattern[pi], pathSegs[si])
		if err != nil || !ok {
			return false
		}
		pi++
		si++
	}
	return true
}

func normalizeRelPath(relPath string) string {
	relPath = strings.TrimSpace(relPath)
	relPath = strings.TrimPrefix(relPath, "./")
	relPath = strings.Trim(relPath, "/")
	return strings.ReplaceAll(relPath, "\\", "/")
}
