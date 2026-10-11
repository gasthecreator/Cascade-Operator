# Kind integration tests: real reconcile loop, unstructured wire-format assertions

**Date:** 2026-08-31
**Author:** Cursor
**Type:** test

## What
Added `test/integration/` — a `//go:build integration` package run via
`make test-integration` against the existing dev Kind+Istio cluster
(`kind-cascade-operator` context), not the scaffold `test/e2e` ephemeral
cluster. First test: `TestRetryStormTripAndRestoreWireFormat` drives
`CascadePolicyReconciler.Reconcile` directly (no operator subprocess) with a
stub `hostAwareQuerier` for deterministic detection and a real
controller-runtime client for patches.

After retry-storm trip, reads `inventory-service` VirtualService and
DestinationRule back via `unstructured.Unstructured` and asserts raw stored
JSON contains `"attempts":0` and `"maxRetries":1`. After six healthy
reconcile ticks, asserts VS restores to demo fixture `attempts:3` and DR
has no `trafficPolicy`. Cleanup runs on failure via `t.Cleanup`.

Also updated `demo/k6/retry-storm.js` header and `demo/k6/README.md` to
reflect mitigation fixes (webhook, zero-value patch, maxRetries=1) with
worklog pointers instead of the stale "patch doesn't work" gap.

## Why
PLAN.md's last open checklist item. The last three slices proved that
typed `networkingv1` reads and fake-client tests cannot catch wire-format
bugs — integration coverage must read raw apiserver JSON after real
`client.Patch` calls on a live Istio CRD.

## How
**Reconciler invocation (preferred over subprocess):** builds kubeconfig
from `INTEGRATION_CONTEXT` (default `kind-cascade-operator`), wires
`CascadePolicyReconciler` with `metrics.Querier` stub. Prometheus
connectivity is intentionally *not* required — stub metrics keep trip/
restore deterministic; what we prove is patch serialization on the real
apiserver. Organic k6+operator remains the manual path.

**Fixtures:** `kubectl apply -f demo/k8s/` plus explicit
`inventory-retry-vs.yaml`; deletes/recreates `payments-service`
DestinationRule before each run so leftover `managed-by` from prior fan-out
runs does not make retry-storm restore walk a foreign edge.

**Makefile:** `test-integration` target; default `make test` excludes
`test/integration` from `go list`.

## Files touched
- `test/integration/doc.go`, `cluster.go`, `retry_storm_test.go`
- `Makefile` — `test-integration`, exclude integration from unit `test`
- `demo/k6/retry-storm.js`, `demo/k6/README.md` — stale gap removed
- `PLAN.md` — checklist item [x] only (no §2 architecture edits)
- `docs/worklog/README.md` — index this entry

## Testing
- `make lint` — 0 issues. `make test` — pass (integration excluded).
- **`make test-integration` against dev Kind cluster — pass.** Raw JSON
  logged by the test (trip):
  - VirtualService: `"retries":{"attempts":0}` (no `retryOn`/`perTryTimeout`
    on trip — primary patch path).
  - DestinationRule: `"maxRetries":1` under `trafficPolicy.connectionPool.http`.
- Restore (same run, logged):
  - VirtualService: `"attempts":3,"perTryTimeout":"2s","retryOn":"5xx,..."`
  - DestinationRule: `{"host":"inventory-service.default.svc.cluster.local"}`
    only — no `trafficPolicy`.
- **Prometheus:** not used; stub querier by design. No port-forward or
  local operator process involved in this suite.

## Follow-ups / known gaps
- Extend coverage to latency/error and fan-out signatures when needed.
- CI still does not run integration tests (Kind+Istio in Actions remains
  deferred per PLAN.md §2.8).
