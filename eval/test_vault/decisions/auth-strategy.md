---
title: "Authentication Strategy Decision"
domain: backend
tags: [auth, security, jwt, decision]
confidence: 0.9
content_type: decision
---

# Authentication Strategy Decision

**Date:** 2025-11-15
**Status:** Accepted
**Participants:** Backend team

## Context

We need a stateless authentication mechanism for the API that supports both web clients and mobile apps. Key requirements:

- Stateless (no server-side session storage)
- Support for token refresh without re-login
- Compatible with microservice architecture
- Short-lived access tokens for security

## Decision

We chose **JWT (JSON Web Tokens)** with a dual-token approach:

1. **Access token**: Short-lived (15 minutes), contains user claims
2. **Refresh token**: Long-lived (7 days), stored in httpOnly cookie, rotated on use

### Why not session-based auth?
- Our API is consumed by mobile clients that can't easily manage cookies
- Microservices need to validate tokens independently without a shared session store
- JWT allows embedding role/permission claims directly in the token

### Why not OAuth2 with a third-party provider only?
- We need fine-grained custom claims (workspace, role, feature flags)
- Third-party providers add latency and a single point of failure
- We still support OAuth2 for social login, but issue our own JWTs after

## Token Structure

```json
{
  "sub": "user_id",
  "workspace": "ws_abc123",
  "role": "admin",
  "features": ["beta_search", "graph_view"],
  "exp": 1700000000,
  "iat": 1699999100
}
```

## Consequences

- Need to implement token rotation logic on the client side
- Must handle clock skew (5-second grace period on expiry)
- Refresh token revocation requires a small deny-list in Redis
- All services must share the JWT signing key (via secrets manager)
