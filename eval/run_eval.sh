#!/usr/bin/env bash
#
# Retrieval Quality Evaluation Suite for SAME
#
# Indexes the test vault and runs all eval test cases via `same search --json`.
# Reports Recall@5, MRR, and per-category breakdown.
#
# Usage:
#   ./eval/run_eval.sh              # run eval (reuses existing index if present)
#   ./eval/run_eval.sh --reindex    # force reindex before eval
#   ./eval/run_eval.sh --json       # output results as JSON only (for CI)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VAULT_DIR="$SCRIPT_DIR/test_vault"
TEST_CASES="$SCRIPT_DIR/retrieval_eval.json"
RESULTS_DIR="$SCRIPT_DIR/results"
SAME_BIN="${SAME_BIN:-same}"

# Colors (disabled if not a terminal)
if [ -t 1 ]; then
    RED='\033[0;31m'
    GREEN='\033[0;32m'
    YELLOW='\033[0;33m'
    BLUE='\033[0;34m'
    BOLD='\033[1m'
    DIM='\033[2m'
    RESET='\033[0m'
else
    RED='' GREEN='' YELLOW='' BLUE='' BOLD='' DIM='' RESET=''
fi

# Parse flags
FORCE_REINDEX=false
JSON_ONLY=false
for arg in "$@"; do
    case "$arg" in
        --reindex) FORCE_REINDEX=true ;;
        --json) JSON_ONLY=true ;;
        --help|-h)
            echo "Usage: $0 [--reindex] [--json]"
            echo "  --reindex   Force reindex of test vault before running eval"
            echo "  --json      Output results as JSON only (for CI)"
            exit 0
            ;;
    esac
done

# Preflight checks
if ! command -v "$SAME_BIN" &>/dev/null; then
    echo "Error: '$SAME_BIN' not found in PATH. Set SAME_BIN to the same binary path." >&2
    exit 1
fi

if ! command -v jq &>/dev/null; then
    echo "Error: 'jq' not found. Install jq to run the eval suite." >&2
    exit 1
fi

if [ ! -f "$TEST_CASES" ]; then
    echo "Error: Test cases file not found at $TEST_CASES" >&2
    exit 1
fi

if [ ! -d "$VAULT_DIR" ]; then
    echo "Error: Test vault directory not found at $VAULT_DIR" >&2
    exit 1
fi

mkdir -p "$RESULTS_DIR"

# Step 1: Initialize and index the vault
log() {
    if [ "$JSON_ONLY" = false ]; then
        echo -e "$@"
    fi
}

log "${BOLD}SAME Retrieval Quality Eval${RESET}"
log "${DIM}$(date -Iseconds)${RESET}"
log ""

VAULT_DB="$VAULT_DIR/.same/data/vault.db"
if [ "$FORCE_REINDEX" = true ] || [ ! -f "$VAULT_DB" ]; then
    log "${BLUE}Indexing test vault...${RESET}"

    # Initialize vault if needed (non-interactive)
    if [ ! -d "$VAULT_DIR/.same" ]; then
        "$SAME_BIN" init --vault "$VAULT_DIR" --provider ollama --yes 2>&1 | while IFS= read -r line; do
            log "  ${DIM}$line${RESET}"
        done
    fi

    # Reindex (force to ensure fresh embeddings)
    "$SAME_BIN" reindex --vault "$VAULT_DIR" --force 2>&1 | while IFS= read -r line; do
        log "  ${DIM}$line${RESET}"
    done

    log "${GREEN}Indexing complete.${RESET}"
    log ""
else
    log "${DIM}Using existing index at $VAULT_DB${RESET}"
    log "${DIM}Pass --reindex to force a fresh index.${RESET}"
    log ""
fi

# Step 2: Run test cases
TOTAL=$(jq 'length' "$TEST_CASES")
PASS=0
FAIL=0
SKIP=0
TOTAL_RR=0  # sum of reciprocal ranks (for MRR)

# Per-category counters (associative arrays)
declare -A CAT_TOTAL CAT_PASS CAT_FAIL CAT_RR

# Detailed results array for JSON output
DETAILS="[]"

log "${BOLD}Running $TOTAL test cases...${RESET}"
log ""

