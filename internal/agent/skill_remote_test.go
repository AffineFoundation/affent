package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestProposeRuntimeSkillFromGitHubTreeURL(t *testing.T) {
	root := t.TempDir()
	source := "https://github.com/openai/skills/tree/main/skills/.curated/playwright"
	fetches := map[string]string{
		"https://raw.githubusercontent.com/openai/skills/main/skills/.curated/playwright/SKILL.md": "AFFENT ACTIVE SKILL: playwright_web_verification\nUse Playwright to verify web changes.",
		"https://raw.githubusercontent.com/openai/skills/main/skills/.curated/playwright/skill.json": `{
  "name": "playwright_web_verification",
  "description": "Verify web UI changes in a browser.",
  "auto_activation": {"any": ["playwright", "browser verify"]},
  "required_tools": ["browser_navigate"]
}`,
	}
	proposal, err := proposeRuntimeSkillFromURL(context.Background(), root, source, RuntimeSkillURLOptions{}, fakeSkillURLFetcher(fetches))
	if err != nil {
		t.Fatalf("propose from GitHub tree URL: %v", err)
	}
	if proposal.Name != "playwright_web_verification" {
		t.Fatalf("proposal.Name = %q", proposal.Name)
	}
	if proposal.Source != source {
		t.Fatalf("proposal.Source = %q, want original URL", proposal.Source)
	}
	if !strings.Contains(proposal.Body, "AFFENT ACTIVE SKILL: playwright_web_verification") {
		t.Fatalf("proposal body = %q", proposal.Body)
	}
	if len(proposal.AutoActivation.Any) != 2 || proposal.AutoActivation.Any[0] != "playwright" {
		t.Fatalf("proposal activation = %+v", proposal.AutoActivation)
	}
	if len(proposal.RequiredTools) != 1 || proposal.RequiredTools[0] != "browser_navigate" {
		t.Fatalf("proposal required tools = %+v", proposal.RequiredTools)
	}
}

func TestProposeRuntimeSkillFromGitHubTreeURLUsesFrontmatter(t *testing.T) {
	root := t.TempDir()
	source := "https://github.com/openai/skills/tree/main/skills/.curated/playwright"
	fetches := map[string]string{
		"https://raw.githubusercontent.com/openai/skills/main/skills/.curated/playwright/SKILL.md": `---
name: "playwright"
description: "Use when the task needs browser automation or web UI verification."
---
# Playwright

Use Playwright to inspect, interact with, and verify web pages.`,
	}
	proposal, err := proposeRuntimeSkillFromURL(context.Background(), root, source, RuntimeSkillURLOptions{}, fakeSkillURLFetcher(fetches))
	if err != nil {
		t.Fatalf("propose from GitHub tree URL: %v", err)
	}
	if proposal.Name != "playwright" {
		t.Fatalf("proposal.Name = %q", proposal.Name)
	}
	if proposal.Description != "Use when the task needs browser automation or web UI verification." {
		t.Fatalf("proposal.Description = %q", proposal.Description)
	}
}

func TestProposeRuntimeSkillManifestOverridesFrontmatter(t *testing.T) {
	root := t.TempDir()
	source := "https://github.com/example/skills/tree/main/review"
	fetches := map[string]string{
		"https://raw.githubusercontent.com/example/skills/main/review/SKILL.md": `---
name: "frontmatter_review"
description: "Frontmatter description."
---
AFFENT ACTIVE SKILL: body_review
Use this review workflow.`,
		"https://raw.githubusercontent.com/example/skills/main/review/skill.json": `{
  "name": "manifest_review",
  "description": "Manifest description."
}`,
	}
	proposal, err := proposeRuntimeSkillFromURL(context.Background(), root, source, RuntimeSkillURLOptions{}, fakeSkillURLFetcher(fetches))
	if err != nil {
		t.Fatalf("propose from GitHub tree URL: %v", err)
	}
	if proposal.Name != "manifest_review" {
		t.Fatalf("proposal.Name = %q", proposal.Name)
	}
	if proposal.Description != "Manifest description." {
		t.Fatalf("proposal.Description = %q", proposal.Description)
	}
}

