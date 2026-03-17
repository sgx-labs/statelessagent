---
title: "Authentication Approach (Superseded)"
domain: backend
tags: [auth, session, outdated, stale]
confidence: 0.2
content_type: decision
trust_state: stale
review_by: "2025-09-15"
---

# Authentication Approach (Superseded)

> **This decision has been superseded by `decisions/auth-strategy.md`.** We switched from session-based auth to JWT.

## Original Decision (June 2025)

We initially chose **server-side sessions** stored in PostgreSQL:

- Session ID stored in a cookie
- Session data in a `sessions` table with user_id, data (jsonb), expires_at
- Session middleware loads session on every request

## Why We Changed

1. **Mobile clients**: Cookie-based sessions don't work well with mobile apps
2. **Microservices**: Every service needed to query the session database
3. **Scalability**: Session table became a bottleneck under load
4. **Latency**: Extra database round-trip on every request (~5ms added latency)

## Files That Were Changed

The following files were modified or deleted during the migration:
- `middleware/session.go` — **Deleted** (replaced by `middleware/auth.go`)
- `models/session.go` — **Deleted** (sessions table dropped)
- `handlers/login.go` — **Rewritten** to use JWT
- Migration `0005_create_sessions.up.sql` — **Rolled back** and replaced

## Lesson Learned

Should have evaluated JWT from the start. The session approach worked for the prototype but didn't scale to our multi-client architecture.
