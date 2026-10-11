# Istio patch: latency/error-cascade primary (DestinationRule outlierDetection)

**Date:** 2026-08-29
**Author:** Cursor
**Type:** feature

## What
On a tripped latency/error cascade, the reconciler now resolves the
dependency's `DestinationRule` by Service FQDN convention and, in
`Mitigate` mode, read-modify-writes `spec.trafficPolicy.outlierDetection`.
Detection and status still run in `DetectOnly`; the mesh is not touched.
No VirtualService timeout, no restore ramp.

## Why
PLAN.md §2.6's first mitigation cell: eject unhealthy pods before the
failure finishes propagating. This is the gap vs. hand-tuned Istio circuit
breaking, and the trip path has to land and be reviewable before the restore
ramp (the next slice) can have anything to unwind.

## How
- **Resolution (§2.3):** parse `<service>.<namespace>.svc.cluster.local` (exactly
  two DNS-1123 labels before `.svc.cluster.local`) and `Get` the
  `DestinationRule` of that name in that namespace. No `Create` — synthesizing
  a DR would invent a traffic policy the user never wrote. Missing object
  (NotFound, or NoMatch when the Istio CRD isn't even installed) sets
  `DependencyObjectMissing` and skips that edge. Unexpected Get/Update errors
  fail the reconcile so they retry; a missing object does not.
- **Read-modify-write:** mutate only `OutlierDetection`'s
  `consecutive5xxErrors`, `interval`, and `baseEjectionTime`. Host, TLS,
  connection pool, subsets, and other outlier fields (`maxEjectionPercent`,
  …) stay as they were. A whole-object overwrite would clobber user-authored
  config — the same class of bug the managed-by annotation exists to prevent.
- **Annotations:** `cascade.gideonsanni.dev/managed-by: cascade-operator` on
  every patch. On **first** patch only (managed-by not already present),
  serialize the pre-patch `outlierDetection` (or `{"unset":true}` if it was
  nil) into `cascade.gideonsanni.dev/original-outlier-detection`. Re-trips of
  an already-managed object re-apply trip values but do not overwrite that
  original. Stored on the DR rather than the policy so the restore slice can
  read it off the object it is ramping, even if the policy is gone. Not a
  CRD status field — that would have been a PLAN.md change and belongs in
  PROPOSALS.md if we later decide the annotation is the wrong home.
- **Trip constants** (internal, not CRD fields): `consecutive5xxErrors=3`
  (Istio default is 5; 1 would eject on a single blip), `interval=5s` (Istio
  default 10s; shorter than the typical 30s PromQL window so a sweep can
  happen inside the detection window), `baseEjectionTime=30s` (Istio's
  default, kept — one PromQL window, long enough that the next tick can see
  the effect, not an invented "longer" that we'd have to justify against
  nothing). Aggressive enough to be the actual mitigation; not so extreme
  it reads as arbitrary.
- **Mode:** empty/`Mitigate` patches; `DetectOnly` logs the would-be patch
  (object, field, values) and returns. Detection is not gated.
- **Client:** `istio.io/client-go` networking v1 types on the manager scheme;
  `r.Client.Get`/`Update` like `CascadePolicy`. No separate Istio clientset.
  RBAC is `get;list;watch;update;patch` with **no `create`**. `list`/`watch`
  are for the cache informer that starts on first Get once the CRD exists;
  `SetupWithManager` does **not** `.Watches` DestinationRules, because that
  would fail manager startup on a cluster without Istio CRDs (today's Kind).

## Files touched
- `internal/mitigation/resolve.go` — `ParseServiceFQDN`
- `internal/mitigation/outlier.go` — constants, annotations, `ApplyLatencyErrorOutlierTrip`
- `internal/mitigation/resolve_test.go` / `outlier_test.go` — no cluster
- `internal/controller/mitigate.go` — resolve, Get, mode gate, condition, Update
- `internal/controller/cascadepolicy_controller.go` — call patch on trip; status dirty-check includes conditions
- `internal/controller/istio_patch_test.go` — fake client: missing, DetectOnly, first trip, re-trip
- `cmd/main.go` / `internal/controller/suite_test.go` — register Istio scheme
- `config/rbac/role.yaml` — regenerated DestinationRule verbs, no create
- `go.mod` / `go.sum` — `istio.io/client-go` direct
- `PLAN.md` — status line + checklist
- `docs/worklog/README.md` — index this entry

## Testing
- `go test ./internal/mitigation/` — pass, 94.9% coverage, no cluster.
- `go test ./internal/controller/ -run 'TestMitigate|TestDetectOnly'` — fake
  client: missing DR → condition, no create; DetectOnly → status Tripped, no
  mesh mutation; first Mitigate trip → both annotations + trip values; already
  managed re-trip → trip values re-applied, original annotation unchanged.
- `make test` — controller 85.8% (envtest trip now also asserts
  `DependencyObjectMissing` because Istio CRDs aren't loaded), metrics 80.4%,
  signatures 87.0% unchanged.
- `make lint` — 0 issues. `gofmt -l` clean. `make manifests` wrote
  DestinationRule RBAC with no `create`.

## Follow-ups / known gaps
- Next slice: restoration ramp, reading `original-outlier-detection` and
  stepwise loosening the same three fields. Do not land it here.
- Secondary cell (VirtualService timeout) still unbuilt, per §2.6.
- Kind still has no Istio; this slice is not an integration test of Envoy
  ejection. The Kind+Istio checklist item stays open.
- `histogram_quantile` without `sum by (le)` and `response_flags=UR` still
  carried forward from the detector slice.
