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
