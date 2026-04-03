# Product Specs — From Hermes Plugin Field Evaluation
**Source:** Field evaluation of native Hermes memory provider plugin
**Date:** April 2026
**Format:** Actionable specs, no internal context

---

## SPEC-01: Headless Vault Initialization

**Priority:** High
**Category:** Setup UX

**Problem:** `same init` requires an interactive TTY. Any automated, remote, or scripted setup (SSH deployments, CI/CD, Docker containers, cloud instances) hits a hard wall at step 1.

**Spec:**
```
same init --headless \
  --vault-path /path/to/vault \
  --embedding-provider ollama \
  --embedding-model nomic-embed-text \
  --graph-mode local-only
```

All config that the interactive wizard collects should be expressible as flags or env vars:

| Interactive prompt | Flag | Env var |
|-------------------|------|---------|
| Vault path | `--vault-path` | `SAME_VAULT_PATH` |
| Embedding provider | `--embedding-provider` | `SAME_EMBEDDING_PROVIDER` |
| Embedding model | `--embedding-model` | `SAME_EMBEDDING_MODEL` |
| Graph LLM mode | `--graph-mode` | `SAME_GRAPH_MODE` |
| Agent name | `--agent` | `SAME_AGENT` |

**Acceptance criteria:**
- `same init --headless --vault-path /tmp/test-vault` completes without any stdin interaction
- Exit code 0 on success, nonzero with clear error on missing required args
- Resulting vault is identical to one created interactively with the same settings
- Works over SSH (`sshpass ssh ... 'same init --headless ...'`)

---

## SPEC-02: .sameignore Templates Shipped with `same init`

**Priority:** High
**Category:** Setup UX / Search Quality

**Problem:** A fresh vault that includes framework repos, dependencies, or generated code produces terrible search results — the signal (actual project knowledge) is buried under thousands of irrelevant indexed files. New users don't know to add `.sameignore` before their first reindex. First impression of search quality is terrible.

**Confirmed:** Vault went from 492 indexed notes to 44 after adding `.sameignore` for a single framework directory. Search quality improvement was immediate and dramatic.

**Spec:**

1. `same init` should detect common project structures and suggest an appropriate template:
   - Detects `hermes-agent/` or `.hermes/` → suggest hermes-agent template
   - Detects `node_modules/` or `package.json` → suggest node template
   - Detects `venv/` or `requirements.txt` or `pyproject.toml` → suggest python template
   - Default → ship `default.sameignore` with sensible universal patterns

2. Prompt during init: "Found [framework]. Add .sameignore to exclude it from your index? [Y/n]"

3. Templates available at `templates/sameignore/` in the repo (already shipped).

4. `same reindex` should warn if no `.sameignore` exists and the vault has >1000 files:
   ```
   Warning: 1,847 files found, no .sameignore configured.
   Large vaults may have poor search quality. See: same ignore --help
   ```

**Acceptance criteria:**
- `same init` in a directory with `node_modules/` offers to add node template
- `same init --headless` accepts `--ignore-template hermes-agent` flag
- `same reindex` warns when note count exceeds threshold and no ignore file exists

---

## SPEC-03: CLI/MCP Binary Version Check

**Priority:** Medium
**Category:** Reliability / Developer Experience

**Problem:** When the installed `same` CLI binary is behind the MCP server (e.g. built from source), the database schema version mismatch causes CLI commands like `same reindex` and `same doctor` to fail with unhelpful errors while the MCP server continues working. Users see contradictory signals — the Hermes plugin reports healthy, but CLI maintenance commands fail.

**Root cause:** MCP server is loaded from the binary on PATH. If that binary was updated (e.g. via `go build` in development) but the database schema was written by a newer version, the old binary can't read it.

**Spec:**

1. Version check in plugin `initialize()` (already shipped in feat/hermes-memory-provider):
   - Compare `serverInfo.version` from MCP handshake against `same --version` output
   - Log warning if they differ: `SAME binary may be outdated (CLI: v0.12.1, MCP: v0.12.5). Run: same update`

2. `same doctor` should detect schema version mismatch explicitly:
   ```
   ✗ Schema version: database is v10, binary supports v9
     → Run: same update
   ```
   Currently shows a generic error that doesn't point to the fix.

3. `same reindex` on schema mismatch should offer auto-migration or clear upgrade path:
   ```
   Error: database schema v10 requires same v0.12.5+. Current: v0.12.1
   Fix: same update
   ```

**Acceptance criteria:**
- `same doctor` identifies schema mismatch with clear actionable message
- `same reindex` fails fast with upgrade instruction rather than cryptic error
- Plugin `initialize()` warns but does not fail on version mismatch

---

## SPEC-04: `same consolidate` Non-Interactive Mode

**Priority:** Medium
**Category:** Memory Quality / Automation

**Problem:** Knowledge extraction (the step that creates "knowledge notes" from raw notes and improves search quality) requires interactive confirmation. Automated deployments, cron jobs, and the Hermes plugin cannot run consolidation without user interaction. Fresh vaults have 0 knowledge notes until a human manually runs this.

