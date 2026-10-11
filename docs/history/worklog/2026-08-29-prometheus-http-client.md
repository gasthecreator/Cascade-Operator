# Prometheus HTTP client behind Query → Snapshot

**Date:** 2026-08-29
**Author:** Cursor
**Type:** feature

## What
Added `internal/metrics`: a Prometheus HTTP client behind
`Query(ctx, promql) (Snapshot, error)`, operator-level `--prometheus-url` /
`PROMETHEUS_URL` config, and httptest coverage. Did not issue PromQL from the
reconciler this slice, and did not stand up Prometheus on Kind.

## Why
PLAN.md §2.4: poll Prometheus's HTTP API on the existing reconcile tick, with
detectors remaining free of Kubernetes and Prometheus-client types. The
checklist item is "Prometheus client + PromQL query layer" — the next slice
wires one detector through this client, not all three.

## How
- Instant `GET /api/v1/query` only. `rate(...[30s])` / `histogram_quantile`
  are already range-vector expressions; Prometheus owns the window. Implementing
  `/api/v1/query_range` would return a matrix and invite a local ring buffer,
  which §2.4 explicitly rejects. Matrix `resultType` is a hard error so a
  mistaken range query fails loudly.
- No `prometheus/client_golang` (or any other Prom library). `net/http` +
  `encoding/json` so the types that cross the interface are ours:
  `Snapshot` is an instant vector of `Sample{Labels, Value, Timestamp}`. Scalar
  results flatten to one sample with empty labels. Detectors can take
  `metrics.Snapshot` without importing this client.
- Default HTTP timeout 5s; `Query`'s context can cancel sooner. Body capped at
  1MiB. URL must be `http`/`https` with a host.
- Flag default is `os.Getenv("PROMETHEUS_URL")` so either `--prometheus-url` or
  the env var works; empty means no client (Kind smoke test keeps working).
  The reconciler gets a `Metrics metrics.Querier` field; `main` injects the
  client when configured. Reconcile still does not call `Query` — a dummy
  `vector(1)` every 10s would not prove Istio PromQL and would change the
  already-verified smoke-test logs. The next slice issues real queries on this
  same `RequeueAfter` tick.
- Did not treat `response_flags=UR` as fact. Test fixtures use generic labels
  (`destination_service`, `response_code`). Retry-metric shape waits on a real
  Istio scrape.
- Skipped a live Prometheus on `kind-cascade-operator`. Unit tests cover the
  client; Kind+Prometheus is extra infra with no Istio metrics to query yet.

## Files touched
- `internal/metrics/snapshot.go` — `Querier`, `Snapshot`, `Sample`
- `internal/metrics/client.go` — HTTP instant-query client
- `internal/metrics/client_test.go` — httptest: vector, scalar, empty, Prom
  error, HTTP 500, malformed JSON, matrix reject, empty PromQL, cancel, timeout
- `cmd/main.go` — `--prometheus-url` / `PROMETHEUS_URL`, inject client
- `internal/controller/cascadepolicy_controller.go` — optional `Metrics` field
- `PLAN.md` — checklist + status line
- `docs/worklog/README.md` — index this entry

## Testing
- `go test ./internal/metrics/` — all cases pass (no Prometheus, no Kind).
- `make test` — metrics 80.2%, controller still 81.2% (nil Metrics, same as
  scaffold).
- `make lint` — 0 issues. `make verify-fmt` — clean.
- No live Prometheus integration; not blocking.

## Follow-ups / known gaps
- Next slice: latency/error-cascade detector as a pure function over
  `Snapshot`, then reconciler `Query` on the 10s tick. Not this PR.
- `response_flags=UR` still unverified against Istio.
- In-cluster `config/manager` does not yet set `PROMETHEUS_URL`; pass the
  flag or env when Prometheus exists.
