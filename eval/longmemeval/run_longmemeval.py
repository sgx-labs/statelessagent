#!/usr/bin/env python3
"""
LongMemEval Benchmark Adapter for SAME (Stateless Agent Memory Engine)

Evaluates SAME's long-term memory retrieval against the LongMemEval benchmark
(ICLR 2025, arXiv:2410.10813). Dataset: xiaowu0162/longmemeval-cleaned on
HuggingFace.

LongMemEval tests five memory abilities across multi-session chat histories:
  - Information extraction (single-session)
  - Multi-session reasoning
  - Temporal reasoning
  - Knowledge updates
  - Abstention (knowing when you don't know)

Usage:
    # Verify oracle data isolation (always run first)
    python run_longmemeval.py --verify-no-leakage

    # Dry run: 5 questions
    python run_longmemeval.py --dry-run

    # Quick smoke test
    python run_longmemeval.py --max-questions 3 --skip-llm

    # Full LongMemEval_S, retrieval only
    python run_longmemeval.py --variant s --skip-llm

    # Full LongMemEval_S with QA
    python run_longmemeval.py --variant s

    # Specific question types
    python run_longmemeval.py --variant s --question-types knowledge-update temporal-reasoning

    # Keep temp vaults for debugging
    python run_longmemeval.py --max-questions 5 --keep-vaults
"""

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.request
from datetime import datetime
from pathlib import Path

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

SAME_BIN = shutil.which("same") or "/usr/local/bin/same"
DEFAULT_EMBED_MODEL = "nomic-embed-text"
SEARCH_TOP_K = 5
QUESTION_TIMEOUT = 60      # seconds per same search / same ask call
REINDEX_TIMEOUT = 600       # seconds for same reindex (sessions can be large)
INIT_TIMEOUT = 120          # seconds for same init
RESULTS_DIR = Path(__file__).parent / "results"

DATASET_NAME = "xiaowu0162/longmemeval-cleaned"

VARIANT_FILES = {
    "s": "longmemeval_s_cleaned.json",
    "m": "longmemeval_m_cleaned.json",
    "oracle": "longmemeval_oracle.json",
}

# LongMemEval question types
QUESTION_TYPES = [
    "single-session-user",
    "single-session-assistant",
    "single-session-preference",
    "multi-session",
    "temporal-reasoning",
    "knowledge-update",
]

# Methodology declaration embedded in every results JSON
METHODOLOGY = {
    "version": 1,
    "constraints": [
        "SAME parameters were not tuned for this benchmark (default config from same init --yes)",
        "Sessions converted to plain markdown with title and date only (no SAME-specific metadata)",
        "Default embedding model and retrieval settings used (no re-ranking, no query expansion)",
        "No oracle leakage (has_answer stripped from turns, answer/answer_session_ids used only for scoring)",
        "All questions reported, no filtering (errors count against the score)",
    ],
    "config_changes": "Only change: enabling embedding provider (ollama) if init did not auto-detect it",
}

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def log(msg: str):
    """Timestamped console logging."""
    ts = datetime.now().strftime("%H:%M:%S")
    print(f"[{ts}] {msg}", flush=True)


def get_ollama_env() -> dict:
    """Build environment with OLLAMA_HOST set if OLLAMA_URL is configured."""
    env = os.environ.copy()
    ollama_url = os.environ.get("OLLAMA_URL")
    if ollama_url:
        env["OLLAMA_HOST"] = ollama_url
    return env


def session_to_markdown(session: list[dict], date: str, index: int) -> str:
    """
    Convert a single chat session (list of turns) into a markdown note
    with minimal YAML frontmatter.

    Methodology constraint: only title and date in frontmatter.
    No tags, no domain, no session_id, no SAME-specific metadata.
    The has_answer field is stripped from turns (no oracle leakage).
    """
    # Build a title from the first user message (truncated)
    title = f"Session {index}"
    for turn in session:
        if turn.get("role") == "user":
            content = turn.get("content", "").strip()
            if content:
                # Use first 80 chars of first user message as title
                clean = content[:80].replace("\n", " ").replace('"', "'").strip()
                if len(content) > 80:
                    clean += "..."
                title = clean
                break

    # Normalize date: "2023/05/20 (Sat) 02:21" -> "2023-05-20"
    date_clean = date
    date_match = re.match(r"(\d{4})/(\d{2})/(\d{2})", date)
    if date_match:
        date_clean = f"{date_match.group(1)}-{date_match.group(2)}-{date_match.group(3)}"

    lines = []
    # Minimal YAML frontmatter: title and date only
    lines.append("---")
    lines.append(f'title: "{title}"')
    lines.append(f"date: {date_clean}")
    lines.append("---")
    lines.append("")

    # Conversation body (has_answer is intentionally NOT included)
    for turn in session:
        role = turn.get("role", "unknown")
        content = turn.get("content", "").strip()
        if role == "user":
            lines.append(f"**User:** {content}")
        elif role == "assistant":
            lines.append(f"**Assistant:** {content}")
        else:
            lines.append(f"**{role}:** {content}")
        lines.append("")

    return "\n".join(lines)


