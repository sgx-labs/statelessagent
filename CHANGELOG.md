# Changelog

## 1.0.0 — 2026-02-01

### Rebranded as The Stateless Agent

Forked from [Statelessagent](https://github.com/heyitsnoah/statelessagent) by Noah Brier and rebranded under `sgx-labs/statelessagent`.

#### Added
- Interactive TUI installer (`node install.mjs`) with prerequisite checks and feature selection
- 3D ASCII banner with red gradient
- This changelog

#### Changed
- All internal references updated from `statelessagent` to `stateless-agent`
- GitHub URLs point to `sgx-labs/statelessagent`
- `/install-statelessagent-command` renamed to `/install-command`
- Fallback vault paths use `stateless-agent-vault`
- ESLint config names use `stateless-agent/*` namespace

#### Preserved
- Full vault structure, PARA method, all slash commands
- Upgrade system, agent roles, MCP integrations
- All acknowledgments and credits to original authors
