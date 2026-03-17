---
title: "Testing Setup (Outdated)"
domain: engineering
tags: [testing, setup, outdated, stale]
confidence: 0.3
content_type: note
trust_state: stale
review_by: "2025-09-01"
---

# Testing Setup (Outdated)

> **OUTDATED**: This document describes the old testing setup before we adopted testcontainers. See `decisions/testing-strategy.md` for the current approach.

## Old Setup

### Unit Tests
- Mocked database using a custom `MockDB` interface
- 43 test files, ~60% coverage
- Tests were fragile — mock behavior often diverged from real database

### Integration Tests
- Required a running PostgreSQL instance on localhost
- Required a running Redis instance on localhost
- Tests failed if services weren't running (common CI issue)
- No test isolation — tests shared the same database and interfered with each other

### E2E Tests
- None. Manual testing only.

## Problems with Old Setup

1. **CI reliability**: Tests depended on infrastructure being available
2. **Mock drift**: MockDB didn't implement all methods, causing false passes
3. **No isolation**: Tests mutated shared state, order-dependent failures
4. **Low coverage**: Hard to test database-specific behavior through mocks
5. **No E2E**: Critical user flows were only tested manually

## What Changed

- Adopted testcontainers for isolated PostgreSQL + Redis instances per test
- Switched from MockDB to real database testing with `OpenMemory()`
- Added Playwright for E2E testing of critical flows
- Coverage improved from 60% to 82%
- CI became deterministic — no more "works on my machine" failures
