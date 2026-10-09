# syntax=docker/dockerfile:1.12

# ---- Build stage -------------------------------------------------------------
# Newer Go toolchains build the module fine (go.mod requires >= 1.23); keep this
# on a supported Go release for up-to-date standard library security fixes.
ARG GO_IMAGE=golang:1.26.9-alpine3.23
# Distroless static image (no shell, no package manager), running as uid 65532.
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian13:nonroot

FROM ${GO_IMAGE} AS build

WORKDIR /src

# Download modules first so they are cached independently of the sources
COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .

ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/swarm-horde-bridge ./cmd/server \
    && mkdir -p /out/data

# ---- Runtime stage -----------------------------------------------------------
FROM ${RUNTIME_IMAGE}

COPY --from=build /out/swarm-horde-bridge /usr/local/bin/swarm-horde-bridge
# Data directory for the persisted job store, owned by the nonroot user so a
# fresh named volume mounted on /data inherits the right ownership.
COPY --from=build --chown=65532:65532 /out/data /data

# Configuration comes from environment variables; mounting a YAML file at
# CONFIG_FILE is optional.
ENV PORT=8080 \
    DATA_DIR=/data \
    CONFIG_FILE=/etc/swarm-horde-bridge/config.yaml

USER 65532:65532
EXPOSE 8080

# The binary checks http://127.0.0.1:$PORT/health itself (no shell/curl needed)
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/usr/local/bin/swarm-horde-bridge", "-healthcheck"]

ENTRYPOINT ["/usr/local/bin/swarm-horde-bridge"]
