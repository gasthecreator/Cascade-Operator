# Operator self-metrics: signatures detected, patches applied, restorations completed/regressed

**Date:** 2026-08-30
**Author:** Cursor
**Type:** feature

## What

Four new Prometheus counters, registered on controller-runtime's existing
metrics registry (the same one `cmd/main.go`'s manager already serves on
the metrics endpoint — no second registration path, no code change to
`cmd/main.go` needed):

- `cascade_signatures_detected_total{signature,dependency}` — every
  confirmed trip a detector reports to `Reconcile`.
- `cascade_mitigation_patches_applied_total{signature,kind}` — every
  successful `Update` of the Istio object a signature's primary patch
  touches (`kind` is `DestinationRule` or `VirtualService`).
- `cascade_restorations_completed_total{signature}` — every time a
  signature's mitigation reaches its true pre-trip state, whether via the
  gradual ramp's final step or a same-object signature handoff's
  synchronous force-complete.
- `cascade_restoration_regressions_total{signature}` — every time a
  signature re-trips while its own restoration ramp is still in progress
  (distinct from a handoff, where a *different* signature trips).

## Why

Per `PLAN.md`'s §3 checklist, this was the one remaining item that doesn't
need a live Kind cluster to build or verify honestly — unlike the two other
open items (the Istio patch secondaries, the integration test suite), which
both need real admission-webhook behavior to trust, the same lesson this
session's earlier slice (`fix/retry-storm-mitigation-webhook`) learned the
hard way. This machine has now hit the same Kind-cluster resource-pressure
wall three sessions running (documented in that slice's worklog and the
root-README slice's), so building more Istio-patching logic blind right now
would repeat a mistake already on record, while self-instrumentation is
pure Go with no cluster dependency at all.

It's also the answer to a real gap: nothing currently tells you *how many*
times this operator has actually detected or acted on anything without
tailing logs or running `kubectl get -w` by hand. "How would you know this
is working in production" is exactly the kind of question this project's
own stated goal (PLAN.md §1: interview-defensible, not feature-complete)
should have a real answer to.

## How

**Where the counters live and how they're registered.** New file
`internal/controller/metrics.go`: four `prometheus.CounterVec`s as
package-level vars, registered once via `ctrlmetrics.Registry.MustRegister`
in an `init()`. This is the standard controller-runtime pattern (the same
one the kubebuilder book documents) — `metricsserver.Options` in
`cmd/main.go` has no `Registry` field to set; it always serves whatever is
registered on the package-level `sigs.k8s.io/controller-runtime/pkg/metrics.Registry`,
so nothing in `cmd/main.go` needed to change for these to show up on the
existing `/metrics` endpoint.

**Label cardinality, thought through rather than assumed.** `signature` is
the CRD's own three-value enum (`cascadev1alpha1.SignatureType`); `kind` is
one of exactly two Istio object kinds the mitigation package ever patches;
`dependency` is bounded by however many `dependsOn` hosts a single policy
declares — the same set already used as a log field on every detector
evaluation, not an arbitrary/unbounded string. Restoration's two counters
deliberately do *not* carry a `dependency` label: `complete*Restore`
operates on every managed edge for a policy at once (a whole-policy
event), not a single host, so adding one would either be misleading
(attributed to one arbitrary edge) or require iterating labels per edge for
an event that isn't really per-edge.

**Where each counter is incremented, and why those exact call sites:**

- `signaturesDetectedTotal` — inside `Reconcile`'s trip branch
  (`cascadepolicy_controller.go`), in the same `if mitErr == nil` guard
  that already gates the existing "cascade signature tripped" log line.
  Reused that guard rather than inventing a new one: a same-tick handoff
  whose force-complete failed already skips that log line today (Reconcile
  returns the error and will retry the same trip next tick), so counting
  the trip there too would double-count on the eventual successful retry.
- `mitigationPatchesAppliedTotal` — one line in each of the three
  `apply*Mitigation` functions (`mitigate.go`, `retry_mitigate.go`,
  `fanout_mitigate.go`), immediately after the successful `r.Update` and
  right next to each function's existing "patched ..." log line. Never
  reached in `DetectOnly` mode or when the dependency object is missing —
  both of those `return nil` earlier in the same function, before any
  patch was actually written.
- `restorationsCompletedTotal` — one line at the end of each of the three
  `complete*Restore` functions (`restore.go`, `retry_restore.go`,
  `fanout_restore.go`), after the loop over managed edges succeeds. This
  turned out to be the single best insertion point rather than the six
  call sites it looked like at first glance: each `complete*Restore`
  function is the one place both the gradual ramp's final step
  (`advanceRestore<Signature>`) *and* `forceCompleteOutgoingRestore`'s
  handoff path already funnel through — instrumenting there once covers
  both without duplicating the increment at every caller, and without
  needing to distinguish "ramp completion" from "handoff completion" as
  separate metrics (the task's own spec didn't ask for that distinction,
  and a worklog test proves both paths land on the counter identically).
- `restorationRegressionsTotal` — inside `Reconcile`'s trip branch,
  computed as a boolean (`regression := !handoff && outgoingSig == sig &&
  policy.Status.Phase == PolicyPhaseRestoring`) read *before* `Phase` gets
  overwritten a few lines later, right alongside where `handoff` itself is
  already computed. Deliberately excludes handoff (a different signature
  tripping) — that's a distinct case with its own counter
  (`restorationsCompletedTotal`, since force-complete finishes the outgoing
  signature rather than regressing it), not a special case of regression.

**The testing approach needed real thought, not just "extend the existing
tests" as suggested — here's why, and what was done instead.** The task
suggested extending the existing fake-client controller tests to also
assert on `testutil.ToFloat64`. Looked at that first, and it doesn't
actually work safely as a mechanical extension: almost every existing test
in `internal/controller` calls `t.Parallel()`, and the vast majority of
them share the exact same fixture host (`patchDepHost =
"payments-service.default.svc.cluster.local"`, defined in
`istio_patch_test.go`) — so dozens of parallel tests would all be
incrementing the *same* global counter/label combination concurrently.
Asserting an absolute value, or even a naive "value went up by 1" delta,
from inside one of those parallel tests would be racing against every
other parallel test hitting that same label, and would be flaky by
construction rather than by mistake — global metrics on a shared registry
and per-test isolation via `t.Parallel()` are fundamentally in tension once
more than one test increments the same series.

Rather than either (a) mechanically extending those tests anyway and
accepting flakiness, or (b) silently doing something different from what
was asked without saying why, added a new file
(`internal/controller/metrics_test.go`) whose tests deliberately never call
`t.Parallel()`, with a comment at the top of the file explaining the actual
mechanism being relied on: Go's `testing` package runs every top-level test
that never calls `t.Parallel()` to full completion, in discovery order,
before releasing any paused-at-`t.Parallel()` test to run its body — this
is documented, relied-upon behavior of the `testing` package, not an
assumption about scheduling luck. So these new sequential tests' own
before/after deltas on the global counters are race-free regardless of what
the package's ~30 parallel tests do once they're released afterward,
without needing to touch `t.Parallel()` on any existing test or invent
per-test-unique label values as a workaround. Verified this reasoning
empirically, not just on paper: ran `go test ./internal/controller/... -race`
(clean) and `go test ./internal/controller/... -run TestMetrics` five times
in a row with `go clean -testcache` between each run (all passed
consistently).

Twelve new sub-tests across seven top-level test functions cover: detection
increments on a trip and does not increment when healthy; mitigation
increments on a successful patch for each of the three signatures and does
not increment in `DetectOnly` mode or when the dependency object is
missing; restoration-completed increments at each signature's ramp final
step *and* via a signature-handoff force-complete (proving the "one
instrumentation site covers both callers" claim above, not just asserting
it); restoration-regression increments on a same-signature re-trip
mid-ramp and does *not* fire on a handoff (the case that's easy to
conflate with regression but isn't one).

## Files touched

- `internal/controller/metrics.go` — new file: the four `CounterVec`s and
  their registration.
- `internal/controller/metrics_test.go` — new file: twelve sub-tests across
  seven non-parallel top-level test functions, with the concurrency
  reasoning documented at the top of the file.
- `internal/controller/cascadepolicy_controller.go` — `Reconcile`'s trip
  branch: computes `regression`, increments `signaturesDetectedTotal` and
  (conditionally) `restorationRegressionsTotal`.
- `internal/controller/mitigate.go`, `retry_mitigate.go`,
  `fanout_mitigate.go` — one `mitigationPatchesAppliedTotal.Inc()` each,
  alongside each function's existing "patched ..." log line.
- `internal/controller/restore.go`, `retry_restore.go`, `fanout_restore.go`
  — one `restorationsCompletedTotal.Inc()` each, at the end of each
  `complete*Restore` function.
- `go.mod` — `github.com/prometheus/client_golang` moved from `// indirect`
  to a direct dependency (`go mod tidy`, no version change — it was already
  present transitively via controller-runtime).
- `PLAN.md` — `[ ] Operator's own Prometheus metrics` → `[x]` in the §3
  checklist.

## Testing

- `go test ./internal/controller/... -run TestMetrics -v`: all twelve
  sub-tests pass.
- `go test ./internal/controller/...` (full package, all ~40 tests
  including the pre-existing parallel suite and the Ginkgo envtest suite):
  pass.
- `go test ./internal/controller/... -race`: pass, no data races detected
  on the shared global counters.
- Five repeated fresh runs (`go clean -testcache` between each) of
  `go test ./internal/controller/... -run TestMetrics`: consistently pass
  — empirical evidence for the non-parallel-test-ordering claim above, not
  just the documented `testing` package contract taken on faith.
- `make test` (full repo, envtest-backed): all packages pass; `internal/controller`
  coverage 77.3% → 78.0%.
- `make lint`: caught one real issue on the first pass — `goconst` flagged
  the literal `"signature"` label name repeated across all four metrics'
  label-name slices; fixed by extracting `labelSignature`/`labelDependency`/
  `labelKind` constants. `0 issues` after the fix.
- `gofmt -l .`: clean.

**Live confirmation attempted, not completed** — same outcome as the prior
two sessions' worklogs for the same reason. The Kind control-plane
container was found `Exited (137)` (OOM-killed) again; `docker start`
brought it back up (confirmed via `docker ps`, "Up 3 minutes" and no
further crash during the attempt), but `kubectl get nodes` returned
`Unable to connect to the server: EOF` on every attempt over roughly 90
seconds of waiting, with load average sitting at 12–15 throughout. Did not
keep fighting past that — same judgment call as the retry-storm-mitigation
and root-README slices made, for the same documented reason. A live
`curl <metrics-endpoint>/metrics` after a k6 run, showing these four
counters with real non-zero values, is worth doing once the machine is not
under this much contention.

## Follow-ups / known gaps

- Live confirmation against the real Kind cluster (see above) — third
  session in a row to defer this for the same root cause. Worth flagging to
  Gideon directly if a fourth session hits the same wall: at some point
  "the machine is busy with something else" stops being an incidental
  blocker and starts being worth addressing directly (more RAM/CPU headroom
  for Docker Desktop, or scheduling Kind-dependent slices for a quieter
  window) rather than re-documenting the same workaround indefinitely.
- The remaining checklist items this slice deliberately left alone: the two
  Istio patch secondaries (`VirtualService` timeout for latency/error
  cascade; `DestinationRule` connectionPool cap for retry storm) and the
  Kind-based integration test suite — both still blocked on the same
  cluster-reliability issue, not this slice's job to unblock.
