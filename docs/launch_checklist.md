# SAME v0.8.3 Launch Checklist

## Pre-Release (Before Tagging)

- [ ] `make build` passes clean
- [ ] `go vet ./...` passes clean
- [ ] `go test ./internal/hooks/... -count=1` passes
- [ ] `go test ./internal/mcp/... -count=1` passes
- [ ] `go test ./internal/web/... -count=1` passes
- [ ] `go test ./internal/guard/... -count=1` passes
- [ ] `go test ./internal/store/... -count=1` passes
- [ ] `same demo` runs end-to-end without errors
- [ ] `same doctor` shows all green on a fresh init
- [ ] `same --version` shows v0.8.3
- [ ] CHANGELOG.md updated with all v0.8.3 changes
- [ ] README.md CLI reference matches actual commands
- [ ] AGENTS.md project structure matches actual files
- [ ] npm/README.md updated

## Tag & Release

- [ ] `git add -A && git status` — review all changes
- [ ] `git commit -m "Release: v0.8.3 — security hardening + keyword-only mode + ARM Linux"`
- [ ] `git tag v0.8.3`
- [ ] `git push origin main --tags`
- [ ] CI builds all binaries (darwin-arm64, linux-amd64, linux-arm64, windows-amd64)
- [ ] GitHub Release created with CHANGELOG excerpt
- [ ] npm publish (`cd npm && npm publish`)

## Post-Release Verification

- [ ] `curl -fsSL statelessagent.com/install.sh | bash` installs v0.8.3
- [ ] `npx -y @sgx-labs/same version` shows v0.8.3
- [ ] `same demo` works on fresh install
- [ ] MCP config snippet works in Claude Code
- [ ] Seed vaults install correctly (`same seed install claude-code-power-user`)

## Launch Day

- [ ] Product Hunt listing live
- [ ] First comment posted
- [ ] Twitter/X announcement thread
- [ ] Discord announcement
- [ ] Reddit posts (r/ClaudeAI, r/cursor, r/LocalLLaMA)
- [ ] Hacker News "Show HN" (stagger by 1 day from PH)

## Post-Launch (48h)

- [ ] Monitor GitHub issues for install problems
- [ ] Respond to all Product Hunt comments
- [ ] Respond to all Discord questions
- [ ] Track download/install metrics
- [ ] Collect user feedback for v0.8.4 planning
