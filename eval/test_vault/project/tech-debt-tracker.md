---
title: "Technical Debt Tracker"
domain: engineering
tags: [tech-debt, maintenance, cleanup]
confidence: 0.7
content_type: tracker
---

# Technical Debt Tracker

## High Priority

### TD-001: Redis connection pooling undersized
- **Where:** `config/redis.go`
- **Problem:** Pool size hardcoded to 10, insufficient for production load
- **Impact:** Under high concurrency, Redis operations queue and add latency
- **Fix:** Make pool size configurable, default to 50
- **Effort:** Small (1-2 hours)

### TD-002: No graceful shutdown handling
- **Where:** `cmd/server/main.go`
- **Problem:** Server stops immediately on SIGTERM, dropping in-flight requests
- **Impact:** Users may see failed requests during deploys
- **Fix:** Implement `http.Server.Shutdown` with 30-second timeout
- **Effort:** Small (2-3 hours)

### TD-003: Search result scoring inconsistency
- **Where:** `internal/store/search.go`
- **Problem:** Vector scores and keyword scores are on different scales
- **Impact:** Keyword results sometimes inappropriately outrank vector results
- **Fix:** Normalize both score types to [0, 1] before merging
- **Effort:** Medium (1-2 days)

## Medium Priority

### TD-004: Frontend bundle size
- **Where:** `frontend/`
- **Problem:** D3.js adds 200KB to the bundle even when graph view isn't used
- **Fix:** Lazy-load the graph visualization component
- **Effort:** Small (2-3 hours)

### TD-005: Test fixtures are copy-pasted
- **Where:** Various `_test.go` files
- **Problem:** Same note creation code duplicated across 15+ test files
- **Fix:** Create `testutil.CreateTestVault()` factory
- **Effort:** Medium (half day)

## Low Priority

### TD-006: Config file parsing doesn't validate types
- **Where:** `internal/config/config.go`
- **Problem:** Invalid config values silently use zero values
- **Fix:** Add strict validation with helpful error messages
- **Effort:** Small (2-3 hours)

### TD-007: MCP server logs to stdout
- **Where:** `internal/mcp/server.go`
- **Problem:** Log output interferes with MCP JSON-RPC protocol
- **Fix:** Route MCP server logs to stderr or a log file
- **Effort:** Small (1 hour)
