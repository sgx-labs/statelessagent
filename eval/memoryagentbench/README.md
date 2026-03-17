# MemoryAgentBench Adapter for SAME

Evaluates [SAME](https://statelessagent.com) against the [MemoryAgentBench](https://huggingface.co/datasets/ai-hyz/MemoryAgentBench) benchmark (ICLR 2026), the academic standard for agent memory system evaluation.

## What is MemoryAgentBench?

MemoryAgentBench provides 146 test cases across 4 splits, each testing a different memory capability:

| Split | Examples | Description | What it tests |
|-------|----------|-------------|---------------|
| **Accurate Retrieval (AR)** | 22 | Find specific facts in large fact stores | Precision of semantic search |
| **Test Time Learning (TTL)** | 6 | Learn and apply new knowledge at inference | Adaptation without retraining |
| **Long Range Understanding (LRU)** | 110 | Synthesize info across long contexts | Cross-chunk reasoning |
| **Conflict Resolution (CR)** | 8 | Handle contradictory or updated facts | Memory integrity and trust |

Each example has:
- A **context** (5K-26K chars of structured facts)
- 100 **questions** (often multi-hop)
- **Expected answers** (list of acceptable strings per question)

## How to run

### Prerequisites

- `same` binary installed and on PATH
- Ollama running with `qwen2.5-coder:3b` (for QA) and `nomic-embed-text` (for embeddings)
- Python 3.11+ with `datasets` package (`pip install datasets`)

### Quick test (dry run)

```bash
python run_bench.py --dry-run
```

Runs 5 questions from Conflict Resolution example 0. Takes about 2-3 minutes.

### Conflict Resolution (recommended first run)

```bash
# Examples 0 and 4 only (200 questions, ~30 min)
python run_bench.py --split Conflict_Resolution --examples 0 4

# Retrieval only, no LLM answering (faster)
python run_bench.py --split Conflict_Resolution --examples 0 4 --skip-llm
```

### Full split

```bash
# All 8 CR examples (800 questions, ~2 hours)
python run_bench.py --split Conflict_Resolution

# All 22 AR examples (2200 questions, ~6 hours)
python run_bench.py --split Accurate_Retrieval
```

### Options

| Flag | Description |
|------|-------------|
| `--split` | Dataset split (default: `Conflict_Resolution`) |
| `--examples 0 4` | Specific example indices |
| `--max-questions 10` | Limit questions per example |
| `--skip-llm` | Retrieval recall only (skip LLM answering) |
| `--dry-run` | Quick sanity check (5 questions) |
| `--chunk-size 800` | Characters per chunk (default: 800) |

## Metrics

### Retrieval Recall@5
Does the expected answer appear (substring match) in any of the top-5 search results?

This is the primary metric for SAME. It measures whether our semantic search + embedding pipeline surfaces the right facts.

### QA Accuracy
Does the LLM's generated answer contain the expected answer?

This is secondary — it depends on both retrieval quality AND the LLM's reasoning ability. With a 3B local model, expect significantly lower QA than retrieval recall.

## Results

Results are saved as JSON to `results/` with timestamps:

```
results/
  Conflict_Resolution_20260316_120000.json
  Conflict_Resolution_dryrun_20260316_115500.json
```

## Expected performance ranges

### SAME (local Ollama, nomic-embed-text)

| Split | Retrieval Recall@5 | QA Accuracy |
|-------|-------------------|-------------|
| CR | 20-40% | 10-25% |
| AR | 30-50% | 15-30% |

Note: Multi-hop questions are hard. The questions often require chaining 2-3 facts (e.g., "What is the country of citizenship of the spouse of the author of Our Mutual Friend?"). Retrieval can find relevant chunks, but a 3B model struggles to chain the reasoning.

### Published baselines (from MemoryAgentBench paper)

| System | CR | AR | TTL | LRU |
|--------|----|----|-----|-----|
| Mem0 | 14.1% | 20.0% | 10.5% | 5.3% |
| Letta (MemGPT) | 22.8% | 25.3% | 18.7% | 7.1% |
| Zep | 18.5% | 22.1% | 12.3% | 6.8% |
| Full-context GPT-4 | 45.2% | 52.0% | 35.1% | 18.3% |

(Paper Table 2, QA accuracy metric. Numbers approximate from published figures.)

SAME's retrieval recall should be competitive with or exceed these systems' QA numbers, since we're measuring the retrieval step separately. The QA gap comes from using a local 3B model vs GPT-4.

## Comparing against baselines

To make a fair comparison:

1. **Retrieval vs QA**: Published numbers are end-to-end QA. Our retrieval recall is a generous upper bound — it means the answer was in the retrieved context. For apples-to-apples QA comparison, use a comparable LLM (GPT-4 via `same ask` with an OpenAI provider).

2. **Chunk size**: We use 800-char chunks. Smaller chunks = better precision but more noise. Larger chunks = more context but lower recall. The 800-char default keeps each chunk to ~10-15 facts.

3. **Top-K**: We measure Recall@5. Increasing to @10 will improve recall at the cost of providing more noise to the LLM.

## Architecture

```
run_bench.py
  |
  |-- Load dataset from HuggingFace (cached)
  |-- For each example:
  |     |-- Chunk context into ~800-char .md files
  |     |-- same init --yes (create vault)
  |     |-- same reindex (embed chunks via Ollama)
  |     |-- For each question:
  |     |     |-- same search --json "<question>" (top-5)
  |     |     |-- Check retrieval recall
  |     |     |-- ollama run qwen2.5-coder:3b (answer generation)
  |     |     |-- Check QA accuracy
  |     |-- Cleanup temp vault
  |-- Compute aggregate metrics
  |-- Save results JSON
```
