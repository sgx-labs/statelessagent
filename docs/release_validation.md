# Release Validation Matrix

This checklist is for maintainers before cutting a release tag.

## Baseline

```bash
make precheck
```

`precheck` now includes:
- version consistency
- build + test
- repo-scope hygiene checks
- CLI smoke
- provider smoke baseline (`provider=none`)

## Provider Matrix

Run the broader provider flow (reindex/search/graph/web) when endpoints are available:

```bash
SAME_SMOKE_PROVIDERS=none,ollama,openai-compatible \
SAME_SMOKE_REQUIRED=none,ollama \
SAME_SMOKE_OPENAI_BASE_URL=http://127.0.0.1:8080/v1 \
SAME_SMOKE_OPENAI_EMBED_MODEL=nomic-embed-text \
make provider-smoke-full
```

Notes:
- `none` verifies keyword-only indexing behavior (`index_mode=lite`).
- non-`none` providers verify semantic indexing behavior (`index_mode=full`).
- in restricted runtimes that block localhost bind, web checks are skipped with an explicit reason.

## Upgrade Path

Run the on-disk migration regression test:

```bash
go test ./internal/store -run TestOpenPath_MigratesLegacyV5ToV6 -count=1
```

This test simulates a schema v5 database and verifies migration to v6 plus graph population and search/read continuity.
