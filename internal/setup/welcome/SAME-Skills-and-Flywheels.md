---
title: "SAME — Skills and Flywheels"
tags: [same, reference, skills, automation]
content_type: hub
---

# Skills and Flywheels

Supercharge SAME with reusable skills and self-reinforcing flywheels.

## What Are Skills?

Skills are reusable prompts that automate common workflows. In Claude Code, they live in `.claude/skills/` and are invoked with slash commands.

## Recommended SAME Skills

### /document-decision

Create a well-formatted decision note:

```markdown
# .claude/skills/document-decision.md

Create a decision note with this structure:

---
title: "Decision: [Topic]"
tags: [decision, <relevant-tags>]
content_type: decision
date: <today>
---

# Decision: [Topic]

## Context
What problem or question prompted this decision?

## Decision
What did we decide? Be specific.

## Rationale
Why this approach? What alternatives were considered?

## Consequences
What are the implications? Any follow-up needed?
```

### /create-handoff

Generate a session summary:

```markdown
# .claude/skills/create-handoff.md

Create a handoff note summarizing this session:

---
title: "Session Handoff - <date>"
tags: [handoff, session]
content_type: handoff
date: <today>
---

# Session Handoff

## What We Worked On
- Bullet points of main activities

## Decisions Made
- Key decisions with brief rationale

## Current State
Where did we leave off?

## Next Steps
What should the next session pick up?

## Open Questions
Anything unresolved?
```

### /create-hub

Create a hub note for a topic:

```markdown
# .claude/skills/create-hub.md

Create a hub note that serves as a central reference:

---
title: "[Topic] Hub"
tags: [hub, <topic>]
content_type: hub
---

# [Topic]

Central reference for [topic].

## Overview
Brief description of this area.

## Key Decisions
- [[Decision: ...]]

## Resources
- Links to relevant notes
- External documentation

## Current Status
What's the state of this area?
```

### /search-deep

Trigger a comprehensive search:

```markdown
# .claude/skills/search-deep.md

Search the user's notes thoroughly:

1. Search with the exact query (top_k=15)
2. Try 2-3 alternate phrasings
3. Search for related concepts
4. Report what you found or didn't find

If nothing found, offer to document the topic.
```

## The SAME Flywheels

Flywheels are self-reinforcing loops. SAME has several:

### 1. The Usage Flywheel

```
AI uses a note
    ↓
Note gets "access boost"
    ↓
Note ranks higher next time
    ↓
AI uses it more
    ↓
Better results over time
```

Notes that actually help get surfaced more. Notes that don't help fade away.

### 2. The Documentation Flywheel

```
User documents decision
    ↓
AI references it in future
    ↓
User sees value ("it remembered!")
    ↓
User documents more
    ↓
Richer knowledge base
```

The more you document, the smarter your AI gets, which motivates more documentation.

### 3. The Quality Flywheel

```
Well-structured note
    ↓
Better search matches
    ↓
AI produces better output
    ↓
User learns what works
    ↓
Writes better notes
```

Good notes teach you what format works, leading to better notes.

### 4. The Handoff Flywheel

```
Session ends with handoff
    ↓
Next session starts with context
    ↓
Faster ramp-up
    ↓
More productive session
    ↓
Better handoff for next time
```

Handoffs compound — each one makes the next session more productive.

## Kickstarting the Flywheels

### Week 1: Foundation
- Document your top 3 project decisions
- Create one hub note for your main project
- Let SAME generate handoffs automatically

### Week 2: Build Momentum
- Document decisions as you make them
- Notice when SAME surfaces something useful
- Add context when SAME misses something

### Week 3+: Cruise
- Flywheels are spinning
- AI remembers your project
- Documentation happens naturally

## Measuring Flywheel Health

```bash
same status          # How many notes indexed?
same log             # What's being surfaced?
same budget          # Is surfaced context being used?
```

If surfaced context isn't being used, either:
- Notes need better structure
- Search profile needs tuning (`same profile use precise`)
- Content isn't matching queries (try different terminology)
