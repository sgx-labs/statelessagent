#!/usr/bin/env bash
# Enforce repository boundaries.
# Product code only — no vault content or local artifacts in git history.

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"

# Forbidden tracked path patterns.
# NOTE: internal/seed and cmd/same/seed_cmd.go are product code and allowed.
FORBIDDEN_REGEXES=(
  '^\.research/'
  '^research/'
  '^sessions/'
  '^seed-vaults(/|$)'
  '^seeds(/|$)'
  '^seed-content(/|$)'
  '^vaults/'
  '^\.mcp\.json$'
  '^\.mcpregistry_'
  '^\.claude/'
  '^\.same/'
  '^\.devlog/'
  '^\.scripts/\.blocklist$'
  '^build/'
  '^same$'
  '^IMPLEMENTATION_PLAN\.md$'
  '^decisions\.md$'
)

OFFENDERS=()
while IFS= read -r f; do
  [ -z "$f" ] && continue
  [ -e "$REPO_ROOT/$f" ] || continue
  for re in "${FORBIDDEN_REGEXES[@]}"; do
    if [[ "$f" =~ $re ]]; then
      OFFENDERS+=("$f")
      break
    fi
  done
done < <(git -C "$REPO_ROOT" ls-files)

if [ "${#OFFENDERS[@]}" -gt 0 ]; then
  echo "Boundary violation: forbidden tracked paths found:"
  for f in "${OFFENDERS[@]}"; do
    echo "  - $f"
  done
  exit 1
fi

echo "Repo boundaries OK."
