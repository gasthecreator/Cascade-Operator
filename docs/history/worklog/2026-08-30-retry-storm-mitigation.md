# Retry-storm mitigation: VirtualService retries.attempts primary, built but not yet wired live

**Date:** 2026-08-30
**Author:** Cursor
**Type:** feature

## What
Added the retry-storm primary from PLAN.md §2.6: cut `retries.attempts` to
0 on every forwarding route of the dependency's `VirtualService`, annotated
the same way the outlier-detection patch is. The mitigation code
(`internal/mitigation/retries.go`) and its reconciler-side resolver
(`internal/controller/retry_mitigate.go`) are built and unit/fake-client
tested. **`Reconcile` does not call it yet** — a retry-storm trip still sets
status only, exactly as it did before this slice. See "The stuck-mitigation
wrinkle" below for why, and a regression test that locks that choice in.

## Why
PLAN.md §2.6: `VirtualService retries.attempts → 0 or 1` is retry storm's
primary, same sequencing as latency/error cascade's own separate Istio-patch
slice (detector first, patch second, restore third). This is that
patch slice.

## How

### Live-verification attempt, and what actually happened instead
The prompt asked me to verify, against the actual Kind cluster, whether
Istio 1.30.4 applies an implicit default retry policy to a route with no
explicit `retries` block — because if it does, patching only routes that
already have an explicit block would leave every other route's implicit
retries completely unmitigated.

I could not reach the cluster to check this live. `kubectl get pods -n
istio-system --request-timeout=8s` returned a clean connection timeout, and
Docker Desktop's own VM console log
(`~/Library/Containers/com.docker.docker/Data/log/vm/console.log`) shows
`POST /analytics/track/oom-kills` at `06:32:04Z` and `06:32:08Z` — the VM
that hosts the Kind cluster had just been OOM-killed (it's a 4096MB VM
running Kind + Istio + Prometheus alongside unrelated containers). `docker
ps` itself hung for minutes. I did not restart Docker Desktop or the VM to
force a retry, since that VM is running other unrelated workloads
(`pharos-cassandra`) I didn't want to disrupt mid-session.

Instead of guessing, I fell back to the next-strongest evidence available:
the **vendored `istio.io/api` Go source**, pinned in `go.mod` to
`v1.30.4-0.20260824163423-69099aec2678` — the exact same 1.30.4 line
installed on the Kind cluster (per PLAN.md §2.7's pin), not a generic
"latest docs" approximation. `go doc istio.io/api/networking/v1alpha3.HTTPRoute`
prints this directly from the proto comment:

> Retry policy for HTTP requests. Note: the default cluster-wide retry
> policy, if not specified, is: `attempts: 2`,
> `retryOn: "connect-failure,refused-stream,unavailable,cancelled"`. This can
> be customized in Mesh Config `defaultHttpRetryPolicy`.

Istio's own current reference docs (`istio.io/latest/docs/reference/config/networking/virtual-service/`,
fetched live) state the identical default, and a GitHub issue thread
(`istio/istio#50506`) confirms this has been Istio's documented behavior
across versions, with only `retryOn`'s exact list debated (whether plain
`503` is included), not whether the default exists at all.

**The claim is real, not a recollection to take on faith: a route with no
explicit `retries` block still gets 2 retries under Istio's implicit
default.** That confirms the scope this slice needed: patching only routes
that already have an explicit `retries` block would leave every other route
retrying twice per request, completely unmitigated, on exactly the routes a
retry storm would be happening on. Every `http[]` route gets an explicit
`retries` block written on trip, not just ones that already have one.

I did not treat this as a PROPOSALS.md-worthy correction — nothing in
PLAN.md currently claims otherwise about implicit retries (§2.6 just says
"cut `retries.attempts`", no implementation detail), so there's no existing
wording to correct, unlike the `URX` finding which *did* correct a specific
PLAN.md sentence. This is scope information for building the already-locked
primary, not an architecture change.

