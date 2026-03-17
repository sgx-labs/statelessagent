---
title: "MCP Server Integration"
domain: integrations
tags: [mcp, ai-tools, integration, architecture]
confidence: 0.85
content_type: architecture
---

# MCP Server Integration

## Overview

SAME exposes a Model Context Protocol (MCP) server that allows AI coding assistants (Claude Code, Cursor, Windsurf) to read and search the vault programmatically.

## Available Tools

### search_notes
Search the vault by semantic similarity and keyword matching.

**Parameters:**
- `query` (string, required): Natural language search query
- `top_k` (int, optional, default 5): Number of results to return

**Returns:** Array of search results with path, title, snippet, score

### read_note
Read the full content of a specific note.

**Parameters:**
- `path` (string, required): Relative path to the note file

**Returns:** Full note content with metadata

### list_notes
List all notes in the vault with optional filtering.

**Parameters:**
- `domain` (string, optional): Filter by domain
- `tags` (array, optional): Filter by tags

**Returns:** Array of note summaries (path, title, domain, tags)

### add_note
Create or update a note in the vault.

**Parameters:**
- `path` (string, required): Where to store the note
- `content` (string, required): Markdown content with frontmatter

## Security

- All tool inputs sanitized via `neutralizeTags()` to prevent tag injection
- File paths validated to prevent directory traversal (`../`)
- MCP server binds to localhost only (no network exposure)
- Read operations are unrestricted; write operations require explicit user consent

## Configuration

The MCP server is configured in the AI tool's settings:

```json
{
  "mcpServers": {
    "same": {
      "command": "same",
      "args": ["mcp"]
    }
  }
}
```

## Transport

- Uses stdio transport (stdin/stdout JSON-RPC)
- Compatible with MCP protocol version 2024-11-05
- Supports both tools and resources endpoints
