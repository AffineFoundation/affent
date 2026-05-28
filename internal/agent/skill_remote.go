package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

const skillURLFetchTimeout = 20 * time.Second

type RuntimeSkillURLOptions struct {
	Name          string
	Description   string
	Triggers      []string
	RequiredTools []string
}

type skillURLFetchFunc func(ctx context.Context, rawURL string, maxBytes int) ([]byte, error)

func ProposeRuntimeSkillFromURL(ctx context.Context, root, rawURL string, opts RuntimeSkillURLOptions) (RuntimeSkillProposal, error) {
	return proposeRuntimeSkillFromURL(ctx, root, rawURL, opts, fetchRuntimeSkillURL)
}

func proposeRuntimeSkillFromURL(ctx context.Context, root, rawURL string, opts RuntimeSkillURLOptions, fetch skillURLFetchFunc) (RuntimeSkillProposal, error) {
	target, err := parseRuntimeSkillGitHubURL(rawURL)
	if err != nil {
		return RuntimeSkillProposal{}, err
	}
	bodyRaw, err := fetch(ctx, target.BodyURL, maxRuntimeSkillBodyBytes)
	if err != nil {
		return RuntimeSkillProposal{}, fmt.Errorf("fetch SKILL.md: %w", err)
	}
	body := strings.TrimSpace(string(bodyRaw))
	if body == "" {
		return RuntimeSkillProposal{}, fmt.Errorf("remote SKILL.md is empty")
	}
	frontmatter := parseSkillMarkdownFrontmatter(body)
	manifest := runtimeSkillManifest{}
	if target.ManifestURL != "" {
		if manifestRaw, err := fetch(ctx, target.ManifestURL, maxRuntimeSkillManifestBytes); err == nil && len(strings.TrimSpace(string(manifestRaw))) > 0 {
			if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
				return RuntimeSkillProposal{}, fmt.Errorf("parse remote skill.json: %w", err)
			}
		}
	}
	skill := Skill{
		Name:           firstNonEmpty(opts.Name, manifest.Name, frontmatter.Name, inferRuntimeSkillNameFromBody(body), target.DefaultName),
		Description:    firstNonEmpty(opts.Description, manifest.Description, frontmatter.Description, "Runtime skill imported from URL."),
		Source:         target.Source,
		Body:           body,
		AutoActivation: manifest.AutoActivation,
		RequiredTools:  append([]string(nil), manifest.RequiredTools...),
	}
	if len(opts.Triggers) > 0 {
		skill.AutoActivation = SkillAutoActivation{Any: opts.Triggers}
	}
	if len(opts.RequiredTools) > 0 {
		skill.RequiredTools = append([]string(nil), opts.RequiredTools...)
	}
	return ProposeRuntimeSkill(root, skill)
}

type runtimeSkillURLTarget struct {
	BodyURL     string
	ManifestURL string
	Source      string
	DefaultName string
}

func parseRuntimeSkillGitHubURL(raw string) (runtimeSkillURLTarget, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return runtimeSkillURLTarget{}, fmt.Errorf("source URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return runtimeSkillURLTarget{}, fmt.Errorf("parse source URL: %w", err)
	}
	if u.Scheme != "https" {
		return runtimeSkillURLTarget{}, fmt.Errorf("skill URL must use https")
	}
	host := strings.ToLower(u.Hostname())
	parts := compactPathParts(u.Path)
	switch host {
	case "raw.githubusercontent.com":
		return parseRawGitHubSkillURL(raw, parts)
	case "github.com", "www.github.com":
		return parseGitHubSkillURL(raw, parts)
	default:
		return runtimeSkillURLTarget{}, fmt.Errorf("unsupported skill URL host %q; supported hosts: github.com, raw.githubusercontent.com", host)
	}
}

func parseRawGitHubSkillURL(raw string, parts []string) (runtimeSkillURLTarget, error) {
	if len(parts) < 4 {
		return runtimeSkillURLTarget{}, fmt.Errorf("raw GitHub URL must include owner/repo/ref/SKILL.md")
	}
	if parts[len(parts)-1] != "SKILL.md" {
		return runtimeSkillURLTarget{}, fmt.Errorf("raw GitHub URL must point to SKILL.md")
	}
	owner, repo, ref := parts[0], parts[1], parts[2]
	dirParts := append([]string(nil), parts[3:len(parts)-1]...)
	manifestParts := append(append([]string(nil), dirParts...), "skill.json")
	return runtimeSkillURLTarget{
		BodyURL:     rawGitHubURL(owner, repo, ref, append(append([]string(nil), dirParts...), "SKILL.md")...),
		ManifestURL: rawGitHubURL(owner, repo, ref, manifestParts...),
		Source:      raw,
		DefaultName: defaultSkillNameFromPath(strings.Join(dirParts, "/")),
	}, nil
}

