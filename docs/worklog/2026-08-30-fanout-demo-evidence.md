# Fan-out demo topology + live-scrape evidence for the fan-out signal

**Date:** 2026-08-30
**Author:** Cursor
**Type:** infra

## What
Built the §2.7-locked demo topology — `checkout → {payments, inventory}`,
three minimal Go services under `demo/`, Dockerfiles, k8s+Istio manifests,
a `make demo-deploy` script — deployed it onto the existing
`kind-cascade-operator` cluster alongside `sleep`/`httpbin`, generated real
traffic through it, and queried Prometheus directly for the fan-out signal:
the request-count relationship between `checkout`'s inbound calls and its
outbound calls to `payments`/`inventory`, under normal conditions and while
`payments` is failing. No detector or mitigation code — evidence only, same
scope as the Kind+Istio dev-env slice for retry storm.

## Why
PLAN.md §2.7 locked this topology after retry storm's full loop landed, and
fan-out is a genuinely different problem from the first two signatures:
latency/error cascade and retry storm both had an obvious metric candidate
from day one (p99+error-rate; a request-count ratio) and the open question
was verifying exact PromQL against live Istio. Fan-out's own definition —
"one failing call triggering a disproportionate number of downstream
calls" — needs a service that actually fans out (one inbound request, N
outbound calls), and nothing built so far (`sleep`→`httpbin`, a plain 1:1
pair) can produce that signal at all. There was nothing to measure before
this slice.

## How

### Topology
- `demo/internal/depsvc` — shared "toggleable dependency" HTTP handler:
  `GET /` returns 200 until `POST /control/fail` flips an `atomic.Bool`,
  `POST /control/heal` flips it back, `GET /healthz` for the k8s probe. Used
  by both `demo/payments` and `demo/inventory` (each a ~10-line `main.go`
  that just calls `depsvc.Run(name, ":8080")`) so the two dependencies are
  genuinely separate binaries/pods, not one service under two Service
  names.
- `demo/checkout` — `GET /checkout` calls `payments` then `inventory` via
  `callWithAppRetry`. `inventory` gets exactly one call, no retry — it's the
  fixed control. `payments` gets up to 3 attempts (`paymentsMaxAttempts`) if
  the previous attempt returned non-2xx. This retry loop is **deliberately
  application-level** — a plain Go `for` loop calling `http.Client.Get`
  again — not an Envoy/Istio retry policy (no `VirtualService.retries` was
  applied to either dependency). The point was to test the prompt's own
  question directly: does anything amplify without this loop? See Findings.
- `demo/go.mod` is its own Go module (not part of the operator's `go.mod`) —
  keeps `make test`/`make lint`/`make generate` at the repo root completely
  untouched. Verified separately: `cd demo && go build ./... && go vet ./...
  && go test ./... && gofmt -l .` and `../bin/golangci-lint run ./...`
  (picks up the repo-root `.golangci.yml` by walking up) — all clean, 0
  lint issues. `demo/checkout/main_test.go` covers the one piece of real
  logic (`callWithAppRetry`): stops on first 2xx, retries up to the max on
  persistent failure, stops early once a retry succeeds.
- `demo/k8s/*.yaml` — one Deployment+Service per service, names
  `checkout-service`/`payments-service`/`inventory-service` in `default`,
  matching the existing `config/samples/cascade_v1alpha1_cascadepolicy.yaml`
  exactly (same names the CascadePolicy sample already `dependsOn`).
  `imagePullPolicy: IfNotPresent`, `httpGet /healthz` probes.
- `hack/deploy-demo.sh` (`make demo-deploy`) builds all three images with
  `demo/` as the Docker build context (`docker build -f
  demo/<svc>/Dockerfile demo/`), `kind load docker-image ... --name
  cascade-operator` (the actual Kind cluster name from `kind get clusters`
  — the kubectl context is `kind-cascade-operator`, one `kind-` prefix
  more), applies `demo/k8s/`, waits for all three rollouts.
- Kept `sleep`/`httpbin`: no PromQL currently in the codebase names either
  host directly (existing exact-string tests use arbitrary hostnames), so
  nothing depends on removing them, and they're the traffic-generation
  client for this slice too (see below) — ripping them out would have
  bought nothing.

### Traffic generation and evidence
Reused the already-injected `sleep` pod as the client (same approach as the
retry-storm evidence slice), talking to `checkout-service` in-mesh:

```bash
SLEEP=$(kubectl get pod -l app=sleep -o jsonpath='{.items[0].metadata.name}')
kubectl exec "$SLEEP" -c sleep -- curl -sS http://checkout-service.default.svc.cluster.local/checkout
# => checkout ok: payments=200 inventory=200
```

First attempt at generating load was a **tight burst** (60 requests back to
back, no delay) — this reproduced the exact "burst → no rate signal" trap
documented in the Kind+Istio dev-env worklog: `increase(...[5m])` on
`payments`/`inventory`'s **destination**-reporter series came back `0` even
though the raw cumulative counter was already `61` (confirmed via a
label-ful instant query on the bare metric). The counter jumped from 0→61
faster than Prometheus's own `scrape_interval: 15s` (confirmed via
`/api/v1/status/config`) could observe more than one value, so `increase()`
saw a flat series (delta between two equal samples = 0) even though the
`checkout`-side (source reporter, same burst) had enough samples to compute
something. Fix: generate traffic **spread out** — 40 requests, `sleep 1`
between each, ~40s total — so multiple 15s scrapes land during the ramp.
After that, `increase()` numbers agreed across all three services and both
reporters within extrapolation noise. This is the same discipline as the
`sum by (le)` / `URX` findings: verify the query methodology against a real
scrape before trusting a single number from it.

