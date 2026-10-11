---
status: accepted
date: 2026-09-01
decision-makers: "@gasthecreator"
---

# 0008. Tetragon kernel-event corroboration is optional and can never trip a signature

## Context and Problem Statement

Mesh metrics can be corroborated with kernel-level TCP events (resets) from
Cilium Tetragon. We had to decide how that signal enters detection and what
happens when Tetragon is absent.

## Decision Drivers

- Detection must behave identically without Tetragon.
- Reuse the existing metrics path rather than add an ingestion pipeline.
- A kernel signal alone is not enough evidence to mitigate.

## Considered Options

- Hard dependency: require Tetragon to detect
- A separate log-tailing component reading Tetragon's event stream
- One more PromQL query through the existing `metrics.Querier`, applied only
  as a bounded confidence boost to an already-tripped verdict

## Decision Outcome

Chosen option: "optional PromQL corroboration". Tetragon exposes
`tetragon_events_total` on its own `:2112/metrics`, confirmed live, so
`signatures.ApplyKernelCorroboration` adds a flat, capped boost (+0.15, max
1.0) and an evidence note to an already-tripped verdict only.

### Consequences

- Good: no new pipeline; zero behavior change when Tetragon is absent.
- Bad: the metric does not say which kprobe fired, which is accurate for the
  current `/control/reset` mechanism but would need refining for others.

### Confirmation

Unit tests in `internal/signatures` (`kernel_corroboration_test.go`) and a
live end-to-end run that logged `kernel_corroboration=true` with confidence
boosted to 1.0.

## Pros and Cons of the Options

### Hard dependency

- Good, because the signal is always present.
- Bad, because it makes a privileged DaemonSet and a BTF-capable kernel
  mandatory.

### Log-tailing component

- Good, because it reads events directly.
- Bad, because it is a second ingestion pipeline next to a Prometheus-polling
  reconciler.

### Optional PromQL corroboration

- Good, because it reuses the existing architecture.
- Bad, because the signal is coarse.

## More Information

PLAN.md §5 Phase 11; `docs/history/worklog/2026-08-31-phase11-tetragon-spike.md`,
`docs/history/worklog/2026-09-01-phase11-kernel-corroboration.md`.
