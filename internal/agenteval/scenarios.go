package agenteval

func goMedianScenario() BatchScenario {
	return BatchScenario{
		Name:   "coding-go-median",
		Prompt: "这个 Go 项目的测试失败。请先运行测试复现，然后修复 Median 的实现，要求保持输入 slice 不被修改，最后再次运行测试确认。只改必要文件。",
		Files: map[string]string{
			"go.mod": `module example.com/median

go 1.22
`,
			"calc/calc.go": `package calc

func Median(nums []int) float64 {
	if len(nums) == 0 {
		return 0
	}
	sorted := append([]int(nil), nums...)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return float64(sorted[mid])
	}
	return float64(sorted[mid-1]+sorted[mid]) / 2
}
`,
			"calc/calc_test.go": `package calc

import (
	"reflect"
	"testing"
)

func TestMedianOddUnsorted(t *testing.T) {
	got := Median([]int{9, 1, 5})
	if got != 5 {
		t.Fatalf("Median odd unsorted = %v, want 5", got)
	}
}

func TestMedianEvenUnsorted(t *testing.T) {
	got := Median([]int{10, 2, 4, 8})
	if got != 6 {
		t.Fatalf("Median even unsorted = %v, want 6", got)
	}
}

func TestMedianDoesNotMutateInput(t *testing.T) {
	in := []int{3, 1, 2}
	before := append([]int(nil), in...)
	_ = Median(in)
	if !reflect.DeepEqual(in, before) {
		t.Fatalf("Median mutated input: got %v want %v", in, before)
	}
}
`,
		},
		VerifyCommand:     "go test ./...",
		ExpectedSkill:     "AFFENT ACTIVE SKILL: coding_repair_workflow",
		RequiredCommands:  []string{`go test`},
		ForbiddenCommands: []string{"find /", "apt-get", "curl -sL", "| head", "|| true", "; echo \"EXIT:$?\"", "pip install"},
		ProtectedFiles:    []string{"calc/calc_test.go"},
		MaxTurns:          10,
	}
}

func goConfigPrecedenceScenario() BatchScenario {
	return BatchScenario{
		Name:   "coding-go-config-precedence",
		Prompt: "这个 Go 项目的配置合并测试失败。请先运行测试复现，然后修复 Resolve 的优先级：CLI > env > config file > defaults。不要修改测试，最后再次运行测试确认。只改必要文件。",
		Files: map[string]string{
			"go.mod": `module example.com/confmerge

go 1.22
`,
			"config/config.go": `package config

import (
	"fmt"
	"strconv"
)

type Options struct {
	Endpoint string
	Retries  int
	Verbose  bool
}

type Sources struct {
	File Options
	Env  map[string]string
	CLI  map[string]string
}

func Resolve(src Sources) (Options, error) {
	out := Options{
		Endpoint: "http://localhost:8080",
		Retries:  3,
	}
	applyOptions(&out, src.File)
	if err := applyMap(&out, src.CLI); err != nil {
		return Options{}, err
	}
	if err := applyMap(&out, src.Env); err != nil {
		return Options{}, err
	}
	return out, nil
}

func applyOptions(out *Options, in Options) {
	if in.Endpoint != "" {
		out.Endpoint = in.Endpoint
	}
	if in.Retries != 0 {
		out.Retries = in.Retries
	}
	if in.Verbose {
		out.Verbose = true
	}
}

func applyMap(out *Options, values map[string]string) error {
	for key, value := range values {
		if value == "" {
			continue
		}
		switch key {
		case "endpoint":
			out.Endpoint = value
		case "retries":
			n, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("retries=%q: %w", value, err)
			}
			out.Retries = n
		case "verbose":
			b, err := strconv.ParseBool(value)
			if err != nil {
				return fmt.Errorf("verbose=%q: %w", value, err)
			}
			out.Verbose = b
		}
	}
	return nil
}
`,
			"config/config_test.go": `package config

import "testing"

func TestResolvePrecedenceCLIOverEnvOverFileOverDefaults(t *testing.T) {
	got, err := Resolve(Sources{
		File: Options{
			Endpoint: "https://file.example",
			Retries:  1,
		},
		Env: map[string]string{
			"endpoint": "https://env.example",
			"retries":  "2",
			"verbose":  "false",
		},
		CLI: map[string]string{
			"endpoint": "https://cli.example",
			"verbose":  "true",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Endpoint != "https://cli.example" {
		t.Fatalf("Endpoint = %q, want CLI value", got.Endpoint)
	}
	if got.Retries != 2 {
		t.Fatalf("Retries = %d, want env value", got.Retries)
	}
	if !got.Verbose {
		t.Fatal("Verbose = false, want CLI true")
	}
}

func TestResolveSkipsEmptyHigherPrecedenceValues(t *testing.T) {
	got, err := Resolve(Sources{
		File: Options{Endpoint: "https://file.example"},
		Env: map[string]string{"endpoint": "https://env.example"},
		CLI: map[string]string{"endpoint": ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Endpoint != "https://env.example" {
		t.Fatalf("Endpoint = %q, want env value", got.Endpoint)
	}
}

func TestResolveInvalidRetriesIsAnError(t *testing.T) {
	_, err := Resolve(Sources{Env: map[string]string{"retries": "many"}})
	if err == nil {
		t.Fatal("expected invalid retries error")
	}
}
`,
		},
		VerifyCommand:     "go test ./...",
		ExpectedSkill:     "AFFENT ACTIVE SKILL: coding_repair_workflow",
		RequiredCommands:  []string{`go test`},
		ForbiddenCommands: []string{"find /", "apt-get", "curl -sL", "| head", "|| true", "; echo \"EXIT:$?\"", "pip install"},
		ProtectedFiles:    []string{"config/config_test.go"},
		MaxTurns:          10,
	}
}

