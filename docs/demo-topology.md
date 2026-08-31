# Demo topology: checkout → {payments, inventory}

The §2.7-locked fan-out demo graph, under `demo/`. Three minimal Go
services — `checkout` calls both `payments` and `inventory` per inbound
request. This is the fan-out signature's evidence-gathering workload (see
`docs/worklog/2026-08-30-fanout-demo-evidence.md`), and it is not the
`sleep`/`httpbin` validation pair from the Kind+Istio dev-env slice (that
pair still exists, unrelated, still useful for the other two signatures'
PromQL history).

## Services

- `demo/payments`, `demo/inventory` — identical shape (`demo/internal/depsvc`):
  `GET /` answers 200 until told otherwise. `POST /control/fail` flips it to
  500; `POST /control/heal` flips it back. `GET /healthz` is the k8s probe.
- `demo/checkout` — `GET /checkout` calls `payments` then `inventory`.
  `payments` gets an **application-level** retry loop (up to 3 attempts on
  a non-2xx response) — deliberately separate from anything Envoy does
  (that's the retry-storm signature); `inventory` gets a single call, no
  retry, so it stays the fixed control an amplified `payments` count can be
  compared against.

Each is its own Go module (`demo/go.mod`) so it never touches the operator's
own `go.mod`/`go.sum` or its `make test`/`make lint`/`make generate` — build,
vet, test, and lint it from `demo/` directly:

```bash
cd demo && go build ./... && go vet ./... && go test ./... && gofmt -l .
../bin/golangci-lint run ./...   # picks up the repo-root .golangci.yml
```

## Deploy

Requires the Kind cluster + Istio already installed (`make istio-install`)
with sidecar injection enabled on `default`.

```bash
make demo-deploy      # builds all three images, kind-loads them, applies demo/k8s/
make demo-undeploy    # removes it
```

`hack/deploy-demo.sh` builds each image with `demo/` as the Docker build
context (`docker build -f demo/<svc>/Dockerfile demo/`), loads it into the
Kind cluster named `cascade-operator` (`kind get clusters`, not the
`kind-`-prefixed kubectl context name), then applies `demo/k8s/*.yaml` and
waits for all three rollouts.

Confirm sidecars:

```bash
kubectl get pods -l 'app in (checkout-service,payments-service,inventory-service)'
# READY 2/2
```

## Generate traffic and induce failure

Reuses the `sleep` pod (already sidecar-injected) as the client, same as the
Kind+Istio dev-env slice:

```bash
SLEEP=$(kubectl get pod -l app=sleep -o jsonpath='{.items[0].metadata.name}')
kubectl exec "$SLEEP" -c sleep -- curl -sS http://checkout-service.default.svc.cluster.local/checkout

# induce a downstream failure
kubectl exec "$SLEEP" -c sleep -- curl -sS http://payments-service.default.svc.cluster.local/control/fail
# ...traffic here amplifies payments' call count, see the worklog...
kubectl exec "$SLEEP" -c sleep -- curl -sS http://payments-service.default.svc.cluster.local/control/heal
```

## Query Prometheus

Same `hack/query-prom.sh` as the dev-env slice. Per-service request counts,
by response code, destination-reporter (i.e. what actually arrived at that
service):

```bash
hack/query-prom.sh 'sum by (response_code) (istio_requests_total{destination_service="payments-service.default.svc.cluster.local",reporter="destination"})'
```

See the worklog for the actual numbers and the resulting ratio.
