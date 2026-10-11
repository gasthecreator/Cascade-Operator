# Prometheus unreachable

## Symptom

Logs show `p99 latency query failed`, `error-rate query failed`,
`retry-storm ratio query failed` or `fan-out ratio query failed`, or
`incomplete metrics for dependency; skipping ...`. Worse, detection may be
off with **no error at all**: if no Prometheus URL is configured, the startup
log says `Prometheus URL not set; metrics polling disabled` and every policy
stays `Normal` forever.

## Impact

No new trips. Policies already `Tripped` or `Restoring` cannot advance their
restoration, because a restore step only proceeds on a reconcile where at
least one dependency produced complete readings (see
[stuck in Tripped](policy-stuck-in-tripped.md)). Existing patches remain in
place.

## Diagnose

```bash
# What is configured? (flags or the PROMETHEUS_URL* environment variables)
kubectl -n $NS logs deploy/$DEPLOY | grep -i 'prometheus'
kubectl -n $NS get deploy/$DEPLOY -o jsonpath='{.spec.template.spec.containers[0].args}{"\n"}{.spec.template.spec.containers[0].env}{"\n"}'
```

Expect one of `Prometheus metrics client configured`, `Istio-mesh Prometheus
metrics client configured`, `Linkerd-mesh Prometheus metrics client
configured`. Each mesh has its own client (`--prometheus-url-istio`,
`--prometheus-url-linkerd`; `--prometheus-url` is the shared fallback). A
policy on a mesh with no matching URL and no fallback is never evaluated,
silently.

Check that the URL resolves and Prometheus answers, using a throwaway pod in
a namespace that allows it (the manager image is distroless, so there is no
shell to exec into). This validates the URL and Prometheus itself, not the
operator's NetworkPolicy path, because the policy selects the manager pods:

```bash
kubectl run promcheck -n default --rm -it --restart=Never --image=curlimages/curl --command -- \
  curl -sS -m 5 "$URL/api/v1/query?query=up"
```

Typical causes:

- **Wrong or missing URL** for the policy's `spec.mesh`.
- **Egress NetworkPolicy** blocking the destination. The shipped policy
  (`config/network-policy-egress/restrict-egress.yaml`) allows DNS, the API
  server, each mesh's Prometheus and Linkerd's control plane only.
- **Linkerd-viz Prometheus returns 403**: it is protected by an
  `AuthorizationPolicy`; the operator pod must be mesh-injected and its
  ServiceAccount allowed (`hack/deploy-operator.sh` sets this up).
- **No traffic**: the query succeeds but returns an empty result (reported as
  incomplete readings). That is not an outage; see the stuck-phase runbooks.

## Mitigate

- Set the correct URL and restart:
  ```bash
  kubectl -n $NS set env deploy/$DEPLOY PROMETHEUS_URL_ISTIO=http://prometheus.istio-system:9090
  ```
- Fix the NetworkPolicy or authorization rule that blocks the path.
- If Prometheus will be down for a while and a policy is `Tripped`, decide
  whether to leave the patch in place (safe default) or remove it by hand
  ([instructions](mitigation-not-effective.md#removing-operator-patches-by-hand)).

## Escalate

If queries succeed from a debug pod but fail from the operator, capture the
operator log lines and the NetworkPolicy/AuthorizationPolicy output and open
an issue.

## Verify

```bash
kubectl -n $NS logs deploy/$DEPLOY --tail=50 | grep -E 'evaluation|incomplete metrics|query failed'
```

`latency/error cascade evaluation` lines, whose `evidence` field carries the
p99 and error-rate values, mean queries are succeeding and returning data.
