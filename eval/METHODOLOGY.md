# SAME Evaluation Methodology

## Overview

SAME uses a three-tier evaluation approach to measure retrieval quality. Each tier serves a different purpose and has different rules about how it can influence development.

## How We Got Here (The Full Story)

We built features, then tested them. Here's what happened — honestly.

### Phase 1: First test on a real vault
We ran 15 developer-realistic queries against SAME's own repo (94 notes, 260 chunks). **Result: 53.3% Recall@5.** Root cause: noise. Skill files, Docker docs, npm READMEs were drowning out actual documentation. The search algorithm was fine — the vault was messy.

### Phase 2: Curated eval vault + tuning
We built a 35-note curated vault and 55 test cases (later expanded to 68). We tuned features — content-type boosting, tag-based graph connections, .sameignore, metadata filters — and re-ran the eval after each change. **Result: 97.1% Recall@5 (keyword mode).** But we were testing with the same data we tuned against.

### Phase 3: The overfitting check
We wrote 30 new "held-out" test cases that we deliberately did NOT look at during development. The runner hides individual query details. We ran the held-out set with keyword-only search. **Result: 10.0% Recall@5.** An 87-point gap from the tuning set. Keyword search alone cannot handle diverse natural language queries.

### Phase 4: The real number
We ran the same 30 held-out cases with semantic search (nomic-embed-text embeddings via Ollama). **Result: 93.33% Recall@5.** The gap between tuning (97.1%) and held-out (93.3%) is 4 points — meaning the improvements genuinely generalize, but semantic search is essential.

### What this means
- **93.3% is the honest number** — blind test cases, semantic search, curated vault
- **Keyword search alone is insufficient** (10%) — embeddings are required for real-world queries
- **The tuning set score (97.1%) is optimistic** — always reported with that caveat
- **Real-world messy vaults score lower** (53.3%) — vault hygiene matters, .sameignore helps
- **Integrity queries are the weakest category** (66.7%) — needs metadata-aware search improvements

### Scores at each stage

| Test | Mode | Recall@5 | Notes |
|------|------|----------|-------|
| Real vault (94 notes) | semantic | 53.3% | Noisy vault, no .sameignore |
| Internal eval (68 cases) | keyword | 97.1% | Tuning set — overfitted |
| Held-out eval (30 cases) | keyword | 10.0% | Keyword can't handle diverse queries |
| **Held-out eval (30 cases)** | **semantic** | **93.3%** | **The honest number** |

---

## Tier 1: Internal Eval (Tuning Set)

**File:** `eval/retrieval_eval.json` (68 test cases)
**Vault:** `eval/test_vault/` (35 curated developer notes)
**Runner:** `eval/run_eval.sh` (CLI, semantic search) or `eval/eval_test.go` (Go, keyword)

### Purpose
Fast iteration during development. Run after every change to check for regressions and measure improvement on known queries.

### Composition
- 44 retrieval queries
- 7 handoff queries
- 8 staleness queries
- 4 graph queries
- 5 integrity queries
- 3 negative cases (topics not in vault)

### Rules
- **MAY** be used to inform feature development
- **MAY** be tuned against (add test cases, adjust expectations)
- **MUST** be labeled as "internal eval on curated test data" in any reporting
- **MUST NOT** be presented as an independent benchmark

### Known Bias
This set was created alongside the features being tested. Queries were sometimes written to validate specific improvements (e.g., handoff queries added when handoff recall was low). Scores on this set are optimistic.

---

## Tier 2: Held-Out Eval (Validation Set)

**File:** `eval/held_out_eval.json` (30 test cases)
**Vault:** `eval/test_vault/` (same vault as Tier 1)
**Runner:** `eval/run_held_out.sh` (hides individual query details)

### Purpose
Check whether improvements generalize beyond the tuning set. The runner intentionally hides individual test case details from terminal output to prevent unconscious overfitting.

### Composition
- 15 retrieval queries (different angles than Tier 1)
- 5 handoff queries
- 4 staleness queries
- 3 integrity queries
- 3 negative cases

### Rules
- **MUST NOT** be used to inform feature development
- **MUST NOT** look at individual failing queries to guide fixes
- **MAY** check aggregate scores (Recall@5, MRR, per-category)
- **MUST** be labeled as "held-out validation set" in any reporting
- If this score improves alongside Tier 1, improvements are generalizing
- If only Tier 1 improves, we are overfitting

### Inspection Policy
The results JSON file (`eval/results/held_out_*.json`) contains full details. Do not inspect it during active development. It is acceptable to inspect it during dedicated evaluation sessions where no code changes follow.

