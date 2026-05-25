package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

type recordedCommand struct {
	name string
	args []string
}

type fakeCommandRunner struct {
	inspectOut      string
	inspectErr      error
	imageInspectOut string
	imageInspectErr error
	calls           []recordedCommand
}

func (r *fakeCommandRunner) Run(name string, args ...string) (string, error) {
	r.calls = append(r.calls, recordedCommand{name: name, args: append([]string(nil), args...)})
	if len(args) >= 2 && args[0] == "image" && args[1] == "inspect" {
		return r.imageInspectOut, r.imageInspectErr
	}
	if len(args) >= 1 && args[0] == "inspect" {
		return r.inspectOut, r.inspectErr
	}
	return "", nil
}

func TestDefaultSandboxWorkspaceUsesXDGDataHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	got := defaultSandboxWorkspace()
	want := filepath.Join(dir, "affent", "sandbox", "workspace")
	if got != want {
		t.Fatalf("defaultSandboxWorkspace() = %q, want %q", got, want)
	}
}

func TestDefaultSandboxWorkspaceFallsBackWhenHomeIsRoot(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", string(os.PathSeparator))
	got := defaultSandboxWorkspace()
	want := filepath.Join(".", "affent", "sandbox", "workspace")
	if got != want {
		t.Fatalf("defaultSandboxWorkspace() = %q, want %q", got, want)
	}
}

func TestDefaultSandboxImageIsProjectOwned(t *testing.T) {
	if defaultSandboxImage != "affinefoundation/affent-sandbox:latest" {
		t.Fatalf("default sandbox image = %q, want project-owned affent sandbox image", defaultSandboxImage)
	}
}

func TestDefaultRuntimeImageIsProjectOwned(t *testing.T) {
	if defaultRuntimeImage != "affinefoundation/affent:latest" {
		t.Fatalf("default runtime image = %q, want project-owned affent runtime image", defaultRuntimeImage)
	}
}

func TestDockerignoreKeepsRuntimeBuildContextSmall(t *testing.T) {
	_, contextDir, ok, err := findRuntimeBuildSource()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("test requires source checkout with runtime Dockerfile")
	}
	raw, err := os.ReadFile(filepath.Join(contextDir, ".dockerignore"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{
		".git/",
		".tmp/",
		".affentctl/",
		"**/node_modules/",
		"**/test-results/",
		"*.jsonl",
		".env.*",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf(".dockerignore missing %q:\n%s", want, body)
		}
	}
}

func TestSandboxDockerfileGoVersionCoversWorkspaceModules(t *testing.T) {
	dockerfile, contextDir, ok, err := findSandboxBuildSource()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("test requires source checkout with sandbox Dockerfile")
	}
	raw, err := os.ReadFile(dockerfile)
	if err != nil {
		t.Fatal(err)
	}
	imageGo, err := parseSandboxDockerfileGoVersion(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	var modules []goVersion
	err = filepath.WalkDir(contextDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".tmp", "affent-workspace", "bin", "dist", "node_modules":
				if path != contextDir {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if d.Name() != "go.mod" {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		v, err := parseGoModDirective(string(body))
		if err != nil {
			return err
		}
		modules = append(modules, v)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(modules) == 0 {
		t.Fatal("no go.mod files found")
	}
	for _, moduleGo := range modules {
		if imageGo.less(moduleGo) {
			t.Fatalf("sandbox Dockerfile Go %s is older than module go %s", imageGo, moduleGo)
		}
	}
}

func TestDockerImagesShareToolPackageManifest(t *testing.T) {
	_, contextDir, ok, err := findSandboxBuildSource()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("test requires source checkout with docker files")
	}
	manifestPath := filepath.Join(contextDir, "docker", "tool-packages.txt")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	var prev string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		pkg := strings.TrimSpace(line)
		if pkg == "" || strings.HasPrefix(pkg, "#") {
			t.Fatalf("tool package manifest must contain only package names, got line %q", line)
		}
		if seen[pkg] {
			t.Fatalf("duplicate package in tool package manifest: %s", pkg)
		}
		if prev != "" && pkg < prev {
			t.Fatalf("tool package manifest must stay sorted: %s before %s", pkg, prev)
		}
		seen[pkg] = true
		prev = pkg
	}
	for _, want := range []string{"bash", "build-essential", "git", "jq", "nodejs", "npm", "python-is-python3", "python3", "ripgrep", "sqlite3"} {
		if !seen[want] {
			t.Fatalf("tool package manifest missing %s", want)
		}
	}
	for _, dockerfile := range []string{"sandbox.Dockerfile", "affent.Dockerfile"} {
		body, err := os.ReadFile(filepath.Join(contextDir, "docker", dockerfile))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "COPY docker/tool-packages.txt /tmp/affent-tool-packages.txt") {
			t.Fatalf("%s must install tools from docker/tool-packages.txt", dockerfile)
		}
	}
}

func TestAffentDockerfilePackagesRuntimeBinaries(t *testing.T) {
	_, contextDir, ok, err := findSandboxBuildSource()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("test requires source checkout with docker files")
	}
	raw, err := os.ReadFile(filepath.Join(contextDir, "docker", "affent.Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{
		"FROM node:22-bookworm AS webui",
		"npm ci",
		"npm run build",
		"COPY docker/go-cgroup-env.sh /tmp/affent-go-cgroup-env",
		"COPY --from=webui /src/extras/webui/dist ./cmd/affentserve/webui/dist",
		". /tmp/affent-go-cgroup-env",
		"go build -trimpath -ldflags=\"-s -w\" -o /out/affentctl ./cmd/affentctl",
		"go build -trimpath -ldflags=\"-s -w\" -o /out/affenteval ./cmd/affenteval",
		"go build -tags webui -trimpath -ldflags=\"-s -w\" -o /out/affentserve .",
		"COPY --from=build /out/affentctl /usr/local/bin/affentctl",
		"COPY --from=build /out/affenteval /usr/local/bin/affenteval",
		"COPY --from=build /out/affentserve /usr/local/bin/affentserve",
		"COPY docker/go-cgroup-env.sh /usr/local/bin/affent-go-cgroup-env",
		"COPY docker/affent-entrypoint.sh /usr/local/bin/affent-entrypoint",
		"ENTRYPOINT [\"affent-entrypoint\"]",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("affent Dockerfile missing %q", want)
		}
	}
	for _, notWant := range []string{"ENV GOMEMLIMIT=", "ENV GOMAXPROCS="} {
		if strings.Contains(body, notWant) {
			t.Fatalf("affent Dockerfile must derive Go limits at runtime, found %q", notWant)
		}
	}
}

