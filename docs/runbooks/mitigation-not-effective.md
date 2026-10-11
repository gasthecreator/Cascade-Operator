# Mitigation applied but not effective

## Symptom

The policy is `Tripped`, `cascade_mitigation_patches_applied_total`
incremented and the mesh object carries
`cascade.gideonsanni.dev/managed-by: cascade-operator`, but the cascade
continues: the failing dependency still receives load, retries still
amplify, or latency does not drop.

## Impact

The operator believes it is protecting the dependency but the data plane is
not enforcing the change. No automatic correction happens; this needs a
human.

## Diagnose

Work from the object, to the proxy, to the traffic.

1. **Was the right object patched?** The operator resolves objects by
   convention: object name equals the dependency's Service name, in the same
   namespace as that Service. A `DestinationRule`/`VirtualService` with a
   different name is never touched.
   ```bash
   kubectl get cascadepolicy <name> -n <ns> -o jsonpath='{.status.conditions}{"\n"}'   # DependencyObjectMissing?
   kubectl get destinationrule,virtualservice -A | grep <dependency>
   ```
2. **Is the patch what you expect?**
   ```bash
   kubectl get destinationrule <dep> -n <ns> -o yaml | grep -B2 -A12 'trafficPolicy\|cascade.gideonsanni.dev'
   kubectl get virtualservice  <dep> -n <ns> -o yaml | grep -B2 -A8  'retries\|timeout\|cascade.gideonsanni.dev'
   ```
   Latency/error: `outlierDetection` (consecutive 5xx, interval, base ejection)
   and a route `timeout`. Retry storm: `retries.attempts: 0` and
   `connectionPool.http.maxRetries: 1`. Fan-out: `connectionPool.http`
   limits.
3. **Did the proxies receive it? (Istio)**
   ```bash
   bin/istioctl proxy-status
   bin/istioctl proxy-config cluster <caller-pod> --fqdn <dep-fqdn> -o json | grep -A12 -i 'outlier\|circuitBreakers'
   ```
   Not synced means istiod has not pushed the change yet. Note that
   `maxRetries: 1` is deliberate: Istio's control plane cannot push an
   explicit `0` for that field to Envoy.
4. **Another object overrides it.** More than one `VirtualService` or
   `DestinationRule` for the same host, a `DestinationRule` subset policy that
   overrides the top-level one, or a GitOps controller reverting the patch
   (the `managed-by` annotation disappears shortly after the trip).
5. **Linkerd specifics.** The Linkerd mitigator sets failure-accrual
   annotations on the dependency's `Service` and a `retryBudget` on its
   `ServiceProfile`. Linkerd suppresses circuit breaking for any Service that
   has a `ServiceProfile`, so a dependency with a pre-provisioned
   `ServiceProfile` cannot receive the latency/error mitigation (the operator
   logs this). Fan-out is detect-only on Linkerd by design: the policy trips
   and reports, but nothing is patched ([ADR-0007](../adr/0007-fan-out-detect-only-on-linkerd.md)).
   ```bash
   kubectl get serviceprofile -n <ns>
   kubectl get svc <dep> -n <ns> -o jsonpath='{.metadata.annotations}'
   ```
6. **Mode.** `spec.mode: DetectOnly` records the signature and patches
   nothing. Confirm `kubectl get cascadepolicy <name> -o jsonpath='{.spec.mode}'`.
7. **The cause is not what the signature assumes.** The mitigations reduce
   retries, eject bad endpoints and bound concurrency. If the failure is, for
   example, a saturated downstream database, none of them will fix it; the
   protection still limits the blast radius.

## Mitigate

- Fix the naming/ownership mismatch (create or rename the mesh object to the
  Service's name) and let the next reconcile patch it.
- Exclude the patched fields from any GitOps sync.
- Tighten the policy's thresholds or add `spec.thresholdOverrides` for the
  edge so the trip is earlier.
- Apply an equivalent change by hand while you investigate. If the signature
  keeps tripping the operator will patch the object again, so set
  `spec.mode: DetectOnly` on the policy first if you need it to stay out of the way.

### Removing operator patches by hand

Use this to return an object to its pre-trip state when the operator cannot.
The originals are stored as JSON in annotations on the patched object:

```bash
kubectl get destinationrule <dep> -n <ns> -o jsonpath='{.metadata.annotations}' | tr ',' '\n' | grep original-
```

Read the `original-*` value, restore those fields in the object's spec,
then remove the operator's annotations so it no longer considers the object
managed:

```bash
kubectl annotate destinationrule <dep> -n <ns> \
  cascade.gideonsanni.dev/managed-by- \
  cascade.gideonsanni.dev/original-outlier-detection- \
  cascade.gideonsanni.dev/original-connection-pool- \
  cascade.gideonsanni.dev/original-retry-connection-pool-
kubectl annotate virtualservice <dep> -n <ns> \
  cascade.gideonsanni.dev/managed-by- \
  cascade.gideonsanni.dev/original-timeout- \
  cascade.gideonsanni.dev/original-retries-
```

Edit with care: an object whose `original-*` annotation is deleted before its
spec is restored loses the only record of the pre-trip configuration.

## Escalate

Open an issue with the policy, the patched objects' YAML, the
`istioctl proxy-config` output (or the Linkerd `Service`/`ServiceProfile`),
and the operator `evaluation` log lines.

## Verify

Re-run the traffic that produced the cascade (see `demo/k6/`) or watch the
dependency's request rate and error ratio in Prometheus; the proxy config
shows the new limits, and the signature stops tripping.
