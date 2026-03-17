---
title: "Multi-Tenancy Architecture"
domain: backend
tags: [multi-tenancy, workspaces, isolation, architecture]
confidence: 0.8
content_type: architecture
---

# Multi-Tenancy Architecture

## Design

SAME uses a **database-per-tenant** isolation model:

- Each vault is a separate SQLite database file
- No cross-vault data leakage is possible at the storage layer
- Vault registry maps aliases to filesystem paths

## Vault Isolation

### Storage Isolation
- Each vault has its own `.same/data/vault.db` file
- SQLite WAL mode ensures concurrent reads don't block writes
- File permissions restrict access to the vault owner

### Search Isolation
- Default search queries only the current vault
- Federated search (`--all`) explicitly opens multiple vault databases
- Each vault's results are tagged with the vault alias

### Configuration Isolation
- Embedding provider config is global (shared across vaults)
- Search profiles and display settings are per-vault
- Vault-specific overrides in `.same/config.toml`

## Vault Registry

```toml
# ~/.config/same/registry.toml
[vaults]
project-a = "/home/user/projects/project-a"
project-b = "/home/user/projects/project-b"
personal = "/home/user/notes"
```

## Cross-Vault Operations

- `same search --all` searches every registered vault
- `same search --vaults a,b` searches specific vaults
- Results include vault alias for disambiguation
- Federated search caps at 50 vaults to prevent resource exhaustion

## Data Migration

- Vaults are portable — copy the directory, it works
- Export: `same export` outputs all notes as markdown
- Import: `same import` indexes markdown files from a directory
- No server-side state to migrate (everything is in the vault directory)
