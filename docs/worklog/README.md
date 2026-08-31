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

- [2026-08-30 — Retry storm's connectionPool secondary drops `http1MaxPendingRequests`](2026-08-30-retry-storm-drop-pending-requests.md)
- [2026-08-30 — Review: retry storm's connectionPool secondary — process correction confirmed, overlap resolved](2026-08-30-review-retry-storm-connpool-secondary.md)
- [2026-08-30 — Retry storm's DestinationRule connectionPool.http secondary — last unbuilt patch cell, with a same-field overlap filed as a proposal rather than locked](2026-08-30-retry-storm-connpool-secondary.md)
- [2026-08-30 — Review: latency/error-cascade's timeout secondary — approved, with a firm protocol flag](2026-08-30-review-latency-error-timeout-secondary.md)
- [2026-08-30 — Latency/error-cascade's `VirtualService` timeout secondary: the first signature to manage two object kinds on one trip](2026-08-30-latency-error-timeout-secondary.md)
- [2026-08-30 — Review: operator self-metrics — live confirmation completed](2026-08-30-review-operator-metrics.md)
- [2026-08-30 — Operator self-metrics: signatures detected, patches applied, restorations completed/regressed](2026-08-30-operator-metrics.md)
- [2026-08-30 — Review: root README](2026-08-30-review-root-readme.md)
- [2026-08-30 — Root README: the front door this repo has needed since the first checklist](2026-08-30-root-readme.md)
- [2026-08-30 — Review: retry-storm mitigation webhook-rejection fix](2026-08-30-review-retry-storm-webhook-fix.md)
- [2026-08-30 — Retry-storm mitigation: clear retryOn/perTryTimeout/backoff on trip so Istio's validating webhook stops rejecting the patch](2026-08-30-retry-storm-mitigation-webhook-fix.md)
- [2026-08-30 — Review: k6 cascade-simulation scripts + webhook-rejection proposal resolved](2026-08-30-review-k6-cascade-simulation.md)
- [2026-08-30 — k6 cascade-simulation scripts: load generation for all three signatures, live evidence, one real mitigation bug found](2026-08-30-k6-cascade-simulation.md)
- [2026-08-30 — Review: signature-handoff restore fix](2026-08-30-review-signature-handoff-fix.md)
- [2026-08-30 — Signature handoff on a shared object: force-complete the outgoing signature's restore before adopting the incoming one](2026-08-30-signature-handoff-restore-fix.md)
- [2026-08-30 — Review: fan-out signature + signature-handoff proposal resolved](2026-08-30-review-fanout-signature.md)
- [2026-08-30 — Fan-out amplification: detector, cross-host PromQL, connectionPool bulkhead, restoration — built together](2026-08-30-fanout-signature.md)
- [2026-08-30 — Review: fan-out demo topology + live-scrape evidence](2026-08-30-review-fanout-demo-evidence.md)
- [2026-08-30 — Fan-out demo topology + live-scrape evidence for the fan-out signal](2026-08-30-fanout-demo-evidence.md)
- [2026-08-30 — Review: retry-storm restoration, signature-dispatched restore machinery](2026-08-30-review-retry-storm-restoration.md)
- [2026-08-30 — Retry-storm restoration: wire the VirtualService patch into Reconcile, dispatch restore by signature](2026-08-30-retry-storm-restoration.md)
- [2026-08-30 — Review: retry-storm mitigation (VirtualService patch, not yet wired live)](2026-08-30-review-retry-storm-mitigation.md)
- [2026-08-30 — Retry-storm mitigation: VirtualService retries.attempts primary, built but not yet wired live](2026-08-30-retry-storm-mitigation.md)
- [2026-08-30 — Review: retry-storm detector slice](2026-08-30-review-retry-storm-detector.md)
- [2026-08-30 — Retry-storm detector, status only](2026-08-30-retry-storm-detector.md)
- [2026-08-29 — Review: Kind + Istio dev environment, PromQL/response_flags proposals](2026-08-29-review-kind-istio-dev-env.md)
- [2026-08-29 — Kind + Istio local mesh; PromQL and response_flags evidence](2026-08-29-kind-istio-dev-env.md)
- [2026-08-29 — Review: restoration ramp slice](2026-08-29-review-restoration-ramp.md)
- [2026-08-29 — Restoration ramp for the latency/error-cascade outlierDetection patch](2026-08-29-restoration-ramp.md)
- [2026-08-29 — Review: Istio outlier-detection patch slice](2026-08-29-review-istio-outlier-patch.md)
- [2026-08-29 — Istio patch: latency/error-cascade primary (DestinationRule outlierDetection)](2026-08-29-istio-outlier-patch.md)
- [2026-08-29 — Review: latency/error-cascade detector slice](2026-08-29-review-latency-error-detector.md)
- [2026-08-29 — Latency/error-cascade detector wired through the reconcile loop](2026-08-29-latency-error-detector.md)
- [2026-08-29 — Review: Prometheus HTTP client slice](2026-08-29-review-prometheus-client.md)
- [2026-08-29 — Prometheus HTTP client behind Query → Snapshot](2026-08-29-prometheus-http-client.md)
- [2026-08-29 — Kind smoke test: manager + CRD + sample CR](2026-08-29-kind-smoke-test.md)
- [2026-08-29 — Review: repo scaffold, CRD, reconciler slice](2026-08-29-review-scaffold-slice.md)
- [2026-08-28 — Repo scaffold, CascadePolicy CRD, logging reconciler](2026-08-28-repo-scaffold-crd-reconciler.md)
- [2026-08-28 — Review: five open-question proposals](2026-08-28-review-open-question-proposals.md)
- [2026-08-28 — Planning pass on open questions](2026-08-28-planning-pass-open-questions.md)
- [2026-08-28 — Repo init and PLAN.md](2026-08-28-repo-init-and-plan.md)
