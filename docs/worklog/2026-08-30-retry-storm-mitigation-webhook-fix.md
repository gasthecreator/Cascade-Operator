# Retry-storm mitigation: clear retryOn/perTryTimeout/backoff on trip so Istio's validating webhook stops rejecting the patch

**Date:** 2026-08-30
**Author:** Cursor
**Type:** fix

## What

`ApplyRetryStormTrip` (`internal/mitigation/retries.go`) now clears a
route's `RetryOn`, `PerTryTimeout`, and `Backoff` fields alongside setting
`Attempts = 0` on trip. Previously it left those three fields exactly as
they were pre-trip, which is a combination Istio's validating webhook
rejects outright: `configuration is invalid: http retry policy configured
when attempts are set to 0 (disabled)`. That meant the `VirtualService`
`Update` never actually succeeded against a route that already had a real
retry policy — which, per the previous slice's live k6 evidence, is exactly
the shape a real retry storm's pre-existing policy always has (the storm
requires a lenient `retryOn` to amplify in the first place).

This is implementation of the already-`[APPROVED]` `PROPOSALS.md` entry —
no new architecture decision here, just carrying out direction 1 as
resolved.

## Why

`PLAN.md`'s caveat note (added 2026-08-30, this slice's predecessor) and the
resolved `PROPOSALS.md` entry both call this out explicitly: the previous
slice's `retry-storm.js` k6 run against `demo/k8s/inventory-retry-vs.yaml`
(a realistic fixture — `attempts: 3, retryOn: 5xx,reset,connect-failure,
refused-stream, perTryTimeout: 2s`) tripped the detector correctly but the
mitigation `Update` was rejected by the admission webhook every single
reconcile. Claude's review of that slice independently reproduced both the
rejection and the fix live against the same fixture before approving
direction 1 (clear the fields) over direction 2 (`attempts: 1` instead of
`0` — also webhook-clean per that spike, but reopens an already-reasoned
amplification-headroom decision for no real benefit). Nothing is lost by
clearing the fields at trip time: the full pre-trip block — including
`RetryOn`/`PerTryTimeout`/`Backoff` — is already captured in
`AnnotationOriginalRetries` by `snapshotRoutesRetriesJSON` *before* the trip
loop runs, and both `applyOriginalRetries` (used by
`ApplyRetryStormRestoreStep` at the final step) and `CompleteRetryStormRestore`
already write back the whole captured block, not just `Attempts`.

## How

Three-line change in `ApplyRetryStormTrip`'s per-route loop: after
`route.Retries.Attempts = TripRetryAttempts`, also set
`route.Retries.RetryOn = ""`, `route.Retries.PerTryTimeout = nil`, and
`route.Retries.Backoff = nil` (the latter two are `*durationpb.Duration`
fields on `apinet.HTTPRetry`, so `nil` is "unset," matching how a route
with no explicit retries block already renders empty). Rewrote the
function's doc comment, which previously said these fields "are preserved
and only attempts changes" — that sentence was the exact source of the bug
and would have been actively misleading left in place next to the new
behavior.

Deliberately did not touch anything on the restore side
(`internal/mitigation/retry_restore.go`). Traced through why that's safe
rather than assuming it: `applyInterpolatedRetries` (the mid-ramp path,
steps 0–3) only ever reads/writes `route.Retries.Attempts` — it never reads
or writes `RetryOn`/`PerTryTimeout`/`Backoff` at all, so clearing them at
trip time doesn't change its behavior; `applyOriginalRetries` (the final-step
path) always constructs a fresh `*apinet.HTTPRetry` from the stored
snapshot and assigns it wholesale, so it doesn't matter what state those
fields were left in on the live object beforehand. There's no intermediate
restore state where `Attempts` is a small non-zero ramp value *and*
`RetryOn`/`PerTryTimeout` are already back — they only reappear together,
atomically, at the step that also flips `Attempts` to the true original.

## Files touched

- `internal/mitigation/retries.go` — `ApplyRetryStormTrip` clears
  `RetryOn`/`PerTryTimeout`/`Backoff` alongside `Attempts = 0`; doc comment
  rewritten to explain why (webhook rejection) instead of claiming the old,
  now-false "preserved" behavior.
- `internal/mitigation/retries_test.go` — `TestApplyRetryStormTripMultiRoute`'s
  "explicit-retries" fixture route now also sets `Backoff` (previously only
  `Attempts`/`RetryOn`/`PerTryTimeout`), so the trip-clears-everything
  assertion has real coverage on all three fields, not just two. Assertions
  flipped from "not clobbered" to "cleared." The original-snapshot assertion
  for that route was extended to also check `Backoff` came through correctly
  in `AnnotationOriginalRetries` (unaffected by the trip-time change, but
  worth re-confirming rather than assuming since the fixture itself changed).
