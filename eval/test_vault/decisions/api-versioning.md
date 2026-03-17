---
title: "API Versioning Strategy"
domain: backend
tags: [api, versioning, rest, decision]
confidence: 0.9
content_type: decision
---

# API Versioning Strategy

**Date:** 2025-09-05
**Status:** Accepted

## Context

We need to evolve our REST API without breaking existing clients. Mobile apps in particular can't be force-updated.

## Decision

**URL-based versioning** with `/v1/`, `/v2/` prefixes:

- Each major version gets its own router group
- Breaking changes increment the major version
- Non-breaking additions (new fields, new endpoints) are added to the current version
- Deprecated versions get a 12-month sunset period with `Sunset` header

### Rate Limiting

Rate limits are configured per API version and per authentication tier:

| Tier     | Requests/min | Burst |
|----------|-------------|-------|
| Free     | 60          | 10    |
| Pro      | 600         | 50    |
| Enterprise | 6000      | 200   |

Rate limit headers: `X-RateLimit-Limit`, `X-RateLimit-Remaining`, `X-RateLimit-Reset`

When rate limited, the API returns `429 Too Many Requests` with a `Retry-After` header.

## Implementation

- Rate limiting uses a sliding window counter in Redis
- Each API key maps to a tier via the `api_keys` table
- Rate limit config lives in `config/rate_limits.yaml`
- Middleware checks limits before routing to handlers

## Consequences

- URL versioning means old versions have dedicated handler code (some duplication)
- Rate limit Redis adds an infrastructure dependency
- Need monitoring dashboards for rate limit hit rates per tier
