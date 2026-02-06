#!/usr/bin/env bash
# ==============================================================================
# pre-commit-check.sh — Vault Data Leak Prevention
# ==============================================================================
# Blocks commits that contain personal identifiers, client-sensitive patterns,
# or real vault data. Patterns are loaded from .scripts/.blocklist (gitignored).
#
# Hard blocks: personal identity, client names, local paths, real API keys.
# These should NEVER appear anywhere in this repo.
# ==============================================================================

set -euo pipefail

RED=""
GREEN=""
YELLOW=""
RESET=""
if [ -t 1 ] 2>/dev/null; then
    RED="\033[0;31m"
    GREEN="\033[0;32m"
    YELLOW="\033[1;33m"
    RESET="\033[0m"
fi

# --- Load blocklist from external file ---
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BLOCKLIST_FILE="$SCRIPT_DIR/.blocklist"

if [ ! -f "$BLOCKLIST_FILE" ]; then
    echo -e "${YELLOW}WARNING: No blocklist file found at .scripts/.blocklist${RESET}"
    echo "  PII scanning is disabled. Create .scripts/.blocklist with one pattern per line."
    echo "  See .scripts/blocklist.example for the expected format."
    exit 0
fi

# Read patterns from file, skip comments and blank lines
HARD_PATTERNS=()
while IFS= read -r line; do
    # Skip comments and blank lines
    [[ "$line" =~ ^[[:space:]]*# ]] && continue
    [[ -z "${line// }" ]] && continue
    HARD_PATTERNS+=("$line")
done < "$BLOCKLIST_FILE"

if [ ${#HARD_PATTERNS[@]} -eq 0 ]; then
    exit 0
fi

# Build grep pattern
PATTERN=""
for p in "${HARD_PATTERNS[@]}"; do
    if [ -z "$PATTERN" ]; then
        PATTERN="$p"
    else
        PATTERN="$PATTERN|$p"
    fi
done

# --- Gather staged files ---
STAGED_FILES=$(git diff --cached --name-only --diff-filter=ACMR 2>/dev/null || true)

if [ -z "$STAGED_FILES" ]; then
    exit 0
fi

# --- Scan ---
FOUND=0
MATCHES=""

while IFS= read -r file; do
    [ ! -f "$file" ] && continue

    # Skip binary files
    lc_file=$(echo "$file" | tr '[:upper:]' '[:lower:]')
    case "$lc_file" in
        *.png|*.jpg|*.jpeg|*.gif|*.webp|*.exe|*.dll|*.so|*.dylib|*.wasm) continue ;;
    esac

    SCAN_OUTPUT=$(git show ":$file" 2>/dev/null | grep -inE "$PATTERN" 2>/dev/null || true)

    if [ -n "$SCAN_OUTPUT" ]; then
        FOUND=$((FOUND + 1))
        MATCHES="${MATCHES}\n${YELLOW}--- ${file} ---${RESET}\n${SCAN_OUTPUT}\n"
    fi
done <<< "$STAGED_FILES"

# --- Report ---
if [ "$FOUND" -gt 0 ]; then
    echo ""
    echo -e "${RED}BLOCKED: Personal/client data detected in ${FOUND} file(s)${RESET}"
    echo ""
    echo -e "$MATCHES"
    echo ""
    echo "This repo must not contain personal identifiers, client names, or real paths."
    echo ""
    echo "To fix:"
    echo "  1. Remove the flagged content"
    echo "  2. Use synthetic/generic data instead"
    echo ""
    echo "To bypass (emergency only):"
    echo "  git commit --no-verify"
    echo ""
    exit 1
fi

exit 0