for i in $(seq 0 $((TOTAL - 1))); do
    # Extract test case fields
    TC=$(jq ".[$i]" "$TEST_CASES")
    TC_ID=$(echo "$TC" | jq -r '.id')
    TC_QUERY=$(echo "$TC" | jq -r '.query')
    TC_EXPECT_NOTE=$(echo "$TC" | jq -r '.expect_note // ""')
    TC_CATEGORY=$(echo "$TC" | jq -r '.category')
    TC_NEGATIVE=$(echo "$TC" | jq -r '.negative // false')
    TC_EXPECT_TERMS=$(echo "$TC" | jq -r '.expect_in_results[]? // empty')

    # Initialize category counters if needed
    CAT_TOTAL[$TC_CATEGORY]=$(( ${CAT_TOTAL[$TC_CATEGORY]:-0} + 1 ))
    CAT_PASS[$TC_CATEGORY]=${CAT_PASS[$TC_CATEGORY]:-0}
    CAT_FAIL[$TC_CATEGORY]=${CAT_FAIL[$TC_CATEGORY]:-0}
    CAT_RR[$TC_CATEGORY]=${CAT_RR[$TC_CATEGORY]:-0}

    # Run search
    SEARCH_OUTPUT=$("$SAME_BIN" search --vault "$VAULT_DIR" --json --top-k 5 "$TC_QUERY" 2>/dev/null || echo "[]")

    # Validate JSON output
    if ! echo "$SEARCH_OUTPUT" | jq empty 2>/dev/null; then
        SEARCH_OUTPUT="[]"
    fi

    NUM_RESULTS=$(echo "$SEARCH_OUTPUT" | jq 'length')

    # For negative tests: pass if no results or results don't contain forbidden terms
    if [ "$TC_NEGATIVE" = "true" ]; then
        TC_EXPECT_NOT=$(echo "$TC" | jq -r '.expect_not_in_results[]? // empty')
        FOUND_NEGATIVE=false

        if [ "$NUM_RESULTS" -gt 0 ] && [ -n "$TC_EXPECT_NOT" ]; then
            ALL_TEXT=$(echo "$SEARCH_OUTPUT" | jq -r '.[].snippet // ""' | tr '[:upper:]' '[:lower:]')
            ALL_TITLES=$(echo "$SEARCH_OUTPUT" | jq -r '.[].title // ""' | tr '[:upper:]' '[:lower:]')
            COMBINED="$ALL_TEXT $ALL_TITLES"

            for term in $TC_EXPECT_NOT; do
                term_lower=$(echo "$term" | tr '[:upper:]' '[:lower:]')
                if echo "$COMBINED" | grep -qi "$term_lower"; then
                    FOUND_NEGATIVE=true
                    break
                fi
            done
        fi

        if [ "$FOUND_NEGATIVE" = true ]; then
            FAIL=$((FAIL + 1))
            CAT_FAIL[$TC_CATEGORY]=$(( ${CAT_FAIL[$TC_CATEGORY]} + 1 ))
            STATUS="FAIL"
            log "  ${RED}FAIL${RESET} [${TC_ID}] ${TC_QUERY} ${DIM}(negative: found forbidden terms)${RESET}"
        else
            PASS=$((PASS + 1))
            CAT_PASS[$TC_CATEGORY]=$(( ${CAT_PASS[$TC_CATEGORY]} + 1 ))
            STATUS="PASS"
            log "  ${GREEN}PASS${RESET} [${TC_ID}] ${TC_QUERY} ${DIM}(negative: correctly absent)${RESET}"
        fi

        # Record detail
        DETAIL=$(jq -n \
            --argjson id "$TC_ID" \
            --arg query "$TC_QUERY" \
            --arg category "$TC_CATEGORY" \
            --arg status "$STATUS" \
            --argjson num_results "$NUM_RESULTS" \
            --arg note "negative test" \
            '{id: $id, query: $query, category: $category, status: $status, num_results: $num_results, note: $note, reciprocal_rank: 0}')
        DETAILS=$(echo "$DETAILS" | jq ". + [$DETAIL]")
        continue
    fi

    # Positive test evaluation
    # Check 1: Expected note appears in results
    NOTE_FOUND=false
    NOTE_RANK=0
    if [ -n "$TC_EXPECT_NOTE" ]; then
        for rank in $(seq 0 $((NUM_RESULTS - 1))); do
            RESULT_PATH=$(echo "$SEARCH_OUTPUT" | jq -r ".[$rank].path // \"\"")
            if [ "$RESULT_PATH" = "$TC_EXPECT_NOTE" ]; then
                NOTE_FOUND=true
                NOTE_RANK=$((rank + 1))
                break
            fi
        done
    fi

    # Check 2: Expected terms appear in top-5 result snippets/titles
    TERMS_FOUND=0
    TERMS_TOTAL=0
    if [ -n "$TC_EXPECT_TERMS" ]; then
        ALL_TEXT=$(echo "$SEARCH_OUTPUT" | jq -r '.[].snippet // ""' | tr '[:upper:]' '[:lower:]')
        ALL_TITLES=$(echo "$SEARCH_OUTPUT" | jq -r '.[].title // ""' | tr '[:upper:]' '[:lower:]')
        COMBINED="$ALL_TEXT $ALL_TITLES"

        for term in $TC_EXPECT_TERMS; do
            TERMS_TOTAL=$((TERMS_TOTAL + 1))
            term_lower=$(echo "$term" | tr '[:upper:]' '[:lower:]')
            if echo "$COMBINED" | grep -qi "$term_lower"; then
                TERMS_FOUND=$((TERMS_FOUND + 1))
            fi
        done
    fi

    # Determine pass/fail
    # Pass if: (a) expected note found in top-5, OR (b) at least half the expected terms found
    PASSED=false
    if [ "$NOTE_FOUND" = true ]; then
        PASSED=true
    elif [ "$TERMS_TOTAL" -gt 0 ] && [ "$TERMS_FOUND" -ge $(( (TERMS_TOTAL + 1) / 2 )) ]; then
        PASSED=true
    fi

    # Compute reciprocal rank
    RR="0"
    if [ "$NOTE_FOUND" = true ] && [ "$NOTE_RANK" -gt 0 ]; then
        RR=$(echo "scale=4; 1 / $NOTE_RANK" | bc)
    fi

    if [ "$PASSED" = true ]; then
        PASS=$((PASS + 1))
        CAT_PASS[$TC_CATEGORY]=$(( ${CAT_PASS[$TC_CATEGORY]} + 1 ))
        STATUS="PASS"

        if [ "$NOTE_FOUND" = true ]; then
            log "  ${GREEN}PASS${RESET} [${TC_ID}] ${TC_QUERY} ${DIM}(note@${NOTE_RANK}, terms: ${TERMS_FOUND}/${TERMS_TOTAL})${RESET}"
        else
            log "  ${GREEN}PASS${RESET} [${TC_ID}] ${TC_QUERY} ${DIM}(terms: ${TERMS_FOUND}/${TERMS_TOTAL}, note not in top-5)${RESET}"
        fi
    else
        FAIL=$((FAIL + 1))
        CAT_FAIL[$TC_CATEGORY]=$(( ${CAT_FAIL[$TC_CATEGORY]} + 1 ))
        STATUS="FAIL"

        # Show what was returned for debugging
        TOP_PATHS=$(echo "$SEARCH_OUTPUT" | jq -r '.[].path // "?"' | head -3 | tr '\n' ', ' | sed 's/,$//')
        log "  ${RED}FAIL${RESET} [${TC_ID}] ${TC_QUERY} ${DIM}(note: ${NOTE_FOUND}, terms: ${TERMS_FOUND}/${TERMS_TOTAL}, got: ${TOP_PATHS})${RESET}"
    fi

    TOTAL_RR=$(echo "$TOTAL_RR + $RR" | bc)
    CAT_RR[$TC_CATEGORY]=$(echo "${CAT_RR[$TC_CATEGORY]} + $RR" | bc)

    # Record detail
    DETAIL=$(jq -n \
        --argjson id "$TC_ID" \
        --arg query "$TC_QUERY" \
        --arg category "$TC_CATEGORY" \
        --arg status "$STATUS" \
        --argjson num_results "$NUM_RESULTS" \
        --arg expect_note "$TC_EXPECT_NOTE" \
        --argjson note_rank "$NOTE_RANK" \
        --argjson terms_found "$TERMS_FOUND" \
        --argjson terms_total "$TERMS_TOTAL" \
        --arg rr "$RR" \
        '{id: $id, query: $query, category: $category, status: $status, num_results: $num_results, expect_note: $expect_note, note_rank: $note_rank, terms_found: $terms_found, terms_total: $terms_total, reciprocal_rank: ($rr | tonumber)}')
    DETAILS=$(echo "$DETAILS" | jq ". + [$DETAIL]")
