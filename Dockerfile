# Stage 1: Build
FROM golang:1.25-bookworm AS builder

# CGO is required for sqlite3 + sqlite-vec
ENV CGO_ENABLED=1

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -ldflags "-s -w" -o /src/build/same ./cmd/same

# Stage 2: Runtime
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /src/build/same /usr/local/bin/same

# Default vault mount point
VOLUME ["/vault"]
ENV VAULT_PATH=/vault

ENTRYPOINT ["same"]
CMD ["status"]
