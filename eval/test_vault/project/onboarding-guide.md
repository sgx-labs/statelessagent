---
title: "Developer Onboarding Guide"
domain: engineering
tags: [onboarding, setup, getting-started]
confidence: 0.8
content_type: guide
---

# Developer Onboarding Guide

## Prerequisites

- Go 1.21+ installed
- Docker and Docker Compose
- Node.js 20+ (for frontend)
- PostgreSQL 15+ (or use Docker)
- Redis 7+ (or use Docker)
- Ollama (for local embeddings)

## Getting Started

### 1. Clone and Build

```bash
git clone <repo-url>
cd project
go build ./cmd/server
```

### 2. Set Up Infrastructure

```bash
docker compose up -d  # starts PostgreSQL + Redis
ollama pull nomic-embed-text  # download embedding model
```

### 3. Run Migrations

```bash
./server migrate up
```

### 4. Configure Environment

Copy `.env.example` to `.env` and set:
- `DATABASE_URL=postgres://localhost:5432/devdb`
- `REDIS_URL=redis://localhost:6379`
- `JWT_SECRET=<generate-a-secret>`
- `OLLAMA_URL=http://localhost:11434`

### 5. Run Tests

```bash
go test ./...           # unit + integration tests
npm test                # frontend tests
```

### 6. Start Development Server

```bash
./server serve          # API on :8080
cd frontend && npm dev  # UI on :3000
```

## Project Structure

```
cmd/server/     — CLI entrypoint
internal/       — Core packages
  auth/         — JWT, OAuth, middleware
  store/        — SQLite storage layer
  embedding/    — Vector embedding providers
  graph/        — Knowledge graph operations
  mcp/          — MCP server for AI tools
frontend/       — React dashboard
migrations/     — SQL migration files
config/         — Environment configs
```

## Key Contacts

- Backend questions: #backend channel
- Frontend questions: #frontend channel
- DevOps/infra: #devops channel
- Security concerns: #security channel