done

# Step 3: Compute aggregate metrics
EVALUATED=$((PASS + FAIL))
if [ "$EVALUATED" -gt 0 ]; then
    RECALL=$(echo "scale=4; $PASS / $EVALUATED" | bc)
    RECALL_PCT=$(echo "scale=1; $RECALL * 100" | bc)
    MRR=$(echo "scale=4; $TOTAL_RR / $EVALUATED" | bc)
else
    RECALL="0"
    RECALL_PCT="0.0"
    MRR="0"
fi

# Step 4: Report
log ""
log "${BOLD}════════════════════════════════════════${RESET}"
log "${BOLD}  Results Summary${RESET}"
log "${BOLD}════════════════════════════════════════${RESET}"
log ""
log "  Total:    $EVALUATED"
log "  ${GREEN}Pass:     $PASS${RESET}"
log "  ${RED}Fail:     $FAIL${RESET}"
log ""
log "  ${BOLD}Recall@5: ${RECALL_PCT}%${RESET}"
log "  ${BOLD}MRR:      ${MRR}${RESET}"
log ""
log "${BOLD}  Per-Category Breakdown:${RESET}"
log ""

# Build category summary JSON
CAT_SUMMARY="{}"
for cat in $(echo "${!CAT_TOTAL[@]}" | tr ' ' '\n' | sort); do
    ct=${CAT_TOTAL[$cat]}
    cp=${CAT_PASS[$cat]}
    cf=${CAT_FAIL[$cat]}
    cr=${CAT_RR[$cat]}
    if [ "$ct" -gt 0 ]; then
        cat_recall=$(echo "scale=4; $cp / $ct" | bc)
        cat_recall_pct=$(echo "scale=1; $cat_recall * 100" | bc)
        cat_mrr=$(echo "scale=4; $cr / $ct" | bc)
    else
        cat_recall_pct="0.0"
        cat_mrr="0"
    fi

    # Color code the category result
    if [ "$(echo "$cat_recall_pct >= 80" | bc)" -eq 1 ]; then
        COLOR="$GREEN"
    elif [ "$(echo "$cat_recall_pct >= 50" | bc)" -eq 1 ]; then
        COLOR="$YELLOW"
    else
        COLOR="$RED"
    fi

    log "  ${BOLD}${cat}${RESET}: ${COLOR}${cat_recall_pct}%${RESET} recall (${cp}/${ct}), MRR: ${cat_mrr}"

    CAT_SUMMARY=$(echo "$CAT_SUMMARY" | jq \
        --arg cat "$cat" \
        --argjson total "$ct" \
        --argjson pass "$cp" \
        --argjson fail "$cf" \
        --arg recall "$cat_recall_pct" \
        --arg mrr "$cat_mrr" \
        '. + {($cat): {total: $total, pass: $pass, fail: $fail, recall_pct: ($recall | tonumber), mrr: ($mrr | tonumber)}}')
