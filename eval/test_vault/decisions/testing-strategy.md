---
title: "Testing Strategy"
domain: engineering
tags: [testing, ci, quality, decision]
confidence: 0.85
content_type: decision
---

# Testing Strategy

**Date:** 2025-08-12
**Status:** Accepted

## Context

We need consistent testing practices across the team. Current coverage is spotty and CI is unreliable.

## Decision

### Test Pyramid

1. **Unit tests** (70%): Fast, isolated, mock external dependencies
2. **Integration tests** (20%): Test real database, real Redis, real HTTP
3. **E2E tests** (10%): Playwright for critical user flows only

### CI Pipeline

```
lint -> unit tests -> integration tests -> build -> deploy staging -> e2e tests
```

- All PRs must pass lint + unit tests before merge
- Integration tests run in parallel using testcontainers
- E2E tests run nightly + before production deploys
- Coverage threshold: 80% for new packages, 60% for existing

### Test Fixtures

- Use `testdata/` directories for fixture files
- Database fixtures use factory functions, not SQL dumps
- API tests use httptest.Server, not real network calls

## Conventions

- Test files: `*_test.go` in the same package
- Test helpers: `testutil/` package for shared test utilities
- Table-driven tests for anything with >3 cases
- No `time.Sleep` in tests — use channels or polling with timeout

## Consequences

- CI runs take ~8 minutes (acceptable)
- testcontainers requires Docker in CI (GitHub Actions supports this)
- Coverage reports uploaded to Codecov on every PR
