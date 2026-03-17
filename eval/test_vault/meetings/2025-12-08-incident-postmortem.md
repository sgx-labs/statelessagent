---
title: "Incident Postmortem — Staging Outage Dec 5"
domain: devops
tags: [meeting, incident, postmortem, outage]
confidence: 0.9
content_type: meeting
---

# Incident Postmortem — Staging Outage December 5, 2025

## Summary

Staging environment was unavailable for 2 hours due to a SQLite WAL file growing unbounded after a reindex operation on a large vault (8,000 notes).

## Timeline

- **14:00** — Automated reindex triggered by CI deploy
- **14:05** — WAL file grows to 500MB (normal is <10MB)
- **14:15** — Disk usage hits 95%, Fly.io health check starts failing
- **14:20** — Alerts fire, on-call notified
- **14:45** — Root cause identified: WAL checkpoint not running during bulk insert
- **15:10** — Manual WAL checkpoint resolves the issue
- **16:00** — Monitoring confirms service stable

## Root Cause

The reindex operation performs a bulk insert of all notes in a single transaction. SQLite defers WAL checkpointing during long transactions, causing the WAL file to grow proportionally to the number of notes.

For 8,000 notes with embeddings, the WAL file reached ~500MB, filling the 1GB disk.

## Fix

1. **Immediate**: Added automatic WAL checkpoint every 1000 notes during reindex
2. **Preventive**: Added disk usage monitoring alert at 70% threshold
3. **Preventive**: Increased staging disk to 2GB
4. **Long-term**: Consider batching reindex into smaller transactions (500 notes per tx)

## Lessons Learned

- Large vault reindex was never tested — we only had vaults with <1000 notes in CI
- WAL behavior under long transactions is well-documented but we didn't account for it
- Disk usage monitoring should have been set up from day one

## Action Items

- [x] Add WAL checkpoint during reindex
- [x] Increase staging disk to 2GB
- [ ] Add vault size to CI test matrix (100, 1000, 5000, 10000 notes)
- [ ] Set up disk usage alerting for production
