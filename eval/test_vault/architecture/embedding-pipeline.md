---
title: "Embedding Pipeline Architecture"
domain: backend
tags: [embeddings, pipeline, ollama, architecture]
confidence: 0.85
content_type: architecture
---

# Embedding Pipeline Architecture

## Overview

The embedding pipeline converts markdown notes into vector representations for semantic search. It supports multiple embedding providers with a unified interface.

## Pipeline Stages

```
File change detected → Parse markdown → Chunk text → Generate embeddings → Store in sqlite-vec
```

### 1. File Change Detection

- `fsnotify` watcher monitors the vault directory
- Debounces changes (500ms) to batch rapid saves
- On startup, compares file hashes to detect changes since last run

### 2. Markdown Parsing

- Extracts frontmatter (YAML) for metadata (title, tags, domain, confidence)
- Strips markdown syntax for clean text embedding
- Preserves heading structure for chunk boundaries

### 3. Text Chunking

- Chunks at heading boundaries (##, ###)
- Max chunk size: 2000 tokens
- Overlap: 200 tokens between adjacent chunks
- Each chunk stored with its heading path for context

### 4. Embedding Generation

Supported providers:
- **Ollama** (default): `nomic-embed-text` model, 768 dimensions
- **OpenAI**: `text-embedding-3-small`, 1536 dimensions
- **OpenAI-compatible**: Any API matching the OpenAI embedding endpoint

### 5. Vector Storage

- sqlite-vec virtual table for KNN search
- Vectors stored alongside note metadata in the same SQLite database
- Cosine distance metric for similarity

## Configuration

```yaml
embedding:
  provider: ollama
  model: nomic-embed-text
  dimensions: 768
  batch_size: 32
```

## Error Handling

- Failed embeddings are retried 3 times with exponential backoff
- Notes that fail embedding are marked as `pending` and retried on next reindex
- Provider unavailability triggers graceful degradation to keyword search
