# Policy stuck in Restoring

## Symptom

`status.phase` is `Restoring` and either `status.restoreStep` never reaches
completion, or it climbs and then snaps back to `0` and `Tripped` over and
over.

## Impact

Mesh objects sit at intermediate (partly loosened) values. Protection is
reduced from the fully tripped state but not removed. A flapping policy also
churns the mesh configuration on every cycle.

## How restoration works

One step per reconcile (default every 10 seconds): steps 0 to 4 loosen the
same fields that were tightened, interpolating toward the originals saved in
the `original-*` annotations; then a confirming reconcile restores the
exact originals, removes the operator's annotations and returns the policy to
`Normal`. Before each step the detectors are re-run. If the **same**
signature trips again during the ramp, the policy goes back to `Tripped` at
step 0 (a *regression*). If a **different** signature trips, the outgoing
signature's restore is force-completed first, then the new one is applied.

## Diagnose

```bash
kubectl get cascadepolicy <name> -n <ns> -w
kubectl -n $NS logs deploy/$DEPLOY --tail=300 | grep -E 'tripped|evaluation|restor'
```

PromQL on the operator's metrics (see the quick reference in the
[runbooks README](README.md)):

```promql
# Flapping: regressions climbing for a signature
increase(cascade_restoration_regressions_total[30m])
# Completed restores for comparison
increase(cascade_restorations_completed_total[30m])
```

1. **Regression loop.** Regressions rise while completions do not: the
   dependency is only healthy while it is protected. Loosening the mitigation
   re-exposes the problem, so the signature trips again. This is the design
   working; the dependency needs fixing, or the thresholds are too close to
   normal behavior (set `spec.thresholdOverrides` for that edge).
2. **Handoff churn.** `lastSignature` alternates between signatures (for
   example latency/error and fan-out on the same dependency).
3. **No complete readings mid-ramp.** Traffic stopped, so detectors cannot
   evaluate and the ramp does not advance. See
   [Prometheus unreachable](prometheus-unreachable.md).
4. **Failed writes.** A failed patch is retried next reconcile; persistent
   `forbidden`/`conflict` in the log means RBAC or another controller
   rewriting the same object (GitOps tools reverting the operator's patch).
   ```bash
   kubectl get destinationrule,virtualservice -n <dep-ns> <dep-name> -o yaml | grep -A3 'cascade.gideonsanni.dev'
   ```
5. **Operator restarted mid-ramp.** State is in the CR status and the
   annotations, so it resumes; verify the manager is leading.

## Mitigate

- Regression loop: fix the dependency or tune thresholds; do not force the
  restore while the dependency is unhealthy.
- A GitOps controller fighting the operator: exclude the annotated fields, or
  the `DestinationRule`/`VirtualService` for protected dependencies, from
  automated sync.
- To abandon the ramp and return the objects to their originals immediately,
  follow [removing operator patches by hand](mitigation-not-effective.md#removing-operator-patches-by-hand)
  and then reset the policy by deleting and re-creating it, or let the next
  healthy reconcile move it to `Normal`.

## Escalate

Open an issue with the policy YAML, 5 minutes of operator logs and the
`cascade_*` metric values.

## Verify

`status.phase` reaches `Normal`, `restoreStep` is `0`, and the dependency's
`DestinationRule`/`VirtualService` no longer carry
`cascade.gideonsanni.dev/managed-by`.
