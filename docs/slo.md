# Service level objectives for the operator itself

These are objectives for Cascade Operator as a piece of infrastructure (is it
reconciling, is it protecting, is its webhook up), not for the services it
protects. They are **proposals**: no baseline has been measured over a long
enough period to commit to the numbers, so each target is a starting point to
tighten or relax once there is data.

Every SLI below says whether the metric it needs exists today. Where one does
not, the gap is named; nothing here adds code.

Metrics that exist today: the operator's own `cascade_*` counters
(`internal/controller/metrics.go`), the controller-runtime reconcile and
webhook metrics served on the same `/metrics` endpoint, and
`status.lastTrippedAt`/`status.phase` on each `CascadePolicy`.

## 1. Reconcile success rate

| | |
|---|---|
| **SLI** | Fraction of reconciles that do not return an error |
| **Proposed SLO** | 99% over 28 days |
| **Exists today?** | Yes |

```promql
1 - (
  sum(rate(controller_runtime_reconcile_errors_total{controller="cascadepolicy"}[5m]))
  /
  sum(rate(controller_runtime_reconcile_total{controller="cascadepolicy"}[5m]))
)
```

Notes: a healthy reconcile reports `result="requeue_after"` (the operator
always requeues in 10 seconds), so count errors, not `success`. Optimistic
concurrency conflicts count as errors and are retried; a persistent error
rate above the budget means a real fault (RBAC, API server, Prometheus
fatal paths). Burn-rate alerting: page on a 14x burn over 1 hour, ticket on
a 3x burn over 6 hours. Triage with
[operator not reconciling](runbooks/operator-not-reconciling.md).

## 2. Detection-to-mitigation latency

| | |
|---|---|
| **SLI** | Time from a signature's thresholds being crossed to the mitigation patch landing |
| **Proposed SLO** | 95% of trips mitigated within 60 seconds of the crossing |
| **Exists today?** | **No.** Needs a new metric |

What is known: detection runs once per reconcile (10-second requeue) over a
`windowSeconds` (default 30) Prometheus range query, so the floor is roughly
one window plus one interval. The single benchmark run in
[`benchmark-results.md`](benchmark-results.md) measured 32 to 47 seconds from
fault start to detection for latency/error and retry storm; it is one run on a
shared local cluster, not a baseline.

What is missing: the operator never sees the moment the threshold was crossed
(only the first reconcile that observed it), and nothing records the time
between its own decision and the patch succeeding. Proposed additions:
a histogram of the patch-apply duration per signature (for example
`cascade_mitigation_apply_seconds{signature}`), and a gauge of the tripped
state. End-to-end latency (crossing to patch) is best measured by an external
probe that injects a known fault, which is what the k6 benchmark already
does; running it on a schedule would give the series.

## 3. Restore completion

| | |
|---|---|
| **SLI** | Fraction of restoration attempts that complete rather than regress |
| **Proposed SLO** | 90% over 28 days (a lower number means mitigations are being loosened while the dependency is still unhealthy) |
| **Exists today?** | Partly: the ratio, not the duration |

```promql
sum(increase(cascade_restorations_completed_total[28d]))
/
(sum(increase(cascade_restorations_completed_total[28d]))
 + sum(increase(cascade_restoration_regressions_total[28d])))
```

The Grafana dashboard already plots the regression share
(`config/observability/grafana-dashboard.json`). Not measurable today: how
long a policy spends in `Tripped` or `Restoring`, and whether any policy is
stuck. That needs a gauge of policies by phase, for example
`cascade_policy_phase{phase,namespace}` (or kube-state-metrics custom resource
state metrics over `status.phase`), after which an alert on "in `Tripped` or
`Restoring` for more than 30 minutes" can drive the
[stuck in Tripped](runbooks/policy-stuck-in-tripped.md) and
[stuck in Restoring](runbooks/policy-stuck-in-restoring.md) runbooks.

## 4. Admission webhook availability

| | |
|---|---|
| **SLI** | Fraction of admission calls the API server completes successfully against the webhook |
| **Proposed SLO** | 99.9% over 28 days |
| **Exists today?** | Yes, with a caveat |

Because the webhook is registered with `failurePolicy: Fail`, its availability
is the availability of writes to `CascadePolicy`.

```promql
# Seen by the webhook server itself (controller-runtime; the `webhook` label
# is the registered path, confirm it on your /metrics output)
sum(rate(controller_runtime_webhook_requests_total{webhook="/validate-cascade-gideonsanni-dev-v1alpha1-cascadepolicy",code=~"5.."}[5m]))
/
sum(rate(controller_runtime_webhook_requests_total{webhook="/validate-cascade-gideonsanni-dev-v1alpha1-cascadepolicy"}[5m]))

# Seen by the API server (counts calls that never reached a pod; metric and
# label names are kube-apiserver's, so confirm them on your cluster version)
sum(rate(apiserver_admission_webhook_rejection_count{name="vcascadepolicy-v1alpha1.kb.io",error_type="calling_webhook_error"}[5m]))
```

The first series only exists while a pod answers, so an outage where no pod
is reachable looks like missing data, not errors; the second, from
kube-apiserver, covers that case. Pair them with an `up` alert on the
metrics scrape. A validation rejection (HTTP 200 with `allowed: false`) is
the webhook working and is not an availability failure. Triage with
[admission webhook failing](runbooks/webhook-failing.md).

## Not proposed, and why

An SLO on "cascades prevented" or on the protected services' error rate would
measure the mesh and the application as much as the operator; the benchmark
shows that retry-storm mitigation changes load on the dependency far more than
it changes the caller's error rate. Those belong in the protected service's own
SLOs.
