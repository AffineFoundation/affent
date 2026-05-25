GO ?= go
AFFENTCTL ?= ./bin/affentctl

SANDBOX_START_ARGS ?=
SANDBOX_STATUS_ARGS ?=
SANDBOX_STOP_ARGS ?=
IMAGE_BUILD_REVISION ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
IMAGE_BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
IMAGE_BUILD_ARGS ?= --build-arg AFFENT_BUILD_REVISION="$(IMAGE_BUILD_REVISION)" --build-arg AFFENT_BUILD_DATE="$(IMAGE_BUILD_DATE)"
IMAGE_RUN_ARGS ?=
IMAGE_COMMAND ?= affentctl --help
IMAGE_WORKSPACE ?= $(CURDIR)/.tmp/runtime-workspace
EVAL_IMAGE ?= affinefoundation/affent:latest
EVAL_ARGS ?= --list
EVAL_WORK_ROOT ?= /workspace/.tmp/eval
EVAL_DOCKER_ARGS ?=
EVAL_RUNTIME_EVAL_MODE ?= false
EVAL_RUNTIME_EVAL_MODE_ARGS = $(if $(filter true yes 1,$(EVAL_RUNTIME_EVAL_MODE)),--runtime-eval-mode,)
EVAL_RUNTIME_MEMORY ?= false
EVAL_RUNTIME_MEMORY_ARGS = $(if $(filter true yes 1,$(EVAL_RUNTIME_MEMORY)),--runtime-memory,)
EVAL_RUNTIME_MCP_CONFIG ?=
EVAL_RUNTIME_MCP_CONFIG_ARGS = $(if $(strip $(EVAL_RUNTIME_MCP_CONFIG)),--runtime-mcp-config "$(EVAL_RUNTIME_MCP_CONFIG)",)
SERVE_EVAL_CONTAINER_NAME ?= affent-eval-serve
SERVE_EVAL_WORKSPACE ?= $(CURDIR)/.tmp/eval-serve
SERVE_EVAL_PUBLISH ?= 127.0.0.1:7777:7777
SERVE_EVAL_PERMISSIONS ?=
SERVE_DEFAULT_ARGS ?= --web=true --browser=true --web-search=false --browser-cache-dir=/workspace/browser-cache
SERVE_ARGS ?=
SERVE_BASE_URL ?= $(or $(AFFENTSERVE_BASE_URL),$(AFFENTCTL_BASE_URL))
SERVE_API_KEY ?= $(or $(AFFENTSERVE_API_KEY),$(AFFENTCTL_API_KEY))
SERVE_MODEL ?= $(or $(AFFENTSERVE_MODEL),$(AFFENTCTL_MODEL))
SERVE_LISTEN ?= 0.0.0.0:7777
SERVE_PUBLISH ?= 127.0.0.1:7777:7777
SERVE_PUBLISH_PARTS := $(subst :, ,$(SERVE_PUBLISH))
SERVE_PUBLISH_WORDS := $(words $(SERVE_PUBLISH_PARTS))
SERVE_HEALTH_HOST := $(if $(filter 3,$(SERVE_PUBLISH_WORDS)),$(word 1,$(SERVE_PUBLISH_PARTS)),127.0.0.1)
SERVE_HEALTH_PORT := $(if $(filter 3,$(SERVE_PUBLISH_WORDS)),$(word 2,$(SERVE_PUBLISH_PARTS)),$(word 1,$(SERVE_PUBLISH_PARTS)))
SERVE_HEALTH_URL ?= http://$(SERVE_HEALTH_HOST):$(SERVE_HEALTH_PORT)/healthz
SERVE_HEALTH_ATTEMPTS ?= 30
SERVE_HEALTH_INTERVAL ?= 1
SERVE_CONTAINER_NAME ?= affent-serve
SERVE_WORKSPACE_ROOT ?= /workspace/sessions
SERVE_MEMORY_ROOT ?= /workspace/session-state
DOCTOR_ARGS ?=
SMOKE_CONTAINER_NAME ?= affent-serve-smoke
SMOKE_WORKSPACE ?= $(CURDIR)/.tmp/image-serve-smoke
SMOKE_PUBLISH ?= 127.0.0.1:7787:7777
SMOKE_PUBLISH_PARTS := $(subst :, ,$(SMOKE_PUBLISH))
SMOKE_PUBLISH_WORDS := $(words $(SMOKE_PUBLISH_PARTS))
SMOKE_HOST := $(if $(filter 3,$(SMOKE_PUBLISH_WORDS)),$(word 1,$(SMOKE_PUBLISH_PARTS)),127.0.0.1)
SMOKE_PORT := $(if $(filter 3,$(SMOKE_PUBLISH_WORDS)),$(word 2,$(SMOKE_PUBLISH_PARTS)),$(word 1,$(SMOKE_PUBLISH_PARTS)))
SMOKE_URL ?= http://$(SMOKE_HOST):$(SMOKE_PORT)
SMOKE_BASE_URL ?= http://127.0.0.1:9
SMOKE_API_KEY ?= test
SMOKE_MODEL ?= fake
SMOKE_SESSION_ID ?= smoke-persist