**Healthy baseline** (40 requests, `payments` and `inventory` both healthy),
raw cumulative counters (destination reporter, i.e. what actually arrived
at each pod), snapshotted before and after:

| service | before | after | delta |
|---|---|---|---|
| checkout (inbound) | 61 | 101 | 40 |
| payments (outbound from checkout) | 61 | 101 | 40 |
| inventory (outbound from checkout) | 61 | 101 | 40 |

Exactly **1:1:1**. No amplification anywhere — every inbound `/checkout`
request produces exactly one call to `payments` and exactly one to
`inventory`, as the handler's code says it should. This confirms the naive
fan-out (no retry logic at all) does **not** amplify anything on its own —
matching the prompt's own hypothesis.

**Payments-failing window**: `POST payments-service/control/fail`, then the
same spread-out 40-request pattern against `checkout`, `POST
payments-service/control/heal` after. Settled cumulative counters (waited
one extra scrape interval past the traffic loop before the final snapshot,
since the tight-burst lesson above made clear that querying immediately
after a loop undercounts the tail):

| service | response | before | after | delta |
|---|---|---|---|---|
| checkout (inbound) | 200 | 101 | 101 | 0 |
| checkout (inbound) | 500 | 0 | 41 | **41** |
| payments (outbound) | 200 | 101 | 102 | 1 (stray — see below) |
| payments (outbound) | 500 | 0 | 123 | **123** |
| inventory (outbound) | 200 | 101 | 142 | **41** |

- `checkout`'s own inbound delta is 41 — every request that made it through
  the mesh in this window, and every one failed (500), since
  `callWithAppRetry` returns an error for `payments` after exhausting
  `paymentsMaxAttempts=3`, and `checkout`'s handler 500s if either
  dependency errors.
- `inventory`'s delta is 41 — **exactly 1×** `checkout`'s inbound delta,
  same as the healthy baseline. `inventory` never fails and only ever gets
  one call per request, so it stays the fixed control throughout — the
  ratio that changes is entirely on `payments`' side.
- `payments`' 500 delta is 123 = **41 × 3, exactly** — matching
  `paymentsMaxAttempts` precisely. The lone stray `+1` on the 200 count is a
  single request that landed in the brief window between the `/control/fail`
  call taking effect and the very next request — not amplification, just a
  toggle-timing race in the harness, and small enough (1 in 124 payments
  calls) not to change the conclusion.
- **payments:checkout ratio = 123/41 = 3.0 exactly.** inventory:checkout
  ratio = 41/41 = 1.0 exactly.

