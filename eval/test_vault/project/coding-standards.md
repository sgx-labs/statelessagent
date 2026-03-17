---
title: "Coding Standards"
domain: engineering
tags: [standards, style-guide, conventions]
confidence: 0.85
content_type: guide
---

# Coding Standards

## Go Code Style

### Formatting
- `gofmt` is mandatory (enforced by CI)
- `golangci-lint` with our custom config (see `.golangci.yml`)
- Max line length: 120 characters (soft limit)

### Naming
- Package names: lowercase, no underscores (`store`, not `data_store`)
- Exported functions: PascalCase with clear verb prefix (`GetUser`, `InsertNote`)
- Unexported functions: camelCase (`parseMarkdown`, `validateInput`)
- Interfaces: describe behavior, not implementation (`Reader`, not `FileReader`)
- Test functions: `TestFunctionName_Scenario` (e.g., `TestSearch_EmptyQuery`)

### Error Handling
- Always check errors, never use `_` for error return values
- Wrap errors with context: `fmt.Errorf("parse config: %w", err)`
- Use sentinel errors for domain logic (`ErrNotFound`, `ErrForbidden`)
- Log at the boundary, wrap in the middle, handle at the top

### Testing
- Table-driven tests for 3+ scenarios
- Use `testutil.OpenMemory()` for database tests
- No `time.Sleep` — use channels, polling, or `testutil.WaitFor`
- Test file naming: `foo_test.go` in same package

## Frontend Code Style (TypeScript/React)

- Strict TypeScript (`strict: true` in tsconfig)
- Functional components with hooks (no class components)
- Props interfaces defined inline for simple components, extracted for complex ones
- CSS: Tailwind utility classes, no custom CSS files unless necessary

## Git Conventions

- Conventional commits: `feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`
- Branch names: `feature/short-description`, `fix/bug-description`
- Squash merge PRs to keep main history clean
- Rebase feature branches before merge (no merge commits)

## Documentation

- Public APIs must have godoc comments
- Architecture decisions recorded in `decisions/` directory
- Session handoffs recorded in `sessions/` directory
- Inline comments explain "why", not "what"
