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

```
classify stage ─▶ (engine) ─dispatch▶ kcloud-loitering-queue.fifo ─▶ hub-loitering
                                                                          │
                                                  measures longest dwell  │
                                                  builds a marker block    ▼
   engine ◀─ route result ◀─ kcloud-workflows-queue ◀── { blocks: [ marker ] }
     │
     └─▶ markers collection  +  run.Results["loitering"]  (fans out conditional stages)
```

The worker is **DB-free**: all persistence is delegated to the workflows engine
via the `loitering` result published on the workflows queue.

## Register it as a stage

Which runs reach this worker is the engine's job, not the worker's. Add the
stage to `PIPELINE_STAGE_REGISTRY` so a run is dispatched here once `classify`
has labelled a person:

```json
[
  {
    "operation": "loitering",
    "dispatch": "queue",
    "queue": "kcloud-loitering-queue.fifo",
    "needs": [
      { "operation": "classify",
        "condition": { "path": "properties", "op": "contains", "value": "person" } }
    ]
  }
]
```

## Configuration

| Env var             | Default                       | Description                                                        |
| ------------------- | ----------------------------- | ------------------------------------------------------------------ |
| `LOITERING_QUEUE`   | `kcloud-loitering-queue.fifo` | Queue this worker consumes (must match the engine's dispatch name) |
| `WORKFLOWS_QUEUE`   | `kcloud-workflows-queue`      | Queue the worker routes its result back to                         |
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
