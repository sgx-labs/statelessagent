# LongMemEval Adapter for SAME

Evaluates [SAME](https://statelessagent.com) against the [LongMemEval](https://github.com/xiaowu0162/LongMemEval) benchmark (ICLR 2025, arXiv:2410.10813), the academic standard for long-term conversational memory evaluation.

## What is LongMemEval?

LongMemEval tests whether a memory system can remember, update, and reason over facts scattered across multi-session chat histories. 500 manually curated questions test five core memory abilities:

| Ability | Question Types | What it Tests |
|---------|---------------|---------------|
| Information Extraction | single-session-user, single-session-assistant, single-session-preference | Finding specific facts in a single past session |
| Multi-session Reasoning | multi-session | Synthesizing information across multiple sessions |
| Temporal Reasoning | temporal-reasoning | Using time constraints to find the right answer |
| Knowledge Updates | knowledge-update | Handling facts that change over time |
| Abstention | (questions ending in `_abs`) | Knowing when to say "I don't know" |

### Dataset Variants

| Variant | Sessions/Question | Tokens | Description |
|---------|-------------------|--------|-------------|
| **s** (default) | ~40 | ~115K | Standard evaluation |
| **m** | ~500 | ~1.5M | Stress test at scale |
| **oracle** | evidence only | varies | Upper bound (only answer-containing sessions) |

Dataset: [xiaowu0162/longmemeval-cleaned](https://huggingface.co/datasets/xiaowu0162/longmemeval-cleaned) on HuggingFace.

---

## Methodology

This section documents the scientific integrity constraints enforced by this adapter. These constraints exist so that results are reproducible, comparable, and not inflated.

### 1. SAME parameters were not tuned for this benchmark

The adapter runs `same init --yes` and uses whatever config it produces. **No retrieval parameters are modified:**

- `distance_threshold` -- default from init
- `composite_threshold` -- default from init
- `max_results` -- default from init
- `max_token_budget` -- default from init

The only config change permitted is enabling the embedding provider (`ollama` with `nomic-embed-text`) if init failed to detect a running Ollama during setup. This enables the feature; it does not tune it. All thresholds remain at their shipped defaults.

### 2. Sessions are converted to plain markdown without SAME-specific metadata

When converting LongMemEval chat sessions to SAME notes, the adapter produces plain markdown with minimal frontmatter:

```yaml
---
title: "First user message (truncated to 80 chars)"
date: 2024-01-15
---
```

That is all. **No tags, no domain, no workstream, no content_type, no confidence, no trust_state.** A real user's chat sessions do not come pre-tagged with SAME-aware metadata. Adding such metadata would inflate retrieval scores by giving the system information a real deployment would not have.

### 3. Default embedding model and retrieval settings used

- Embedding model: whatever `same init --yes` selects (typically `nomic-embed-text` via Ollama)
- Search command: `same search --json --top-k 5 "<question>"`
- No custom retrieval pipelines, no re-ranking, no query expansion
- No fine-tuned models, no benchmark-specific embeddings

### 4. No oracle leakage

The LongMemEval dataset contains oracle fields that **must not** influence retrieval or answer generation:

| Oracle Field | Where It Appears | How This Adapter Handles It |
|-------------|------------------|----------------------------|
| `answer` | Top-level per question | Used **only** during scoring, after retrieval completes |
| `answer_session_ids` | Top-level per question | Used **only** during scoring, after retrieval completes |
| `has_answer` | On individual turns within sessions | **Stripped** when converting sessions to markdown |

The `--verify-no-leakage` flag runs structural checks confirming these constraints hold:

```bash
python run_bench.py --verify-no-leakage
```

This verifies:
- `has_answer` fields are stripped from generated markdown notes
- No SAME-specific metadata is injected into notes
- Frontmatter contains only `title` and `date`
- Oracle answer and evidence fields exist in the dataset but are isolated from retrieval

### 5. All questions reported, no filtering

Every question in the dataset is evaluated and reported in the results. If a question returns no results, it is recorded as a miss (`retrieval_hit: false`). If vault creation fails, the error is recorded and the question counts against the score. The aggregate numbers reflect all questions evaluated -- no cherry-picking, no post-hoc exclusion.

### 6. Reproducibility

Every results JSON file includes:
- SAME binary path
- Dataset variant and file used
- Timestamp
- Full methodology declaration (embedded in the JSON)
- Per-question retrieval and QA results
- Aggregate scores with per-question-type breakdown

---

## How to Run

### Prerequisites

- `same` binary installed and on PATH
- Ollama running with `nomic-embed-text` (for embeddings)
- Ollama running with `qwen2.5-coder:3b` (for QA, optional with `--skip-llm`)
- Python 3.11+

### Step 1: Verify integrity

```bash
python run_bench.py --verify-no-leakage
```

Always run this first. It confirms that oracle data is properly isolated.

### Step 2: Dry run

```bash
python run_bench.py --dry-run
```

Runs 5 questions. Takes about 5-10 minutes depending on hardware.

### Step 3: Full evaluation

```bash
# All 500 questions, retrieval only (~2-4 hours)
python run_bench.py --variant s --skip-llm

# All 500 questions with QA (~4-8 hours)
python run_bench.py --variant s

# Specific question types
python run_bench.py --variant s --question-types knowledge-update temporal-reasoning

# Scale test with ~500 sessions per question
python run_bench.py --variant m --skip-llm
```

### Options

| Flag | Description |
|------|-------------|
| `--variant` | Dataset variant: `s` (default), `m`, `oracle` |
| `--question-types` | Filter to specific question types |
| `--max-questions N` | Limit number of questions evaluated |
| `--skip-llm` | Retrieval recall only (skip LLM answering) |
| `--dry-run` | Quick sanity check (5 questions) |
| `--verify-no-leakage` | Confirm oracle data isolation and exit |

## Metrics

### Retrieval Recall

Does the expected answer appear (case-insensitive substring) in any of the top-5 search results? This is the primary metric. It measures whether SAME's semantic search surfaces the right information from past conversations.

### QA Accuracy

Does the LLM's generated answer contain the expected answer? This is secondary -- it depends on both retrieval quality AND the LLM's reasoning ability. With a local 3B model, expect significantly lower QA scores than retrieval recall.

### Per-type breakdown

Scores are broken down by all six question types plus abstention. This reveals which memory abilities SAME handles well and which need improvement.

## Results

Results are saved as JSON to `results/` with timestamps:

```
results/
  longmemeval_s_20260327_120000.json
  longmemeval_s_dryrun_20260327_115500.json
```

## Official LongMemEval Evaluation

For GPT-4o-judged QA scoring (the official metric), export predictions and use the upstream evaluation script:

1. Clone: `git clone https://github.com/xiaowu0162/LongMemEval.git`
2. Download `longmemeval_oracle.json` from HuggingFace
3. Run:
   ```bash
   cd LongMemEval/src/evaluation
   python evaluate_qa.py gpt-4o path/to/predictions.jsonl path/to/longmemeval_oracle.json
   ```

## Architecture

```
run_bench.py
  |
  |-- Download dataset from HuggingFace (cached in data/)
  |-- Group questions by shared haystack (minimize vault creation)
  |-- For each unique haystack:
  |     |-- Convert sessions to plain markdown (title + date only)
  |     |-- same init --yes (default config, NO parameter tuning)
  |     |-- same reindex --force (embed sessions via Ollama)
  |     |-- For each question sharing this haystack:
  |     |     |-- same search --json "<question>" (top-5, default thresholds)
  |     |     |-- Score retrieval recall using oracle answer (scoring only)
  |     |     |-- [optional] Generate answer via ollama (qwen2.5-coder:3b)
  |     |     |-- Score QA accuracy using oracle answer (scoring only)
  |     |     |-- Record result (including misses and errors)
  |     |-- Cleanup temp vault
  |-- Compute aggregate metrics (ALL questions, no filtering)
  |-- Save results JSON with embedded methodology declaration
```

## Comparison Context

When comparing SAME results against published numbers:

1. **Published LongMemEval scores use GPT-4o** for both answer generation and evaluation judging. Our local QA numbers with a 3B model will be lower. Retrieval recall is the fairer comparison for the retrieval layer.

2. **Session count matters.** longmemeval_s (~40 sessions) is the standard variant. longmemeval_m (~500 sessions) is significantly harder.

3. **SAME is a retrieval system, not an end-to-end chat assistant.** LongMemEval was designed for chat assistants with full conversation context. SAME converts sessions to notes and retrieves by semantic similarity. This is a different architecture than the systems the benchmark was designed for.

4. **No parameter tuning.** Many published results involve system-specific optimizations (query expansion, session decomposition, etc.). This adapter tests SAME as shipped, with default settings.