### Patch mechanics
`mitigation.ApplyRetryStormTrip(vs)` iterates `vs.Spec.Http` and, for each
route:
- **Skips routes with no destinations** (`Redirect`, `DirectResponse`, or
  `Delegate` — no upstream call happens, so retries are meaningless and
  Envoy ignores the field anyway). Captured in the original snapshot as
  `{"skipped":true}` so a future restore pass can rely on the array being
  the same length and order as `spec.http` without re-deriving which routes
  were touched.
- For a **forwarding route with no existing `retries` block**: creates a
  minimal `&HTTPRetry{Attempts: 0}`. Snapshot: `{"unset":true}`.
- For a **forwarding route with an existing `retries` block**: only
  `Attempts` changes; `RetryOn`, `PerTryTimeout`, `Backoff` are preserved
  untouched, same read-modify-write discipline as
  `ApplyLatencyErrorOutlierTrip`'s handling of `MaxEjectionPercent` and TLS
  settings on the DestinationRule. Snapshot captures all four fields.

### Trip value: 0, not 1
PLAN.md §2.6 says "0 or 1." Picked **0**. Reasoning: 1 still lets a second
attempt through — under the destination:source ratio detector, a 2×
amplification (1 retry) can itself sit at or above a low
`retryStormMultiplier`, so a policy could re-trip immediately after
"mitigating." 0 fully halts the amplification mechanism on the first patch,
matching the same aggressive-trip/gradual-restore split the
outlier-detection primary already established (tighten hard on trip, ramp
back gradually on restore — not a soft trip that might not hold).

### Original annotation: a new key, array-shaped
`cascade.gideonsanni.dev/original-retries` (not
`original-outlier-detection` — that key's JSON shape is DestinationRule-
specific and there's only ever one `outlierDetection` block per object,
whereas a VirtualService can have several `http[]` routes). Value is a JSON
array, one entry per route, same order as `spec.http`, so the same array can
be zipped against `spec.http` again on restore. First-patch-only capture:
`ApplyRetryStormTrip` only writes the annotation when `managed-by` is not
already set, same non-overwrite rule as the outlier-detection annotation.

### Resolution and status wiring
Reused `mitigation.ParseServiceFQDN` (unchanged) to get name/namespace from
the `dependsOn` host, then `Get` a `VirtualService` by that name/namespace
convention — same as the `DestinationRule` lookup, no `Create` on miss.
`setDependencyMissing`/`clearDependencyMissing` in `mitigate.go` were
DestinationRule-flavored in their `Reason` strings
(`DestinationRuleNotFound`/`Found`) and hardcoded message
(`"DestinationRule resolved..."`). Generalized both to
`DependencyObjectNotFound`/`Found` and a kind-agnostic message, since these
helpers are now shared across two Istio object kinds and no test asserted
on the old literal strings. Not a PLAN.md change — the
`DependencyObjectMissing` condition's own doc comment already says
"e.g. no DestinationRule found," generically — so this is a routine
implementation refactor, not a proposal.

### The stuck-mitigation wrinkle: chose option (a)
`beginRestore`/`advanceRestore` in `internal/controller/restore.go` only
call `listManagedEdges`, which only looks for managed `DestinationRule`s.
If `Reconcile` called `applyRetryStormMitigation` on a live retry-storm
trip, the very next healthy tick would call `beginRestore`, find zero
managed `DestinationRule`s (correctly — none were touched), and snap
straight to `Normal` per the existing fallback — while the `VirtualService`
still sits at `retries.attempts: 0` forever. That's a real stuck mitigation:
status says `Normal`, the mesh says otherwise.

