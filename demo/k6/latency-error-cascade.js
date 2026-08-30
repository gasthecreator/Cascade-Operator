// Latency/error cascade demo (PLAN.md checklist: "k6 cascade-simulation
// test scripts"). Drives steady load through checkout-service's /checkout
// endpoint, then flips payments-service into "slow" mode partway through
// the run (demo/internal/depsvc's new /control/slow toggle: every request
// sleeps slowLatency=800ms, comfortably above the CRD sample's
// latencyP99Ms=500 threshold, and 1 in 5 comes back 500, a fixed 20% error
// rate against errorRateFraction=0.05) — a p99 spike and an error-rate rise
// at the same time, exactly the AND DetectLatencyError requires (latency
// without errors is just a slow-but-healthy dependency; errors without
// latency is a fast fail, not a propagating stall — see
// internal/signatures/latency_error.go's own doc comment).
//
// Run alongside `kubectl get cascadepolicy -w` to watch status transition
// Normal -> Tripped -> Restoring -> Normal. See demo/k6/README.md for the
// full setup (port-forwards, running the operator, applying the demo
// topology + CascadePolicy).
import http from 'k6/http';
import { check } from 'k6';

// Runs inside the cluster by default (see hack/run-k6-demo.sh) — this
// script's own detector doesn't depend on checkout's destination-side
// metric the way fan-out's does, but staying consistent with the other two
// scripts' in-cluster execution avoids re-litigating the port-forward
// caveat documented in fanout-amplification.js.
const CHECKOUT_URL = __ENV.CHECKOUT_URL || 'http://checkout-service.default.svc.cluster.local/checkout';
const PAYMENTS_URL = __ENV.PAYMENTS_URL || 'http://payments-service.default.svc.cluster.local';

// Same timeline shape as the fan-out script: 20s healthy baseline, 60s
// induced (slow mode adds up to ~2.4s per checkout->payments call on a
// retried attempt, comfortably inside checkout's own 3s http.Client
// timeout so checkout itself never times out), then heal with ~90s left
// to watch the restoration ramp complete.
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
    induceSlow: {
      executor: 'shared-iterations',
      vus: 1,
      iterations: 1,
      startTime: '20s',
      exec: 'induceSlow',
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
  const res = http.get(CHECKOUT_URL, { timeout: '10s' });
  check(res, { 'checkout responded': (r) => r.status !== 0 });
}

export function induceSlow() {
  console.log(`[latency-error] inducing payments slowness: GET ${PAYMENTS_URL}/control/slow`);
  const res = http.get(`${PAYMENTS_URL}/control/slow`);
  check(res, { 'control/slow acknowledged': (r) => r.status === 200 });
}

export function heal() {
  console.log(`[latency-error] healing payments: GET ${PAYMENTS_URL}/control/heal`);
  const res = http.get(`${PAYMENTS_URL}/control/heal`);
  check(res, { 'control/heal acknowledged': (r) => r.status === 200 });
}
