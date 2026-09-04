// Tetragon kernel-signal corroboration demo (PLAN.md §5 Phase 11's own
// stated follow-up, flagged in
// docs/worklog/2026-09-01-phase11-tcp-reset-fault-injection.md as "a
// reasonable next step" and left unbuilt until now). Drives the same
// steady load through checkout-service's /checkout endpoint as the other
// three scripts, then flips payments-service into "resetting" mode
// (demo/internal/depsvc's /control/reset — a genuine TCP RST via
// http.Hijacker + SO_LINGER 0, not an HTTP error response) partway through
// the run.
//
// Unlike the other three scripts, this one isn't demonstrating a signature
// detector on its own — internal/signatures.ApplyKernelCorroboration only
// ever *boosts* an already-Tripped verdict's confidence and evidence
// string, it can never trip a signature by itself (PLAN.md §5 Phase 11's
// own stated design, kernel_corroboration_test.go locks this in). What
// this script demonstrates is the *combination*: checkout's own calls to
// a resetting payments-service fail at the connection level (Envoy
// reports this as a real error, same as any other upstream failure), so
// the same latency/error-cascade path the latency-error-cascade.js script
// exercises should trip here too — but this time with a genuine kernel
// event (tcp_send_active_reset, captured by
// demo/tetragon/tcp-reset-policy.yaml's TracingPolicy) corroborating it,
// visible in the operator's own reconcile logs as
// `kernel_corroboration=true` with a real `kernel_events` count and
// confidence boosted toward 1.0 (see internal/signatures/kernel_corroboration.go).
//
// Needs Tetragon installed (`make tetragon-install`) to observe the
// corroboration itself — the k6 scenario and the underlying trip/mitigate/
// restore cycle work identically with Tetragon absent, just without the
// kernel_corroboration=true evidence line, matching this project's own
// "detection is unchanged with Tetragon absent" requirement.
//
// Run alongside `kubectl get cascadepolicy -w` and
// `kubectl -n kube-system logs -l app.kubernetes.io/name=tetragon -c
// export-stdout -f` to watch both the CascadePolicy status transition and
// the real kernel events landing at the same time. See demo/k6/README.md
// for the full setup.
import http from 'k6/http';
import { check } from 'k6';

const CHECKOUT_URL = __ENV.CHECKOUT_URL || 'http://checkout-service.default.svc.cluster.local/checkout';
const PAYMENTS_URL = __ENV.PAYMENTS_URL || 'http://payments-service.default.svc.cluster.local';

// Same timeline shape as the other three scripts: 20s healthy baseline,
// 60s induced, heal at 80s, ~90s more load to watch the restoration ramp
// complete.
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
    induceReset: {
      executor: 'shared-iterations',
      vus: 1,
      iterations: 1,
      startTime: '20s',
      exec: 'induceReset',
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
  // A reset upstream call can surface to checkout's own caller as a
  // non-2xx status, a connection error (status 0), or a slow response
  // (Envoy retrying/timing out before giving up) — unlike the other
  // scripts' loadCheckout, this one doesn't assert a specific shape,
  // since the whole point is a genuinely disruptive, not-fully-
  // predictable failure mode at the transport level.
  http.get(CHECKOUT_URL, { timeout: '10s' });
}

export function induceReset() {
  console.log(`[tetragon-reset] inducing payments TCP resets: GET ${PAYMENTS_URL}/control/reset`);
  const res = http.get(`${PAYMENTS_URL}/control/reset`);
  check(res, { 'control/reset acknowledged': (r) => r.status === 200 });
}

export function heal() {
  console.log(`[tetragon-reset] healing payments: GET ${PAYMENTS_URL}/control/heal`);
  const res = http.get(`${PAYMENTS_URL}/control/heal`);
  check(res, { 'control/heal acknowledged': (r) => r.status === 200 });
}