---

## Tier 3: MemoryAgentBench (External Benchmark)

**Dataset:** `ai-hyz/MemoryAgentBench` on HuggingFace (146 test cases, 4 splits)
**Adapter:** `eval/memoryagentbench/run_bench.py`
**Paper:** ICLR 2026, arXiv:2507.05257

### Purpose
Produce comparable scores against published results from other systems (Mem0, Letta, Cognee, HippoRAG). This is the only tier suitable for public comparison claims.

### Splits
| Split | Cases | Tests |
|-------|-------|-------|
| Accurate Retrieval (AR) | 22 | Single/multi-hop fact retrieval |
| Test-Time Learning (TTL) | 6 | Learning during evaluation |
| Long-Range Understanding (LRU) | 110 | Global comprehension |
| Conflict Resolution (CR) | 8 | Handling contradictory information |

### Rules
- **MUST NOT** use any MemoryAgentBench data for training or tuning
- **MUST NOT** examine specific failing questions to guide SAME feature development
- **MAY** run the benchmark and report aggregate scores
- **MAY** compare against published baselines (Mem0: 68.5%, Letta: 74% on LoCoMo)
- **MUST** label as "MemoryAgentBench (ICLR 2026)" with split name in any reporting
- **MUST** disclose that 5 CR questions were observed during adapter development (dry run)

### Data Hygiene
- No MemoryAgentBench data exists in the repository (exports were deleted in commit `1df9345`)
- The adapter downloads fresh data from HuggingFace on each run
- Training data for fine-tuning (eval/training/) must never include MemoryAgentBench content
- The fine-tuning pipeline trains on entity/relationship extraction, not on Q&A — different task

### Published Baselines (for comparison context)
| System | LoCoMo Accuracy | Source |
|--------|----------------|--------|
| Mem0 (graph) | 68.5% | Letta blog, 2026 |
| Letta (filesystem) | 74.0% | Letta blog, 2026 |
| AMA-Agent (causality graph) | 57.2% | AMA-Bench paper, 2026 |

---

## Metrics

All tiers use the same metrics:

- **Recall@5**: Does the expected note/content appear in the top 5 search results?
- **MRR (Mean Reciprocal Rank)**: Average of 1/rank for the first correct result. Higher = correct results appear earlier.
- **Per-category breakdown**: Scores split by query type (retrieval, handoff, staleness, integrity, graph, negative).

---

## Test Vault

**Location:** `eval/test_vault/`
**Notes:** 35 markdown files across 6 directories

| Directory | Count | Content |
|-----------|-------|---------|
| decisions/ | 5 | Architecture decision records |
| sessions/ | 5 | Session handoff notes |
| architecture/ | 10 | System design documents |
| meetings/ | 5 | Sprint planning, reviews, postmortems |
| project/ | 5 | Onboarding, standards, release process |
| stale/ | 5 | Deliberately outdated notes (trust_state: stale) |

All notes have realistic frontmatter (title, domain, tags, confidence, content_type) and substantive technical content.

---

## Running Evaluations

### Quick (keyword-only, ~0.2s)
```bash
go test ./eval/ -v
```

### Full (semantic search, ~2-5 min depending on hardware)
```bash
cd eval/test_vault && same reindex --force
cd .. && bash run_eval.sh
```

### Held-out (semantic search, aggregate only)
```bash
cd eval/test_vault && same reindex --force
cd .. && bash run_held_out.sh
```

### MemoryAgentBench (external, ~10-30 min)
```bash
cd eval/memoryagentbench
python3 run_bench.py --split Conflict_Resolution --examples 0,4
```

---

## Reporting Guidelines

When reporting evaluation results:

1. Always state which tier the result comes from
2. Always state the search mode (keyword vs semantic)
3. Always state the embedding model used
4. Always state the vault (curated eval vault vs real-world vault)
5. Never present Tier 1 scores as if they are independent benchmarks
6. Never present results without disclosing the evaluation methodology
7. Use Tier 3 (MemoryAgentBench) for any public competitive comparisons

### Example (correct)
> "SAME achieves 92.7% Recall@5 on our internal eval suite (68 curated test cases, semantic search with nomic-embed-text). On the held-out validation set (30 blind cases), recall is X%. On MemoryAgentBench CR split, retrieval recall is Y%."

### Example (incorrect)
> "SAME achieves 97.1% retrieval precision." ← No context, keyword-only tuning set score presented as a benchmark.
