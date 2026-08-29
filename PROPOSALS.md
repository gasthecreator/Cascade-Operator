# PROPOSALS.md — Architecture & Process Change Requests

## Protocol (Cursor and Claude both follow this — read before editing anything)

- **Never edit `PLAN.md` directly** to change an architecture decision, the
  CRD shape, the language/tooling choice, the mitigation strategy, or anything
  else in PLAN.md section 2 ("Architecture Decisions") or section 4
  ("Open Questions"). If, while building, you (Cursor) hit a reason to change
  or resolve one of those, add a new entry below under **Pending Proposals**
  instead of editing PLAN.md.
- Gideon brings this file to Claude for review in a separate session. Claude
  evaluates the proposal against PLAN.md and the project's goals, then does
  exactly one of:
  - **Approves** — updates PLAN.md itself, moves the entry to
    **Resolved Proposals** marked `APPROVED`, with a one-line note on what
    changed in PLAN.md and where.
  - **Rejects** — moves the entry to **Resolved Proposals** marked
    `REJECTED`, with reasoning, and PLAN.md stays as-is.
  - **Needs discussion** — leaves it in Pending, adds a `Claude's question:`
    line under it; Gideon relays the answer back to Cursor, which updates the
    same entry rather than opening a new one.
- This file is a proposal *queue*, not a history of what was built — that's
  `docs/worklog/`.
- Routine implementation choices that don't contradict or extend a decision
  already in PLAN.md (variable/function naming, which helper to extract,
  normal refactors within an already-decided approach) do **not** need a
  proposal. This file is for things that change what PLAN.md says, not how a
  decision already made in PLAN.md gets carried out.

## Template — copy this for a new proposal

```
### [PENDING] <short title>
**Proposed by:** Cursor
**Date:** YYYY-MM-DD
**Affects:** <PLAN.md section, e.g. "2.3 CRD shape" or "Open Question #2">

**Current state:** what PLAN.md says now (quote or summarize the relevant part).

**Proposed change:** what you want it to say instead.

**Why:** the concrete thing you ran into while building that makes the
current plan wrong, incomplete, or worth revisiting. Cite the actual
constraint, error, API limitation, or test result — not a general preference.

**Impact if approved:** what else in the codebase or plan this touches
(files, other open questions, checklist items).
```

---

## Pending Proposals

_(none yet)_

---

## Resolved Proposals

_(none yet)_
