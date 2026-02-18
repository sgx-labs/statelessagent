# SAME Design Context — Human-First System Design

SAME exists to help people think better with AI, not to make people dependent on AI.

This document explains the philosophy behind the product: why it is local-first,
why markdown is the source of truth, why the tool should teach by use, and why
the goal is to make individuals more capable over time.

If AI tooling changes tomorrow, your notes are still yours. Your knowledge still
compounds. That is the core design constraint.

**Scope:** product philosophy, architecture direction, and settled technical decisions.
**Non-scope:** launch tactics, internal planning, user vault data, or PII.

---

## Human-First Thesis

1. **Humans own their knowledge.** SAME stores durable memory in plain markdown.
2. **Learning is a product feature.** The system should help users improve by doing.
3. **Local-first by default.** Core workflows work offline with no cloud dependency.
4. **AI is a multiplier, not the owner.** SAME is designed for a symbiotic workflow.
5. **Build for the individual.** Solo developers, creatives, and researchers should
   be able to build meaningful systems without a large team.
6. **Seeing is believing.** The tool should demonstrate value quickly through use,
   not require trust in hidden magic.

### Learning loop (product behavior)

The intended loop is simple and repeatable:

1. The user does real work (writes notes, builds code, asks questions).
2. SAME captures structured artifacts (decisions, handoffs, relationships).
3. Next sessions start with better orientation and context.
4. The user learns faster because prior work is visible and reusable.

This loop should make both the human and the agent more capable over time.

---

## 1. The v2 Architecture Pivot

SAME is pivoting from a **retrieval/injection engine** to a **session continuity engine**. The tagline is: **"Compass, Not Search Engine."**

### Why the pivot

1. **Session continuity is the real value.** "Every AI session starts from zero. Not anymore." The magic moment is when a new session picks up where the last one left off without the user doing anything.

2. **Agents are better searchers than hooks.** Modern AI tools have file access and MCP tools. Give them orientation and get out of the way — they'll find what they need.

3. **Graceful degradation matters.** The system must recover context even when sessions end ungracefully. Critical paths cannot depend on exit hooks.

4. **Industry convergence.** The broader ecosystem is moving toward structured storage with agent-directed retrieval.

### Architecture direction

The pivot moves from per-prompt context injection toward compact session orientation at start, with agent-driven search via MCP during the session. This dramatically reduces token overhead while improving relevance — the agent searches intentionally instead of receiving speculative injections.

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
**Decision:** Reversed the decision to kill UserPromptSubmit entirely. Instead, keep context surfacing but add a multi-gate chain that suppresses injection on ~80% of prompts.
**Why:** Zero injection loses the ambient awareness that makes SAME feel alive. The gate chain preserves injection for high-value moments only — filtering out short prompts, commands, social chatter, repeat topics, and low-signal queries.

### RLM Reranking Layer (Feb 2, deferred)
**Decision:** Do not add a reranking language model layer.
**Why:** Current architecture is sufficient for vaults up to ~500 notes. Bottleneck is embedding quality and chunk boundaries, not reranking precision. Latency would increase from ~100ms to ~300-600ms.
**Revisit when:** Vaults >500 notes with user reports of wrong context, or lightweight reranker available in Ollama.

### BSL 1.1 Licensing (Feb 2)
**Decision:** License under Business Source License 1.1.
**Why:** Source visible and free for personal/educational/hobby use. Retains ability to charge for commercial production use. Auto-converts to Apache 2.0 on 2030-02-02.
**Rejected at launch:** MIT (too permissive for sustainable development), AGPL (companies blanket-ban it).

### Hard Separation: Dev vs. Vault (Feb 6)
**Decision:** SAME code is only written from the dedicated dev workspace. Vault sessions never touch source code.
**Why:** Prior incidents showed that mixing live vault sessions with code work can leak sensitive context into source control. Separate workspaces make this structurally harder.
**Product insight:** Every SAME user with a public repo has this risk. SAME Guard should become a user-facing feature (it already is).

---

## 4. Eval Tuning Rationale

The search quality constants were tuned across 105 ground-truth test cases spanning factual, negative, cross-topic, recency, and decision queries.

### Results
```
Precision:  0.995
Coverage:   0.905
MRR:        0.949
BAD cases:  0
```

The ranking pipeline combines semantic distance, composite scoring, title overlap, content-type priority, gap-based trimming, and near-dedup. Constants and algorithms are in `internal/store/ranking.go` — shared between hooks and MCP to ensure consistent quality across all retrieval paths.

All evaluation uses synthetic vault data with known relevance judgments. No user data is used.

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
| **Embedding** | Local by default (Ollama). Optional support for OpenAI and OpenAI-compatible endpoints. Degrades to keyword-only if no embedding provider is available, never fails. |
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