**Chose (a): keep this slice's mitigation code built, tested, and reviewable,
but do not call it from `Reconcile`.** Rejected (b) — teaching
`beginRestore` to recognize "a VirtualService is managed but I don't know
how to restore it, hold at Tripped" — because that means touching
`restore.go` this slice regardless, which the prompt explicitly scoped out
("No restoration logic for the VirtualService — that's the very next
slice"), and a half-built recognize-but-can't-restore branch is exactly the
kind of code that's easy to forget to finish once the "real" restore logic
lands next to it. Landing the `Reconcile` wiring and VirtualService-aware
restoration together, in one slice, means there is never a commit where the
live behavior can silently drop into `Normal` with a mesh that's still
mitigated — the risk window doesn't exist rather than being narrowed.
`TestReconcileDoesNotWireRetryStormMitigationYet` asserts a full `Reconcile`
call on a retry-storm trip leaves an existing `VirtualService` completely
untouched, so this choice is enforced by a test, not just a comment.

### RBAC
Added `+kubebuilder:rbac:groups=networking.istio.io,resources=virtualservices,verbs=get;list;watch;update;patch`
next to the new resolver function (mirrors the existing `destinationrules`
marker). `make manifests` regenerated `config/rbac/role.yaml` accordingly.
No CRD/API type changes this slice — `networkingv1.AddToScheme` in
`cmd/main.go` already covers `VirtualService` (same package as
`DestinationRule`), so no scheme change either.

## Files touched
- `internal/mitigation/retries.go` — `ApplyRetryStormTrip`, per-route
  original snapshot, `AnnotationOriginalRetries`, `TripRetryAttempts`
- `internal/mitigation/retries_test.go` — multi-route patch (implicit
  default / explicit block / redirect-only), re-trip no-overwrite, empty
  `http[]` list
- `internal/controller/retry_mitigate.go` — `applyRetryStormMitigation`
  (resolve, missing-object condition, `DetectOnly` no-op, patch); RBAC
  marker; **not called from `Reconcile`**, documented inline and in this
  worklog
- `internal/controller/retry_mitigate_test.go` — fake-client tests calling
  the resolver directly (missing VS, `DetectOnly`, first trip, re-trigger),
  plus `TestReconcileDoesNotWireRetryStormMitigationYet`
- `internal/controller/mitigate.go` — generalized
  `reasonDestinationRuleNotFound/Found` → `reasonDependencyObjectNotFound/Found`
  and the clear-condition message, now shared across object kinds
- `config/rbac/role.yaml` — regenerated, adds `virtualservices` verbs
- `PLAN.md` — status line + checklist paragraph (not §2)
- `docs/worklog/README.md` — index this entry

## Testing
- `go build ./...`, `go vet ./...` — clean.
- `make test` — `internal/mitigation` 84.9%, `internal/controller` 82.9%
  (both up from the previous slice's baseline), `internal/signatures` and
  `internal/metrics` unchanged.
- `make lint` — 0 issues, after extracting a `testRetryOn5xx` test constant
  (goconst: `"5xx"` had crossed the 3-occurrence threshold across the new
  test file) and a scoped `//nolint:unparam` on `applyRetryStormMitigation`
  (its `host` parameter is only ever called with one constant value right
  now because nothing wires it into `Reconcile` yet — the false positive
  resolves itself the moment the next slice calls it per `dependsOn` edge,
  same as `applyLatencyErrorMitigation`'s identical-shaped parameter).
- `make manifests generate` — only `config/rbac/role.yaml` changed; no CRD
  drift.

## Follow-ups / known gaps
- **Next slice, same as the outlier-detection → restoration-ramp split**:
  wire `applyRetryStormMitigation` into `Reconcile`'s tripped branch *and*
  extend `listManagedEdges`/`beginRestore`/`advanceRestore` to recognize a
  managed `VirtualService`, in the same change — not two separate slices,
  per the reasoning above.
- The implicit-default-retry finding is corroborated by the vendored
  proto source (exact version match) and Istio's own docs, but not by
  actually inducing traffic on this specific Kind cluster — Docker's VM was
  OOM-killed while writing this slice. Worth a live check once the cluster
  is healthy again: a route with no explicit `retries` block, traffic that
  trips `connect-failure`/`refused-stream` (not just a plain 5xx response,
  which the docs suggest is *not* covered by the implicit default's
  `retryOn` list), and confirm the dest:source ratio actually moves.
- `DestinationRule` `connectionPool.http.maxRetries` / `http1MaxPendingRequests`
  (retry storm's §2.6 *secondary*) — still not built, later slice.
- Fan-out detector — still unbuilt, third and last signature.
