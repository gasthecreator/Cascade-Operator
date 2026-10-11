# Phase 11 (slice 1): real TCP-layer fault injection, live-verified against Tetragon

**Date:** 2026-09-01
**Author:** Claude (solo — Cursor unavailable)
**Type:** feature (new demo fault-injection mode + Tetragon policy; no
reconciler/detection-pipeline wiring yet)

## What
- `demo/internal/depsvc`: a new `/control/reset` mode. Unlike `/control/fail`
  (writes a 500) and `/control/slow` (sleeps), this one never writes an
  HTTP response at all — it hijacks the underlying connection
  (`http.Hijacker`) and force-closes it with `SO_LINGER 0`
  (`net.TCPConn.SetLinger(0)` + `Close()`), which makes the kernel send a
  real RST segment instead of a clean FIN. `/control/heal` now also clears
  this mode.
- `demo/tetragon/tcp-reset-policy.yaml` (new): a `TracingPolicy` watching
  `tcp_send_active_reset` — the specific kernel function that runs when a
  local socket actively sends an RST, exactly what `SetLinger(0)` +
  `Close()` triggers.
- `demo/internal/depsvc/depsvc_test.go`: a new test confirming a real
  `net/http.Client` observes a connection error (not a status code) during
  reset mode, and that `heal` clears it.

## Why
PLAN.md §5 Phase 11's own spike (`docs/worklog/2026-08-31-phase11-tetragon-spike.md`)
found Tetragon genuinely works in this dev environment, but every existing
fault-injection mode in `demo/internal/depsvc` is HTTP-layer only (500s,
`time.Sleep`) — none of them ever produces a real TCP-layer disruption, so
the `tcp_retransmit_skb` signal that spike confirmed *can* fire had never
actually fired during an induced incident. That spike's own stated
options were "a new TCP-layer fault-injection mechanism... or a different
choice of Tetragon signal, decided deliberately." This slice does both,
together: a new fault-injection mode that produces a genuine kernel-level
disruption from plain Go application code (no `tc`/`netem`/`iptables`, no
extra privileges), paired with the actual kernel function it triggers —
not `tcp_retransmit_skb` (packet-loss retransmission, still a real, valid,
but separately-unexercised signal — its own policy is left in place
untouched) but `tcp_send_active_reset` (an actively-sent RST).

## How

### Choosing the kprobe target: verified live, not guessed
Confirmed `tcp_send_active_reset` is a real, global (kprobe-able) kernel
symbol on this exact dev kernel before writing any policy against it —
same method the original spike used for its own kprobe choice:
```
docker run --rm --privileged debian:stable-slim cat /proc/kallsyms | grep tcp_send_active_reset
ffff80008121d4c8 T tcp_send_active_reset
```
(`T` = global text symbol, kprobe-able.) `tcp_reset` also exists (fires on
the *receiving* side of an incoming RST) but `tcp_send_active_reset` is the
specific function for a locally-initiated abortive close, which is exactly
what `SetLinger(0)` produces — the more precise match, not a guess between
two similarly-named options.

### A real design question, answered rather than assumed: does a same-pod loopback RST still count as a genuine signal?
`/control/reset` hijacks the connection between the app container and
*its own* mesh sidecar (the only TCP connection the app process itself
ever sees — the sidecar terminates the real cross-pod connection
separately). This still produces a genuine kernel-level
`tcp_send_active_reset` event, in that pod's own network namespace,
regardless of whether the disruption propagates any further — Tetragon
observes kernel events per-node, not scoped to "only cross-pod traffic."
For Phase 11's actual stated goal (a kernel signal that fires during the
same window mesh-metric-based detection also fires, corroborating it —
not a claim that the RST reaches the original caller unmodified), that is
sufficient and doesn't require solving the harder problem of forcing a
raw RST all the way through a mesh sidecar to the original caller.

## Live verification (real cluster, real Tetragon, real captured events)
Rebuilt and reloaded `cascade-demo-payments:dev`/`cascade-demo-inventory:dev`
with the new code, restarted both demo namespaces' deployments, applied
`tcp-reset-policy.yaml` — confirmed the sensor loaded from the Tetragon
pod's own logs:
```
level=info msg="Added kprobe" return=false function=tcp_send_active_reset ...
level=info msg="Loaded sensor successfully" sensor=generic_kprobe
```
Then, against the live `linkerd-demo` namespace: called `/control/reset`
on `inventory-service`, sent two real requests to it, called
`/control/heal`. Read Tetragon's own `export-stdout` event log for the
same window and found real `process_kprobe` events for
`tcp_send_active_reset`, `binary=/inventory` (the demo app's own compiled
binary) — not simulated, not another pod's noise. A genuinely interesting,
unplanned finding from this same window: **well over a hundred** reset
events fired from two curl requests, not two — Linkerd's own proxy
evidently retries a broken upstream connection to the app rapidly, and
since `/control/reset` resets *every* new connection while active
(indiscriminately, not just the first), each retry attempt was itself
immediately reset, producing a real connection-retry storm entirely as a
side effect of this fault-injection choice. Not a bug — a real, live
system behaving exactly as its own retry/backoff logic dictates when an
upstream keeps refusing connections — and arguably makes the corroborating
signal *more* reliable in practice (many real events per incident, not a
single borderline one), so left as-is rather than "fixed" to fire exactly
once.

## Files touched
- `demo/internal/depsvc/depsvc.go` — `/control/reset`, `abortWithTCPReset`
- `demo/internal/depsvc/depsvc_test.go` — new test
- `demo/tetragon/tcp-reset-policy.yaml` (new)

## Testing
- `go build`/`go vet`/`gofmt -l .`/`go test ./... -race` — clean, in both
  the main module and the `demo/` module (its own `go.mod`).
- `golangci-lint run` against the `demo/` module directly (not covered by
  `make lint`, same as the rest of `demo/`) — 0 issues.
- **Live-verified** against the real dev cluster and a real Tetragon
  install: sensor load confirmed from Tetragon's own logs, and real
  `tcp_send_active_reset` `process_kprobe` events captured from the demo
  app's own binary during an actual induced incident — the exact gap the
  original spike left open, now closed.

## Follow-ups / known gaps
- **Not done in this slice, deliberately**: wiring this signal into
  `internal/signatures`/the reconciler as "surfaced in `Verdict.Evidence`
  / an optional confidence adjustment" (PLAN.md §5's own stated design).
  That needs a way to actually *query* Tetragon for recent events in a
  window (Tetragon's `export-stdout` is a log stream, not a queryable
  API/Prometheus metric the way this project's existing detectors poll
  Prometheus) — a real, separate design/build problem, not a small
  addition to this slice.
- The `k6`-driven benchmark/demo scripts don't yet call `/control/reset`
  — only manually exercised via `curl` in this slice's own verification.
  Wiring a `reset` scenario into `demo/k6/` (or a new script) is a
  reasonable next step once the detection-pipeline side exists to observe
  it end-to-end.
- Tested against the Linkerd demo namespace only (Tetragon is
  mesh-agnostic — a node-level kernel observer, not scoped to either
  mesh's own metrics — so this doesn't need separate confirmation against
  the Istio namespace too; the depsvc binary itself is identical in both).
