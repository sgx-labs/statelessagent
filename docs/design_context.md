# SAME Design Context — For Development Instances

This document provides architectural direction, design principles, settled decisions, and tuning rationale that exist outside the source code. It bridges the gap between "how the code works" (readable from the codebase) and "why it works this way and where it's going" (captured during design sessions).

**No PII, vault content, or personal data is included.**

---

## 1. The v2 Architecture Pivot

SAME is pivoting from a **retrieval/injection engine** to a **session continuity engine**. The tagline is: **"Compass, Not Search Engine."**

### Why the pivot

1. **Per-prompt injection is the wrong abstraction.** It guesses relevance with an embedding match (~46% hook precision on isolated prompts). The agent can read the user's actual intent and search intentionally — with follow-up searches, filtered queries, full note reads. The agent is a better searcher than the hook will ever be.

2. **Claude Code can search on its own.** It has Read/Grep/Glob tools plus the MCP tools (`search_notes`, `get_note`, `find_similar_notes`). Give the agent orientation and tools, get out of the way.

3. **Session continuity is the real value.** "Every AI session starts from zero. Not anymore." — that tagline is about continuity, not search. The magic moment is when a new session picks up where the last one left off without the user doing anything.

4. **The Stop hook is unreliable.** If you close the terminal window, the Stop hook never fires. Any architecture that depends on Stop for continuity is fragile. Session data should be processed when the NEXT session needs it, from data the IDE already persists.

5. **Claude Code already stores session transcripts.** `sessions-index.json` has session summaries, first prompts, message counts, timestamps. The data survives regardless of how the session ended.

6. **Industry convergence.** Graphiti, Letta, A-MEM, GAM, the MCP reference memory server — all moving to structured storage + agent-directed retrieval. None use always-on vector injection.

### v0.x vs v2 architecture

**v0.x (current):**
```
SessionStart       → staleness check (no context injection)
UserPromptSubmit   → embed prompt → KNN search → composite score → inject top 2
Stop               → regex decision extraction → handoff generation
MCP                → available but secondary to hooks
```

**v2 (target):**
```
SessionStart       → read previous session data → inject compact orientation → register instance
UserPromptSubmit   → lightweight or removed (gate chain suppresses ~80% already)
Stop               → best-effort session capture (value-add, not critical path)
MCP                → primary search interface, agent-driven
```

### Token economics

- v0.x: ~800 tokens per prompt from hooks × N prompts per session = ~40,000 tokens/session from hooks alone
- v2: ~200 tokens total (SessionStart orientation only)
- For a 50-prompt session: **40,000 → 200 tokens. 99.5% reduction.**

### What stays, what goes

**Keep:** Decision extraction, handoff generation, staleness detection, MCP server (all tools), CLI tooling, cross-platform builds, confidence scoring, file watcher.

**Cut (or gate):** Per-prompt context-surfacing injection. Agent searches via MCP when it needs context. The gate chain (already implemented) suppresses ~80% of injections as a transition step.

**New:** SessionStart orientation (deterministic previous-session injection), instance registration (multi-session awareness), session data reader (parse IDE session storage).

---

## 2. Core Design Principles

Eight principles. Each is testable, constraining, and non-obvious — meaning a reasonable person could argue against them.

### Principle 1: Ship Seeds, Not Products

The system grows through use. Don't build finished features; build starting points that agents extend through conversation.

**Test:** Does this feature work on day 1 AND get measurably better on day 30? If it only works on day 1, it's a product feature. If it gets better with use, it's a seed.

**Rules out:** Feature-complete releases. Extensive configuration UIs. "Finished" states.

### Principle 2: Teach, Don't Tell

The tool adapts to the user's demonstrated proficiency. First encounter: full context. Fifth encounter: one-liner. Expert: silent unless novel.

**Test:** Does this feature change its behavior based on observed usage? If it speaks the same way to a beginner and a veteran, it violates this principle.

**Rules out:** Static documentation. "Beginner mode" toggles. Tooltips that never go away.

### Principle 3: Serve Two Customers

Every feature serves the human AND the agent. Markdown is human-readable AND machine-parseable. Decisions answer "why did we do that?" for the developer AND "what was decided about X?" for the agent.

**Test:** Can a human read this artifact and get value? Can an agent parse it and act on it? If either answer is no, it's half a feature.

### Principle 4: Deposit Before You Withdraw

The system's primary value is what it captures, not what it retrieves. Decision extraction, handoff generation, research findings, proficiency signals — the write side. Search can be replaced by grep. Automated knowledge capture cannot.

