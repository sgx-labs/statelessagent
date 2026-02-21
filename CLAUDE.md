# SAME — Public Repository Rules

**This is a PUBLIC open-source repository.** Every file is visible to the world.

## Absolute Rules

1. **ZERO PII** — No personal names, emails, usernames, device names, client names, local paths, or API keys. Ever. In any file. In any commit message.
2. **No vault content** — This repo has no connection to any personal vault. Do not reference vault-specific paths, note structures, or personal workflows.
3. **Generic test data only** — All test fixtures use synthetic data (user@example.com, /home/user/notes, jdoe, etc.)
4. **Check before commit** — The pre-commit hook loads `.scripts/.blocklist` (gitignored) to scan for PII patterns. If the hook isn't set up, run: `cp .git/hooks/pre-commit.sample .git/hooks/pre-commit && chmod +x .git/hooks/pre-commit`

## Git Identity

All commits must use the pseudonymous identity:
```
git config user.name "sgx-labs"
git config user.email "dev@sgx-labs.dev"
```

## Development

```bash
make build          # build binary
make test           # run tests
make install        # install to $GOPATH/bin or /usr/local/bin
```

Requires Go 1.25+ and CGO_ENABLED=1.

## Security Testing

```bash
make security-test   # run all security-focused tests
```

Security tests cover: prompt injection sanitization, plugin validation, MCP input validation, web middleware, path traversal, claims normalization, search term sanitization, and PII pattern detection.

When adding new MCP handlers or hook output paths, ensure all text returned to agents passes through `neutralizeTags()` (MCP) or `sanitizeContextTags()` (hooks).

## Git Protocol

### STOP AND ASK before:
- **git commit** — "Ready to commit?"
- **git push** — "Ready to push?"
- **git tag / release** — "Ready to tag/release?"

**NEVER auto-commit, auto-push, or auto-release.**

## What NOT to do from this workspace

- Do NOT open or reference vault notes
- Do NOT use vault search MCP tools
- Do NOT copy patterns, paths, or names from other projects
- Do NOT develop vault-specific features without using synthetic test data

## Session Continuity

**Every session MUST maintain the project memory.** Context lost between sessions is the #1 productivity killer for this project.

### At session start:
1. Read `memory/MEMORY.md` (loaded automatically via auto-memory)
2. Check `git log --oneline -10` to understand recent work
3. Check `git status` for uncommitted work in progress

### At session end (before the user closes):
1. **Update `memory/MEMORY.md`** with:
   - Current version and commit hash
   - What was done this session (bullet points)
   - What's uncommitted in the working tree
   - What's next / unfinished
2. Keep MEMORY.md under 200 lines (it's loaded into system prompt)
3. Move detailed notes to topic files in the memory directory if needed

### What to capture:
- Architectural decisions and WHY they were made
- Bugs found and how they were fixed
- Features added with their scope
- Things that were tried and didn't work
- Current state of git (ahead of origin? uncommitted work?)

### What NOT to put in MEMORY.md:
- PII or local paths
- Duplicates of what's already in CHANGELOG.md
- Speculative plans that haven't been discussed with the user
