#!/usr/bin/env bash
#
# Seed Vault Retrieval Benchmark
# Tests SAME search quality on seed vault content the engine was never tuned against.
#
# Metrics:
#   Recall@5  — Did the expected note appear in top 5 results?
#   MRR       — Mean Reciprocal Rank (1/rank of first correct hit)
#   Term Hit  — Did the expected terms appear in any result snippet?
#
# Usage:
#   ./run_seed_bench.sh                   # full run
#   ./run_seed_bench.sh --skip-reindex    # skip reindex (if already done)
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VAULT_DIR="$SCRIPT_DIR/seed_bench"
EVAL_FILE="$SCRIPT_DIR/seed_bench_eval.json"
RESULTS_DIR="$SCRIPT_DIR/results"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
RESULTS_FILE="$RESULTS_DIR/seed_bench_${TIMESTAMP}.json"
TOP_K=5

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

echo -e "${BOLD}${CYAN}"
echo "  ╔══════════════════════════════════════════════╗"
echo "  ║   SAME Seed Vault Retrieval Benchmark        ║"
echo "  ╚══════════════════════════════════════════════╝"
echo -e "${NC}"

# --- Prerequisites ---
if ! command -v same &>/dev/null; then
    echo -e "${RED}Error: 'same' binary not found in PATH${NC}"
    exit 1
fi

if ! command -v python3 &>/dev/null; then
    echo -e "${RED}Error: python3 required${NC}"
    exit 1
fi

if [ ! -f "$EVAL_FILE" ]; then
    echo -e "${RED}Error: $EVAL_FILE not found${NC}"
    exit 1
fi

mkdir -p "$RESULTS_DIR"

# --- Reindex ---
if [[ "${1:-}" != "--skip-reindex" ]]; then
    echo -e "${CYAN}[1/3] Reindexing vault...${NC}"
    cd "$VAULT_DIR"
    same reindex 2>&1 | tail -5
    echo ""
else
    echo -e "${YELLOW}[1/3] Skipping reindex (--skip-reindex)${NC}"
    echo ""
fi

# --- Run all queries and collect search results ---
echo -e "${CYAN}[2/3] Running $TOP_K-result retrieval for 30 queries...${NC}"
echo ""

# Build a file of search results: one JSON line per query
SEARCH_RESULTS_FILE=$(mktemp)

cd "$VAULT_DIR"

TOTAL=$(python3 -c "import json; print(len(json.load(open('$EVAL_FILE'))))")
CURRENT=0

# Read queries from eval file and run search for each
python3 -c "
import json
with open('$EVAL_FILE') as f:
    for item in json.load(f):
        print(item['id'], '|||', item['query'])
" | while IFS= read -r line; do
    CURRENT=$((CURRENT + 1))
    id=$(echo "$line" | cut -d'|' -f1 | tr -d ' ')
    query=$(echo "$line" | sed 's/^[0-9]* ||| //')

    # Run search, capture JSON output
    search_json=$(same search "$query" --top-k $TOP_K --json 2>/dev/null || echo "[]")

    # Write id and search results as a JSON line
    python3 -c "
import json, sys
results = json.loads(sys.stdin.read())
print(json.dumps({'id': $id, 'results': results}))
" <<< "$search_json" >> "$SEARCH_RESULTS_FILE"

    echo -e "  [${CURRENT}/${TOTAL}] id=$id  $query"
done

echo ""

# --- Evaluate and report ---
echo -e "${CYAN}[3/3] Computing metrics...${NC}"
echo ""

export SEARCH_RESULTS_FILE
export EVAL_FILE
export RESULTS_FILE

python3 << 'PYEOF'
import json, os, sys
from collections import defaultdict
from datetime import datetime

eval_file = os.environ["EVAL_FILE"]
search_file = os.environ["SEARCH_RESULTS_FILE"]
results_file = os.environ["RESULTS_FILE"]

# Load eval cases
with open(eval_file) as f:
    eval_cases = {c['id']: c for c in json.load(f)}

# Load search results
search_map = {}
with open(search_file) as f:
    for line in f:
        line = line.strip()
        if line:
            obj = json.loads(line)
            search_map[obj['id']] = obj['results']

# Evaluate each query
results = []
for qid, case in sorted(eval_cases.items()):
    search_results = search_map.get(qid, [])
    expect_note = case['expect_note']
    expect_terms = case['expect_in_results']

    # Extract result paths
    result_paths = [r.get('path', '') for r in search_results]

    # Recall@5: is the expected note in the top 5?
    recall_hit = 0
    rank = 0
    for i, path in enumerate(result_paths[:5]):
        if path.endswith(expect_note) or expect_note in path:
            recall_hit = 1
            rank = i + 1
            break

    # Term hits in snippets + titles
    all_text = ' '.join(
        [r.get('snippet', '') + ' ' + r.get('title', '') + ' ' + r.get('path', '')
         for r in search_results]
    ).lower()
    term_hits = sum(1 for t in expect_terms if t.lower() in all_text)
    term_total = len(expect_terms)

    # MRR contribution
    mrr_score = (1.0 / rank) if rank > 0 else 0.0

    results.append({
        'id': qid,
        'query': case['query'],
        'seed': case['seed'],
        'difficulty': case['difficulty'],
        'expect_note': expect_note,
        'recall_at_5': recall_hit,
        'rank': rank,
        'mrr': mrr_score,
        'term_hits': term_hits,
        'term_total': term_total,
        'result_paths': result_paths[:5],
    })

