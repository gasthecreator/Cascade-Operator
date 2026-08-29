# PROPOSALS.md — Architecture & Process Change Requests

## Protocol (Cursor and Claude both follow this — read before editing anything)

- **Never edit `PLAN.md` directly** to change an architecture decision, the
  CRD shape, the language/tooling choice, the mitigation strategy, or anything
  else in PLAN.md section 2 ("Architecture Decisions") or section 4
  ("Open Questions"). If, while building, you (Cursor) hit a reason to change
  or resolve one of those, add a new entry below under **Pending Proposals**
  instead of editing PLAN.md.
- Gideon brings this file to Claude for review in a separate session. Claude
  evaluates the proposal against PLAN.md and the project's goals, then does
  exactly one of:
  - **Approves** — updates PLAN.md itself, moves the entry to
    **Resolved Proposals** marked `APPROVED`, with a one-line note on what
    changed in PLAN.md and where.
  - **Rejects** — moves the entry to **Resolved Proposals** marked
    `REJECTED`, with reasoning, and PLAN.md stays as-is.
  - **Needs discussion** — leaves it in Pending, adds a `Claude's question:`
    line under it; Gideon relays the answer back to Cursor, which updates the
    same entry rather than opening a new one.
- This file is a proposal *queue*, not a history of what was built — that's
  `docs/worklog/`.
- Routine implementation choices that don't contradict or extend a decision
  already in PLAN.md (variable/function naming, which helper to extract,
  normal refactors within an already-decided approach) do **not** need a
  proposal. This file is for things that change what PLAN.md says, not how a
  decision already made in PLAN.md gets carried out.

## Template — copy this for a new proposal

```
### [PENDING] <short title>
**Proposed by:** Cursor
**Date:** YYYY-MM-DD
**Affects:** <PLAN.md section, e.g. "2.3 CRD shape" or "Open Question #2">

**Current state:** what PLAN.md says now (quote or summarize the relevant part).

**Proposed change:** what you want it to say instead.

**Why:** the concrete thing you ran into while building that makes the
current plan wrong, incomplete, or worth revisiting. Cite the actual
constraint, error, API limitation, or test result — not a general preference.

**Impact if approved:** what else in the codebase or plan this touches
(files, other open questions, checklist items).
```

---

## Pending Proposals

_(none — all five below resolved 2026-08-28)_

---

## Resolved Proposals

### [APPROVED] Lock CRD as CascadePolicy / cascade.gideonsanni.dev/v1alpha1
**Proposed by:** Cursor
**Date:** 2026-08-28
**Affects:** Open Question #1; 2.3 CRD shape

**Current state:** `CascadePolicy` under `cascade.gideonsanni.dev/v1alpha1` is a placeholder. PLAN.md says confirm before kubebuilder scaffolding because renaming after codegen means regenerating deepcopy and CRD YAML. The draft spec also has a single `targetVirtualService` on the policy, named after the *protected* service (e.g. `checkout-service`).

**Proposed change:**
- Lock kind `CascadePolicy`, group `cascade.gideonsanni.dev`, version `v1alpha1`.
- Namespaced (not cluster-scoped).
- Drop `spec.targetVirtualService`. The operator resolves Istio objects from each `dependsOn` host by the usual one-DestinationRule-per-host / one-VirtualService-per-host convention (object name = Kubernetes Service name, same namespace as the Service). If a referenced DestinationRule or VirtualService is missing, set a status condition and do not patch anything else for that edge.
- Keep `spec.service` + `spec.dependsOn` as FQDNs. Thresholds stay policy-wide for v1alpha1 (no per-edge overrides).
- Optional, still in this CRD lock: add `spec.mode: DetectOnly | Mitigate` (default `Mitigate`) so a demo can show detection without mutating mesh config.

**Why:** Open Question #1 is explicitly blocking for `kubebuilder init` / `create api`. `CascadePolicy` is the right kind: the CR is a desired constraint the controller enforces, not the breaker implementation. `CircuitBreakerPolicy` would collide with Istio's own vocabulary and muddy interview conversations. The draft's single `targetVirtualService: checkout-service` points at the *caller*, but the objects we actually patch are the *callee's* DestinationRule / VirtualService (outlier detection, retry budget, connection pool, timeout all hang off the destination host). Encoding that as a convention rather than a required field keeps the CRD honest and avoids a field we would immediately misuse. `DetectOnly` is the answer to "what if the patch is wrong during a demo."

**Impact if approved:** Unblocks repo scaffold and CRD types. Mitigation layer (checklist item "Istio patch layer") looks up objects per `dependsOn` instead of reading `targetVirtualService`. Status needs a condition type for missing Istio objects. If Gideon does not want `gideonsanni.dev` as the API-group domain string, substitute before codegen — same kind/version, different group.

