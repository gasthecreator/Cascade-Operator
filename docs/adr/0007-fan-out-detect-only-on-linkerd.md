---
status: accepted
date: 2026-08-31
decision-makers: "@gasthecreator"
---

# 0007. Fan-out amplification is detect-only on Linkerd

## Context and Problem Statement

On Istio, fan-out is mitigated with `DestinationRule` `connectionPool.http`
limits. Linkerd has no connection-pool or concurrency-limiting primitive.

## Decision Drivers

- Never claim a mitigation that is not enforced.
- Keep detection value on Linkerd even without a mitigation.

## Considered Options

- Detect and record the signature on Linkerd, but apply no patch
- Approximate with another Linkerd mechanism (retry budgets, failure accrual)
- Do not support the signature on Linkerd at all

## Decision Outcome

Chosen option: "detect-only on Linkerd". The signature is detected and shown
in status; the Linkerd Mitigator's fan-out trip is a deliberate no-op. It is
documented as a limitation, not a silent skip.

### Consequences

- Good: honest behavior; users still get the signal and the verdict.
- Bad: no automatic protection against fan-out on Linkerd.

### Confirmation

`TestLinkerdFanOutDetectOnlyNeverPatchesTheService` in
`test/integration/` asserts the Service is never patched.

## Pros and Cons of the Options

### Detect-only

- Good, because it is accurate about what is enforced.
- Bad, because it offers no mitigation.

### Approximate with another mechanism

- Good, because something would change on trip.
- Bad, because none of those mechanisms limit in-flight calls, so the
  mitigation would not address the signature.

### Not supported

- Good, because it is the simplest.
- Bad, because it discards a working detector.

## More Information

PLAN.md §5 Phase 6.6; `docs/history/worklog/2026-08-31-phase6.6-linkerd-mitigator.md`.
