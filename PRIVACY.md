# Privacy Policy

**Last updated:** 2026-02-05

SAME (Stateless Agent Memory Engine) is designed to be local-first and privacy-respecting.

## What Data SAME Processes

| Data Type | Where It Goes | Retention |
|-----------|---------------|-----------|
| Your markdown notes | Read from disk, never transmitted | N/A (your files) |
| Embeddings (vectors) | Stored locally in SQLite | Until you delete or reindex |
| Search queries | Processed locally | Not stored |
| Config settings | Stored in `.same/config.toml` | Until you delete |

## Network Requests

SAME makes network requests only in these cases:

### Ollama (Default)
- **Destination:** `localhost:11434` (your local Ollama instance)
- **Data sent:** Note content chunks for embedding
- **Data stays:** On your machine

### OpenAI (Optional)
- **When:** Only if you set `SAME_EMBED_PROVIDER=openai`
- **Destination:** `api.openai.com`
- **Data sent:** Note content chunks for embedding
- **OpenAI's privacy policy applies:** https://openai.com/privacy

### No Other Network Calls
- No telemetry
- No analytics
- No crash reporting
- No update checks (manual only)
- No cloud sync

## What SAME Does NOT Do

- Does not send your notes to any external server (unless you opt into OpenAI embeddings)
- Does not track usage patterns
- Does not collect personal information
- Does not phone home
- Does not require an account
- Does not store data outside your vault directory

## Private Content

Directories named `_PRIVATE` are:
- Excluded from indexing
- Never embedded
- Never surfaced to AI agents
- Not scanned or read by SAME

Configure additional skip patterns in `.same/config.toml`:
```toml
[vault]
skip_dirs = ["_PRIVATE", "secrets", ".hidden"]
```

## AI Tool Integration

When SAME surfaces context to your AI tool (Claude Code, Cursor, etc.):
- That content becomes part of your conversation
- It is sent to the AI provider's API (Anthropic, OpenAI, etc.)
- Their privacy policies apply to that data
- This is identical to manually pasting the same content

SAME doesn't add any data beyond what you would paste yourself.

## Data Location

All SAME data is stored in your vault:
```
your-vault/
  .same/
    config.toml    # Your configuration
    data/
      vault.db     # SQLite database with embeddings
```

Delete `.same/` to remove all SAME data.

## Your Rights

- **Access:** Your data is on your machine. Read it anytime.
- **Delete:** Delete `.same/` folder. Done.
- **Portability:** SQLite is a standard format. Export/backup as needed.
- **Control:** You control what gets indexed via skip patterns.

## Contact

Questions about privacy: dev@thirty3labs.com