def verify_no_leakage(items: list[dict]) -> bool:
    """
    Structural verification that oracle data is properly isolated.
    Checks that:
    1. has_answer fields exist in the raw data (confirming dataset has them)
    2. session_to_markdown strips has_answer from output
    3. Frontmatter contains only title and date
    4. No SAME-specific metadata is injected
    Returns True if all checks pass.
    """
    log("Verifying oracle data isolation...")
    passed = True

    # Check 1: has_answer exists in raw data (search more broadly)
    has_answer_found = False
    for item in items[:50]:
        for session in item.get("haystack_sessions", []):
            for turn in session:
                if "has_answer" in turn:
                    has_answer_found = True
                    break
            if has_answer_found:
                break
        if has_answer_found:
            break

    if has_answer_found:
        log("  [PASS] has_answer fields found in raw dataset (confirming dataset includes oracle markers)")
    else:
        log("  [WARN] has_answer fields not found in raw dataset (dataset may not include oracle markers)")

    # Check 2: session_to_markdown strips has_answer
    test_session = [
        {"role": "user", "content": "Hello", "has_answer": True},
        {"role": "assistant", "content": "Hi there", "has_answer": False},
    ]
    md = session_to_markdown(test_session, "2024-01-15", 0)
    if "has_answer" in md:
        log("  [FAIL] has_answer leaked into generated markdown")
        passed = False
    else:
        log("  [PASS] has_answer stripped from generated markdown")

    # Check 3: Frontmatter contains only title and date
    frontmatter_lines = []
    in_frontmatter = False
    for line in md.split("\n"):
        if line.strip() == "---":
            if in_frontmatter:
                break
            in_frontmatter = True
            continue
        if in_frontmatter:
            frontmatter_lines.append(line)

    allowed_prefixes = ("title:", "date:")
    for fm_line in frontmatter_lines:
        fm_line = fm_line.strip()
        if fm_line and not any(fm_line.startswith(p) for p in allowed_prefixes):
            log(f"  [FAIL] Unexpected frontmatter field: {fm_line}")
            passed = False

    if passed:
        log("  [PASS] Frontmatter contains only title and date")

    # Check 4: No SAME-specific metadata
    same_metadata_terms = ["tags:", "domain:", "workstream:", "content_type:", "confidence:", "trust_state:", "session_id:"]
    for term in same_metadata_terms:
        if term in md:
            log(f"  [FAIL] SAME-specific metadata found in markdown: {term}")
            passed = False

    if passed:
        log("  [PASS] No SAME-specific metadata injected")

    # Check 5: Oracle fields exist in dataset
    sample = items[0]
    if "answer" in sample:
        log("  [PASS] Oracle 'answer' field exists in dataset (used only for scoring)")
    else:
        log("  [WARN] Oracle 'answer' field not found in dataset")

    if "answer_session_ids" in sample:
        log("  [PASS] Oracle 'answer_session_ids' field exists in dataset (used only for scoring)")
    else:
        log("  [WARN] Oracle 'answer_session_ids' field not found in dataset")

    if passed:
        log("All integrity checks passed.")
    else:
        log("INTEGRITY CHECK FAILED. Fix the issues above before running the benchmark.")

    return passed


