# Retry-storm detector, status only

**Date:** 2026-08-30
**Author:** Cursor
**Type:** feature

## What
Added `internal/signatures.DetectRetryStorm` (pure function, no cluster types)
and a dest:source PromQL query, then evaluated it on every `dependsOn` host
each tick after the latency/error-cascade detector. A retry-storm trip sets
`status.phase=Tripped` / `lastSignature=RetryStorm` / `lastTrippedAt` and
does **not** patch Istio. No VirtualService change, no restore-state-machine
change.

## Why
PLAN.md's sequencing: once one signature proved detect → trip → patch →
restore, the other two detectors are copies of the same interface. Kind +
Istio 1.30.4 scrape evidence (worklog 2026-08-29) unblocked the signal:
source reporter counted 35 `response_flags=URX` (retries fully exhausted)
while destination counted 140 503s in the same window — 4×, matching
`retries.attempts: 3` (1 original + 3 retries). `URX` only fires when *all*
retries fail, so it would miss amplification where a retry later succeeds.
The destination:source request-count ratio catches both.

`retryStormMultiplier` was still commented as "retries/sec vs baseline" on
the CRD from before that scrape. No baseline is stored locally — implicit
baseline is 1 (one destination attempt per source request), which is what
§2.4 already prefers (Prometheus owns the window).

## How
- **Detector:** `RetryStormInput` is plain floats (`DestSourceRatio`,
  `Multiplier`), not `metrics.Snapshot` and not the CRD. Trip is inclusive
  (`ratio >= multiplier`), same convention as `DetectLatencyError`. NaN/Inf
  (empty `rate()`, divide-by-zero) do not trip. Confidence is 0.5 at the
  threshold and 1.0 at 2× (single-signal analog of the latency detector's
  0.5-at-threshold formula). Signature type is
  `cascadev1alpha1.SignatureRetryStorm`, already defined, unused until now.
- **PromQL:** one instant query, `sum(rate(...reporter="destination")) /
  sum(rate(...reporter="source"))`, `destination_service` matched to the
  `dependsOn` FQDN via `%q`, window from `spec.thresholds.windowSeconds`.
  `sum()` collapses leftover labels (`response_code`, pod, …) so the ratio
  is service-level. No `response_flags` / `URX` filter — that would miss
  successful retries. Exact-string test locks the query.
- **Priority:** per host, latency/error cascade first, then retry storm.
  First trip wins; status tracks one `lastSignature`. If both could fire on
  the same host, latency/error cascade wins and still patches
  `DestinationRule` outlierDetection. Retry-storm-only trips skip
  `applyLatencyErrorMitigation` regardless of `spec.mode` — there is
  nothing to gate, same as the first detector-wiring slice before any
  mitigation existed.
- **Restoration:** not touched. `beginRestore` already calls
  `listManagedEdges`, finds zero managed DestinationRules (retry storm never
  annotated one), and snaps to `Normal`. Confirmed by a fake-client test
  with an unmanaged DR present so we don't accidentally start restoring
  someone else's object.
- **CRD comment:** updated `retryStormMultiplier` on the Go type (and thus
  the generated CRD description) to dest:source. PLAN.md §2.3's YAML
  example still has the old `# retries/sec vs baseline` — that's a §2 edit
  for Claude, not this slice.

Did not add concurrent-signature status tracking. One `lastSignature` is
the CRD shape; changing it would be a PROPOSALS.md entry.

## Files touched
- `internal/signatures/retry_storm.go` — `DetectRetryStorm`
- `internal/signatures/retry_storm_test.go` — table tests (below / at /
  above threshold, NaN/Inf)
- `internal/signatures/latency_error_test.go` — shared
  `evidenceIncompleteReadings` const (goconst across the two detector
  test files)
- `internal/controller/promql.go` — `retryStormRatioQuery`
- `internal/controller/promql_test.go` — exact query string, no
  `response_flags`
- `internal/controller/cascadepolicy_controller.go` — `detectSignatures`
  (latency then retry storm per host); retry-storm trips skip mitigation
- `internal/controller/cascadepolicy_controller_test.go` — fakeQuerier
  returns dest:source ratio for the destination-reporter query
- `internal/controller/retry_storm_test.go` — fake-client: status-only
  trip, healthy → Normal, latency wins when both fire
- `internal/controller/restore_test.go` — healthy querier includes ratio
  1.0
- `api/v1alpha1/cascadepolicy_types.go` — field comment
- `config/crd/bases/cascade.gideonsanni.dev_cascadepolicies.yaml` — generated
  description
- `PLAN.md` — status line + checklist (not §2)
- `docs/worklog/README.md` — index this entry

## Testing
- `go test ./internal/signatures/` — pass, 91.9%, no cluster.
- `make test` — controller 82.9% (envtest + fake-client); metrics 80.4% and
  mitigation 82.6% unchanged.
- Fake-client: retry-storm trip leaves DestinationRule unannotated and does
  not create one; Tripped + healthy snaps to Normal (not Restoring);
  both-signal host records `LatencyErrorCascade` and still patches
  outlierDetection.
- `make lint` — 0 issues after extracting a shared
  `evidenceIncompleteReadings` test const (goconst across the two detector
  test files).

## Follow-ups / known gaps
- VirtualService retry-budget patch (§2.6 primary for retry storm) is a
  later slice, same sequencing as latency/error cascade.
- Fan-out detector still unbuilt.
- PLAN.md §2.3 CRD example still comments `retryStormMultiplier` as
  retries/sec vs baseline. Detector implements dest:source; Claude can
  sync the example if wanted.
- Did not re-run the live Istio scrape in this slice. Ratio of 4.0 at
  threshold 3.0 is taken from the 2026-08-29 Kind evidence. If real mixed
  success/failure retry traffic makes the ratio noisy (scrape skew between
  reporters, `sum()` including unrelated codes), that's a PROPOSALS.md
  entry with what the scrape actually showed — not a guess in this code.