CONTAINER_GO_IMAGE ?= golang:1.24-bookworm
CONTAINER_MEMORY ?= 2g
CONTAINER_CPUS ?= 2
CONTAINER_PIDS ?= 512
TEST_DIR ?= .
GO_TEST_FLAGS ?= -p=1
TEST_PACKAGES ?= ./...

define require_affent_runtime_container
if test -z "$(SERVE_CONTAINER_NAME)"; then \
	echo "SERVE_CONTAINER_NAME is required, e.g. make $(1) SERVE_CONTAINER_NAME=affent-serve" >&2; \
	exit 2; \
fi; \
label=$$(docker inspect "$(SERVE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime"}}' 2>/dev/null) || { echo "container $(SERVE_CONTAINER_NAME) not found" >&2; exit 2; }; \
if test "$$label" != "true"; then \
	echo "container $(SERVE_CONTAINER_NAME) is not an Affent runtime container" >&2; \
	exit 2; \
fi
endef

.PHONY: affentctl affentctl-local doctor sandbox-start sandbox-status sandbox-stop image-build image-run image-serve image-serve-up image-serve-status image-serve-health image-serve-health-wait image-serve-logs image-serve-stop image-serve-restart image-serve-smoke eval-container eval-agent-container eval-serve-container eval-serve-browser-container test-container

affentctl:
	mkdir -p "$(dir $(AFFENTCTL))" .tmp/go-build .tmp/go-mod
	docker run --rm \
		--memory "$(CONTAINER_MEMORY)" \
		--memory-swap "$(CONTAINER_MEMORY)" \
		--cpus "$(CONTAINER_CPUS)" \
		--pids-limit "$(CONTAINER_PIDS)" \
		-e HOST_UID="$$(id -u)" \
		-e HOST_GID="$$(id -g)" \
		-e GOCACHE=/work/.tmp/go-build \
		-e GOMODCACHE=/work/.tmp/go-mod \
		-v "$(CURDIR):/work" \
		-w /work \
		"$(CONTAINER_GO_IMAGE)" \
		sh -lc 'trap '\''chown -R "$$HOST_UID:$$HOST_GID" "$(dir $(AFFENTCTL))" .tmp/go-build .tmp/go-mod'\'' EXIT; . /work/docker/go-cgroup-env.sh; /usr/local/go/bin/go build -buildvcs=false -o "$(AFFENTCTL)" ./cmd/affentctl'

affentctl-local:
	mkdir -p "$(dir $(AFFENTCTL))"
	$(GO) build -o "$(AFFENTCTL)" ./cmd/affentctl

doctor: affentctl
	"$(AFFENTCTL)" doctor $(DOCTOR_ARGS)

sandbox-start: affentctl
	"$(AFFENTCTL)" sandbox start --memory "$(CONTAINER_MEMORY)" --cpus "$(CONTAINER_CPUS)" --pids-limit "$(CONTAINER_PIDS)" $(SANDBOX_START_ARGS)

sandbox-status: affentctl
	"$(AFFENTCTL)" sandbox status $(SANDBOX_STATUS_ARGS)

sandbox-stop: affentctl
	"$(AFFENTCTL)" sandbox stop $(SANDBOX_STOP_ARGS)

image-build: affentctl
	"$(AFFENTCTL)" image build --memory "$(CONTAINER_MEMORY)" $(IMAGE_BUILD_ARGS)

