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

## 11. Post-Release

- [ ] Verify `npx -y @sgx-labs/same version` shows new version
- [ ] Verify `curl -fsSL https://statelessagent.com/install.sh | bash` works
- [ ] Announce on Discord
- [ ] Check GitHub release page renders correctly
