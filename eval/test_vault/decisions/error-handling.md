---
title: "Error Handling Convention"
domain: engineering
tags: [errors, logging, observability, decision]
confidence: 0.9
content_type: decision
---

# Error Handling Convention

**Date:** 2025-07-30
**Status:** Accepted

## Context

Inconsistent error handling across services makes debugging difficult. Some services return raw errors, others swallow them.

## Decision

### Error Types

All API errors use a structured error response:

```json
{
  "error": {
    "code": "RESOURCE_NOT_FOUND",
    "message": "User with ID xyz not found",
    "request_id": "req_abc123"
  }
}
```

### Error Codes

- `VALIDATION_ERROR` (400) — Invalid request parameters
- `UNAUTHORIZED` (401) — Missing or invalid authentication
- `FORBIDDEN` (403) — Valid auth but insufficient permissions
- `RESOURCE_NOT_FOUND` (404) — Requested resource doesn't exist
- `RATE_LIMITED` (429) — Too many requests
- `INTERNAL_ERROR` (500) — Unexpected server error

### Logging

- All 5xx errors are logged with full stack trace and request context
- 4xx errors are logged at INFO level (no stack trace)
- Structured logging via `slog` with JSON output
- Every log line includes `request_id`, `user_id`, `workspace_id`

### Error Wrapping

Go errors use `fmt.Errorf("context: %w", err)` for wrapping. Never lose the original error.

Sentinel errors for domain logic:
```go
var (
    ErrNotFound      = errors.New("not found")
    ErrAlreadyExists = errors.New("already exists")
    ErrForbidden     = errors.New("forbidden")
)
```

## Consequences

- Consistent client-side error handling
- Request ID enables end-to-end tracing
- Structured logging works well with log aggregation (Grafana Loki)