image-run: image-build
	"$(AFFENTCTL)" image run --workspace "$(IMAGE_WORKSPACE)" --memory "$(CONTAINER_MEMORY)" --cpus "$(CONTAINER_CPUS)" --pids-limit "$(CONTAINER_PIDS)" $(IMAGE_RUN_ARGS) -- $(IMAGE_COMMAND)

image-serve: image-build
	"$(AFFENTCTL)" image run --workspace "$(IMAGE_WORKSPACE)" --memory "$(CONTAINER_MEMORY)" --cpus "$(CONTAINER_CPUS)" --pids-limit "$(CONTAINER_PIDS)" $(if $(SERVE_CONTAINER_NAME),--name "$(SERVE_CONTAINER_NAME)") --timeout 0s --detach --rm=false --publish "$(SERVE_PUBLISH)" $(IMAGE_RUN_ARGS) -- affentserve --listen "$(SERVE_LISTEN)" $(if $(SERVE_BASE_URL),--base-url "$(SERVE_BASE_URL)") $(if $(SERVE_API_KEY),--api-key "$(SERVE_API_KEY)") $(if $(SERVE_MODEL),--model "$(SERVE_MODEL)") --workspace-root "$(SERVE_WORKSPACE_ROOT)" --memory-root "$(SERVE_MEMORY_ROOT)" --builtins $(SERVE_DEFAULT_ARGS) $(SERVE_ARGS)

