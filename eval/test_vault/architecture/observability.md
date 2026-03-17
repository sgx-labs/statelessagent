---
title: "Observability Stack"
domain: devops
tags: [observability, monitoring, metrics, logging, architecture]
confidence: 0.75
content_type: architecture
---

# Observability Stack

## Logging

### Structured Logging
- All logs are JSON-formatted via Go's `slog` package
- Every log line includes: `timestamp`, `level`, `message`, `request_id`
- Request-scoped fields: `user_id`, `workspace_id`, `method`, `path`

### Log Levels
- **DEBUG**: Detailed trace for development (disabled in production)
- **INFO**: Normal operations (request started/completed, index updated)
- **WARN**: Recoverable issues (cache miss, retry, slow query)
- **ERROR**: Failures requiring attention (DB connection lost, embedding failed)

### Log Aggregation
- Logs shipped to Grafana Loki via Fly.io log drain
- 30-day retention for INFO+, 7-day for DEBUG
- Structured queries via LogQL

## Metrics

### Application Metrics
- `search_latency_seconds` — Histogram of search response times
- `index_notes_total` — Counter of indexed notes
- `cache_hit_ratio` — Gauge of query cache effectiveness
- `embedding_latency_seconds` — Histogram of embedding generation time
- `active_vaults` — Gauge of currently registered vaults

### Infrastructure Metrics
- CPU, memory, disk via Fly.io machine metrics
- SQLite database size per vault
- Redis connection pool utilization

### Alerting Rules
- Search p99 > 500ms for 5 minutes → warning
- Error rate > 5% for 2 minutes → critical
- Disk usage > 80% → warning
- Database WAL size > 100MB → warning (checkpoint needed)

## Tracing

- Distributed tracing via OpenTelemetry (planned, not yet implemented)
- Request IDs propagated through all service boundaries
- Currently relying on correlated logs for debugging
