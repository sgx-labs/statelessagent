#!/usr/bin/env bash
# ==============================================================================
# provider-smoke.sh — Runtime smoke matrix for SAME providers
# ==============================================================================
# Runs a lightweight end-to-end flow for selected embedding providers:
#   reindex -> search -> graph stats/query -> web api ping
#
# Defaults are conservative for local/CI:
#   SAME_SMOKE_PROVIDERS=none
#   SAME_SMOKE_REQUIRED=none
#
# To run a fuller matrix locally:
#   SAME_SMOKE_PROVIDERS=none,ollama,openai-compatible \
#   SAME_SMOKE_REQUIRED=none,ollama \
#   SAME_SMOKE_OPENAI_BASE_URL=http://127.0.0.1:8080/v1 \
#   SAME_SMOKE_OPENAI_EMBED_MODEL=nomic-embed-text \
#   .scripts/provider-smoke.sh
# ==============================================================================

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${SAME_SMOKE_BIN:-$REPO_ROOT/build/same}"
PROVIDERS="${SAME_SMOKE_PROVIDERS:-none}"
REQUIRED="${SAME_SMOKE_REQUIRED:-none}"

PASS=0
FAIL=0
SKIP=0

pass() { printf "  PASS  %s\n" "$1"; PASS=$((PASS + 1)); }
fail() { printf "  FAIL  %s\n" "$1"; FAIL=$((FAIL + 1)); }
skip() { printf "  SKIP  %s\n" "$1"; SKIP=$((SKIP + 1)); }
info() { printf "  ....  %s\n" "$1"; }

contains_csv() {
    local list="$1"
    local want="$2"
    local item
    IFS=',' read -r -a __items <<<"$list"
    for item in "${__items[@]}"; do
        item="$(echo "$item" | tr '[:upper:]' '[:lower:]' | xargs)"
        if [ "$item" = "$want" ]; then
            return 0
        fi
    done
    return 1
}

provider_required() {
    contains_csv "$REQUIRED" "$1"
}

build_env_for_provider() {
    local provider="$1"
    local vault_dir="$2"
    local home_dir="$3"

    case "$provider" in
        none)
            cat <<EOF
VAULT_PATH=$vault_dir
HOME=$home_dir
SAME_EMBED_PROVIDER=none
SAME_CHAT_PROVIDER=none
SAME_GRAPH_LLM=off
EOF
            ;;
        ollama)
            local ollama_url="${SAME_SMOKE_OLLAMA_URL:-${OLLAMA_URL:-http://127.0.0.1:11434}}"
            cat <<EOF
VAULT_PATH=$vault_dir
HOME=$home_dir
SAME_EMBED_PROVIDER=ollama
OLLAMA_URL=$ollama_url
SAME_CHAT_PROVIDER=none
SAME_GRAPH_LLM=off
EOF
            ;;
        openai-compatible)
            local base_url="${SAME_SMOKE_OPENAI_BASE_URL:-}"
            local model="${SAME_SMOKE_OPENAI_EMBED_MODEL:-}"
            local api_key="${SAME_SMOKE_OPENAI_API_KEY:-}"
            if [ -z "$base_url" ] || [ -z "$model" ]; then
                return 1
            fi
            cat <<EOF
VAULT_PATH=$vault_dir
HOME=$home_dir
SAME_EMBED_PROVIDER=openai-compatible
SAME_EMBED_BASE_URL=$base_url
SAME_EMBED_MODEL=$model
SAME_EMBED_API_KEY=$api_key
SAME_CHAT_PROVIDER=none
SAME_GRAPH_LLM=off
EOF
            ;;
        *)
            return 1
            ;;
    esac
    return 0
}

probe_ollama() {
    local ollama_url="${SAME_SMOKE_OLLAMA_URL:-${OLLAMA_URL:-http://127.0.0.1:11434}}"
    local code
    code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 2 "${ollama_url%/}/api/tags" || true)"
    [ "$code" = "200" ]
}

