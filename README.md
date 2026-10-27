# hub-loitering

A standalone, **clone-and-build** example of a custom Kerberos Hub workflow
**stage** worker. It is the companion module for the **Build a custom workflow
stage** tutorial in the Kerberos Hub documentation.

Where the [`hub-anpr`](https://github.com/uug-ai/hub-workflows/tree/main/hub-anpr)
example emits a **detection**, `hub-loitering` emits a **marker** — a named span
on the recording's timeline.
It is wired up as a **marker demo**: a deliberately *non-redundant* stage that
adds a new kind of result instead of re-deriving what the classifier already
produced.

The hub-workflows engine dispatches a `loitering` stage to this worker; the
worker measures how long a subject lingers in the run's `classify` trajectory,
and routes a `marker` block back to the engine, which persists it to the markers
collection and shows it on the recording's timeline.

> This is a **demo**, not a production behaviour analyser. It works purely from
> the classify boxes already on the run (no media fetch, no model), so it builds
> and runs end-to-end. Swap `internal/loitering` for real dwell-time analysis
> while keeping the same queue wiring.

## How it fits the workflow

Build and test with Go 1.26.10 or later. Production images use digest-pinned
Go 1.26.10 and Wolfi build bases, with a package-managed dynamic Kafka runtime.

```
classify stage ─▶ (engine) ─dispatch▶ kcloud-loitering-queue.fifo ─▶ hub-loitering
                                                                          │
                                                  measures longest dwell  │
                                                  builds a marker block    ▼
   engine ◀─ route result ◀─ hub-workflows-queue ◀── { blocks: [ marker ] }
     │
     └─▶ markers collection  +  run.Results["loitering"]  (fans out conditional stages)
```

The worker is **DB-free**: all persistence is delegated to the workflows engine
via the `loitering` result published on the workflows queue.

## Production image contract

Images are published as `ghcr.io/uug-ai/hub-loitering:<version>` for Linux
amd64 and arm64. Deploy the immutable manifest digest from the verified release,
not `latest`. The Wolfi build base uses Chainguard's public `latest` channel
pinned to a verified multi-platform digest. The final scratch image contains
only the genuine APK-installed runtime tree and the application, not the
builder's compiler or package-manager executables. Refresh both base pins and
runtime packages regularly.

* Entrypoint: `/app/main`, working directory `/app`, UID/GID `65532:65532`.
  The executable is root-owned and mode `0555`; the runtime user cannot replace it.
* No shell, curl, package manager, model weights, media assets or writable
  application directories are required. The existing trajectory/dwell logic
  reads only the workflow invocation and returns the same marker result.
* The shared queue library's Kafka implementation is retained, dynamically
  linked with `-tags dynamic,timetzdata,netgo` against APK
  `librdkafka=2.16.0-r0`. Its OpenSSL, SASL, compression, HTTP/OIDC and glibc
  dependencies come from real signed APK packages; `/lib/apk/db/installed`
  retains their actual metadata and file ownership. This avoids hiding native
  Kafka components inside the Go wrapper's bundled static archive.
* `ca-certificates-bundle` supplies the CA bundle. The complete package tree
  is copied without blanket `--chmod`, preserving searchable `/etc` parents.
  Timezone data is embedded with `timetzdata`. Go builds retain symbol tables
  (`-w -buildid=`, **not** `-s`) for binary vulnerability reachability checks.
* Configure `RABBITMQ_HOST`, `RABBITMQ_EXCHANGE`, `RABBITMQ_USERNAME` and
  `RABBITMQ_PASSWORD` through deployment secrets/configuration.
  `LOITERING_QUEUE` defaults to `kcloud-loitering-queue.fifo`;
  `WORKFLOWS_QUEUE` defaults to `hub-workflows-queue`. Optional `LOG_LEVEL` and
  `OTEL_EXPORTER_OTLP_ENDPOINT` retain their existing behavior.
* Outbound access is needed to RabbitMQ and, when configured, the OTLP endpoint
  (plus DNS). No MongoDB, storage downloads or inference service is required.
* Prometheus metrics use HTTP `GET /metrics` on port **8080**, expected status
  **200**, with a recommended probe timeout of **2 seconds**. This is process
  liveness, **not** queue readiness. CI also requires one consumer on the stage
  queue. There is no shell-based image healthcheck.
* Helm/deployment integration must use numeric `runAsUser: 65532`,
  `runAsGroup: 65532`, `runAsNonRoot: true`, `readOnlyRootFilesystem: true`,
  `allowPrivilegeEscalation: false`, dropped `ALL` capabilities and
  `seccompProfile.type: RuntimeDefault`. No writable volume or tmpfs is needed.
  Replace any old `/home/hub-loitering/main` command override or exec/curl probe
  with the image entrypoint or an HTTP metrics probe. Chart changes belong in
  the deployment repository, not this service.

The `.dockerignore` allowlist sends only Go sources and module manifests to the
builder. Public Go checksum verification stays enabled; `GOPRIVATE` scopes only
`github.com/uug-ai/*`. If a private dependency is introduced, use
`docker buildx build --secret id=github_token,env=GITHUB_TOKEN .` with a scoped,
read-only token; never use credential build arguments. Current CI builds without
private-module credentials.

### Verification and release

The **Security scan** workflow calls the existing shared hardened **worker**
profile on native amd64 and arm64 runners. It runs the Dockerfile `test` target
(vet, unit tests, race tests, pinned govulncheck source and binary checks), scans
source/configuration/secrets, inspects the runtime filesystem, and starts the
actual image against an isolated CI RabbitMQ broker with a read-only root,
all capabilities dropped and no privilege escalation. It checks both metrics
and the default stage queue consumer; no production credentials or traffic are
used. Workflow invocation/result, tracing, credential stripping, and trajectory
behavior are covered by the existing Go tests, not by the startup smoke alone.

`TestDynamicKafkaNativeRuntime` checks dynamic librdkafka 2.16.0, required
gzip/snappy/SSL/SASL/LZ4/SCRAM/OAUTHBEARER/Zstd/HTTP/OIDC feature availability,
CA parsing, and a bounded producer/consumer roundtrip through librdkafka's
in-process mock broker. It runs both in the runtime package root and in the
**actual final image**, with UID/GID 65532, read-only root, no capabilities,
no external network, and readable `/etc/resolv.conf`. The verifier is mounted
read-only for the test; it is not shipped in the production image. No production
authentication settings are changed. This does **not** establish external
SASL/TLS interoperability: those still require deployment-specific tests
against the real configured broker and identity provider.

The additional smoke extracts the real APK database, inventories all native
ELF dependencies/interpreter and package-owned `.so` files, and retains the
ownership joins under `runtime-evidence`. The `native-inventory` CI job requires
exact name/version closure between every installed APK, the scanner OS
inventory and the SPDX package inventory, on **both** architectures.
It retains comparison tables and counts in `native-inventory-closure`.

Final image scans block medium/high/critical vulnerabilities and secrets;
the shared pipeline retains JSON reports, rootfs inventory, smoke logs and
`image.spdx.json` in per-architecture `hardened-image-*` artifacts. These gates
must pass on both architectures before calling the image verified.

**Bump release** is the sole release entrypoint. It reserves a draft tag, runs
the same native gates, publishes provenance/SBOM and immutable image digests,
signs the multi-platform image and only then finalizes the release and requests
the existing GitOps update at `kerberoshub.services.loitering.tag`.
`publish: false` validates without creating a version; `existing_tag` retries
an existing draft. No release-created workflow bypasses those gates.
This rollout consumes the immutable `uug-ai/workflows` hardened worker and
draft-release interfaces at `f987049597f729e1ca3ca801f6fdedc2b1b06917`;
no new shared profile is required.
The worker smoke explicitly selects `QUEUE_SYSTEM=RABBITMQ`.
Keep the caller ref and signer identity synchronized when promoting the shared
workflow changes.

Download the SBOM and scan evidence from the release workflow artifacts. Verify
the published digest with Cosign, binding the certificate identity to the
successful
`uug-ai/workflows/.github/workflows/release-create.yml@f987049597f729e1ca3ca801f6fdedc2b1b06917`
workflow and issuer `https://token.actions.githubusercontent.com`; see the
[image-hardening guide](https://learn.uug.ai/guides/security/image-hardening/)
for the platform policy. Do not infer complete deployment CIS compliance from
image construction alone.

## Register it as a stage

Which runs reach this worker is the engine's job, not the worker's. Add the
stage to `PIPELINE_STAGE_REGISTRY` so a run is dispatched here once `classify`
has labelled a person:

```json
[
  {
    "operation": "loitering",
    "dispatch": "conditional",
    "queue": "kcloud-loitering-queue.fifo",
    "needs": [
      { "operation": "classify",
        "condition": { "path": "inputs.classify.details.*.classified", "op": "eq", "value": "person" } }
    ]
  }
]
```

Match `inputs.classify.details.*.classified`, not `inputs.classify.properties`:
the `.details.*.classified` fan-out is the per-object class this worker actually
reads (a run only reaches loitering when a detected object was classified
`person`). `inputs.classify.properties` is a separate, frequently-empty summary
list, so gating on it silently drops many person recordings.

`dispatch` is the closed enum `always` | `conditional` (a stage with `needs`
must be `conditional`), and a condition `path` is absolute from the run root —
`inputs.classify.details.*.classified`, not a bare `details`. The `*` fans out
across the `details` array; the engine validates both `dispatch` and `path` at
boot and refuses to start on an invalid registry.

## Configuration

| Env var             | Default                       | Description                                                        |
| ------------------- | ----------------------------- | ------------------------------------------------------------------ |
| `LOITERING_QUEUE`   | `kcloud-loitering-queue.fifo` | Queue this worker consumes (must match the engine's dispatch name) |
| `WORKFLOWS_QUEUE`   | `hub-workflows-queue`         | Queue the worker routes its result back to                         |
| `LOG_LEVEL`         | `info`                        | `trace` \| `debug` \| `info` \| `warn` \| `error`                  |
| `RABBITMQ_HOST`     | —                             | RabbitMQ host                                                      |
| `RABBITMQ_EXCHANGE` | —                             | RabbitMQ exchange                                                  |
| `RABBITMQ_USERNAME` | —                             | RabbitMQ username                                                  |
| `RABBITMQ_PASSWORD` | —                             | RabbitMQ password                                                  |

See `.env.example` for the full set. In a Hub deployment these are injected by
the Helm chart (`charts/hub` → `hub-stage.yaml`); locally they are provided via
`.env.local`.

Prometheus metrics are exposed on `:8080/metrics`.

## Clone and build

```bash
git clone https://github.com/uug-ai/hub-loitering
cd hub-loitering
go build ./...
go test ./...
```

The module pins `github.com/uug-ai/models v1.6.3` and `github.com/uug-ai/queue`
in `go.mod`, so it builds against published releases with nothing else checked
out.

## Run locally

```bash
go run .
```

Inside the uug-ai monorepo a local (git-ignored) `go.work` wires the sibling
`models` / `queue` checkouts, so you can also build against unreleased changes.

## Layout

```
main.go                       Command wiring: logger, env, queue, metrics, read loop
handleMessage.go              Transport glue: dispatch -> internal/loitering -> route result
internal/loitering/           Stage behaviour (Process, HasCredentials, Operation)
```
