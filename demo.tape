# SAME Terminal Demo — 30 seconds
# Regenerate: vhs demo.tape

Output assets/demo.gif
Set Width 1200
Set Height 600
Set FontSize 38
Set FontFamily "JetBrains Mono"
Set Padding 20
Set BorderRadius 8
Set Framerate 24
Set Theme "Dracula"
Set TypingSpeed 80ms

# Start clean
Hide
Type "cd /tmp && rm -rf same-demo && mkdir same-demo && cd same-demo"
Enter
Sleep 500ms
Type "mkdir -p decisions notes research"
Enter
Type "echo '---\ntitle: Use PostgreSQL\ntags: [database, backend]\n---\n\n# Decision: Use PostgreSQL\n\nSQLite cant handle concurrent agent writes. PostgreSQL gives us connection pooling and row-level locking.\n\nAlternatives considered: SQLite WAL mode, DuckDB.' > decisions/use-postgresql.md"
Enter
Type "echo '---\ntitle: Auth Flow Research\ntags: [auth, security]\n---\n\n# OAuth2 with PKCE\n\nPKCE is required for public clients. Authorization code flow with S256 challenge.\n\nSource: RFC 7636, OWASP recommendations.' > research/auth-flow.md"
Enter
Type "echo '---\ntitle: API Rate Limiting\ntags: [api, performance]\n---\n\n# Rate Limiting Strategy\n\nToken bucket at 100 req/min per user. Redis-backed for distributed counting.\nReturn 429 with Retry-After header.' > notes/rate-limiting.md"
Enter
Sleep 200ms
Show

# Init
Type "same init --yes"
Enter
Sleep 3s

Type "clear"
Enter
Sleep 300ms

# Search — the money shot
Type "same search \"how do we handle authentication\""
Enter
Sleep 2500ms

# Health — trust overview
Type "same health"
Enter
Sleep 3s

# Stale detection hint
Type "same stale"
Enter
Sleep 2s
