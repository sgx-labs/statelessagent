---
title: "Knowledge Graph Data Model"
domain: backend
tags: [graph, knowledge-graph, data-model, architecture]
confidence: 0.8
content_type: architecture
---

# Knowledge Graph Data Model

## Overview

The knowledge graph tracks relationships between notes, enabling traversal-based discovery beyond pure semantic search.

## Schema

### Nodes

Each note is a node in the graph, identified by its file path:

```sql
CREATE TABLE graph_nodes (
    id INTEGER PRIMARY KEY,
    path TEXT UNIQUE NOT NULL,
    node_type TEXT DEFAULT 'note',
    created_at REAL,
    updated_at REAL
);
```

### Edges

Relationships between notes:

```sql
CREATE TABLE graph_edges (
    id INTEGER PRIMARY KEY,
    source_path TEXT NOT NULL,
    target_path TEXT NOT NULL,
    edge_type TEXT NOT NULL,
    weight REAL DEFAULT 1.0,
    metadata TEXT,  -- JSON
    created_at REAL,
    UNIQUE(source_path, target_path, edge_type)
);
```

## Edge Types

| Type | Meaning | Example |
|------|---------|---------|
| `depends_on` | A depends on B | Feature spec depends on auth decision |
| `references` | A mentions B | Meeting notes reference architecture doc |
| `produced` | Session A produced artifact B | Handoff produced code file |
| `supersedes` | A replaces B | New decision supersedes old one |
| `related` | Semantic similarity | Auto-detected by embedding proximity |

## Graph Queries

### Find all dependencies of a note
```sql
SELECT target_path FROM graph_edges
WHERE source_path = ? AND edge_type = 'depends_on'
```

### Find notes that reference a decision
```sql
SELECT source_path FROM graph_edges
WHERE target_path = ? AND edge_type = 'references'
```

### Transitive closure (2 hops)
Used for "related to related" discovery — find notes connected through intermediate nodes.

## Automatic Edge Detection

- `references` edges created when a note contains `[[wiki-link]]` or markdown link to another note
- `related` edges created when two notes have embedding distance < 10.0
- `supersedes` edges must be manually annotated in frontmatter
