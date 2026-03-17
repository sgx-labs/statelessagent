---
title: "Deployment Pipeline (Outdated)"
domain: devops
tags: [deploy, pipeline, outdated, stale]
confidence: 0.3
content_type: note
trust_state: stale
review_by: "2025-10-01"
---

# Deployment Pipeline

> **WARNING: This document is outdated.** The deployment pipeline has been migrated from CircleCI to GitHub Actions. See `sessions/handoff-005.md` for the current setup.

## Old Pipeline (CircleCI)

The deployment pipeline used CircleCI with the following stages:

1. Build: `go build` in a Docker container
2. Test: Run unit tests only (no integration tests)
3. Deploy: SSH into the server and restart the process

### Configuration

```yaml
# .circleci/config.yml
version: 2.1
jobs:
  build:
    docker:
      - image: golang:1.20
    steps:
      - checkout
      - run: go build ./cmd/server
      - run: go test ./...
  deploy:
    machine: true
    steps:
      - run: ssh deploy@server "cd /app && git pull && systemctl restart app"
```

## Known Issues with Old Pipeline

- No staging environment — deployed directly to production
- Tests ran in Docker but deployment was bare metal (inconsistent)
- No rollback capability — had to manually git revert and redeploy
- Secret management via environment variables on the server (insecure)
- No health checks after deploy

## Migration Notes

Migrated to GitHub Actions on November 28, 2025. The old CircleCI configuration has been removed from the repository. See the CI/CD handoff note for the new setup.
