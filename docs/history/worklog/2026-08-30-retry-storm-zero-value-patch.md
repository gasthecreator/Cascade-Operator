# Retry storm's zero trip values now cross the wire as explicit JSON zeros

**Date:** 2026-08-30
**Author:** Cursor
**Type:** fix

## What
Switched retry storm's two zero-valued trip writes — `VirtualService`
`retries.attempts → 0` and `DestinationRule` `connectionPool.http.maxRetries
→ 0` — from typed `client.Update()` to a raw patch whose JSON literally
contains `"attempts":0` / `"maxRetries":0`. Every other mitigation write
path is unchanged.

## Why
`HTTPRetry.Attempts` and `HTTPSettings.MaxRetries` are plain `int32` with
`json:"...,omitempty"`. A typed `Update()` marshals the Go struct, strips
the zeros, and the API server stores an absent field. Envoy then falls
back to its own defaults (`attempts: 2`, `max_retries: 3`) — the opposite
of the mitigation. Confirmed live by Claude
(`docs/worklog/2026-08-30-retry-storm-zero-value-serialization-bug.md`);
`PROPOSALS.md` approved the patch-based write rather than weakening the
trip value to 1. Fake-client tests could not have caught this: they store
the Go struct in memory and never marshal.

## How
`ApplyRetryStormTrip` / `ApplyRetryStormConnectionPoolTrip` still mutate
the in-memory object (existing unit tests, later restore reads). The wire
write is a separate payload, built from `map[string]any` so
`encoding/json` has no `omitempty` tag to honor:

- **VirtualService:** RFC 6902 JSON Patch. `spec.http` is an array; a
  JSON merge patch would replace the whole list. Each forwarding route
  gets `{"op":"add","path":"/spec/http/N/retries","value":{"attempts":0}}`,
  which is also what clears `retryOn`/`perTryTimeout`/`backoff` for the
  webhook. Redirect/delegate routes are skipped, same as the typed
  mutate. Annotations go out as a single `add` of the in-memory map
  (fetched object plus our two keys).
- **DestinationRule:** JSON merge patch of
  `spec.trafficPolicy.connectionPool.http.maxRetries: 0` plus this
  signature's two annotation keys. Nested objects merge, so fan-out's
  `http1`/`http2`, outlierDetection, and TLS are untouched.

Controller `applyRetryStormRetriesPrimary` /
`applyRetryStormConnectionPoolSecondary` call `client.Patch` with those
bytes instead of `client.Update`. Restoration still uses `Update` —
out of scope; it writes non-zero interpolated values today. A captured
original of 0 at *completion* would hit the same omitempty hole; noted
below rather than widened here.

## Files touched
- `internal/mitigation/retries.go` — `RetryStormAttemptsJSONPatch`
- `internal/mitigation/retry_connpool.go` — `RetryStormMaxRetriesMergePatch`
- `internal/mitigation/retries_test.go`, `retry_connpool_test.go` —
  marshal the patch bytes; assert they contain the explicit zeros the
  typed struct marshal does not
- `internal/controller/retry_mitigate.go` — `Patch` instead of `Update`
- `PLAN.md` — status-header only (implementation landed). §2.6's
  architecture note left for Claude.
- `PROPOSALS.md` — pending entry for Istio not translating `maxRetries: 0`
  into Envoy CDS (found during the Envoy-level check).
- `docs/worklog/README.md` — index this entry

## Testing
- `gofmt -l .` — clean. `make lint` — 0 issues. `go test ./...` — pass.
- New unit tests marshal the patch bytes (not the typed struct) and assert
  they contain `"attempts":0` / `"maxRetries":0`. The same tests marshal
  the typed `HTTPRetry` / `HTTPSettings` structs and assert those do
  *not* contain the zeros, so a future proto-tag change would fail the
  "this still demonstrates omitempty" check rather than silently pass.
- Existing fake-client trip/restore/handoff tests still pass on in-memory
  struct assertions.
- **Live, Kubernetes object (required):** `hack/run-k6-demo.sh retry-storm`
  against the operator on this branch. At RetryStorm trip (organic
  `FanOutAmplification → RetryStorm` handoff), raw stored JSON:
  - VirtualService `spec.http[0].retries` is `{"attempts":0}` — key
    present, value 0, `retryOn`/`perTryTimeout` cleared.
  - DestinationRule `connectionPool.http` is `{"maxRetries":0}` — key
    present, value 0. Previously this was `"http":{}`.
- **Live, Envoy admin API** (`istioctl` not installed; used
  `kubectl exec <checkout-pod> -c istio-proxy -- curl localhost:15000/config_dump`).
  The k6 RetryStorm window was only ~12s before a handoff back to fan-out,
  too short for a clean xDS settle. Follow-up: stopped the operator,
  applied the same production patch bytes via `kubectl patch` on the
  restored fixtures, waited 12s for istiod:
  - Inventory outbound **route `retry_policy` is `null`** — Istio
    rendered `attempts: 0` as retries fully disabled. Primary works at
    Envoy.
  - Inventory outbound **cluster `circuit_breakers.max_retries` is
    `4294967295`** (`2^32-1`), not 0, despite the stored object having
    `"maxRetries":0`. Secondary's 0 reaches etcd but Istio's CDS path
    does not push it. Filed as a pending `PROPOSALS.md` entry; not
    decided here. Fixtures restored afterward.

## Follow-ups / known gaps
- **Envoy-level check: done, mixed result.** Primary (`attempts: 0`) is
  enforced (route `retry_policy` absent). Secondary (`maxRetries: 0`) is
  stored on the Kubernetes object but Envoy still shows
  `max_retries: 4294967295`. See pending `PROPOSALS.md` entry — Istio
  translation, not our marshal path. Independent verification should
  re-check CDS specifically, not only `kubectl get -o json`.
- Restoration of a *zero original* at the final step still goes through
  typed `Update()` and would strip the zeros the same way. Out of scope
  this slice.
- Kind-based integration test suite remains the open checklist item;
  a marshal-round-trip assertion for zero trip values should live there
  eventually, since fake-client cannot catch this class of bug.
