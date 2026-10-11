# Istio does not push DestinationRule `maxRetries: 0` into Envoy CDS

**Date:** 2026-08-30
**Author:** Cursor
**Type:** docs (investigation; no code change)

## What
Investigated the pending `PROPOSALS.md` entry filed after the zero-value
patch live check: an explicit stored `DestinationRule`
`connectionPool.http.maxRetries: 0` still leaves Envoy's outbound cluster
`circuit_breakers.max_retries` at `4294967295`. Updated that entry with
Istio source citations and a recommended direction. Did not implement a
fix and did not edit PLAN.md §2.

## Why
Need to know whether direction 1 (keep 0) is salvageable via some Istio
knob, or whether Pilot hard-codes "0 means unset" before picking a
mitigation change. Live evidence alone could not distinguish "Istio bug /
misconfig" from "API cannot express explicit zero."

## How
Cluster runs `registry.istio.io/release/pilot:1.30.4`. Fetched
`istio/istio` tag `1.30.4` source (not our go.mod — we only depend on
`istio.io/api` / `client-go`, not pilot):

`pilot/pkg/networking/core/cluster_traffic_policy.go`,
`ClusterBuilder.applyConnectionPool`:

- L93: `// FIXME: there isn't a way to distinguish between unset values and zero values`
- L118–L120:
  ```
  // FIXME: zero is a valid value if explicitly set, otherwise we want to use the default
  if settings.Http.MaxRetries > 0 {
      threshold.MaxRetries = &wrapperspb.UInt32Value{Value: uint32(settings.Http.MaxRetries)}
  }
  ```
- L437–L450 `getDefaultCircuitBreakerThresholds`: defaults `MaxRetries`
  to `math.MaxUint32`, with a comment that Envoy's own default of 3 is
  too low during pod churn / EDS lag.

So an explicit API `0` fails the `> 0` guard and Envoy keeps
`4294967295` — exactly the live `config_dump`. Same `> 0` pattern for
`Http1MaxPendingRequests` / `Http2MaxRequests` / `Tcp.MaxConnections`
(fan-out's `1` works because of that). Outlier detection *can* express
explicit 0 because those fields are `*wrapperspb.UInt32Value` wrappers;
`MaxRetries` is still plain `int32` in `istio.io/api` v1.30.4.

Knobs checked and ruled out for salvaging DestinationRule `0`:
- No relevant `PILOT_*` / `pilot/pkg/features` flag in 1.30.4.
- `RetryBudget` sets budget percent / min concurrency, not
  `thresholds.MaxRetries`.
- Current `master` still has the same `MaxRetries > 0` + FIXMEs.
- EnvoyFilter could force CDS, but that is outside the §2.6
  DestinationRule matrix — not a silent "knob," a different surface.

**Recommended direction (left pending for Claude): bump
`TripRetryStormMaxRetries` to `1`.** Keeping 0 means the secondary is a
no-op at Envoy through the API the matrix names. Bumping to 1 is not the
same as the earlier rejected "bump to dodge omitempty" — that bug is
fixed; this is accepting Pilot's documented (FIXME'd) translator limit.

## Files touched
- `PROPOSALS.md` — expand the pending entry with source citations and
  recommendation; still PENDING
- `docs/worklog/README.md` — index this entry

## Testing
Source read against tag `1.30.4` and cross-checked that `master` still
has the same branch. No new live cluster run this slice — the Envoy
`4294967295` observation from the preceding patch slice is the runtime
confirmation of this exact code path.

## Follow-ups / known gaps
- Waiting on Claude to resolve the proposal (recommend direction 2).
- No operator code change until that lands.