def create_vault(vault_dir: str, sessions_md: list[tuple[str, str]], embed_model: str) -> bool:
    """
    Initialize a SAME vault in vault_dir and write session markdown files.
    Returns True on success.

    sessions_md: list of (filename, content) tuples
    """
    env = get_ollama_env()

    # Clear any stale init lockfile from previous runs
    lockfile = os.path.expanduser("~/.config/same/init.lock")
    if os.path.exists(lockfile):
        try:
            os.remove(lockfile)
            log(f"  Cleared stale lockfile: {lockfile}")
        except OSError:
            pass

    os.makedirs(vault_dir, exist_ok=True)

    # Write session markdown files
    sessions_dir = os.path.join(vault_dir, "sessions")
    os.makedirs(sessions_dir, exist_ok=True)
    for filename, content in sessions_md:
        filepath = os.path.join(sessions_dir, filename)
        with open(filepath, "w") as f:
            f.write(content)

    # Step 1: Run same init --yes (default config, NO parameter tuning)
    log(f"  Initializing vault ({len(sessions_md)} sessions)...")
    t0 = time.time()
    result = subprocess.run(
        [SAME_BIN, "init", "--yes"],
        cwd=vault_dir,
        capture_output=True,
        text=True,
        timeout=INIT_TIMEOUT,
        env=env,
    )
    init_elapsed = time.time() - t0
    if result.returncode != 0:
        log(f"  ERROR: same init failed ({init_elapsed:.1f}s): {result.stderr[:300]}")
        return False

    # Step 2: Enable embedding provider if init didn't auto-detect Ollama.
    # This enables the feature; it does NOT tune any retrieval parameters.
    # All thresholds (distance_threshold, composite_threshold, etc.) remain
    # at their shipped defaults from same init --yes.
    config_path = os.path.join(vault_dir, ".same", "config.toml")

    # Read existing config to preserve defaults
    existing_config = ""
    if os.path.exists(config_path):
        with open(config_path, "r") as f:
            existing_config = f.read()

    # Only add embedding config if not already present
    if "[embedding]" not in existing_config:
        ollama_url = os.environ.get("OLLAMA_URL", "")
        embed_section = f'\n[embedding]\n  provider = "ollama"\n  model = "{embed_model}"\n'
        if ollama_url:
            embed_section += f'  url = "{ollama_url}"\n'
        with open(config_path, "a") as f:
            f.write(embed_section)
    else:
        # Embedding section exists; check if model matches
        if embed_model not in existing_config:
            # Replace model in existing config
            lines = existing_config.split("\n")
            new_lines = []
            for line in lines:
                if line.strip().startswith("model") and "[embedding]" in "\n".join(new_lines[-5:]):
                    new_lines.append(f'  model = "{embed_model}"')
                else:
                    new_lines.append(line)
            with open(config_path, "w") as f:
                f.write("\n".join(new_lines))

    # Step 3: Force reindex with semantic search
    log(f"  Reindexing with {embed_model}...")
    t1 = time.time()
    reindex_result = subprocess.run(
        [SAME_BIN, "reindex", "--force"],
        cwd=vault_dir,
        capture_output=True,
        text=True,
        timeout=REINDEX_TIMEOUT,
        env=env,
    )
    reindex_elapsed = time.time() - t1

    if reindex_result.returncode != 0:
        log(f"  ERROR: same reindex failed ({reindex_elapsed:.1f}s): {reindex_result.stderr[:300]}")
        return False

    # Report search mode
    combined_output = (reindex_result.stdout + reindex_result.stderr).lower()
    if "semantic" in combined_output:
        log(f"  Search mode: semantic (embeddings active)")
    elif "keyword" in combined_output:
        log(f"  WARNING: Search mode: keyword-only (embeddings NOT active)")

    log(f"  Vault ready in {init_elapsed + reindex_elapsed:.1f}s (init: {init_elapsed:.1f}s, reindex: {reindex_elapsed:.1f}s)")
    return True


def same_search(vault_dir: str, query: str, top_k: int = SEARCH_TOP_K) -> list[dict]:
    """
    Run `same search --json --top-k 5` and return the top-k results as dicts.
    Each result has at least: path, title, snippet, score.
    Uses default thresholds (no tuning).
    """
    env = get_ollama_env()
    try:
        result = subprocess.run(
            [SAME_BIN, "search", "--json", "--top-k", str(top_k), query],
            cwd=vault_dir,
            capture_output=True,
            text=True,
            timeout=QUESTION_TIMEOUT,
            env=env,
        )
    except subprocess.TimeoutExpired:
        log(f"    TIMEOUT: same search for '{query[:50]}...'")
        return []

    if result.returncode != 0:
        return []

    try:
        data = json.loads(result.stdout)
    except json.JSONDecodeError:
        return []

    # Normalize to list of dicts
    if isinstance(data, list):
        return data[:top_k]
    elif isinstance(data, dict):
        results = data.get("results") or data.get("matches") or data.get("notes") or []
        return results[:top_k]
    return []


