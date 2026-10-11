# Retry storm's zero-value trip fields never actually reach the API server

**Date:** 2026-08-30
**Author:** Claude
**Type:** fix (investigation + proposal; implementation is the next slice)

## What
Found, independently verified against the live cluster with the real
production code path, and resolved the direction for: retry storm's
primary (`VirtualService.HTTPRoute.Retries.Attempts = 0`) and its secondary
(`DestinationRule...ConnectionPoolSettings_HTTPSettings.MaxRetries = 0`,
this session's own slice) both set a plain `int32` proto field to exactly
`0` on trip — and that value never actually reaches the stored Kubernetes
object, meaning it never reaches Istio's control plane or Envoy either.
**Retry storm's mitigation has likely never actually reduced retries at the
Envoy enforcement level, on either its primary or secondary, despite every
prior test and live check passing.**

## Why
The immediately preceding slice's own worklog noted, almost in passing:
"`MaxRetries=0` still serializes as empty `http: {}` at trip time (proto3
zero); flagged in the worklog, not a proposal." That framing undersold what
was actually being observed. I investigated rather than accepting it, since
"the object I just wrote to looks different from what I wrote" is exactly
the kind of symptom worth chasing to a root cause rather than noting and
moving on.

## How

### The mechanism, verified step by step, not theorized
1. Checked the vendored `istio.io/api` v1.30.4 source directly:
   `HTTPRetry.Attempts` and `ConnectionPoolSettings_HTTPSettings.MaxRetries`
   are both declared as plain `int32` with `json:"...,omitempty"` — not
   `*wrapperspb.UInt32Value` like `outlierDetection`'s
   `Consecutive_5XxErrors` is. `omitempty` on a plain scalar strips it from
   JSON output whenever the value equals the type's zero value — and `0`
   *is* the zero value for `int32`.
2. Confirmed this isn't just a theoretical proto3 rule: wrote a standalone
   program using `encoding/json.Marshal` directly on
   `ConnectionPoolSettings_HTTPSettings{MaxRetries: 0}` and separately on
   `HTTPRetry{Attempts: 0}` — both produced `{}`.
