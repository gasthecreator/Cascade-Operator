# k6 cascade-simulation scripts: load generation for all three signatures, live evidence, one real mitigation bug found

**Date:** 2026-08-30
**Author:** Cursor
**Type:** feature / test / fix (partial — see Follow-ups)

## What

Three k6 scripts under `demo/k6/` (`fanout-amplification.js`,
`latency-error-cascade.js`, `retry-storm.js`), each driving load through
`checkout-service` and toggling the right control endpoint at the right
point in its ~170s timeline to induce that signature's pattern, plus:

- A small "slow" mode added to `demo/internal/depsvc` (`/control/slow`,
  alongside the existing `/control/fail`/`/control/heal`) so `payments` or
  `inventory` can be told to add artificial latency (800ms/request) and an
  elevated error rate (1-in-5) — the mechanism `latency-error-cascade.js`
  induces with.
- `demo/k8s/inventory-retry-vs.yaml`: a committed `VirtualService` retry
  policy (`attempts: 3`, `retryOn: 5xx,reset,connect-failure,refused-stream`)
  on `inventory-service`, the retry-storm fixture.
- `demo/k8s/payments-destinationrule.yaml`: a minimal, field-empty
  `DestinationRule` on `payments-service` so the operator has an object to
  patch for the two signatures that trip there.
- `demo/k8s/cascadepolicy.yaml`: the `CascadePolicy` CR watching this
  topology, with `fanOutMultiplier: 2` (see Why) rather than the CRD
  sample's default `5`.
- `hack/demo-port-forward.sh`: port-forwards checkout/payments/inventory/
  Prometheus at once, for the operator and manual `curl` checks.
- `hack/run-k6-demo.sh`: runs a named script as an in-cluster `Job` (see
  Why) instead of from the host, with sidecar injection disabled on the k6
  pod and optional `CHECKOUT_URL`/`PAYMENTS_URL`/`INVENTORY_URL` env
  passthrough.
- `demo/k6/README.md` rewritten to describe the in-cluster-Job approach
  (the original draft, written before live-testing, assumed host-side
  `k6 run` against port-forwarded URLs — that turned out to be wrong, see
  Why) and to document the retry-storm mitigation gap found below.

Live evidence gathered against the Kind + Istio cluster for all three
signatures: `Normal → Tripped → Restoring → Normal` for fan-out
amplification and latency/error cascade (both confirmed complete, clean
end states); `Normal → Tripped` confirmed for retry storm, but its
mitigation patch itself fails against this fixture — see Follow-ups. No
detector/mitigation/restoration/CRD code was changed except the one
mitigation-adjacent fix described there (which was left unfixed, per scope,
and instead written up as a `PROPOSALS.md` entry).

## Why

PLAN.md's checklist has had "k6 cascade-simulation test scripts (latency
spike, retry storm, fan-out)" unchecked since the CRD was locked — it's the
one item that turns "three tested Go packages plus a demo topology" into
"watch the operator actually catch and heal a cascade against real load."
The fan-out demo topology and its 1:1:1 healthy / 3x-tripped evidence
already existed (`2026-08-30-fanout-demo-evidence.md`); this slice is the
first time load is generated *continuously* against it with an actual
`CascadePolicy` watching, rather than a handful of manual `curl`s.

`fanOutMultiplier: 2` instead of the CRD sample's `5`: the earlier evidence
slice measured checkout's own app-level retry loop producing exactly a 3x
ratio on a failing payments. `5` would never trip against that topology at
all — the demo's own default has to be tuned to the demo's own measured
behavior, not copied from the CRD sample's generic placeholder.

## How

### Script shape

All three scripts share one scenario shape (k6 `scenarios`, not multiple
files per phase): a `constant-arrival-rate` `loadCheckout` executor running
the whole ~170s, plus two `shared-iterations` (1 VU, 1 iteration)
executors — `induce*` at `startTime: 20s`, `heal` at `startTime: 80s` — that
each fire a single control-endpoint request. `loadCheckout` keeps running
through and after both, so the "confirm it registers" and "confirm
restoration" halves of the load → induce → confirm → heal → confirm shape
both have live traffic to detect against, not just the induce/heal calls
themselves.

### depsvc slow mode

`NewMux` (extracted from `Run` so `depsvc_test.go` can exercise the real
handler over `httptest` instead of a duplicate fake) gained a `slow
atomic.Bool` alongside the existing `failing atomic.Bool`, and a
`slowRequestCount atomic.Uint64` for the 1-in-5 error cadence. `/control/
heal` now clears both flags — a script that induced slowness and forgot to
heal it before the next script's `fail`-based induce would otherwise leave
stale latency in the mesh. Values chosen (`slowLatency = 800ms`,
`slowErrorEvery = 5`) clear the CRD sample's default thresholds
(`latencyP99Ms: 500`, `errorRateFraction: 0.05`) by a wide, unambiguous
margin — same "comfortably above the line, not exactly on it" principle the
existing `fanOutMultiplier`/`retryStormMultiplier` demo values already use.
Every request sleeps while slow, not a fraction of them, because
`histogram_quantile`'s p99 only crosses 500ms if the large majority of the
window's samples already clear it.

