// Fan-out amplification demo (PLAN.md checklist: "k6 cascade-simulation
// test scripts"). Drives steady load through checkout-service's /checkout
// endpoint, then fails payments-service partway through the run so
// checkout's own application-level retry loop (demo/checkout/main.go,
// paymentsMaxAttempts=3) turns each failed inbound request into up to 3
// real outbound calls to payments — the exact pattern the fan-out
// demo-evidence slice already proved produces a clean 3x
// dependency:caller ratio on a live scrape. No topology changes needed;
// this script only drives load and flips payments' existing /control/fail
// and /control/heal toggle.
//
// Run alongside `kubectl get cascadepolicy -w` to watch status transition
// Normal -> Tripped -> Restoring -> Normal. See demo/k6/README.md for the
// full setup (port-forwards, running the operator, applying the demo
// topology + CascadePolicy).
import http from 'k6/http';
import { check } from 'k6';

// Defaults assume this script runs *inside* the cluster (see
// hack/run-k6-demo.sh) — hitting checkout-service's ClusterIP DNS name
// directly, not via `kubectl port-forward`. That matters here specifically:
// fanOutRatioQuery's denominator is checkout-service's own reporter=
// "destination" metric, which is only emitted when a request actually
// arrives via the pod's normal network path and gets intercepted by its
// sidecar's inbound iptables rules. `kubectl port-forward` tunnels in a way
// that bypasses that interception entirely (confirmed empirically: with k6
// running on the host against a port-forwarded checkout-service, this
// query's denominator stayed permanently absent, evaluating to
// dependency_caller_ratio=+Inf every tick, which DetectFanOut correctly
// refuses to trip on since +Inf isn't finite — see PLAN.md's k6-scripts
// worklog for the full account). Override these if you have a different
// in-cluster access path; overriding to a host-side port-forward URL will
// reproduce that same +Inf problem for this script specifically.
const CHECKOUT_URL = __ENV.CHECKOUT_URL || 'http://checkout-service.default.svc.cluster.local/checkout';
const PAYMENTS_URL = __ENV.PAYMENTS_URL || 'http://payments-service.default.svc.cluster.local';

// Timeline: 20s healthy baseline, then payments fails for 60s (long enough
// to span several 30s detection windows and several 10s reconcile ticks),
// then heals with ~90s of continued load left to observe the 5-step
// restoration ramp (RestoreFinalStep=4, one step per reconcile tick) run
// all the way back to Normal.
export const options = {
  scenarios: {
    load: {
      executor: 'constant-arrival-rate',
      rate: 5,
      timeUnit: '1s',
      duration: '170s',
      preAllocatedVUs: 20,
      maxVUs: 40,
      exec: 'loadCheckout',
    },
    induceFailure: {
      executor: 'shared-iterations',
      vus: 1,
      iterations: 1,
      startTime: '20s',
      exec: 'induceFailure',
    },
    heal: {
      executor: 'shared-iterations',
      vus: 1,
      iterations: 1,
      startTime: '80s',
      exec: 'heal',
    },
  },
};

export function loadCheckout() {
  const res = http.get(CHECKOUT_URL);
  check(res, { 'checkout responded': (r) => r.status !== 0 });
}

export function induceFailure() {
  console.log(`[fanout] inducing payments failure: GET ${PAYMENTS_URL}/control/fail`);
  const res = http.get(`${PAYMENTS_URL}/control/fail`);
  check(res, { 'control/fail acknowledged': (r) => r.status === 200 });
}

export function heal() {
  console.log(`[fanout] healing payments: GET ${PAYMENTS_URL}/control/heal`);
  const res = http.get(`${PAYMENTS_URL}/control/heal`);
  check(res, { 'control/heal acknowledged': (r) => r.status === 200 });
}