def same_ask(vault_dir: str, question: str) -> str:
    """
    Run `same ask` and capture the LLM-generated answer.
    Returns the answer text, or empty string on failure.
    """
    env = get_ollama_env()
    try:
        result = subprocess.run(
            [SAME_BIN, "ask", question],
            cwd=vault_dir,
            capture_output=True,
            text=True,
            timeout=QUESTION_TIMEOUT * 2,  # ask takes longer (search + LLM)
            env=env,
        )
    except subprocess.TimeoutExpired:
        log(f"    TIMEOUT: same ask for '{question[:50]}...'")
        return ""

    if result.returncode != 0:
        return ""

    # Parse the output: same ask prints the answer between "Answer" and "Sources" lines
    output = result.stdout
    answer_lines = []
    in_answer = False
    for line in output.split("\n"):
        stripped = line.strip()
        if "Answer" in stripped and "---" in stripped:
            in_answer = True
            continue
        if "Sources" in stripped and "---" in stripped:
            in_answer = False
            continue
        if in_answer and stripped:
            answer_lines.append(stripped)

    return " ".join(answer_lines).strip()


def check_retrieval_hit(retrieved_snippets: list[str], expected_answer: str) -> bool:
    """
    Retrieval hit: does the expected answer appear (case-insensitive substring)
    in any of the retrieved snippets?
    """
    if not expected_answer:
        return False
    combined = " ".join(retrieved_snippets).lower()
    return expected_answer.lower().strip("'\"") in combined


def compute_retrieval_metrics(
    retrieved_paths: list[str],
    answer_session_ids: list[str],
    all_session_ids: list[str],
) -> dict:
    """
    Compute session-level retrieval metrics given retrieved paths and oracle
    evidence session IDs.

    Maps retrieved file paths back to session IDs for comparison.
    Returns dict with recall@1, recall@5, MRR.
    """
    # The retrieved paths are relative to vault, e.g., "sessions/session_0042.md"
    # We need to map session index back to session_id
    retrieved_indices = []
    for p in retrieved_paths:
        basename = os.path.basename(p)
        if basename.startswith("session_") and basename.endswith(".md"):
            try:
                idx_str = basename.replace("session_", "").replace(".md", "")
                idx = int(idx_str)
                retrieved_indices.append(idx)
            except ValueError:
                pass

    # Map answer_session_ids to their indices in all_session_ids
    oracle_indices = set()
    for sid in answer_session_ids:
        if sid in all_session_ids:
            oracle_indices.add(all_session_ids.index(sid))

    if not oracle_indices:
        return {"recall_at_1": 0.0, "recall_at_5": 0.0, "mrr": 0.0}

    # Recall@K: fraction of oracle sessions found in top-K retrieved
    retrieved_set = set(retrieved_indices)
    recall_at_1 = 1.0 if retrieved_indices and retrieved_indices[0] in oracle_indices else 0.0

    found_in_5 = len(oracle_indices & retrieved_set)
    recall_at_5 = found_in_5 / len(oracle_indices)

    # MRR: reciprocal rank of first relevant result (in retrieval order)
    mrr = 0.0
    for rank, idx in enumerate(retrieved_indices, 1):
        if idx in oracle_indices:
            mrr = 1.0 / rank
            break

    return {"recall_at_1": recall_at_1, "recall_at_5": recall_at_5, "mrr": mrr}


# ---------------------------------------------------------------------------
# Main benchmark runner
# ---------------------------------------------------------------------------

