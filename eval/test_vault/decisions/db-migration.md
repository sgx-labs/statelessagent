---
title: "Database Migration Strategy"
domain: backend
tags: [database, migration, postgres, decision]
confidence: 0.85
content_type: decision
---

# Database Migration Strategy

**Date:** 2025-10-20
**Status:** Accepted

## Context

Our PostgreSQL schema needs to evolve safely as we add features. We've had incidents where manual ALTER TABLE statements broke production.

## Decision

Adopt **golang-migrate** for all schema changes:

1. All migrations are numbered SQL files in `migrations/`
2. Migrations run automatically on deploy via init container
3. Every migration must have an UP and DOWN file
4. No migration may take a lock longer than 5 seconds (use `CREATE INDEX CONCURRENTLY`)

### Migration naming convention
```
NNNN_description.up.sql
NNNN_description.down.sql
```

### Rules
- Never modify a migration that has been applied to production
- Add new columns as nullable, then backfill, then add NOT NULL constraint
- Foreign keys use `ON DELETE SET NULL` unless business logic requires CASCADE
- Every migration is reviewed by at least one DBA-experienced engineer

## Current Schema Version

Schema version 8. Key tables:
- `users` - core user accounts
- `workspaces` - multi-tenant workspace isolation
- `api_keys` - per-workspace API keys with scoped permissions
- `audit_log` - immutable append-only audit trail

## Consequences

- Deploy pipeline is slightly slower (migration check adds ~2s)
- Rollback requires running DOWN migrations manually
- Schema version tracked in `schema_migrations` table