probe_openai_compatible() {
    local base_url="${SAME_SMOKE_OPENAI_BASE_URL:-}"
    [ -n "$base_url" ] || return 1

    local api_key="${SAME_SMOKE_OPENAI_API_KEY:-}"
    local trimmed="${base_url%/}"
    local url code
    for url in "$trimmed/models" "$trimmed/v1/models"; do
        if [ -n "$api_key" ]; then
            code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 2 -H "Authorization: Bearer $api_key" "$url" || true)"
        else
            code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 2 "$url" || true)"
        fi
        if [ "$code" != "000" ]; then
            return 0
        fi
    done
    return 1
}

write_fixture_notes() {
    local vault_dir="$1"
    mkdir -p "$vault_dir/notes"
    cat >"$vault_dir/notes/alpha.md" <<'EOF'
---
title: Alpha
agent: woody
tags: [smoke, graph]
---

We decided: keep migration logic in internal/store/db.go.
Related context: notes/beta.md and cmd/same/main.go.
EOF

    cat >"$vault_dir/notes/beta.md" <<'EOF'
---
title: Beta
agent: buzz
tags: [smoke]
---

We chose to keep commands modular and testable.
See internal/indexer/indexer.go for indexing behavior.
EOF
}

wait_for_web_status() {
    local port="$1"
    local i
    for i in $(seq 1 25); do
        if curl -fsS --max-time 1 "http://127.0.0.1:${port}/api/status" >/dev/null 2>&1; then
            return 0
        fi
        sleep 0.2
    done
    return 1
}

