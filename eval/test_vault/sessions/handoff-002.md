---
title: "Session Handoff 002 — Database Schema v8 Migration"
domain: backend
tags: [handoff, database, migration, session]
confidence: 0.8
content_type: handoff
---

# Session Handoff 002 — Database Schema v8 Migration

**Date:** 2025-11-20
**Agent:** claude-code
**Duration:** 1.5 hours

## What was accomplished

- Created migration 0008_add_audit_log.up.sql and 0008_add_audit_log.down.sql
- Added `audit_log` table with columns: id, actor_id, action, resource_type, resource_id, metadata (jsonb), created_at
- Added index on (actor_id, created_at) for efficient per-user audit queries
- Updated the Go model in `models/audit.go` with struct tags and validation

## What's in progress

- Audit log writer service that accepts events and batches inserts
- The batch writer uses a channel + ticker pattern (100 events or 5 seconds, whichever comes first)

## Decisions made during session

- Chose `jsonb` for metadata column instead of separate columns — more flexible for different event types
- Set `ON DELETE SET NULL` for actor_id FK so audit records survive user deletion
- Added a partial index on `action = 'login'` for the security dashboard query

## Blockers

- Need to verify that `CREATE INDEX CONCURRENTLY` works with our migration runner
- Audit log retention policy not yet defined (30 days? 90 days? indefinite?)

## Next steps

1. Finish the batch writer with proper shutdown handling
2. Add audit logging middleware that auto-captures API mutations
3. Define retention policy and implement cleanup job