image-serve-up:
	@if test -z "$(SERVE_CONTAINER_NAME)"; then \
		echo "SERVE_CONTAINER_NAME is required, e.g. make image-serve-up SERVE_CONTAINER_NAME=affent-serve" >&2; \
		exit 2; \
	fi; \
	if docker inspect "$(SERVE_CONTAINER_NAME)" >/dev/null 2>&1; then \
		label=$$(docker inspect "$(SERVE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime"}}' 2>/dev/null); \
		if test "$$label" != "true"; then \
			echo "container $(SERVE_CONTAINER_NAME) is not an Affent runtime container" >&2; \
			exit 2; \
		fi; \
		expected_workspace="$(abspath $(IMAGE_WORKSPACE))"; \
		actual_workspace=$$(docker inspect "$(SERVE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.workspace"}}' 2>/dev/null); \
		if test "$$actual_workspace" != "$$expected_workspace"; then \
			echo "container $(SERVE_CONTAINER_NAME) was created with workspace=$$actual_workspace, but requested workspace=$$expected_workspace" >&2; \
			echo "run make image-serve-restart to recreate it with the requested persistent workspace" >&2; \
			exit 2; \
		fi; \
		actual_memory=$$(docker inspect "$(SERVE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.memory"}}' 2>/dev/null); \
		actual_cpus=$$(docker inspect "$(SERVE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.cpus"}}' 2>/dev/null); \
		actual_pids=$$(docker inspect "$(SERVE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.pids_limit"}}' 2>/dev/null); \
		if test "$$actual_memory" != "$(CONTAINER_MEMORY)" || test "$$actual_cpus" != "$(CONTAINER_CPUS)" || test "$$actual_pids" != "$(CONTAINER_PIDS)"; then \
			echo "container $(SERVE_CONTAINER_NAME) was created with memory=$$actual_memory cpus=$$actual_cpus pids_limit=$$actual_pids, but requested memory=$(CONTAINER_MEMORY) cpus=$(CONTAINER_CPUS) pids_limit=$(CONTAINER_PIDS)" >&2; \
			echo "run make image-serve-restart to recreate it with the requested limits" >&2; \
			exit 2; \
		fi; \
		actual_publish=$$(docker inspect "$(SERVE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.publish"}}' 2>/dev/null); \
		if test "$$actual_publish" != "$(SERVE_PUBLISH)" ; then \
			echo "container $(SERVE_CONTAINER_NAME) was created with publish=$$actual_publish, but requested publish=$(SERVE_PUBLISH)" >&2; \
			echo "run make image-serve-restart to recreate it with the requested port publishing" >&2; \
			exit 2; \
		fi; \
		actual_listen=$$(docker inspect "$(SERVE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.serve.listen"}}' 2>/dev/null); \
		actual_serve_workspace_root=$$(docker inspect "$(SERVE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.serve.workspace_root"}}' 2>/dev/null); \
		actual_serve_memory_root=$$(docker inspect "$(SERVE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.serve.memory_root"}}' 2>/dev/null); \
		if test "$$actual_listen" != "$(SERVE_LISTEN)" || test "$$actual_serve_workspace_root" != "$(SERVE_WORKSPACE_ROOT)" || test "$$actual_serve_memory_root" != "$(SERVE_MEMORY_ROOT)" ; then \
			echo "container $(SERVE_CONTAINER_NAME) was created with listen=$$actual_listen workspace_root=$$actual_serve_workspace_root memory_root=$$actual_serve_memory_root, but requested listen=$(SERVE_LISTEN) workspace_root=$(SERVE_WORKSPACE_ROOT) memory_root=$(SERVE_MEMORY_ROOT)" >&2; \
			echo "run make image-serve-restart to recreate it with the requested affentserve paths" >&2; \
			exit 2; \
		fi; \
		expected_serve_builtins=true; expected_serve_eval_mode="$(AFFENTSERVE_EVAL_MODE)"; expected_serve_memory="$(AFFENTSERVE_MEMORY)"; expected_serve_browser="$(AFFENTSERVE_BROWSER)"; expected_serve_browser_screenshot="$(AFFENTSERVE_BROWSER_SCREENSHOT)"; expected_serve_web="$(AFFENTSERVE_WEB)"; expected_serve_web_search="$(AFFENTSERVE_WEB_SEARCH)"; expected_serve_subagent="$(AFFENTSERVE_SUBAGENT)"; expected_serve_focused_tasks="$(AFFENTSERVE_FOCUSED_TASKS)"; expected_serve_browser_cache_dir=""; \
		set -- --builtins $(SERVE_DEFAULT_ARGS) $(SERVE_ARGS); \
		while test "$$#" -gt 0; do \
			arg="$$1"; shift; \
			case "$$arg" in \
				--builtins) expected_serve_builtins=true ;; --builtins=*) expected_serve_builtins=$${arg#--builtins=} ;; \
				--eval-mode) expected_serve_eval_mode=true ;; --eval-mode=*) expected_serve_eval_mode=$${arg#--eval-mode=} ;; \
				--memory) expected_serve_memory=true ;; --memory=*) expected_serve_memory=$${arg#--memory=} ;; \
				--browser) expected_serve_browser=true ;; --browser=*) expected_serve_browser=$${arg#--browser=} ;; \
				--browser-cache-dir) if test "$$#" -gt 0 && ! printf '%s\n' "$$1" | grep -q '^--'; then expected_serve_browser_cache_dir="$$1"; shift; fi ;; \
				--browser-cache-dir=*) expected_serve_browser_cache_dir=$${arg#--browser-cache-dir=} ;; \
				--browser-screenshot) expected_serve_browser_screenshot=true ;; --browser-screenshot=*) expected_serve_browser_screenshot=$${arg#--browser-screenshot=} ;; \
				--web) expected_serve_web=true ;; --web=*) expected_serve_web=$${arg#--web=} ;; \
				--web-search) expected_serve_web_search=true ;; --web-search=*) expected_serve_web_search=$${arg#--web-search=} ;; \
				--subagent) expected_serve_subagent=true ;; --subagent=*) expected_serve_subagent=$${arg#--subagent=} ;; \
				--focused-tasks) expected_serve_focused_tasks=true ;; --focused-tasks=*) expected_serve_focused_tasks=$${arg#--focused-tasks=} ;; \
			esac; \
		done; \
		actual_serve_builtins=$$(docker inspect "$(SERVE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.serve.builtins"}}' 2>/dev/null); \
		actual_serve_eval_mode=$$(docker inspect "$(SERVE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.serve.eval_mode"}}' 2>/dev/null); \
		actual_serve_memory=$$(docker inspect "$(SERVE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.serve.memory"}}' 2>/dev/null); \
		actual_serve_browser=$$(docker inspect "$(SERVE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.serve.browser"}}' 2>/dev/null); \
		actual_serve_browser_cache_dir=$$(docker inspect "$(SERVE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.serve.browser_cache_dir"}}' 2>/dev/null); \
		actual_serve_browser_screenshot=$$(docker inspect "$(SERVE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.serve.browser_screenshot"}}' 2>/dev/null); \
		actual_serve_web=$$(docker inspect "$(SERVE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.serve.web"}}' 2>/dev/null); \
		actual_serve_web_search=$$(docker inspect "$(SERVE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.serve.web_search"}}' 2>/dev/null); \
		actual_serve_subagent=$$(docker inspect "$(SERVE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.serve.subagent"}}' 2>/dev/null); \
		actual_serve_focused_tasks=$$(docker inspect "$(SERVE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.serve.focused_tasks"}}' 2>/dev/null); \
		if test "$$actual_serve_builtins" != "$$expected_serve_builtins" || test "$$actual_serve_eval_mode" != "$$expected_serve_eval_mode" || test "$$actual_serve_memory" != "$$expected_serve_memory" || test "$$actual_serve_browser" != "$$expected_serve_browser" || test "$$actual_serve_browser_cache_dir" != "$$expected_serve_browser_cache_dir" || test "$$actual_serve_browser_screenshot" != "$$expected_serve_browser_screenshot" || test "$$actual_serve_web" != "$$expected_serve_web" || test "$$actual_serve_web_search" != "$$expected_serve_web_search" || test "$$actual_serve_subagent" != "$$expected_serve_subagent" || test "$$actual_serve_focused_tasks" != "$$expected_serve_focused_tasks"; then \
			echo "container $(SERVE_CONTAINER_NAME) was created with serve flags builtins=$$actual_serve_builtins eval_mode=$$actual_serve_eval_mode memory=$$actual_serve_memory browser=$$actual_serve_browser browser_cache_dir=$$actual_serve_browser_cache_dir browser_screenshot=$$actual_serve_browser_screenshot web=$$actual_serve_web web_search=$$actual_serve_web_search subagent=$$actual_serve_subagent focused_tasks=$$actual_serve_focused_tasks, but requested builtins=$$expected_serve_builtins eval_mode=$$expected_serve_eval_mode memory=$$expected_serve_memory browser=$$expected_serve_browser browser_cache_dir=$$expected_serve_browser_cache_dir browser_screenshot=$$expected_serve_browser_screenshot web=$$expected_serve_web web_search=$$expected_serve_web_search subagent=$$expected_serve_subagent focused_tasks=$$expected_serve_focused_tasks" >&2; \
			echo "run make image-serve-restart to recreate it with the requested affentserve feature flags" >&2; \
			exit 2; \
		fi; \
		running=$$(docker inspect "$(SERVE_CONTAINER_NAME)" --format '{{.State.Running}}' 2>/dev/null); \
		if test "$$running" = "true"; then \
			echo "container $(SERVE_CONTAINER_NAME) already running; waiting for health"; \
		else \
			echo "container $(SERVE_CONTAINER_NAME) exists but is not running; starting"; \
			docker start "$(SERVE_CONTAINER_NAME)"; \
		fi; \
	else \
		$(MAKE) image-serve; \
	fi
	$(MAKE) image-serve-health-wait

