# Retry storm's connectionPool secondary trips `maxRetries` to 1, not 0

**Date:** 2026-08-30
**Author:** Cursor
**Type:** fix

## What
Changed `TripRetryStormMaxRetries` from `0` to `1` in
`internal/mitigation/retry_connpool.go`, with a doc comment explaining
why: Istio Pilot's `applyConnectionPool` (`istio/istio` 1.30.4
`pilot/pkg/networking/core/cluster_traffic_policy.go` L118–L120) only
copies `MaxRetries` into Envoy CDS when the value is `> 0`; an explicit
`0` on the DestinationRule is indistinguishable from unset and Envoy
keeps Pilot's `math.MaxUint32` default (`4294967295`). References the
resolved `PROPOSALS.md` entry (approved 2026-08-30, direction 2).

Restore ramp's `from` anchor already used `TripRetryStormMaxRetries` via
`lerpI32(TripRetryStormMaxRetries, origRetryMaxRetriesTarget(orig), t)` —
no hardcoded `0` there. DetectOnly logging already emits
`mitigation.TripRetryStormMaxRetries` from `retry_mitigate.go`.

The merge-patch write path (`RetryStormMaxRetriesMergePatch` +
`client.Patch`) is unchanged — still the mechanism for this secondary
even though `1` serializes under plain `omitempty`. Tests renamed/simplified:
`TestRetryStormMaxRetriesMergePatchContainsTripValue` asserts patch bytes
contain `"maxRetries":1`; dropped the typed-struct omitempty demonstration
for this field since it no longer teaches anything. Primary `attempts: 0`
patch path untouched.

## Why
The zero-value patch slice fixed Kubernetes serialization — `"maxRetries":0`
now reaches etcd — but live Envoy verification showed Istio's translator
still renders `circuit_breakers.max_retries: 4294967295`. That is a
separate layer from our marshal bug: Pilot treats proto3 zero as unset for
this field. Direction 2 (`1`) is the smallest value that actually reaches
Envoy as a real outstanding-retry circuit-breaker cap without weakening
the primary (`retries.attempts → 0` still disables per-route retries).

## How
- `TripRetryStormMaxRetries = int32(1)` with expanded doc comment (Pilot
  guard, Envoy default, distinction from primary).
- `ApplyRetryStormConnectionPoolTrip` / `RetryStormMaxRetriesMergePatch`
  unchanged in structure; constant drives both in-memory mutate and patch
  payload.
- Restore tests updated for trip anchor `1`: controller restore step
  expectations `[3,5,6,8,10,10]` from `lerp(1,10,t)`; unit restore step-0
  comment `lerp(1,3,0.2)=1`; `dualManagedDR`/`tripleManagedDR` seed
  `MaxRetries: 10` before retry-storm trip so step 0 is observably off the
  trip value.

## Files touched
- `internal/mitigation/retry_connpool.go` — constant, doc comment
- `internal/mitigation/retry_connpool_test.go` — trip-value merge-patch test
- `internal/mitigation/retry_connpool_restore_test.go` — step-0 lerp comment
- `internal/controller/retry_connpool_restore_test.go` — ramp expectations
- `internal/controller/retry_restore_test.go` — seed non-zero MaxRetries
- `internal/controller/fanout_restore_test.go` — seed non-zero MaxRetries
- `docs/worklog/README.md` — index this entry

## Testing
- `gofmt`, `make lint` (0 issues), `go test ./...` — pass.
- `TestRetryStormMaxRetriesMergePatchContainsTripValue` locks patch bytes
  contain `"maxRetries":1`.
- **Live, Kubernetes object (required):** applied the production merge-patch
  shape (`maxRetries: 1` plus retry-storm annotations) to
  `inventory-service`'s DestinationRule on the Kind cluster (Istio
  `pilot:1.30.4`). Raw stored JSON:
  `spec.trafficPolicy.connectionPool.http` is `{"maxRetries": 1}`.
- **Live, Envoy admin API (required — the point of this slice):**
  `kubectl exec <checkout-pod> -c istio-proxy -- curl localhost:15000/config_dump`,
  parsed `ClustersConfigDump` for `outbound|80||inventory-service...`:
  `circuit_breakers.thresholds[0].max_retries` is **`1`**, not
  `4294967295`. Confirms Istio translates a nonzero DR value through to
  Envoy CDS — the gap the `0` trip value left open.
- Organic operator trip during this session was blocked by a flaky local
  Prometheus port-forward (`connection refused` on `127.0.0.1:19090`), so
  the operator never observed RetryStorm metrics to patch live. The K8s +
  Envoy check used the same production patch bytes the operator would emit;
  that is the layer this slice fixes (Pilot translation), not detection
  wiring.

## Follow-ups / known gaps
- Restoration of a captured *zero* original at the final step still goes
  through typed `Update()` and would strip `maxRetries: 0` the same way as
  the pre-patch bug — out of scope here; rare in practice.
- Kind-based integration test asserting Envoy `max_retries` after trip
  remains the open checklist item for CI.
