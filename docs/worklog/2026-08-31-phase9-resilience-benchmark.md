# Phase 9: quantified resilience benchmark

**Date:** 2026-08-31
**Author:** Claude (solo — Cursor unavailable)
**Type:** feature (new hack script + generated results doc, no runtime/operator behavior change)

## What
`hack/run-benchmark.sh` (+ `make benchmark`) runs each of `demo/k6/*.js`'s
three cascade scenarios twice against the live dev Kind cluster — once
with `checkout-service`'s `CascadePolicy` in `DetectOnly` (the real
baseline) and once in `Mitigate` — measuring time-to-detect, blast radius
(total/5xx requests over the incident window via Prometheus `increase()`),
and time-to-restore, writing `docs/benchmark-results.md`.

## Why
PLAN.md §5 Phase 9 exists to convert "it works" into a falsifiable,
measured claim, using infrastructure that already exists (`demo/k6/*.js`,
`internal/controller/promql.go`'s query shapes, the CRD's existing
`DetectOnly`/`Mitigate` modes) rather than building anything new.

## How
- Starts its own Prometheus port-forward and its own `go run ./cmd/main.go`
  (`ENABLE_WEBHOOKS=false` — see the real bug this surfaced, below), so the
  whole benchmark is one command rather than `demo/k6/README.md`'s
  four-terminal manual setup.
- Drives each k6 scenario in the background (not blocking) so it can poll
  `CascadePolicy.status.phase` every 2s and timestamp the actual
  `Tripped`/`Normal` transitions, rather than parsing the script's own
  internal relative timeline.
- **Real bug found and fixed while building this, not simulated**:
  `demo/k6/README.md`'s own documented `go run ./cmd/main.go
  --prometheus-url=...` instruction has been broken since Phase 3 landed —
  the manager always constructs a webhook HTTPS server, which tries to
  load a TLS cert from the default `k8s-webhook-server/serving-certs`
  directory the moment anything registers a handler on it, and this dev
  cluster has no cert-manager or manually-issued cert. Traced into
  `sigs.k8s.io/controller-runtime@v0.24.1`'s own source
  (`pkg/manager/internal.go`'s `GetWebhookServer`) to confirm the fix is
  exactly `ENABLE_WEBHOOKS=false` (already a real, working escape hatch in
  `cmd/main.go` — it just was never documented as required) rather than a
  code change. Fixed in this script, `hack/capture-episode.sh`, and
  `demo/k6/README.md` itself.
- **Second real bug found and fixed mid-benchmark**: a k6 scenario's own
  `/control/heal` call is a single, unretried HTTP request, and
  `demo/internal/depsvc`'s fault injection (1-in-5 requests fail during
  "slow" mode) applies uniformly to every request that service receives —
  including its own heal endpoint. Bad luck can leave a fault permanently
  un-healed for every scenario run afterward. Added `force_heal_all()`
  (calls `/control/heal` directly via the `sleep` pod after every run) as
  a safety net independent of the script's own single unretried call —
  same fix applied to `hack/capture-episode.sh`.
- **Third finding, flagged to a separate task rather than fixed here**:
  while investigating the retry-storm scenario's apparently-stuck
  `LatencyErrorCascade` trip (which turned out to be the heal-race above),
  found `internal/controller/promql.go`'s `errorRateQuery` had no `sum()`
  aggregation, unlike the p99 query — spawned as its own background task
  (`task_d124f1e4`), since it's real production detection-accuracy code,
  not benchmark-script code, and deserved its own dedicated verification
  pass rather than a fix-in-passing. That task completed independently
  (see `docs/worklog/2026-08-31-error-rate-query-sum-fix.md`) while this
  benchmark was still running — see this benchmark's own results doc for
  how that timing affects (and doesn't affect) its numbers.

## Files touched
- `hack/run-benchmark.sh` (new)
- `Makefile` — `benchmark` target
- `demo/k6/README.md` — documents the `ENABLE_WEBHOOKS=false` requirement
- `docs/benchmark-results.md` (generated)

## Testing
- Live-verified only, by definition — this whole phase's deliverable is a
  live-cluster run, not a unit-testable one. Ran end to end twice: the
  first run hit the heal-race bug (retry-storm's own run inherited a
  stuck `LatencyErrorCascade` fault from latency-error-cascade's Mitigate
  leg's unlucky heal failure), diagnosed live via direct PromQL queries
  and a manual `/control/heal` call, then re-ran clean after hardening the
  script with `force_heal_all()`.
- `go build ./...`, `go vet ./...`, `gofmt -l .`, `go test ./... -race`,
  `make lint` — all clean (this phase's Go-code footprint is zero; these
  confirm the rest of the repo is unaffected).

## Follow-ups / known gaps
- See `docs/benchmark-results.md`'s own "Notes and caveats" section for
  the honest read of this run's numbers — in particular, retry-storm's
  modest error-rate improvement under Mitigate is expected (the mitigation
  reduces load/amplification, not the dependency's own downtime), and
  fanout-amplification's `time_to_detect=0`/`n/a` figures are consistent
  with `demo/k6/README.md`'s already-documented boundary-blip phenomenon
  plus this script's coarse 2-second poll granularity.
- Single run, not averaged — a real Kind node's resource contention means
  absolute numbers will vary between runs; the relative Mitigate-vs-
  DetectOnly comparison within each scenario is the more defensible read.
- Re-running after the `errorRateQuery` fix (above) is a reasonable
  follow-up, not required before treating this as done.