image-serve-status:
	@$(call require_affent_runtime_container,image-serve-status); \
	docker inspect "$(SERVE_CONTAINER_NAME)" \
		--format 'name={{.Name}} state={{.State.Status}} image={{.Config.Image}} workspace={{index .Config.Labels "affent.runtime.workspace"}} publish={{index .Config.Labels "affent.runtime.publish"}} serve_listen={{index .Config.Labels "affent.runtime.serve.listen"}} serve_workspace_root={{index .Config.Labels "affent.runtime.serve.workspace_root"}} serve_memory_root={{index .Config.Labels "affent.runtime.serve.memory_root"}} serve_builtins={{index .Config.Labels "affent.runtime.serve.builtins"}} serve_eval_mode={{index .Config.Labels "affent.runtime.serve.eval_mode"}} serve_memory={{index .Config.Labels "affent.runtime.serve.memory"}} serve_browser={{index .Config.Labels "affent.runtime.serve.browser"}} serve_browser_cache_dir={{index .Config.Labels "affent.runtime.serve.browser_cache_dir"}} serve_browser_screenshot={{index .Config.Labels "affent.runtime.serve.browser_screenshot"}} serve_web={{index .Config.Labels "affent.runtime.serve.web"}} serve_web_search={{index .Config.Labels "affent.runtime.serve.web_search"}} serve_subagent={{index .Config.Labels "affent.runtime.serve.subagent"}} serve_focused_tasks={{index .Config.Labels "affent.runtime.serve.focused_tasks"}} memory={{index .Config.Labels "affent.runtime.memory"}} cpus={{index .Config.Labels "affent.runtime.cpus"}} pids_limit={{index .Config.Labels "affent.runtime.pids_limit"}} host_memory_bytes={{.HostConfig.Memory}} host_nano_cpus={{.HostConfig.NanoCpus}} host_pids_limit={{.HostConfig.PidsLimit}}'; \
	ports=$$(docker port "$(SERVE_CONTAINER_NAME)" 2>/dev/null || true); \
	if test -n "$$ports"; then printf 'ports:\n%s\n' "$$ports"; else echo "ports: none"; fi

