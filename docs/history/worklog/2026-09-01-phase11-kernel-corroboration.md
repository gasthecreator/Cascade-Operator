# Phase 11 (slice 2, final): kernel-event corroboration wired into detection, live-verified end-to-end

**Date:** 2026-09-01
**Author:** Claude (solo — Cursor unavailable)
**Type:** feature (new detection-pipeline enrichment; no change to which
signature trips or when — see "How" below)

## What
- `internal/signatures/kernel_corroboration.go` (new): `ApplyKernelCorroboration(v Verdict, kernelEventCount float64) Verdict` —
  a flat, capped (`KernelCorroborationBoost = 0.15`) confidence bump plus an
  evidence-string addition, applied only to an *already*-tripped verdict.
  A no-op whenever `!v.Tripped`, `kernelEventCount <= 0`, or the count
  isn't finite.
- `internal/controller/kernel_corroboration.go` (new):
  `tetragonKernelEventCountQuery(host, windowSeconds)` — one more PromQL
  string through the exact same `metrics.Querier` every other signal
  already uses — and `(*CascadePolicyReconciler).applyKernelCorroboration`,
  a best-effort wrapper (query error, no data, or `r.Metrics == nil` all
  degrade to "no corroboration this tick," never a reconcile error).
- `cascadepolicy_controller.go`: `evalLatencyError`/`evalRetryStorm`/
  `evalFanOut` each call `applyKernelCorroboration` — but only when their
  own verdict already tripped, so a healthy tick never pays for the extra
  query.
- `hack/install-tetragon.sh`: now also applies `tcp-reset-policy.yaml`
  (added in the prior slice, previously missing from this script) and
  patches whichever mesh Prometheus ConfigMap(s) are present with a static
  scrape job for Tetragon's own `/metrics` endpoint, restarting Prometheus
  to pick it up. Best-effort per mesh — skips cleanly if that mesh's
  Prometheus isn't installed.

## Why
PLAN.md §5 Phase 11's stated design: "surfaced in `Verdict.Evidence` / an
optional confidence adjustment, never a hard dependency — detection must
work identically with Tetragon absent." The prior slice closed the
fault-injection gap (a real TCP RST now fires and Tetragon captures it);
this slice is the actual wiring that design called for.

## How

### The real design question this slice had to answer first: how do you *query* Tetragon at all?
Tetragon's own event stream (`export-stdout`) is a log, not something this
project's existing, entirely-Prometheus-poll-based reconciler could query
in a time window. Checked live, rather than assuming a bespoke
log-tailing component was needed: Tetragon's Helm chart already exposes a
`:2112/metrics` endpoint in real Prometheus exposition format, including
`tetragon_events_total{namespace,workload,pod,type,binary}` — a genuine
counter, confirmed live via `curl http://<tetragon-svc>:2112/metrics`
before writing any Go code against it. That single finding is what let
this slice reuse the *entire* existing architecture (`metrics.Querier`,
one more PromQL string) instead of building a second, parallel
event-ingestion pipeline — the only new infrastructure needed was making
sure a Prometheus instance actually scrapes that endpoint.

### A real, stated limitation: `tetragon_events_total` doesn't disambiguate which kprobe fired
Confirmed live (`type="PROCESS_KPROBE"` is the only event-type label; the
only labels that *do* carry `attach`/`policy` are the separate
`tetragon_missed_*_probes_total` diagnostic metrics, not the actual event
counter) that this metric can't currently tell a TCP reset from a
retransmit from any other kprobe-tracked event. In this project's current
state that is an *accurate, not hidden* simplification:
`tcp-reset-policy.yaml` (via `/control/reset`) is the only mechanism that
has ever actually produced a real kprobe event on the demo topology —
`tcp-retransmit-policy.yaml` remains genuinely unexercised (prior slice's
own finding). If a future slice adds a real packet-loss mechanism too,
this query would need refining (Tetragon's per-event JSON export, not
this aggregate counter) to disambiguate them — not assumed to still be
precise as-is, and said so directly in the code's own doc comment.

### Wiring shape: corroboration lives in `internal/signatures`, fetching lives in the controller
`ApplyKernelCorroboration` is exported and called explicitly by the
controller on an already-tripped verdict, rather than folded into each
`DetectX`'s own `Input` struct — corroboration is the same, orthogonal
step for all three signatures, not specific to any one of them, so it's
one function called from three call sites rather than three duplicated
fields. The query itself isn't part of `mesh.QueryBuilder`: Tetragon
observes the kernel, not mesh-proxy metrics, so the same query applies
regardless of which mesh a policy selects — forcing it through a
mesh-specific interface would be a poor fit.

