# Phase 11: eBPF/Tetragon corroboration — environment spike (real, but partial)

**Date:** 2026-08-31
**Author:** Claude (solo — Cursor unavailable)
**Type:** infra spike + real install, honestly-scoped (not full corroboration integration)

## What
Per PLAN.md §5 Phase 11's own stated discipline ("starts with a spike
confirming Tetragon actually runs cleanly in this exact dev environment,
same discipline as Phase 6's Linkerd spike"), this slice:

1. Checked the actual prerequisite (BTF support in Docker Desktop's Linux
   VM) on this specific machine, live — not assumed from documentation.
2. Installed Cilium's Tetragon for real via Helm and confirmed it loads
   its BPF sensors and captures real kernel events.
3. Applied a real `TracingPolicy` watching `tcp_retransmit_skb`
   (`demo/tetragon/tcp-retransmit-policy.yaml`) and confirmed its sensor
   loads without error.
4. **Found, and is stating plainly rather than working around**: this
   project's current demo topology has no fault-injection path that
   actually produces a TCP-layer disruption, so the corroborating signal
   the spike confirmed *can* work has not been exercised end-to-end
   against a real induced incident.

## Why
The plan's own text flags this as the highest-risk phase after Linkerd —
privileged DaemonSet, kernel/BPF compatibility inside the Kind/Docker
Desktop VM needed confirming before committing further. Rather than
writing detection-pipeline integration code against an assumption, the
spike was run for real first, exactly as the plan specifies.

## How / live verification (all of this is real, not simulated)
- **Environment check**: `docker version` → Docker Desktop 29.7.2;
  `docker run --rm --privileged debian:stable-slim uname -r` →
  `7.0.12-linuxkit` (arm64) — well past the documented BTF threshold
  (24.0.6 / kernel 6.4.16-linuxkit). Confirmed BTF is actually present:
  `ls /sys/kernel/btf/vmlinux` → a real 6.7MB file, inside a privileged
  container matching the Kind node's own kernel.
- **Install**: no Helm was present on this machine (`brew install helm`,
  a new local dev-tool dependency — matches this repo's existing
  precedent of documenting `brew install k6` as a demo prerequisite, not a
  project dependency shipped in the repo). `helm install tetragon
  cilium/tetragon -n kube-system` — no raw-manifest kubectl-apply install
  path exists upstream for Tetragon on Kubernetes (only Helm, or a Docker-
  Compose-only quickstart for non-k8s use, confirmed via Tetragon's own
  docs before committing to the Helm dependency).
- **Confirmed working, from the pod's own logs**: `BTF file: using
  metadata file metadata=/sys/kernel/btf/vmlinux`, `Loaded sensor
  successfully sensor=__base__` — and its `export-stdout` sidecar emitting
  real `process_exec` events for the demo topology's actual pods (envoy/
  pilot-agent in checkout-service, the k6 Job itself starting mid-
  benchmark-run) — this is genuine kernel-level tracing on real workloads,
  not a health-check-only "pod is Running" check.
- **Confirmed working, the actual TracingPolicy**: applied
  `cascade-tcp-retransmit` (kprobe on `tcp_retransmit_skb`, args: index 0
  type `sock` — mirroring `cilium/tetragon`'s own published
  `tcp-connect.yaml` example's shape rather than guessing arg types) —
  logs show `Added generic kprobe sensor: ... -> tcp_retransmit_skb`,
  `Loaded sensor successfully sensor=generic_kprobe`.
- **The honest gap**: read `demo/internal/depsvc/depsvc.go`'s `/control/
  fail` and `/control/slow` handlers directly — both only ever return
  HTTP 500 / add `time.Sleep`, entirely application-layer. Neither drops a
  connection, resets a socket, or does anything at the TCP layer. Checked
  live for `process_kprobe` events (the event type `tcp_retransmit_skb`
  would emit) across the whole benchmark run so far: zero. This is
  consistent with the fault injection genuinely never producing a real
  TCP-layer disruption on a local Docker Desktop bridged network, not a
  sensor malfunction — the sensor itself is confirmed loaded and correctly
  would fire on a real retransmit.

## Deliberately not done in this slice
- **No integration into `internal/signatures` or the reconciler.** The
  plan calls for Tetragon signals to be "surfaced in the evidence string /
  an optional confidence adjustment" — building that against zero real
  captured retransmit/reset events would mean designing and testing
  against fabricated data, which this project's whole session has
  explicitly avoided doing for every other signal. Wiring this in
  honestly needs either (a) a new TCP-layer fault-injection mechanism in
  the demo topology (e.g., Istio fault injection's connection-abort mode,
  or a `tc`/`iptables`-based packet-drop toggle), or (b) reconsidering
  which Tetragon signal actually corroborates HTTP-layer cascades in this
  topology — worth a follow-up design decision, not a default assumption.
- Namespace-scoping the TracingPolicy to the demo namespace specifically
  (Tetragon's arg-based selectors filter on destination address, not
  namespace/pod labels directly in the version installed here — getting a
  namespaced variant exactly right wasn't verified live in this pass, so
  the applied policy is cluster-wide).

## Files touched
- `demo/tetragon/tcp-retransmit-policy.yaml` (new)
- `hack/install-tetragon.sh` (new)
- `Makefile` — `tetragon-install` target

## Testing
- Live-verified only (matches this whole phase's own "starts with a
  spike" framing) — no Go code changed, nothing to unit test.
- `go build ./...`, `go vet ./...`, `gofmt -l .` — unaffected, clean
  (confirmed no accidental changes to Go sources in this slice).

## Follow-ups / known gaps
- The spike passed: this exact dev environment can run Tetragon cleanly.
  The corroboration *integration* (Phase 11's actual stated deliverable)
  is not done — it needs either new fault-injection capability in the
  demo topology or a different choice of Tetragon signal, decided
  deliberately rather than defaulted, before any detection-pipeline code
  is written against it.