**Test:** If you removed search entirely, would the system still be valuable for what it deposits?

### Principle 5: Every Session Compounds

Each interaction makes the next one better. Vault grows. Decisions accumulate. Research enriches. No session is wasted.

**Test:** After 100 sessions, is the system measurably more valuable than after 10?

### Principle 6: Privacy Is Structural

Private data can't leak because it's never in the shared directory. Not policy-based. Filesystem-level.

**Test:** Run `git push` on the entire repo. Does any private data leave the machine? If the answer depends on a config flag rather than a directory structure, the boundary is too weak.

### Principle 7: Survive the Crash

No critical path depends on graceful exits. SessionStart recovers from ungraceful Stop. The system degrades gracefully — every component assumes the previous component may have failed.

**Test:** Close the terminal window mid-sentence. On next SessionStart, does the system recover context?

### Principle 8: Local Until Proven Otherwise

All data stays on the machine by default. No cloud dependency for core functionality. The system works offline. Network features are additive layers, never required.

**Test:** Disconnect from the internet. Does core function still work?

### Principle conflict resolution

- **Privacy vs. Compounding:** Privacy wins. Compounding operates within privacy boundaries.
- **Local-First vs. Team Features:** Team features use git as transport. No additional network dependencies.
- **Teach Don't Tell vs. Expert Users:** Proficiency tracked per concept, not per user. An expert encountering a new convention gets full explanation for that one thing.
- **Write-Side vs. Search Quality:** Write side gets development priority. Search quality is already strong (P=0.995) and not over-invested.
- **Coaching vs. Token Cost:** Budget cap at 100 tokens. A single successful coaching intervention recovers thousands.

---

## 3. Settled Decisions (Do Not Re-Litigate)

These decisions have been made with rationale. The development instance should treat them as constraints, not open questions.

### Architecture Pivot (Feb 3)
**Decision:** Pivot from retrieval/injection engine to knowledge capture engine.
**Why:** Per-prompt injection has irreducible false positives (DeepMind LIMIT proves this mathematically). Write side (decision extraction, handoffs) creates artifacts that don't exist otherwise — this is the unique value. Industry converging on structured storage + agent-directed retrieval.

### Pointer-Based Orientation (Feb 3, shipped)
**Decision:** SessionStart injects file paths, not content. Agent reads files itself.
**Why:** Previous approach injected ~2000 tokens of extracted/truncated content. Pointer-based is ~80 tokens. 25x reduction. The agent has Read/Grep/Glob — it's a better reader than regex extraction. Removed 6 parsing functions.

### Stop Hook Is Not Critical Path (Feb 3)
**Decision:** Session continuity must not depend on Stop hook firing.
**Why:** Closing terminal window = Stop hooks don't execute. IDE persists session transcripts regardless of exit method. Solution: process previous session data at NEXT SessionStart from already-persisted data.

### UserPromptSubmit Repurposed (Feb 3)
**Decision:** Stop embedding + searching + injecting on every prompt. Use UserPromptSubmit only to tag the current instance with the user's first prompt (~5ms, one file read + conditional write).
**Status:** Not yet fully implemented. Context-surfacing still active behind a gate chain as transition.

### Cross-Machine Continuity Gap (Feb 3, accepted)
**Decision:** Accept that cross-machine continuity for ungraceful exits has no automatic recovery. Same-machine is deterministic; cross-machine depends on Stop hooks.
**Continuity matrix:**

| Scenario | Same-machine | Cross-machine |
|---|---|---|
| Graceful exit (Stop fires) | Full | Full (handoff syncs) |
| Ungraceful after PreCompact | Good (checkpoint) | Good (checkpoint syncs) |
| Ungraceful, short session | Basic (session index) | None |

### Session Flow: Six Phases (Feb 3)
1. **SessionStart** (guaranteed) — orient, register instance, point to files
2. **First prompt** (repurposed hook) — tag instance with session topic
3. **During session** (agent-driven) — MCP tools for search, zero hook overhead
4. **PreCompact** (conditional) — checkpoint handoff + decision extraction
5. **Session end** (best-effort) — full handoff + decisions if Stop fires
6. **Between sessions** — sync delivers artifacts cross-machine

**Key rule:** Everything critical flows through Phase 1. Phases 4-5 are value-add, not dependencies.

