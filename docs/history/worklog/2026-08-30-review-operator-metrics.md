# Review: operator self-metrics — live confirmation completed

**Date:** 2026-08-30
**Author:** Claude
**Type:** docs

## What
Reviewed `feat/operator-metrics` by independently rebuilding/testing, and —
with the Kind cluster finally healthy again — completed the live
confirmation that the previous two sessions both attempted and couldn't
finish due to real resource contention.

## Why
Same reviewer role as every slice. This one's live confirmation had been
blocked twice in a row for reasons unrelated to the code itself, so once the
cluster was actually available, finishing that check properly was worth
doing rather than shipping on fake-client confidence a third time.

## How
- Diffed `PLAN.md`: single checklist line only.
- `go build`, `gofmt -l`, `go vet` — clean. `go test
  ./internal/controller/... -race -count=1`, repeated three times with
  `go clean -testcache` between runs — all three passed, confirming the
  non-flaky claim rather than trusting the "ran it five times" report at
  face value. `make lint` (0 issues) and `make verify-generate` (no drift)
  also independently run.
- Read `metrics.go`: four counters registered once on
  controller-runtime's existing `ctrlmetrics.Registry` (not a second
  registration path), label cardinality reasoning is sound (signature is a
  3-value enum, kind is one of two Istio object kinds, dependency is
  bounded by a policy's own `dependsOn` list). Confirmed by `grep` that
  `completeLatencyErrorRestore` genuinely has exactly the two call sites
  claimed (the ramp's final step and the handoff force-complete), so the
  "one insertion point covers both paths" claim is verified, not just
  asserted.
- Read `metrics_test.go`'s no-`t.Parallel()` reasoning: accurate description
  of how Go's test runner sequences non-parallel and parallel top-level
  tests, and the right call given ~30 other tests in the package share
  label values and mostly run in parallel.
- **Completed the live verification**: brought the Kind cluster back up,
  found and killed a stray leftover `go run` process from an earlier
  session that was squatting on port 8081 (blocking a fresh manager from
  starting), then ran the actual operator locally against the live cluster
  with a real Prometheus port-forward. Induced fan-out amplification the
  same way the k6/evidence slices did (`payments-service`'s `/control/fail`
  plus a checkout traffic burst), watched it trip and patch twice in the
  logs, then — because I stopped generating traffic rather than explicitly
  healing payments — watched the fan-out ratio naturally decay and the
  policy run a full restoration ramp to completion on its own. Queried
  `curl localhost:8080/metrics` afterward:

  ```
  cascade_signatures_detected_total{signature="FanOutAmplification",dependency="payments-service..."} 2
  cascade_mitigation_patches_applied_total{signature="FanOutAmplification",kind="DestinationRule"} 2
  cascade_restorations_completed_total{signature="FanOutAmplification"} 1
  ```

  All three values match the log exactly (two trip+patch cycles, one
  completed restoration ramp traced step by step in the logs from
  `restoreStep: 0` through `restoreStep: 4` to "Completed restoration
  ramp"). `cascade_restoration_regressions_total` wasn't exercised in this
  run (no regression occurred), but that path is already covered by the
  fake-client unit tests and is a harder scenario to trigger live without
  more deliberate timing.
- Cleaned up afterward: killed the local manager and port-forward, healed
  `payments-service` back, confirmed the `CascadePolicy` settled at
  `Phase: Normal` — left the cluster in the same clean state I found it.

## Verdict
**Approved, no changes requested — and live-confirmed this time.** The
implementation, test design, and label-cardinality reasoning were all
already solid on read-through; what this review adds is that three of the
four metrics are now proven working against a real running operator and a
real Istio mesh, not just fake-client tests. The stray process on 8081 was
leftover session debris, not a defect in this slice.

## Files touched
- `docs/worklog/2026-08-30-review-operator-metrics.md` — this file
- `docs/worklog/README.md` — index this entry

## Testing
See "How" above — includes genuine live verification against the real
cluster, completing what the last two sessions attempted but couldn't
finish.

## Follow-ups / known gaps
- `cascade_restoration_regressions_total` is unit-tested but not yet
  live-confirmed — worth doing opportunistically next time the cluster is
  up and something induces a regression naturally (e.g. during a k6 run).
- Remaining Istio patch secondaries and the Kind-based integration test
  suite are the only checklist items left open.
