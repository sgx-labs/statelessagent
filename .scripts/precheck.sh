#!/usr/bin/env bash
# ==============================================================================
# precheck.sh — Pre-release verification gate
# ==============================================================================
# Run before every push/tag. Repo-scope hygiene checks for version consistency,
# test/build health, and blocklist-pattern scanning in tracked files.
# ==============================================================================

set -euo pipefail

RED=""
GREEN=""
YELLOW=""
RESET=""
BOLD=""
if [ -t 1 ] 2>/dev/null; then
    RED="\033[0;31m"
    GREEN="\033[0;32m"
    YELLOW="\033[1;33m"
    BOLD="\033[1m"
    RESET="\033[0m"
fi

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
PASS=0
FAIL=0
WARN=0

pass() { echo -e "  ${GREEN}PASS${RESET}  $1"; PASS=$((PASS + 1)); }
fail() { echo -e "  ${RED}FAIL${RESET}  $1"; FAIL=$((FAIL + 1)); }
warn() { echo -e "  ${YELLOW}WARN${RESET}  $1"; WARN=$((WARN + 1)); }
info() { echo -e "  ${BOLD}....${RESET}  $1"; }

echo ""
echo -e "${BOLD}Pre-release checks${RESET}"
echo ""

# --- 1. Version consistency ---
echo -e "${BOLD}Version consistency${RESET}"

MAKE_VER=$(grep '^VERSION' "$REPO_ROOT/Makefile" | awk '{print $NF}')
NPM_VER=$(grep '"version"' "$REPO_ROOT/npm/package.json" | head -1 | grep -oE '[0-9]+\.[0-9]+\.[0-9]+')
# server.json has two version fields — check the top-level one
SERVER_VER=$(grep '"version"' "$REPO_ROOT/server.json" | head -1 | grep -oE '[0-9]+\.[0-9]+\.[0-9]+')

if [ "$MAKE_VER" = "$NPM_VER" ] && [ "$MAKE_VER" = "$SERVER_VER" ]; then
    pass "All files: v$MAKE_VER (Makefile, npm/package.json, server.json)"
else
    fail "Version mismatch: Makefile=$MAKE_VER npm=$NPM_VER server=$SERVER_VER"
fi

# Check CHANGELOG mentions this version
if grep -q "## v${MAKE_VER}" "$REPO_ROOT/CHANGELOG.md" 2>/dev/null; then
    pass "CHANGELOG.md has v${MAKE_VER} entry"
else
    fail "CHANGELOG.md missing v${MAKE_VER} entry"
fi

# Repo boundary guard
if /usr/bin/env bash "$REPO_ROOT/.scripts/check-repo-boundaries.sh" >/dev/null 2>&1; then
    pass "Repo boundaries (no seed-content/internal planning artifacts tracked)"
else
    fail "Repo boundaries violated (run .scripts/check-repo-boundaries.sh)"
fi

# --- 2. Build ---
echo ""
echo -e "${BOLD}Build${RESET}"

info "Building..."
if BUILD_OUT=$(make -C "$REPO_ROOT" build 2>&1); then
    pass "make build"
else
    fail "make build"
    echo "$BUILD_OUT" | tail -5 | while IFS= read -r l; do echo "        $l"; done
fi

