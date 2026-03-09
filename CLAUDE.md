# SAME (Stateless Agent Memory Engine)

A cross-agent memory layer. Single Go binary with built-in MCP server, SQLite storage, and local embeddings via Ollama.

**License:** BSL-1.1

## Build and Test

```bash
go build ./cmd/same        # build the binary
go test ./...              # run all tests
golangci-lint run          # lint
```

Requires Go 1.25+. No CGO. Produces a single static binary.

## Project Structure

| Directory | Purpose |
|-----------|---------|
| `cmd/same/` | CLI entrypoint |
| `internal/` | Core packages (storage, embeddings, MCP server, graph, guard) |
| `npm/` | npm wrapper for `npx same` distribution |

## Security Testing

```bash
make security-test
```

Covers prompt injection, MCP input validation, path traversal, PII detection, and plugin sandboxing. New MCP handlers must sanitize output through `neutralizeTags()`.

## Code Style

- Standard Go conventions (`gofmt`, `go vet`)
- Conventional commits (e.g., `feat:`, `fix:`, `docs:`)
- Keep commit messages concise (one line summary, optional body)

## Contributing

1. Fork and create a feature branch
2. Ensure `go test ./...` passes
3. Ensure `golangci-lint run` is clean
4. Open a pull request against `main`

## Contact

dev@thirty3labs.com