func TestParseSkillMarkdownFrontmatterRequiresClosingFence(t *testing.T) {
	frontmatter := parseSkillMarkdownFrontmatter(`---
name: "unfinished"
description: "This should be ignored."
# Missing closing fence.`)
	if frontmatter.Name != "" || frontmatter.Description != "" {
		t.Fatalf("frontmatter = %+v, want empty", frontmatter)
	}
}

func TestProposeRuntimeSkillFromRawURLUsesHeaderFallback(t *testing.T) {
	root := t.TempDir()
	source := "https://raw.githubusercontent.com/example/skills/abc123/review/SKILL.md"
	fetches := map[string]string{
		source: "AFFENT ACTIVE SKILL: review_helper\nUse this reviewed workflow.",
	}
	proposal, err := proposeRuntimeSkillFromURL(context.Background(), root, source, RuntimeSkillURLOptions{
		Triggers: []string{"review helper"},
	}, fakeSkillURLFetcher(fetches))
	if err != nil {
		t.Fatalf("propose from raw URL: %v", err)
	}
	if proposal.Name != "review_helper" {
		t.Fatalf("proposal.Name = %q", proposal.Name)
	}
	if len(proposal.AutoActivation.Any) != 1 || proposal.AutoActivation.Any[0] != "review helper" {
		t.Fatalf("proposal activation = %+v", proposal.AutoActivation)
	}
}

func TestProposeRuntimeSkillFromRootRawURL(t *testing.T) {
	root := t.TempDir()
	source := "https://raw.githubusercontent.com/example/skills/abc123/SKILL.md"
	fetches := map[string]string{
		source: "AFFENT ACTIVE SKILL: root_helper\nUse this root skill.",
	}
	proposal, err := proposeRuntimeSkillFromURL(context.Background(), root, source, RuntimeSkillURLOptions{}, fakeSkillURLFetcher(fetches))
	if err != nil {
		t.Fatalf("propose from root raw URL: %v", err)
	}
	if proposal.Name != "root_helper" {
		t.Fatalf("proposal.Name = %q", proposal.Name)
	}
}

func TestProposeRuntimeSkillFromRootBlobURL(t *testing.T) {
	root := t.TempDir()
	source := "https://github.com/example/skills/blob/abc123/SKILL.md"
	fetches := map[string]string{
		"https://raw.githubusercontent.com/example/skills/abc123/SKILL.md": "AFFENT ACTIVE SKILL: blob_root_helper\nUse this root blob skill.",
	}
	proposal, err := proposeRuntimeSkillFromURL(context.Background(), root, source, RuntimeSkillURLOptions{}, fakeSkillURLFetcher(fetches))
	if err != nil {
		t.Fatalf("propose from root blob URL: %v", err)
	}
	if proposal.Name != "blob_root_helper" {
		t.Fatalf("proposal.Name = %q", proposal.Name)
	}
	if proposal.Source != source {
		t.Fatalf("proposal.Source = %q, want original URL", proposal.Source)
	}
}

func TestParseRuntimeSkillGitHubURLRejectsUnsupportedHost(t *testing.T) {
	_, err := parseRuntimeSkillGitHubURL("https://example.com/skills/demo/SKILL.md")
	if err == nil || !strings.Contains(err.Error(), "unsupported skill URL host") {
		t.Fatalf("unsupported host error = %v", err)
	}
}

func fakeSkillURLFetcher(files map[string]string) skillURLFetchFunc {
	return func(_ context.Context, rawURL string, _ int) ([]byte, error) {
		body, ok := files[rawURL]
		if !ok {
			return nil, fmt.Errorf("missing fake URL %s", rawURL)
		}
		return []byte(body), nil
	}
}
