---
title: "Environment Configuration"
domain: devops
tags: [environment, config, secrets, setup]
confidence: 0.8
content_type: reference
---

# Environment Configuration

## Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `DATABASE_URL` | Yes | — | PostgreSQL connection string |
| `REDIS_URL` | Yes | — | Redis connection string |
| `JWT_SECRET` | Yes | — | Secret key for JWT signing |
| `OLLAMA_URL` | No | `http://localhost:11434` | Ollama API endpoint |
| `OPENAI_API_KEY` | No | — | OpenAI API key (if using OpenAI embeddings) |
| `PORT` | No | `8080` | HTTP server port |
| `LOG_LEVEL` | No | `info` | Logging level (debug/info/warn/error) |
| `EMBEDDING_PROVIDER` | No | `ollama` | Embedding provider (ollama/openai/openai-compatible) |
| `EMBEDDING_MODEL` | No | `nomic-embed-text` | Embedding model name |

## Configuration Files

### `config/base.yaml`
Shared configuration across all environments.

### `config/staging.yaml`
Staging-specific overrides (smaller rate limits, debug logging).

### `config/production.yaml`
Production-specific settings (strict rate limits, error-only logging).

## Secrets Management

### Local Development
- Use `.env` file (git-ignored)
- Copy from `.env.example`

### Staging/Production
- Secrets stored in Fly.io secrets manager
- Set via `fly secrets set KEY=value`
- Rotated quarterly (JWT_SECRET, API keys)
- Never logged or exposed in error messages

## Infrastructure

### PostgreSQL
- Version: 15
- Staging: Fly Postgres (1 instance, 256MB)
- Production: Fly Postgres (2 instances, 1GB, HA)

### Redis
- Version: 7
- Staging: Fly Redis (1 instance, 100MB)
- Production: Upstash Redis (serverless, 256MB)

### Fly.io
- Region: `iad` (US East)
- Machine size: `shared-cpu-1x` (staging), `performance-1x` (production)
- Auto-scaling: 1-3 machines based on CPU
