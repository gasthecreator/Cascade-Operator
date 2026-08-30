# Kind + Istio local mesh; PromQL and response_flags evidence

**Date:** 2026-08-29
**Author:** Cursor
**Type:** infra

## What
Pinned Istio **1.30.4** (current stable; matches `istio.io/client-go`) onto the
existing Kind cluster via `make istio-install`, deployed Istio's sleep +
httpbin samples (`make istio-samples`), and queried Prometheus with the exact
`latencyP99Query` plus a retry-fault experiment. No operator Go logic changed.
The custom §2.7 demo topology was not built.

## Why
PLAN.md §2.7's Kind+Istio checklist item, and the two live-data questions
carried since the metrics/detector slices: whether `histogram_quantile`
without `sum by (le)` is wrong on a real scrape, and whether retries show
up as `response_flags=UR`.

## How
- **Pin:** GitHub `istio/istio` latest stable release is 1.30.4
  (2026-08-27). 1.31.0 is still RC. Same version as go.mod's client-go.
- **Install:** `istioctl install --set profile=demo -y`, then
  `samples/addons/prometheus.yaml` from the 1.30.4 tag, then
  `kubectl label namespace default istio-injection=enabled`. Script lives at
  `hack/install-istio.sh`; `istioctl` lands in `bin/` like `controller-gen`.
- **Workload:** Istio sample `sleep` + `httpbin` (READY 2/2). Not Bookinfo,
  not checkout/payments/inventory.
- **p99 query (exact operator PromQL)** after sustained sleep→httpbin `/get`
  traffic, instant query against `istio-system/prometheus`:

```
histogram_quantile(0.99, rate(istio_request_duration_milliseconds_bucket{destination_service="httpbin.default.svc.cluster.local"}[30s]))
```

Result: **2 series**, both finite once the 30s window had a non-zero rate
(earlier, a single burst produced `NaN` because `rate()` had no observations):

| reporter | response_code | p99 (ms) |
|---|---|---|
| source | 200 | 23.95 |
| destination | 200 | 9.30 |

`count(..._bucket{...})` = 40 (20 `le` buckets × 2 reporters). `le` values
are the usual Istio histogram (0.5 … +Inf).

`histogram_quantile(0.99, sum by (le) (rate(...[30s])))` → **one** sample,
~22.90ms. That merges source and destination of the **same** requests
(double-count). So: multiple series, yes; garbage mixed-`le` histogram, no;
`sum by (le)` across both reporters is the wrong fix. Proposed PromQL is
`reporter="source"` then `sum by (le)` — see PROPOSALS.md.

- **Retries:** `config/dev/httpbin-retry-vs.yaml` (`retries.attempts: 3`,
  `retryOn: 5xx`) + 15–20 curls to `httpbin:8000/status/503`. Client HTTP
  status 503. Prometheus (`increase(istio_requests_total{destination_service="httpbin.default.svc.cluster.local"}[2m])`):

| reporter | response_code | response_flags | increase (approx) |
|---|---|---|---|
| source | 503 | **URX** | ~36 |
| destination | 503 | `-` | ~104 |
| source | 200 | `-` | leftover /get traffic |
| destination | 200 | `-` | leftover /get traffic |

Cumulative counters after the 503-only burst: source 503/URX = **35**,
destination 503/`-` = **140** = 35 × 4 (1 try + 3 retries). A cluster-wide
`response_flags=~".*UR.*"` returned **only URX**. `UR` never appeared. A
50% abort + retry follow-up produced 200 with `flags="-"` on success, still
no `UR`. Retry-storm groundwork: use **URX** (exhausted budget) and/or the
destination:source ratio, not `UR` alone.

## Files touched
- `hack/install-istio.sh` — download pinned istioctl, demo profile, Prometheus, inject
- `hack/deploy-istio-samples.sh` — sleep + httpbin
- `hack/query-prom.sh` — one-shot PromQL via port-forward :19090
- `config/dev/httpbin-retry-vs.yaml` — retry VS for the flags experiment
- `docs/dev-istio.md` — short how-to (not the polished README)
- `Makefile` — `istio-install`, `istio-samples`, `query-prom`; `ISTIO_VERSION=1.30.4`
- `PROPOSALS.md` — two pending entries (p99 PromQL; URX vs UR)
- `PLAN.md` — status line + Kind+Istio checklist
- `docs/worklog/README.md` — index this entry

## Testing
Ran on `kind-cascade-operator` (context `kind-cascade-operator`):

```
make istio-install     # Istio 1.30.4 demo + Prometheus, injection on default
make istio-samples     # sleep + httpbin 2/2
kubectl exec "$SLEEP" -c sleep -- sh -c 'for i in $(seq 1 80); do curl -sS -o /dev/null http://httpbin:8000/get; done'
# port-forward svc/prometheus 19090:9090, then the queries above
kubectl apply -f config/dev/httpbin-retry-vs.yaml
kubectl exec "$SLEEP" -c sleep -- curl ... http://httpbin:8000/status/503
```

`istioctl client version: 1.30.4`. No Go tests in this slice. `make lint`
not required for scripts/docs; no Go files changed.

## Follow-ups / known gaps
- Pending PROPOSALS.md review before touching `promql.go`.
- Custom 3-service demo topology and k6 still later.
- Retry-storm / fan-out detectors still unbuilt; they now have scrape evidence.
- Kind cluster left running with Istio + samples. `istioctl uninstall --purge`
  if you want it gone; do not delete the Kind cluster unless you mean to.
