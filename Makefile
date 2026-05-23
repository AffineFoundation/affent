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
SERVE_LISTEN ?= 0.0.0.0:7777
SERVE_PUBLISH ?= 127.0.0.1:7777:7777
SERVE_CONTAINER_NAME ?= affent-serve
SERVE_WORKSPACE_ROOT ?= /workspace/sessions
SERVE_MEMORY_ROOT ?= /workspace/session-state
DOCTOR_ARGS ?=

CONTAINER_GO_IMAGE ?= golang:1.24-bookworm
CONTAINER_MEMORY ?= 1g
CONTAINER_CPUS ?= 2
CONTAINER_PIDS ?= 512
TEST_DIR ?= .
GO_TEST_FLAGS ?= -p=1
TEST_PACKAGES ?= ./...

.PHONY: affentctl affentctl-local doctor sandbox-start sandbox-status sandbox-stop image-build image-run image-serve image-serve-status image-serve-stop eval-container test-container

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

image-run: affentctl
	"$(AFFENTCTL)" image run --workspace "$(IMAGE_WORKSPACE)" --memory "$(CONTAINER_MEMORY)" --cpus "$(CONTAINER_CPUS)" --pids-limit "$(CONTAINER_PIDS)" $(IMAGE_RUN_ARGS) -- $(IMAGE_COMMAND)

image-serve: affentctl
	"$(AFFENTCTL)" image run --workspace "$(IMAGE_WORKSPACE)" --memory "$(CONTAINER_MEMORY)" --cpus "$(CONTAINER_CPUS)" --pids-limit "$(CONTAINER_PIDS)" $(if $(SERVE_CONTAINER_NAME),--name "$(SERVE_CONTAINER_NAME)") --timeout 0s --publish "$(SERVE_PUBLISH)" $(IMAGE_RUN_ARGS) -- affentserve --listen "$(SERVE_LISTEN)" --workspace-root "$(SERVE_WORKSPACE_ROOT)" --memory-root "$(SERVE_MEMORY_ROOT)" --builtins $(SERVE_ARGS)

image-serve-status:
	@if test -n "$(SERVE_CONTAINER_NAME)"; then \
		docker inspect "$(SERVE_CONTAINER_NAME)" \
			--format 'name={{.Name}} state={{.State.Status}} image={{.Config.Image}} workspace={{index .Config.Labels "affent.runtime.workspace"}} memory={{index .Config.Labels "affent.runtime.memory"}} cpus={{index .Config.Labels "affent.runtime.cpus"}} pids_limit={{index .Config.Labels "affent.runtime.pids_limit"}} host_memory_bytes={{.HostConfig.Memory}} host_nano_cpus={{.HostConfig.NanoCpus}} host_pids_limit={{.HostConfig.PidsLimit}}'; \
	else \
		echo "SERVE_CONTAINER_NAME is required, e.g. make image-serve-status SERVE_CONTAINER_NAME=affent-serve" >&2; \
		exit 2; \
	fi

image-serve-stop:
	@if test -n "$(SERVE_CONTAINER_NAME)"; then docker rm -f "$(SERVE_CONTAINER_NAME)"; else echo "SERVE_CONTAINER_NAME is required, e.g. make image-serve-stop SERVE_CONTAINER_NAME=affent-serve" >&2; exit 2; fi

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
