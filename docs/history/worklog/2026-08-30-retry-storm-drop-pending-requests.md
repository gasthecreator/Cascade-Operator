# Retry storm's connectionPool secondary drops `http1MaxPendingRequests`

**Date:** 2026-08-30
**Author:** Cursor
**Type:** feature

## What
Implemented the already-resolved `http1MaxPendingRequests` overlap
(`PROPOSALS.md`, approved 2026-08-30, direction 2). Retry storm's
`DestinationRule` `connectionPool.http` secondary now writes, captures,
and restores only `maxRetries`. Fan-out's primary
(`internal/mitigation/connpool.go`) is untouched. Field disjointness is
back as the invariant across the three DestinationRule-touching
signatures, not just a force-complete-on-handoff property of one shared
scalar.

Also: restore now prunes an empty `connectionPool.http` shell (the
live-observed leftover from the previous slice) when writing `MaxRetries`
back to 0 leaves nothing else on the sub-message.

## Why
Claude already decided this. Direction 2 was the right call on the
merits: `maxRetries` is the retry-specific half of this secondary, and
every other object-kind sharing in this project is field-disjoint. Keeping
both signatures on `http1MaxPendingRequests` made force-complete-on-handoff
load-bearing for that field's *data integrity* (incoming snapshot of
outgoing trip value as "original"), not just for orphaned annotations.
Dropping retry storm's claim restores the shape every other sharing case
already has.

This slice is implementation only — no new `PROPOSALS.md` entry, no
PLAN.md §2 architecture rewrite. The status-header sentence that named
this as the next slice is the only PLAN.md edit.

## How

### Trip / capture / restore
- `ApplyRetryStormConnectionPoolTrip` sets `http.MaxRetries =
  TripRetryStormMaxRetries` (still 0) and nothing else.
  `TripRetryStormMaxPendingRequests` is gone.
- `originalRetryConnectionPoolJSON` is `{MaxRetries int32}` with
  `omitempty`. Capture/restore of a 0 original still marshals as `{}`.
- Interpolation ramps only `MaxRetries`. Fan-out's
  `Http1MaxPendingRequests`/`Http2MaxRequests` are never read or written
  by this signature, including on a handoff — not merely restored after
  one.

### Empty-shell prune (the optional observation)
The previous live run left `inventory-service`'s DestinationRule with
`trafficPolicy.connectionPool.http: {}` after a full restore, rather
than the fully-absent `trafficPolicy` of the true original fixture.
Harmless (Envoy treats empty and absent identically here), and previously
deliberate: never nil the sub-message as a whole, because fan-out might
have fields living there too.

Now that this signature only ever owns `MaxRetries`, that caution no
longer applies at the *empty* case. `applyOriginalRetryConnectionPool`
writes the captured scalar, then if `httpSettingsEmpty` (every
`HTTPSettings` field, including `MaxConcurrentStreams` and
`IdleTimeout`) calls the existing `clearConnectionPoolHTTP` — same
cascade fan-out's Unset path already uses (nil http, then empty
connectionPool, then empty trafficPolicy except TLS / outlierDetection /
etc.). A sibling still set — fan-out's `Http2MaxRequests`, a user-authored
`MaxRequestsPerConnection` — keeps the block. Tests cover both sides
(`ZeroPrunesEmptyShell`, `ZeroWithSiblingKeepsBlock`).

Did **not** change the trip value. `MaxRetries=0` is proto3-zero, so a
freshly-tripped DestinationRule whose original http block was absent
may still *look* like `connectionPool.http: {}` in YAML at trip time
(Istio's own comment says default `2^32-1`; Envoy's circuit-breaker
proto says 3 — already documented on `TripRetryStormMaxRetries`). That
is a serialization quirk of the trip value, not a restore-shape bug, and
does not block this slice.

## Files touched
- `internal/mitigation/retry_connpool.go` — MaxRetries-only trip; drop
  `TripRetryStormMaxPendingRequests`; snapshot JSON is one field
- `internal/mitigation/retry_connpool_restore.go` — interpolate one
  field; prune empty http via `clearConnectionPoolHTTP`
- `internal/mitigation/retry_connpool_test.go`,
  `retry_connpool_restore_test.go` — trip does not write http1; restore
  never moves fan-out's fields; empty-shell prune / sibling-keeps-block
- `internal/controller/retry_mitigate.go`, `retry_restore.go`,
  `restore.go` — comments and DetectOnly log match the one-field
  secondary
- `internal/controller/retry_connpool_mitigate_test.go`,
  `retry_connpool_restore_test.go`, `retry_connpool_handoff_test.go` —
  only MaxRetries set/captured/restored; handoff asserts fan-out's
  http1/http2 are never written by retry storm
- `internal/controller/retry_storm_test.go`, `retry_restore_test.go`,
  `fanout_restore_test.go` — dispatch isolation and win-the-race
  assertions no longer treat http1 as this signature's field
- `PLAN.md` — status-header only: "Implementation of that drop is the
  next slice" → "That drop is now implemented." §2.6 matrix left for
  Claude.
- `docs/worklog/README.md` — index this entry

## Testing
- `gofmt -l .` — clean. `make lint` — 0 issues. `go test ./internal/mitigation/ ./internal/controller/` — pass.
- Mitigation-package tests: trip captures only `maxRetries`; does not write
  `http1MaxPendingRequests` even when fan-out's fields are already live;
  restore ramps only `maxRetries`; empty-shell prune on a zero original
  with no siblings; sibling `http2MaxRequests=128` keeps the block.
- Controller tests: DetectOnly / both-present / each-missing-the-other;
  restore e2e keeps a pre-existing `http1=64` motionless while
  `maxRetries` ramps `2,4,6,8,10`; both-direction handoff asserts fan-out's
  `http1`/`http2` are never written by retry storm (not merely restored
  after); dispatch isolation and win-the-race tests updated the same way.
- **Live-confirmed on the Kind cluster.** Recreated
  `inventory-service` DestinationRule from the original fixture (no
  `trafficPolicy`). Ran `hack/run-k6-demo.sh retry-storm` against the
  operator on this branch. Sequence: LatencyErrorCascade tripped first
  (inventory 5xx), then an organic handoff
  `LatencyErrorCascade → RetryStorm` (`outgoing: LatencyErrorCascade,
  drEdges: 1, vsEdges: 1`) followed 10s later by
  `RetryStorm → FanOutAmplification`. Snapshot at RetryStorm trip:
  `original-retry-connection-pool: '{}'`, `connectionPool.http: {}`,
  **no** `http1MaxPendingRequests` (the previous trip would have written
  `1`). `MaxRetries=0` serializes as the empty `http: {}` shell, as
  flagged above. VirtualService `retries.attempts` cleared. Final
  restored DestinationRule has fully-absent `trafficPolicy` (fan-out's
  Unset path — fan-out was last; retry storm's own prune ran on the
  force-complete into fan-out, same reconcile as fan-out's trip).

## Follow-ups / known gaps
- PLAN.md §2.6's matrix table still lists
  `http1MaxPendingRequests` on retry storm's secondary. Cursor does not
  edit that table; Claude owns PLAN.md §2.
- Kind-based integration test suite remains the only open checklist
  item.
- Trip-time `MaxRetries=0` may still serialize as an empty `http: {}`
  in YAML. Not a proposal — flagging so a future live look isn't
  surprised. Envoy treats 0 vs absent according to its own default (3),
  which is the same mismatch `TripRetryStormMaxRetries`'s comment already
  records.