func pythonSlugScenario() BatchScenario {
	return BatchScenario{
		Name:   "coding-python-slug",
		Prompt: "这个 Python 项目的测试失败。请先运行测试复现，然后修复 slugify / unique_slug 的实现。要求不要修改测试，最后再次运行测试确认。只改必要文件。",
		Files: map[string]string{
			"textstats/__init__.py": `from .slug import slugify, unique_slug

__all__ = ["slugify", "unique_slug"]
`,
			"textstats/slug.py": `import re


def slugify(title: str) -> str:
    text = title.lower().strip()
    text = re.sub(r"[^a-z0-9]+", "-", text)
    return text.strip("-")


def unique_slug(title: str, existing: set[str]) -> str:
    base = slugify(title)
    if base not in existing:
        return base
    suffix = 1
    candidate = f"{base}-{suffix}"
    while candidate in existing:
        suffix += 1
        candidate = f"{base}-{suffix}"
    return candidate
`,
			"test_slug.py": `from textstats import slugify, unique_slug


def test_slugify_ascii_and_whitespace():
    assert slugify("  Hello, Affine World!  ") == "hello-affine-world"


def test_slugify_unicode_words_are_transliterated():
    assert slugify("Café déjà vu") == "cafe-deja-vu"


def test_slugify_empty_after_cleanup_has_fallback():
    assert slugify("!!!") == "untitled"


def test_unique_slug_uses_first_free_number_after_existing_suffixes():
    existing = {"report", "report-1", "report-2"}
    assert unique_slug("Report", existing) == "report-3"
`,
		},
		VerifyCommand:     "python3 -m pytest -q",
		ExpectedSkill:     "AFFENT ACTIVE SKILL: coding_repair_workflow",
		RequiredCommands:  []string{`python(3)? -m pytest`},
		ForbiddenCommands: []string{"find /", "apt-get", "curl -sL", "| head", "|| true", "; echo \"EXIT:$?\"", "pip install"},
		ProtectedFiles:    []string{"test_slug.py"},
		ForbiddenFileSubstrings: map[string][]string{
			"textstats/slug.py": {"unidecode", "text_unidecode"},
		},
		MaxTurns: 10,
	}
}
