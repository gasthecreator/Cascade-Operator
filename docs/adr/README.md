# Architecture decision records

Short records of decisions that are significant or hard to reverse: the
context, the options that were really on the table, what was chosen, and how
we know it holds. Format: [MADR](https://adr.github.io/madr/); start from
[`0000-template.md`](0000-template.md).

Write an ADR when a decision changes the CRD shape, the mesh adapter
interfaces, the security posture, or anything costly to undo. Routine
implementation choices do not need one. ADRs are immutable once accepted: to
change a decision, add a new ADR and mark the old one `superseded by ADR-NNNN`.

The longer reasoning and live evidence behind each decision is in
[`PLAN.md`](../../PLAN.md), the resolved entries in
[`PROPOSALS.md`](../../PROPOSALS.md) and the worklog; each ADR links to its
sources.

| # | Title | Status | Date |
|---|---|---|---|
| 0001 | [Build the operator in Go with kubebuilder and controller-runtime](0001-use-go-and-kubebuilder.md) | accepted | 2026-08-28 |
| 0002 | [One shared `QueryBuilder`/`Mitigator` interface; Istio and Linkerd are both first-class implementations](0002-shared-mesh-interfaces.md) | accepted | 2026-08-31 |
| 0003 | [Detectors are pure functions over a metric snapshot; metrics come from polling Prometheus](0003-pure-detectors-over-prometheus-snapshots.md) | accepted | 2026-08-28 |
| 0004 | [Restore by loosening the same knobs in steps, gated by a fresh metrics check](0004-stepwise-restoration-ramp.md) | accepted | 2026-08-28 |
| 0005 | [One active signature per policy; force-complete the outgoing restore on handoff](0005-single-active-signature-and-synchronous-handoff.md) | accepted | 2026-08-30 |
| 0006 | [Linkerd: failure-accrual annotations for latency/error cascade, ServiceProfile retry budgets for retry storm](0006-linkerd-failure-accrual-vs-serviceprofile.md) | accepted | 2026-08-31 |
| 0007 | [Fan-out amplification is detect-only on Linkerd](0007-fan-out-detect-only-on-linkerd.md) | accepted | 2026-08-31 |
| 0008 | [Tetragon kernel-event corroboration is optional and can never trip a signature](0008-tetragon-corroboration-is-optional.md) | accepted | 2026-09-01 |
| 0009 | [Namespace-scoped RBAC and an egress NetworkPolicy as opt-in hardening](0009-namespaced-rbac-and-egress-networkpolicy.md) | accepted | 2026-09-04 |
| 0010 | [Sign images keylessly with cosign and attach an SBOM and SLSA provenance](0010-keyless-image-signing-with-sbom-and-provenance.md) | accepted | 2026-09-04 |
