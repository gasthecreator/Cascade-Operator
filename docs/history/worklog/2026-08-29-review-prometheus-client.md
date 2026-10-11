# Review: Prometheus HTTP client slice

**Date:** 2026-08-29
**Author:** Claude
**Type:** docs, fix

## What
Reviewed `feat/prometheus-client` against PLAN.md §2.4 by independently
rebuilding, testing, and reading the code — not from the summary alone.
Found and fixed one real gap: the client only extracted Prometheus's
structured `{status,errorType,error}` error envelope on HTTP 200 responses;
real Prometheus sends query errors (bad PromQL, execution failures) as
non-200 (400/422/503) with that same envelope, so the clean error message
was effectively unreachable in the case that actually happens in practice.

## Why
Same reviewer role as the last two slices: verify before approving, and fix
small well-scoped defects directly rather than bouncing them back for a full
round trip.

## How
- `go build`, `gofmt -l`, `go vet`, `go test ./internal/metrics/... -cover`,
  `make lint`, `make test`, `make verify-generate` — all run independently,
  all matched or exceeded the reported numbers (80.2% metrics coverage
  reported and confirmed; controller unchanged at 81.2%; 0 lint issues
  confirmed).
- Diffed `PLAN.md`: status line and two checklist boxes only, no architecture
  edits — protocol held.
- Read `internal/metrics/{snapshot,client}.go` end to end against §2.4:
  `Querier` interface is exactly `Query(ctx, promql) (Snapshot, error)`;
  `Snapshot`/`Sample` are plain structs with no Kubernetes or
  Prometheus-client-library types; instant query only
  (`/api/v1/query`, never `/api/v1/query_range`); matrix `resultType` is a
  hard error, which is the correct way to fail loudly if a detector ever
  passes a range query by mistake instead of letting Prometheus own the
  window per §2.4.
- Confirmed the reconciler wiring is genuinely inert this slice — `Metrics`
  field added, injected from `main` when `--prometheus-url`/`PROMETHEUS_URL`
  is set, but `Reconcile` never calls `Query`. Correct call: a placeholder
  query would have proven nothing about real Istio PromQL and would have
  perturbed the already-verified smoke-test log output for no reason.
- **Found the gap:** `TestQueryPrometheusErrorStatus` tests `status=error`
  paired with HTTP 200. Real Prometheus's documented behavior
  (`/api/v1/query`) is to send query errors as 400 (bad_data), 422
  (execution), or 503 (unavailable) — carrying the identical JSON envelope.
  The client's non-200 branch only ever produced a generic
  `HTTP <code>: <raw truncated body>` message, never reaching the
  errorType/error extraction that the 200 path has. Not a crash or
  correctness bug — the raw JSON was still present in the truncated string —
  but the "clean" error path was dead code against real Prometheus traffic,
  and the test suite's claimed error-case coverage didn't actually exercise
  the shape that happens in practice.
- **Fixed directly** (small, contained, reviewer-appropriate rather than a
  full slice to hand back): added `parseErrorBody` in `client.go`, tried on
  the non-200 path before falling back to the raw truncated body. Added
  `TestQueryPrometheusErrorStatusCode` using HTTP 422 with a realistic
  `execution` errorType, matching Prometheus's actual documented behavior for
  an unexecutable query. Re-ran the full suite: coverage moved from 80.2% to
  80.4%, still 0 lint issues, gofmt clean.

## Verdict
**Approved with one fix applied.** The design (interface shape, instant-query
decision, matrix rejection, dependency-free `Snapshot`, not wiring `Query`
into the reconciler yet) is exactly right and needed no changes. The one gap
was a real but narrow error-message-quality issue, now fixed and tested.

## Files touched
- `internal/metrics/client.go` — `parseErrorBody` + non-200 path now uses it
- `internal/metrics/client_test.go` — `TestQueryPrometheusErrorStatusCode`
- `docs/worklog/2026-08-29-review-prometheus-client.md` — this file
- `docs/worklog/README.md` — index this entry

## Testing
See "How" above — full independent re-verification, plus the new test for
the fix itself.

## Follow-ups / known gaps
- Next slice (Cursor's, per its own worklog entry): the latency/error-cascade
  detector as a pure function over `Snapshot`, then wiring `Query` onto the
  existing 10s tick.
- `response_flags=UR` still unverified against a real Istio install — carried
  forward, not resolved here.
- `config/manager` doesn't set `PROMETHEUS_URL` yet — expected, there's no
  Prometheus in the cluster to point at until a later slice.