done

log ""

# Step 5: Write JSON results
TIMESTAMP=$(date -Iseconds)
RESULT_FILE="$RESULTS_DIR/eval_$(date +%Y%m%d_%H%M%S).json"

RESULT_JSON=$(jq -n \
    --arg timestamp "$TIMESTAMP" \
    --argjson total "$EVALUATED" \
    --argjson pass "$PASS" \
    --argjson fail "$FAIL" \
    --arg recall "$RECALL_PCT" \
    --arg mrr "$MRR" \
    --argjson categories "$CAT_SUMMARY" \
    --argjson details "$DETAILS" \
    '{
        timestamp: $timestamp,
        summary: {
            total: $total,
            pass: $pass,
            fail: $fail,
            recall_at_5_pct: ($recall | tonumber),
            mrr: ($mrr | tonumber)
        },
        categories: $categories,
        details: $details
    }')

echo "$RESULT_JSON" > "$RESULT_FILE"

if [ "$JSON_ONLY" = true ]; then
    echo "$RESULT_JSON"
else
    log "${DIM}Results saved to: $RESULT_FILE${RESET}"
    log ""
fi

# Exit with non-zero if recall is below threshold
THRESHOLD="${EVAL_RECALL_THRESHOLD:-0}"
if [ "$THRESHOLD" != "0" ]; then
    if [ "$(echo "$RECALL_PCT < $THRESHOLD" | bc)" -eq 1 ]; then
        log "${RED}Recall ${RECALL_PCT}% is below threshold ${THRESHOLD}%${RESET}"
        exit 1
    fi
fi
