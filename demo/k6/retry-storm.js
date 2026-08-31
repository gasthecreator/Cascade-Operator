// Retry storm demo (PLAN.md checklist: "k6 cascade-simulation test
// scripts"). Drives steady load through checkout-service's /checkout
// endpoint, then fails inventory-service partway through the run. Unlike
// the fan-out script, the amplification here is not checkout's own
// application code — inventory has no app-level retry loop — it's Envoy's:
// demo/k8s/inventory-retry-vs.yaml gives inventory-service a real
// VirtualService retries.attempts=3 policy, so each of checkout's single
// logical calls to inventory turns into up to 3 real requests arriving at
// inventory while it is failing, producing the destination:source
// request-count ratio retryStormRatioQuery measures (internal/controller/
// promql.go). See demo/k8s/inventory-retry-vs.yaml's own comment for why
// this fixture lives on inventory and not payments (payments already
// carries the fan-out demo's own amplification signal; stacking an
// Envoy-level retry policy on top of it would compound the two).
//
// Run alongside `kubectl get cascadepolicy -w` to watch status transition.
// Mitigation is confirmed working as of 2026-08-30 (webhook rejection fix,
// zero-value patch path, maxRetries trip value 1) — see demo/k6/README.md
// and docs/worklog/2026-08-30-retry-storm-mitigation-webhook-fix.md,
// docs/worklog/2026-08-30-retry-storm-zero-value-patch.md,
// docs/worklog/2026-08-30-retry-storm-maxretries-one.md. Wire-format
// assertions also live in test/integration (make test-integration).
import http from 'k6/http';
import { check } from 'k6';

// Runs inside the cluster by default (see hack/run-k6-demo.sh); see
// fanout-amplification.js's comment for why in-cluster (not
// `kubectl port-forward`) is the right default for all three scripts.
const CHECKOUT_URL = __ENV.CHECKOUT_URL || 'http://checkout-service.default.svc.cluster.local/checkout';
const INVENTORY_URL = __ENV.INVENTORY_URL || 'http://inventory-service.default.svc.cluster.local';

// Same timeline shape as the other two scripts: 20s healthy baseline, 60s
// induced, then heal with ~90s left to watch the restoration ramp
// complete. Retry storm's primary patch is VirtualService retries.attempts
// (mitigation/retry_restore.go), same 5-step ramp shape as the other two
// signatures.
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
  const res = http.get(CHECKOUT_URL, { timeout: '10s' });
  check(res, { 'checkout responded': (r) => r.status !== 0 });
}

export function induceFailure() {
  console.log(`[retry-storm] inducing inventory failure: GET ${INVENTORY_URL}/control/fail`);
  const res = http.get(`${INVENTORY_URL}/control/fail`);
  check(res, { 'control/fail acknowledged': (r) => r.status === 200 });
}

export function heal() {
  console.log(`[retry-storm] healing inventory: GET ${INVENTORY_URL}/control/heal`);
  const res = http.get(`${INVENTORY_URL}/control/heal`);
  check(res, { 'control/heal acknowledged': (r) => r.status === 200 });
}