A live `rate()`-based ratio taken mid-window (before the "settled" snapshot,
so it's smoothed over a non-uniform 1-req/s-then-stop traffic shape) gave
`sum(rate(payments{reporter=destination}[2m])) /
sum(rate(checkout{reporter=destination}[2m]))` ≈ **2.86** and the inventory
equivalent ≈ **0.76** — same order of magnitude as the settled raw-delta
numbers (3.0 / 1.0), confirming the raw cumulative-counter-delta method is
the more trustworthy way to state an exact ratio; a `rate()`/`increase()`
instant query over a bursty, short traffic pattern will always carry some
extrapolation noise, same lesson as the tight-burst `0` result above.

## Findings (the actual answer to the prompt's question)

**Amplification only happens because `checkout`'s application code has a
deliberate retry-on-failure loop — it does not happen "for free" from
Envoy/Istio defaults.** No `VirtualService.retries` was applied to
`payments` or `inventory` in this experiment; the mesh's own retry policy
never engaged (there's nothing to retry from Envoy's perspective — each of
`checkout`'s three attempts is a fully independent HTTP request/response
cycle that Envoy sees as a plain, unrelated call, exactly as intended when
writing `callWithAppRetry`). This is a genuinely different amplification
mechanism than retry storm's, and it also means the eventual fan-out
detector's PromQL will need a **cross-host** ratio, unlike retry storm's:
retry storm's ratio compares two reporters on the *same* `destination_service`
host (source vs destination on `payments`, say); fan-out's ratio needs
`destination_service=<a dependsOn host>` in the numerator against
`destination_service=<spec.service, the caller>` in the denominator — two
different hosts, both already present on the CascadePolicy CR (`spec.service`
and `spec.dependsOn`). That's a query-shape note for whoever designs the
detector next, not a change to anything already locked.

## Files touched
- `demo/go.mod`, `demo/internal/depsvc/depsvc.go` — shared toggleable-dependency HTTP handler
- `demo/payments/main.go`, `demo/inventory/main.go` — thin wrappers around `depsvc.Run`
- `demo/checkout/main.go`, `demo/checkout/main_test.go` — fan-out caller + app-level retry loop + its tests
- `demo/{checkout,payments,inventory}/Dockerfile` — multi-stage builds, context = `demo/`
- `demo/k8s/{checkout,payments,inventory}.yaml` — Deployment+Service per service
- `hack/deploy-demo.sh` — build, kind-load, apply, wait for rollout
- `Makefile` — `demo-deploy`, `demo-undeploy` targets
- `docs/demo-topology.md` — how-to, mirrors `docs/dev-istio.md`'s shape
- `docs/worklog/README.md` — index this entry
- `PLAN.md` — status line + checklist (`Demo microservice topology` now checked)

## Testing
- `cd demo && go build ./... && go vet ./... && go test ./... && gofmt -l .`
  — clean, `TestCallWithAppRetry{SucceedsFirstTry,ExhaustsAttemptsOnPersistentFailure,StopsOnceHealthy}`
  pass.
- `../bin/golangci-lint run ./...` from `demo/` — 0 issues (picked up the
  repo-root `.golangci.yml`).
- No changes to the operator's own Go code — repo-root `make test`/`make
  lint`/`make verify-generate` not run since nothing there changed, but
  confirmed `demo/` is a separate module so `go build ./...`/`go test ./...`
  from the repo root do not even see it (nested-module boundary), i.e. zero
  risk of this slice affecting CI for the operator itself.
- Live cluster: all three services deployed with sidecars (`READY 2/2`),
  full `/checkout` → `payments` + `inventory` round trip confirmed manually,
  `/control/fail` and `/control/heal` confirmed to change `checkout`'s
  response, all PromQL above run against the real `istio-system/prometheus`
  instant-query API via `hack/query-prom.sh`. Left `payments` healed and all
  three demo pods running at the end of this slice (not torn down).

## Follow-ups / known gaps
- No fan-out detector yet — this was explicitly evidence-gathering, not
  detector design. Next slice should use the cross-host ratio shape noted
  in Findings, and the CRD's `fanOutMultiplier` threshold semantics
  ("downstream calls vs baseline per inbound call", baseline implicitly 1,
  same pattern as `retryStormMultiplier`) should map directly onto the
  payments:checkout / inventory:checkout ratios measured here.
- No fan-out mitigation — same reason.
- No k6 scripts — manual curl loops were sufficient for this evidence, per
  scope.
- The stray `+1` on payments' 200 count (toggle-timing race in the manual
  harness, not the app or mesh) is noted above but not something worth
  fixing — it doesn't change the ratio's conclusion and this harness isn't
  shipped code.
- `sleep`/`httpbin` were kept, unmodified, and still running — no
  code currently depends on removing them.
