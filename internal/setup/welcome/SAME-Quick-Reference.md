---
title: "SAME — Quick Reference"
tags: [same, reference, commands]
content_type: hub
---

# SAME Quick Reference

## Daily Commands

```bash
same status          # What's indexed, provider/runtime health
same doctor          # Health check with fix suggestions
same log             # Recent SAME activity
```

## Search & Explore

```bash
same search "auth decisions"           # Search from CLI
same search "auth" --top-k 15          # Dig deeper (more results)
same related path/to/note.md           # Find similar notes
```

**Tip:** If your AI didn't find something you know exists, just say:
- "Search my notes more broadly for X"
- "Look harder for anything about Y"
- "Try searching for [alternate term]"

## Configuration

```bash
same display full      # Show detailed surfacing box (default)
same display compact   # One-line summary
same display quiet     # Silent mode

same profile use precise    # Fewer, highly relevant results
same profile use balanced   # Default balance
same profile use broad      # More results (~2x tokens)

same config show       # View current settings
same config edit       # Open config in $EDITOR
```

## Maintenance

```bash
same reindex           # Re-index changed files
same reindex --force   # Re-index everything
same watch             # Auto-reindex on file changes
```

## Multi-Vault

```bash
same vault list                    # Show registered vaults
same vault add work ~/work/notes   # Register a vault
same vault default work            # Set default
same --vault work status           # Use specific vault
```

## Troubleshooting

### "Semantic search unavailable"

1. Run `same doctor` for provider diagnostics
2. Configure embeddings (`SAME_EMBED_PROVIDER=ollama|openai|openai-compatible`)
3. Run `same reindex --force`

### "No results found"

1. Check your notes are `.md` files
2. Run `same reindex --force`
3. Try `same search "test"` to verify search works

### "Wrong notes surfaced"

1. Try `same profile use precise` for stricter matching
2. Add better frontmatter (tags, content_type)
3. Check `same log` to see what was surfaced

### "Too verbose"

Run `same display compact` or `same display quiet`.

## Files & Locations

| Path | Purpose |
|------|---------|
| `.same/config.toml` | Your configuration |
| `.same/data/vault.db` | SQLite database |
| `~/.config/same/vaults.json` | Vault registry |
| `sessions/` | Handoff notes (configurable) |
| `decisions.md` | Decision log (configurable) |

## Getting Help

- Run `same doctor` for diagnostics
- Check `same --help` for all commands
- Discord: https://discord.gg/9KfTkcGs7g