### The port-forward bug (again, and now understood precisely)

`fanout-amplification.js`'s first draft ran from the host against
`kubectl port-forward`'d URLs, exactly like the earlier fan-out evidence
slice had. It reproduced that slice's own `+Inf` ratio problem immediately
— `dependency_caller_ratio=+Inf` in the operator's logs, meaning
checkout's own inbound request count (the ratio's denominator, keyed on
`destination_service=<spec.service>, reporter=destination`) was reading
zero even while checkout was visibly responding to k6's `curl`s. Same root
cause as before: `kubectl port-forward` opens a tunnel directly into the
pod's network namespace, which is *inside* where Istio's `iptables`
interception would redirect inbound traffic to the sidecar — port-forward
traffic never passes through that redirect, so the sidecar's inbound
listener (and therefore its `destination`-reported telemetry) never sees
it. checkout's *outbound* calls to payments/inventory still go through its
own sidecar normally (those are real HTTP calls the checkout binary makes,
unaffected by how its own inbound traffic arrived), which is why the
numerator side of ratio-based signatures (fan-out, retry storm) was never
the problem — only ever the denominator (`spec.service`'s own inbound
count) or, for latency/error cascade, whichever side of *that* signature's
query is scoped to the caller-facing route.

Fix: run k6 itself as an in-cluster `Job` (`hack/run-k6-demo.sh`), so its
HTTP client is a normal in-mesh caller and checkout's sidecar sees and
reports the inbound request like it would from any other pod. The Job's
own pod explicitly opts out of sidecar injection
(`sidecar.istio.io/inject: "false"`) — not because being in the mesh would
break anything, but because an injected `istio-proxy` container never exits
on its own, and a `Job` only reaches `Complete` when every container in the
pod has exited; sidecar injection would make it hang forever in a running
state despite k6 itself finishing correctly.

### `hack/run-k6-demo.sh` mechanics

Ships the target script into the cluster as a `ConfigMap`
(`kubectl create configmap ... --from-file=<name>.js=<path>`, so the file's
actual content is what runs, not a copy that could drift), runs it as a
`Job` mounting that `ConfigMap`, polls for the pod to exist before
`kubectl wait --for=condition=Ready` (a plain `wait` immediately after
`apply` can race the pod's own creation and fail with "no matching
resources found" — the poll loop is the fix), then `logs -f`s the Job and
finally `wait --for=condition=Complete --timeout=300s` (k6's own scenarios
run ~170s; 300s gives real headroom before treating a hang as a hang, not a
slow finish). A `trap cleanup EXIT` removes the `Job`/`ConfigMap` whether
the run succeeds, fails, or is interrupted, and `cleanup` also runs *before*
creating anything, so a leftover `Job`/`ConfigMap` name collision from a
prior interrupted run doesn't block the next one.

### Rebuilt-image-but-stale-pod trap (real, cost real debugging time)

`latency-error-cascade.js`'s first live run showed `LatencyErrorCascade`
never tripping, with the operator logging `p99_ms=<small number>
error_rate=NaN ... incomplete readings` for `payments-service` throughout
the entire induced-slow window. Manually `curl`ing `payments-service`'s own
`/control/slow` through the port-forward returned `payments: ok` — the
*fallback* `/` handler's response, not `/control/slow`'s own `payments: now
slow` — meaning the running pod was still serving depsvc's code from
*before* this slice's slow-mode addition even existed. `make demo-deploy`
had rebuilt the image and `kind load docker-image`'d the new bytes into the
node's containerd, but the *running* `Deployment`'s pod was untouched:
`kubectl apply`'s own diff saw an unchanged Pod spec (same image string,
`cascade-demo-payments:dev` — a mutable tag, so the string itself never
changes across rebuilds) and correctly considered there to be nothing to
roll out. Kubernetes has no way to know the bytes behind an already-pulled
tag changed unless something asks it to reschedule. Fix:
`kubectl rollout restart deployment/payments-service
deployment/inventory-service deployment/checkout-service` after any
`demo-deploy` that changed Go code, confirmed by `curl`ing `/control/slow`
directly afterward and seeing the real response before trusting the next
k6 run. Documented explicitly in `demo/k6/README.md` so this doesn't cost
someone else the same debugging pass.

### Live evidence

**Fan-out amplification** (`hack/run-k6-demo.sh fanout-amplification`):
`kubectl get cascadepolicy checkout-service -w` showed
`Normal → Tripped → Restoring (×5) → Normal`, `LastSignature:
FanOutAmplification`, `dependency_caller_ratio≈4.4` against the
`fanOutMultiplier: 2` threshold. `payments-service`'s `DestinationRule` was
inspected post-restore: bare `spec.host`, no `connectionPool`, no
`cascade.gideonsanni.dev/*` annotations — full clean restore.