# Overall metrics
total = len(results)
recall_hits = sum(r['recall_at_5'] for r in results)
recall_at_5 = recall_hits / total if total else 0
mrr = sum(r['mrr'] for r in results) / total if total else 0
all_term_hits = sum(r['term_hits'] for r in results)
all_term_total = sum(r['term_total'] for r in results)
term_rate = all_term_hits / all_term_total if all_term_total else 0

# Per-seed metrics
seeds = defaultdict(list)
for r in results:
    seeds[r['seed']].append(r)

seed_metrics = {}
for seed_name, seed_results in sorted(seeds.items()):
    n = len(seed_results)
    s_recall = sum(r['recall_at_5'] for r in seed_results) / n
    s_mrr = sum(r['mrr'] for r in seed_results) / n
    s_th = sum(r['term_hits'] for r in seed_results)
    s_tt = sum(r['term_total'] for r in seed_results)
    seed_metrics[seed_name] = {
        'count': n,
        'recall_at_5': round(s_recall, 3),
        'mrr': round(s_mrr, 3),
        'term_hit_rate': round(s_th / s_tt if s_tt else 0, 3),
    }

# Per-difficulty metrics
difficulties = defaultdict(list)
for r in results:
    difficulties[r['difficulty']].append(r)

diff_metrics = {}
for diff_name, diff_results in sorted(difficulties.items()):
    n = len(diff_results)
    d_recall = sum(r['recall_at_5'] for r in diff_results) / n
    d_mrr = sum(r['mrr'] for r in diff_results) / n
    diff_metrics[diff_name] = {
        'count': n,
        'recall_at_5': round(d_recall, 3),
        'mrr': round(d_mrr, 3),
    }

# Misses
misses = [r for r in results if r['recall_at_5'] == 0]

# Print report
W = 60
print("\033[1m" + "=" * W + "\033[0m")
print("\033[1m  SEED VAULT RETRIEVAL BENCHMARK RESULTS\033[0m")
print("\033[1m" + "=" * W + "\033[0m")
print()
print(f"  Queries:          {total}")
print(f"  Top-K:            5")
print(f"  Notes indexed:    153")
print(f"  Chunks indexed:   391")
print()
print("\033[1m  Overall Metrics\033[0m")
print(f"  {'─' * 42}")
print(f"  Recall@5:         {recall_at_5:.1%}  ({recall_hits}/{total})")
print(f"  MRR:              {mrr:.3f}")
print(f"  Term Hit Rate:    {term_rate:.1%}  ({all_term_hits}/{all_term_total})")
print()

print("\033[1m  Per Seed Vault\033[0m")
print(f"  {'─' * 52}")
print(f"  {'Seed':<30} {'Recall@5':>10} {'MRR':>8} {'Terms':>8}")
print(f"  {'─' * 52}")
for sn, m in seed_metrics.items():
    print(f"  {sn:<30} {m['recall_at_5']:>9.1%} {m['mrr']:>8.3f} {m['term_hit_rate']:>7.1%}")
print()

print("\033[1m  Per Difficulty\033[0m")
print(f"  {'─' * 42}")
print(f"  {'Level':<15} {'Count':>6} {'Recall@5':>10} {'MRR':>8}")
print(f"  {'─' * 42}")
for dn, m in diff_metrics.items():
    print(f"  {dn:<15} {m['count']:>6} {m['recall_at_5']:>9.1%} {m['mrr']:>8.3f}")
print()

# Per-query detail
print("\033[1m  Per-Query Results\033[0m")
print(f"  {'─' * 70}")
for r in results:
    status = "\033[32mHIT @{}\033[0m".format(r['rank']) if r['recall_at_5'] else "\033[31mMISS\033[0m"
    terms_status = f"{r['term_hits']}/{r['term_total']}"
    print(f"  id={r['id']:>2} {status:<20} terms={terms_status}  {r['query'][:55]}")
print()

if misses:
    print("\033[1m  Miss Analysis\033[0m")
    print(f"  {'─' * 65}")
    for m in misses:
        print(f"  id={m['id']:>2}  expected: {m['expect_note']}")
        print(f"         query:    {m['query'][:65]}")
        for i, p in enumerate(m['result_paths'][:3]):
            print(f"         got[{i+1}]:   {p}")
        print()

# Save JSON
output = {
    'timestamp': datetime.now().isoformat(),
    'benchmark': 'seed_vault_retrieval',
    'description': 'Cross-domain retrieval benchmark using SAME seed vault content',
    'config': {
        'top_k': 5,
        'notes_indexed': 153,
        'chunks_indexed': 391,
        'seeds': ['claude-code-power-user', 'security-audit-framework', 'ai-agent-architecture'],
        'embedding_model': 'nomic-embed-text',
    },
    'overall': {
        'recall_at_5': round(recall_at_5, 4),
        'mrr': round(mrr, 4),
        'term_hit_rate': round(term_rate, 4),
        'total_queries': total,
        'hits': recall_hits,
        'misses': total - recall_hits,
    },
    'per_seed': seed_metrics,
    'per_difficulty': diff_metrics,
    'queries': results,
    'miss_analysis': [
        {
            'id': m['id'],
            'query': m['query'],
            'expect_note': m['expect_note'],
            'got_top3': m['result_paths'][:3]
        }
        for m in misses
    ],
}

with open(results_file, 'w') as f:
    json.dump(output, f, indent=2)

print(f"  Results saved: {results_file}")
print()
PYEOF

# Clean up
rm -f "$SEARCH_RESULTS_FILE"

echo -e "${GREEN}${BOLD}Benchmark complete.${NC}"
