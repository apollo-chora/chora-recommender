# chora-recommender

## About

`chora-recommender` is a Go service that exposes a single ADK agent, `content_recommender`, for returning 3-5 ranked LearningAtom recommendations for a learner. Recommendation context comes from ADK session state, including the tenant, learner identity, persona, and optional topic hint; ranked atom candidates can be injected into that state, with a gRPC fallback to `chora-consumption`. LLM calls are sent through `chora-model-gateway`, and the service exposes the standard ADK REST API on port 8080.

## Quick start

Prerequisites:

- Go 1.26.6
- A reachable `chora-model-gateway`, or a local gateway running without TLS for development
- Docker is optional

Build and test the service:

```sh
go build ./...
go vet ./...
go test ./...
```

Run it locally with the default stub recommendation source:

```sh
export CHORA_GATEWAY_TENANT_ID=tenant-1
export CHORA_GATEWAY_GCID=gcid-1
export CHORA_GATEWAY_ENDPOINT=localhost:9090
export CHORA_GATEWAY_INSECURE=1
export CONSUMPTION_GRPC_ENDPOINT=stub://chora-consumption

go run ./cmd/recommender
```

The server listens on `http://localhost:8080` by default.

To build the container image:

```sh
docker build -t chora-recommender .
docker run --rm -p 8080:8080 \
  -e CHORA_GATEWAY_TENANT_ID=tenant-1 \
  -e CHORA_GATEWAY_GCID=gcid-1 \
  -e CHORA_GATEWAY_ENDPOINT=host.docker.internal:9090 \
  -e CHORA_GATEWAY_INSECURE=1 \
  chora-recommender
```

## Usage

The service uses the ADK REST API. The supported routes are:

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/api/apps/{app}/users/{user}/sessions` | Create a session |
| `POST` | `/api/apps/{app}/users/{user}/sessions/{id}` | Create a session with an explicit ID |
| `GET` | `/api/apps/{app}/users/{user}/sessions/{id}` | Fetch a session |
| `DELETE` | `/api/apps/{app}/users/{user}/sessions/{id}` | Delete a session |
| `POST` | `/api/run` | Run the agent and return JSON events |
| `POST` | `/api/run_sse` | Run the agent as an SSE stream |

Use `content_recommender` as the ADK app name in the request path. Callers create a session first, then run turns against that session.

Session state provides the trusted per-learner context used by the agent and recommendation tool:

```json
{
  "tenant_id": "<tenant-uuid>",
  "user_gcid": "<gcid>",
  "learner_persona": "curious-explorer",
  "topic_hint": "graph-algorithms",
  "atom_candidates": "[{\"atom_id\":\"atom-1\",\"title\":\"...\",\"snippet\":\"...\"}]"
}
```

`tenant_id` is required by the recommendation tool. `user_gcid` identifies the learner; `learner_gcid` is accepted as an alias. `learner_persona` supports `curious-explorer`, `cert-focused`, and `social-leader`, with `curious-explorer` as the fallback. `topic_hint` is optional. When `atom_candidates` contains a valid non-empty JSON list, those candidates are used directly; otherwise the service uses its configured searcher.

The `recommend_atoms_for_learner` tool accepts a learner GCID plus optional persona, topic hint, and limit. The default limit is 5 and the maximum is 10.

Configuration is controlled with environment variables:

| Variable | Purpose | Default |
| --- | --- | --- |
| `PORT` | HTTP listen port | `8080` |
| `RECOMMENDER_MODEL` | Override the configured primary model | `longcat-2.5-preview` |
| `CHORA_GATEWAY_ENDPOINT` | Model gateway gRPC target | `gateway.chora.site:443` |
| `CHORA_GATEWAY_TENANT_ID` | Process-level fallback tenant for gateway calls | unset; required |
| `CHORA_GATEWAY_GCID` | Process-level fallback actor for gateway calls | unset; required |
| `CHORA_GATEWAY_AUDIENCE` | Gateway token audience | `https://gateway.chora.site` |
| `CHORA_GATEWAY_TOKEN` | Static bearer token for the gateway | unset |
| `CHORA_GATEWAY_INSECURE` | Use plaintext gRPC for a local gateway | unset |
| `CONSUMPTION_GRPC_ENDPOINT` | Recommendation source; `stub://...` enables deterministic stubs | `stub://chora-consumption` |
| `TENANCY_GRPC_ENDPOINT` | Tenancy endpoint | `stub://chora-tenancy` |
| `CREATION_GRPC_ENDPOINT` | Creation endpoint | `stub://chora-creation` |
| `CHORA_ENV` | Environment label | `dev` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP trace endpoint | stdout |
| `CHORA_SERVICE_VERSION` | OTLP `service.version` value | `dev` |

For production gateway connections, leave `CHORA_GATEWAY_INSECURE` unset so the client uses TLS and `CHORA_GATEWAY_TOKEN`.

The embedded agent configuration in `internal/agentconfig/recommender.yaml` defines the `recommend` sub-agent as HIGH tier, with `longcat-2.5-preview` as the primary model, `longcat-2.5-preview` as its fallback (single-provider deployment), and prompt version `v1`. `RECOMMENDER_MODEL` can override the primary model without changing the configured fallback chain.

## Development

The repository is organized around the service entry point, agent composition, configuration, and recommendation adapter:

```text
cmd/recommender/                  Service entry point and HTTP server
internal/agent/                   Prompt composition and per-turn instruction provider
internal/agentconfig/             Embedded model and prompt configuration
internal/tool/                    Recommendation tool and Searcher port
internal/adapter/consumptionrag/  gRPC adapter for chora-consumption
```

The application entry point is `cmd/recommender/main.go`. The recommender prompt is composed per turn from session state in `internal/agent/composer.go`, with few-shot fixtures under `internal/agent/few_shots/`. The production recommendation adapter calls the `RecommendAtomsForLearner` RPC exposed by `chora-consumption`.

Run the full local verification suite:

```sh
gofmt -l .
go mod tidy
go vet ./...
go test ./...
```

CI runs the same formatting, module-consistency, vet, and test checks on pushes and pull requests targeting `main`.

The test suite does not require a live broker, database, gateway, or recommendation backend. For container builds, the Dockerfile runs the build, vet, and tests in a Go builder stage, then copies the binary into a distroless nonroot runtime image.
