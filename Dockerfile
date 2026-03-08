# Stage 1: Build
FROM golang:1.25-bookworm AS builder

ARG VERSION=dev

# CGO is required for sqlite3 + sqlite-vec
ENV CGO_ENABLED=1

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -ldflags "-s -w -X main.Version=${VERSION}" -o /src/build/same ./cmd/same

# Stage 2: Runtime
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Non-root user for security
RUN groupadd -r same && useradd -r -g same -d /home/same -m same

COPY --from=builder /src/build/same /usr/local/bin/same

# Default vault mount point
RUN mkdir -p /vault && chown same:same /vault
VOLUME ["/vault"]
ENV VAULT_PATH=/vault

USER same

# OCI image metadata
LABEL org.opencontainers.image.source="https://github.com/sgx-labs/statelessagent"
LABEL org.opencontainers.image.title="SAME - Stateless Agent Memory Engine"
LABEL org.opencontainers.image.description="Persistent memory for AI coding agents. Local-first vault with semantic search, 12 MCP tools, and Claude Code hooks."
LABEL org.opencontainers.image.licenses="BSL-1.1"
LABEL org.opencontainers.image.url="https://statelessagent.com"

# MCP server uses stdio, no ports needed
ENTRYPOINT ["same"]
CMD ["mcp"]
