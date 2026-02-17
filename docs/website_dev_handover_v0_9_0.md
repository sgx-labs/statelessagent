# Website Dev Handover — v0.9.0 Runtime/Hardening Pass

## What shipped

1. Graph LLM extraction is now explicit policy, default-off.
- New setting: `SAME_GRAPH_LLM=off|local-only|on`
- Default is `off` (regex extraction only)
- `local-only` enables LLM graph extraction only when chat resolves to a localhost endpoint

2. Self-update path is now checksum-verified.
- `same update` now requires `sha256sums.txt` in the release assets
- Downloaded binary SHA256 is verified before install
- Unverified updates are refused

3. Status and diagnostics are provider-neutral.
- `same status` now reports embedding/chat/graph runtime state (not Ollama-only)
- `same doctor` now checks provider config and graph LLM policy across `ollama`, `openai`, `openai-compatible`, and `none`

4. Operational boundary hardening landed across file-path flows.
- `same watch` now cleans up stale entries on rename/missing-file races
- vault feed source/target containment checks use boundary-safe path validation
- seed manifest cache now enforces the same validation as fresh manifest downloads
- `same seed install --force` now refuses dangerous destination paths (root/home/seed-root parent)
- seed extraction now normalizes manifest `./path` entries and fails loudly on directory-create errors
- `SafeVaultSubpath` rejects absolute inputs and enforces vault-root containment
- guard allowlist file entries now use exact path matching to avoid nested basename bypasses
- key write paths (config/registry/handoff/index stats/tutorial scaffolding/seed config rewrites/verbose logs) now surface write failures instead of silently skipping writes

## Suggested website copy updates

### Security / trust section
- "SAME now verifies update binaries with release-published SHA256 checksums before install."
- "Graph LLM extraction is opt-in and policy-gated (`off`, `local-only`, `on`) with `off` as default."

### Feature section
- "Provider-flexible runtime: use Ollama, OpenAI-compatible local servers (llama.cpp/LM Studio/vLLM), OpenAI, or keyword-only mode."
- "Diagnostics understand your actual provider stack (`same status`, `same doctor`)."

### Knowledge graph section
- "Knowledge Graph works out of the box with regex extraction; optional LLM enrichment is explicit and controlled."

## Suggested launch bullets (short)

- "Safer updates: checksum-verified binary installs"
- "Graph LLM is now explicit opt-in with local-only mode"
- "Provider-neutral health checks and runtime visibility"

## Dev notes for docs/site links

Point advanced users to:
- Runtime env vars in README (`SAME_EMBED_*`, `SAME_CHAT_*`, `SAME_GRAPH_LLM`)
- `same status --json` for integration dashboards
- `same doctor` for support triage
- `make precheck-full` when maintainers want full tracked-file hygiene scans before release

## Positioning guidance

Lead with trust + control, then flexibility:
1. Control: local-first, explicit policies
2. Trust: verified updates
3. Flexibility: bring-your-own provider stack
