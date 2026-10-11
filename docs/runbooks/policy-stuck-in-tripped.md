# Policy stuck in Tripped

## Symptom

`kubectl get cascadepolicy <name> -o jsonpath='{.status.phase}'` stays
`Tripped` long after the incident ended. `status.restoreStep` stays 0 and the
mesh objects keep their tripped values.

## Impact

The dependency stays protected with tightened circuit breaking (ejected
endpoints, shortened timeouts, retries cut to 0, or a capped connection
pool), which can itself degrade healthy traffic. Nothing is lost: the original
configuration is preserved in annotations on the patched objects.

## How a policy leaves Tripped

Each reconcile (every 10 seconds) evaluates every `dependsOn` host. If any
detector trips, the policy stays or becomes `Tripped`. If none trips **and at
least one dependency produced complete readings**, restoration starts
(`Restoring`, step 0). So a policy stays `Tripped` when either a signature is
genuinely still tripping, or no dependency is being evaluated at all.

## Diagnose

```bash
kubectl get cascadepolicy <name> -n <ns> -o yaml | sed -n '/^status:/,$p'
kubectl -n $NS logs deploy/$DEPLOY --tail=200 | grep -E 'evaluation|incomplete metrics|query failed|tripped'
```

Read the `evaluation` lines per dependency: `tripped=false` for all of them
means restoration should be starting; `tripped=true` shows which signature and
the `evidence` (values against thresholds) that keeps it tripped.

1. **Still genuinely tripping.** Compare `evidence` with the thresholds in
   `spec.thresholds` (and `spec.thresholdOverrides` for that host). If the
   dependency is really unhealthy, the policy is working as designed; fix the
   dependency. If thresholds are too tight for normal traffic, raise them.
2. **No complete readings** (`incomplete metrics for dependency; skipping`
   for every host): with no traffic, Prometheus returns an empty result and the
   detectors cannot evaluate. Restoration will not advance until traffic
   returns. Also covers [Prometheus being unreachable](prometheus-unreachable.md).
3. **Detection is off**: log line `Prometheus URL not set; metrics polling
   disabled`, or the policy's `spec.mesh` has no matching Prometheus client.
4. **Operator not leading or not running**: see
   [operator not reconciling](operator-not-reconciling.md).
5. **Mitigation errors**: a failed patch is returned and retried on the next
   reconcile; repeated `forbidden` or `conflict` errors in the log keep the
   policy cycling. Check RBAC as in the other runbook.
6. **Missing mesh object**: condition `DependencyObjectMissing=True` means a
   `dependsOn` host has no `DestinationRule`/`VirtualService` named after its
   Service in the Service's namespace, so there is nothing to restore for that
   edge.
   ```bash
   kubectl get cascadepolicy <name> -n <ns> -o jsonpath='{.status.conditions}'
   ```

## Mitigate

- Restore sooner than the incident clears: raise the threshold that is
  tripping, or set `spec.mode: DetectOnly` to stop further patching (existing
  patches still need restoring, and in `DetectOnly` the operator logs what it
  would do without writing).
- To remove the patches by hand when the operator cannot, follow
  [removing operator patches by hand](mitigation-not-effective.md#removing-operator-patches-by-hand).

## Escalate

If every dependency reports `tripped=false` with complete readings, the
operator is leading, and the phase still does not leave `Tripped` within a few
reconcile intervals, open an issue with the policy YAML, the `evaluation` log
lines and `kubectl get destinationrule,virtualservice -o yaml` for the
dependency.

## Verify

```bash
kubectl get cascadepolicy <name> -n <ns> -w          # Tripped -> Restoring -> Normal
```

`cascade_restorations_completed_total{signature="..."}` increases when the
restore completes.
