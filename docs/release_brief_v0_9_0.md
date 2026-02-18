# SAME v0.9.0 Release Brief

## Summary

SAME v0.9.0 makes the knowledge graph production-ready, expands provider flexibility, and hardens core filesystem/security boundaries for local-first workflows.

## What’s New

- Knowledge graph CLI: `same graph stats`, `same graph query`, `same graph path`, `same graph rebuild`
- Graph-backed web dashboard sections:
  - graph highlights (nodes/edges/relationship mix)
  - note-level knowledge connections
- Graph tutorial lesson: `same tutorial graph`
- Provider-flex chat routing for `same ask` and graph extraction (`auto`, `ollama`, `openai`, `openai-compatible`, `none`)
- Optional graph LLM policy mode: `SAME_GRAPH_LLM=off|local-only|on` (`off` default)
- Raspberry Pi profile preset: `same profile use pi`

## Reliability and Bug Fixes

- Watcher rename/delete consistency fixes (stale rows cleaned during file churn)
- Keyword-only and semantic reindex fallback behavior improved
- CLI `--vault` precedence fixed to avoid indexing/querying the wrong vault
- Graph delete/force-clear consistency: related graph rows now stay in sync with note lifecycle
- Graph query/path quality improvements for note/file node mismatches

## Security and Hardening

- Self-update checksum verification enforced via release `sha256sums.txt`
- Path-boundary hardening across vault feed, MCP writes, web API paths, and seed extraction
- Seed install/remove safety:
  - dangerous `--force --path` destinations rejected
  - seed root deletion rejected
  - manifest/cache validation parity enforced
  - extraction declared-size overflow checks added
- Guard allowlist hardened to exact path matches (no basename bypass)
- Write/cleanup failure visibility improved across config/registry/handoff/decision/update paths
- Config/init lock handling now surfaces stale-lock cleanup failures and warns on cleanup fallback paths

## Validation Status

- `make precheck` passing
- `make precheck-full` passing
- `make release-candidate` passing
- `go vet ./...` clean (excluding expected sqlite-vec deprecation warnings on macOS SDK headers)

## Upgrade Notes

- Existing 0.8.x vaults migrate forward with schema v6 (graph tables) automatically
- Graph extraction remains local to indexed notes; `_PRIVATE/` remains excluded
- For broader provider smoke tests, use `make provider-smoke-full` with provider env vars

## Maintainer Validation Loop

- `make precheck` for release readiness in current working set
- `make precheck-full` for full tracked-file hygiene scan
- `make release-candidate` before tagging