def load_dataset_items(variant: str = "s", limit: int | None = None,
                       question_types: list[str] | None = None) -> list[dict]:
    """
    Load LongMemEval from HuggingFace and return as a list of dicts.

    Downloads the specific JSON file directly via urllib to avoid requiring
    the datasets library (which tries to generate all splits -- the M split
    is 2.7GB and causes int32 overflow in pyarrow).
    """
    filename = VARIANT_FILES.get(variant)
    if not filename:
        log(f"ERROR: Unknown variant '{variant}'. Choose from: {list(VARIANT_FILES.keys())}")
        sys.exit(1)

    cache_dir = Path(__file__).parent / "data"
    cache_dir.mkdir(exist_ok=True)
    local_path = cache_dir / filename

    if not local_path.exists():
        url = f"https://huggingface.co/datasets/{DATASET_NAME}/resolve/main/{filename}"
        log(f"Downloading {filename} from HuggingFace...")
        try:
            urllib.request.urlretrieve(url, str(local_path))
            log(f"  Saved to {local_path}")
        except Exception as e:
            log(f"ERROR: Failed to download dataset: {e}")
            log(f"  Manual download: {url}")
            sys.exit(1)
    else:
        log(f"Using cached dataset: {local_path}")

    log(f"Loading JSON from {local_path}...")
    with open(local_path, "r") as f:
        data = json.load(f)

    log(f"Loaded {len(data)} questions from {filename}")

    # Filter by question types if specified
    if question_types:
        data = [item for item in data if item.get("question_type") in question_types]
        log(f"Filtered to {len(data)} questions matching types: {question_types}")

    # Apply limit
    if limit is not None:
        data = data[:limit]

    return data


def group_by_haystack(items: list[dict]) -> dict[str, list[dict]]:
    """
    Group questions by their haystack (set of session IDs).
    Questions sharing the same haystack can reuse the same vault.

    Returns: {haystack_key: [items...]}
    """
    groups: dict[str, list[dict]] = {}
    for item in items:
        # Create a hashable key from session IDs
        session_ids = item.get("haystack_session_ids", [])
        key = "|".join(sorted(session_ids)) if session_ids else item["question_id"]
        if key not in groups:
            groups[key] = []
        groups[key].append(item)

    return groups


def run_question(
    item: dict,
    vault_dir: str,
    all_session_ids: list[str],
    skip_llm: bool = False,
) -> dict:
    """
    Run a single LongMemEval question against an already-initialized vault.
    Returns a result dict.
    """
    question_id = item["question_id"]
    question = item["question"]
    answer = item.get("answer", "")
    question_type = item.get("question_type", "unknown")
    answer_session_ids = item.get("answer_session_ids", [])
    is_abstention = question_id.endswith("_abs")

    result = {
        "question_id": question_id,
        "question_type": question_type,
        "question": question,
        "expected_answer": answer,
        "is_abstention": is_abstention,
        "hypothesis": "",
        "retrieved_paths": [],
        "retrieval_hit": False,
        "retrieval_metrics": {},
    }

    # Search (default thresholds, no tuning)
    search_results = same_search(vault_dir, question)
    retrieved_paths = [r.get("path", "") for r in search_results if isinstance(r, dict)]
    retrieved_snippets = [
        r.get("snippet", "") or r.get("content", "") or r.get("text", "")
        for r in search_results if isinstance(r, dict)
    ]
    result["retrieved_paths"] = retrieved_paths

    # Retrieval hit: does the answer appear in any retrieved snippet?
    result["retrieval_hit"] = check_retrieval_hit(retrieved_snippets, answer)

    # Session-level retrieval metrics (if oracle evidence available)
    if answer_session_ids and all_session_ids:
        result["retrieval_metrics"] = compute_retrieval_metrics(
            retrieved_paths, answer_session_ids, all_session_ids
        )

    # Generate answer (scoring only -- oracle answer used AFTER retrieval)
    if not skip_llm:
        hypothesis = same_ask(vault_dir, question)
        result["hypothesis"] = hypothesis
    else:
        # For retrieval-only mode, concatenate retrieved snippets as the "hypothesis"
        result["hypothesis"] = " ".join(retrieved_snippets[:3]).strip()

    # Log status
    r_metrics = result.get("retrieval_metrics", {})
    recall5 = r_metrics.get("recall_at_5", -1)
    hit_str = "HIT" if result["retrieval_hit"] else "miss"
    recall_str = f"R@5={recall5:.0%}" if recall5 >= 0 else "no-oracle"
    log(f"  [{hit_str}|{recall_str}] {question_type}: {question[:55]}...")

    return result


