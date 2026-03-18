#!/usr/bin/env python3
"""
MemoryAgentBench Adapter for SAME (Stateless Agent Memory Engine)

Evaluates SAME's memory retrieval against the MemoryAgentBench benchmark
(ICLR 2026). Dataset: ai-hyz/MemoryAgentBench on HuggingFace.

Usage:
    # Dry run: 5 questions from CR example 0
    python run_bench.py --dry-run

    # Full CR examples 0 and 4
    python run_bench.py --split Conflict_Resolution --examples 0 4

    # All examples in a split
    python run_bench.py --split Accurate_Retrieval

    # Specific question count per example (for testing)
    python run_bench.py --split Conflict_Resolution --examples 0 --max-questions 10
"""

import argparse
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
from datetime import datetime
from pathlib import Path

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

SAME_BIN = shutil.which("same") or "/usr/local/bin/same"
OLLAMA_MODEL = "qwen2.5-coder:3b"
CHUNK_SIZE = 800          # characters per markdown chunk
SEARCH_TOP_K = 5          # top-k results for retrieval
QUESTION_TIMEOUT = 60     # seconds per LLM call
REINDEX_TIMEOUT = 300     # seconds for same reindex
RESULTS_DIR = Path(__file__).parent / "results"

SPLIT_NAMES = [
    "Accurate_Retrieval",
    "Test_Time_Learning",
    "Long_Range_Understanding",
    "Conflict_Resolution",
]

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def log(msg: str):
    """Timestamped console logging."""
    ts = datetime.now().strftime("%H:%M:%S")
    print(f"[{ts}] {msg}", flush=True)


def chunk_context(context: str, chunk_size: int = CHUNK_SIZE) -> list[str]:
    """
    Split context into chunks of approximately `chunk_size` characters,
    breaking at newline boundaries where possible.

    Each fact line is kept whole unless it exceeds chunk_size on its own.
    """
    lines = context.strip().split("\n")
    chunks: list[str] = []
    current: list[str] = []
    current_len = 0

    for line in lines:
        line_len = len(line) + 1  # +1 for the newline
        if current_len + line_len > chunk_size and current:
            chunks.append("\n".join(current))
            current = []
            current_len = 0
        current.append(line)
        current_len += line_len

    if current:
        chunks.append("\n".join(current))

    return chunks


def create_vault(vault_dir: str, chunks: list[str]) -> bool:
    """
    Initialize a SAME vault in vault_dir and write chunks as .md files.
    `same init --yes` discovers and indexes all files in one step.
    Returns True on success.
    """
    # Clear any stale init lockfile from previous runs
    lockfile = os.path.expanduser("~/.config/same/init.lock")
    if os.path.exists(lockfile):
        try:
            os.remove(lockfile)
            log(f"  Cleared stale lockfile: {lockfile}")
        except OSError:
            pass

    os.makedirs(vault_dir, exist_ok=True)

    # Write chunks as numbered markdown files
    notes_dir = os.path.join(vault_dir, "notes")
    os.makedirs(notes_dir, exist_ok=True)
    for i, chunk in enumerate(chunks):
        filepath = os.path.join(notes_dir, f"facts_{i:04d}.md")
        with open(filepath, "w") as f:
            f.write(chunk)

    # Write a config.toml to ensure semantic search is used (not keyword-only).
    # Without this, same init in a bare temp dir may default to keyword-only
    # if it doesn't detect Ollama during the init flow.
    same_dir = os.path.join(vault_dir, ".same")
    os.makedirs(same_dir, exist_ok=True)
    config_path = os.path.join(same_dir, "config.toml")
    with open(config_path, "w") as f:
        f.write(f'[vault]\n  path = "{vault_dir}"\n  handoff_dir = "sessions"\n  decision_log = "decisions.md"\n\n')
        f.write('[embedding]\n  provider = "ollama"\n  model = "nomic-embed-text"\n\n')
        f.write('[display]\n  mode = "compact"\n')

    # same init --yes discovers files and indexes them in one pass.
    # With ~34 chunks and local Ollama embeddings, this can take 60-180 seconds.
    log(f"  Initializing + indexing vault at {vault_dir} ({len(chunks)} chunks)...")
    t0 = time.time()
    result = subprocess.run(
        [SAME_BIN, "init", "--yes"],
        cwd=vault_dir,
        capture_output=True,
        text=True,
        timeout=REINDEX_TIMEOUT,
    )
    elapsed = time.time() - t0

    # Check if embeddings were actually used
    if "keyword-only" in result.stdout.lower() or "keyword-only" in result.stderr.lower():
        log(f"  WARNING: Vault initialized in keyword-only mode. Embeddings may not be available.")
        log(f"  Attempting explicit reindex with embeddings...")
        reindex_result = subprocess.run(
            [SAME_BIN, "reindex", "--force"],
            cwd=vault_dir,
            capture_output=True,
            text=True,
            timeout=REINDEX_TIMEOUT,
        )
        if "semantic" in reindex_result.stdout.lower():
            log(f"  Reindex upgraded to semantic search.")
        else:
            log(f"  WARNING: Still not using semantic search. Results will be keyword-only.")

    if result.returncode != 0:
        log(f"  ERROR: same init failed ({elapsed:.1f}s): {result.stderr[:300]}")
        return False

    log(f"  Vault ready in {elapsed:.1f}s")
    return True


