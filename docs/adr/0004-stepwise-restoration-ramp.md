---
status: accepted
date: 2026-08-28
decision-makers: "@gasthecreator"
---

# 0004. Restore by loosening the same knobs in steps, gated by a fresh metrics check

## Context and Problem Statement

After a trip clears, the mesh configuration has to return to normal without
immediately re-triggering the failure.

## Decision Drivers

- One restore mechanism for all three signatures.
- Never clobber user-authored routing.
- A regression during restore must re-trip immediately.

## Considered Options

- Ramp the same fields that were tightened on trip, in steps (`restoreStep`
  0-4), with a metrics re-check between steps
- Traffic-weight ramp (10%, 25%, 50%, 100%) through `VirtualService` route
  weights

## Decision Outcome

Chosen option: "loosen the same fields in steps". The operator records the
original values in annotations at trip time, steps back toward them, and
returns to `Normal` after step 4. A regression at any step re-enters
`Tripped` at step 0.

### Consequences

- Good: one ramp mechanism; the object ends up looking untouched, so a later
  trip snapshots a fresh original.
- Bad: restore speed is bounded by reconcile cadence and the number of steps.

### Confirmation

`status.restoreStep` and the `Normal -> Tripped -> Restoring(N) -> Normal`
state machine are covered by unit tests and by the rapid property tests
(Phase 10); live trip/restore cycles run in `make test-integration`.

## Pros and Cons of the Options

### Same-field stepwise ramp

- Good, because it is simple and reversible.
- Bad, because it cannot shed load gradually.

### Traffic-weight ramp

- Good, because it is a familiar canary pattern.
- Bad, because it needs a dummy or abort destination, a second state machine,
  and risks overwriting user-authored routes.

## More Information

PLAN.md §2.6; `docs/history/worklog/2026-08-29-restoration-ramp.md`.
