---
status: accepted
date: 2026-08-30
decision-makers: "@gasthecreator"
---

# 0005. One active signature per policy; force-complete the outgoing restore on handoff

## Context and Problem Statement

`status.lastSignature` tracks one signature per policy. Latency/error cascade
and fan-out amplification both patch the same `DestinationRule` (disjoint
fields: `outlierDetection` vs `connectionPool.http`). If the detected
signature changes between ticks without a healthy tick in between, the
outgoing signature's patched fields and its `original-*` annotation are
orphaned.

## Decision Drivers

- No CRD status-shape change.
- Keep trip functions scoped to their own signature.
- Never leave an object with non-default config and no record of what to
  restore.

## Considered Options

- Multi-tick handoff: finish the outgoing restore over several ticks first
- Each trip function defensively cleans up the other signature's leftovers
- When `Reconcile` is about to adopt a different signature while
  `Phase != Normal`, synchronously call the outgoing signature's existing
  `complete*Restore` first, in the same reconcile

## Decision Outcome

Chosen option: "synchronous force-complete". It reuses already-tested
restore-to-original logic at a new call site. It is safe because the outgoing
signature's own detector confirmed on this same tick that its condition has
cleared, so there is no ramp to protect.

### Consequences

- Good: no new status field and no multi-tick state machine.
- Bad: the outgoing restore skips the gradual ramp, which is acceptable only
  under the condition above.

### Confirmation

Cross-signature dispatch tests in `internal/controller`, the rapid
state-machine tests, and a live retry-storm to latency/error handoff during
the Phase 6 verification run.

## Pros and Cons of the Options

### Multi-tick handoff

- Good, because it preserves the gradual ramp.
- Bad, because it needs a status-shape (CRD) change.

### Defensive cleanup in trip functions

- Good, because the CRD stays unchanged.
- Bad, because it pushes cross-signature awareness into every trip path.

### Synchronous force-complete

- Good, because it adds a call site, not logic.
- Bad, because it is only valid while detectors are evaluated before the
  handoff.

## More Information

PROPOSALS.md "Signature handoff on a shared DestinationRule can orphan the
outgoing signature's fields" (approved 2026-08-30); PLAN.md §2.6.