image-serve-health:
	@$(call require_affent_runtime_container,image-serve-health)
	curl -fsS "$(SERVE_HEALTH_URL)"

image-serve-health-wait:
	@$(call require_affent_runtime_container,image-serve-health-wait); \
	attempt=1; \
	while test "$$attempt" -le "$(SERVE_HEALTH_ATTEMPTS)"; do \
		if curl -fsS "$(SERVE_HEALTH_URL)"; then \
			exit 0; \
		fi; \
		state=$$(docker inspect "$(SERVE_CONTAINER_NAME)" --format '{{.State.Status}}' 2>/dev/null || true); \
		if test "$$state" != "running"; then \
			echo "container $(SERVE_CONTAINER_NAME) is $$state while waiting for health: $(SERVE_HEALTH_URL)" >&2; \
			echo "last container logs:" >&2; \
			docker logs --tail 100 "$(SERVE_CONTAINER_NAME)" >&2 || true; \
			exit 1; \
		fi; \
		echo "health check failed ($$attempt/$(SERVE_HEALTH_ATTEMPTS)); retrying in $(SERVE_HEALTH_INTERVAL)s" >&2; \
		attempt=$$((attempt + 1)); \
		sleep "$(SERVE_HEALTH_INTERVAL)"; \
	done; \
	echo "health check failed after $(SERVE_HEALTH_ATTEMPTS) attempts: $(SERVE_HEALTH_URL)" >&2; \
	exit 1

image-serve-logs:
	@$(call require_affent_runtime_container,image-serve-logs)
	docker logs --tail 100 "$(SERVE_CONTAINER_NAME)"

image-serve-stop:
	@$(call require_affent_runtime_container,image-serve-stop)
	docker rm -f "$(SERVE_CONTAINER_NAME)"

image-serve-restart:
	@if test -z "$(SERVE_CONTAINER_NAME)"; then \
		echo "SERVE_CONTAINER_NAME is required, e.g. make image-serve-restart SERVE_CONTAINER_NAME=affent-serve" >&2; \
		exit 2; \
	fi; \
	if docker inspect "$(SERVE_CONTAINER_NAME)" >/dev/null 2>&1; then \
		label=$$(docker inspect "$(SERVE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime"}}' 2>/dev/null); \
		if test "$$label" != "true"; then \
			echo "container $(SERVE_CONTAINER_NAME) is not an Affent runtime container" >&2; \
			exit 2; \
		fi; \
		docker rm -f "$(SERVE_CONTAINER_NAME)"; \
	fi
	$(MAKE) image-serve
	$(MAKE) image-serve-health-wait