def run_benchmark(
    items: list[dict],
    variant: str = "s",
    skip_llm: bool = False,
    keep_vaults: bool = False,
    embed_model: str = DEFAULT_EMBED_MODEL,
    dry_run: bool = False,
) -> dict:
    """
    Run the LongMemEval benchmark on the given items.
    Groups questions by haystack to reuse vaults where possible.
    Returns aggregate results dict.
    """
    if dry_run:
        # In dry-run mode, limit to 5 questions
        items = items[:5]
        log("DRY RUN: showing what would be tested (5 questions)")
        log(f"  Total questions: {len(items)}")
        groups = group_by_haystack(items)
        log(f"  Unique haystacks: {len(groups)}")
        for i, (key, group_items) in enumerate(groups.items()):
            sample = group_items[0]
            n_sessions = len(sample.get("haystack_sessions", []))
            types = set(it.get("question_type", "?") for it in group_items)
            log(f"  Haystack {i}: {len(group_items)} questions, {n_sessions} sessions, types: {types}")
            if i >= 9:
                log(f"  ... and {len(groups) - 10} more haystacks")
                break
        return {"dry_run": True, "total_questions": len(items), "unique_haystacks": len(groups)}

    all_results = {
        "benchmark": "LongMemEval",
        "dataset": DATASET_NAME,
        "variant": variant,
        "variant_file": VARIANT_FILES[variant],
        "adapter": "SAME",
        "same_binary": SAME_BIN,
        "embed_model": embed_model,
        "search_top_k": SEARCH_TOP_K,
        "timestamp": datetime.now().isoformat(),
        "methodology": METHODOLOGY,
        "total_questions": len(items),
        "questions": [],
        "by_type": {},
        "aggregate": {},
    }

    groups = group_by_haystack(items)
    log(f"Questions: {len(items)}, Unique haystacks: {len(groups)}")

    completed = 0
    total = len(items)

    for haystack_idx, (haystack_key, group_items) in enumerate(groups.items()):
        sample = group_items[0]  # All items in group share the same haystack
        sessions = sample.get("haystack_sessions", [])
        dates = sample.get("haystack_dates", [])
        session_ids = sample.get("haystack_session_ids", [])

        log(f"=== Haystack {haystack_idx + 1}/{len(groups)} ({len(group_items)} questions, {len(sessions)} sessions) ===")

        # Convert sessions to plain markdown (title + date only, no SAME metadata)
        sessions_md = []
        for i, session in enumerate(sessions):
            date = dates[i] if i < len(dates) else "unknown"
            filename = f"session_{i:04d}.md"
            content = session_to_markdown(session, date, i)
            sessions_md.append((filename, content))

        # Create temp vault
        vault_dir = tempfile.mkdtemp(prefix=f"same_longmemeval_{haystack_idx}_")
        log(f"  Vault: {vault_dir}")

        vault_ok = create_vault(vault_dir, sessions_md, embed_model)

        if not vault_ok:
            # Errors count against the score (methodology constraint 5)
            log(f"  FAILED: Could not create vault. Skipping {len(group_items)} questions.")
            for item in group_items:
                err_result = {
                    "question_id": item["question_id"],
                    "question_type": item.get("question_type", "unknown"),
                    "question": item["question"],
                    "expected_answer": item.get("answer", ""),
                    "is_abstention": item["question_id"].endswith("_abs"),
                    "hypothesis": "",
                    "retrieved_paths": [],
                    "retrieval_hit": False,
                    "retrieval_metrics": {},
                    "error": "vault_creation_failed",
                }
                all_results["questions"].append(err_result)
                completed += 1
            continue

        # Run each question against this vault
        for item in group_items:
            q_result = run_question(item, vault_dir, session_ids, skip_llm=skip_llm)
            all_results["questions"].append(q_result)
            completed += 1
            if completed % 10 == 0:
                log(f"  Progress: {completed}/{total} questions")

        # Cleanup
        if not keep_vaults:
            try:
                shutil.rmtree(vault_dir)
            except Exception as e:
                log(f"  Warning: cleanup failed: {e}")
        else:
            log(f"  Kept vault at {vault_dir}")

    # Aggregate metrics
    _compute_aggregates(all_results)

    return all_results


