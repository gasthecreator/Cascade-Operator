# Operator not reconciling

## Symptom

`CascadePolicy` objects never leave their initial phase, `kubectl get
cascadepolicy` shows stale data, the manager pods are not Ready, or the
logs contain no `reconciling CascadePolicy` lines.

## Impact

No detection and no mitigation for any policy. Mesh objects the operator
already patched stay as they are: nothing restores them until it recovers.

## Diagnose

```bash
kubectl -n $NS get pods -l control-plane=controller-manager -o wide
kubectl -n $NS describe deploy/$DEPLOY | tail -30
kubectl -n $NS logs deploy/$DEPLOY --tail=100
```

1. **Pods not Running or crash-looping**: read the previous container's log
   (`kubectl -n $NS logs <pod> --previous`) and look for `Failed to start
   manager`, `Failed to create controller`, or `Invalid prometheus-url`.
2. **Not Ready**: probes are `GET :8081/healthz` and `/readyz`; `kubectl -n $NS
   describe pod <pod>` shows probe failures.
3. **Running but idle: leader election.** Two replicas run, one leads.
   ```bash
   kubectl -n $NS get lease 3417ff6e.gideonsanni.dev -o jsonpath='{.spec.holderIdentity}{"\n"}{.spec.renewTime}{"\n"}'
   kubectl -n $NS logs deploy/$DEPLOY | grep -i 'leaderelection\|leader'
   ```
   A `renewTime` that stops advancing while the holder pod is gone points at a
   stale lease; repeated `Error retrieving lease lock ... context deadline
   exceeded` points at API-server connectivity from the pod (check the egress
   NetworkPolicy and, on a meshed cluster, that port 443 is excluded from
   proxy interception: `config.linkerd.io/skip-outbound-ports: "443"`).
4. **Policies in a namespace are ignored.** If the operator runs with
   `--watch-namespaces` / `WATCH_NAMESPACES`, a `CascadePolicy` in any other
   namespace is silently invisible. Look for `Restricting CascadePolicy
   watches to specific namespaces` in the startup log and compare.
   ```bash
   kubectl -n $NS get deploy/$DEPLOY -o jsonpath='{.spec.template.spec.containers[0].env}'
   ```
5. **RBAC denied.** `forbidden` errors in the log mean the manager lacks a
   permission, commonly after switching to namespaced RBAC.
   ```bash
   SA=system:serviceaccount:$NS:cascade-operator-controller-manager
   kubectl auth can-i list cascadepolicies --as=$SA -n <policy-namespace>
   kubectl auth can-i patch destinationrules.networking.istio.io --as=$SA -n <dependency-namespace>
   ```

## Mitigate

- Crash loop from a bad flag or URL: fix the Deployment args or env, then
  `kubectl -n $NS rollout restart deploy/$DEPLOY`.
- Stale lease: deleting it forces a clean election; the next reconcile starts
  normally: `kubectl -n $NS delete lease 3417ff6e.gideonsanni.dev`.
- Wrong watch set: add the namespace to `WATCH_NAMESPACES` and re-run
  `NAMESPACES=a,b hack/switch-to-namespaced-rbac.sh` so the Roles match.
- If the operator must be taken out of the loop entirely, set the affected
  policies to `spec.mode: DetectOnly` or scale the Deployment to 0. Remove
  leftover patches as described in [mitigation not effective](mitigation-not-effective.md#removing-operator-patches-by-hand).

## Escalate

Open an issue with the pod logs (`--previous` included), `kubectl describe`
output and the operator image tag/digest. For a suspected security problem
follow [SECURITY.md](../../SECURITY.md) instead.

## Verify

```bash
kubectl -n $NS get pods -l control-plane=controller-manager          # all Ready
kubectl -n $NS logs deploy/$DEPLOY --tail=20 | grep 'reconciling CascadePolicy'
kubectl get cascadepolicy -A                                         # phases populated
```
