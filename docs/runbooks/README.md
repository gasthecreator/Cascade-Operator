# Runbooks

Operational procedures for Cascade Operator. Each runbook follows the same
shape: **symptom**, **impact**, **diagnose**, **mitigate**, **escalate**,
**verify**. Commands assume the default install, where everything lives in
`cascade-operator-system`; adjust `NS` if you deployed elsewhere.

```bash
export NS=cascade-operator-system
export DEPLOY=cascade-operator-controller-manager
```

| Runbook | Use it when |
|---|---|
| [Operator not reconciling](operator-not-reconciling.md) | Policies never change phase, no reconcile logs, pods crash-looping or not leading |
| [Prometheus unreachable](prometheus-unreachable.md) | Query errors in the logs, or detection silently never runs |
| [Policy stuck in Tripped](policy-stuck-in-tripped.md) | `status.phase` stays `Tripped` after the incident is over |
| [Policy stuck in Restoring](policy-stuck-in-restoring.md) | `status.phase` stays `Restoring`, or keeps resetting to step 0 |
| [Admission webhook failing](webhook-failing.md) | Creating or updating a `CascadePolicy` is rejected with a webhook error, or the certificate expired |
| [Mitigation applied but not effective](mitigation-not-effective.md) | The operator tripped and patched, but the failure continues |

Related: [security threat model](../security-threat-model.md),
[SLOs](../slo.md), [ADRs](../adr/README.md).

## Quick reference

| What | Where |
|---|---|
| Manager Deployment | `deploy/cascade-operator-controller-manager` (label `control-plane=controller-manager`) |
| Health probes | `:8081` `/healthz`, `/readyz` |
| Metrics | Service `cascade-operator-controller-manager-metrics-service`, port 8443 (HTTPS, authenticated) |
| Leader election lease | `3417ff6e.gideonsanni.dev` in `$NS` |
| Webhook | Service `cascade-operator-webhook-service`; `ValidatingWebhookConfiguration/cascade-operator-validating-webhook-configuration` (`failurePolicy: Fail`) |
| Operator metrics | `cascade_signatures_detected_total{signature,dependency}`, `cascade_mitigation_patches_applied_total{signature,kind}`, `cascade_restorations_completed_total{signature}`, `cascade_restoration_regressions_total{signature}` |
| Annotations on patched objects | `cascade.gideonsanni.dev/managed-by`, `original-outlier-detection`, `original-connection-pool`, `original-timeout`, `original-retries`, `original-retry-connection-pool` |
| Status | `status.phase` (`Normal`/`Tripped`/`Restoring`), `status.lastSignature`, `status.lastTrippedAt`, `status.restoreStep` (0-4), condition `DependencyObjectMissing` |

Reconcile interval is 10 seconds by default; restoration advances one step per
reconcile.
