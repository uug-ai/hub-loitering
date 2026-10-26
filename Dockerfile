# syntax=docker/dockerfile:1.7

FROM golang:1.26.10-bookworm@sha256:632dff7351799a5a9954a48dc06670ade72ee44e8c351cdc0e5a57af2fea21af AS builder

ENV GOTOOLCHAIN=local GOPRIVATE=github.com/uug-ai/* CGO_ENABLED=1
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
    go build -mod=readonly -trimpath -buildvcs=false \
      -tags timetzdata,netgo -ldflags="-w -buildid=" -o /out/main .

FROM builder AS test
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    go vet ./... && go test ./... && go test -race -p 2 ./... && \
    go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./... && \
    go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 -mode=binary /out/main

FROM cgr.dev/chainguard/glibc-dynamic:latest@sha256:2827063c9efb4ba971d8621eb75f4e61b8d19850a6293a797df37358cc6f687b AS runtime

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