image-serve-smoke:
	@set -eu; \
	if test -z "$(SMOKE_CONTAINER_NAME)"; then \
		echo "SMOKE_CONTAINER_NAME is required" >&2; \
		exit 2; \
	fi; \
	if test -z "$(SMOKE_WORKSPACE)" || test "$(SMOKE_WORKSPACE)" = "/"; then \
		echo "SMOKE_WORKSPACE must be a non-root path" >&2; \
		exit 2; \
	fi; \
	docker rm -f "$(SMOKE_CONTAINER_NAME)" >/dev/null 2>&1 || true; \
	mkdir -p "$(SMOKE_WORKSPACE)"; \
	cleanup() { docker rm -f "$(SMOKE_CONTAINER_NAME)" >/dev/null 2>&1 || true; }; \
	trap cleanup EXIT; \
	$(MAKE) image-serve-up \
		SERVE_CONTAINER_NAME="$(SMOKE_CONTAINER_NAME)" \
		IMAGE_WORKSPACE="$(SMOKE_WORKSPACE)" \
		SERVE_PUBLISH="$(SMOKE_PUBLISH)" \
		SERVE_BASE_URL="$(SMOKE_BASE_URL)" \
		SERVE_API_KEY="$(SMOKE_API_KEY)" \
		SERVE_MODEL="$(SMOKE_MODEL)" \
		CONTAINER_MEMORY="$(CONTAINER_MEMORY)" \
		CONTAINER_CPUS="$(CONTAINER_CPUS)" \
		CONTAINER_PIDS="$(CONTAINER_PIDS)"; \
	curl -fsS "$(SMOKE_URL)/healthz" >/dev/null; \
	curl -fsS -X POST "$(SMOKE_URL)/v1/sessions" \
		-H 'Content-Type: application/json' \
		--data '{"session_id":"$(SMOKE_SESSION_ID)"}' | grep -q '"durable":true'; \
	tools=$$(curl -fsS "$(SMOKE_URL)/v1/sessions/$(SMOKE_SESSION_ID)/tools"); \
	echo "$$tools" | grep -q '"browser_navigate"'; \
	echo "$$tools" | grep -q '"web_fetch"'; \
	if echo "$$tools" | grep -q '"web_search"'; then \
		echo "image-serve-smoke expected web_search to stay disabled by default" >&2; \
		exit 1; \
	fi; \
	test -f "$(SMOKE_WORKSPACE)/session-state/$(SMOKE_SESSION_ID)/conversation.jsonl"; \
	docker stop "$(SMOKE_CONTAINER_NAME)" >/dev/null; \
	$(MAKE) image-serve-up \
		SERVE_CONTAINER_NAME="$(SMOKE_CONTAINER_NAME)" \
		IMAGE_WORKSPACE="$(SMOKE_WORKSPACE)" \
		SERVE_PUBLISH="$(SMOKE_PUBLISH)" \
		SERVE_BASE_URL="$(SMOKE_BASE_URL)" \
		SERVE_API_KEY="$(SMOKE_API_KEY)" \
		SERVE_MODEL="$(SMOKE_MODEL)" \
		CONTAINER_MEMORY="$(CONTAINER_MEMORY)" \
		CONTAINER_CPUS="$(CONTAINER_CPUS)" \
		CONTAINER_PIDS="$(CONTAINER_PIDS)"; \
	detail=$$(curl -fsS "$(SMOKE_URL)/v1/sessions/$(SMOKE_SESSION_ID)"); \
	echo "$$detail" | grep -q '"durable":true'; \
	echo "$$detail" | grep -q '"active":false'; \
	memory_label=$$(docker inspect "$(SMOKE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.memory"}}'); \
	browser_label=$$(docker inspect "$(SMOKE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.serve.browser"}}'); \
	web_label=$$(docker inspect "$(SMOKE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.serve.web"}}'); \
	web_search_label=$$(docker inspect "$(SMOKE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.serve.web_search"}}'); \
	browser_cache_label=$$(docker inspect "$(SMOKE_CONTAINER_NAME)" --format '{{index .Config.Labels "affent.runtime.serve.browser_cache_dir"}}'); \
	test "$$memory_label" = "$(CONTAINER_MEMORY)"; \
	test "$$browser_label" = "true"; \
	test "$$web_label" = "true"; \
	test "$$web_search_label" = "false"; \
	test "$$browser_cache_label" = "/workspace/browser-cache"; \
	echo "image-serve-smoke ok: $(SMOKE_URL) session=$(SMOKE_SESSION_ID) memory=$(CONTAINER_MEMORY) browser=$$browser_label web=$$web_label cache=$$browser_cache_label"

