---
title: "Security Review — Nov 24"
domain: security
tags: [meeting, security, review, vulnerabilities]
confidence: 0.85
content_type: meeting
---

# Security Review — November 24, 2025

## Agenda

1. Review prompt injection mitigations
2. Audit authentication implementation
3. Discuss PII detection in notes

## Findings

### Prompt Injection

- MCP tool outputs are sanitized via `neutralizeTags()` — confirmed working
- Edge case found: Unicode directional override characters could bypass tag detection
- Fix: Add Unicode normalization before tag sanitization
- Severity: Medium — requires attacker to have write access to vault

### Authentication Audit

- JWT implementation follows OWASP guidelines
- Token expiry is properly enforced (tested with expired tokens)
- Refresh token rotation is implemented but needs additional test for race conditions
- Finding: The `kid` (key ID) header is not set — will need this for key rotation
- Severity: Low — key rotation is planned for Q1

### PII Detection

- The `guard` command scans notes for common PII patterns
- Currently detects: email, phone, SSN, credit card numbers
- Missing: API keys, AWS credentials, private keys
- Action item: Add detection patterns for cloud credentials

## Risk Register Updates

| Risk | Severity | Status |
|------|----------|--------|
| Prompt injection via vault notes | Medium | Mitigated (Unicode fix pending) |
| JWT key rotation | Low | Planned for Q1 |
| PII leakage in shared vaults | Medium | Partial (guard command) |
| SQL injection | Low | Mitigated (parameterized queries) |

## Next Review

Scheduled for December 8, 2025 — focus on deployment security and secrets management.
