---
title: "System Architecture Overview"
domain: engineering
tags: [architecture, overview, microservices]
confidence: 0.9
content_type: architecture
---

# System Architecture Overview

## High-Level Design

The system follows a modular monolith architecture with clear domain boundaries, designed to be split into microservices if needed.

### Core Services

1. **API Gateway** — Rate limiting, authentication, request routing
2. **User Service** — Account management, profile, preferences
3. **Workspace Service** — Multi-tenant workspace isolation and billing
4. **Search Service** — Vector search, keyword search, hybrid ranking
5. **Indexing Service** — File watching, markdown parsing, embedding generation
6. **Graph Service** — Knowledge graph operations, relationship traversal

### Data Stores

- **PostgreSQL** — Primary relational data (users, workspaces, metadata)
- **SQLite + sqlite-vec** — Per-vault note storage and vector search
- **Redis** — Rate limiting, session cache, pub/sub for real-time events

### Communication

- Synchronous: REST API between client and gateway, gRPC between internal services
- Asynchronous: Redis pub/sub for indexing events, webhooks for external integrations

## Deployment

Single binary deployment on Fly.io. The monolithic binary contains all services, configured via environment variables and YAML config files.

## Scaling Strategy

- Horizontal scaling via Fly.io machine auto-scaling
- SQLite databases are per-vault (no shared state between vaults)
- Redis is the only shared infrastructure component
- Stateless request handling (JWT auth, no server sessions)
