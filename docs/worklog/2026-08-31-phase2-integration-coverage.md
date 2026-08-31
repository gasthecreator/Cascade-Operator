# Phase 2: integration coverage for latency/error-cascade and fan-out amplification

**Date:** 2026-08-31
**Author:** Claude
**Type:** test

## What
Extended `test/integration/` (PLAN.md §5 Phase 2) with
`TestLatencyErrorCascadeTripAndRestoreWireFormat` and
`TestFanOutTripAndRestoreWireFormat`, mirroring
`TestRetryStormTripAndRestoreWireFormat`'s exact discipline: drive
`CascadePolicyReconciler.Reconcile` directly against the dev Kind+Istio
cluster, stub Prometheus for deterministic detection, read the patched
Istio objects back as raw `unstructured` JSON, assert literal field values.
All three of the project's signatures now have this coverage; before this
slice only retry storm did.

## Why
The retry-storm bug thread (zero-value serialization, then Istio's own
Pilot translation limit) only surfaced because live checks eventually went
deep enough to read raw JSON instead of a typed struct. Latency/error-cascade
and fan-out amplification had never been checked at that level — their
fields are mostly nullable wrapper/duration types that structurally don't
have the same `omitempty` failure mode, but "structurally shouldn't" isn't
the same as "verified doesn't," which is the whole reason this slice exists
rather than being skipped as low-value.

## How
- Extended the shared `hostAwareQuerier` stub in `cluster.go` (didn't
  duplicate it into three separate stub types) with two new fields,
  `inventoryLatencyError` and `inventoryFanOut`, alongside the existing
  `inventoryRetryStorm`/`healthy`. Each field elevates exactly the PromQL
  shape and host that signature's own query construction uses
  (`internal/controller/promql.go`), above the exact thresholds in
  `demo/k8s/cascadepolicy.yaml` (`latencyP99Ms:500`, `errorRateFraction:0.05`,
  `fanOutMultiplier:2.0`); everything else stays at a safe healthy default.
- The one real subtlety, read from source before writing a single line of
  stub logic rather than guessed: `fanOutRatioQuery` and
  `retryStormRatioQuery` both use `reporter="destination"`, but only
  `fanOutRatioQuery` also names the caller host (`policy.Spec.Service`) in
  its text — `retryStormRatioQuery` never does, even for the identical
  dependency host. That's the only reliable way to tell the two apart from
  raw PromQL text, and it's what the fan-out branch checks
  (`strings.Contains(promql, inventoryName) && strings.Contains(promql,
  "checkout-service")`) rather than a looser match that could have tripped
  both signatures at once for the same test.
- Also confirmed, before writing the switch: `detectSignatures` evaluates
  latency/error before retry storm before fan-out, per host, first-trip-wins
  — so the fan-out test's stub must keep latency/error's and retry storm's
  own queries healthy for `inventory-service`, not just elevate fan-out's,
  or fan-out would never even be reached for that host.
- One assertion was deliberately left loose on the first run rather than
  guessed: the VirtualService `timeout` secondary's exact JSON string.
  Istio's client-go types marshal `durationpb.Duration` using protojson's
  canonical string form, not Go's native `time.Duration.String()` — the two
  happen to coincide for whole seconds (retry storm's `"2s"`) but not for
  `latencyP99Ms:500` (500ms). Ran the test once with a presence-only check,
  read the real raw JSON (`"timeout":"0.500s"`), then tightened the
  assertion to the confirmed literal string rather than assuming a format.

## Files touched
- `test/integration/cluster.go` — extended `hostAwareQuerier` with two new
  signature-specific boolean fields
- `test/integration/latency_error_test.go` — new
- `test/integration/fanout_test.go` — new
- `PLAN.md` — §5 Phase 2 checklist only
- `docs/worklog/README.md` — index this entry

## Testing
- `go build ./...`, `gofmt -l .`, `go vet ./...`, `go vet -tags=integration ./...` — all clean.
- `go test $(go list ./... | grep -v /e2e | grep -v /test/integration) -race -count=1 -cover` —
  full existing suite, unaffected: controller 79.3%, metrics 80.4%,
  mitigation 90.9%, signatures 94.1%.
- `make lint` — 0 issues.
- **`make test-integration` against the live dev Kind+Istio cluster — all
  three tests pass, run together, twice in a row:**
  - `TestLatencyErrorCascadeTripAndRestoreWireFormat` (4.3–4.7s): trip shows
    `"consecutive5xxErrors":3`, `"interval":"5s"`, `"baseEjectionTime":"30s"`
    on the DestinationRule and `"timeout":"0.500s"` on the VirtualService;
    restore clears both back to the fixture baseline (no `trafficPolicy`,
    no `timeout`).
  - `TestFanOutTripAndRestoreWireFormat` (4.5–4.6s): trip shows
    `"http1MaxPendingRequests":1`, `"http2MaxRequests":1`; restore clears
    `trafficPolicy` entirely.
  - `TestRetryStormTripAndRestoreWireFormat`: unaffected, still passes.
- Confirmed the cluster is left fully clean after a complete run: both
  `payments-service`/`inventory-service` DestinationRules and the
  `inventory-service` VirtualService back to their committed fixtures, no
  cascade annotations, `CascadePolicy` status fully reset
  (`Phase: Normal`, empty `lastSignature`).

## Follow-ups / known gaps
- None specific to this slice. Phase 3 (admission webhook) is next per
  PLAN.md §5's sequencing.
