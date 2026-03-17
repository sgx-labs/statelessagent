---
title: "Search Ranking Algorithm"
domain: backend
tags: [search, ranking, algorithm, architecture]
confidence: 0.85
content_type: architecture
---

# Search Ranking Algorithm

## Overview

Search uses a hybrid approach combining vector similarity with keyword matching and title relevance signals.

## Scoring Components

### 1. Vector Similarity (Primary)

- Cosine distance from sqlite-vec KNN search
- Absolute scoring: `score = 1.0 - (distance / 20.0)`
- Blended with relative scoring: `0.7 * absolute + 0.3 * relative`
- Distance ceiling of 20.0 ensures irrelevant results score near zero

### 2. Keyword Title Matching (Secondary)

- Extracts search terms after stop-word removal
- Matches terms against note titles
- Exact title match scores 0.95
- Partial matches scored proportionally: `0.5 + 0.35 * (matched/total)`

### 3. Reconsolidation Boost

- Frequently accessed notes get a small score boost
- Formula: `log1p(access_count) * 0.02`
- Capped at 1.0 to prevent access count from dominating

## Hybrid Merge Strategy

1. Run vector search (primary results)
2. Run keyword title search (supplemental)
3. Boost vector results that also appear in keyword results (if keyword score >= 0.7)
4. Reserve 30% of slots for keyword-only results
5. Sort merged results by final score
6. Post-process: title-overlap-aware reranking

## Deduplication

- Results are deduplicated by file path
- Best-scoring chunk per note wins
- Versioned files (v1, v2) are near-deduped

## Configuration

- Default top-k: 5
- Maximum top-k: 100
- Fetch multiplier: 5x (fetch 25 to return top 5)