**Resolved by Claude, 2026-08-28: APPROVED as written**, including the optional `spec.mode` field. `gideonsanni.dev` confirmed as the API-group domain — no real DNS ownership needed for a CRD group string, and it's already used consistently. PLAN.md §2.3 rewritten with the locked group/kind, dropped `targetVirtualService`, added `spec.mode` and the `DependencyObjectMissing` status condition. This is the correct call: patching the dependency's Istio objects rather than the protected service's is not a style preference, it's the difference between the operator working and silently patching the wrong service.

---

### [APPROVED] Custom 3-service demo topology, not Bookinfo
**Proposed by:** Cursor
**Date:** 2026-08-28
**Affects:** Open Question #2; 2.7 Local dev/test environment

**Current state:** "likely Istio's Bookinfo sample extended with a fault-injection sidecar endpoint, unless that proves too limited."

**Proposed change:** Do not use Bookinfo. Ship a minimal custom topology that matches the CRD example: `checkout` → `{payments, inventory}`, plus a tiny frontend/gateway if we need a single k6 entrypoint. Three small Go services, Dockerfiles, and Kubernetes+Istio manifests. Induce each signature with Istio fault injection and k6, not with extra sidecars. Defer building this until after one detect→mitigate loop exists; it is not part of the first scaffold slice.

**Why:** Bookinfo's graph (productpage → details/reviews → ratings) is built to demo Istio traffic splitting, not these three failure modes. Fan-out is not disproportionate (reviews→ratings is 1:1). Retry behavior is not a first-class, controllable client. Extending it with a custom fault-injection sidecar is the worst of both worlds: an implicit topology plus extra moving parts. A purpose-built graph is a cleaner interview story and makes "induce exactly this signature" a script, not a fight with sample-app defaults. Cost is bounded: ~50-line services, not a fourth product.

**Impact if approved:** Checklist item "Demo microservice topology" becomes a small in-repo app under something like `demo/`, not a Bookinfo overlay. k6 scripts target that graph. Kind+Istio docs install the custom manifests instead of `bookinfo.yaml`. No change to detection or mitigation code.

**Resolved by Claude, 2026-08-28: APPROVED as written.** PLAN.md §2.7 updated with the locked topology and the rejected-Bookinfo reasoning kept inline so a future reader doesn't re-litigate it. Sequencing note (build after one detect→mitigate loop) carried into §3's new build-order note rather than duplicated.

---

### [APPROVED] Per-signature Istio patch matrix; restore by loosening the same knobs
**Proposed by:** Cursor
**Date:** 2026-08-28
**Affects:** Open Question #3; 2.6 Mitigation

**Current state:** On trip, patch DestinationRule (outlier detection / connection pool) *or* VirtualService (retry budget, timeout). Restoration example is a traffic-weight ramp (10% → 25% → 50% → 100%) *or* loosened outlier-detection thresholds. No per-signature mapping.

**Proposed change:** One primary (and one optional secondary) patch per signature. Restoration always ramps the *same fields we tightened*, never a separate weighted-route shed.

| Signature | Trip (primary) | Trip (secondary, same slice or immediately after) | Restore |
|---|---|---|---|
| Latency/error cascade | DestinationRule `outlierDetection`: lower `consecutive5xxErrors`, shorter `interval`, longer `baseEjectionTime` | VirtualService `timeout` on the dependency host, capped at `thresholds.latencyP99Ms` | Stepwise loosen those same fields |
| Retry storm | VirtualService `retries.attempts` → 0 or 1 | DestinationRule `connectionPool.http.maxRetries` and `http1MaxPendingRequests` | Stepwise raise attempts / pool limits |
| Fan-out amplification | DestinationRule `connectionPool.http` (`http1MaxPendingRequests`, `http2MaxRequests`) on the *downstream* host — bulkhead in-flight calls | none for v1alpha1 | Stepwise raise pool limits |

Every patch still carries `cascade.gideonsanni.dev/managed-by: cascade-operator`. Do not use VirtualService destination weights for restoration in v1alpha1.

**Why:** Open Question #3 is marked as required before the mitigation layer. The three signatures are different amplifiers, so they need different knobs:

- A latency/error *cascade* is service-level. Outlier detection is instance-scoped (eject bad pods); it is the project's stated gap vs. hand-tuned Istio circuit breaking, so it is the primary for this signature. A VirtualService timeout is the fail-fast that stops remaining latency from propagating up the chain if every pod is sick (outlier detection ejects 100% and you still need a timeout).
- A retry storm is a *policy* problem. DestinationRule outlier detection does not stop Envoy from retrying. Cutting `retries.attempts` is the direct counter; the connection-pool retry/pending caps are the bulkhead.
- Fan-out amplification is a *concurrency* problem. Connection-pool max pending / HTTP2 max requests on the callee is bulkheading; timeouts and outlier ejection do not reduce call count.

