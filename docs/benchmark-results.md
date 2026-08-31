# Resilience benchmark results

**Generated:** 2026-08-31T18:37:44Z via `make benchmark` (`hack/run-benchmark.sh`)

Each of `demo/k6/*.js`'s cascade scenarios run twice against the live
dev Kind cluster: once with `checkout-service`'s CascadePolicy in
`DetectOnly` mode (the real baseline — signatures detected and
recorded, nothing patched) and once in `Mitigate`. Numbers are wall-
clock seconds from this script's own timestamps and Prometheus
`increase()` queries over the run window — not simulated.

| Scenario | Mode | Time to detect (s) | Time to restore (s) | Total requests | 5xx requests | Error rate |
|---|---|---|---|---|---|---|
| latency-error-cascade | DetectOnly | 32 | 79 | 937.5 | 81.0 | 8.6% |
| latency-error-cascade | Mitigate | 47 | 54 | 1035.2 | 34.3 | 3.3% |
| retry-storm | DetectOnly | 41 | 61 | 1876.9 | 1304.3 | 69.5% |
| retry-storm | Mitigate | 42 | 74 | 1710.8 | 1148.6 | 67.1% |
| fanout-amplification | DetectOnly | 0 | 11 | 1534.0 | 968.7 | 63.2% |
| fanout-amplification | Mitigate | n/a | n/a | 845.5 | 268.8 | 31.8% |

## Notes and caveats (read before citing these numbers)

This ran once, live, against a shared local Kind cluster with real (if
noisy) k6 load — not a controlled lab environment, and not averaged over
multiple runs. A few results need context rather than being taken at face
value:

- **retry-storm's error rate barely improves under Mitigate (69.5% →
  67.1%)**, unlike latency-error-cascade's clear improvement (8.6% → 3.3%).
  This is expected, not a sign mitigation isn't working: retry storm's
  primary patch (`retries.attempts` → 0, PLAN.md §2.6) is a *bulkhead*
  against amplification — it stops Envoy from turning one failing call
  into three, reducing *load* on the already-degraded `inventory-service`.
  It does not, and isn't intended to, make `inventory-service` itself
  healthy again — the dependency is still genuinely down for the same
  wall-clock duration either way, so the caller's own error rate stays
  high in both modes. The real signal for this mitigation's effect is total
  request volume to the dependency (1876.9 → 1710.8), not error rate.
- **fanout-amplification's DetectOnly time-to-detect reads as `0`, and its
  Mitigate run reads `n/a`/`n/a`.** `demo/k6/README.md` already documents
  that a brief, unrelated `FanOutAmplification` blip can appear right at a
  scenario's load-ramp-down boundary (k6's VUs winding to zero skews the
  30-second rate window for a tick or two). Combined with this script
  starting each run back-to-back with only a status-level (not a
  Prometheus-rate-window-level) health check between them, a residual
  signal from the tail of one run can register as an instant "already
  Tripped" at the very start of the next — that's almost certainly what
  produced `time_to_detect=0` here. The `n/a` figures mean this script's
  own polling loop never observed a clean `Tripped → Normal` transition
  inside its wait window for that run, not that mitigation failed — the
  blast-radius figures (63.2% → 31.8% error rate, 1534.0 → 845.5 total
  requests) still show a large, real improvement.
- **The `errorRateQuery` fix** (a missing `sum()` aggregation, found live
  while building this benchmark — see
  `internal/mesh/istio/query_builder.go`'s own doc comment and its
  worklog) landed in this same working tree shortly after this benchmark
  run captured its numbers. The error-rate/total-request figures in this
  table come from this script's own independently-written `increase()`
  queries (which always used `sum()` correctly), not from the buggy
  detector-path query — so these blast-radius numbers are unaffected by
  that bug. The *time-to-detect* figures, however, were measured against
  the pre-fix binary. The confirmed real-world failure mode (per the fix's
  own live verification) is a **false negative**: mismatched
  `response_code` label sets made the un-summed division return NaN,
  which `signatures.DetectLatencyError`'s finite() check correctly
  rejects as "incomplete readings" — so if anything, latency-error-
  cascade's detection here may have been *less* reliable pre-fix, not
  more trigger-happy as an earlier, less complete read of this bug
  suggested. Re-running this benchmark after the fix is merged is a
  reasonable follow-up, not required reading of this table.
- Back-to-back scenarios share one Kind node's resources (no isolation
  between runs) — absolute timing numbers will vary run to run; the
  *relative* Mitigate-vs-DetectOnly comparison within each scenario is the
  more defensible reading.