### Context Surfacing v2: Gate Chain (Feb 4)
**Decision:** Reversed the decision to kill UserPromptSubmit entirely. Instead, keep context surfacing but add a 6-gate chain that suppresses injection on ~80% of prompts.
**Why:** Zero injection loses the ambient awareness that makes SAME feel alive. The gate chain preserves injection for high-value moments only.

**Gate chain (evaluated in order, first match skips):**
1. **minChars** — skip prompts < 20 chars
2. **slashCmd** — skip prompts starting with `/`
3. **conversational** — skip social phrases ("thanks", "ok", "sounds good")
4. **lowSignal** — skip prompts with no specific terms
5. **mode detection** — skip Executing and Socializing modes; allow Exploring, Deepening, Reflecting
6. **topicChange** — Jaccard similarity on extracted terms; skip if same topic (threshold 0.35)

### RLM Reranking Layer (Feb 2, deferred)
**Decision:** Do not add a reranking language model layer.
**Why:** Current architecture is sufficient for vaults up to ~500 notes. Bottleneck is embedding quality and chunk boundaries, not reranking precision. Latency would increase from ~100ms to ~300-600ms.
**Revisit when:** Vaults >500 notes with user reports of wrong context, or lightweight reranker available in Ollama.

### BSL 1.1 Licensing (Feb 2)
**Decision:** License under Business Source License 1.1.
**Why:** Source visible and free for personal/educational/hobby use. Retains ability to charge for commercial production use. Auto-converts to Apache 2.0 on 2030-02-02.
**Rejected:** MIT (too permissive), AGPL (companies blanket-ban it), Open Core (premature feature segmentation).

### Hard Separation: Dev vs. Vault (Feb 6)
**Decision:** SAME code is only written from the dedicated dev workspace. Vault sessions never touch source code.
**Why:** PII leaked into the repo three times. Root cause: vault sessions have personal/client data loaded via hooks, which bled into code, test data, and commit messages.
**Product insight:** Every SAME user with a public repo has this risk. SAME Guard should become a user-facing feature (it already is).

---

## 4. Eval Tuning Rationale

The search quality constants aren't arbitrary. They were tuned across 105 ground-truth test cases in a multi-hour optimization sprint. Here's why each key constant is set where it is.

### Final metrics
```
Precision:  0.995
Coverage:   0.905
BAD cases:  0
MISS cases: 0
Waste:      0
Cases:      105 (factual 61, negative 22, cross_topic 12, recency 7, decision 3)
```

### MCP search metrics (after ranking.go)
```
MRR:        0.949 (up from 0.668)
BAD cases:  0 (down from 20)
Recall@10:  0.918
```

### Key tuning constants and why

| Constant | Value | Rationale |
|---|---|---|
| `maxDistance` | 16.3 | L2 distance threshold. Relaxed from 16.0→16.2→16.3 to avoid dropping relevant results at the margin. |
| `minComposite` | 0.70 | Composite score floor. Balances false negatives vs. noise. |
| `minSemanticFloor` | 0.25 | Absolute minimum — skip anything below this regardless of other signals. |
| `highTierOverlap` | 0.199 | Floating-point-safe threshold for "strong title match." Set to 0.199 instead of 0.20 because IEEE 754 causes `3/5 * 3/9 = 0.19999...` which fails `>= 0.20`. |
| `minTitleOverlap` | 0.10 | Minimum title relevance to enter the positive sorting tier. |
| `maxResults` | 3 (standard), 2 (ambiguous) | When top candidate's title overlap < 0.199, reduce to 2. Ambiguous queries produce noise in the 3rd slot. |
| `gapCap` | 0.65 | Trims results below 65% of the best match's overlap. **Biggest single precision gain** (+0.087). |
| `maxTokenBudget` | 800 | Per-injection token limit. Balances context richness vs. waste. |
| `topicChangeThreshold` | 0.35 | Jaccard similarity for gate chain topic detection. Below this = new topic, trigger search. |

### Key algorithmic decisions

1. **Three-tier sort**: High overlap (>=0.20) → positive overlap (>0) → zero overlap. Within tiers: priority content types first, then composite score. Prevents zero-overlap priority files from outranking title-relevant results.

2. **Gap cap at 0.65**: The biggest single precision improvement. Trims results that are less than 65% as relevant as the best match. Stopped support files from displacing actually-relevant results.

3. **Mode 5 positive-candidate guard**: Content rescue boost (artificial overlap=0.25) only fires when NO existing candidate has any title relevance. When even weak title overlap exists, content-density heuristics are suppressed so gap cap and zero-overlap trim can work properly.

