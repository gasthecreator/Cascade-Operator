# Latency/error-cascade detector wired through the reconcile loop

**Date:** 2026-08-29
**Author:** Cursor
**Type:** feature

## What
Added `internal/signatures.DetectLatencyError` (pure function, no cluster
types) and wired it into `Reconcile` on the existing 10s tick: two PromQL
instant queries per `dependsOn` host, detector, then
`status.phase=Tripped` / `lastSignature=LatencyErrorCascade` /
`lastTrippedAt`. No Istio client, no restore ramp.

## Why
PLAN.md §2.5 (decoupled detectors) and the build-order note: one signature
through metrics → detector → status before the other two detectors or the
§2.6 patch matrix. The Prometheus client already existed and was injected
but never called.

## How
- Detector takes floats (`LatencyErrorInput`), not `metrics.Snapshot` and
  not the CRD. The reconciler maps Snapshot → numbers. That keeps
  `internal/signatures` free of Kubernetes and of `metrics.Querier`, so
  `go test ./internal/signatures` needs no envtest.
- Trip is **AND**: p99 >= `latencyP99Ms` **and** error rate >=
  `errorRateFraction`. Latency alone is a slow-but-healthy dep; errors
  without latency are a fast fail, not a propagating stall. Inclusive so a
  policy of 500ms / 0.05 trips at those values. NaN/Inf (empty histogram
  quantile) does not trip.
- Confidence is 0.5 at both thresholds and rises toward 1 as both signals
  exceed (capped). Evidence is a single string the reconciler logs.
- Signature type is **not** reinvented in this package: the reconciler
  writes `cascadev1alpha1.SignatureLatencyErrorCascade` on trip.
- PromQL is the slice's specified instant queries, `destination_service`
  matched to the `dependsOn` FQDN via `%q`, window from
  `spec.thresholds.windowSeconds`. No `response_flags` — that's still
  unverified Istio scrape territory for retry-storm. Did not add
  `sum by (le)`; if a query returns several series we take the **max**
  (conservative). Empty snapshot skips that edge. Query errors log and
  continue; they do not fail the reconcile (same spirit as missing Istio
  objects in §2.3).
- Left `status.phase=Tripped` in place when the detector later goes quiet —
  restoration is the next slice, not an unpatch in this one.
- Did not touch `internal/metrics/client.go`. Claude's non-200
  `parseErrorBody` path stays as-is.

## Files touched
- `internal/signatures/verdict.go` — `Verdict`
- `internal/signatures/latency_error.go` — `DetectLatencyError`
- `internal/signatures/latency_error_test.go` — table tests (AND, boundary, NaN)
- `internal/controller/promql.go` — query builders + snapshot max
- `internal/controller/promql_test.go` — exact query strings, no `response_flags`
- `internal/controller/cascadepolicy_controller.go` — Query + detect on the 10s tick
- `internal/controller/cascadepolicy_controller_test.go` — nil Metrics, under
  threshold, trip, Query error
- `PLAN.md` — checklist + status line
- `docs/worklog/README.md` — index this entry

## Testing
- `go test ./internal/signatures/` — pass, 87.0%, no cluster.
- `make test` — controller 88.7% (envtest + `fakeQuerier`), metrics 80.4%
  unchanged.
- `make lint` — 0 issues after fixing logcheck (no logger+context params)
  and goconst in the detector tests.

## Follow-ups / known gaps
- Next slice: Istio patch layer for the latency/error primary
  (`DestinationRule` outlier detection) and then the restore ramp. Not this PR.
- `histogram_quantile` without `sum by (le)` may be noisy on a real Istio
  scrape (one series per reporter/source). Taking max is the stand-in;
  change the PromQL via PROPOSALS.md if a live scrape shows it's wrong.
- Retry-storm / fan-out detectors still unbuilt. `response_flags=UR` still
  unverified.