def same_search(vault_dir: str, query: str, top_k: int = SEARCH_TOP_K) -> list[str]:
    """
    Run `same search` and return the top-k result texts.
    Returns a list of result strings.
    """
    try:
        result = subprocess.run(
            [SAME_BIN, "search", "--json", "--top-k", str(top_k), query],
            cwd=vault_dir,
            capture_output=True,
            text=True,
            timeout=QUESTION_TIMEOUT,
        )
    except subprocess.TimeoutExpired:
        log(f"    TIMEOUT: same search for '{query[:50]}...'")
        return []

    if result.returncode != 0:
        return []

    # Parse JSON output
    try:
        data = json.loads(result.stdout)
    except json.JSONDecodeError:
        # Fallback: return raw stdout lines
        return [line.strip() for line in result.stdout.strip().split("\n") if line.strip()]

    # Extract text from results — adapt to SAME's JSON format
    texts = []
    if isinstance(data, list):
        for item in data:
            if isinstance(item, dict):
                # SAME uses "snippet" for retrieved text
                text = item.get("snippet") or item.get("content") or item.get("text") or item.get("body") or ""
                if text:
                    texts.append(text)
            elif isinstance(item, str):
                texts.append(item)
    elif isinstance(data, dict):
        # Might be wrapped in a results key
        results = data.get("results") or data.get("matches") or data.get("notes") or []
        for item in results:
            if isinstance(item, dict):
                text = item.get("snippet") or item.get("content") or item.get("text") or item.get("body") or ""
                if text:
                    texts.append(text)
            elif isinstance(item, str):
                texts.append(item)

    return texts[:top_k]


def llm_answer(question: str, context_chunks: list[str]) -> str:
    """
    Use Ollama to generate an answer given retrieved context chunks.
    Returns the LLM's answer string.
    """
    context = "\n---\n".join(context_chunks)
    prompt = f"""Based on the following retrieved facts, answer the question concisely.
Give ONLY the answer — no explanation, no preamble. If you cannot determine the answer, say "UNKNOWN".

Retrieved facts:
{context}

Question: {question}
Answer:"""

    try:
        result = subprocess.run(
            ["ollama", "run", OLLAMA_MODEL],
            input=prompt,
            capture_output=True,
            text=True,
            timeout=QUESTION_TIMEOUT,
        )
        answer = result.stdout.strip()
        # Take just the first line if multi-line
        if "\n" in answer:
            answer = answer.split("\n")[0].strip()
        return answer
    except subprocess.TimeoutExpired:
        return "TIMEOUT"
    except Exception as e:
        return f"ERROR: {e}"


def check_retrieval_recall(retrieved_texts: list[str], expected_answers: list[str]) -> bool:
    """
    Retrieval Recall@K: does any expected answer appear (case-insensitive substring)
    in any of the top-K retrieved chunks?
    """
    combined = " ".join(retrieved_texts).lower()
    for ans in expected_answers:
        if ans.lower() in combined:
            return True
    return False


def check_qa_accuracy(llm_answer_text: str, expected_answers: list[str]) -> bool:
    """
    QA accuracy: does the LLM answer contain any expected answer
    (case-insensitive substring match)?
    """
    answer_lower = llm_answer_text.lower()
    for ans in expected_answers:
        if ans.lower() in answer_lower:
            return True
    return False


# ---------------------------------------------------------------------------
# Main benchmark runner
# ---------------------------------------------------------------------------

