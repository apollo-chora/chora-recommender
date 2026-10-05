# chora-recommender

Content Recommender crew for Chora — a P1 single-agent ReAct ADK-Go agent
(Google `google.golang.org/adk` framework) that surfaces 3-5 ranked
LearningAtoms for a learner, biased by persona + topic hint. It serves the
Phyllis Step 8 daily-dose 40% slot.

Module path: `github.com/apollo-chora/chora-recommender`.

The crew is cloud-neutral: it serves the standard ADK REST API, calls the
model broker (`chora-model-gateway`) over gRPC for every LLM turn, and
optionally dials `chora-consumption` for real atom recommendations. No cloud
account or managed service is required.

## What it does

1. **Serves the standard ADK REST API** on `:8080` — session create/list,
   agent run (JSON events), and SSE stream.
2. **Ranks atoms per learner** via the `recommend_atoms_for_learner` tool.
   The primary path reads pre-fetched, pre-ranked atom candidates injected
   into the ADK session state by the caller (`chora-consumption`, which owns
   the `atom_index` projection) — no backend dial. The fallback path dials
   `chora-consumption` gRPC, or returns deterministic stub atoms when
   `CONSUMPTION_GRPC_ENDPOINT=stub://…`.
3. **Composes its instruction per turn** from live session state
   (`tenant_id` / `user_gcid` / `learner_persona` / `topic_hint`) via an
   `InstructionProvider`, so the model always sees the real runtime context.
4. **Routes every LLM call through the model broker** (`chora-model-gateway`)
   with per-request tenant/gcid propagation and the `content_recommendation`
   action code, so mana metering + cost attribution stay centralized.
5. **Emits `chora.ai_kernel.agent.terminated.v1`** on every agent-run boundary
   exit (structured log event via `terminationplugin`).

## Architecture

- **Compute**: any host running the Go binary or the container image.
- **Sessions**: in-memory only (`session.InMemoryService`). A recommend
  turn's session must hit the same instance, so run replicas=1.
- **Model calls**: gRPC to `chora-model-gateway` (via
  `chora-adk-common/modelgatewayclient`); TLS + `CHORA_GATEWAY_TOKEN` in
  production, plaintext with `CHORA_GATEWAY_INSECURE=1` for local dev.
- **Traces**: standard OTLP via `chora-common/otel`
  (`OTEL_EXPORTER_OTLP_ENDPOINT`; stdout when unset).
- **Events**: none — the termination event is a structured log record, not a
  bus message. No NATS required.
- **Database**: none.

## HTTP surface

The `{app}` path segment is the ADK agent name: `content_recommender` (or
empty). The single-agent loader accepts both.

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/api/apps/{app}/users/{user}/sessions` | Create session (state rides in the body) |
| `POST` | `/api/apps/{app}/users/{user}/sessions/{id}` | Create session with explicit id |
| `GET` | `/api/apps/{app}/users/{user}/sessions/{id}` | Fetch session |
| `DELETE` | `/api/apps/{app}/users/{user}/sessions/{id}` | Delete session |
| `POST` | `/api/run` | Run the agent (JSON event list) |
| `POST` | `/api/run_sse` | Run the agent (SSE stream) |

Session state carries the per-learner context the tool and the instruction
provider read:

```json
{
  "tenant_id": "<tenant-uuid>",
  "user_gcid": "<gcid>",
  "learner_persona": "curious-explorer",
  "topic_hint": "graph-algorithms",
  "atom_candidates": "[{\"atom_id\":\"atom-1\",\"title\":\"…\",\"snippet\":\"…\"}]"
}
```

`tenant_id` is required (RLS-bearing — the recommend tool refuses without
it). `user_gcid` (or the `learner_gcid` alias) identifies the learner.
`atom_candidates` is the pre-fetched ranked slate written by the caller.

## Configuration

| Variable | Purpose | Local default |
| --- | --- | --- |
| `PORT` | REST API listen port | `8080` |
| `RECOMMENDER_MODEL` | Ops override of the agentconfig primary model | `gemini-3.1-pro-preview` (HIGH tier) |
| `CHORA_GATEWAY_ENDPOINT` | Model-gateway gRPC target | `gateway.chora.site:443` |
| `CHORA_GATEWAY_TENANT_ID` | Process-fallback tenant for gateway Invoke | unset (required) |
| `CHORA_GATEWAY_GCID` | Process-fallback actor for gateway Invoke | unset (required) |
| `CHORA_GATEWAY_AUDIENCE` | Audience of the gateway token | `https://gateway.chora.site` |
| `CHORA_GATEWAY_TOKEN` | Static bearer token for the gateway | unset |
| `CHORA_GATEWAY_INSECURE` | Plaintext gRPC to a local gateway (dev only) | unset |
| `CONSUMPTION_GRPC_ENDPOINT` | Atom source: `stub://…` for deterministic stubs, gRPC URL for real atoms | `stub://chora-consumption` |
| `TENANCY_GRPC_ENDPOINT` | Tenancy service (reserved) | `stub://chora-tenancy` |
| `CREATION_GRPC_ENDPOINT` | Creation service (reserved, `cite_atom`) | `stub://chora-creation` |
| `CHORA_ENV` | Environment label | `dev` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP trace endpoint | stdout |
| `CHORA_SERVICE_VERSION` | Stamped as the OTLP `service.version` attribute | `dev` |

## Build and test

```sh
go build ./...
go vet ./...
gofmt -l .   # must be empty
go test ./...
```

The suite is hermetic — no broker, database, gateway, or network is required.

## Docker

```sh
docker build -t chora-recommender .
docker run --rm -p 8080:8080 \
  -e CHORA_GATEWAY_TENANT_ID=tenant-1 \
  -e CHORA_GATEWAY_GCID=gcid-1 \
  -e CHORA_GATEWAY_ENDPOINT=host.docker.internal:9090 \
  -e CHORA_GATEWAY_INSECURE=1 \
  chora-recommender
```

The image is multi-stage (build + vet + test → distroless static, nonroot)
and builds from this repository's context alone.
