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

- [2026-09-04 — Three security-hardening items built; a real upstream Calico bug found and fixed along the way; full live verification incomplete](2026-09-04-security-hardening-and-a-real-calico-bug.md)
- [2026-09-04 — Two genuinely deferred deliverables closed: Phase 7's Claude Artifact copy, and a Tetragon reset k6 scenario](2026-09-04-phase7-artifact-copy-and-tetragon-k6-scenario.md)
- [2026-09-03 — hack/deploy-operator.sh validated as a genuine from-scratch run](2026-09-03-deploy-operator-fresh-run-validation.md)
- [2026-09-03 — Per-mesh Prometheus client, and getting past linkerd-viz's own locked-down AuthorizationPolicy](2026-09-03-per-mesh-prometheus-client-and-linkerd-viz-authz.md)
- [2026-09-03 — Operator deployed in-cluster for real, and its own metrics genuinely scraped](2026-09-03-operator-in-cluster-deploy-and-metrics-scrape.md)
- [2026-09-01 — Documentation follow-up: docs/dev-linkerd.md, and a closer look at the operator-metrics gap](2026-09-01-docs-dev-linkerd-and-audit-followup.md)
- [2026-09-01 — Phase 11 (slice 2, final): kernel-event corroboration wired into detection, live-verified end-to-end](2026-09-01-phase11-kernel-corroboration.md)
- [2026-09-01 — Phase 11 (slice 1): real TCP-layer fault injection, live-verified against Tetragon](2026-09-01-phase11-tcp-reset-fault-injection.md)
- [2026-09-01 — Phase 6.6 follow-up: wire Linkerd into CI's integration job](2026-09-01-phase6.6-ci-linkerd-integration.md)
- [2026-09-01 — Phase 6.6 (slice 5, final): Linkerd integration test coverage, live-verified](2026-09-01-phase6.6-linkerd-integration-tests.md)
- [2026-09-01 — Phase 6.6 (slice 4): hack/install-linkerd.sh, live-verified](2026-09-01-phase6.6-linkerd-install-script.md)
- [2026-08-31 — Phase 6.6 (slice 3): spec.mesh reconciler dispatch, live-verified](2026-08-31-phase6.6-reconciler-mesh-dispatch.md)
- [2026-08-31 — Phase 6.6 (slice 2): Linkerd Mitigator, live-verified against the real cluster](2026-08-31-phase6.6-linkerd-mitigator.md)
- [2026-08-31 — Phase 6.6 (slice 1): Linkerd QueryBuilder, live-verified against a real Linkerd install](2026-08-31-phase6.6-linkerd-query-builder.md)
- [2026-08-31 — Phase 6.5: retry storm migrated to mesh.Mitigator (last of three signatures)](2026-08-31-phase6.5-mitigator-retrystorm.md)
- [2026-08-31 — Phase 6.4: latency/error-cascade migrated to mesh.Mitigator](2026-08-31-phase6.4-mitigator-latency-error.md)
- [2026-08-31 — Phase 6.3: Mitigator interface + fan-out amplification migrated](2026-08-31-phase6.3-mitigator-fanout.md)
- [2026-08-31 — Phase 7: visual cascade replay](2026-08-31-phase7-visual-cascade-replay.md)
- [2026-08-31 — Phase 11: eBPF/Tetragon corroboration — environment spike (real, but partial)](2026-08-31-phase11-tetragon-spike.md)
- [2026-08-31 — Phase 6.1/6.2: mesh adapter interface (QueryBuilder) + spec.mesh field](2026-08-31-phase6-mesh-query-builder.md)
- [2026-08-31 — errorRateQuery missing sum() aggregation, fixed](2026-08-31-error-rate-query-sum-fix.md)
- [2026-08-31 — Phase 9: quantified resilience benchmark](2026-08-31-phase9-resilience-benchmark.md)
- [2026-08-31 — Phase 8: postmortem generator (cmd/postmortem)](2026-08-31-phase8-postmortem-generator.md)
- [2026-08-31 — Phase 10: property-based state-machine verification (pgregory.net/rapid)](2026-08-31-phase10-property-based-verification.md)
- [2026-08-31 — Phase 5 (4/4): per-edge threshold overrides — additive, not breaking](2026-08-31-phase5-per-edge-threshold-overrides.md)
- [2026-08-31 — Phase 5 (3/4): security threat-model doc](2026-08-31-phase5-security-threat-model.md)
- [2026-08-31 — Phase 5 (2/4): HA — replicas: 2 + preferred pod anti-affinity](2026-08-31-phase5-ha-replicas.md)
- [2026-08-31 — Phase 5 (1/4): retry storm's restore-completion zero-value bug — VirtualService fixed, DestinationRule found not applicable](2026-08-31-phase5-retry-storm-restore-zero-value.md)
- [2026-08-31 — Phase 4: Grafana dashboard + trip/restore webhook notifier](2026-08-31-phase4-observability.md)
- [2026-08-31 — Phase 3: CascadePolicy admission webhook](2026-08-31-phase3-admission-webhook.md)
- [2026-08-31 — Phase 2: integration coverage for latency/error-cascade and fan-out amplification](2026-08-31-phase2-integration-coverage.md)
- [2026-08-31 — Review: Phase 1 CI workflows — approved, verification independently confirmed](2026-08-31-review-phase1-ci-workflows.md)
- [2026-08-31 — Review: Phase 0 repo hygiene — approved, two small fixes](2026-08-31-review-phase0-repo-hygiene.md)
- [2026-08-31 — Phase 1 CI: Kind+Istio integration workflow, govulncheck, CodeQL](2026-08-31-phase1-ci-workflows.md)
- [2026-08-31 — Phase 0 repo hygiene: standard org files (PLAN.md §5)](2026-08-31-phase0-repo-hygiene.md)
- [2026-08-31 — Review: Kind integration test suite — approved, one broken-link fix](2026-08-31-review-kind-integration-tests.md)
- [2026-08-31 — Kind integration tests: real reconcile loop, unstructured wire-format assertions](2026-08-31-kind-integration-tests.md)
- [2026-08-31 — Review: retry storm's maxRetries=1 fix — approved, organic trip gap filled](2026-08-30-review-retry-storm-maxretries-one.md)
- [2026-08-30 — Retry storm's connectionPool secondary trips `maxRetries` to 1, not 0](2026-08-30-retry-storm-maxretries-one.md)
- [2026-08-30 — Review: Istio maxRetries-zero translation investigation — approved, direction 2](2026-08-30-review-istio-maxretries-zero-translation.md)
- [2026-08-30 — Istio does not push DestinationRule `maxRetries: 0` into Envoy CDS](2026-08-30-istio-maxretries-zero-translation.md)
- [2026-08-30 — Review: retry storm's zero-value patch fix — approved, Envoy-level check confirmed independently](2026-08-30-review-retry-storm-zero-value-patch.md)
- [2026-08-30 — Retry storm's zero trip values now cross the wire as explicit JSON zeros](2026-08-30-retry-storm-zero-value-patch.md)
- [2026-08-30 — Retry storm's zero-value trip fields never actually reach the API server](2026-08-30-retry-storm-zero-value-serialization-bug.md)
- [2026-08-30 — Review: drop http1MaxPendingRequests — approved; investigation surfaced a much bigger finding](2026-08-30-review-retry-storm-drop-pending-requests.md)
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
