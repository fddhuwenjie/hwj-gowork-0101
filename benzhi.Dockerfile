# syntax=docker/dockerfile:1
FROM golang@sha256:53eeac89074db483fdf0ab3be1df32bf6e47562263d2d0d6baa7f26acb4957dd

ENV GOTOOLCHAIN=local \
    GOFLAGS=-mod=vendor \
    GOPROXY=off \
    GOSUMDB=off \
    GOMODCACHE=/go/pkg/mod \
    GOCACHE=/root/.cache/go-build

WORKDIR /workspace
COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    cp -a /workspace /tmp/source && \
    cd /tmp/source && \
    go build ./... && \
    rm -rf /tmp/source

CMD ["go", "run", "./cmd/server"]
