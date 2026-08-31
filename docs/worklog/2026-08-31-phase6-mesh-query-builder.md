# Phase 6.1/6.2: mesh adapter interface (QueryBuilder) + spec.mesh field

**Date:** 2026-08-31
**Author:** Claude (solo — Cursor unavailable)
**Type:** refactor (new package, additive CRD field) — the first, smallest
real slice of PLAN.md §5 Phase 6, not the full phase

## What
- `internal/mesh/mesh.go`: `QueryBuilder` interface (four methods —
  `LatencyP99Query`, `ErrorRateQuery`, `RetryStormRatioQuery`,
  `FanOutRatioQuery`), modeled directly on `internal/metrics.Querier`'s
  existing precedent.
- `internal/mesh/istio/query_builder.go`: Istio's implementation, moved
  verbatim (mechanical extraction, not a rewrite) from
  `internal/controller/promql.go`.
- `CascadePolicyReconciler.QueryBuilder mesh.QueryBuilder` field, defaulting
  to `istio.QueryBuilder{}` when nil (`queryBuilder()` helper) — same
  optional-field pattern as `Metrics`/`Notify`, chosen so no existing test
  constructing a `CascadePolicyReconciler` directly needed updating.
- `CascadePolicySpec.Mesh MeshType` (`Istio | Linkerd`, default `Istio`) —
  purely additive, same pattern as Phase 5's `thresholdOverrides`.

## Why
This is the "spike/design proposal first" step PLAN.md §5 Phase 6.1 calls
for, done as real code against the actual seams mapped from the codebase
(not assumed): everything Istio-specific lives in exactly two places —
`internal/mitigation/` (10 files, all `*networkingv1.DestinationRule`/
`*networkingv1.VirtualService`-typed) and `internal/controller`'s
hardcoded PromQL builders. `QueryBuilder` is the smaller, self-contained
half of that surface — pure string-building functions with zero
Kubernetes/controller-runtime dependency, so extracting it first is low-
risk and immediately useful (it's also what let `cmd/postmortem`,
Phase 8, exist without pulling in `internal/controller`'s full dependency
graph, though that command ended up not reusing it directly — see its own
worklog for why).

**What this slice deliberately does not do** — stated plainly rather than
implied: it does not touch `internal/mitigation` or the `Get`/`Update`/
`Patch` call sites in `internal/controller/{mitigate,restore,
fanout_mitigate,fanout_restore,retry_mitigate,retry_restore}.go`. A
`Mitigator` interface covering trip/restore patch application is real,
larger surface PLAN.md §5 Phase 6 still calls for — refactoring it, and
then building Linkerd's actual `QueryBuilder`/`Mitigator` implementations,
a Linkerd dev environment, and integration coverage, are separate,
substantial slices of their own (the plan's own text: "expect several
independently-reviewed slices, not one"). This worklog covers exactly
6.1 and 6.2, not the whole phase.

## How
- `queryBuilder()` returns `r.QueryBuilder` if set, else `istio.QueryBuilder{}`
  — `QueryBuilder` isn't optional infrastructure the way `Metrics`/`Notify`
  are (detection cannot run without one), but defaulting it this way meant
  zero changes to any existing test file or to `cmd/main.go`.
- Moved, not duplicated: the four query-builder functions' doc comments,
  reasoning, and any known issue with them carried over unchanged — in
  particular `ErrorRateQuery`'s missing-`sum()` issue was carried over
  as-is in this move, then independently found and fixed by a separately
  spawned task shortly after (see `docs/worklog/2026-08-31-error-rate-
  query-sum-fix.md`) — this refactor is not what fixed it, and didn't try
  to.
- `promql_test.go`'s four query-builder tests moved to
  `internal/mesh/istio/query_builder_test.go` alongside the functions;
  `TestSnapshotMax` (a controller-specific, mesh-agnostic helper) stayed.

## Files touched
- `internal/mesh/mesh.go` (new)
- `internal/mesh/istio/query_builder.go`, `query_builder_test.go` (new)
- `internal/controller/cascadepolicy_controller.go` — `QueryBuilder` field,
  `queryBuilder()`, four call sites updated
- `internal/controller/promql.go`, `promql_test.go` — the four functions
  and their tests removed (moved, see above)
- `api/v1alpha1/cascadepolicy_types.go` — `MeshType`, `Mesh` field;
  `zz_generated.deepcopy.go`, `config/crd/bases/...yaml` regenerated via
  `make generate manifests`

## Testing
- `go build ./...`, `go vet ./...`, `gofmt -l .` — clean.
- `go test ./... -race` — full suite passes, including the moved tests
  under their new package.
- `make lint` — 0 issues.
- `make verify-generate` — shows the expected diff against not-yet-
  committed regenerated files (the new `mesh` field), not real drift.
- No live-cluster re-verification needed for this slice specifically —
  the query strings themselves are unchanged (mechanical move), and
  `spec.mesh` isn't read by any code path yet (added, not yet wired into
  reconcile dispatch — that's part of the larger Mitigator-refactor slice
  this worklog explicitly defers).

## Follow-ups / known gaps
- `Mitigator` interface + Istio reference implementation (moving
  `internal/mitigation`'s 10 files and the controller's Get/Update/Patch
  call sites behind it) — not done, the largest remaining piece.
- Linkerd's actual `QueryBuilder`/`Mitigator` implementations, the
  failure-accrual/ServiceProfile mutual-exclusivity arbitration, a Linkerd
  dev environment, and integration coverage — not done.
- `spec.mesh` is added to the CRD but not yet read by the reconciler
  (nothing dispatches on it yet) — it's schema-ready, not behavior-ready.
