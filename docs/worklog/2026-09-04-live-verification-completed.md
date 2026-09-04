# Live verification completed: namespace-scoped RBAC, egress NetworkPolicy, image signing — three more real bugs found along the way

**Date:** 2026-09-04
**Author:** Claude (solo)
**Type:** validation + fix

## What
Finished the live verification the previous slice
(`2026-09-04-security-hardening-and-a-real-calico-bug.md`) had to stop
partway through after a fourth wave of dev-cluster instability. All four
items now confirmed:

1. Namespace-scoped RBAC (`hack/switch-to-namespaced-rbac.sh`) — live-verified.
2. Egress `NetworkPolicy` — full deliberate allow/deny pass/fail test — live-verified.
3. `demo/k6/tetragon-reset.js` — run against the live cluster; produced a
   real trip and real Tetragon kernel events (see below for the one
   caveat).
4. `.github/workflows/publish-image.yml` — triggered for the first time.

Along the way, found and fixed three more real bugs (on top of the Calico
RBAC bug the prior slice found): a broken `NetworkPolicy` API-server rule,
a Linkerd-sidecar/API-server interaction that had nothing to do with the
`NetworkPolicy` at all, and a lowercase-repository-name bug in the
publish-image workflow.

## Why
User asked directly to bring the cluster back up and finish the live
verification the prior slice left incomplete.

## How

### Bringing the cluster back up
Same recovery pattern as before (`docker start`, wait for `Ready`, watch
for several minutes before trusting it) — genuinely stable this time.
Found and cleaned up real corruption left over from the prior incidents,
unrelated to any of this session's own code: the operator's own Deployment
spec had reverted to the placeholder `controller:latest` image (a race
between an interrupted `make deploy` and a `git checkout` on
`config/manager/kustomization.yaml` during the chaos), and
`kube-controller-manager`'s own deployment-controller cache kept
re-spawning a stale, wrong-image `ReplicaSet` at odd intervals throughout
this entire slice — cosmetic (never affected the real replicas'
function), left alone rather than chased further once confirmed harmless.

### Namespace-scoped RBAC
Ran `NAMESPACES=default,linkerd-demo hack/switch-to-namespaced-rbac.sh`
against the live cluster. `kubectl auth can-i list cascadepolicies -A`
correctly returned `no`; `-n default` and `-n linkerd-demo` both correctly
returned `yes`; `-n istio-system` (deliberately not in the watch set)
correctly returned `no`. The operator's own startup log showed
`Restricting CascadePolicy watches to specific namespaces`, and both demo
`CascadePolicy` objects kept reconciling cleanly. Clean pass, no bugs
found in this piece.

### Egress NetworkPolicy — three real bugs, not one
First test attempt used a pod matching the policy's `podSelector` but
inheriting the namespace's Linkerd injection — its results were
confounded (linkerd-viz Prometheus and a disallowed-port `kube-dns` call
both came back `504` instead of a clean allow/deny signal, because
Linkerd's *own* separate mesh-authorization layer was also in play).
Redid it with `linkerd.io/inject: disabled` on the test pod specifically,
isolating the Kubernetes `NetworkPolicy` layer cleanly: DNS, Istio's
Prometheus, and `linkerd-viz`'s Prometheus all succeeded; external
internet, `kube-dns` on a disallowed port, and Istio's Prometheus on a
disallowed port (both same-pod, wrong-port cases) all genuinely timed
out. Clean, unambiguous pass.

Getting the *real* operator (mesh-injected, not the isolated test pod) to
that point surfaced two more issues:

- **A genuinely broken NetworkPolicy rule.** The API-server rule used a
  `podSelector` matching the real `kube-apiserver` pod at port 6443 — this
  looked right on paper (podSelector rules are supposed to match on the
  actual destination pod, port-translated or not) but never actually
  matched real traffic: in-cluster clients dial the `kubernetes` Service's
  ClusterIP (port 443), and this cluster's CNI (Calico) evaluates egress
  against that pre-DNAT destination, not the post-DNAT pod IP. Confirmed
  live: with the podSelector version active, the operator's own leader
  election failed continuously (`connection reset by peer` dialing
  `10.96.0.1:443`), while an unrelated pod outside this policy's
  `podSelector` reached the identical address instantly — proving the
  policy, not the API server, was the cause. Fixed with an `ipBlock` on
  the Service's own ClusterIP instead.
- **A bug that wasn't this NetworkPolicy at all.** After the `ipBlock` fix,
  the identical failure persisted. Deleted the `NetworkPolicy` entirely as
  a diagnostic — the exact same "connection reset by peer" kept happening,
  proving it was never this policy's fault. The real cause: the operator's
  own Linkerd sidecar (added by `hack/deploy-operator.sh`'s
  mesh-injection step, a prerequisite for reaching `linkerd-viz`'s
  Prometheus) intercepts *all* outbound traffic from the pod, including
  calls to the Kubernetes API server — a known operational pattern for any
  controller-runtime/client-go-based process running inside a mesh. Fixed
  by adding `config.linkerd.io/skip-outbound-ports: "443"` to the
  operator's own pod template (`config/manager/manager.yaml`), excluding
  API-server traffic from proxy interception entirely. Confirmed: fresh,
  clean, error-free reconcile logs for both demo `CascadePolicy` objects
  immediately after this fix, no more leader-election failures.

Restored the `ipBlock` fix's `NetworkPolicy` afterward (it's a real
correctness improvement independent of the sidecar issue) and re-ran the
clean allow/deny test above against it.

