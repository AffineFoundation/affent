GO ?= go
AFFENTCTL ?= ./bin/affentctl

SANDBOX_START_ARGS ?=
SANDBOX_STATUS_ARGS ?=
SANDBOX_STOP_ARGS ?=
IMAGE_BUILD_ARGS ?=
IMAGE_RUN_ARGS ?=
IMAGE_COMMAND ?= affentctl --help
IMAGE_WORKSPACE ?= $(CURDIR)/.tmp/runtime-workspace
EVAL_IMAGE ?= affinefoundation/affent:latest
EVAL_ARGS ?= --list
EVAL_WORK_ROOT ?= /workspace/.tmp/eval
EVAL_DOCKER_ARGS ?=
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
CONTAINER_MEMORY ?= 1g
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

.PHONY: affentctl affentctl-local doctor sandbox-start sandbox-status sandbox-stop image-build image-run image-serve image-serve-up image-serve-status image-serve-health image-serve-health-wait image-serve-logs image-serve-stop image-serve-restart image-serve-smoke eval-container test-container

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
	"$(AFFENTCTL)" image run --workspace "$(IMAGE_WORKSPACE)" --memory "$(CONTAINER_MEMORY)" --cpus "$(CONTAINER_CPUS)" --pids-limit "$(CONTAINER_PIDS)" $(if $(SERVE_CONTAINER_NAME),--name "$(SERVE_CONTAINER_NAME)") --timeout 0s --detach --rm=false --publish "$(SERVE_PUBLISH)" $(IMAGE_RUN_ARGS) -- affentserve --listen "$(SERVE_LISTEN)" $(if $(SERVE_BASE_URL),--base-url "$(SERVE_BASE_URL)") $(if $(SERVE_API_KEY),--api-key "$(SERVE_API_KEY)") $(if $(SERVE_MODEL),--model "$(SERVE_MODEL)") --workspace-root "$(SERVE_WORKSPACE_ROOT)" --memory-root "$(SERVE_MEMORY_ROOT)" --builtins $(SERVE_ARGS)

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
		--format 'name={{.Name}} state={{.State.Status}} image={{.Config.Image}} workspace={{index .Config.Labels "affent.runtime.workspace"}} publish={{index .Config.Labels "affent.runtime.publish"}} serve_listen={{index .Config.Labels "affent.runtime.serve.listen"}} serve_workspace_root={{index .Config.Labels "affent.runtime.serve.workspace_root"}} serve_memory_root={{index .Config.Labels "affent.runtime.serve.memory_root"}} memory={{index .Config.Labels "affent.runtime.memory"}} cpus={{index .Config.Labels "affent.runtime.cpus"}} pids_limit={{index .Config.Labels "affent.runtime.pids_limit"}} host_memory_bytes={{.HostConfig.Memory}} host_nano_cpus={{.HostConfig.NanoCpus}} host_pids_limit={{.HostConfig.PidsLimit}}'; \
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
	test "$$memory_label" = "$(CONTAINER_MEMORY)"; \
	echo "image-serve-smoke ok: $(SMOKE_URL) session=$(SMOKE_SESSION_ID) memory=$(CONTAINER_MEMORY)"

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
		-e AFFENTCTL_TEMPERATURE \
		-e AFFENTCTL_TOP_P \
		-e AFFENTCTL_MAX_TOKENS \
		-v "$(CURDIR):/workspace" \
		-w /workspace \
		$(EVAL_DOCKER_ARGS) \
		"$(EVAL_IMAGE)" \
		go run ./cmd/affenteval --repo-root /workspace --work-root "$(EVAL_WORK_ROOT)" --executor local $(EVAL_ARGS)

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