# Verify binary version matches
if [ -f "$REPO_ROOT/build/same" ]; then
    BIN_VER=$("$REPO_ROOT/build/same" version 2>&1 | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' || echo "unknown")
    if [ "$BIN_VER" = "$MAKE_VER" ]; then
        pass "Binary reports v$BIN_VER"
    else
        fail "Binary reports v$BIN_VER but Makefile says v$MAKE_VER"
    fi
fi

# --- 3. Tests ---
echo ""
echo -e "${BOLD}Tests${RESET}"

info "Running test suite..."
if TEST_OUT=$(make -C "$REPO_ROOT" test 2>&1); then
    pass "make test (all packages)"
else
    fail "make test"
    echo "$TEST_OUT" | grep -E '(FAIL|panic)' | tail -5 | while IFS= read -r l; do echo "        $l"; done
fi

# --- 4. Repo-scope pattern scan ---
echo ""
echo -e "${BOLD}Release hygiene (repo scope)${RESET}"
info "Scope: scans changed tracked repo files with configured blocklist patterns."
info "Does not audit user vault contents, full git history, forks, or mirrors."

# Run blocklist pattern checks against changed tracked files.
BLOCKLIST="$REPO_ROOT/.scripts/.blocklist"
BLOCKLIST_LABEL=".scripts/.blocklist"
if [ ! -f "$BLOCKLIST" ] && [ -f "$REPO_ROOT/.scripts/blocklist.example" ]; then
    BLOCKLIST="$REPO_ROOT/.scripts/blocklist.example"
    BLOCKLIST_LABEL=".scripts/blocklist.example"
fi
if [ -f "$BLOCKLIST" ]; then
    # Scan all tracked files (not just staged) for PII patterns
    PATTERN=""
    while IFS= read -r line; do
        [[ "$line" =~ ^[[:space:]]*# ]] && continue
        [[ -z "${line// }" ]] && continue
        if [ -z "$PATTERN" ]; then
            PATTERN="$line"
        else
            PATTERN="$PATTERN|$line"
        fi
    done < "$BLOCKLIST"

    if [ -n "$PATTERN" ]; then
        # Check both uncommitted and staged changes
        CHANGED_FILES=$( (git -C "$REPO_ROOT" diff HEAD --name-only 2>/dev/null; git -C "$REPO_ROOT" diff --cached --name-only 2>/dev/null) | sort -u)
        PII_HITS=""
        while IFS= read -r f; do
            [ -z "$f" ] && continue
            [ -f "$REPO_ROOT/$f" ] || continue
            if grep -lE "$PATTERN" "$REPO_ROOT/$f" >/dev/null 2>&1; then
                PII_HITS="$PII_HITS $f"
            fi
        done <<< "$CHANGED_FILES"
        if [ -z "$PII_HITS" ]; then
            pass "No blocklist pattern matches in changed tracked files (patterns: $BLOCKLIST_LABEL)"
        else
            fail "Blocklist pattern matches found in changed tracked files:$PII_HITS"
        fi
    fi
else
    warn "No .scripts/.blocklist — repo blocklist scan skipped"
fi

# Check git identity
GIT_NAME=$(git -C "$REPO_ROOT" config user.name 2>/dev/null || echo "")
GIT_EMAIL=$(git -C "$REPO_ROOT" config user.email 2>/dev/null || echo "")
if [ "$GIT_NAME" = "sgx-labs" ] && [ "$GIT_EMAIL" = "dev@sgx-labs.dev" ]; then
    pass "Git identity: $GIT_NAME <$GIT_EMAIL>"
else
    fail "Git identity: $GIT_NAME <$GIT_EMAIL> (expected sgx-labs <dev@sgx-labs.dev>)"
fi

# Check server.json is valid JSON
if python3 -c "import json,sys; json.load(open(sys.argv[1]))" "$REPO_ROOT/server.json" 2>/dev/null; then
    pass "server.json is valid JSON"
else
    fail "server.json is invalid JSON"
fi

# --- 5. CLI smoke test ---
echo ""
echo -e "${BOLD}CLI smoke test${RESET}"

if "$REPO_ROOT/build/same" --help 2>&1 | grep -q "Getting Started"; then
    pass "same --help shows grouped commands"
else
    fail "same --help missing command groups"
fi

# --- 6. Provider smoke matrix ---
echo ""
echo -e "${BOLD}Provider smoke matrix${RESET}"
info "Default runs provider=none only. Set SAME_SMOKE_PROVIDERS for broader matrix."
if SMOKE_OUT=$(SAME_SMOKE_PROVIDERS="${SAME_SMOKE_PROVIDERS:-none}" \
    SAME_SMOKE_REQUIRED="${SAME_SMOKE_REQUIRED:-none}" \
    /usr/bin/env bash "$REPO_ROOT/.scripts/provider-smoke.sh" 2>&1); then
    pass "provider smoke matrix (${SAME_SMOKE_PROVIDERS:-none})"
else
    fail "provider smoke matrix failed"
    echo "$SMOKE_OUT" | tail -10 | while IFS= read -r l; do echo "        $l"; done
fi

# --- Summary ---
echo ""
TOTAL=$((PASS + FAIL + WARN))
if [ "$FAIL" -gt 0 ]; then
    echo -e "${RED}${BOLD}FAILED${RESET}: $PASS passed, $FAIL failed, $WARN warnings (of $TOTAL checks)"
    echo ""
    echo "Fix failures before pushing."
    exit 1
else
    echo -e "${GREEN}${BOLD}ALL CLEAR${RESET}: $PASS passed, $WARN warnings (of $TOTAL checks)"
    echo ""
    echo "Ready to push (repo-scope checks)."
    exit 0
fi
