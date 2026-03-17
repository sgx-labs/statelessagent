---
title: "Embedding Model Selection (Outdated)"
domain: backend
tags: [embeddings, model, outdated, stale]
confidence: 0.3
content_type: note
trust_state: stale
review_by: "2025-10-15"
---

# Embedding Model Selection

> **OUTDATED**: We initially used `all-MiniLM-L6-v2` (384 dimensions). We have since switched to `nomic-embed-text` (768 dimensions) for better retrieval quality. See `architecture/embedding-pipeline.md` for current setup.

## Original Analysis (August 2025)

### Models Evaluated

| Model | Dimensions | Speed | Quality (MTEB) |
|-------|-----------|-------|----------------|
| all-MiniLM-L6-v2 | 384 | 15ms | 0.63 |
| nomic-embed-text | 768 | 25ms | 0.74 |
| text-embedding-3-small | 1536 | 50ms* | 0.78 |

*OpenAI API call, includes network latency

### Original Choice: all-MiniLM-L6-v2

We chose MiniLM for:
- Smallest dimensions (384) → smallest database size
- Fastest inference (15ms on CPU)
- Runs on minimal hardware

### Why We Switched

After real-world testing with developer vaults:
- MiniLM confused semantically similar but contextually different notes
- Retrieval precision dropped below 60% on multi-topic vaults
- nomic-embed-text showed 15+ percentage point improvement on our eval set
- The 10ms speed difference was negligible in practice

## Migration

All vaults were re-indexed after the model switch. The migration required:
1. Update config to `nomic-embed-text`
2. Run `same reindex --force` on all vaults
3. Update embedding dimension from 384 to 768 in the schema
