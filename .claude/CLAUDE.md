# Internal Development Rules

## Confidentiality — MANDATORY

All code, comments, commit messages, and documentation in this repo are PUBLIC.
Never include any of the following in committed content:

### Banned Terms (use the alternative instead)
| Never Use | Use Instead |
|-----------|-------------|
| CEO | admin |
| company-hq | data directory |
| investor, investors | (omit entirely) |
| strategy, competitive analysis | (omit entirely) |
| revenue, pricing, fundraising | (omit entirely) |
| Sean, Gleason | (omit entirely) |
| protonmail | (omit entirely) |
| sgx-labs.dev | thirty3labs.com |
| SAME_COMPANY_HQ | SAME_DATA_DIR |

### Never Include
- Real names or personal email addresses
- Internal business strategy or competitive intelligence
- References to other internal repos or company-hq/
- Pricing, revenue targets, or financial information
- Investor communications or fundraising details
- Internal org structure or team composition

### Commit Message Rules — MANDATORY
- Describe WHAT changed technically, not WHY from a business perspective
- Never reference internal decisions, CEO directives, or company strategy
- Never use words that reveal internal motivation: "downgrade", "inflate", "deflate", "defensible", "credibility", "competitive", "positioning", "strategy"
- Use generic terms: "admin" not "CEO", "data directory" not "company-hq"
- Good: "docs: clarify metrics methodology in README"
- Bad: "Downgrade inflated claims to defensible language"
- Good: "fix: update license description to BSL 1.1"
- Bad: "Fix open source claim before launch"
- When in doubt, describe the technical diff, nothing more

### If Unsure
If content could reveal internal business context, leave it out.

---

## Git Discipline — MANDATORY (this is a PUBLIC repo)

### Never Push Directly
- **NEVER run `git push` without explicit admin approval in that moment**
- A previous push approval does NOT carry forward. Ask EVERY time.
- Before pushing, ALWAYS run `git log origin/main..HEAD` and show what will go out
- Be aware that `git push origin main` pushes ALL unpushed commits, not just the latest

### Commit Hygiene
- **Squash related work** before pushing — don't push 5 commits that should be 1
- **Use feature branches** for multi-commit work. Squash-merge to main.
- No "fix the fix" chains — get it right before committing, or amend before pushing
- No iterative data commits (batch 1, then batch 1+2, then all) — commit once when complete
- Every pushed commit should be self-contained and meaningful

### Never Run Destructive Git Operations
- No `git push --force` — ever, on a public repo
- No `git reset --hard` on pushed commits
- No `git rebase` on pushed history
- No `git push --tags` without explicit approval

### Commit Message Quality
- This is a public repo. People read the log.
- Conventional commits: `feat:`, `fix:`, `docs:`, `test:`, `chore:`, `refactor:`
- One clear sentence describing the change
- No internal context, no business motivation
- Good: `feat: add metadata search filters (--trust, --type, --tag)`
- Bad: `feat: improve eval scores for launch readiness`

### Before Every Push
1. Run `git log origin/main..HEAD --oneline` — review what goes out
2. Verify no banned terms: `git diff origin/main..HEAD | grep -iE "company-hq|CEO|investor"`
3. Verify tests pass: `go test ./...`
4. Show the commit list to admin and get explicit "push" approval
5. Only then: `same push-allow && git push origin main`
