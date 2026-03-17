---
title: "Roadmap Planning — Dec 1"
domain: product
tags: [meeting, roadmap, planning, priorities]
confidence: 0.7
content_type: meeting
---

# Roadmap Planning — December 1, 2025

## Q4 Review

### Completed
- JWT authentication (access + refresh tokens)
- Database migration v8 (audit log)
- Search performance optimization (p99 < 100ms)
- CI/CD pipeline with staging deploy
- Frontend dashboard MVP

### Not Completed
- Production deploy automation (70% done)
- Graph visualization performance (deferred)
- Mobile responsive layout (deferred)

## Q1 2026 Priorities

### Must Have (P0)
1. **Production deploy with rollback** — Complete the deployment pipeline
2. **Key rotation** — Switch JWT from HS256 to RS256 with automated rotation
3. **Search eval suite** — Build automated evaluation for search quality
4. **Vault sharing** — Allow read-only vault sharing between users

### Should Have (P1)
5. **Graph visualization** — Fix performance for large vaults
6. **Mobile layout** — Responsive design for tablet/phone
7. **Webhook integrations** — Notify external systems on vault changes

### Nice to Have (P2)
8. **Browser extension** — Clip web pages into vault
9. **Collaborative editing** — Real-time multi-user note editing
10. **AI summarization** — Auto-generate note summaries

## Resource Allocation

- Backend: 2 engineers — priorities 1-4
- Frontend: 1 engineer — priorities 5-6
- DevOps: 1 engineer (part-time) — priority 1
- Security: Cross-cutting with priority 2

## Success Metrics

- Search recall@5 > 80% on eval suite
- Deploy to production in < 10 minutes
- Zero security incidents from auth system
- 95% uptime for hosted service