3. Confirmed the *actual production code path* has the identical problem,
   not just the raw proto struct in isolation: constructed a full
   `networkingv1.DestinationRule` (the `istio.io/client-go` CRD wrapper
   type the operator's `Reconcile` loop actually uses) with `MaxRetries: 0`
   nested inside it, marshaled the whole object — same result,
   `"http":{}`` inside the spec, no `maxRetries` key.
4. **Ran the exact real mitigation function against the real live
   cluster**, not a simulation: temporarily placed a small Go program
   inside the module (to access the `internal/mitigation` package), used a
   real `controller-runtime` client against the actual running Kind
   cluster's API server, called
   `mitigation.ApplyRetryStormConnectionPoolTrip(dr)` (the unmodified
   production function) on a real `inventory-service` `DestinationRule`,
   and called `c.Update(ctx, dr)` — the identical call
   `applyRetryStormMitigation` makes. The `Update` call succeeded with no
   error.
5. Re-fetched the object through the typed client — its Go struct read
   back `MaxRetries: 0`, which could still mean either "explicitly stored
   as 0" or "absent, and Go zero-filled it on unmarshal." Those look
   identical through a typed struct, so I went one level lower.
6. **Checked the raw stored JSON directly** via
   `kubectl get destinationrule inventory-service -o json`, bypassing Go
   struct interpretation entirely: `spec.trafficPolicy.connectionPool.http`
   is `{}`. **No `maxRetries` key exists in the object actually stored by
   the Kubernetes API server.** This is the conclusive step — it rules out
   "the Go struct just displays it oddly" and confirms the field never
   reached storage at all.
7. Cleaned up: deleted the temporary in-module test program and all
   scratch files, restored `inventory-service`'s `DestinationRule` to its
   committed fixture's exact clean state via `kubectl apply` on the real
   fixture file.

### Why this evaded every prior check
- Fake-client unit tests (the vast majority of this project's test suite)
  store the Go struct directly in an in-memory fake client — no marshal/
  unmarshal round trip ever happens, so `omitempty`-driven data loss is
  structurally invisible to them. Every test asserting
  `route.Retries.GetAttempts() == 0` after calling the trip function is
  checking the in-memory struct, which is correct — the bug isn't in the
  mitigation logic, it's in what happens when that struct crosses the wire
  to the real API server.
- Every live check across this project's k6/evidence slices verified "the
  reconcile trip succeeded, no webhook rejection, the policy transitioned
  to `Tripped`" — none of them checked whether Envoy's *actual* rendered
  proxy config reflects the intended value, because nothing so far has had
  reason to distrust the object read-back (which, per point 5 above, looks
  identical whether the field is truly `0` or truly absent).
- This is on me too, not just the slice that wrote the code: I approved
  the original retry-storm mitigation slice and the webhook-rejection fix
  slice without independently checking Envoy-level enforcement either time
  — only that the `Update` succeeded and the Go-level test assertions
  passed. This finding is as much a gap in my own review methodology to
  date as it is a gap in the code.

### Scope — confirmed narrow, not pervasive
Checked every other trip-time field this project's mitigation package
writes: `outlierDetection`'s `Consecutive_5XxErrors` is a
`*wrapperspb.UInt32Value` (nullable wrapper, immune — the pointer itself is
non-nil even when the wrapped value is 0); `Interval`/`BaseEjectionTime`
and the timeout secondary's `Timeout` are all `*durationpb.Duration`
(message-type pointers, immune the same way); fan-out's
`Http1MaxPendingRequests`/`Http2MaxRequests` trip values are both `1`, not
`0` (non-zero, unaffected). **The bug is isolated to exactly the two fields
where the intended trip value happens to be the type's zero value: retry
storm's `Attempts` and `MaxRetries`.** Not a pervasive issue across the
mitigation package — a narrow but real one, isolated to one signature's
entire mitigation strategy.

## Resolution
Filed as a `PROPOSALS.md` entry and resolved there (see that file) rather
than picked silently, given the severity and that it revisits two
already-approved slices' actual runtime correctness — this deserves the
same process rigor as any other architecture-affecting finding, arguably
more given what's at stake. Recommended direction: switch these two
fields' writes to a raw JSON/merge-patch built from explicit bytes or an
unstructured map (not the typed Go struct), so an explicit `0` survives
being sent to the API server, rather than weakening the mitigation by
changing the trip value to `1` just because `1` happens to serialize
correctly.

## Files touched
- `docs/worklog/2026-08-30-retry-storm-zero-value-serialization-bug.md` —
  this file
- `docs/worklog/README.md` — index this entry
- `PROPOSALS.md` — new entry, resolved
- `PLAN.md` — status note

## Testing
See "How" above — every step was independently executed and verified
against the real proto types, the real generated client-go wrapper types,
and the real live Kind cluster's API server, not assumed from documentation
or reasoned about abstractly.

## Follow-ups / known gaps
- **Next slice, top priority**: implement the raw-patch fix for both
  `internal/mitigation/retries.go` (`ApplyRetryStormTrip`'s `Attempts`
  write) and `internal/mitigation/retry_connpool.go`
  (`ApplyRetryStormConnectionPoolTrip`'s `MaxRetries` write). Both need the
  same mechanism-level fix.
- Once fixed, this needs fresh live re-verification specifically checking
  Envoy's actual rendered config (`istioctl proxy-config cluster` or the
  Envoy admin API), not just the stored Kubernetes object — that's the
  actual gap this whole finding is about, and re-checking only the K8s
  object again would repeat the same blind spot.
- Worth considering, separately: whether any *future* signature's
  mitigation should get a deliberate "does this trip value survive a real
  marshal round-trip" check as a matter of course, given how easily this
  slipped through everywhere else.