def _compute_aggregates(results: dict):
    """Compute aggregate and per-type metrics from question results."""
    by_type: dict[str, dict] = {}
    all_recall5 = []
    all_recall1 = []
    all_mrr = []
    all_hits = []

    for q in results["questions"]:
        qtype = q.get("question_type", "unknown")
        if qtype not in by_type:
            by_type[qtype] = {
                "recall_at_1": [], "recall_at_5": [], "mrr": [],
                "retrieval_hits": [], "count": 0,
            }

        by_type[qtype]["count"] += 1
        by_type[qtype]["retrieval_hits"].append(1 if q.get("retrieval_hit") else 0)
        all_hits.append(1 if q.get("retrieval_hit") else 0)

        metrics = q.get("retrieval_metrics", {})
        if metrics:
            r1 = metrics.get("recall_at_1", 0.0)
            r5 = metrics.get("recall_at_5", 0.0)
            mrr = metrics.get("mrr", 0.0)
            by_type[qtype]["recall_at_1"].append(r1)
            by_type[qtype]["recall_at_5"].append(r5)
            by_type[qtype]["mrr"].append(mrr)
            all_recall1.append(r1)
            all_recall5.append(r5)
            all_mrr.append(mrr)

    # Per-type summary
    type_summary = {}
    for qtype, data in by_type.items():
        n_session = len(data["recall_at_5"])
        n_total = data["count"]
        type_summary[qtype] = {
            "count": n_total,
            "retrieval_hit_rate": sum(data["retrieval_hits"]) / n_total if n_total > 0 else 0.0,
            "evaluated_session_level": n_session,
            "recall_at_1": sum(data["recall_at_1"]) / n_session if n_session > 0 else 0.0,
            "recall_at_5": sum(data["recall_at_5"]) / n_session if n_session > 0 else 0.0,
            "mrr": sum(data["mrr"]) / n_session if n_session > 0 else 0.0,
        }
    results["by_type"] = type_summary

    # Overall
    n_total = results["total_questions"]
    n_session = len(all_recall5)
    results["aggregate"] = {
        "total_questions": n_total,
        "retrieval_hit_rate": sum(all_hits) / n_total if n_total > 0 else 0.0,
        "evaluated_session_level": n_session,
        "recall_at_1": sum(all_recall1) / n_session if n_session > 0 else 0.0,
        "recall_at_5": sum(all_recall5) / n_session if n_session > 0 else 0.0,
        "mrr": sum(all_mrr) / n_session if n_session > 0 else 0.0,
    }


def save_results(results: dict, variant: str = "s") -> tuple[Path | None, Path | None]:
    """
    Save results in two formats:
    1. predictions JSONL (for official LongMemEval eval script)
    2. full results JSON (detailed, with embedded methodology)

    Returns (predictions_path, results_path).
    """
    RESULTS_DIR.mkdir(parents=True, exist_ok=True)
    ts = datetime.now().strftime("%Y%m%d_%H%M%S")

    if results.get("dry_run"):
        return None, None

    # 1. Predictions JSONL (official format: question_id + hypothesis)
    pred_file = RESULTS_DIR / f"longmemeval_{variant}_predictions_{ts}.jsonl"
    with open(pred_file, "w") as f:
        for q in results.get("questions", []):
            line = {
                "question_id": q["question_id"],
                "hypothesis": q.get("hypothesis", ""),
            }
            f.write(json.dumps(line) + "\n")
    log(f"Predictions saved to {pred_file}")

    # 2. Full results JSON (with methodology declaration)
    results_file = RESULTS_DIR / f"longmemeval_{variant}_{ts}.json"
    with open(results_file, "w") as f:
        json.dump(results, f, indent=2, default=str)
    log(f"Full results saved to {results_file}")

    return pred_file, results_file