- `internal/controller/retry_restore_test.go` —
  `TestRetryStormRestoreAdvancesEachStepThenCompletes` previously asserted
  `route[1].Retries.RetryOn == "5xx"` at every mid-ramp tick, which was only
  ever true because the old trip behavior left it untouched from the
  pre-trip fixture. Flipped to assert it stays cleared (`""`) through every
  tick before `RestoreFinalStep`, and comes back only at the tick where the
  full original block is written back (step `RestoreFinalStep`, one tick
  before the phase itself flips to `Normal` — `ApplyRetryStormRestoreStep`
  writes the true original at `step >= RestoreFinalStep` a tick before
  `advanceRestore` transitions the phase). This is the one test that this
  slice's own changes actually broke; everything else (restore, regression,
  cross-signature dispatch) needed no changes because their assertions never
  depended on `RetryOn`/`PerTryTimeout` surviving trip in the first place.
- `PLAN.md` — updated the existing retry-storm caveat note: the fix is now
  implemented, not just resolved-but-pending, with a note that this pass
  couldn't re-confirm live (cluster unreachable this session — see Testing).

## Testing

- `go test ./internal/mitigation/... ./internal/controller/... -run Retry
  -v`: all retry-storm-related tests pass, including the one test this
  change broke (`TestRetryStormRestoreAdvancesEachStepThenCompletes`,
  fixed as described above) and every restore/regression/cross-signature
  dispatch test (`TestCompleteRetryStormRestoreRestoresOriginalAndStripsAnnotations`,
  `TestRetryStormRestoreCompleteWithStoredOriginalValues`,
  `TestRetryStormRestoreRegressionReTripsAndBumpsLastTrippedAt`,
  `TestRestoreDispatchTouchesOnlyVirtualServiceForRetryStorm`, etc.) — all
  passed without any changes, confirming the restore path really is
  unaffected, not just assumed to be.
- `make test` (full suite, envtest-backed): all packages pass —
  `internal/controller` 77.3%, `internal/mitigation` 89.6%,
  `internal/signatures` 94.1%, `internal/metrics` 80.4%.
- `make lint`: `0 issues.`
- `gofmt -l .`: no output (clean).

**Live confirmation attempted, not completed.** The demo fixtures
(`demo/k8s/inventory-retry-vs.yaml`, the `checkout-service` `CascadePolicy`)
are still deployed on the `cascade-operator` Kind cluster from the previous
slice, and the plan was to re-run `hack/run-k6-demo.sh retry-storm` (or a
smaller manual trip) to watch the operator's `Update` actually succeed this
time. The machine this session ran on was under heavy, unrelated resource
pressure — a separate project's containers (three Cassandra nodes, three
Kafka brokers, Prometheus, Grafana) were running alongside the Kind
control-plane container, `top` showed load average ~16 and 74% system CPU,
and even `docker ps` took over 80 seconds to return. The Kind control-plane
container itself had been Docker-paused (likely by Docker Desktop's own
resource management under that pressure); unpausing it did not bring
`kubectl` back within a reasonable wait — `TLS handshake timeout` on every
attempt, including with `--request-timeout=15s`. Given the fix is a small,
mechanical field-clearing change with direct unit-test coverage of the
exact clobbered-field behavior, and Claude's review of the previous slice
already independently reproduced this *exact* fix working live against
this *exact* fixture (documented in `PROPOSALS.md`'s resolved entry) before
approving it, shipping on fake-client test confidence rather than forcing a
live run against a resource-starved cluster was the right call this time —
but it's worth a real live re-check next time the cluster is in a healthy,
uncontended state, same as the previous slice's own "worth checking once
healthy again" note that this whole slice exists to close out.

## Follow-ups / known gaps

- Live re-confirmation of this exact fix (not just Claude's independent
  spike) against the real `inventory-service` fixture is still open —
  next session with a healthy, uncontended Kind cluster should run
  `hack/run-k6-demo.sh retry-storm` and confirm no admission rejection
  appears in the operator's logs, and that `kubectl get virtualservice
  inventory-service` actually shows the `cascade.gideonsanni.dev/managed-by`
  annotation and `attempts: 0` mid-run (the two things the previous slice's
  worklog showed were *not* happening).
- No other gaps. This was a scoped, single-function fix with no CRD change,
  no trip-value change, and no effect on the other two signatures' mitigation
  paths (verified by the unchanged cross-signature dispatch tests, not just
  assumed).