def run_example(
    split_name: str,
    example_idx: int,
    context: str,
    questions: list[str],
    answers: list[list[str]],
    max_questions: int | None = None,
    skip_llm: bool = False,
    chunk_size: int = CHUNK_SIZE,
) -> dict:
    """
    Run the benchmark on a single example.
    Returns a results dict with per-question details and aggregate metrics.
    """
    log(f"=== {split_name} / Example {example_idx} ===")
    log(f"  Context: {len(context)} chars, {len(questions)} questions")

    # Chunk the context
    chunks = chunk_context(context, chunk_size=chunk_size)
    log(f"  Chunked into {len(chunks)} pieces (~{chunk_size} chars each)")

    # Create a temporary vault
    vault_dir = tempfile.mkdtemp(prefix=f"same_bench_{split_name}_{example_idx}_")
    log(f"  Vault: {vault_dir}")

    results = {
        "split": split_name,
        "example_idx": example_idx,
        "context_length": len(context),
        "num_chunks": len(chunks),
        "num_questions": len(questions),
        "vault_dir": vault_dir,
        "questions": [],
        "retrieval_recall_at_k": 0.0,
        "qa_accuracy": 0.0,
        "errors": [],
    }

    # Initialize vault
    if not create_vault(vault_dir, chunks):
        results["errors"].append("Vault creation failed")
        log("  FAILED: Could not create vault. Skipping example.")
        return results

    # Determine how many questions to run
    n_questions = len(questions)
    if max_questions is not None:
        n_questions = min(n_questions, max_questions)
    log(f"  Running {n_questions} questions (skip_llm={skip_llm})")

    retrieval_hits = 0
    qa_hits = 0

    for qi in range(n_questions):
        q = questions[qi]
        expected = answers[qi]
        q_result = {
            "question_idx": qi,
            "question": q,
            "expected_answers": expected,
            "retrieved_texts": [],
            "retrieval_hit": False,
            "llm_answer": None,
            "qa_hit": False,
        }

        # Search
        retrieved = same_search(vault_dir, q)
        q_result["retrieved_texts"] = retrieved

        # Retrieval recall
        hit = check_retrieval_recall(retrieved, expected)
        q_result["retrieval_hit"] = hit
        if hit:
            retrieval_hits += 1

        # QA (optional)
        if not skip_llm and retrieved:
            answer = llm_answer(q, retrieved)
            q_result["llm_answer"] = answer
            qa_hit = check_qa_accuracy(answer, expected)
            q_result["qa_hit"] = qa_hit
            if qa_hit:
                qa_hits += 1

        status = "R" if hit else "."
        if not skip_llm and q_result["llm_answer"]:
            status += "A" if q_result["qa_hit"] else "."
        log(f"  [{status}] Q{qi}: {q[:60]}... -> {expected}")

        results["questions"].append(q_result)

    # Aggregate metrics
    results["retrieval_recall_at_k"] = retrieval_hits / n_questions if n_questions > 0 else 0.0
    results["qa_accuracy"] = qa_hits / n_questions if n_questions > 0 else 0.0
    results["retrieval_hits"] = retrieval_hits
    results["qa_hits"] = qa_hits
    results["questions_evaluated"] = n_questions

    log(f"  Retrieval Recall@{SEARCH_TOP_K}: {results['retrieval_recall_at_k']:.1%} ({retrieval_hits}/{n_questions})")
    if not skip_llm:
        log(f"  QA Accuracy: {results['qa_accuracy']:.1%} ({qa_hits}/{n_questions})")

    # Cleanup vault
    try:
        shutil.rmtree(vault_dir)
        log(f"  Cleaned up {vault_dir}")
    except Exception as e:
        log(f"  Warning: cleanup failed: {e}")

    return results


