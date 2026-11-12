# syntax=docker/dockerfile:1.7
FROM golang:1.26.11-bookworm@sha256:dc40a13b61304647ed7e5d581d246b86413dfc29d63c6d018310feb93cd98fc0 AS builder

# RabbitMQ-only image: CGO is off, so github.com/uug-ai/queue compiles its
# !cgo Kafka stub and no librdkafka or libc is linked or shipped.
ENV GOTOOLCHAIN=local GOPRIVATE=github.com/uug-ai/* CGO_ENABLED=0
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=secret,id=github_token \
    --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    if [ -s /run/secrets/github_token ]; then \
      export GIT_CONFIG_COUNT=1 \
        GIT_CONFIG_KEY_0=credential.https://github.com.helper \
        GIT_CONFIG_VALUE_0='!f() { if [ "$1" = get ]; then printf "username=x-access-token\npassword="; cat /run/secrets/github_token; printf "\n"; fi; }; f'; \
    fi; \
    GIT_TERMINAL_PROMPT=0 go mod download && go mod verify

COPY *.go ./
COPY internal/ ./internal/
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    test "$(go env GOVERSION)" = go1.26.11 && \
    go build -mod=readonly -trimpath -buildvcs=false \
      -tags timetzdata,netgo -ldflags="-w -buildid=" -o /out/main .

FROM builder AS test
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    go vet ./... && go test ./... && CGO_ENABLED=1 go test -race -p 2 ./... && \
    go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./... && \
    go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 -mode=binary /out/main

# Static, shell-less runtime: CA bundle, /tmp (1777) and the 65532 user only.
FROM cgr.dev/chainguard/static:latest@sha256:fc42af33e643f20d08baf03cddcc1c02ba57052dfb02edb0d79f3fc47bcea8d9 AS runtime

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

COPY --from=builder --chown=0:0 --chmod=0555 /out/main /app/main
USER 65532:65532
WORKDIR /app
EXPOSE 8080/tcp
ENTRYPOINT ["/app/main"]