func parseGitHubSkillURL(raw string, parts []string) (runtimeSkillURLTarget, error) {
	if len(parts) < 4 {
		return runtimeSkillURLTarget{}, fmt.Errorf("GitHub URL must include owner/repo/tree-or-blob/ref")
	}
	owner, repo, kind, ref := parts[0], parts[1], parts[2], parts[3]
	if kind != "tree" && kind != "blob" {
		return runtimeSkillURLTarget{}, fmt.Errorf("GitHub URL must use /tree/ or /blob/")
	}
	targetParts := parts[4:]
	if kind == "blob" {
		if len(targetParts) == 0 || targetParts[len(targetParts)-1] != "SKILL.md" {
			return runtimeSkillURLTarget{}, fmt.Errorf("GitHub blob URL must point to SKILL.md")
		}
		targetParts = targetParts[:len(targetParts)-1]
	}
	if len(targetParts) > 0 && targetParts[len(targetParts)-1] == "SKILL.md" {
		targetParts = targetParts[:len(targetParts)-1]
	}
	return runtimeSkillURLTarget{
		BodyURL:     rawGitHubURL(owner, repo, ref, append(append([]string(nil), targetParts...), "SKILL.md")...),
		ManifestURL: rawGitHubURL(owner, repo, ref, append(append([]string(nil), targetParts...), "skill.json")...),
		Source:      raw,
		DefaultName: defaultSkillNameFromPath(strings.Join(targetParts, "/")),
	}, nil
}

func rawGitHubURL(owner, repo, ref string, pathParts ...string) string {
	parts := []string{"https://raw.githubusercontent.com", owner, repo, ref}
	parts = append(parts, pathParts...)
	return strings.Join(parts, "/")
}

func compactPathParts(rawPath string) []string {
	cleaned := path.Clean("/" + rawPath)
	if cleaned == "/" || cleaned == "." {
		return nil
	}
	raw := strings.Split(strings.TrimPrefix(cleaned, "/"), "/")
	out := make([]string, 0, len(raw))
	for _, part := range raw {
		if part = strings.TrimSpace(part); part != "" && part != "." && part != ".." {
			out = append(out, part)
		}
	}
	return out
}

func defaultSkillNameFromPath(rawPath string) string {
	parts := compactPathParts(rawPath)
	if len(parts) == 0 {
		return "imported_skill"
	}
	return strings.NewReplacer(" ", "-", ".", "-", "/", "-").Replace(parts[len(parts)-1])
}

func inferRuntimeSkillNameFromBody(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "AFFENT ACTIVE SKILL:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "AFFENT ACTIVE SKILL:"))
		}
	}
	return ""
}

type skillMarkdownFrontmatter struct {
	Name        string
	Description string
}

func parseSkillMarkdownFrontmatter(body string) skillMarkdownFrontmatter {
	lines := strings.Split(body, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return skillMarkdownFrontmatter{}
	}
	var frontmatter skillMarkdownFrontmatter
	for i := 1; i < len(lines) && i <= 80; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "---" {
			return frontmatter
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "name":
			frontmatter.Name = parseSkillFrontmatterScalar(value)
		case "description":
			frontmatter.Description = parseSkillFrontmatterScalar(value)
		}
	}
	return skillMarkdownFrontmatter{}
}

func parseSkillFrontmatterScalar(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "\"") {
		if unquoted, err := strconv.Unquote(value); err == nil {
			return strings.TrimSpace(unquoted)
		}
	}
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		return strings.TrimSpace(strings.ReplaceAll(value[1:len(value)-1], "''", "'"))
	}
	if comment := strings.Index(value, " #"); comment >= 0 {
		value = value[:comment]
	}
	return strings.TrimSpace(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func fetchRuntimeSkillURL(ctx context.Context, rawURL string, maxBytes int) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, fmt.Errorf("maxBytes must be positive")
	}
	ctx, cancel := context.WithTimeout(ctx, skillURLFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s returned %s", rawURL, resp.Status)
	}
	limited := io.LimitReader(resp.Body, int64(maxBytes)+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxBytes {
		return nil, fmt.Errorf("remote file is %d+ bytes; max %d", maxBytes, maxBytes)
	}
	return raw, nil
}
