# Kind + Istio local mesh (validation, not the demo topology)

Pin **Istio 1.31.0** (current stable as of 2026-09-04; matches
`istio.io/client-go` in go.mod — bumped from 1.30.4, see
`docs/worklog/2026-09-04-istio-1.31-upgrade.md` for what was checked before
moving the pin). The demo profile plus the sample Prometheus addon is
enough to scrape real Envoy metrics. This is **not** the §2.7
`checkout → {payments, inventory}` graph — that is a later checklist item.

## Prerequisites

- Kind cluster in the current kube context (the scaffold smoke test left
  `kind-cascade-operator` running).
- `kubectl`, `curl`, Docker.

## Install

```bash
make istio-install          # istioctl + demo profile + Prometheus + injection on default
make istio-samples          # sleep + httpbin (sidecar-injected)
```

`hack/install-istio.sh` downloads `istioctl` into `bin/` (gitignored), same
pattern as `controller-gen`. Override with `ISTIO_VERSION=…` only if you
know you want a different pin.

Confirm sidecars:

```bash
kubectl get pods -l 'app in (sleep,httpbin)'
# READY 2/2
```

## Generate traffic and query Prometheus

```bash
SLEEP=$(kubectl get pod -l app=sleep -o jsonpath='{.items[0].metadata.name}')
kubectl exec "$SLEEP" -c sleep -- curl -sS -o /dev/null -w '%{http_code}\n' http://httpbin:8000/get

# Instant query (same shape as latencyP99Query in internal/controller/promql.go).
# Call the script with single quotes so destination_service="..." is not eaten by Make:
hack/query-prom.sh 'histogram_quantile(0.99, rate(istio_request_duration_milliseconds_bucket{destination_service="httpbin.default.svc.cluster.local"}[30s]))'
```

`hack/query-prom.sh` port-forwards `istio-system/prometheus` to localhost:19090
for one request and prints the JSON.

## Grafana dashboard

```bash
make grafana-install   # installs Istio's sample Grafana addon, imports the dashboard below
kubectl -n istio-system port-forward svc/grafana 3000:3000
# open http://localhost:3000/d/cascade-operator
```

The dashboard (`config/observability/grafana-dashboard.json`) visualizes the
operator's own metrics (`internal/controller/metrics.go` —
`cascade_signatures_detected_total`, `cascade_mitigation_patches_applied_total`,
`cascade_restorations_completed_total`, `cascade_restoration_regressions_total`)
plus controller-runtime's standard reconcile-health metrics. No new metrics —
this is a visualization layer only. Re-running `make grafana-install` re-imports
the dashboard, so edits to the JSON show up without a full reinstall.

The default `go run ./cmd/main.go` invocation leaves the metrics server
enabled but unreached by a mesh's Prometheus — there's nothing in-cluster
for it to scrape. `make deploy-operator` (`hack/deploy-operator.sh`)
deploys the operator in-cluster for real (cert-manager for the webhook's
TLS, RBAC bound so a mesh's Prometheus can read `/metrics`, a static
scrape job on that Prometheus) and is what this project's own dev cluster
now runs — confirmed live by triggering a real trip and querying
`cascade_signatures_detected_total` back out through Prometheus itself, not
just curling the operator directly. The operator also runs one Prometheus
client per mesh (`--prometheus-url-istio`/`--prometheus-url-linkerd`, or
their env-var equivalents), so a `CascadePolicy` on either mesh detects
against that mesh's own real proxy metrics, not a single shared client that
silently starves whichever mesh it doesn't point at (the earlier shape of
this gap, fixed and live-verified against both meshes at once).

## Retries / `response_flags`

```bash
kubectl apply -f config/dev/httpbin-retry-vs.yaml
kubectl exec "$SLEEP" -c sleep -- curl -sS -o /dev/null -w '%{http_code}\n' http://httpbin:8000/status/503
hack/query-prom.sh 'sum by (reporter, response_code, response_flags) (increase(istio_requests_total{destination_service="httpbin.default.svc.cluster.local"}[2m]))'
```

## Uninstall (optional)

```bash
kubectl delete -f https://raw.githubusercontent.com/istio/istio/1.31.0/samples/addons/prometheus.yaml --ignore-not-found
bin/istioctl uninstall -y --purge
kubectl delete namespace istio-system --ignore-not-found
kubectl label namespace default istio-injection-
```

Do **not** `kind delete` unless you want to throw away the whole cluster.
