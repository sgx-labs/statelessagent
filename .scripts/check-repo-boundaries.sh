#!/usr/bin/env bash
# Enforce repository boundaries for consumer-facing releases.
# This repo contains SAME product code only.
# Seed content and internal planning artifacts must stay out of git history.

set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"

# Forbidden tracked path patterns.
# NOTE: internal/seed and cmd/same/seed_cmd.go are product code and allowed.
FORBIDDEN_REGEXES=(
  '^\.research/'
  '^research/'
  '^sessions/'
  '^ralph/'
  '^awesome-mcp-servers(/|$)'
  '^awesome-mcp-servers-1(/|$)'
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
  # Treat deleted paths as already-remediated during local cleanup work.
  [ -e "$REPO_ROOT/$f" ] || continue
  for re in "${FORBIDDEN_REGEXES[@]}"; do
    if [[ "$f" =~ $re ]]; then
      OFFENDERS+=("$f")
      break
    fi
  done
done < <(git -C "$REPO_ROOT" ls-files)

if [ "${#OFFENDERS[@]}" -gt 0 ]; then
  echo "Repository boundary violation: forbidden tracked paths found:"
  for f in "${OFFENDERS[@]}"; do
    echo "  - $f"
  done
  echo ""
  echo "Keep seed content in https://github.com/sgx-labs/seed-vaults"
  echo "Keep internal research/handoffs out of this repo."
  exit 1
fi

echo "Repo boundaries OK (no forbidden tracked paths)."
