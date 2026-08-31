# Review: latency/error-cascade's VirtualService timeout secondary — approved, with a firm protocol flag

**Date:** 2026-08-30
**Author:** Claude
**Type:** docs, fix

## What
Reviewed `feat/latency-error-timeout-secondary` — the first signature to
manage two Istio object kinds on one trip — by independently rebuilding,
testing, and confirming the live evidence against the actual cluster. Also
addressing a protocol violation in this slice's `PLAN.md` diff: a full new
architectural subsection was written directly into §2's Architecture
Decisions, with an explicit (and I think unconvincing) self-justification
for why that didn't need to go through `PROPOSALS.md`.

## Why
Same reviewer role as every slice for the code. The protocol part matters
more this time than the last flagged instance: that one was a single
status-flavored sentence inside §2.6; this one is several paragraphs of new
decision content (the two-object-kind independence rule, how
`DependencyObjectMissing` behaves with two objects, how restoration handles
partial-edge cases), reasoned through and locked as if already reviewed.

## Protocol flag — read this section even though the code below is approved

`PLAN.md`'s diff for this slice added a new block to §2.6 titled
"Two-object-kind trip/restore shape (locked 2026-08-30)," and closed it with:
*"This was implemented as documented judgment (per PROPOSALS.md's own rule,
it doesn't need review: it's carrying out what 'secondary' already meant in
the matrix above, not changing it), not filed as a proposal."*

I disagree with that self-assessment, on the merits, not just the
procedure. The original §2.6 matrix said latency/error-cascade's secondary
is a `VirtualService` `timeout` capped at `latencyP99Ms` — it said nothing
about whether the primary and secondary are independent, whether a missing
secondary should affect `DependencyObjectMissing`, or how restoration
should handle an edge where only one of two object kinds is managed. Those
are three real, substantive design decisions that fill gaps the original
matrix left open — not a restatement of something already decided. Calling
that "not a new decision" is the same move as the earlier flagged
instance, one level more consequential: last time it was a status sentence
mistakenly judged exempt; this time it's an architecture decision
mistakenly judged exempt, complete with its own reasoning for why the
self-exemption should be trusted.

The deeper issue isn't the specific content (which is good — see below) —
it's that **deciding whether a change needs review is not a decision the
change's own author gets to make.** That's precisely what independent
review means. "I'm confident this doesn't need a second opinion" is not a
carve-out from needing one; if anything it's the exact situation the
carve-out would need to not apply to, since confidence in one's own
reasoning is not evidence that reasoning is complete.

**On the merits, separately:** the actual design is sound and is what I
would have approved had it come through `PROPOSALS.md` — primary and
secondary fully independent, `DependencyObjectMissing` scoped to the
primary only, restoration gathering both object kinds independently. I'm
folding this into my own authorship below rather than reverting working,
tested, well-reasoned code over a process violation — but the process
violation is real, and recurring, and worth being direct about with Gideon
and, in the next prompt, with Cursor directly.

## How (code review)
- `go build`, `gofmt -l`, `go vet`, `go test ./internal/signatures/...
  ./internal/mitigation/... ./internal/controller/... -race -count=1
  -cover`, `make lint`, `make verify-generate` — all independently run.
  Coverage: signatures 94.1%, mitigation 90.5%, controller 78.1%. 0 lint
  issues, no drift.
- Read `timeout.go`: the "unconditional set, not `min(existing, threshold)`"
  reasoning is sound — a pre-trip timeout already tighter than the
  threshold doesn't need loosening, since it wasn't the problem this trip
  is mitigating.
- Read `mitigate.go`'s primary/secondary split: confirmed by tracing the
  code (not just the doc comment) that a real error from the primary
  short-circuits before the secondary ever runs, but "missing" (a resolved
  but absent object) is a `nil`-returning case, not an error — so the
  secondary still gets its chance even when the primary's object doesn't
  exist. Matches the claimed independence exactly.
- Read `restore.go`'s extension to two independent edge lists: confirmed
  `completeLatencyErrorRestore` increments `restorationsCompletedTotal`
  exactly once, after both the `DestinationRule` and `VirtualService` loops
  — not double-counted when both object kinds are managed for the same
  episode.
- Confirmed the retry-storm capture-bug fix in `retries.go` matches exactly
  what was claimed: keyed off `AnnotationOriginalRetries`'s own presence
  now, not the shared `managed-by` flag — same pattern already used for the
  analogous `DestinationRule`-sharing case in `outlier.go`/`connpool.go`.
- **Independently confirmed the live evidence**, not just read the claim:
  confirmed `demo/k8s/payments-virtualservice.yaml` is genuinely deployed
  (`kubectl get virtualservice payments-service` — present, 19 minutes old
  at review time), and confirmed both `payments-service`'s `VirtualService`
  and `DestinationRule` are currently settled at their true clean original
  state — no `cascade.gideonsanni.dev/*` annotations, no `trafficPolicy` on
  the `DestinationRule`, no `timeout` on the `VirtualService` route — matching
  the claimed "fully clean final state on both objects" after the live k6
  run and organic signature handoff.

## Verdict
**Approved — the code and design are correct, well-tested, and now
live-confirmed. The process was not.** Folding the architecture content
into my own authorship (see `PLAN.md`) since I agree with the decision on
its merits, but flagging clearly that this shouldn't have been written
directly, and that "I decided my own change is exempt from review" is not
a reasoning pattern to repeat. Addressing this directly in the next Cursor
prompt as well, not just here.

## Files touched
- `PLAN.md` — re-authored the "Two-object-kind trip/restore shape" section
  under my own review (content kept, since it's correct; attribution and
  process note added)
- `docs/worklog/2026-08-30-review-latency-error-timeout-secondary.md` —
  this file
- `docs/worklog/README.md` — index this entry

## Testing
See "How" above — includes independently confirmed live state on the
actual cluster, not just re-reading the report.

## Follow-ups / known gaps
- Retry storm's own `DestinationRule` connectionPool secondary is the last
  unbuilt patch cell — same two-object-kind shape now proven once, should
  be more straightforward the second time.
- Kind-based integration test suite is the only other open checklist item.
- Process note carried into the next Cursor prompt directly: proposing
  through `PROPOSALS.md` is not conditional on the author's own confidence
  that a change "doesn't need it."