def run_benchmark(
    split_name: str,
    example_indices: list[int] | None = None,
    max_questions: int | None = None,
    skip_llm: bool = False,
    dry_run: bool = False,
    chunk_size: int = CHUNK_SIZE,
) -> dict:
    """
    Run the benchmark on the specified split and examples.
    Returns aggregate results.
    """
    from datasets import load_dataset

    log("Loading MemoryAgentBench dataset...")
    ds = load_dataset("ai-hyz/MemoryAgentBench")

    if split_name not in ds:
        log(f"ERROR: Split '{split_name}' not found. Available: {list(ds.keys())}")
        sys.exit(1)

    split = ds[split_name]
    log(f"Split '{split_name}': {len(split)} examples")

    # Determine which examples to run
    if dry_run:
        example_indices = [0]
        max_questions = 5
        log("DRY RUN: 5 questions from example 0")
    elif example_indices is None:
        example_indices = list(range(len(split)))

    # Validate indices
    for idx in example_indices:
        if idx >= len(split):
            log(f"ERROR: Example index {idx} out of range (split has {len(split)} examples)")
            sys.exit(1)

    all_results = {
        "benchmark": "MemoryAgentBench",
        "adapter": "SAME",
        "same_binary": SAME_BIN,
        "ollama_model": OLLAMA_MODEL,
        "chunk_size": chunk_size,
        "search_top_k": SEARCH_TOP_K,
        "split": split_name,
        "timestamp": datetime.now().isoformat(),
        "dry_run": dry_run,
        "examples": [],
        "aggregate": {},
    }

    total_retrieval_hits = 0
    total_qa_hits = 0
    total_questions = 0

    for idx in example_indices:
        example = split[idx]
        result = run_example(
            split_name=split_name,
            example_idx=idx,
            context=example["context"],
            questions=example["questions"],
            answers=example["answers"],
            max_questions=max_questions,
            skip_llm=skip_llm,
            chunk_size=chunk_size,
        )
        all_results["examples"].append(result)
        total_retrieval_hits += result.get("retrieval_hits", 0)
        total_qa_hits += result.get("qa_hits", 0)
        total_questions += result.get("questions_evaluated", 0)

    # Aggregate
    all_results["aggregate"] = {
        "total_questions": total_questions,
        "retrieval_recall_at_k": total_retrieval_hits / total_questions if total_questions > 0 else 0.0,
        "qa_accuracy": total_qa_hits / total_questions if total_questions > 0 else 0.0,
        "retrieval_hits": total_retrieval_hits,
        "qa_hits": total_qa_hits,
    }

    log("=" * 60)
    log(f"AGGREGATE RESULTS ({split_name})")
    log(f"  Total questions: {total_questions}")
    log(f"  Retrieval Recall@{SEARCH_TOP_K}: {all_results['aggregate']['retrieval_recall_at_k']:.1%}")
    if not skip_llm:
        log(f"  QA Accuracy: {all_results['aggregate']['qa_accuracy']:.1%}")
    log("=" * 60)

    return all_results


def save_results(results: dict):
    """Save results to JSON file."""
    RESULTS_DIR.mkdir(parents=True, exist_ok=True)
    ts = datetime.now().strftime("%Y%m%d_%H%M%S")
    split = results.get("split", "unknown")
    dry = "_dryrun" if results.get("dry_run") else ""
    filename = f"{split}{dry}_{ts}.json"
    filepath = RESULTS_DIR / filename

    with open(filepath, "w") as f:
        json.dump(results, f, indent=2, default=str)

    log(f"Results saved to {filepath}")
    return filepath


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def main():
    parser = argparse.ArgumentParser(
        description="MemoryAgentBench adapter for SAME",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=__doc__,
    )
    parser.add_argument(
        "--split",
        default="Conflict_Resolution",
        choices=SPLIT_NAMES,
        help="Dataset split to evaluate (default: Conflict_Resolution)",
    )
    parser.add_argument(
        "--examples",
        type=int,
        nargs="+",
        default=None,
        help="Example indices to run (default: all in split)",
    )
    parser.add_argument(
        "--max-questions",
        type=int,
        default=None,
        help="Max questions per example (default: all)",
    )
    parser.add_argument(
        "--skip-llm",
        action="store_true",
        help="Skip LLM answering, only measure retrieval recall",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Quick test: 5 questions from CR example 0",
    )
    parser.add_argument(
        "--chunk-size",
        type=int,
        default=CHUNK_SIZE,
        help=f"Chunk size in characters (default: {CHUNK_SIZE})",
    )

    args = parser.parse_args()

    # Apply chunk size override
    chunk_size_override = args.chunk_size

    # Check prerequisites
    if not os.path.isfile(SAME_BIN):
        log(f"ERROR: SAME binary not found at {SAME_BIN}")
        sys.exit(1)

    # Check Ollama is running (unless skip_llm)
    if not args.skip_llm:
        try:
            subprocess.run(
                ["ollama", "list"],
                capture_output=True,
                text=True,
                timeout=10,
            )
        except (FileNotFoundError, subprocess.TimeoutExpired):
            log("WARNING: Ollama not available. Use --skip-llm for retrieval-only eval.")

    results = run_benchmark(
        split_name=args.split,
        example_indices=args.examples,
        max_questions=args.max_questions,
        skip_llm=args.skip_llm,
        dry_run=args.dry_run,
        chunk_size=chunk_size_override,
    )

    save_results(results)


if __name__ == "__main__":
    main()
