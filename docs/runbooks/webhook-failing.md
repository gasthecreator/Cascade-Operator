# Admission webhook failing or certificate expired

## Symptom

`kubectl apply` of a `CascadePolicy` fails with an error such as
`failed calling webhook "vcascadepolicy-v1alpha1.kb.io"`, `x509: certificate
has expired or is not yet valid`, `connection refused` or a timeout. Or the
webhook is reachable but rejects the object with a validation message
(self-dependency, duplicate `dependsOn` entries, malformed Service FQDN).

## Impact

The webhook is registered with `failurePolicy: Fail`, so while it is
unreachable or its certificate is invalid, **creating and updating
`CascadePolicy` objects is blocked cluster-wide**. Existing policies keep
being reconciled; only writes are affected. A validation rejection, by
contrast, is the webhook working correctly.

## Diagnose

```bash
# 1. Is it a rejection or an outage? Read the full error from kubectl.
kubectl apply -f policy.yaml

# 2. Are the manager pods (which serve the webhook) up?
kubectl -n $NS get pods -l control-plane=controller-manager
kubectl -n $NS get endpoints cascade-operator-webhook-service

# 3. Certificate state (issued by cert-manager)
kubectl -n $NS get certificate,issuer
kubectl -n $NS describe certificate cascade-operator-serving-cert | sed -n '/Status:/,$p'

# 4. Does the webhook configuration carry a valid CA bundle?
kubectl get validatingwebhookconfiguration cascade-operator-validating-webhook-configuration \
  -o jsonpath='{.metadata.annotations}{"\n"}{.webhooks[0].clientConfig.service}{"\n"}'
kubectl -n $NS logs deploy/$DEPLOY | grep -i 'cert\|webhook\|tls'
```

- **No endpoints**: the manager pods are not Ready; see
  [operator not reconciling](operator-not-reconciling.md).
- **Certificate not Ready, expired or `Issuing` forever**: cert-manager is not
  running or cannot reach the issuer. `kubectl -n cert-manager get pods` and
  `kubectl describe certificaterequest -n $NS`.
- **Certificate Ready but the API server still rejects it**: the CA injected
  into the `ValidatingWebhookConfiguration` is stale. cert-manager's
  cainjector keeps it in sync via the `cert-manager.io/inject-ca-from`
  annotation; confirm the annotation is present and cainjector is healthy.
- **Rotated certificate not picked up by the pod**: the manager watches the
  mounted certificate files (webhook cert watcher), so rotation needs no
  restart; if the log shows no reload, restarting the Deployment is safe.

## Mitigate

1. Fix the root cause above (restore cert-manager, re-issue the certificate):
   ```bash
   kubectl -n $NS delete secret <secret-named-in-the-Certificate>   # cert-manager re-issues
   kubectl -n $NS rollout restart deploy/$DEPLOY
   ```
2. **Break-glass** if policies must be changed while the webhook is down: this
   removes validation for the duration, so use it briefly and restore it.
   ```bash
   kubectl get validatingwebhookconfiguration cascade-operator-validating-webhook-configuration -o yaml > /tmp/vwc.yaml
   kubectl delete validatingwebhookconfiguration cascade-operator-validating-webhook-configuration
   # ... make the urgent change ...
   kubectl apply -f /tmp/vwc.yaml        # or: make deploy IMG=<image> to recreate it
   ```
   Objects created without validation can violate the rules the webhook
   enforces; re-validate them afterwards with `kubectl apply --dry-run=server`.

## Escalate

If the certificate is healthy, the pod is serving, and the API server still
reports TLS errors, open an issue with `kubectl describe certificate`, the
`ValidatingWebhookConfiguration` YAML and the manager log.

## Verify

```bash
kubectl apply --dry-run=server -f config/samples/cascade_v1alpha1_cascadepolicy.yaml
```

succeeds, and an intentionally invalid policy (a `dependsOn` that equals
`spec.service`) is rejected with the webhook's message.
