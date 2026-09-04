# k6 cascade-simulation scripts

Four scripts driving load through `checkout-service` and toggling the
right control endpoint at the right point to induce a pattern against the
§2.7 demo topology (`checkout -> {payments, inventory}`) — one per failure
signature, plus a fourth for the eBPF/Tetragon kernel-signal corroboration
path (PLAN.md §5 Phase 11). Each script's timeline is the same shape: 20s
healthy baseline -> induce at 20s -> 60s induced -> heal at 80s -> 90s more
load to watch the restoration ramp complete. Total run time per script is
~170s.

| Script | Host toggled | Mechanism |
|---|---|---|
| `fanout-amplification.js` | `payments-service` `/control/fail` | checkout's own app-level retry loop (3 real HTTP attempts per failed call) |
| `latency-error-cascade.js` | `payments-service` `/control/slow` | depsvc's slow mode: every request sleeps 800ms + 1-in-5 errors |
| `retry-storm.js` | `inventory-service` `/control/fail` | Envoy `VirtualService` `retries.attempts: 3` (`demo/k8s/inventory-retry-vs.yaml`) |
| `tetragon-reset.js` | `payments-service` `/control/reset` | A genuine TCP RST (`SO_LINGER 0` + `Close()`, not an HTTP error) — trips the same latency/error-cascade path as `latency-error-cascade.js`, but with Tetragon (if installed) corroborating a real `tcp_send_active_reset` kernel event into the verdict's confidence/evidence |

Why payments hosts two of these and inventory hosts the third: payments
already carries the fan-out signal (checkout's application-level retry
loop) as a fixed property of the topology, so the latency/error demo
reuses it via a purely application-level toggle (`/control/slow`, no new
Istio object) rather than introducing a second host. The retry-storm
fixture, by contrast, is a *permanent* `VirtualService` — putting it on
payments would compound with the fan-out signal (each of checkout's 3
app-level attempts could itself be retried up to 3 more times by Envoy),
corrupting the already-proven clean 3x fan-out ratio. inventory has no
app-level retry loop from checkout, so it's the clean host for a
permanent Envoy retry policy.

## Run k6 *inside* the cluster, not from the host

**Do not** run `k6 run demo/k6/*.js` from your host machine against
`kubectl port-forward`'d URLs. `port-forward` tunnels directly into the pod
network namespace and bypasses the Istio sidecar's inbound interception for
that connection, so `checkout-service`'s own sidecar never records the
inbound request — every PromQL query keyed on `destination_service` for
checkout comes back empty or `+Inf` for ratio-based signatures (fan-out,
retry storm), even though the demo services themselves respond correctly.
This was discovered live while building this slice (see the worklog) — the
fix is to run k6 as an in-cluster `Job` so its traffic goes through the
mesh like any other caller's would. `hack/run-k6-demo.sh` does exactly
that: it ships the script into the cluster as a `ConfigMap`, runs it as a
`Job` with sidecar injection explicitly disabled (an injected sidecar on
the k6 pod itself would hang the Job — it has nothing to terminate it),
streams the logs, and cleans up the `Job`/`ConfigMap` on exit.

```bash
hack/run-k6-demo.sh fanout-amplification
hack/run-k6-demo.sh latency-error-cascade
hack/run-k6-demo.sh retry-storm
hack/run-k6-demo.sh tetragon-reset
```

Port-forwarding is still useful for the *operator itself* (it needs to
reach Prometheus, and you'll want to `curl` the demo services directly to
sanity-check a control endpoint) — just not for k6's own load generation.

## Prerequisites

```bash
brew install k6                 # or see https://k6.io/docs/get-started/installation/
make istio-install               # Kind + Istio + Prometheus, if not already up
make demo-deploy                 # builds/loads/deploys checkout, payments, inventory,
                                  # and applies everything under demo/k8s/*.yaml
                                  # (CascadePolicy, inventory's retry VirtualService,
                                  # payments' baseline DestinationRule)
```

