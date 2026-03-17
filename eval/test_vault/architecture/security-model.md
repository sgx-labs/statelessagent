---
title: "Security Model"
domain: security
tags: [security, threat-model, permissions, architecture]
confidence: 0.9
content_type: architecture
---

# Security Model

## Threat Model

### Assets
- User data (notes, preferences, API keys)
- Authentication credentials (JWT secrets, OAuth tokens)
- System infrastructure (databases, servers)

### Threats
1. **Unauthorized access** — Stolen JWT or API key
2. **Data exfiltration** — SQL injection, path traversal
3. **Prompt injection** — Malicious content in notes affecting AI responses
4. **Denial of service** — Rate limit bypass, resource exhaustion

## Authentication Security

- JWT tokens signed with HS256 (rotating to RS256 in Q1)
- Refresh tokens: httpOnly, Secure, SameSite=Strict cookies
- Failed login attempts: exponential backoff after 5 failures
- Account lockout after 20 failed attempts in 1 hour

## Authorization

Role-based access control (RBAC) with workspace scoping:

| Role | Read | Write | Admin | Billing |
|------|------|-------|-------|---------|
| viewer | yes | no | no | no |
| editor | yes | yes | no | no |
| admin | yes | yes | yes | no |
| owner | yes | yes | yes | yes |

## Data Protection

- All data at rest encrypted via PostgreSQL TDE
- All data in transit over TLS 1.3
- PII fields (email, name) encrypted at application level
- Vault data stored locally, never transmitted unless user explicitly shares

## Input Validation

- All API inputs validated with JSON schema
- File paths sanitized to prevent directory traversal
- MCP tool inputs sanitized through `neutralizeTags()` to prevent injection
- SQL parameters always use prepared statements (never string concatenation)

## Incident Response

1. Automated alerts on authentication anomalies (>10 failed logins/minute)
2. Audit log preserves all mutations for forensic analysis
3. JWT secret rotation procedure documented in runbook
4. 24-hour disclosure timeline for security vulnerabilities