4. **ContentBoosted transparency**: The maxResults-2 rule sees through artificial overlap from content rescue. ContentBoosted overlap is treated as 0 for the ambiguity check.

5. **Zero-overlap trim**: After maxResults truncation, when the top result has weak but positive title relevance, trailing zero-overlap results are removed. These are vector-search artifacts that are semantically similar but not title-relevant.

6. **Near-dedup**: Collapses versioned copies in the same directory (e.g., "Guide.md" and "Guide v1.md"). Uses full-path overlap with query terms to keep the version-specific file when the user asks for a version.

7. **Recency title search**: Added `KeywordSearchTitleMatch` to recency hybrid search path. Uses minMatches=2 and relaxed overlap threshold (0.05 vs standard 0.10) to handle date-diluted title overlap.

8. **Mode 2 filter: max(titleOnly, fullOverlap)**: Path components can DILUTE wordCoverage by adding non-matching terms to the denominator. Using max prevents false rejections while still requiring minimum signal.

9. **Shared ranking module (ranking.go)**: MCP `HybridSearch` now uses the same title overlap scoring, three-tier sort, near-dedup, and raw output filtering as the hook. MRR nearly doubled from 0.668 to 0.949.

---

## 5. Design Philosophy: Guided Autonomy

The cross-project design philosophy that governs all UX decisions.

**One-liner:** Build tools that orient, adapt, and teach — without getting in the way.

### Five practices

1. **Recommended defaults, visible overrides** — Works with zero configuration. Every default is explained, every override is documented. The transition between beginner and advanced is a slider, not a cliff.

2. **Contextual learning (first-touch / nth-touch)** — Track what concepts the user has encountered. First encounter: full explanation. Fifth encounter: one-liner. Never condescending, never opaque.

3. **Explain the why, not just the what** — Every output, setting, and action should answer "why does this exist?" Turns every setting into a micro-lesson.

4. **Visible machinery** — The user can always see what the tool did and why. Four levels: silent (heartbeats), subtle (brief annotation), transparent (full explanation on demand), educational (includes learning context). Default to subtle.

5. **Track the journey, don't judge it** — Build a picture of where the user is to calibrate communication, never to gate features. Track concepts encountered, not behavior patterns.

### Anti-patterns to avoid
- Smart defaults with no overrides (black box)
- 50-page manuals (nobody reads them)
- "Beginner mode" that's dumbed down (patronizing)
- Track everything about the user (surveillance)
- Explain everything always (exhausting)

### Design checklist for new features
- Does it work with zero configuration?
- Can the user discover what it's doing and why?
- Is there an explanation for someone encountering this concept for the first time?
- Does the explanation get out of the way for returning users?
- Can an advanced user customize the behavior?
- Is the customization explained, not just exposed?

---

## 6. Architectural Invariants

Non-negotiable constraints that hold regardless of which features get built.

| Invariant | Rule |
|---|---|
| **Data format** | Obsidian-flavored Markdown + YAML frontmatter. The markdown files are the source of truth; the DB is a derived index. |
| **Transport** | MCP over stdio for agent-facing tools. No custom protocols. |
| **Embedding** | Local Ollama, always. No cloud API calls for core search. Degrades to keyword-only if Ollama is down, never fails. |
| **Storage** | SQLite, single file per vault. If the DB is lost, it's rebuilt from the markdown. |
| **Hook lifecycle** | SessionStart is the only guaranteed hook. Everything critical reads or emits at SessionStart. Stop/UserPromptSubmit/PreCompact are best-effort. |

---

## 7. What's Designed But Not Built

These features have detailed design docs but no implementation yet. The development instance should be aware of them to avoid conflicting decisions.

- **Session Pulse / Adaptive Coaching** — Go hook tracks interaction patterns (message timing, file churn, prompt sizes), Claude provides contextual judgment, proficiency tracker fades coaching with demonstrated competence. Not Clippy — it learns and shuts up.
- **Team Memory** — 3-layer privacy architecture (shared conventions, opt-in session broadcasts, private transcripts). Uses git as transport.
- **Lab GUI** — Web-based vault viewer with activity feed, flywheel dashboard, gamification. Tauri desktop app in later phase.
- **Research Flywheel** — Research agents that simultaneously test the platform AND enrich the vault with evaluated, source-attributed findings.
- **Seed Marketplace** — Community-contributed vault structure templates and skill packs.

---

*This document was generated from vault design sessions. It contains no PII, client data, or personal information.*
*Last generated: 2026-02-06*