eval-container: affentctl
	"$(AFFENTCTL)" image build --image "$(EVAL_IMAGE)" --memory "$(CONTAINER_MEMORY)" $(IMAGE_BUILD_ARGS)
	mkdir -p .tmp/eval-container/home .tmp/eval-container/cache .tmp/eval-container/go-build .tmp/eval-container/go-mod .tmp/eval-container/npm .tmp/eval-container/pip .tmp/eval
	docker run --rm \
		--memory "$(CONTAINER_MEMORY)" \
		--memory-swap "$(CONTAINER_MEMORY)" \
		--cpus "$(CONTAINER_CPUS)" \
		--pids-limit "$(CONTAINER_PIDS)" \
		--user "$$(id -u):$$(id -g)" \
		-e HOME=/workspace/.tmp/eval-container/home \
		-e XDG_CACHE_HOME=/workspace/.tmp/eval-container/cache \
		-e GOCACHE=/workspace/.tmp/eval-container/go-build \
		-e GOMODCACHE=/workspace/.tmp/eval-container/go-mod \
		-e NPM_CONFIG_CACHE=/workspace/.tmp/eval-container/npm \
		-e PIP_CACHE_DIR=/workspace/.tmp/eval-container/pip \
		-e AFFENTCTL_BASE_URL \
		-e AFFENTCTL_API_KEY \
		-e AFFENTCTL_MODEL \
		-e AFFENTCTL_MEMORY_MAX_CHARS \
		-e AFFENTCTL_MEMORY_TOPIC_MAX_CHARS \
		-e AFFENTCTL_MEMORY_MAX_TOPICS \
		-e AFFENTEVAL_PROVIDER_LABEL \
		-v "$(CURDIR):/workspace" \
		-w /workspace \
		$(EVAL_DOCKER_ARGS) \
		"$(EVAL_IMAGE)" \
		go run ./cmd/affenteval --repo-root /workspace --work-root "$(EVAL_WORK_ROOT)" --executor local $(EVAL_RUNTIME_EVAL_MODE_ARGS) $(EVAL_RUNTIME_MEMORY_ARGS) $(EVAL_RUNTIME_MCP_CONFIG_ARGS) $(EVAL_ARGS)

eval-agent-container: EVAL_RUNTIME_EVAL_MODE=true
eval-agent-container: eval-container

eval-serve-container:
	@set -eu; \
	args="--eval-mode"; \
	for permission in $(SERVE_EVAL_PERMISSIONS); do \
		case "$$permission" in \
			none) ;; \
			browser) args="$$args --browser=true" ;; \
			browser-screenshot) args="$$args --browser=true --browser-screenshot=true" ;; \
			web) args="$$args --web=true" ;; \
			web-search) args="$$args --web=true --web-search=true" ;; \
			memory) args="$$args --memory=true" ;; \
			*) echo "unknown SERVE_EVAL_PERMISSIONS entry: $$permission (valid: none browser browser-screenshot web web-search memory)" >&2; exit 2 ;; \
		esac; \
	done; \
	if test -n "$(SERVE_ARGS)"; then args="$$args $(SERVE_ARGS)"; fi; \
	$(MAKE) image-serve-restart \
		SERVE_CONTAINER_NAME="$(SERVE_EVAL_CONTAINER_NAME)" \
		IMAGE_WORKSPACE="$(SERVE_EVAL_WORKSPACE)" \
		SERVE_PUBLISH="$(SERVE_EVAL_PUBLISH)" \
		SERVE_DEFAULT_ARGS="" \
		SERVE_ARGS="$$args" \
		CONTAINER_MEMORY="$(CONTAINER_MEMORY)" \
		CONTAINER_CPUS="$(CONTAINER_CPUS)" \
		CONTAINER_PIDS="$(CONTAINER_PIDS)"

eval-serve-browser-container: SERVE_EVAL_PERMISSIONS=browser
eval-serve-browser-container: eval-serve-container

test-container:
	mkdir -p .tmp/go-build .tmp/go-mod
	docker run --rm \
		--memory "$(CONTAINER_MEMORY)" \
		--memory-swap "$(CONTAINER_MEMORY)" \
		--cpus "$(CONTAINER_CPUS)" \
		--pids-limit "$(CONTAINER_PIDS)" \
		--user "$$(id -u):$$(id -g)" \
		-e GOCACHE=/work/.tmp/go-build \
		-e GOMODCACHE=/work/.tmp/go-mod \
		-v "$(CURDIR):/work" \
		-w "/work/$(TEST_DIR)" \
		"$(CONTAINER_GO_IMAGE)" \
		sh -lc '. /work/docker/go-cgroup-env.sh; /usr/local/go/bin/go test $(GO_TEST_FLAGS) $(TEST_PACKAGES)'
