# syntax=docker/dockerfile:1.7

FROM node:22-bookworm AS webui

WORKDIR /src/extras/webui

COPY extras/webui/package.json extras/webui/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci

COPY extras/webui ./
RUN npm run build

FROM golang:1.24-bookworm AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY extras/web/go.mod extras/web/go.sum ./extras/web/
RUN --mount=type=cache,target=/go/pkg/mod \
    cd extras/web && go mod download

COPY extras/browser/go.mod extras/browser/go.sum ./extras/browser/
RUN --mount=type=cache,target=/go/pkg/mod \
    cd extras/browser && go mod download

COPY cmd/affentserve/go.mod cmd/affentserve/go.sum ./cmd/affentserve/
RUN --mount=type=cache,target=/go/pkg/mod \
    cd cmd/affentserve && go mod download

COPY docker/go-cgroup-env.sh /tmp/affent-go-cgroup-env
COPY . .
COPY --from=webui /src/extras/webui/dist ./cmd/affentserve/webui/dist
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    . /tmp/affent-go-cgroup-env \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/affentctl ./cmd/affentctl \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/affenteval ./cmd/affenteval \
    && cd cmd/affentserve \
    && CGO_ENABLED=0 go build -tags webui -trimpath -ldflags="-s -w" -o /out/affentserve .

FROM golang:1.24-bookworm

LABEL org.opencontainers.image.title="Affent"
LABEL org.opencontainers.image.description="Affent runtime image with affentctl, affentserve, affenteval, and the standard tool sandbox packages."
LABEL org.opencontainers.image.source="https://github.com/affinefoundation/affent"

ENV DEBIAN_FRONTEND=noninteractive
ENV PATH=/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

COPY docker/tool-packages.txt /tmp/affent-tool-packages.txt

RUN apt-get update \
    && xargs -r apt-get install -y --no-install-recommends < /tmp/affent-tool-packages.txt \
    && ln -sf /usr/bin/fdfind /usr/local/bin/fd \
    && ln -sf /usr/local/go/bin/go /usr/local/bin/go \
    && ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/* /tmp/affent-tool-packages.txt

COPY docker/go-cgroup-env.sh /usr/local/bin/affent-go-cgroup-env
COPY docker/affent-entrypoint.sh /usr/local/bin/affent-entrypoint
RUN chmod +x /usr/local/bin/affent-go-cgroup-env /usr/local/bin/affent-entrypoint

COPY --from=build /out/affentctl /usr/local/bin/affentctl
COPY --from=build /out/affenteval /usr/local/bin/affenteval
COPY --from=build /out/affentserve /usr/local/bin/affentserve

WORKDIR /workspace
EXPOSE 7777

ENTRYPOINT ["affent-entrypoint"]
CMD ["affentserve", "--listen", "0.0.0.0:7777", "--workspace-root", "/workspace/sessions", "--memory-root", "/workspace/session-state", "--builtins=true", "--web=true"]