Traffic-weight restoration is a different pattern (load shedding / canary). It needs a dummy destination or an abort route, a second state machine, and it is easy to clobber user routing. Loosening the knobs we already annotated is one state machine (`Normal → Tripped → Restoring(step N) → Normal`) and matches the existing `restoreStep: 0-4` status field.

**Impact if approved:** Unblocks the Istio patch layer and restoration state machine. CRD no longer needs a single `targetVirtualService` (see CRD proposal). Restoration code has one ramp implementation parameterized by field set, not a weight table. k6 / demo assertions check the specific fields above. If Claude wants v1alpha1 even thinner: implement only the **primary** column for the first mitigation slice (latency → outlier detection), keep the matrix in PLAN.md as the contract for the other two.

**Resolved by Claude, 2026-08-28: APPROVED, and taking the thinner v1alpha1 option** — first mitigation slice implements only the latency/error cascade primary (outlier detection); the rest of the matrix is contract, not yet built. PLAN.md §2.6 rewritten with the full matrix and the reasoning per signature. The restore-by-loosening-the-same-knobs call over a traffic-weight ramp is the right simplification — one state machine instead of two, and it removes the risk of the operator's restoration logic fighting user-authored VirtualService routing.

---

### [APPROVED] Close custom-metrics-API vs. polling: keep direct Prometheus HTTP
**Proposed by:** Cursor
**Date:** 2026-08-28
**Affects:** Open Question #4; 2.4 Metrics

**Current state:** Direct PromQL over the Prometheus HTTP API on each reconcile tick. Revisit only if reconcile-loop latency against Prometheus is a real bottleneck.

**Proposed change:** Close the question. Do not add a custom metrics adapter, Metrics APIService, or controller-runtime custom-metrics path. Operator-level config (flag/env) for Prometheus URL, not a per-policy field. Reconcile on CR watch events *and* `RequeueAfter` (implementation default: 10s) so polling happens without an extra timer controller.

**Why:** The custom-metrics API exists to feed HPA. Standing it up is another Deployment, an aggregation-layer APIService, and a security surface, for no interview-visible gain. PromQL (`histogram_quantile`, `rate(...[30s])`) *is* the talking point, and Prometheus already owns the 30-second window — detectors then take a snapshot struct, not a local ring buffer. Demo scale will not bottleneck on one HTTP query per policy per 10s. Closing it now prevents a mid-scaffold "should we adapter this" detour.

**Impact if approved:** Open Question #4 marked resolved in PLAN.md. Prometheus client in the first metrics slice is an HTTP client behind a narrow interface (`Query(ctx, PromQL) → snapshot`) so detectors stay dependency-free. No Kubernetes Metrics APIs in RBAC.

**Resolved by Claude, 2026-08-28: APPROVED as written.** This mostly confirmed the existing PLAN.md §2.4 decision; folded in the operator-level-config and watch+RequeueAfter(10s) detail. Noted directly in PLAN.md: the `response_flags=UR` retry-metric assumption needs validating against a real Istio scrape before the retry-storm detector is written — that's flagged as a Kind-cluster validation item, not something to guess at in the scaffold slice.

---

### [APPROVED] CI from commit one: lint + unit tests only (no Kind in Actions yet)
**Proposed by:** Cursor
**Date:** 2026-08-28
**Affects:** Open Question #5; checklist "golangci-lint + gofmt CI gate"

**Current state:** GitHub Actions for lint/test now (empty repo, cheap) vs. defer until there is code. PLAN.md leans toward now so gofmt/golangci-lint exist from commit one.

**Proposed change:** Add GitHub Actions on the first scaffold PR, running on PRs and on `main`:

1. `gofmt -l` must be empty
2. `golangci-lint` (version pinned)
3. `go test ./...`
4. `make manifests` / `make generate` drift check (generated CRD YAML and deepcopy match committed files)

Do **not** run Kind, Istio, or k6 in CI until the integration-test checklist item is actually being built. Pin Go (propose 1.24.x, matching current kubebuilder defaults unless `kubebuilder init` picks otherwise — follow the tool).

**Why:** The working agreement already requires gofmt and golangci-lint from the first commit; CI is how that is enforced after the laptop. Kind-in-CI is the expensive part (Istio install, image loads, flake). Putting that in an empty-repo workflow would slow every PR for no signal. A generate-drift check is worth having the moment CRD YAML is generated, because hand-edits to generated files are the usual kubebuilder footgun.

**Impact if approved:** First scaffold slice includes `.github/workflows/ci.yml`, `.golangci.yml`, and Makefile targets. Checklist items "Repo scaffold" and "golangci-lint + gofmt CI gate" complete together. Integration tests stay a later workflow file.

**Resolved by Claude, 2026-08-28: APPROVED as written.** PLAN.md gets a new §2.8 for this (it's a real architecture/process decision, same as the others, not just a checklist item). Go version: follow whatever `kubebuilder init` picks — don't pre-pin a version number that init might override.
