#!/usr/bin/env bash
# ==============================================================================
# pre-commit-check.sh — Vault Data Leak Prevention
# ==============================================================================
# Blocks commits that contain vault-specific paths, personal identifiers,
# or client-sensitive patterns. This hook exists because SAME is developed
# separately from the vault it operates on — nothing from the vault should
# ever appear in product source code.
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

# --- Patterns that should NEVER appear in product code ---
PATTERNS=(
    # Vault folder structure
    "00_Inbox"
    "01_Projects"
    "02_Areas"
    "03_Resources"
    "04_Archive"
    "05_Attachments"
    "06_Metadata"
    "07_Journal"
    "_PRIVATE/"

    # Personal identifiers
    "REDACTED"
    "REDACTED"
    "REDACTED"
    "REDACTED"
    "REDACTED"

    # Local paths
    "/Users/REDACTED"
    "REDACTED"
    "REDACTED"
    "REDACTED"

    # Real API keys (prefixes)
    "sk-ant-api"
    "sk-proj-"
    "AIzaSy[A-Za-z0-9]"
)

# Build grep pattern
PATTERN=""
for p in "${PATTERNS[@]}"; do
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

    # Skip this hook itself
    [ "$file" = ".scripts/pre-commit-check.sh" ] && continue
    [ "$file" = ".git/hooks/pre-commit" ] && continue

    SCAN_OUTPUT=$(git show ":$file" 2>/dev/null | grep -inE "$PATTERN" 2>/dev/null || true)

    if [ -n "$SCAN_OUTPUT" ]; then
        FOUND=$((FOUND + 1))
        MATCHES="${MATCHES}\n${YELLOW}--- ${file} ---${RESET}\n${SCAN_OUTPUT}\n"
    fi
done <<< "$STAGED_FILES"

# --- Report ---
if [ "$FOUND" -gt 0 ]; then
    echo ""
    echo -e "${RED}BLOCKED: Vault data detected in ${FOUND} file(s)${RESET}"
    echo ""
    echo -e "$MATCHES"
    echo ""
    echo "This repo must not contain vault paths, personal info, or client data."
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