### What corroboration does *not* do
`Verdict.Confidence` doesn't gate anything today — mitigation already
triggers purely on `Tripped`, confirmed by re-reading `detectSignatures`'s
own dispatch before wiring this in. So this slice's only observable
effect is a richer log line (`kernel_corroboration=true
kernel_events=N`) and a higher logged `Confidence` — exactly the scope
PLAN.md's own text calls for ("an optional confidence adjustment"), not
a new gate on mitigation decisions.

## Live verification (real cluster, real Prometheus, real Reconcile — not a fake querier)
1. Patched both `istio-system/prometheus` and `linkerd-viz/prometheus-config`
   to scrape Tetragon; confirmed via each Prometheus's own `/api/v1/targets`
   that the `tetragon` job came up `health: "up"`.
2. Confirmed the exact corroboration query
   (`sum(increase(tetragon_events_total{namespace="linkerd-demo",workload="inventory-service",type="PROCESS_KPROBE"}[30s]))`)
   read `0` at a healthy baseline, then `151.5` immediately after inducing
   a real `/control/reset` window — the query genuinely reacts to a real
   disruption, not a canned value.
3. **The full end-to-end proof**: drove `inventory-service` through
   `/control/slow` (real p99/error-rate signal) followed by
   `/control/reset` (real kernel signal) inside the same 60s window, then
   ran the actual, unmodified `CascadePolicyReconciler.Reconcile` — using
   the real `metrics.Client` HTTP querier pointed at linkerd-viz's
   Prometheus (now scraping both Linkerd proxy metrics and Tetragon), not
   a fake — against a real `CascadePolicy`. Result, straight from the
   reconciler's own log line:
   ```
   "tripped": true, "confidence": 1,
   "evidence": "dependency=inventory-service.linkerd-demo.svc.cluster.local
     p99_ms=995 (threshold 500) error_rate=0.1667 (threshold 0.05)
     latency_spike=true error_rise=true
     kernel_corroboration=true kernel_events=129.6"
   ```
   `LatencyErrorCascade` tripped for real on real p99/error-rate readings,
   and the corroboration boost and evidence text both applied correctly —
   the full pipeline, end to end, on real data, not mocked at any layer.
4. **A real bug caught by actually running the updated
   `hack/install-tetragon.sh`, not assumed correct**: its first version
   used `kubectl get configmap ... -o jsonpath="{.data.${key}}"` with
   `key="prometheus.yml"` — an unescaped `.` inside a jsonpath expression
   is a field separator, not a literal character, so this silently
   returned empty rather than erroring, and the script's own Python
   YAML-parsing step then crashed on the empty input
   (`TypeError: 'NoneType' object is not subscriptable`). Fixed by
   escaping the key's dots before interpolating into the jsonpath
   expression. Verified the fix on both branches: reverted
   `istio-system/prometheus` to its pre-patch content and re-ran the
   script to confirm the "add a scrape job" path now works, then
   confirmed the target came back `up`; separately confirmed the
   "already scrapes tetragon" idempotency check correctly detects an
   already-patched ConfigMap without re-patching it.

## Files touched
- `internal/signatures/kernel_corroboration.go`,
  `kernel_corroboration_test.go` (new)
- `internal/controller/kernel_corroboration.go`,
  `kernel_corroboration_test.go` (new)
- `internal/controller/cascadepolicy_controller.go` — three call sites
- `hack/install-tetragon.sh` — applies both policies, patches Prometheus
  scrape config
- `Makefile` — updated `tetragon-install` description

## Testing
- `go build`/`go vet`/`gofmt -l .`/`go test ./... -race`/`make lint` —
  clean, including new unit tests for `ApplyKernelCorroboration` (no-op
  cases, boost, cap-at-1) and `applyKernelCorroboration`/
  `tetragonKernelEventCountQuery` (exact query string, unparseable-host
  degradation, query-error/no-data/nil-Metrics all no-ops).
- `demo/` module: `go build`/`go test ./... -race` unaffected (no changes
  in this slice).
- **Live-verified** end-to-end against the real dev cluster — see "Live
  verification" above, including a real bug in the install script caught
  and fixed by actually running it, not assumed correct from reading it.

## Follow-ups / known gaps
- This is the last piece of Phase 11 PLAN.md called for. The one
  remaining, already-documented limitation:
  `tetragon_events_total{type="PROCESS_KPROBE"}` doesn't disambiguate
  *which* kprobe fired — accurate for this project's current state
  (only `/control/reset` produces real events today) but would need
  refining if a real packet-loss mechanism is ever added alongside it.
- CI does not install Tetragon or exercise this corroboration path — it's
  a local dev-environment enhancement (`make tetragon-install`), the same
  scope the original spike itself was given, not extended here.
