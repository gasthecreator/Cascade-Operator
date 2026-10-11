---
status: accepted
date: 2026-08-28
decision-makers: "@gasthecreator"
---

# 0001. Build the operator in Go with kubebuilder and controller-runtime

## Context and Problem Statement

The project needs a Kubernetes controller that watches a custom resource,
queries Prometheus, and patches service-mesh objects. Language and scaffold
choices fix the codegen, informer-cache, leader-election, and webhook story
for the life of the project.

## Decision Drivers

- First-class CRD and deepcopy codegen, informer caching, leader election and
  admission webhooks, without hand-rolling them.
- A large body of reference operators to pattern-match against.
- The mesh APIs being patched (Istio's `networking.istio.io` types) are
  themselves Go libraries.
- Keep review conversations on detection and reconciliation design, not on
  tooling choices.

## Considered Options

- Go with kubebuilder and controller-runtime
- Rust with kube-rs

## Decision Outcome

Chosen option: "Go with kubebuilder and controller-runtime", because it is the
de facto standard for operators (cert-manager, Argo CD and Istio itself are
Go) and the Istio client types are native Go.

### Consequences

- Good: generated CRD YAML, RBAC and deepcopy code; `envtest` for tests
  against a real API server; Istio and Linkerd types importable directly.
- Bad: generated files must never be hand-edited, so CI needs a drift gate;
  kubebuilder scaffold markers must be preserved.

### Confirmation

`make verify-generate` fails CI when `config/crd/bases/`, `config/rbac/role.yaml`
or `zz_generated.*.go` drift from what `make manifests generate` produces
(PLAN.md §2.8).

## Pros and Cons of the Options

### Go with kubebuilder and controller-runtime

- Good, because codegen, caching, leader election and webhooks are
  battle-tested and built in.
- Good, because Istio's client types are Go.
- Bad, because it ties the project to controller-runtime's upgrade cadence.

### Rust with kube-rs

- Good, because it is capable and gives stronger compile-time guarantees.
- Bad, because its pattern library for this exact problem shape is much
  smaller, and the Istio types would need separate generation.

## More Information

PLAN.md §2.1 (language) and §2.2 (scaffold).
