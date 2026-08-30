# Review: latency/error-cascade detector slice

**Date:** 2026-08-29
**Author:** Claude
**Type:** docs

## What
Reviewed `feat/latency-error-detector` against PLAN.md §2.5/§2.6 by
independently rebuilding, testing, and reading the code end to end.

## Why
Same reviewer role as the previous three slices.

## How
- `go build`, `gofmt -l`, `go vet`, `go test ./internal/signatures/... -cover`,
  `make test`, `make lint`, `make verify-generate` — all run independently.
  Numbers matched exactly: signatures 87.0%, controller 88.7% (up from
  81.2%), metrics unchanged at 80.4%, 0 lint issues, no drift.
- Diffed `PLAN.md`: status line and checklist boxes only — protocol held.
- Read `internal/signatures/latency_error.go` line by line: pure function,
  `LatencyErrorInput` is plain floats (not `metrics.Snapshot`, not the CRD),
  AND logic on p99/error-rate (correctly reasoned — latency alone is a
  slow-but-healthy dependency, errors alone are a fast fail rather than a
  cascade), inclusive threshold (`>=`), NaN/Inf guarded before comparison,
  confidence formula checked by hand against the "both exactly at threshold"
  (0.5) and "well above" (capped at 1.0) test cases — both correct.
- Read `internal/controller/promql.go` and the reconciler wiring: the two
  PromQL queries match what was specified — `destination_service` matched
  verbatim to the `dependsOn` FQDN, window from `thresholds.windowSeconds`,
  `response_code=~"5.."` for the error ratio, no `response_flags` (a test
  explicitly asserts this — `TestErrorRateQuery` fails if `response_flags`
  ever sneaks in, which is a good tripwire for later slices). First tripped
  `dependsOn` host wins; query failures on one host log and `continue`
  rather than failing the whole reconcile, matching the existing
  `DependencyObjectMissing` pattern's spirit of not letting one bad edge take
  down the loop.
- Verified the "leave Tripped alone" and "don't bump `LastTrippedAt` on
  repeated detection of an already-tripped state" behavior by tracing the
  `origPhase`/`origSig` comparison — `LastTrippedAt` only updates on the
  `Normal→Tripped` transition, and `Status().Update` is skipped entirely when
  nothing changed, avoiding needless API writes on a steady-state tripped
  policy. Correct and more careful than the slice strictly required.
- Read the new envtest cases (`fakeQuerier` implementing `metrics.Querier`)
  and the `promql_test.go` exact-string tests — cover nil `Metrics`,
  under-threshold, trip, and Query-error paths as described, no gaps.

## Verdict
**Approved, no changes requested.** This is the cleanest slice yet — design,
tests, and worklog all matched on the first read. The one open item
(`histogram_quantile` without `sum by (le)` possibly needing correction once
real Istio traffic exists) is correctly self-flagged as a live-data question
that no unit test can resolve pre-cluster, and routed through PROPOSALS.md
per the protocol rather than guessed at now. Leaving it as documented debt is
the right call, not a gap to close in this review.

## Files touched
- `docs/worklog/2026-08-29-review-latency-error-detector.md` — this file
- `docs/worklog/README.md` — index this entry

## Testing
See "How" above.

## Follow-ups / known gaps
- Next slice (Cursor's): Istio patch layer for the latency/error-cascade
  primary (`DestinationRule` outlier detection), per §2.6 — no restore ramp
  yet.
- Carried forward, not resolved here: `histogram_quantile` aggregation
  correctness pending a real Istio scrape; `response_flags=UR` for
  retry-storm still unverified.
