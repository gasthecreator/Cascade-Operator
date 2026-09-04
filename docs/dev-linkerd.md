# Kind + Linkerd local mesh (validation, not the demo topology)

Pin **Linkerd edge-26.6.3** (Linkerd has no numbered stable release series
the way Istio does, only rolling "edge" builds — this is the specific
build PLAN.md §5 Phase 6.6's own spike live-verified working, not a moving
target). Installs the control plane plus the `linkerd-viz` extension
(for its bundled Prometheus, the only piece of `linkerd-viz` this
project's `internal/mesh/linkerd.QueryBuilder` actually depends on). This
is **not** the `checkout → {payments, inventory}` demo topology — that's
`demo/k8s-linkerd/`, a separate step below. Both this mesh and Istio
(`docs/dev-istio.md`) can run on the same Kind cluster at once, in
separate namespaces, with no conflict — this project's own dev cluster
runs both simultaneously.

## Prerequisites

- Kind cluster in the current kube context (the scaffold smoke test left
  `kind-cascade-operator` running).
- `kubectl`, `curl`.

## Install

```bash
make linkerd-install
```

`hack/install-linkerd.sh` downloads the `linkerd` CLI into `bin/`
(gitignored, same pattern as `istioctl`/`controller-gen`), installs the
Gateway API CRDs (a Linkerd 2.16+ prerequisite), then the control plane
and `linkerd-viz`. It detects an already-installed control plane
(`linkerd-config` ConfigMap present) and uses `linkerd upgrade` instead of
`linkerd install` in that case — `linkerd install` refuses outright
otherwise, unlike `istioctl install`'s own reconcile-in-place behavior.
Override `LINKERD_VERSION=…`/`GATEWAY_API_VERSION=…` only if you know you
want a different pin.

Confirm the control plane and `linkerd-viz`:

```bash
kubectl -n linkerd get pods
kubectl -n linkerd-viz get pods
# READY 2/2 (or 1/1 for viz components with no injected proxy) on all of them
```

## Deploy the Linkerd copy of the demo topology

```bash
kubectl apply -f demo/k8s-linkerd/
kubectl -n linkerd-demo get pods -l 'app in (checkout-service,payments-service,inventory-service)'
# READY 2/2 once linkerd-proxy sidecars inject
```

`demo/k8s-linkerd/namespace.yaml` carries its own
`linkerd.io/inject: enabled` annotation (unlike Istio's namespace-label
injection, there's no separate labeling step). This reuses the exact same
`cascade-demo-{checkout,payments,inventory}:dev` images the Istio demo
topology does (`make demo-deploy` builds/loads them) — if you haven't run
that yet, do it first so the images exist on the Kind node.

## Generate traffic and query Prometheus

```bash
kubectl -n linkerd-demo run curl-client --image=curlimages/curl --restart=Never --rm -i --command -- \
  curl -sS -o /dev/null -w '%{http_code}\n' http://checkout-service.linkerd-demo.svc.cluster.local/checkout

# Same script as docs/dev-istio.md, pointed at linkerd-viz's Prometheus instead:
PROM_NAMESPACE=linkerd-viz PROM_SERVICE=prometheus hack/query-prom.sh \
  'histogram_quantile(0.99, sum by (le) (rate(response_latency_ms_bucket{authority="payments-service.linkerd-demo.svc.cluster.local",direction="outbound"}[30s])))'
```

Linkerd's own metric names/labels are genuinely different from Istio's —
confirmed live, not assumed, during PLAN.md §5 Phase 6.6's own spike (see
`docs/worklog/2026-08-31-phase6.6-linkerd-query-builder.md` for the full
accounting): `request_total`/`response_total` (not
`istio_requests_total`), `response_latency_ms_bucket` (not
`istio_request_duration_milliseconds_bucket`), `authority`/`direction`/
`classification` in place of `destination_service`/`reporter`/
`response_code`.

## Retry storm's fixture: `ServiceProfile`

Linkerd's retry-storm mitigation needs a pre-provisioned `ServiceProfile`
to patch (`internal/mesh/linkerd.Mitigator` only ever patches
`spec.retryBudget` on an existing one — same "operator patches fields,
never creates the object" convention as every Istio signature). The demo
topology's own fixture is `demo/k8s-linkerd/inventory-serviceprofile.yaml`,
applied as part of `kubectl apply -f demo/k8s-linkerd/` above. Inspect it
directly, or via Prometheus once a `CascadePolicy` has tripped retry storm
on it:

```bash
kubectl -n linkerd-demo get serviceprofile inventory-service.linkerd-demo.svc.cluster.local -o yaml
```

## A real TCP-layer disruption, for Tetragon corroboration

`demo/internal/depsvc`'s `/control/reset` (not Istio- or Linkerd-specific
— the same demo binary runs in both topologies) hijacks the connection
and force-closes it with `SO_LINGER 0`, producing a genuine kernel-level
TCP RST rather than an HTTP error:

```bash
kubectl -n linkerd-demo run curl-reset --image=curlimages/curl --restart=Never --rm -i --command -- sh -c '
  curl -s http://inventory-service.linkerd-demo.svc.cluster.local/control/reset
  curl -s -m 3 http://inventory-service.linkerd-demo.svc.cluster.local/ >/dev/null 2>&1
  curl -s http://inventory-service.linkerd-demo.svc.cluster.local/control/heal
'
```

There is no standalone Tetragon dev-environment doc yet — `hack/install-
tetragon.sh`'s own header comment and `docs/worklog/2026-09-01-phase11-*.md`
cover installing it and confirming it actually captures this as a real
`tcp_send_active_reset` event.

## Grafana / operator metrics

Same as `docs/dev-istio.md`: `make deploy-operator`
(`hack/deploy-operator.sh`) deploys the operator in-cluster for real
(cert-manager for the webhook's TLS, RBAC bound so a mesh's Prometheus can
read `/metrics`, a static scrape job on that Prometheus — the same
technique `hack/install-tetragon.sh` already uses for Tetragon's own
`/metrics`), and this dev cluster's `linkerd-viz` Prometheus does scrape it
now. The Linkerd-mesh demo `CascadePolicy` in this doc detects against real
`linkerd-viz` proxy metrics too: the operator runs a separate
`--prometheus-url-linkerd` client alongside `--prometheus-url-istio`, not
one shared client that would starve whichever mesh it doesn't point at.

Reaching `linkerd-viz`'s own Prometheus for real needs one more thing
beyond RBAC, confirmed live: `linkerd-viz` ships its Prometheus locked down
by a deny-by-default `AuthorizationPolicy` on its query port (only its own
`metrics-api` ServiceAccount is allowed in, authenticated via mTLS) — an
unmeshed caller gets a clean HTTP 403 regardless of RBAC or query
correctness. `hack/deploy-operator.sh` handles this by mesh-injecting the
operator itself (`linkerd.io/inject: enabled` on its namespace) and adding
a *second*, additive `AuthorizationPolicy` granting the operator's own
ServiceAccount identity access to the same `Server` — Linkerd's own policy
validator rejects more than one ServiceAccount per `AuthorizationPolicy`,
so this is a new policy alongside the existing `metrics-api` one, not an
edit to it. This cluster's `defaultInboundPolicy` is `all-unauthenticated`,
confirmed before doing this, so meshing the operator does not newly
restrict any of its *other* inbound traffic (istio-system's own, unmeshed
Prometheus scraping the operator's `/metrics` keeps working
unauthenticated).

## Uninstall (optional)

```bash
kubectl delete -f demo/k8s-linkerd/ --ignore-not-found
bin/linkerd viz uninstall | kubectl delete -f - --ignore-not-found
bin/linkerd uninstall | kubectl delete -f - --ignore-not-found
kubectl delete namespace linkerd linkerd-viz linkerd-demo --ignore-not-found
```

Do **not** `kind delete` unless you want to throw away the whole cluster
(including Istio, if it's also installed there).