def print_summary(results: dict):
    """Print a human-readable summary of benchmark results."""
    if results.get("dry_run"):
        return

    print("\n" + "=" * 70)
    print("LONGMEMEVAL RESULTS SUMMARY")
    print("=" * 70)

    agg = results.get("aggregate", {})
    print(f"\n  Total questions:        {agg.get('total_questions', 0)}")
    print(f"  Retrieval hit rate:     {agg.get('retrieval_hit_rate', 0):.1%}")
    print(f"  Session-level eval:     {agg.get('evaluated_session_level', 0)}")
    print(f"  Session Recall@1:       {agg.get('recall_at_1', 0):.1%}")
    print(f"  Session Recall@5:       {agg.get('recall_at_5', 0):.1%}")
    print(f"  MRR:                    {agg.get('mrr', 0):.1%}")

    by_type = results.get("by_type", {})
    if by_type:
        print(f"\n  {'Type':<30} {'Count':>6} {'Hit%':>8} {'R@1':>8} {'R@5':>8} {'MRR':>8}")
        print("  " + "-" * 70)
        for qtype in QUESTION_TYPES:
            if qtype in by_type:
                t = by_type[qtype]
                print(f"  {qtype:<30} {t['count']:>6} {t['retrieval_hit_rate']:>7.1%} "
                      f"{t['recall_at_1']:>7.1%} {t['recall_at_5']:>7.1%} {t['mrr']:>7.1%}")
        # Any types not in the predefined list
        for qtype, t in by_type.items():
            if qtype not in QUESTION_TYPES:
                print(f"  {qtype:<30} {t['count']:>6} {t['retrieval_hit_rate']:>7.1%} "
                      f"{t['recall_at_1']:>7.1%} {t['recall_at_5']:>7.1%} {t['mrr']:>7.1%}")

    print("\n" + "=" * 70)
    print()


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def main():
    parser = argparse.ArgumentParser(
        description="LongMemEval benchmark adapter for SAME",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=__doc__,
    )
    parser.add_argument(
        "--variant",
        default="s",
        choices=list(VARIANT_FILES.keys()),
        help="Dataset variant: s (default), m, oracle",
    )
    parser.add_argument(
        "--question-types",
        nargs="+",
        choices=QUESTION_TYPES,
        default=None,
        help="Filter to specific question types",
    )
    parser.add_argument(
        "--max-questions",
        type=int,
        default=None,
        help="Limit number of questions evaluated (default: all)",
    )
    parser.add_argument(
        "--skip-llm",
        action="store_true",
        help="Retrieval recall only (skip LLM answering via same ask)",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Quick sanity check (5 questions)",
    )
    parser.add_argument(
        "--keep-vaults",
        action="store_true",
        help="Keep temp vaults after each haystack (for debugging)",
    )
    parser.add_argument(
        "--model",
        default=DEFAULT_EMBED_MODEL,
        help=f"Embedding model for Ollama (default: {DEFAULT_EMBED_MODEL})",
    )
    parser.add_argument(
        "--verify-no-leakage",
        action="store_true",
        help="Confirm oracle data isolation and exit",
    )

    args = parser.parse_args()

    # Check prerequisites
    if not os.path.isfile(SAME_BIN):
        log(f"ERROR: SAME binary not found at {SAME_BIN}")
        log("  Install SAME or set it on your PATH.")
        sys.exit(1)
    log(f"SAME binary: {SAME_BIN}")

    # Check Ollama (unless dry-run or verify-only)
    if not args.verify_no_leakage:
        env = get_ollama_env()
        try:
            result = subprocess.run(
                ["ollama", "list"],
                capture_output=True,
                text=True,
                timeout=10,
                env=env,
            )
            if result.returncode == 0:
                log("Ollama: available")
            else:
                log("WARNING: Ollama returned non-zero. Embeddings may fail.")
        except (FileNotFoundError, subprocess.TimeoutExpired):
            log("WARNING: Ollama not available. Embeddings will fail.")
            if not args.dry_run:
                log("  Start Ollama or set OLLAMA_URL env var.")
                sys.exit(1)

    # Load dataset
    items = load_dataset_items(
        variant=args.variant,
        limit=args.max_questions,
        question_types=args.question_types,
    )
    if not items:
        log("ERROR: No items loaded from dataset.")
        sys.exit(1)

    # Verify-only mode
    if args.verify_no_leakage:
        ok = verify_no_leakage(items)
        sys.exit(0 if ok else 1)

    # Run benchmark
    t_start = time.time()
    results = run_benchmark(
        items=items,
        variant=args.variant,
        skip_llm=args.skip_llm,
        keep_vaults=args.keep_vaults,
        embed_model=args.model,
        dry_run=args.dry_run,
    )
    elapsed = time.time() - t_start
    log(f"Total runtime: {elapsed:.0f}s ({elapsed / 60:.1f} min)")

    if not results.get("dry_run"):
        results["runtime_seconds"] = elapsed

    # Save and print
    save_results(results, variant=args.variant)
    print_summary(results)


if __name__ == "__main__":
    main()
