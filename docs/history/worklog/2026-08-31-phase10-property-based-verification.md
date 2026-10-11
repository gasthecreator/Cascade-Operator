# Phase 10: property-based state-machine verification (pgregory.net/rapid)

**Date:** 2026-08-31
**Author:** Claude (solo — Cursor unavailable for the remainder of this initiative; see PLAN.md §5's note on the changed execution model)
**Type:** test-only, no runtime behavior change

## What
Added `pgregory.net/rapid` and two property-test files that generalize
several existing fixed-fixture regression tests into properties checked
across randomly generated input spaces:

- `internal/controller/statemachine_rapid_test.go` —
  `TestRapidSignatureHandoffNeverOrphansAnnotations` drives the real
  `Reconcile` path through a random sequence (1–15 ticks) of
  healthy/latency-error/fan-out signals on a single shared
  `DestinationRule` and checks, after every tick: `RestoreStep` always
  stays in `[0, RestoreFinalStep]`; while continuously `Restoring` it only
  ever advances by exactly one or resets to 0; whenever `LastSignature`
  changes to a different non-empty value than the tick before, the
  outgoing signature's own `original-*` annotation is never still present;
  and at `Phase == Normal` neither signature's annotation survives.
- `internal/mitigation/retries_rapid_test.go` —
  `TestRapidRetryStormTripAlwaysPatchesExplicitZero` and
  `TestRapidRetryStormRestoreAlwaysRecoversTrueOriginalAttempts`
  generalize `retries_test.go`'s fixed-fixture zero-value assertions across
  a generated pre-trip input space (any original `attempts` 0–5, `retryOn`
  set or not, a per-try timeout present or not, or no retries block at
  all/`Unset`) — proving trip always emits an explicit `attempts:0` patch
  op and restore always carries the *true* original value (including a
  true original of exactly 0) through both the intermediate-step merge
  patch and the completion patch, for any generated input, not just the
  one hand-picked case each existing test covers.

## Why
PLAN.md §5's Phase 10 (re-sequenced to run first, see the approved
solo-execution plan) exists specifically to lock in the invariants this
project has already hit real bugs on — the same-object signature-handoff
orphaning bug (fixed via `forceCompleteOutgoingRestore`) and the
retries.attempts zero-value serialization bug (fixed via merge-patch
writes) — as a property proven across generated sequences/inputs, not just
the specific hand-picked scenarios the existing test suite happens to
cover. This is deliberately sequenced before Phase 6 (Linkerd), which will
extend this same state machine to a second mesh: better to have this net
in place before the state machine grows a new dimension.

Scoped to latency-error/fan-out (not retry storm) for the controller-level
property test because those two are the pair that actually share one
object kind (`DestinationRule`, disjoint fields) and can hand off between
each other on the same object — retry storm's primary lives on a
`VirtualService`, a different handoff shape not exercised by this fixture.

## How
- Reused every existing test-harness pattern rather than building a new
  one: `patchTestScheme`/`patchTestDR`/`patchPolicyName` etc.
  (`istio_patch_test.go`), the `fake.NewClientBuilder()` + real
  `Reconcile()` shape already used throughout `internal/controller`'s test
  suite, and the query-shape-dispatch convention `fakeQuerier`/
  `hostAwareQuerier` already established (matched by PromQL substring, not
  by host, since this test only needs one host).
- `patchTestScheme` takes `*testing.T`; `rapid.Check`'s callback only
  receives `*rapid.T`, so the scheme is built once outside `rapid.Check`
  with the outer `*testing.T`, and only the fake client/policy/reconciler
  are rebuilt fresh per generated iteration inside it.
- For the mitigation-package tests, deliberately did **not** try to apply
  the patch bytes through a live patch-application library — the project's
  own existing tests already established (and this slice confirmed by
  grep) that nothing in the repo imports a JSON-Patch/merge-patch library
  directly; wire-format proof stays the job of `test/integration` against
  a real apiserver. These tests instead do raw-JSON `bytes`/`map[string]any`
  inspection of the patch-builder functions' own output, generalized
  across generated inputs — the same class of proof the existing
  fixed-fixture tests already give, just over more of the input space.

## Files touched
- `go.mod`, `go.sum` — added `pgregory.net/rapid v1.3.0`
- `internal/controller/statemachine_rapid_test.go` (new)
- `internal/mitigation/retries_rapid_test.go` (new)
- `PLAN.md` — §5, Phase 10 checked off

## Testing
- `go build ./...`, `go vet ./...`, `gofmt -l .` — clean.
- `go test ./... -race` — full suite passes, all packages, no new failures.
- `make lint` — 0 issues (one `modernize`/range-over-int violation caught
  and fixed during this slice: `for i := 0; i < numTicks; i++` →
  `for i := range numTicks`).
- `go test ./internal/controller/... -run TestRapid -v` and
  `go test ./internal/mitigation/... -run TestRapid -v` — all three
  property tests pass at rapid's default 100 iterations each; no live
  cluster involved (fake-client-only, matching the plan's own scoping —
  this phase is explicitly test-only).

## Follow-ups / known gaps
- Coverage is intentionally narrow to the two bug classes named in the
  plan (handoff orphaning, zero-value round-trip) plus basic
  `RestoreStep` bounds/monotonicity — not an exhaustive state-machine
  model of every signature pair. Extending this to retry storm's own
  handoff shape (`VirtualService` primary + `DestinationRule` secondary)
  would be a reasonable future addition but wasn't required to satisfy
  Phase 10's stated invariants.
- Rapid's default iteration count (100) was left as-is per the plan
  ("no need to hand-tune") — fast (well under a second per test) and
  sufficient to exercise the generated space meaningfully.
