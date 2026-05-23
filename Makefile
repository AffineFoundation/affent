GO ?= go
AFFENTCTL ?= ./bin/affentctl

SANDBOX_START_ARGS ?=
SANDBOX_STOP_ARGS ?=
IMAGE_BUILD_ARGS ?=
IMAGE_RUN_ARGS ?=
IMAGE_COMMAND ?= affentctl --help
SERVE_ARGS ?=
SERVE_LISTEN ?= 0.0.0.0:7777
SERVE_PUBLISH ?= 127.0.0.1:7777:7777
SERVE_WORKSPACE_ROOT ?= /workspace/sessions
DOCTOR_ARGS ?=

CONTAINER_GO_IMAGE ?= golang:1.24-bookworm
CONTAINER_MEMORY ?= 1g
CONTAINER_CPUS ?= 2
TEST_DIR ?= .
GO_TEST_FLAGS ?= -p=1
TEST_PACKAGES ?= ./...

.PHONY: affentctl affentctl-local doctor sandbox-start sandbox-status sandbox-stop image-build image-run image-serve test-container

affentctl:
	mkdir -p "$(dir $(AFFENTCTL))" .tmp/go-build .tmp/go-mod
	docker run --rm \
		--memory "$(CONTAINER_MEMORY)" \
		--memory-swap "$(CONTAINER_MEMORY)" \
		--cpus "$(CONTAINER_CPUS)" \
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
	"$(AFFENTCTL)" sandbox start $(SANDBOX_START_ARGS)

sandbox-status: affentctl
	"$(AFFENTCTL)" sandbox status

sandbox-stop: affentctl
	"$(AFFENTCTL)" sandbox stop $(SANDBOX_STOP_ARGS)

image-build: affentctl
	"$(AFFENTCTL)" image build $(IMAGE_BUILD_ARGS)

image-run: affentctl
	"$(AFFENTCTL)" image run $(IMAGE_RUN_ARGS) -- $(IMAGE_COMMAND)

image-serve: affentctl
	"$(AFFENTCTL)" image run --timeout 0s --publish "$(SERVE_PUBLISH)" $(IMAGE_RUN_ARGS) -- affentserve --listen "$(SERVE_LISTEN)" --workspace-root "$(SERVE_WORKSPACE_ROOT)" $(SERVE_ARGS)

test-container:
	mkdir -p .tmp/go-build .tmp/go-mod
	docker run --rm \
		--memory "$(CONTAINER_MEMORY)" \
		--memory-swap "$(CONTAINER_MEMORY)" \
		--cpus "$(CONTAINER_CPUS)" \
		--user "$$(id -u):$$(id -g)" \
		-e GOCACHE=/work/.tmp/go-build \
		-e GOMODCACHE=/work/.tmp/go-mod \
		-v "$(CURDIR):/work" \
		-w "/work/$(TEST_DIR)" \
		"$(CONTAINER_GO_IMAGE)" \
		sh -lc '. /work/docker/go-cgroup-env.sh; /usr/local/go/bin/go test $(GO_TEST_FLAGS) $(TEST_PACKAGES)'
