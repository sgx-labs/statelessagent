---
title: "Architecture Review — Nov 17"
domain: engineering
tags: [meeting, architecture, review]
confidence: 0.8
content_type: meeting
---

# Architecture Review — November 17, 2025

## Agenda

1. Review search ranking algorithm changes
2. Discuss graph model schema
3. Evaluate caching strategy

## Discussion Notes

### Search Ranking

- Current hybrid search merges vector and keyword results well for precise queries
- Problem: vague queries like "what's important" return noisy results
- Proposal: Add a "freshness" signal that boosts recently modified notes
- Decision: Implement freshness boost as `0.1 * recency_factor` where recency decays over 30 days
- Action item: Update the search ranking doc to reflect this change

### Graph Model

- The `supersedes` edge type was discussed — we need it for decision versioning
- When a new decision supersedes an old one, the old one should be marked as stale
- Graph traversal should respect supersession: skip superseded nodes by default
- Action item: Add `is_superseded` flag to graph_nodes table

### Caching

- LRU cache at 1000 entries seems fine for vaults up to 5k notes
- For larger vaults, consider a two-level cache (hot/warm)
- Redis cache not needed for single-user mode (only for hosted service)
- Decision: Keep current LRU, revisit when we see vault sizes > 5k notes

## Action Items

- [ ] Add freshness boost to search ranking
- [ ] Update search ranking architecture doc
- [ ] Add `is_superseded` to graph schema
- [ ] Benchmark cache hit ratio at 5k notes