func TestMakeImageServeEnablesBuiltinsInsideRuntimeContainer(t *testing.T) {
	_, contextDir, ok, err := findSandboxBuildSource()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("test requires source checkout with Makefile")
	}
	raw, err := os.ReadFile(filepath.Join(contextDir, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{
		"IMAGE_WORKSPACE ?= $(CURDIR)/.tmp/runtime-workspace",
		"SERVE_CONTAINER_NAME ?= affent-serve",
		"SERVE_BASE_URL ?= $(or $(AFFENTSERVE_BASE_URL),$(AFFENTCTL_BASE_URL))",
		"SERVE_API_KEY ?= $(or $(AFFENTSERVE_API_KEY),$(AFFENTCTL_API_KEY))",
		"SERVE_MODEL ?= $(or $(AFFENTSERVE_MODEL),$(AFFENTCTL_MODEL))",
		"SMOKE_CONTAINER_NAME ?= affent-serve-smoke",
		"SMOKE_WORKSPACE ?= $(CURDIR)/.tmp/image-serve-smoke",
		"SMOKE_PUBLISH ?= 127.0.0.1:7787:7777",
		"SMOKE_URL ?= http://$(SMOKE_HOST):$(SMOKE_PORT)",
		"SMOKE_BASE_URL ?= http://127.0.0.1:9",
		"SMOKE_API_KEY ?= test",
		"SMOKE_MODEL ?= fake",
		"SMOKE_SESSION_ID ?= smoke-persist",
		"SERVE_EVAL_CONTAINER_NAME ?= affent-eval-serve",
		"SERVE_EVAL_WORKSPACE ?= $(CURDIR)/.tmp/eval-serve",
		"SERVE_EVAL_PUBLISH ?= 127.0.0.1:7777:7777",
		"SERVE_EVAL_PERMISSIONS ?=",
		"SERVE_PUBLISH_PARTS := $(subst :, ,$(SERVE_PUBLISH))",
		"SERVE_PUBLISH_WORDS := $(words $(SERVE_PUBLISH_PARTS))",
		"SERVE_HEALTH_HOST := $(if $(filter 3,$(SERVE_PUBLISH_WORDS)),$(word 1,$(SERVE_PUBLISH_PARTS)),127.0.0.1)",
		"SERVE_HEALTH_PORT := $(if $(filter 3,$(SERVE_PUBLISH_WORDS)),$(word 2,$(SERVE_PUBLISH_PARTS)),$(word 1,$(SERVE_PUBLISH_PARTS)))",
		"SERVE_HEALTH_URL ?= http://$(SERVE_HEALTH_HOST):$(SERVE_HEALTH_PORT)/healthz",
		"SERVE_HEALTH_ATTEMPTS ?= 30",
		"SERVE_HEALTH_INTERVAL ?= 1",
		"SERVE_MEMORY_ROOT ?= /workspace/session-state",
		"image-run: image-build",
		"image-serve: image-build",
		"image-serve-up:",
		"image-serve-status:",
		"image-serve-health:",
		"image-serve-health-wait:",
		"image-serve-logs:",
		"image-serve-stop:",
		"image-serve-restart:",
		"image-serve-smoke:",
		"eval-serve-container:",
		"eval-serve-browser-container: SERVE_EVAL_PERMISSIONS=browser",
		"eval-serve-browser-container: eval-serve-container",
		"define require_affent_runtime_container",
		`SERVE_CONTAINER_NAME is required`,
		`make image-serve-up SERVE_CONTAINER_NAME=affent-serve`,
		`make image-serve-restart SERVE_CONTAINER_NAME=affent-serve`,
		`make $(1) SERVE_CONTAINER_NAME=affent-serve`,
		`docker inspect "$(SERVE_CONTAINER_NAME)"`,
		`{{index .Config.Labels "affent.runtime"}}`,
		`is not an Affent runtime container`,
		`docker port "$(SERVE_CONTAINER_NAME)"`,
		`ports: none`,
		`curl -fsS "$(SERVE_HEALTH_URL)"`,
		`serve_eval_mode={{index .Config.Labels "affent.runtime.serve.eval_mode"}}`,
		`serve_browser={{index .Config.Labels "affent.runtime.serve.browser"}}`,
		`serve_web_search={{index .Config.Labels "affent.runtime.serve.web_search"}}`,
		`health check failed ($$attempt/$(SERVE_HEALTH_ATTEMPTS))`,
		`docker logs --tail 100 "$(SERVE_CONTAINER_NAME)"`,
		`affent.runtime.memory`,
		`host_memory_bytes={{.HostConfig.Memory}}`,
		`docker rm -f "$(SERVE_CONTAINER_NAME)"`,
		`{{.State.Running}}`,
		`already running; waiting for health`,
		`exists but is not running; starting`,
		`expected_workspace="$(abspath $(IMAGE_WORKSPACE))"`,
		`actual_workspace=$$(docker inspect "$(SERVE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.workspace"}}'`,
		`was created with workspace=$$actual_workspace, but requested workspace=$$expected_workspace`,
		`run make image-serve-restart to recreate it with the requested persistent workspace`,
		`actual_memory=$$(docker inspect "$(SERVE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.memory"}}'`,
		`actual_cpus=$$(docker inspect "$(SERVE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.cpus"}}'`,
		`actual_pids=$$(docker inspect "$(SERVE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.pids_limit"}}'`,
		`was created with memory=$$actual_memory cpus=$$actual_cpus pids_limit=$$actual_pids`,
		`run make image-serve-restart to recreate it with the requested limits`,
		`actual_publish=$$(docker inspect "$(SERVE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.publish"}}'`,
		`was created with publish=$$actual_publish, but requested publish=$(SERVE_PUBLISH)`,
		`run make image-serve-restart to recreate it with the requested port publishing`,
		`actual_listen=$$(docker inspect "$(SERVE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.serve.listen"}}'`,
		`actual_serve_workspace_root=$$(docker inspect "$(SERVE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.serve.workspace_root"}}'`,
		`actual_serve_memory_root=$$(docker inspect "$(SERVE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.serve.memory_root"}}'`,
		`was created with listen=$$actual_listen workspace_root=$$actual_serve_workspace_root memory_root=$$actual_serve_memory_root`,
		`run make image-serve-restart to recreate it with the requested affentserve paths`,
		`docker start "$(SERVE_CONTAINER_NAME)"`,
		`$(MAKE) image-serve`,
		`$(MAKE) image-serve-health-wait`,
		`curl -fsS "$(SMOKE_URL)/healthz"`,
		`SMOKE_CONTAINER_NAME is required`,
		`SMOKE_WORKSPACE must be a non-root path`,
		`--data '{"session_id":"$(SMOKE_SESSION_ID)"}'`,
		`test -f "$(SMOKE_WORKSPACE)/session-state/$(SMOKE_SESSION_ID)/conversation.jsonl"`,
		`docker stop "$(SMOKE_CONTAINER_NAME)"`,
		`"$(SMOKE_URL)/v1/sessions/$(SMOKE_SESSION_ID)"`,
		`memory_label=$$(docker inspect "$(SMOKE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.memory"}}')`,
		`image-serve-smoke ok`,
		`$(if $(SERVE_CONTAINER_NAME),--name "$(SERVE_CONTAINER_NAME)")`,
		`--timeout 0s --detach --rm=false --publish "$(SERVE_PUBLISH)"`,
		`$(if $(SERVE_BASE_URL),--base-url "$(SERVE_BASE_URL)")`,
		`$(if $(SERVE_API_KEY),--api-key "$(SERVE_API_KEY)")`,
		`$(if $(SERVE_MODEL),--model "$(SERVE_MODEL)")`,
		`--detach --rm=false --publish "$(SERVE_PUBLISH)"`,
		"affentserve --listen \"$(SERVE_LISTEN)\" $(if $(SERVE_BASE_URL),--base-url \"$(SERVE_BASE_URL)\") $(if $(SERVE_API_KEY),--api-key \"$(SERVE_API_KEY)\") $(if $(SERVE_MODEL),--model \"$(SERVE_MODEL)\") --workspace-root \"$(SERVE_WORKSPACE_ROOT)\" --memory-root \"$(SERVE_MEMORY_ROOT)\" --builtins $(SERVE_ARGS)",
		`args="--eval-mode"`,
		`browser) args="$$args --browser=true"`,
		`browser-screenshot) args="$$args --browser=true --browser-screenshot=true"`,
		`web-search) args="$$args --web=true --web-search=true"`,
		`memory) args="$$args --memory=true"`,
		`unknown SERVE_EVAL_PERMISSIONS entry`,
		`$(MAKE) image-serve-restart`,
		`SERVE_CONTAINER_NAME="$(SERVE_EVAL_CONTAINER_NAME)"`,
		`IMAGE_WORKSPACE="$(SERVE_EVAL_WORKSPACE)"`,
		`SERVE_PUBLISH="$(SERVE_EVAL_PUBLISH)"`,
		`SERVE_ARGS="$$args"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("Makefile image-serve target missing %q", want)
		}
	}
}

func TestTechnicalManualDocumentsImageServeSessionPersistence(t *testing.T) {
	_, contextDir, ok, err := findSandboxBuildSource()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("test requires source checkout with technical manual")
	}
	raw, err := os.ReadFile(filepath.Join(contextDir, "docs", "technical-manual.md"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{
		"`make image-serve-up` and `make image-serve-restart`",
		"`/workspace/session-state`",
		"`IMAGE_WORKSPACE`",
		"preserves conversation history as long as `IMAGE_WORKSPACE` is the same host",
		"`DELETE /v1/sessions/{id}`",
		"intentionally removes that durable state",
		"`make image-serve-smoke`",
		"verifies the durable session is",
		"still listed",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("technical manual session persistence docs missing %q", want)
		}
	}
}

func TestMakeOneClickContainerTargetsUseSharedLimits(t *testing.T) {
	_, contextDir, ok, err := findSandboxBuildSource()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("test requires source checkout with Makefile")
	}
	raw, err := os.ReadFile(filepath.Join(contextDir, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{
		"CONTAINER_MEMORY ?= 1g",
		"CONTAINER_CPUS ?= 2",
		"CONTAINER_PIDS ?= 512",
		"EVAL_RUNTIME_EVAL_MODE ?= false",
		"EVAL_RUNTIME_MEMORY ?= false",
		"EVAL_RUNTIME_MCP_CONFIG ?=",
		"eval-agent-container: eval-container",
		"eval-serve-container:",
		"eval-serve-browser-container: SERVE_EVAL_PERMISSIONS=browser",
		"eval-serve-browser-container: eval-serve-container",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("Makefile one-click container targets missing %q", want)
		}
	}
	for target, wants := range map[string][]string{
		"affentctl": {
			`--memory "$(CONTAINER_MEMORY)"`,
			`--memory-swap "$(CONTAINER_MEMORY)"`,
			`--cpus "$(CONTAINER_CPUS)"`,
			`--pids-limit "$(CONTAINER_PIDS)"`,
		},
		"sandbox-start": {
			`sandbox start --memory "$(CONTAINER_MEMORY)" --cpus "$(CONTAINER_CPUS)" --pids-limit "$(CONTAINER_PIDS)"`,
		},
		"image-build": {
			`image build --memory "$(CONTAINER_MEMORY)"`,
		},
		"image-run": {
			`image run --workspace "$(IMAGE_WORKSPACE)" --memory "$(CONTAINER_MEMORY)" --cpus "$(CONTAINER_CPUS)" --pids-limit "$(CONTAINER_PIDS)" $(IMAGE_RUN_ARGS)`,
		},
		"image-serve": {
			`image run --workspace "$(IMAGE_WORKSPACE)" --memory "$(CONTAINER_MEMORY)" --cpus "$(CONTAINER_CPUS)" --pids-limit "$(CONTAINER_PIDS)" $(if $(SERVE_CONTAINER_NAME),--name "$(SERVE_CONTAINER_NAME)") --timeout 0s --detach --rm=false`,
		},
		"image-serve-restart": {
			`$(MAKE) image-serve`,
			`$(MAKE) image-serve-health-wait`,
		},
		"image-serve-smoke": {
			`SERVE_CONTAINER_NAME="$(SMOKE_CONTAINER_NAME)"`,
			`IMAGE_WORKSPACE="$(SMOKE_WORKSPACE)"`,
			`SERVE_PUBLISH="$(SMOKE_PUBLISH)"`,
			`CONTAINER_MEMORY="$(CONTAINER_MEMORY)"`,
			`CONTAINER_CPUS="$(CONTAINER_CPUS)"`,
			`CONTAINER_PIDS="$(CONTAINER_PIDS)"`,
		},
		"eval-container": {
			`image build --image "$(EVAL_IMAGE)" --memory "$(CONTAINER_MEMORY)"`,
			`--memory "$(CONTAINER_MEMORY)"`,
			`--memory-swap "$(CONTAINER_MEMORY)"`,
			`--cpus "$(CONTAINER_CPUS)"`,
			`--pids-limit "$(CONTAINER_PIDS)"`,
			`-e AFFENTEVAL_PROVIDER_LABEL`,
			`$(EVAL_RUNTIME_EVAL_MODE_ARGS) $(EVAL_RUNTIME_MEMORY_ARGS) $(EVAL_RUNTIME_MCP_CONFIG_ARGS) $(EVAL_ARGS)`,
		},
		"eval-agent-container": {
			`eval-agent-container: EVAL_RUNTIME_EVAL_MODE=true`,
		},
		"eval-serve-container": {
			`args="--eval-mode"`,
			`browser) args="$$args --browser=true"`,
			`web-search) args="$$args --web=true --web-search=true"`,
			`$(MAKE) image-serve-restart`,
			`CONTAINER_MEMORY="$(CONTAINER_MEMORY)"`,
			`CONTAINER_CPUS="$(CONTAINER_CPUS)"`,
			`CONTAINER_PIDS="$(CONTAINER_PIDS)"`,
		},
		"test-container": {
			`--memory "$(CONTAINER_MEMORY)"`,
			`--memory-swap "$(CONTAINER_MEMORY)"`,
			`--cpus "$(CONTAINER_CPUS)"`,
			`--pids-limit "$(CONTAINER_PIDS)"`,
		},
	} {
		block := makefileTargetBlock(t, body, target)
		for _, want := range wants {
			if !strings.Contains(block, want) {
				t.Fatalf("Makefile target %s missing %q\nblock:\n%s", target, want, block)
			}
		}
	}
	for _, unwanted := range []string{
		`-e AFFENTCTL_TEMPERATURE`,
		`-e AFFENTCTL_TOP_P`,
		`-e AFFENTCTL_MAX_TOKENS`,
		`-e AFFENTCTL_SEED`,
	} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("eval container should require explicit affenteval sampling flags, found %q", unwanted)
		}
	}
}

func makefileTargetBlock(t *testing.T, body, target string) string {
	t.Helper()
	startNeedle := "\n" + target + ":"
	start := strings.Index(body, startNeedle)
	if start < 0 {
		if strings.HasPrefix(body, target+":") {
			start = 0
		} else {
			t.Fatalf("Makefile target %s not found", target)
		}
	} else {
		start++
	}
	rest := body[start:]
	for i, line := range strings.Split(rest, "\n") {
		if i == 0 || strings.HasPrefix(line, "\t") || strings.TrimSpace(line) == "" {
			continue
		}
		if strings.Contains(line, ":") {
			return strings.Join(strings.Split(rest, "\n")[:i], "\n")
		}
	}
	return rest
}

func TestMakeEvalServePermissionsExpandToServeArgs(t *testing.T) {
	_, contextDir, ok, err := findSandboxBuildSource()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("test requires source checkout with Makefile")
	}
	out := runMakeEvalServePermissions(t, contextDir, "web-search memory")
	for _, want := range []string{
		"image-serve-restart",
		"SERVE_ARGS=--eval-mode --web=true --web-search=true --memory=true",
		"CONTAINER_MEMORY=1g",
		"CONTAINER_CPUS=2",
		"CONTAINER_PIDS=512",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expanded eval serve args missing %q:\n%s", want, out)
		}
	}

	out = runMakeEvalServePermissions(t, contextDir, "browser")
	for _, want := range []string{
		"SERVE_ARGS=--eval-mode --browser=true",
		"IMAGE_WORKSPACE=" + filepath.Join(contextDir, ".tmp", "eval-serve"),
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expanded browser eval serve args missing %q:\n%s", want, out)
		}
	}
	for _, forbidden := range []string{"--web=true", "--web-search=true", "--memory=true"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("browser-only eval serve args should not include %q:\n%s", forbidden, out)
		}
	}

	cmd := exec.Command("make", "-s", "eval-serve-container", "SERVE_EVAL_PERMISSIONS=bad", `MAKE=printf "%s\n"`)
	cmd.Dir = contextDir
	raw, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("unknown SERVE_EVAL_PERMISSIONS should fail, output:\n%s", raw)
	}
	if !strings.Contains(string(raw), "unknown SERVE_EVAL_PERMISSIONS entry: bad") {
		t.Fatalf("unknown permission error missing useful message:\n%s", raw)
	}
}

func runMakeEvalServePermissions(t *testing.T, contextDir, permissions string) string {
	t.Helper()
	cmd := exec.Command("make", "-s", "eval-serve-container", "SERVE_EVAL_PERMISSIONS="+permissions, `MAKE=printf "%s\n"`)
	cmd.Dir = contextDir
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make eval-serve-container permissions %q: %v\n%s", permissions, err, raw)
	}
	return string(raw)
}

func TestMakeSandboxStatusAcceptsArgs(t *testing.T) {
	_, contextDir, ok, err := findSandboxBuildSource()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("test requires source checkout with Makefile")
	}
	raw, err := os.ReadFile(filepath.Join(contextDir, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{
		"SANDBOX_STATUS_ARGS ?=",
		`sandbox status $(SANDBOX_STATUS_ARGS)`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("Makefile sandbox-status target missing %q", want)
		}
	}
}

func TestAffentEntrypointUsesSharedGoCgroupEnvHelper(t *testing.T) {
	_, contextDir, ok, err := findSandboxBuildSource()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("test requires source checkout with docker files")
	}
	entrypoint, err := os.ReadFile(filepath.Join(contextDir, "docker", "affent-entrypoint.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(entrypoint), ". /usr/local/bin/affent-go-cgroup-env") {
		t.Fatalf("affent entrypoint should source shared cgroup helper, got:\n%s", entrypoint)
	}
	helper, err := os.ReadFile(filepath.Join(contextDir, "docker", "go-cgroup-env.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"GOMEMLIMIT", "GOMAXPROCS", "/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/cpu.max"} {
		if !strings.Contains(string(helper), want) {
			t.Fatalf("go cgroup helper missing %q", want)
		}
	}
}

func TestOSCommandRunnerTimeout(t *testing.T) {
	prev := sandboxDockerCommandTimeout
	sandboxDockerCommandTimeout = 10 * time.Millisecond
	t.Cleanup(func() {
		sandboxDockerCommandTimeout = prev
	})
	_, err := osCommandRunner{}.Run("sh", "-c", "sleep 1")
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
}

func TestOSCommandRunnerStreamsOutputWhenConfigured(t *testing.T) {
	var stdout, stderr strings.Builder
	out, err := osCommandRunner{stdout: &stdout, stderr: &stderr, streamAll: true}.Run("sh", "-c", "echo out; echo err >&2")
	if err != nil {
		t.Fatal(err)
	}
	if out != "out" {
		t.Fatalf("captured stdout = %q, want out", out)
	}
	if strings.TrimSpace(stdout.String()) != "out" {
		t.Fatalf("streamed stdout = %q, want out", stdout.String())
	}
	if strings.TrimSpace(stderr.String()) != "err" {
		t.Fatalf("streamed stderr = %q, want err", stderr.String())
	}
}

func TestOSCommandRunnerCanStreamOnlyDockerBuild(t *testing.T) {
	dir := t.TempDir()
	dockerPath := filepath.Join(dir, "docker")
	if err := os.WriteFile(dockerPath, []byte("#!/bin/sh\necho streamed\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var stdout strings.Builder
	runner := osCommandRunner{stdout: &stdout, streamBuild: true}
	if _, err := runner.Run("docker", "version"); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("non-build output should not stream, got %q", stdout.String())
	}
	if _, err := runner.Run("docker", "build"); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout.String()) != "streamed" {
		t.Fatalf("docker build output was not streamed: %q", stdout.String())
	}
}

func TestBuildDockerImageUsesProjectDockerfileAndMemoryLimit(t *testing.T) {
	dockerfile, contextDir, ok, err := findSandboxBuildSource()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("test requires source checkout with sandbox Dockerfile")
	}
	runner := &fakeCommandRunner{}
	opts := dockerBuildOptions{
		Image:      "example/affent-sandbox:test",
		Dockerfile: "docker/sandbox.Dockerfile",
		Context:    ".",
		Memory:     "768m",
		NoCache:    true,
	}
	if err := buildDockerImage(opts, runner); err != nil {
		t.Fatalf("buildDockerImage: %v", err)
	}
	want := []recordedCommand{
		{name: "docker", args: []string{
			"build",
			"--memory", "768m",
			"--memory-swap", "768m",
			"-f", dockerfile,
			"-t", "example/affent-sandbox:test",
			"--no-cache",
			contextDir,
		}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %+v, want %+v", runner.calls, want)
	}
}

func TestBuildDockerImageRequiresMeaningfulInputs(t *testing.T) {
	for _, c := range []struct {
		name string
		opts dockerBuildOptions
		want string
	}{
		{
			name: "image",
			opts: dockerBuildOptions{Dockerfile: "docker/sandbox.Dockerfile", Context: ".", Memory: "1g"},
			want: "--image is required",
		},
		{
			name: "image whitespace",
			opts: dockerBuildOptions{Image: "bad image", Dockerfile: "docker/sandbox.Dockerfile", Context: ".", Memory: "1g"},
			want: "--image must not contain whitespace",
		},
		{
			name: "image leading dash",
			opts: dockerBuildOptions{Image: "-bad", Dockerfile: "docker/sandbox.Dockerfile", Context: ".", Memory: "1g"},
			want: "--image must not start",
		},
		{
			name: "dockerfile",
			opts: dockerBuildOptions{Image: "image", Context: ".", Memory: "1g"},
			want: "--dockerfile is required",
		},
		{
			name: "context",
			opts: dockerBuildOptions{Image: "image", Dockerfile: "docker/sandbox.Dockerfile", Memory: "1g"},
			want: "--context is required",
		},
		{
			name: "memory",
			opts: dockerBuildOptions{Image: "image", Dockerfile: "docker/sandbox.Dockerfile", Context: "."},
			want: "--memory is required",
		},
		{
			name: "invalid memory",
			opts: dockerBuildOptions{Image: "image", Dockerfile: "docker/sandbox.Dockerfile", Context: ".", Memory: "not-memory"},
			want: "positive Docker memory limit",
		},
		{
			name: "zero memory",
			opts: dockerBuildOptions{Image: "image", Dockerfile: "docker/sandbox.Dockerfile", Context: ".", Memory: "0"},
			want: "positive Docker memory limit",
		},
		{
			name: "too little memory",
			opts: dockerBuildOptions{Image: "image", Dockerfile: "docker/sandbox.Dockerfile", Context: ".", Memory: "64m"},
			want: "at least 128m",
		},
		{
			name: "missing dockerfile path",
			opts: dockerBuildOptions{Image: "image", Dockerfile: filepath.Join(t.TempDir(), "missing.Dockerfile"), Context: ".", Memory: "1g"},
			want: "--dockerfile path",
		},
		{
			name: "dockerfile is directory",
			opts: dockerBuildOptions{Image: "image", Dockerfile: t.TempDir(), Context: ".", Memory: "1g"},
			want: "must be a file",
		},
		{
			name: "missing context path",
			opts: dockerBuildOptions{Image: "image", Dockerfile: "docker/sandbox.Dockerfile", Context: filepath.Join(t.TempDir(), "missing-context"), Memory: "1g"},
			want: "--context path",
		},
		{
			name: "context is file",
			opts: func() dockerBuildOptions {
				dir := t.TempDir()
				path := filepath.Join(dir, "context-file")
				if err := os.WriteFile(path, []byte("not a dir"), 0o600); err != nil {
					t.Fatal(err)
				}
				return dockerBuildOptions{Image: "image", Dockerfile: "docker/sandbox.Dockerfile", Context: path, Memory: "1g"}
			}(),
			want: "must be a directory",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			runner := &fakeCommandRunner{}
			err := buildDockerImage(c.opts, runner)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v, want contains %q", err, c.want)
			}
			if len(runner.calls) != 0 {
				t.Fatalf("invalid build options must not call docker, calls=%+v", runner.calls)
			}
		})
	}
}

func TestImageBuildCmdUsesRuntimeDockerfileAndMemoryLimit(t *testing.T) {
	dockerfile, contextDir, ok, err := findRuntimeBuildSource()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("test requires source checkout with runtime Dockerfile")
	}
	runner := &fakeCommandRunner{}
	var stdout, stderr strings.Builder
	code := imageBuildCmd([]string{"--image", "example/affent:test", "--memory", "768m", "--no-cache"}, runner, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d stderr=%s", code, stderr.String())
	}
	want := []recordedCommand{
		{name: "docker", args: []string{
			"build",
			"--memory", "768m",
			"--memory-swap", "768m",
			"-f", dockerfile,
			"-t", "example/affent:test",
			"--no-cache",
			contextDir,
		}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %+v, want %+v", runner.calls, want)
	}
	if !strings.Contains(stdout.String(), "image: example/affent:test") {
		t.Fatalf("stdout = %q, want built image tag", stdout.String())
	}
}

func TestDefaultRuntimeBuildOptionsUseMeaningfulDefaults(t *testing.T) {
	got := defaultRuntimeBuildOptions()
	want := dockerBuildOptions{
		Image:      defaultRuntimeImage,
		Dockerfile: defaultRuntimeDockerfile,
		Context:    defaultSandboxBuildContext,
		Memory:     defaultSandboxMemory,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("defaultRuntimeBuildOptions() = %+v, want %+v", got, want)
	}
}

func TestDefaultRuntimeWorkspaceUsesXDGDataHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	got := defaultRuntimeWorkspace()
	want := filepath.Join(dir, "affent", "runtime", "workspace")
	if got != want {
		t.Fatalf("defaultRuntimeWorkspace() = %q, want %q", got, want)
	}
}

func TestDefaultRuntimeWorkspaceFallsBackWhenHomeIsRoot(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", string(os.PathSeparator))
	got := defaultRuntimeWorkspace()
	want := filepath.Join(".", "affent", "runtime", "workspace")
	if got != want {
		t.Fatalf("defaultRuntimeWorkspace() = %q, want %q", got, want)
	}
}

func TestDefaultRuntimeRunOptionsUseMeaningfulDefaults(t *testing.T) {
	workspace := t.TempDir()
	got := defaultRuntimeRunOptions(workspace)
	if got.Image != defaultRuntimeImage {
		t.Fatalf("runtime image = %q, want %q", got.Image, defaultRuntimeImage)
	}
	if got.Workspace != workspace {
		t.Fatalf("runtime workspace = %q, want %q", got.Workspace, workspace)
	}
	if got.Memory != defaultSandboxMemory || got.CPUs != defaultSandboxCPUs || got.PIDsLimit != defaultSandboxPIDs {
		t.Fatalf("runtime limits = memory %q cpus %q pids %q, want %q/%q/%q", got.Memory, got.CPUs, got.PIDsLimit, defaultSandboxMemory, defaultSandboxCPUs, defaultSandboxPIDs)
	}
	if got.Timeout != runtimeDockerRunTimeout {
		t.Fatalf("runtime timeout = %s, want %s", got.Timeout, runtimeDockerRunTimeout)
	}
	if !got.Remove {
		t.Fatal("runtime containers should be removed by default")
	}
}

func TestRunRuntimeImageUsesPersistentWorkspaceAndLimits(t *testing.T) {
	t.Setenv("AFFENTCTL_API_KEY", "host-key")
	workspace := filepath.Join(t.TempDir(), "runtime ws")
	runner := &fakeCommandRunner{}
	opts := runtimeRunOptions{
		Name:      "affent-runtime",
		Image:     "example/affent:local",
		Workspace: workspace,
		Memory:    "768m",
		CPUs:      "1.5",
		PIDsLimit: "256",
		User:      "123:456",
		TTY:       true,
		Remove:    true,
		Env:       []string{"AFFENTCTL_API_KEY=explicit-key", "EXTRA_FLAG=1"},
		Publish:   []string{"7777:7777"},
		Command:   []string{"affentctl", "run", "--prompt", "hi"},
	}
	if err := runRuntimeImage(opts, runner); err != nil {
		t.Fatalf("runRuntimeImage: %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("calls = %+v, want one docker run", runner.calls)
	}
	run := runner.calls[0]
	if run.name != "docker" || len(run.args) == 0 || run.args[0] != "run" {
		t.Fatalf("call = %+v, want docker run", run)
	}
	for _, want := range []string{
		"--rm",
		"--name", "affent-runtime",
		"-i",
		"--init",
		"--memory", "768m",
		"--memory-swap", "768m",
		"--cpus", "1.5",
		"--pids-limit", "256",
		"--label", runtimeLabelManaged + "=true",
		"--label", runtimeLabelImage + "=example/affent:local",
		"--label", runtimeLabelWorkspace + "=" + workspace,
		"--label", runtimeLabelMemory + "=768m",
		"--label", runtimeLabelCPUs + "=1.5",
		"--label", runtimeLabelPIDsLimit + "=256",
		"--label", runtimeLabelUser + "=123:456",
		"--label", runtimeLabelPublish + "=7777:7777",
		"-v", workspace + ":/workspace",
		"-w", "/workspace",
		"-t",
		"--user", "123:456",
		"-e", "HOME=/workspace/.home",
		"-e", "XDG_CACHE_HOME=/workspace/.cache",
		"-e", "GOCACHE=/workspace/.cache/go-build",
		"-e", "GOMODCACHE=/workspace/.cache/go-mod",
		"-e", "NPM_CONFIG_CACHE=/workspace/.cache/npm",
		"-e", "PIP_CACHE_DIR=/workspace/.cache/pip",
		"-e", "AFFENTCTL_API_KEY=explicit-key",
		"-e", "EXTRA_FLAG=1",
		"-p", "7777:7777",
		"example/affent:local",
		"affentctl",
		"run",
		"--prompt",
		"hi",
	} {
		if !contains(run.args, want) {
			t.Fatalf("docker run args missing %q:\n%v", want, run.args)
		}
	}
	if contains(run.args, "AFFENTCTL_API_KEY=host-key") {
		t.Fatalf("explicit --env should override host env, args:\n%v", run.args)
	}
	for _, dir := range runtimePersistentDirs(workspace) {
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			t.Fatalf("persistent dir %s not created; stat=%v err=%v", dir, st, err)
		}
	}
}

func TestRunRuntimeImageDetachRunsInBackground(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "runtime")
	runner := &fakeCommandRunner{}
	opts := runtimeRunOptions{
		Name:      "affent-serve",
		Image:     "example/affent:local",
		Workspace: workspace,
		Memory:    "768m",
		CPUs:      "1.5",
		PIDsLimit: "256",
		Detach:    true,
		Remove:    true,
		Command:   []string{"affentserve", "--listen", "0.0.0.0:7777"},
	}
	if err := runRuntimeImage(opts, runner); err != nil {
		t.Fatalf("runRuntimeImage: %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("calls = %+v, want one docker run", runner.calls)
	}
	args := runner.calls[0].args
	if !contains(args, "--detach") {
		t.Fatalf("detached runtime missing --detach:\n%v", args)
	}
	if contains(args, "-i") {
		t.Fatalf("detached runtime should not keep stdin open:\n%v", args)
	}
	if !contains(args, "--name") || !contains(args, "affent-serve") {
		t.Fatalf("detached service should keep a stable container name:\n%v", args)
	}
}

func TestRunRuntimeImageLabelsAffentServeRuntimePaths(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "runtime")
	runner := &fakeCommandRunner{}
	opts := runtimeRunOptions{
		Image:     "example/affent:local",
		Workspace: workspace,
		Memory:    "768m",
		CPUs:      "1.5",
		PIDsLimit: "256",
		Command: []string{
			"affentserve",
			"--listen=0.0.0.0:7777",
			"--workspace-root", "/workspace/sessions",
			"--memory-root", "/workspace/session-state",
			"--builtins",
			"--eval-mode",
			"--browser=true",
			"--web-search=false",
			"--api-key", "secret-value",
		},
	}
	if err := runRuntimeImage(opts, runner); err != nil {
		t.Fatalf("runRuntimeImage: %v", err)
	}
	args := runner.calls[0].args
	for _, want := range []string{
		"--label", runtimeLabelServeListen + "=0.0.0.0:7777",
		"--label", runtimeLabelServeWorkspaceRoot + "=/workspace/sessions",
		"--label", runtimeLabelServeMemoryRoot + "=/workspace/session-state",
		"--label", runtimeLabelServeBuiltins + "=true",
		"--label", runtimeLabelServeEvalMode + "=true",
		"--label", runtimeLabelServeBrowser + "=true",
		"--label", runtimeLabelServeWebSearch + "=false",
	} {
		if !contains(args, want) {
			t.Fatalf("docker run args missing %q:\n%v", want, args)
		}
	}
	for i, arg := range args {
		if strings.HasPrefix(arg, runtimeLabelServeListen+"=") ||
			strings.HasPrefix(arg, runtimeLabelServeWorkspaceRoot+"=") ||
			strings.HasPrefix(arg, runtimeLabelServeMemoryRoot+"=") ||
			strings.HasPrefix(arg, runtimeLabelServeBuiltins+"=") ||
			strings.HasPrefix(arg, runtimeLabelServeEvalMode+"=") ||
			strings.HasPrefix(arg, runtimeLabelServeBrowser+"=") ||
			strings.HasPrefix(arg, runtimeLabelServeWebSearch+"=") {
			if i == 0 || args[i-1] != "--label" {
				t.Fatalf("service label %q should be passed as value after --label:\n%v", arg, args)
			}
		}
		if strings.Contains(arg, "secret-value") && strings.HasPrefix(arg, "affent.runtime.") {
			t.Fatalf("secret must not be written into runtime labels: %q", arg)
		}
	}
}

func TestRuntimeServeCommandLabelsIgnoresUnknownFlagsWithoutSkippingKnownOnes(t *testing.T) {
	got := runtimeServeCommandLabels([]string{
		"affentserve",
		"--builtins",
		"--eval-mode",
		"--browser=true",
		"--memory=false",
		"--workspace-root", "/workspace/sessions",
		"--unknown-flag",
		"--memory-root=/workspace/session-state",
		"--api-key", "secret-value",
		"--listen", "0.0.0.0:7777",
	})
	joined := strings.Join(got, "\n")
	for _, want := range []string{
		runtimeLabelServeListen + "=0.0.0.0:7777",
		runtimeLabelServeWorkspaceRoot + "=/workspace/sessions",
		runtimeLabelServeMemoryRoot + "=/workspace/session-state",
		runtimeLabelServeBuiltins + "=true",
		runtimeLabelServeEvalMode + "=true",
		runtimeLabelServeBrowser + "=true",
		runtimeLabelServeMemory + "=false",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("runtimeServeCommandLabels missing %q:\n%v", want, got)
		}
	}
	if strings.Contains(joined, "secret-value") {
		t.Fatalf("secret should not be included in service labels: %v", got)
	}
}

func TestRuntimeForwardEnvIncludesPortableCLIAndServeConfig(t *testing.T) {
	for _, name := range runtimeForwardEnvNames() {
		t.Setenv(name, "host-"+name)
	}
	for _, name := range []string{
		"AFFENTCTL_WORKSPACE",
		"AFFENTCTL_CONFIG",
		"AFFENTCTL_MCP_CONFIG",
		"AFFENTCTL_EXECUTOR",
		"AFFENTSERVE_WORKSPACE_ROOT",
		"AFFENTSERVE_MEMORY_ROOT",
	} {
		t.Setenv(name, "host-"+name)
	}
	got, err := runtimeForwardEnv([]string{
		"AFFENTCTL_API_KEY=explicit-cli-key",
		"AFFENTCTL_WORKSPACE=/workspace/explicit",
		"AFFENTSERVE_TEMPERATURE=0",
	})
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, kv := range got {
		name, value, ok := strings.Cut(kv, "=")
		if !ok {
			t.Fatalf("env missing '=': %q", kv)
		}
		env[name] = value
	}
	for _, name := range []string{
		"AFFENTCTL_SUBAGENT",
		"AFFENTCTL_EVAL_MODE",
		"AFFENTCTL_SUBAGENT_MAX_DEPTH",
		"AFFENTCTL_FOCUSED_TASKS",
		"AFFENTCTL_MAX_TURNS",
		"AFFENTCTL_MAX_CALL_TIMEOUT",
		"AFFENTCTL_RETRY_TRANSIENT",
		"AFFENTCTL_RETRY_BACKOFF",
		"AFFENTCTL_MEMORY",
		"AFFENTCTL_MEMORY_ONLY",
		"AFFENTCTL_MEMORY_MAX_CHARS",
		"AFFENTCTL_MEMORY_TOPIC_MAX_CHARS",
		"AFFENTCTL_MEMORY_MAX_TOPICS",
		"AFFENTCTL_PROJECT_CONTEXT",
		"AFFENTCTL_COMPACT_TRIGGER",
		"AFFENTCTL_COMPACT_KEEP_LAST",
		"AFFENTCTL_TEMPERATURE",
		"AFFENTCTL_TOP_P",
		"AFFENTCTL_MAX_TOKENS",
		"AFFENTCTL_SEED",
		"AFFENTSERVE_BROWSER",
		"AFFENTSERVE_BROWSER_SCREENSHOT",
		"AFFENTSERVE_WEB",
		"AFFENTSERVE_WEB_SEARCH",
		"AFFENTSERVE_MEMORY",
		"AFFENTSERVE_BUILTINS",
		"AFFENTSERVE_EVAL_MODE",
		"AFFENTSERVE_SUBAGENT",
		"AFFENTSERVE_SUBAGENT_MAX_DEPTH",
		"AFFENTSERVE_FOCUSED_TASKS",
		"AFFENTSERVE_SESSION_RETENTION",
		"AFFENTSERVE_TOP_P",
		"AFFENTSERVE_MAX_TOKENS",
		"TAVILY_API_KEY",
	} {
		if env[name] != "host-"+name {
			t.Fatalf("%s was not forwarded correctly: %q", name, env[name])
		}
	}
	for _, name := range []string{
		"AFFENTCTL_CONFIG",
		"AFFENTCTL_MCP_CONFIG",
		"AFFENTCTL_EXECUTOR",
		"AFFENTSERVE_WORKSPACE_ROOT",
		"AFFENTSERVE_MEMORY_ROOT",
	} {
		if _, ok := env[name]; ok {
			t.Fatalf("%s should not be auto-forwarded from the host into the runtime container", name)
		}
	}
	if env["AFFENTCTL_API_KEY"] != "explicit-cli-key" {
		t.Fatalf("explicit env should override host CLI key, got %q", env["AFFENTCTL_API_KEY"])
	}
	if env["AFFENTCTL_WORKSPACE"] != "/workspace/explicit" {
		t.Fatalf("explicit container workspace should be forwarded, got %q", env["AFFENTCTL_WORKSPACE"])
	}
	if env["AFFENTSERVE_TEMPERATURE"] != "0" {
		t.Fatalf("explicit env should override host serve temperature, got %q", env["AFFENTSERVE_TEMPERATURE"])
	}
}

func TestImageRunCmdDefaultsCommandAndLimits(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_DATA_HOME", base)
	runner := &fakeCommandRunner{}
	var stdout, stderr strings.Builder
	code := imageRunCmd([]string{"--image", "example/affent:local"}, runner, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d stderr=%s", code, stderr.String())
	}
	workspace := filepath.Join(base, "affent", "runtime", "workspace")
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want command output only", stdout.String())
	}
	if len(runner.calls) != 1 {
		t.Fatalf("calls = %+v, want one docker run", runner.calls)
	}
	args := runner.calls[0].args
	for _, want := range []string{
		"--rm",
		"--memory", defaultSandboxMemory,
		"--memory-swap", defaultSandboxMemory,
		"--cpus", defaultSandboxCPUs,
		"--pids-limit", defaultSandboxPIDs,
		"-v", workspace + ":/workspace",
		"-e", "GOMEMLIMIT=768MiB",
		"-e", "GOMAXPROCS=2",
		"example/affent:local",
		"affentctl",
		"--help",
	} {
		if !contains(args, want) {
			t.Fatalf("docker run args missing %q:\n%v", want, args)
		}
	}
}

func TestImageRunCmdAcceptsDetachFlag(t *testing.T) {
	workspace := t.TempDir()
	runner := &fakeCommandRunner{}
	var stdout, stderr strings.Builder
	code := imageRunCmd([]string{
		"--image", "example/affent:local",
		"--workspace", workspace,
		"--detach",
		"--",
		"affentserve",
	}, runner, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d stderr=%s", code, stderr.String())
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want command output only", stdout.String())
	}
	if len(runner.calls) != 1 {
		t.Fatalf("calls = %+v, want one docker run", runner.calls)
	}
	args := runner.calls[0].args
	if !contains(args, "--detach") {
		t.Fatalf("docker run args missing --detach:\n%v", args)
	}
	if contains(args, "-i") {
		t.Fatalf("detached image run should not pass -i:\n%v", args)
	}
}

func TestRunRuntimeImageRequiresMeaningfulInputs(t *testing.T) {
	base := runtimeRunOptions{
		Image:     "image",
		Workspace: t.TempDir(),
		Memory:    "1g",
		CPUs:      "2",
		PIDsLimit: "512",
		Command:   []string{"affentctl", "--help"},
	}
	for _, c := range []struct {
		name string
		edit func(*runtimeRunOptions)
		want string
	}{
		{name: "image", edit: func(o *runtimeRunOptions) { o.Image = "" }, want: "--image is required"},
		{name: "image whitespace", edit: func(o *runtimeRunOptions) { o.Image = "bad image" }, want: "--image must not contain whitespace"},
		{name: "image leading dash", edit: func(o *runtimeRunOptions) { o.Image = "-bad" }, want: "--image must not start"},
		{name: "name bad char", edit: func(o *runtimeRunOptions) { o.Name = "bad/name" }, want: "--name may contain only"},
		{name: "workspace", edit: func(o *runtimeRunOptions) { o.Workspace = "" }, want: "--workspace is required"},
		{name: "memory", edit: func(o *runtimeRunOptions) { o.Memory = "" }, want: "--memory is required"},
		{name: "invalid memory", edit: func(o *runtimeRunOptions) { o.Memory = "wat" }, want: "positive Docker memory limit"},
		{name: "zero memory", edit: func(o *runtimeRunOptions) { o.Memory = "0" }, want: "positive Docker memory limit"},
		{name: "too little memory", edit: func(o *runtimeRunOptions) { o.Memory = "64m" }, want: "at least 128m"},
		{name: "cpus", edit: func(o *runtimeRunOptions) { o.CPUs = "" }, want: "--cpus is required"},
		{name: "invalid cpus", edit: func(o *runtimeRunOptions) { o.CPUs = "many" }, want: "positive Docker CPU limit"},
		{name: "zero cpus", edit: func(o *runtimeRunOptions) { o.CPUs = "0" }, want: "positive Docker CPU limit"},
		{name: "pids", edit: func(o *runtimeRunOptions) { o.PIDsLimit = "" }, want: "--pids-limit is required"},
		{name: "invalid pids", edit: func(o *runtimeRunOptions) { o.PIDsLimit = "64.5" }, want: "positive integer process limit"},
		{name: "unlimited pids", edit: func(o *runtimeRunOptions) { o.PIDsLimit = "-1" }, want: "positive integer process limit"},
		{name: "too few pids", edit: func(o *runtimeRunOptions) { o.PIDsLimit = "32" }, want: "at least 64"},
		{name: "user whitespace", edit: func(o *runtimeRunOptions) { o.User = "bad user" }, want: "--user must not contain whitespace"},
		{name: "user missing group", edit: func(o *runtimeRunOptions) { o.User = "1000:" }, want: "--user must include a group after ':'"},
		{name: "user too many colons", edit: func(o *runtimeRunOptions) { o.User = "1000:1000:extra" }, want: "--user must have the form user or user:group"},
		{name: "user invalid char", edit: func(o *runtimeRunOptions) { o.User = "bad/user" }, want: "--user user must use letters"},
		{name: "timeout", edit: func(o *runtimeRunOptions) { o.Timeout = -time.Second }, want: "--timeout must be zero or a positive duration"},
		{name: "command", edit: func(o *runtimeRunOptions) { o.Command = nil }, want: "runtime command is required"},
		{name: "blank command executable", edit: func(o *runtimeRunOptions) { o.Command = []string{"   ", "--help"} }, want: "runtime command executable is required"},
		{name: "env", edit: func(o *runtimeRunOptions) { o.Env = []string{"=bad"} }, want: "missing variable name"},
		{name: "env missing value separator", edit: func(o *runtimeRunOptions) { o.Env = []string{"AFFENTCTL_API_KEY"} }, want: "expected KEY=VALUE"},
		{name: "env invalid name", edit: func(o *runtimeRunOptions) { o.Env = []string{"BAD NAME=1"} }, want: "variable name must match"},
		{name: "env name trailing space", edit: func(o *runtimeRunOptions) { o.Env = []string{"FOO =bar"} }, want: "variable name must match"},
		{name: "env name leading space", edit: func(o *runtimeRunOptions) { o.Env = []string{" FOO=bar"} }, want: "variable name must match"},
		{name: "env duplicate", edit: func(o *runtimeRunOptions) { o.Env = []string{"A=1", "A=2"} }, want: "duplicate variable A"},
		{name: "publish", edit: func(o *runtimeRunOptions) { o.Publish = []string{""} }, want: "--publish values must not be empty"},
		{name: "publish whitespace", edit: func(o *runtimeRunOptions) { o.Publish = []string{"7777:7777 bad"} }, want: "whitespace is not allowed"},
		{name: "publish bad protocol", edit: func(o *runtimeRunOptions) { o.Publish = []string{"7777:7777/http"} }, want: "protocol must be tcp, udp, or sctp"},
		{name: "publish zero port", edit: func(o *runtimeRunOptions) { o.Publish = []string{"7777:0"} }, want: "container port must be between 1 and 65535"},
		{name: "publish bad range", edit: func(o *runtimeRunOptions) { o.Publish = []string{"9000-8000"} }, want: "port range end must be greater"},
	} {
		t.Run(c.name, func(t *testing.T) {
			opts := base
			c.edit(&opts)
			runner := &fakeCommandRunner{}
			err := runRuntimeImage(opts, runner)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v, want contains %q", err, c.want)
			}
			if len(runner.calls) != 0 {
				t.Fatalf("invalid runtime options must not call docker, calls=%+v", runner.calls)
			}
		})
	}
}

func TestValidateDockerUserAllowsCommonForms(t *testing.T) {
	for _, in := range []string{
		"",
		"1000",
		"1000:1000",
		"node",
		"node:node",
		"app-user:app_group",
		"svc.user:1000",
	} {
		t.Run(in, func(t *testing.T) {
			if err := validateDockerUser("--user", in); err != nil {
				t.Fatalf("validateDockerUser(%q): %v", in, err)
			}
		})
	}
}

func TestValidateDockerContainerNameAllowsCommonForms(t *testing.T) {
	for _, in := range []string{
		"affent-sandbox",
		"affent_sandbox",
		"affent.sandbox",
		"a",
		"A1",
		"agent-01",
	} {
		t.Run(in, func(t *testing.T) {
			if err := validateDockerContainerName("--name", in); err != nil {
				t.Fatalf("validateDockerContainerName(%q): %v", in, err)
			}
		})
	}
}

func TestValidateDockerImageRefAllowsCommonForms(t *testing.T) {
	for _, in := range []string{
		"alpine",
		"alpine:3.20",
		"example.com/team/affent:local",
		"localhost:5000/affent/runtime:v1",
		"affinefoundation/affent@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	} {
		t.Run(in, func(t *testing.T) {
			if err := validateDockerImageRef("--image", in); err != nil {
				t.Fatalf("validateDockerImageRef(%q): %v", in, err)
			}
		})
	}
}

func TestValidateDockerPublishAllowsCommonForms(t *testing.T) {
	for _, in := range []string{
		"7777",
		"7777:7777",
		"127.0.0.1:7777:7777",
		"127.0.0.1::7777",
		"[::1]:7777:7777",
		"8000-8003",
		"8080/tcp",
		"5353/udp",
		"7777/sctp",
	} {
		t.Run(in, func(t *testing.T) {
			got, err := validateDockerPublish(in)
			if err != nil {
				t.Fatalf("validateDockerPublish(%q): %v", in, err)
			}
			if got != in {
				t.Fatalf("validateDockerPublish(%q) = %q", in, got)
			}
		})
	}
}

func TestRunRuntimeImageRejectsInvalidEnvBeforeAutoBuild(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "ws")
	runner := &fakeCommandRunner{imageInspectErr: errors.New("missing image")}
	opts := defaultRuntimeRunOptions(workspace)
	opts.Env = []string{"AFFENTCTL_API_KEY"}
	opts.Command = []string{"affentctl", "--help"}
	err := runRuntimeImage(opts, runner)
	if err == nil || !strings.Contains(err.Error(), "expected KEY=VALUE") {
		t.Fatalf("runRuntimeImage error = %v, want KEY=VALUE", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("invalid env must be rejected before Docker calls, calls=%+v", runner.calls)
	}
	if _, statErr := os.Stat(workspace); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("invalid env must be rejected before creating workspace; stat err=%v", statErr)
	}
}

func TestRunRuntimeImageRejectsManagedEnvBeforeAutoBuild(t *testing.T) {
	for _, name := range []string{
		"HOME",
		"XDG_CACHE_HOME",
		"GOCACHE",
		"GOMODCACHE",
		"NPM_CONFIG_CACHE",
		"PIP_CACHE_DIR",
		"GOMEMLIMIT",
		"GOMAXPROCS",
	} {
		t.Run(name, func(t *testing.T) {
			workspace := filepath.Join(t.TempDir(), "ws")
			runner := &fakeCommandRunner{imageInspectErr: errors.New("missing image")}
			opts := defaultRuntimeRunOptions(workspace)
			opts.Env = []string{name + "=override"}
			opts.Command = []string{"affentctl", "--help"}
			err := runRuntimeImage(opts, runner)
			if err == nil || !strings.Contains(err.Error(), "managed by affentctl image run") {
				t.Fatalf("runRuntimeImage error = %v, want managed env rejection", err)
			}
			if len(runner.calls) != 0 {
				t.Fatalf("managed env must be rejected before Docker calls, calls=%+v", runner.calls)
			}
			if _, statErr := os.Stat(workspace); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("managed env must be rejected before creating workspace; stat err=%v", statErr)
			}
		})
	}
}

func TestRunRuntimeImageAutoBuildsDefaultImageWhenMissing(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "ws")
	dockerfile, contextDir, ok, err := findRuntimeBuildSource()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("test requires source checkout with runtime Dockerfile")
	}
	runner := &fakeCommandRunner{imageInspectErr: errors.New("missing image")}
	opts := defaultRuntimeRunOptions(workspace)
	opts.Command = []string{"affentctl", "--help"}
	if err := runRuntimeImage(opts, runner); err != nil {
		t.Fatalf("runRuntimeImage: %v", err)
	}
	if len(runner.calls) != 3 {
		t.Fatalf("calls = %+v, want image inspect, build, run", runner.calls)
	}
	if got := runner.calls[0]; got.name != "docker" || !reflect.DeepEqual(got.args, []string{"image", "inspect", defaultRuntimeImage}) {
		t.Fatalf("image inspect call = %+v", got)
	}
	wantBuild := []string{
		"build",
		"--memory", defaultSandboxMemory,
		"--memory-swap", defaultSandboxMemory,
		"-f", dockerfile,
		"-t", defaultRuntimeImage,
		contextDir,
	}
	if got := runner.calls[1]; got.name != "docker" || !reflect.DeepEqual(got.args, wantBuild) {
		t.Fatalf("build call = %+v, want docker %v", got, wantBuild)
	}
	if got := runner.calls[2]; got.name != "docker" || len(got.args) == 0 || got.args[0] != "run" {
		t.Fatalf("run call = %+v", got)
	}
}

func TestRunRuntimeImageSkipsAutoBuildWhenDefaultImageExists(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "ws")
	runner := &fakeCommandRunner{}
	opts := defaultRuntimeRunOptions(workspace)
	opts.Command = []string{"affentctl", "--help"}
	if err := runRuntimeImage(opts, runner); err != nil {
		t.Fatalf("runRuntimeImage: %v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("calls = %+v, want image inspect and run", runner.calls)
	}
	if got := runner.calls[0]; got.name != "docker" || !reflect.DeepEqual(got.args, []string{"image", "inspect", defaultRuntimeImage}) {
		t.Fatalf("image inspect call = %+v", got)
	}
	if got := runner.calls[1]; got.name != "docker" || len(got.args) == 0 || got.args[0] != "run" {
		t.Fatalf("run call = %+v", got)
	}
}

func TestStartSandboxCreatesMemoryLimitedPersistentContainer(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "ws")
	runner := &fakeCommandRunner{inspectErr: errors.New("missing")}
	opts := sandboxStartOptions{
		Name:      "affent-test",
		Image:     "example/image:latest",
		Workspace: workspace,
		Memory:    "768m",
		CPUs:      "1.5",
		PIDsLimit: "256",
		User:      "123:456",
	}
	if err := startSandbox(opts, runner); err != nil {
		t.Fatalf("startSandbox: %v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("calls = %+v, want inspect + run", runner.calls)
	}
	run := runner.calls[1]
	if run.name != "docker" || len(run.args) == 0 || run.args[0] != "run" {
		t.Fatalf("second call = %+v, want docker run", run)
	}
	wantPieces := []string{
		"--name", "affent-test",
		"--label", sandboxLabelManaged + "=true",
		"--label", sandboxLabelImage + "=example/image:latest",
		"--label", sandboxLabelWorkspace + "=" + workspace,
		"--label", sandboxLabelMemory + "=768m",
		"--label", sandboxLabelCPUs + "=1.5",
		"--label", sandboxLabelPIDsLimit + "=256",
		"--label", sandboxLabelUser + "=123:456",
		"--memory", "768m",
		"--memory-swap", "768m",
		"--cpus", "1.5",
		"--pids-limit", "256",
		"-v", workspace + ":" + workspace,
		"-w", workspace,
		"--user", "123:456",
		"-e", "HOME=" + filepath.Join(workspace, ".home"),
		"-e", "XDG_CACHE_HOME=" + filepath.Join(workspace, ".cache"),
		"-e", "GOCACHE=" + filepath.Join(workspace, ".cache", "go-build"),
		"-e", "GOMODCACHE=" + filepath.Join(workspace, ".cache", "go-mod"),
		"-e", "NPM_CONFIG_CACHE=" + filepath.Join(workspace, ".cache", "npm"),
		"-e", "PIP_CACHE_DIR=" + filepath.Join(workspace, ".cache", "pip"),
		"-e", "GOMEMLIMIT=576MiB",
		"-e", "GOMAXPROCS=2",
		"example/image:latest",
		"sleep", "infinity",
	}
	for _, want := range wantPieces {
		if !contains(run.args, want) {
			t.Fatalf("docker run args missing %q:\n%v", want, run.args)
		}
	}
	for _, dir := range sandboxPersistentDirs(workspace) {
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			t.Fatalf("persistent dir %s not created; stat=%v err=%v", dir, st, err)
		}
	}
}

func TestSandboxPersistentEnvDerivesGoLimits(t *testing.T) {
	got := sandboxPersistentEnv("/tmp/ws", "1g", "1.5")
	for _, want := range []string{
		"HOME=/tmp/ws/.home",
		"XDG_CACHE_HOME=/tmp/ws/.cache",
		"GOCACHE=/tmp/ws/.cache/go-build",
		"GOMODCACHE=/tmp/ws/.cache/go-mod",
		"NPM_CONFIG_CACHE=/tmp/ws/.cache/npm",
		"PIP_CACHE_DIR=/tmp/ws/.cache/pip",
		"GOMEMLIMIT=768MiB",
		"GOMAXPROCS=2",
	} {
		if !contains(got, want) {
			t.Fatalf("persistent env missing %q:\n%v", want, got)
		}
	}
}

func TestParseDockerMemoryBytes(t *testing.T) {
	for _, c := range []struct {
		in   string
		want int64
	}{
		{in: "1g", want: 1024 * 1024 * 1024},
		{in: "768m", want: 768 * 1024 * 1024},
		{in: "500mb", want: 500 * 1000 * 1000},
		{in: "1048576", want: 1048576},
	} {
		got, ok := parseDockerMemoryBytes(c.in)
		if !ok || got != c.want {
			t.Fatalf("parseDockerMemoryBytes(%q) = %d, %t; want %d, true", c.in, got, ok, c.want)
		}
	}
}

func TestStartSandboxAutoBuildsDefaultImageWhenMissing(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "ws")
	dockerfile, contextDir, ok, err := findSandboxBuildSource()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("test requires source checkout with sandbox Dockerfile")
	}
	runner := &fakeCommandRunner{
		inspectErr:      errors.New("missing container"),
		imageInspectErr: errors.New("missing image"),
	}
	opts := defaultSandboxStartOptions(workspace)
	if err := startSandbox(opts, runner); err != nil {
		t.Fatalf("startSandbox: %v", err)
	}
	if len(runner.calls) != 4 {
		t.Fatalf("calls = %+v, want container inspect, image inspect, build, run", runner.calls)
	}
	if got := runner.calls[1]; got.name != "docker" || !reflect.DeepEqual(got.args, []string{"image", "inspect", defaultSandboxImage}) {
		t.Fatalf("image inspect call = %+v", got)
	}
	build := runner.calls[2]
	wantBuild := []string{
		"build",
		"--memory", defaultSandboxMemory,
		"--memory-swap", defaultSandboxMemory,
		"-f", dockerfile,
		"-t", defaultSandboxImage,
		contextDir,
	}
	if build.name != "docker" || !reflect.DeepEqual(build.args, wantBuild) {
		t.Fatalf("build call = %+v, want docker %v", build, wantBuild)
	}
	if got := runner.calls[3]; got.name != "docker" || len(got.args) == 0 || got.args[0] != "run" {
		t.Fatalf("run call = %+v", got)
	}
}

func TestStartSandboxSkipsAutoBuildWhenDefaultImageExists(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "ws")
	runner := &fakeCommandRunner{inspectErr: errors.New("missing container")}
	opts := defaultSandboxStartOptions(workspace)
	if err := startSandbox(opts, runner); err != nil {
		t.Fatalf("startSandbox: %v", err)
	}
	if len(runner.calls) != 3 {
		t.Fatalf("calls = %+v, want container inspect, image inspect, run", runner.calls)
	}
	if got := runner.calls[1]; got.name != "docker" || !reflect.DeepEqual(got.args, []string{"image", "inspect", defaultSandboxImage}) {
		t.Fatalf("image inspect call = %+v", got)
	}
	if got := runner.calls[2]; got.name != "docker" || len(got.args) == 0 || got.args[0] != "run" {
		t.Fatalf("run call = %+v", got)
	}
}

func TestStartSandboxSkipsAutoBuildOutsideSourceTree(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatalf("restore working directory: %v", err)
		}
	})
	workspace := filepath.Join(t.TempDir(), "ws")
	runner := &fakeCommandRunner{
		inspectErr:      errors.New("missing container"),
		imageInspectErr: errors.New("missing image"),
	}
	opts := defaultSandboxStartOptions(workspace)
	if err := startSandbox(opts, runner); err != nil {
		t.Fatalf("startSandbox: %v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("calls = %+v, want container inspect and run only", runner.calls)
	}
	if got := runner.calls[1]; got.name != "docker" || len(got.args) == 0 || got.args[0] != "run" {
		t.Fatalf("run call = %+v", got)
	}
}

func TestStartSandboxReusesRunningContainer(t *testing.T) {
	opts := sandboxStartOptions{
		Name:      "affent-test",
		Image:     "image",
		Workspace: t.TempDir(),
		Memory:    defaultSandboxMemory,
		CPUs:      defaultSandboxCPUs,
		PIDsLimit: defaultSandboxPIDs,
	}
	runner := &fakeCommandRunner{inspectOut: sandboxInspectOutput("true", opts)}
	if err := startSandbox(opts, runner); err != nil {
		t.Fatalf("startSandbox: %v", err)
	}
	if len(runner.calls) != 1 || runner.calls[0].args[0] != "inspect" {
		t.Fatalf("calls = %+v, want only inspect", runner.calls)
	}
}

func TestStartSandboxStartsStoppedContainer(t *testing.T) {
	opts := sandboxStartOptions{
		Name:      "affent-test",
		Image:     "image",
		Workspace: t.TempDir(),
		Memory:    defaultSandboxMemory,
		CPUs:      defaultSandboxCPUs,
		PIDsLimit: defaultSandboxPIDs,
	}
	runner := &fakeCommandRunner{inspectOut: sandboxInspectOutput("false", opts)}
	if err := startSandbox(opts, runner); err != nil {
		t.Fatalf("startSandbox: %v", err)
	}
	want := []recordedCommand{
		{name: "docker", args: []string{"inspect", "-f", sandboxInspectTemplate, "affent-test"}},
		{name: "docker", args: []string{"start", "affent-test"}},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %+v, want %+v", runner.calls, want)
	}
}

func TestStartSandboxRejectsMismatchedExistingContainer(t *testing.T) {
	opts := sandboxStartOptions{
		Name:      "affent-test",
		Image:     "image",
		Workspace: t.TempDir(),
		Memory:    defaultSandboxMemory,
		CPUs:      defaultSandboxCPUs,
		PIDsLimit: defaultSandboxPIDs,
	}
	existing := opts
	existing.Workspace = filepath.Join(t.TempDir(), "other")
	runner := &fakeCommandRunner{inspectOut: sandboxInspectOutput("true", existing)}
	err := startSandbox(opts, runner)
	if err == nil || !strings.Contains(err.Error(), "--replace") || !strings.Contains(err.Error(), sandboxLabelWorkspace) {
		t.Fatalf("error = %v, want mismatch error mentioning --replace and workspace label", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("mismatched container must not be started or replaced implicitly; calls=%+v", runner.calls)
	}
}

func TestStartSandboxRejectsExistingContainerWithDriftedRuntimeLimits(t *testing.T) {
	opts := sandboxStartOptions{
		Name:      "affent-test",
		Image:     "image",
		Workspace: t.TempDir(),
		Memory:    "1g",
		CPUs:      defaultSandboxCPUs,
		PIDsLimit: "512",
	}
	cases := []struct {
		name       string
		inspectOut string
		want       string
	}{
		{
			name:       "memory",
			inspectOut: sandboxInspectOutputWithRuntimeLimits("true", opts, "0", "1073741824", "2000000000", "512"),
			want:       "HostConfig.Memory",
		},
		{
			name:       "memory swap",
			inspectOut: sandboxInspectOutputWithRuntimeLimits("true", opts, "1073741824", "0", "2000000000", "512"),
			want:       "HostConfig.MemorySwap",
		},
		{
			name:       "cpus",
			inspectOut: sandboxInspectOutputWithRuntimeLimits("true", opts, "1073741824", "1073741824", "1000000000", "512"),
			want:       "HostConfig.NanoCpus",
		},
		{
			name:       "pids",
			inspectOut: sandboxInspectOutputWithRuntimeLimits("true", opts, "1073741824", "1073741824", "2000000000", "0"),
			want:       "HostConfig.PidsLimit",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			runner := &fakeCommandRunner{inspectOut: c.inspectOut}
			err := startSandbox(opts, runner)
			if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "--replace") {
				t.Fatalf("error = %v, want %q and --replace", err, c.want)
			}
			if len(runner.calls) != 1 {
				t.Fatalf("drifted container must not be started or replaced implicitly; calls=%+v", runner.calls)
			}
		})
	}
}

func TestStartSandboxRequiresResourceLimits(t *testing.T) {
	for _, c := range []struct {
		name string
		opts sandboxStartOptions
		want string
	}{
		{
			name: "container name",
			opts: sandboxStartOptions{Image: "image", Workspace: t.TempDir(), Memory: "1g", CPUs: "2", PIDsLimit: "512"},
			want: "--name is required",
		},
		{
			name: "container name starts with punctuation",
			opts: sandboxStartOptions{Name: "-bad", Image: "image", Workspace: t.TempDir(), Memory: "1g", CPUs: "2", PIDsLimit: "512"},
			want: "--name must start with a letter or digit",
		},
		{
			name: "container name bad char",
			opts: sandboxStartOptions{Name: "bad/name", Image: "image", Workspace: t.TempDir(), Memory: "1g", CPUs: "2", PIDsLimit: "512"},
			want: "--name may contain only",
		},
		{
			name: "image whitespace",
			opts: sandboxStartOptions{Name: "affent-test", Image: "bad image", Workspace: t.TempDir(), Memory: "1g", CPUs: "2", PIDsLimit: "512"},
			want: "--image must not contain whitespace",
		},
		{
			name: "memory",
			opts: sandboxStartOptions{Name: "affent-test", Image: "image", Workspace: t.TempDir(), CPUs: "2", PIDsLimit: "512"},
			want: "--memory is required",
		},
		{
			name: "invalid memory",
			opts: sandboxStartOptions{Name: "affent-test", Image: "image", Workspace: t.TempDir(), Memory: "nope", CPUs: "2", PIDsLimit: "512"},
			want: "positive Docker memory limit",
		},
		{
			name: "too little memory",
			opts: sandboxStartOptions{Name: "affent-test", Image: "image", Workspace: t.TempDir(), Memory: "64m", CPUs: "2", PIDsLimit: "512"},
			want: "at least 128m",
		},
		{
			name: "cpus",
			opts: sandboxStartOptions{Name: "affent-test", Image: "image", Workspace: t.TempDir(), Memory: "1g", PIDsLimit: "512"},
			want: "--cpus is required",
		},
		{
			name: "zero cpus",
			opts: sandboxStartOptions{Name: "affent-test", Image: "image", Workspace: t.TempDir(), Memory: "1g", CPUs: "0", PIDsLimit: "512"},
			want: "positive Docker CPU limit",
		},
		{
			name: "pids",
			opts: sandboxStartOptions{Name: "affent-test", Image: "image", Workspace: t.TempDir(), Memory: "1g", CPUs: "2"},
			want: "--pids-limit is required",
		},
		{
			name: "unlimited pids",
			opts: sandboxStartOptions{Name: "affent-test", Image: "image", Workspace: t.TempDir(), Memory: "1g", CPUs: "2", PIDsLimit: "-1"},
			want: "positive integer process limit",
		},
		{
			name: "too few pids",
			opts: sandboxStartOptions{Name: "affent-test", Image: "image", Workspace: t.TempDir(), Memory: "1g", CPUs: "2", PIDsLimit: "32"},
			want: "at least 64",
		},
		{
			name: "bad user",
			opts: sandboxStartOptions{Name: "affent-test", Image: "image", Workspace: t.TempDir(), Memory: "1g", CPUs: "2", PIDsLimit: "512", User: "bad user"},
			want: "--user must not contain whitespace",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			runner := &fakeCommandRunner{}
			err := startSandbox(c.opts, runner)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v, want contains %q", err, c.want)
			}
			if len(runner.calls) != 0 {
				t.Fatalf("invalid sandbox options must not call docker, calls=%+v", runner.calls)
			}
		})
	}
}

func TestSandboxStartPrintEnv(t *testing.T) {
	var out strings.Builder
	printSandboxStartResult(&out, sandboxStartOptions{
		Name:      "affent-test",
		Workspace: "/tmp/affent ws",
		PrintEnv:  true,
	})
	got := out.String()
	for _, want := range []string{
		"export AFFENTCTL_EXECUTOR='docker:affent-test'",
		"export AFFENTCTL_WORKSPACE='/tmp/affent ws'",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestMaybeStartSandboxExecutor(t *testing.T) {
	t.Run("pass through non-sandbox executor", func(t *testing.T) {
		runner := &fakeCommandRunner{}
		got, err := maybeStartSandboxExecutor("docker:abc", "/workspace", runner)
		if err != nil {
			t.Fatal(err)
		}
		if got != "docker:abc" {
			t.Fatalf("executor = %q, want docker:abc", got)
		}
		if len(runner.calls) != 0 {
			t.Fatalf("non-sandbox executor should not call docker: %+v", runner.calls)
		}
	})
	t.Run("starts default sandbox", func(t *testing.T) {
		workspace := filepath.Join(t.TempDir(), "ws")
		runner := &fakeCommandRunner{inspectErr: errors.New("missing")}
		got, err := maybeStartSandboxExecutor("sandbox", workspace, runner)
		if err != nil {
			t.Fatal(err)
		}
		if got != "docker:"+defaultSandboxName {
			t.Fatalf("executor = %q, want docker:%s", got, defaultSandboxName)
		}
		if len(runner.calls) != 3 || runner.calls[2].args[0] != "run" {
			t.Fatalf("sandbox executor should inspect then run: %+v", runner.calls)
		}
		if !contains(runner.calls[2].args, workspace+":"+workspace) {
			t.Fatalf("sandbox run args missing workspace bind mount:\n%v", runner.calls[2].args)
		}
	})
}

func TestInspectSandboxStatus(t *testing.T) {
	opts := sandboxStartOptions{
		Name:      "affent-test",
		Image:     "image",
		Workspace: "/tmp/ws",
		Memory:    "1g",
		CPUs:      "2",
		PIDsLimit: "512",
		User:      "123:456",
	}
	runner := &fakeCommandRunner{inspectOut: sandboxStatusOutput("running", "true", opts, "1073741824", "1073741824", "2000000000", "512", "/tmp/ws")}
	got, err := inspectSandboxStatus("affent-test", runner)
	if err != nil {
		t.Fatalf("inspectSandboxStatus: %v", err)
	}
	if got.Name != "affent-test" || !got.Running || got.Workspace != "/tmp/ws" || got.User != "123:456" || got.MemoryBytes != "1073741824" || got.NanoCPUsActual != "2000000000" || got.WorkingDir != "/tmp/ws" {
		t.Fatalf("unexpected status: %+v", got)
	}
	if len(runner.calls) != 1 || runner.calls[0].args[0] != "inspect" || runner.calls[0].args[2] != sandboxStatusTemplate {
		t.Fatalf("inspect call = %+v", runner.calls)
	}
}

func TestInspectSandboxStatusRejectsUnmanagedContainer(t *testing.T) {
	runner := &fakeCommandRunner{inspectOut: strings.Join([]string{
		"running", "true", "", "", "", "", "", "", "", "0", "0", "0", "0", "/",
	}, "\n")}
	_, err := inspectSandboxStatus("affent-test", runner)
	if err == nil || !strings.Contains(err.Error(), "not an affent sandbox") {
		t.Fatalf("error = %v, want unmanaged-container error", err)
	}
}

func TestInspectSandboxStatusRejectsInvalidNameBeforeDocker(t *testing.T) {
	runner := &fakeCommandRunner{}
	_, err := inspectSandboxStatus("bad/name", runner)
	if err == nil || !strings.Contains(err.Error(), "--name may contain only") {
		t.Fatalf("inspectSandboxStatus error = %v, want invalid name", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("invalid name must not call docker, calls=%+v", runner.calls)
	}
}

func TestStopSandbox(t *testing.T) {
	opts := sandboxStartOptions{
		Name:      "affent-test",
		Image:     "image",
		Workspace: "/tmp/ws",
		Memory:    "1g",
		CPUs:      "2",
		PIDsLimit: "512",
	}
	t.Run("stop", func(t *testing.T) {
		runner := &fakeCommandRunner{inspectOut: sandboxStatusOutput("running", "true", opts, "1", "1", "2000000000", "512", "/tmp/ws")}
		if err := stopSandbox("affent-test", false, runner); err != nil {
			t.Fatalf("stopSandbox: %v", err)
		}
		want := []recordedCommand{
			{name: "docker", args: []string{"inspect", "-f", sandboxStatusTemplate, "affent-test"}},
			{name: "docker", args: []string{"stop", "affent-test"}},
		}
		if !reflect.DeepEqual(runner.calls, want) {
			t.Fatalf("calls = %+v, want %+v", runner.calls, want)
		}
	})
	t.Run("remove", func(t *testing.T) {
		runner := &fakeCommandRunner{inspectOut: sandboxStatusOutput("running", "true", opts, "1", "1", "2000000000", "512", "/tmp/ws")}
		if err := stopSandbox("affent-test", true, runner); err != nil {
			t.Fatalf("stopSandbox remove: %v", err)
		}
		if got := runner.calls[len(runner.calls)-1]; got.name != "docker" || !reflect.DeepEqual(got.args, []string{"rm", "-f", "affent-test"}) {
			t.Fatalf("last call = %+v, want docker rm -f", got)
		}
	})
}

func TestStopSandboxRejectsInvalidNameBeforeDocker(t *testing.T) {
	runner := &fakeCommandRunner{}
	err := stopSandbox("bad/name", false, runner)
	if err == nil || !strings.Contains(err.Error(), "--name may contain only") {
		t.Fatalf("stopSandbox error = %v, want invalid name", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("invalid name must not call docker, calls=%+v", runner.calls)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func sandboxStatusOutput(state, running string, opts sandboxStartOptions, memoryBytes, swapBytes, nanoCPUs, pidsActual, workingDir string) string {
	return strings.Join([]string{
		state,
		running,
		"true",
		opts.Image,
		opts.Workspace,
		opts.Memory,
		opts.CPUs,
		opts.PIDsLimit,
		opts.User,
		memoryBytes,
		swapBytes,
		nanoCPUs,
		pidsActual,
		workingDir,
	}, "\n")
}

func sandboxInspectOutput(running string, opts sandboxStartOptions) string {
	memoryBytes := "0"
	if n, ok := parseDockerMemoryBytes(opts.Memory); ok {
		memoryBytes = strconv.FormatInt(n, 10)
	}
	nanoCPUs := "0"
	if n, ok := dockerNanoCPUs(opts.CPUs); ok {
		nanoCPUs = strconv.FormatInt(n, 10)
	}
	return sandboxInspectOutputWithRuntimeLimits(running, opts, memoryBytes, memoryBytes, nanoCPUs, opts.PIDsLimit)
}

func sandboxInspectOutputWithRuntimeLimits(running string, opts sandboxStartOptions, memoryBytes, swapBytes, nanoCPUs, pidsActual string) string {
	return strings.Join([]string{
		running,
		"true",
		opts.Image,
		opts.Workspace,
		opts.Memory,
		opts.CPUs,
		opts.PIDsLimit,
		opts.User,
		memoryBytes,
		swapBytes,
		nanoCPUs,
		pidsActual,
	}, "\n")
}

type goVersion struct {
	major int
	minor int
}

func (v goVersion) String() string {
	return strconv.Itoa(v.major) + "." + strconv.Itoa(v.minor)
}

func (v goVersion) less(other goVersion) bool {
	if v.major != other.major {
		return v.major < other.major
	}
	return v.minor < other.minor
}

func parseSandboxDockerfileGoVersion(dockerfile string) (goVersion, error) {
	for _, line := range strings.Split(dockerfile, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || strings.ToUpper(fields[0]) != "FROM" {
			continue
		}
		image := fields[1]
		const prefix = "golang:"
		if !strings.HasPrefix(image, prefix) {
			continue
		}
		tag := strings.TrimPrefix(image, prefix)
		if idx := strings.IndexAny(tag, "-@"); idx >= 0 {
			tag = tag[:idx]
		}
		return parseMajorMinor(tag)
	}
	return goVersion{}, errors.New("sandbox Dockerfile must use a golang:<version> base image")
}

func parseGoModDirective(body string) (goVersion, error) {
	for _, line := range strings.Split(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "go" {
			return parseMajorMinor(fields[1])
		}
	}
	return goVersion{}, errors.New("go.mod missing go directive")
}

func parseMajorMinor(raw string) (goVersion, error) {
	parts := strings.Split(raw, ".")
	if len(parts) < 2 {
		return goVersion{}, fmt.Errorf("parse Go version %q: want major.minor", raw)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return goVersion{}, fmt.Errorf("parse Go major version %q: %w", raw, err)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return goVersion{}, fmt.Errorf("parse Go minor version %q: %w", raw, err)
	}
	return goVersion{major: major, minor: minor}, nil
}