`demo-deploy` builds fresh images and loads them into the Kind node, but if
the `Deployment`s already exist with the same image tag (`:dev`), Kubernetes
won't restart the running pods on its own — `kubectl rollout restart
deployment/payments-service deployment/inventory-service
deployment/checkout-service` after `demo-deploy` if you've changed
`demo/` Go code and need the new binary actually running (this bit the
first live run of `latency-error-cascade.js` in this slice: the deployed
`payments` pod was still serving depsvc's fail/heal-only code from *before*
slow mode was added, so `/control/slow` matched depsvc's fallback `/`
handler instead of a slow-mode route and silently did nothing until the
image was rebuilt and the rollout was restarted).

The operator itself needs to be running against the cluster, watching the
policy — `make run` runs it from the host using your current kube context.
It also needs a `--prometheus-url`, since the mitigation/restore dispatch
only happens when `r.Metrics != nil` (see `cmd/main.go`).

## Running a scenario end to end

Three things need to be running at once: the operator, `kubectl get
cascadepolicy -w` (the evidence), and the in-cluster k6 job.

**Terminal 1 — port-forward Prometheus (and the demo services, for manual
`curl` sanity checks) for the operator and yourself:**

```bash
hack/demo-port-forward.sh
```

**Terminal 2 — the operator**, pointed at the port-forwarded Prometheus:

```bash
ENABLE_WEBHOOKS=false go run ./cmd/main.go --prometheus-url=http://127.0.0.1:19090
```

`ENABLE_WEBHOOKS=false` is required here since PLAN.md §5 Phase 3: this dev
cluster has no cert-manager (or any webhook TLS cert) installed, and the
manager only tries to load one if something registers a webhook handler on
it — skipping registration is sufficient, not just a workaround. Without
this, `go run` fails immediately with `open .../k8s-webhook-server/
serving-certs/tls.crt: no such file or directory`.

**Terminal 3 — watch the policy's status transitions**, the actual
evidence this slice exists to produce:

```bash
kubectl get cascadepolicy checkout-service -w
```

**Terminal 4 — run one script as an in-cluster Job:**

```bash
hack/run-k6-demo.sh fanout-amplification
# or: hack/run-k6-demo.sh latency-error-cascade
# or: hack/run-k6-demo.sh retry-storm
# or: hack/run-k6-demo.sh tetragon-reset
```

Expected `-w` output shape (`PHASE` column): `Normal` for the first ~20-40s
while load is healthy and the induced condition's PromQL window fills in,
then `Tripped` once a detector crosses threshold, then `Restoring` (with
`RESTORE STEP` counting 0 through 4, one step per ~10s reconcile tick)
starting a tick or two after the 80s heal call, then back to `Normal` once
the ramp completes — roughly 40-60s after healing, depending on how many
ticks land before the window fully rolls off the induced condition.

Run scripts one at a time, not concurrently — they all use the same
`CascadePolicy`/topology, and only one signature can be
`status.lastSignature` at once (a same-host handoff mid-ramp is handled
correctly, see PLAN.md §2.6, but isn't what these scripts are demonstrating).
Let each script's own heal-and-restore tail finish (`Phase: Normal` again)
before starting the next one.

A brief, unrelated `FanOutAmplification` blip on whichever host isn't the
one you're testing can appear right as a script's `load` scenario winds
down (k6's VUs ramping to zero over the last few seconds skews the
30-second rate window's caller:dependency ratio for one or two ticks). It
self-corrects within one reconcile tick back to `Normal` and is not a bug —
see the worklog for the live evidence.

## Retry storm: mitigation confirmed (2026-08-30)

`retry-storm.js` drives `RetryStorm` to `Tripped` and the mitigation patches
now apply end-to-end on this fixture. Three bugs were fixed in sequence:

1. **Webhook rejection** — trip must clear `retryOn`/`perTryTimeout`/`backoff`
   alongside `attempts: 0`. See
   [`docs/worklog/2026-08-30-retry-storm-mitigation-webhook-fix.md`](../../docs/worklog/2026-08-30-retry-storm-mitigation-webhook-fix.md).
2. **Zero-value serialization** — `attempts: 0` / `maxRetries: 0` must use
   patch-based writes so `omitempty` does not strip them from stored JSON.
   See
   [`docs/worklog/2026-08-30-retry-storm-zero-value-patch.md`](../../docs/worklog/2026-08-30-retry-storm-zero-value-patch.md).
3. **Istio Pilot translation** — explicit `maxRetries: 0` never reaches Envoy;
   trip value is now `1`. See
   [`docs/worklog/2026-08-30-retry-storm-maxretries-one.md`](../../docs/worklog/2026-08-30-retry-storm-maxretries-one.md).

Automated wire-format checks (raw apiserver JSON for `"attempts":0` and
`"maxRetries":1`, plus full restore) live in `test/integration/` — run
`make test-integration` against the dev Kind cluster (`kind-cascade-operator`
context). Organic k6 + operator runs remain the manual demo path; Prometheus
port-forward stability is the usual operational footgun (name it if trips
fail to fire while patches look correct in isolation).

## Tetragon reset: kernel corroboration via k6 (2026-09-04)

`tetragon-reset.js` (PLAN.md §5 Phase 11's own follow-up, unbuilt until
now — see
[`docs/worklog/2026-09-01-phase11-tcp-reset-fault-injection.md`](../../docs/worklog/2026-09-01-phase11-tcp-reset-fault-injection.md)'s
own "reasonable next step" note) drives the same load/induce/heal timeline
as the other three scripts, but induces via `/control/reset` — a genuine
TCP RST, not an HTTP error. With Tetragon installed (`make
tetragon-install`) and its `TracingPolicy` watching
`tcp_send_active_reset`, the same `LatencyErrorCascade` trip the other
payments-service scenario produces should show `kernel_corroboration=true`
in the operator's own reconcile logs, with confidence boosted toward 1.0 —
confirm with:

```bash
hack/run-k6-demo.sh tetragon-reset
# in another terminal, watch for the corroborated evidence line:
kubectl logs -n cascade-operator-system deploy/cascade-operator-controller-manager -f | grep kernel_corroboration
```

## Custom ports/URLs

Every script reads its URLs from environment variables with in-cluster
ClusterIP DNS defaults built in (`http://checkout-service.default.svc.
cluster.local/checkout`, etc.) — matching what `hack/run-k6-demo.sh`'s Job
pod resolves inside the cluster. Override only if you have a different
in-cluster naming scheme:

```bash
CHECKOUT_URL=http://checkout-service.default.svc.cluster.local/checkout \
PAYMENTS_URL=http://payments-service.default.svc.cluster.local \
  hack/run-k6-demo.sh fanout-amplification
```