**Confirmed:** Vault health score was 51/100 ("Fair") with 0 knowledge notes despite 100% embedding coverage. Consolidation is the missing step between "indexed" and "useful."

**Spec:**

```bash
same consolidate --yes              # skip all confirmation prompts
same consolidate --dry-run          # show what would be consolidated, no writes
same consolidate --threshold 0.85   # similarity threshold, default 0.80
same consolidate --max-notes 50     # limit scope for large vaults
```

Plugin hook: `mem_consolidate` MCP tool already exists with `dry_run` and `threshold` params. The plugin's `on_session_end()` should optionally call it:

```python
# In plugin config schema, add:
{
  "key": "auto_consolidate",
  "description": "Run consolidation at session end (slow, improves search quality)",
  "default": False,
}
```

**Acceptance criteria:**
- `same consolidate --yes` runs to completion without stdin
- Usable in cron: `0 2 * * * same consolidate --yes --max-notes 20`
- Plugin can trigger consolidation via `mem_consolidate` MCP tool with `dry_run=False`

---

## SPEC-05: Trust-State Gating for Prefetch

**Priority:** Medium
**Category:** Security / Memory Quality

**Problem:** All notes are injected into agent context via prefetch regardless of trust state. Notes with `unknown` or `stale` trust state are treated identically to `validated` notes. This:
1. Creates a vault poisoning attack surface — any note written to the vault can influence agent behavior
2. Surfaces potentially outdated information silently
3. Makes the trust system feel cosmetic rather than functional

**Confirmed:** A note with fake "system instructions" injected into the vault surfaced in prefetch results for unrelated queries.

**Spec:**

Configurable trust gating in the Hermes plugin:

```json
{
  "vault_path": "...",
  "prefetch_trust_gate": "unknown",
  "prefetch_tag_stale": true
}
```

`prefetch_trust_gate` options:
- `"all"` — current behavior, inject everything
- `"unknown"` — inject `validated`, `fresh`, `unknown`; skip `stale`, `contradicted`
- `"validated"` — only inject `validated` and `fresh` notes (strictest)

`prefetch_tag_stale`: when true, adds `⚠` to stale/unknown notes in the injected context so the agent knows to verify.

The injection filter (pattern matching for instruction-like text) should remain as a defense-in-depth layer regardless of trust gating setting.

**Acceptance criteria:**
- With `prefetch_trust_gate: "unknown"`, stale notes are excluded from prefetch
- With `prefetch_tag_stale: true`, unknown notes appear as `- **title** ⚠: snippet`
- A note with content matching injection patterns is always excluded regardless of trust state

---

## SPEC-06: `same health` Integration into Host Tools

**Priority:** Low
**Category:** Observability

**Problem:** Vault health score (0-100) is valuable signal but only visible via `same health` CLI. Users running Hermes as a background service, gateway, or cron agent have no visibility into vault degradation.

**Opportunity:** Surface the score in places users already look.

**Spec:**

1. Expose health score via MCP `mem_health` tool (already exists — returns score in text output). Plugin should parse and cache it.

2. `hermes status` should include SAME health score when plugin is active:
   ```
   ◆ Memory (SAME)
     Provider:    same
     Vault:       ~/projects/myproject
     Health:      72/100 (Good) — 3 stale notes
     Last active: 2 minutes ago
   ```

3. Plugin should log a warning at session start if health score drops below 50:
   ```
   WARNING: SAME vault health is 38/100. Run 'same consolidate' or 'same health' for details.
   ```

4. Optional: expose health score as a Hermes cron job template:
   ```
   # Weekly vault health check
   hermes cron create --schedule "0 9 * * 1" \
     --prompt "Check SAME vault health with same_health tool. If score below 70, summarize what needs attention."
   ```

**Acceptance criteria:**
- `hermes status` shows SAME health score when provider is active
- Session-start warning fires when health < 50
- Warning includes a specific actionable command

---

## SPEC-07: First-Run Experience Polish

**Priority:** Low
**Category:** Setup UX

**Three small fixes with outsized first-impression impact:**

**7a. Actionable error when binary not found**

Current: `is_available()` silently returns `False`. Hermes skips the plugin with no message.

Fix:
```
WARNING: SAME plugin disabled — 'same' not found on PATH.
Install from https://statelessagent.com or set SAME_BINARY=/path/to/same in ~/.hermes/.env
```

**7b. Help URL in config schema**

The `vault_path` config field in `get_config_schema()` has no `url` pointing to setup docs. The `hermes memory setup` wizard shows a URL for every other provider. Add:
```python
{
  "key": "vault_path",
  "url": "https://statelessagent.com/docs/getting-started",
  ...
}
```

**7c. When-to-use guidance in system_prompt_block**

Current system prompt lists tools but doesn't say when to use them. LLMs tend to either over-call or under-call without this signal.

Add to system_prompt_block:
```
Search at session start and when context feels incomplete.
Save decisions when something important is resolved.
Save notes when producing work that future sessions should know about.
```
