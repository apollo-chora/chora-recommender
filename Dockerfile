# syntax=docker/dockerfile:1.6
#
# chora-recommender Dockerfile — standalone Go service (Content Recommender
# ADK-Go crew, P1 single-agent ReAct; Phyllis Step 8 daily-dose 40% slot).
#
# Build context = this repository. Shared Chora modules (chora-adk-common,
# chora-contracts) are resolved through the Go module proxy, not a workspace.
#
# The crew is cloud-neutral: it serves the standard ADK REST API, calls the
# model broker (chora-model-gateway) over gRPC, and optionally dials
# chora-consumption for real atom recommendations. No cloud account or
# managed service is required.

ARG GO_VERSION=1.26.6
ARG ALPINE_VERSION=3.23
ARG SERVICE_NAME=chora-recommender
ARG GIT_SHA=unknown
ARG BUILD_TIME=unknown

############################
# Stage 1 — build + vet + test
############################
FROM golang:${GO_VERSION}-alpine${ALPINE_VERSION} AS builder

ARG SERVICE_NAME
ARG GIT_SHA
ARG BUILD_TIME
ARG TARGETARCH

WORKDIR /src

RUN apk add --no-cache ca-certificates git

COPY . .

RUN go mod download

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=${TARGETARCH}

# A service image that ships with a failing test is worse than no image:
# the failure would surface only at runtime in a downstream stack.
RUN go build -trimpath \
      -ldflags "-s -w \
        -X main.serviceName=${SERVICE_NAME} \
        -X main.gitSHA=${GIT_SHA} \
        -X main.buildTime=${BUILD_TIME}" \
      -o /out/recommender \
      ./cmd/recommender \
 && go vet ./... \
 && go test ./...

############################
# Stage 2 — runtime
############################
FROM gcr.io/distroless/static-debian12:nonroot

ARG SERVICE_NAME
ARG GIT_SHA
ARG BUILD_TIME

LABEL org.opencontainers.image.title="${SERVICE_NAME}" \
      org.opencontainers.image.source="https://github.com/apollo-chora/chora-recommender" \
      org.opencontainers.image.revision="${GIT_SHA}" \
      org.opencontainers.image.created="${BUILD_TIME}" \
      org.opencontainers.image.vendor="Chora Platform" \
      org.opencontainers.image.licenses="UNLICENSED" \
      io.chora.service="${SERVICE_NAME}" \
      io.chora.git-sha="${GIT_SHA}" \
      io.chora.build-time="${BUILD_TIME}"

WORKDIR /

COPY --from=builder /out/recommender /recommender

USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/recommender"]
