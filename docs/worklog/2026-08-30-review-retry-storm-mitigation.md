# Review: retry-storm mitigation (VirtualService patch, not yet wired live)

**Date:** 2026-08-30
**Author:** Claude
**Type:** docs, fix

## What
Reviewed `feat/retry-storm-mitigation` against PLAN.md §2.6 and the
stuck-mitigation risk flagged in the prompt. Verified independently
(build/vet/gofmt/tests/lint, no drift) and fixed a real, if minor, comment
bug: a doc comment above `applyRetryStormMitigation` read as a garbled,
out-of-order sentence.

## Why
Same reviewer role as every slice. The cluster was genuinely unreachable
during Cursor's work (confirmed on this machine too — Istio pods showing
recent restarts, sleep/httpbin stuck in `Init:1/2`), which is worth noting
since it's why this slice's verification leaned on vendored source instead
of live traffic.

## How
- Confirmed the cluster's degraded state myself (`kubectl get pods
  -n istio-system` showed restart counts consistent with the OOM account;
  `sleep`/`httpbin` stuck in `Init:1/2`). This doesn't block a code review —
  `make test`/`lint` use envtest and fakes, not the live Kind cluster — but
  it's real corroboration, not just taking the claim on faith.
- `go build`, `gofmt -l`, `go vet`, `go test ./internal/mitigation/...
  ./internal/controller/... -cover`, `make lint`, `make verify-generate` —
  all independently run. Coverage matched exactly: mitigation 84.9%,
  controller 82.9%. 0 lint issues. The only "drift" in `verify-generate` is
  the same uncommitted-branch-vs-HEAD artifact as every prior slice
  (expected `virtualservices` RBAC line).
- Diffed `PLAN.md`: status line and checklist paragraph only, no
  architecture edits.
- Read `internal/mitigation/retries.go` closely: `ApplyRetryStormTrip`
  correctly skips routes with no destination (Redirect/DirectResponse/
  Delegate — retries are meaningless there), preserves `RetryOn`/
  `PerTryTimeout`/`Backoff` on routes that already had a `retries` block,
  and captures a per-route original snapshot (`Skipped`/`Unset`/full value)
  as a JSON array so the shape survives a multi-route `VirtualService`.
  First-patch-only annotation capture, same non-overwrite rule as the
  outlier-detection patch.
- **Verified the "must patch every route, not just ones with an explicit
  retries block" reasoning is genuinely evidenced, not assumed**: the
  worklog documents that the live cluster was unreachable (Docker Desktop's
  VM OOM-killed, confirmed via the VM console log timestamps), so Cursor
  fell back to the vendored `istio.io/api` proto doc comment (exact version
  pin match) plus Istio's own current reference docs, both stating the same
  implicit-default retry policy (`attempts: 2`,
  `retryOn: connect-failure,refused-stream,unavailable,cancelled`). This
  matches my own recollection of Istio's documented behavior, and — more
  importantly — the worklog is honest that this hasn't been confirmed by
  actually inducing traffic on this specific cluster, and flags exactly what
  a later live check should confirm (whether `retryOn`'s list actually
  covers what a real retry storm looks like, since a plain 5xx response
  isn't obviously covered by that default list). Good calibration: strong
  enough evidence to build on, honestly flagged as not fully closed.
- **Traced the stuck-mitigation decision through the actual code**, not just
  the worklog's account: confirmed `cascadepolicy_controller.go`'s dispatch
  still only calls `applyLatencyErrorMitigation` for
  `SignatureLatencyErrorCascade` — no corresponding call exists anywhere for
  `SignatureRetryStorm`. Also ran (mentally and by reading)
  `TestReconcileDoesNotWireRetryStormMitigationYet`, which asserts a full
  `Reconcile` call on a retry-storm trip leaves an existing `VirtualService`
  completely untouched — this is a real regression test locking in the
  choice, not just a comment promising it.
- Agreed with rejecting option (b) (teaching `beginRestore` to recognize an
  unrestorable managed object): the reasoning that a half-built
  "recognize-but-can't-restore" branch is easy to leave unfinished once real
  restore logic lands next to it is sound, and choosing to land the
  `Reconcile` wiring and `VirtualService`-aware restoration together removes
  the risk window entirely rather than narrowing it.
- **Found one real, if minor, defect**: the doc comment directly above
  `applyRetryStormMitigation` had a sentence split across two comment groups
  in the wrong order, reading as "Reconcile yet; it will vary once the next
  slice does, same as applyLatencyErrorMitigation's identically-shaped
  parameter." followed by "//nolint:unparam // host is constant only because
  nothing calls this from" — clearly one sentence, printed back-to-front.
  Traced *why*: gofmt canonicalizes a `//nolint:` directive to be the single
  comment line immediately touching the declaration (required for
  golangci-lint to associate it correctly), so a sentence spanning the
  paragraph-before-the-directive and the directive-comment-itself gets
  reordered by `gofmt -w` into something that reads backwards. Fixed by
  folding the explanation into the main doc paragraph and making the
  `//nolint` comment a short, self-contained line — verified `gofmt -l`
  clean and the prose now reads in order.

## Verdict
**Approved, with one comment fix.** The mitigation design, multi-route
handling, annotation shape, and — most importantly — the stuck-mitigation
handling are all correct and well-reasoned. The evidence-gathering under a
genuinely broken cluster (vendored source instead of live traffic) was
handled honestly rather than papered over.

## Files touched
- `internal/controller/retry_mitigate.go` — fixed the garbled doc comment
- `docs/worklog/2026-08-30-review-retry-storm-mitigation.md` — this file
- `docs/worklog/README.md` — index this entry

## Testing
See "How" above.

## Follow-ups / known gaps
- Next slice (Cursor's own, already scoped in their worklog): wire
  `applyRetryStormMitigation` into `Reconcile`'s tripped branch **and**
  extend `listManagedEdges`/`beginRestore`/`advanceRestore` to recognize a
  managed `VirtualService`, landing together — not split, per the reasoning
  already agreed with above.
- Once the Kind cluster is healthy again: confirm live whether a route with
  no explicit `retries` actually retries on a plain 5xx (not just
  connect-failure/refused-stream/unavailable/cancelled) — the worklog
  already flags this as the thing worth checking, not assuming either way.
- `DestinationRule` `connectionPool` secondary for retry storm, and the
  fan-out detector, both still unbuilt.