**Latency/error cascade** (`hack/run-k6-demo.sh latency-error-cascade`,
after the rebuild/rollout-restart fix above): same watch output,
`LastSignature: LatencyErrorCascade`, operator log showing
`p99_ms=995 (threshold 500) error_rate=1 (threshold 0.05) latency_spike=true
error_rise=true` at trip, `p99_ms=2188.75` on a later tick during the ramp
(the slow-mode 800ms sleep plus queuing under sustained load easily clears
the 500ms line). Post-restore `DestinationRule` inspection: same clean
bare-host result as fan-out.

**Retry storm** (`hack/run-k6-demo.sh retry-storm`): `RetryStorm` tripped
correctly — `dest_source_ratio≈4` against `retryStormMultiplier: 3`,
confidence rising to 1.0 — confirming the detector and the `Tripped`
transition both work against a real Envoy-level retry policy (not just the
fake-client unit tests). The mitigation patch itself does not work against
this fixture; see Follow-ups for the full finding, which is real and
material enough not to bury it in this section.

A brief, transient `FanOutAmplification` trip on whichever host *wasn't*
under test appeared at the tail end of both the latency/error-cascade and
retry-storm runs, right as k6's `load` scenario's VUs ramp down over its
last few seconds — this is the same warm-up/cool-down PromQL rate-window
artifact already documented in the fan-out evidence slice's worklog (sparse
samples at a load transition skew a 30s rate window's ratio for one or two
ticks), reproduced here at the *end* of load instead of the start. Both
occurrences self-corrected to `Normal` within one reconcile tick — one via
the full restoration ramp (a `DestinationRule` patch had actually landed
before the ratio dropped back below threshold), one via the
`snapToNormalNoRestore`-style "no managed object, nothing to restore"
fallback (the ratio dropped back below threshold before any patch call
happened). Confirmed harmless both times by re-inspecting the object
afterward.

## Testing

- `cd demo && go build ./... && go vet ./... && go test ./...` — clean,
  including the rewritten `depsvc_test.go` (`TestFailReturns500UntilHealed`,
  `TestSlowEveryFifthRequestErrorsTheRest200`,
  `TestHealResetsBothFailingAndSlow`, plus the pre-existing tests against
  the real `NewMux` handler now instead of a duplicated fake).
- `make lint` (root module, the project's pinned `golangci-lint` build with
  the `logcheck` plugin) — 0 issues. `gofmt -l` on everything touched —
  empty.
- All three scripts run live against a Kind + Istio 1.30.4 cluster with the
  operator running locally (`go run ./cmd/main.go --prometheus-url=...`)
  and `kubectl get cascadepolicy -w` capturing status transitions in real
  time, per PROPOSALS.md/the prompt's evidence bar — not just k6's own
  `checks_succeeded` summary (which was 100%, 98.12%, and 100% across the
  three runs respectively — the one dip during latency-error-cascade is
  consistent with a handful of checkout requests genuinely erroring while
  payments was slow/failing, not a script problem).

## Follow-ups / known gaps

- **Real bug, filed as a `PROPOSALS.md` entry, not fixed this slice (out of
  scope per the prompt):** `ApplyRetryStormTrip`
  (`internal/mitigation/retries.go`) sets `retries.attempts: 0` on trip
  while leaving a route's existing `retryOn`/`perTryTimeout` untouched by
  design (the doc comment explicitly calls this out as intentional — "if a
  route already had an explicit retries block, its retryOn/perTryTimeout/
  backoff are preserved and only attempts changes"). Istio's validating
  webhook rejects that exact combination: `configuration is invalid: http
  retry policy configured when attempts are set to 0 (disabled)`. Every
  reconcile against `inventory-service`'s fixture (`attempts: 3, retryOn:
  ..., perTryTimeout: 2s` — a realistic retry policy, not a contrived one)
  errored and retried for the whole ~17s the storm was active; the object
  was never actually patched. The retry-storm mitigation worklog itself
  (`2026-08-30-retry-storm-mitigation.md`) already flagged live verification
  as an open gap ("Docker's VM was OOM-killed while writing this slice...
  Worth a live check once the cluster is healthy again") — this is that
  live check, and it found a real problem, not a confirmation. See
  `PROPOSALS.md` for the open entry.
- `demo/k6/README.md`'s original draft (written before any live run)
  assumed host-side `k6 run` against port-forwarded URLs. Corrected in this
  slice once the same `+Inf`-ratio failure mode as the earlier fan-out
  evidence slice reproduced immediately — worth calling out explicitly here
  since it means the README as first written would have actively misled
  the next person to run it.
- Retry-storm's `VirtualService` secondary
  (`DestinationRule.connectionPool.http.maxRetries`, PLAN.md §2.6) is still
  unbuilt — unaffected by this slice either way.