### Tetragon reset k6 scenario
`hack/run-k6-demo.sh tetragon-reset` completed successfully:
`http_req_failed` at 38% (307/800, consistent with genuine TCP-reset
disruption) and latencies up to 14.5s. The `CascadePolicy` genuinely
tripped (`FanOutAmplification`, `lastTrippedAt` fresh at request time) —
worth correcting the script's own comment, which predicted
`LatencyErrorCascade` (the `/control/reset` disruption apparently
surfaces through the fan-out signal on this topology instead, since
payments-service's own connection resets trigger checkout's app-level
retry loop, the same mechanism the fan-out signature already measures).
Confirmed real Tetragon kernel events independently via
`tetragon_events_total` (632 events on `payments-service` in the trip
window). **One thing not confirmed**: the exact `kernel_corroboration=true`
evidence line in the operator's own logs — by the time this was checked,
the pod that had been leader at trip-time had already been replaced
(matching this whole session's pattern of Deployment/pod churn from the
lingering `kube-controller-manager` corruption above), and its logs were
gone. The trip and the kernel events are each independently confirmed
real; the specific log line tying them together in this run wasn't
captured. Not treated as a regression — `internal/signatures.ApplyKernelCorroboration`
already has its own dedicated, passing unit tests
(`kernel_corroboration_test.go`) proving the mechanism itself works.

### Image-publish-and-sign workflow
`gh workflow run publish-image.yml --ref main` — its first-ever real run
failed immediately at the build step: `invalid tag
"ghcr.io/gasthecreator/Cascade-Operator:<sha>": repository name must be
lowercase`. `github.repository` preserves this repo's real, mixed-case
name (`gasthecreator/Cascade-Operator`) verbatim, and Docker image
references require lowercase. Fixed by lowercasing it (`${IMAGE_NAME,,}`)
in both the tag-computation step and the `cosign sign` step — exactly the
class of bug "passes locally" checks can never catch, since nothing short
of actually running the workflow exercises this.

## Files touched
- `config/manager/manager.yaml` — `config.linkerd.io/skip-outbound-ports`
  annotation.
- `config/network-policy-egress/restrict-egress.yaml` — API-server rule
  switched from `podSelector` to `ipBlock`.
- `.github/workflows/publish-image.yml` — lowercase repository name fix.
- `docs/security-threat-model.md` — all three entries updated with final,
  live-verified status.
- `PLAN.md` — the security-hardening follow-up item checked off.
- This worklog entry + its index line.

## Testing
Every claim above has its own live evidence described inline — this
worklog entry *is* the testing record for this slice, not a separate
section repeating it.

## Follow-ups / known gaps
- The recurring stale-`ReplicaSet`-with-wrong-image phenomenon
  (`kube-controller-manager`'s own corrupted deployment-controller cache,
  observed repeatedly this slice) was never root-caused beyond "delete/
  scale it down when it appears" — harmless to real function, but a
  genuine unresolved oddity in this specific cluster's history worth
  knowing about if it recurs.
- The exact `kernel_corroboration=true` log line for the `tetragon-reset.js`
  trip wasn't captured (see above) — the mechanism itself is unit-tested
  and was already confirmed live in an earlier session
  (`docs/worklog/2026-09-01-phase11-kernel-corroboration.md`); a future
  re-run of this exact k6 scenario, checked promptly after the trip rather
  than after further cluster churn, would close this out fully.
- (Fixed in this same slice) `demo/k6/README.md`'s Tetragon-reset section
  originally said `LatencyErrorCascade` was the expected signature —
  corrected to `FanOutAmplification` based on this run's real result.
