# Engineering Worklog

This directory is the permanent, append-only record of every piece of work
done on Cascade Operator — what was built, why, and how. Write it the way
you'd document work on a real engineering team: specific enough that someone
with no memory of the session that produced it can reconstruct the reasoning
later, purely from this file plus the diff.

This is **not** the same as:
- `PROPOSALS.md` — change requests to already-decided architecture/process,
  reviewed before being merged into PLAN.md. Nothing here changes PLAN.md.
- `PLAN.md` — reflects *current* state and decisions, not history. If a
  worklog entry causes a PLAN.md checklist item to flip from unchecked to
  checked, update the checklist too — but the reasoning lives here, not there.

## Convention

- One file per unit of work: a feature, a bugfix, a refactor, a test suite, an
  infra/tooling change. Roughly "one file per thing you'd describe in a
  standup," not one file per commit and not one file per week.
- Filename: `YYYY-MM-DD-short-slug.md`.
- Whoever does the work writes the entry — Cursor, Claude, or Gideon.
- Don't skip **Why** or **How**. `git log` already gives you *what* for free;
  this file exists for the parts git doesn't capture.

## Template — copy this for a new entry

```
# <Title>

**Date:** YYYY-MM-DD
**Author:** Cursor | Claude | Gideon
**Type:** feature | fix | refactor | infra | test | docs

## What
Concrete description of what changed.

## Why
What requirement, PLAN.md decision, or problem this addresses.

## How
The concrete approach taken and any implementation-level choices made along
the way — not "why Go" (that's PLAN.md territory) but e.g. "used a ring
buffer for the metric window instead of re-querying Prometheus every tick,
because repeated range queries were adding ~200ms per reconcile."

## Files touched
- path/to/file — one-line description of the change in that file

## Testing
What was actually run/verified, and the result. If nothing was tested, say so
and why.

## Follow-ups / known gaps
Anything deliberately deferred, and why.
```

## Index (newest first)

- [2026-08-29 — Review: Prometheus HTTP client slice](2026-08-29-review-prometheus-client.md)
- [2026-08-29 — Prometheus HTTP client behind Query → Snapshot](2026-08-29-prometheus-http-client.md)
- [2026-08-29 — Kind smoke test: manager + CRD + sample CR](2026-08-29-kind-smoke-test.md)
- [2026-08-29 — Review: repo scaffold, CRD, reconciler slice](2026-08-29-review-scaffold-slice.md)
- [2026-08-28 — Repo scaffold, CascadePolicy CRD, logging reconciler](2026-08-28-repo-scaffold-crd-reconciler.md)
- [2026-08-28 — Review: five open-question proposals](2026-08-28-review-open-question-proposals.md)
- [2026-08-28 — Planning pass on open questions](2026-08-28-planning-pass-open-questions.md)
- [2026-08-28 — Repo init and PLAN.md](2026-08-28-repo-init-and-plan.md)
