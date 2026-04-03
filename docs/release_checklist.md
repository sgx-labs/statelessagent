# Release Checklist

Run through this checklist **every release**, no exceptions.

## 1. Version Bump

- [ ] `Makefile` — `VERSION` variable
- [ ] `npm/package.json` — `"version"`
- [ ] `server.json` — `"version"`
- [ ] `CHANGELOG.md` — new section at top

## 2. Build & Test Gate

```bash
make release-candidate    # precheck + vet + migration test
go test ./... -count=1    # full test suite
```

## 3. MCP Tool Count

If tools were added/removed, update the count in **all** of these:

- [ ] `README.md` — badge, MCP tools table, description
- [ ] `npm/README.md` — heading + table
- [ ] `AGENTS.md` — `mcp/` line in project structure
- [ ] `Dockerfile` — OCI label
- [ ] `server.json` — description
- [ ] `glama.json` — description + tools array
- [ ] `npm/package.json` — description
- [ ] `internal/setup/mcp.go` — print message + tool list

Quick check: `grep -rn "17 tools\|17 MCP" --include='*.md' --include='*.go' --include='*.json' --include='Dockerfile'`

## 4. Doctor Check Count

If checks were added/removed in `doctor_cmd.go`:

- [ ] `AGENTS.md` — `doctor_cmd.go` comment
- [ ] `README.md` — do NOT hardcode a number (use "20+ checks" or "diagnostic checks")

Quick check: `grep -c 'check(' cmd/same/doctor_cmd.go`

## 5. Public-Facing Docs

- [ ] `SECURITY.md` — supported versions table
- [ ] `PRIVACY.md` — data types table (add new stored data types)
- [ ] `CHANGELOG.md` — no self-deprecating language ("aspirational", "inflated", etc.)
- [ ] `README.md` — no inflated timing claims, feature list current
- [ ] `docs/design_context.md` — "Last generated" date

## 6. Scrub

- [ ] No PII, banned terms, strategy language (see `.claude/CLAUDE.md`)
- [ ] No embarrassing TODOs, HACKs, or debug prints in new code
- [ ] Commit messages are clean (no banned terms — see `.claude/CLAUDE.md`)
- [ ] No hardcoded test paths or personal directories

Quick check: `git log --oneline HEAD~5..HEAD` — review every message.

## 7. Security

- [ ] New MCP handlers use `neutralizeTags()` on output
- [ ] New file reads validate paths with `safeVaultPath()`
- [ ] New XML wrapper tags added to `sanitizeContextTags()` tag list
- [ ] New frontmatter writes strip newlines from user input
- [ ] `make security-test` passes

## 8. Tag & Push

```bash
git tag vX.Y.Z
same push-allow && git push origin main
same push-allow && git push origin vX.Y.Z
```

## 9. GitHub Release

```bash
gh release create vX.Y.Z --title "vX.Y.Z — Title" --notes-file /tmp/release-notes.md
```

## 10. npm Publish

```bash
cd npm && npm publish
```

Verify: `npm view @sgx-labs/same version` should show the new version.

## 10b. Homebrew Tap

Automated by `homebrew-update` job in release workflow. Verify:

```bash
brew update && brew info sgx-labs/tap/same
```

Should show the new version. If the workflow failed, update manually:

```bash
cd /path/to/homebrew-tap
./update-formula.sh vX.Y.Z
git add Formula/same.rb && git commit -m "same X.Y.Z" && git push
```

## 11. Website Update (statelessagent.com)

- [ ] Version bumps across all pages (index, docs, briefs, sgx-labs, llms.txt, llms-full.txt, .well-known/mcp.json)
- [ ] `sitemap.xml` — update `lastmod` dates
- [ ] `changelog/index.html` — add release card, move "Latest" tag
- [ ] New features documented on docs page
- [ ] Push to main (Vercel auto-deploys)
- [ ] Verify live site shows new version

## 12. Distribution — Update ALL Directory Listings

Every release. No exceptions. Stale listings lose us to competitors.

### Official registries
- [ ] **Official MCP Registry** — `mcp-publisher publish` (source of truth for other directories)
- [ ] **npm** — `cd npm && npm publish` then verify `npm view @sgx-labs/same version`
- [ ] **Homebrew** — automated, verify `brew info sgx-labs/tap/same`

### Directory listings (update version, description, tool count)
- [ ] **Glama.ai** — https://glama.ai/mcp/servers/@sgx-labs/statelessagent
- [ ] **mcp.so** — check listing, update if manual
- [ ] **PulseMCP** — https://pulsemcp.com (check/update)
- [ ] **Smithery** — https://smithery.ai (check/update)
- [ ] **mcpservers.org** — check listing
- [ ] **LobeHub MCP** — check listing
- [ ] **awesome-mcp-servers** (punkpeye) — PR if description changed
- [ ] **cursor.directory** — check listing
- [ ] **OpenTools** — check listing
- [ ] **awesome-ai-memory** — check listing

### Verify after updating
- [ ] `glama.json` in repo matches the current version and tool count
- [ ] `server.json` in repo matches the current version
- [ ] Google "SAME MCP memory" — check what shows up

Quick check: `grep -rn "version" server.json glama.json npm/package.json | grep -v node_modules`

## 13. Post-Release Verification

- [ ] Verify `npx -y @sgx-labs/same version` shows new version
- [ ] Verify `curl -fsSL https://statelessagent.com/install.sh | bash` works
- [ ] Verify `same demo` works end-to-end on a fresh machine
- [ ] Announce on Discord
- [ ] Check GitHub release page renders correctly
- [ ] Run `same seed list --refresh` to verify seed manifest works
