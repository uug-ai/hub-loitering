# syntax=docker/dockerfile:1.7

FROM golang:1.26.10-bookworm@sha256:632dff7351799a5a9954a48dc06670ade72ee44e8c351cdc0e5a57af2fea21af AS go-toolchain

FROM cgr.dev/chainguard/wolfi-base:latest@sha256:0feab531471d2edfcca55f18883441cb7df598e724fa2dedf9985273f532eed7 AS native
RUN apk add --no-cache build-base coreutils git librdkafka-dev=2.16.0-r0 && \
    mkdir -p /runtime/etc/apk/keys && \
    cp /etc/apk/keys/* /runtime/etc/apk/keys/ && \
    cp /etc/apk/repositories /runtime/etc/apk/repositories && \
    apk --root /runtime --initdb --no-cache --no-scripts add \
      wolfi-baselayout wolfi-keys ca-certificates-bundle librdkafka=2.16.0-r0 && \
    apk --root /runtime info -v && \
    apk --root /runtime info --who-owns /usr/lib/librdkafka.so.1

FROM native AS builder
COPY --from=go-toolchain /usr/local/go/ /usr/local/go/
ENV PATH="/usr/local/go/bin:${PATH}" \
    GOTOOLCHAIN=local GOPRIVATE=github.com/uug-ai/* CGO_ENABLED=1 GOMAXPROCS=2
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=secret,id=github_token \
    --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    GIT_CONFIG_COUNT=1 \
    GIT_CONFIG_KEY_0=credential.https://github.com.helper \
    GIT_CONFIG_VALUE_0='!f() { if [ "$1" = get ] && [ -f /run/secrets/github_token ]; then printf "username=x-access-token\npassword="; cat /run/secrets/github_token; printf "\n"; fi; }; f' \
    GIT_TERMINAL_PROMPT=0 go mod download && go mod verify

COPY *.go ./
COPY internal/ ./internal/
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    test "$(go env GOVERSION)" = go1.26.10 && \
    test "$(pkg-config --modversion rdkafka)" = 2.16.0 && \
    go build -p=1 -mod=readonly -trimpath -buildvcs=false \
      -tags dynamic,timetzdata,netgo -ldflags="-w -buildid=" -o /out/main . && \
    readelf -d /out/main | grep 'Shared library: \[librdkafka.so.1\]'

FROM builder AS test
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    go vet -p=1 -tags dynamic ./... && \
    go test -p=1 -tags dynamic -timeout 120s ./... && \
    go test -race -p=1 -tags dynamic -timeout 120s ./... && \
    go test -c -p=1 -tags dynamic -o /out/kafka.test . && \
    install -m 0555 /out/kafka.test /runtime/kafka.test && \
    chroot --userspec=65532:65532 /runtime /kafka.test \
      -test.run '^TestDynamicKafkaNativeRuntime$' -test.timeout 30s -test.v && \
    go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 -tags dynamic,timetzdata,netgo ./... && \
    go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 -mode=binary /out/main

FROM scratch AS runtime

ARG VERSION=dev
ARG REVISION=unknown
ARG CREATED
LABEL org.opencontainers.image.title="hub-loitering" \
      org.opencontainers.image.description="Workflow stage emitting trajectory dwell timeline markers" \
      org.opencontainers.image.source="https://github.com/uug-ai/hub-loitering" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.created="${CREATED}" \
      org.opencontainers.image.vendor="UUG.AI"

COPY --from=native --chown=0:0 /runtime/ /
COPY --from=builder --chown=0:0 --chmod=0555 /out/main /app/main
USER 65532:65532
WORKDIR /app
EXPOSE 8080/tcp
ENTRYPOINT ["/app/main"]
