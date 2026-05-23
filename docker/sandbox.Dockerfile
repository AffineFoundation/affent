# syntax=docker/dockerfile:1.7

FROM golang:1.24-bookworm

LABEL org.opencontainers.image.title="Affent Sandbox"
LABEL org.opencontainers.image.description="Persistent Docker tool sandbox for affentctl shell and file execution."
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

WORKDIR /workspace

CMD ["sleep", "infinity"]
