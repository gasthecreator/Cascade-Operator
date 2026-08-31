# Phase 8: postmortem generator (cmd/postmortem)

**Date:** 2026-08-31
**Author:** Claude (solo — Cursor unavailable)
**Type:** feature (new CLI, no runtime/operator behavior change)

## What
`cmd/postmortem` — an on-demand CLI that renders a real incident postmortem
(timeline, root cause, blast radius, restoration status) for one
`CascadePolicy`'s most recent trip, reading its live status plus
Prometheus:

```bash
go run ./cmd/postmortem --policy=checkout-service --prometheus-url=http://127.0.0.1:19090
```

## Why
PLAN.md §5 Phase 8's original text says root cause comes from "the
signature's own logged confidence/evidence string" — but
`signatures.Verdict.Evidence` is only ever `log.Info`'d
(`internal/controller/cascadepolicy_controller.go`'s `evalLatencyError`
etc.), never persisted to `CascadePolicy.status`. Requiring this tool to
read the operator's pod logs (via `kubectl logs`, filtered by timestamp/
reconcileID) would be a much heavier, more fragile dependency than
reconstructing the same numbers from Prometheus history instead —
Prometheus's own PromQL `@<unix-timestamp>` modifier lets a query be
evaluated *as of* `status.lastTrippedAt` exactly, which reconstructs real
historical evidence without needing log access at all. Verified live: the
tool's root-cause section for a real trip showed p99=995ms/error_rate=1.0
at the trip timestamp, matching what actually happened.

**Real, honestly-scoped limitations, not silently assumed away:**
- `status.LastSignature` records which *signature* tripped, not which
  *dependsOn host* — the tool assumes the first configured dependency,
  correct for this project's demo topology (one dependency per signature)
  but not precise for a policy with multiple hosts tripping independently.
  Fixing this would mean adding a host field to status — out of scope here.
- Restore completion has no timestamp in status either, so "resolution
  timing" is a *typical* ramp duration computed from known constants
  (`RestoreFinalStep=4` × the reconciler's 10s cadence ≈ 50s), not a
  measured one. Both limitations are stated plainly in the generated
  report's own "Known limitations" section, not hidden.

## How
- Builds a plain `controller-runtime` `client.New(...)` (no manager, no
  leader election, no webhook server) — a one-shot CLI has no business
  paying for any of that.
- Reuses `internal/metrics.Client`/`Querier` exactly as-is (`Query(ctx,
  promql string) (Snapshot, error)`) — the historical query is just a
  normal PromQL string with `@<unix>` appended, no client changes needed.
- **Deliberately duplicates**, rather than imports, the four PromQL
  query-builder functions from `internal/controller/promql.go` (p99,
  error-rate; retry-storm and fan-out ratios weren't needed here). Importing
  `internal/controller` from a CLI would pull in the full manager/
  client-go-scheme/webhook dependency graph for four `fmt.Sprintf` calls.
  This is a known, deliberate duplication — noted here rather than hidden —
  and the natural fix is Phase 6's own planned `mesh.QueryBuilder`
  interface, which will give both this tool and the reconciler one shared,
  properly-abstracted source for these queries instead of two copies.
- Report rendered via `text/template`, not string concatenation — the
  template embeds its own "Known limitations" section so every generated
  report carries the caveats forward, not just this worklog entry.

## Files touched
- `cmd/postmortem/main.go` (new)

## Testing
- `go build ./...`, `go vet ./...`, `gofmt -l .` — clean.
- **Live verification**: ran against the actual dev cluster mid-Phase-9
  benchmark run (reusing its already-running Prometheus port-forward and
  operator instance) — the generated report's timeline, root-cause
  numbers, and blast-radius figures were cross-checked against the real
  `CascadePolicy` status and Prometheus at the time and matched.

## Follow-ups / known gaps
- See "Why" above for the two limitations stated directly in every
  generated report.
- **Update, Phase 6.1 (later the same day):** the mesh query-builder
  interface this note anticipated now exists (`internal/mesh`,
  `internal/mesh/istio`) — but it turns out *not* to fit this command's
  actual need cleanly. Its methods return a live "now" query; this
  command needs the `@<unix-timestamp>` historical modifier applied
  *inside* the `rate(...[30s])` range-vector selector specifically (the
  only place PromQL's `@` modifier is unambiguously well-defined), which
  the interface has no parameter for. Forcing that in — e.g. string-
  splicing `@<ts>` into a black-box query string — would be more fragile
  than the duplication it's meant to remove. Left as intentional,
  independently-justified duplication rather than a bad-fit abstraction;
  worth reconsidering only if `mesh.QueryBuilder` grows a
  timestamp-aware variant for its own reasons.
- No automated test suite for this command (a CLI-only, live-cluster-
  verified tool, consistent with how `hack/*.sh` scripts in this repo are
  treated — not unit-tested, verified live instead).