run_mode() {
    local provider="$1"
    local required="$2"
    local tmp_root home_dir vault_dir env_file web_ok

    case "$provider" in
        ollama)
            if ! probe_ollama; then
                if [ "$required" = "1" ]; then
                    fail "provider=$provider required but Ollama is not reachable"
                else
                    skip "provider=$provider not reachable (optional)"
                fi
                return
            fi
            ;;
        openai-compatible)
            if ! probe_openai_compatible; then
                if [ "$required" = "1" ]; then
                    fail "provider=$provider required but base URL is not reachable"
                else
                    skip "provider=$provider not reachable/configured (optional)"
                fi
                return
            fi
            ;;
    esac

    tmp_root="$(mktemp -d "${TMPDIR:-/tmp}/same-smoke-${provider//[^a-zA-Z0-9]/_}-XXXXXX")"
    home_dir="$tmp_root/home"
    vault_dir="$tmp_root/vault"
    env_file="$tmp_root/env.sh"
    mkdir -p "$home_dir" "$vault_dir"
    write_fixture_notes "$vault_dir"

    if ! build_env_for_provider "$provider" "$vault_dir" "$home_dir" >"$env_file"; then
        if [ "$required" = "1" ]; then
            fail "provider=$provider required but smoke env is incomplete"
        else
            skip "provider=$provider skipped (missing config)"
        fi
        rm -rf "$tmp_root"
        return
    fi

    run_same_cmd() {
        local log_file="$1"
        shift
        (
            cd "$REPO_ROOT"
            set -a
            # shellcheck disable=SC1090
            . "$env_file"
            set +a
            "$BIN" "$@"
        ) >"$log_file" 2>&1
    }

    info "provider=$provider: reindex"
    if ! run_same_cmd "/tmp/same-smoke-${provider}.reindex.log" reindex --force; then
        if [ "$required" = "1" ]; then
            fail "provider=$provider reindex failed (see /tmp/same-smoke-${provider}.reindex.log)"
        else
            skip "provider=$provider reindex failed (optional)"
        fi
        rm -rf "$tmp_root"
        return
    fi

    info "provider=$provider: search"
    if ! run_same_cmd "/tmp/same-smoke-${provider}.search.log" search "migration logic" --top-k 3; then
        if [ "$required" = "1" ]; then
            fail "provider=$provider search failed"
        else
            skip "provider=$provider search failed (optional)"
        fi
        rm -rf "$tmp_root"
        return
    fi

    info "provider=$provider: graph stats/query"
    if ! run_same_cmd "/tmp/same-smoke-${provider}.graph-stats.json" graph stats --json; then
        if [ "$required" = "1" ]; then
            fail "provider=$provider graph stats failed"
        else
            skip "provider=$provider graph stats failed (optional)"
        fi
        rm -rf "$tmp_root"
        return
    fi
    if ! run_same_cmd "/tmp/same-smoke-${provider}.graph-query.json" graph query --type note --node notes/alpha.md --depth 2 --json; then
        if [ "$required" = "1" ]; then
            fail "provider=$provider graph query failed"
        else
            skip "provider=$provider graph query failed (optional)"
        fi
        rm -rf "$tmp_root"
        return
    fi

    web_ok=1
    info "provider=$provider: web api status"
    local port pid
    port="$((4300 + (RANDOM % 400)))"
    (
        cd "$REPO_ROOT"
        set -a
        # shellcheck disable=SC1090
        . "$env_file"
        set +a
        "$BIN" web --port "$port" >/tmp/same-smoke-${provider}.web.log 2>&1
    ) &
    pid=$!

    if ! wait_for_web_status "$port"; then
        kill "$pid" >/dev/null 2>&1 || true
        wait "$pid" >/dev/null 2>&1 || true
        if grep -qi "operation not permitted" "/tmp/same-smoke-${provider}.web.log" 2>/dev/null; then
            skip "provider=$provider web check skipped (sandbox/runtime blocks localhost bind)"
            web_ok=0
        fi
        if [ "$web_ok" = "1" ]; then
            if [ "$required" = "1" ]; then
                fail "provider=$provider web status endpoint not reachable"
                rm -rf "$tmp_root"
                return
            else
                skip "provider=$provider web status endpoint not reachable (optional)"
                rm -rf "$tmp_root"
                return
            fi
        fi
    else
        kill "$pid" >/dev/null 2>&1 || true
        wait "$pid" >/dev/null 2>&1 || true
        web_ok=1
    fi

    local db_path index_mode
    db_path="$vault_dir/.same/data/vault.db"
    index_mode="$(python3 - "$db_path" <<'PY'
import sqlite3
import sys

path = sys.argv[1]
try:
    conn = sqlite3.connect(path)
    cur = conn.cursor()
    cur.execute("SELECT value FROM schema_meta WHERE key='index_mode'")
    row = cur.fetchone()
    print(str(row[0]) if row else "")
except Exception:
    print("error")
PY
)"

    if [ "$provider" = "none" ]; then
        if [ "$index_mode" = "lite" ]; then
            pass "provider=$provider smoke flow (keyword-only expected, index_mode=lite)"
        else
            fail "provider=$provider smoke flow expected index_mode=lite, got ${index_mode:-<empty>}"
        fi
    else
        if [ "$index_mode" = "full" ]; then
            pass "provider=$provider smoke flow (semantic expected, index_mode=full)"
        else
            if [ "$required" = "1" ]; then
                fail "provider=$provider expected index_mode=full, got ${index_mode:-<empty>}"
            else
                skip "provider=$provider did not produce semantic index_mode=full (optional)"
            fi
        fi
    fi

    rm -rf "$tmp_root"
}

echo ""
echo "Provider smoke matrix"
echo "  Providers: $PROVIDERS"
echo "  Required:  $REQUIRED"

if [ ! -x "$BIN" ]; then
    echo "  FAIL  binary not found/executable: $BIN"
    exit 1
fi

IFS=',' read -r -a __providers <<<"$PROVIDERS"
for p in "${__providers[@]}"; do
    p="$(echo "$p" | tr '[:upper:]' '[:lower:]' | xargs)"
    [ -n "$p" ] || continue
    req=0
    if provider_required "$p"; then
        req=1
    fi
    run_mode "$p" "$req"
done

echo ""
echo "Provider smoke summary: ${PASS} passed, ${FAIL} failed, ${SKIP} skipped"

if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
exit 0
