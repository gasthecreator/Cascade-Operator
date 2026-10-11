# Review: retry storm's zero-value patch fix — approved; Envoy-level check confirmed independently, new Istio-translation gap correctly left pending

**Date:** 2026-08-30
**Author:** Claude
**Type:** docs

## What
Reviewed and committed `fix/retry-storm-zero-value-patch`. Independently
verified build/vet/gofmt/lint, the full mitigation+controller test suite
with `-race`, the actual patch-construction logic in both files, and —
critically, since this whole slice exists because a prior live check wasn't
deep enough — re-ran the live verification myself down to Envoy's rendered
config, not just trusting the worklog's numbers.

## Why
Same reviewer role as every slice. Extra scrutiny here specifically because
the underlying bug slipped through this project's normal test suite and two
prior rounds of my own review; a review that only reads the report and
reruns `go test` would repeat exactly that failure mode.

## How
- `go build ./...`, `gofmt -l .`, `go vet ./...` — all clean.
- `go clean -testcache && go test ./internal/mitigation/... ./internal/controller/... -race -count=1 -cover` —
  pass, mitigation 90.9%, controller 79.3%.
- `make lint` — 0 issues.
- Read both patch-construction functions in full:
  - `RetryStormAttemptsJSONPatch` (`internal/mitigation/retries.go`) — RFC
    6902 JSON Patch, one `add` op per forwarding route at
    `/spec/http/N/retries`, correctly using the route's positional index
    even though redirect-only routes are skipped (no off-by-one). The
    `add` op replaces the retries object wholesale, which is also how
    `retryOn`/`perTryTimeout`/`backoff` get cleared for the webhook — same
    behavior as the typed mutate it's replacing. Annotations go out as a
    single `add` of the already-fetched-and-mutated in-memory map, so nothing
    pre-existing on the object is lost.
  - `RetryStormMaxRetriesMergePatch` (`internal/mitigation/retry_connpool.go`) —
    JSON Merge Patch (RFC 7396). Checked specifically for an annotation-wipe
    risk here, since this function builds its annotations map from scratch
    (only copying two known keys) rather than reusing the full fetched map
    the way the VS patch does: merge patch recursively merges nested
    objects key-by-key, so a patch's `metadata.annotations` only touches the
    keys it names and leaves any other annotation on the live object alone.
    Confirmed this is the correct choice, not an oversight — RFC 7396's
    object-merge semantics make this safe in a way a JSON Patch `replace`
    at the same path would not be.
  - Confirmed call-site ordering in `internal/controller/retry_mitigate.go`:
    `ApplyRetryStormTrip`/`ApplyRetryStormConnectionPoolTrip` mutate the
    in-memory object (setting the two annotation keys) *before* the patch
    functions read `vs.Annotations`/`dr.Annotations` to build their payload
    — the keys exist by the time the patch is constructed.
- Read the new tests in both `_test.go` files: they marshal the patch bytes
  and assert the explicit zero is present, and separately marshal the typed
  struct and assert the zero is *absent* — so a future proto-tag change
  that accidentally fixed `omitempty` upstream would fail the "still
  demonstrates the bug" assertion rather than silently passing. This is the
  right regression lock; a test that only checked the patch bytes would not
  catch the test itself going stale.
- **Live re-verification, done myself, not just read:**
  - Confirmed `inventory-service`'s `DestinationRule` was at its clean
    fixture baseline (`{host: ...}`, no `trafficPolicy`) before testing.
  - Applied the exact production merge-patch payload
    (`{"metadata":{"annotations":{...}},"spec":{"trafficPolicy":{"connectionPool":{"http":{"maxRetries":0}}}}}`)
    via `kubectl patch --type=merge` myself. Raw stored JSON:
    `spec.trafficPolicy.connectionPool.http` is `{"maxRetries": 0}` — key
    present, value 0. Matches the worklog's claim exactly.
  - Waited for istiod propagation, then read `checkout-service`'s Envoy
    admin `/config_dump` myself (`kubectl exec ... -c istio-proxy -- curl
    localhost:15000/config_dump`), parsed the `ClustersConfigDump`, and
    found the `outbound|80||inventory-service...` cluster's
    `circuit_breakers.thresholds[0].max_retries` is `4294967295` —
    independently reproducing the pending proposal's central claim, not
    just trusting the number in the worklog.
  - Cleanup: `kubectl apply -f demo/k8s/inventory-destinationrule.yaml`
    reported "unchanged" and did **not** actually clear the
    `trafficPolicy` field left over from testing — worth knowing for
    future cleanup in this project: this CRD has no server-side-apply /
    strategic-merge schema, so `kubectl apply`'s three-way merge against
    the `last-applied-configuration` annotation only patches fields that
    changed relative to what was last applied, and won't remove a field
    the fixture never declared in the first place. Had to
    `kubectl patch --type=json -p '[{"op":"remove","path":"/spec/trafficPolicy"}]'`
    to actually restore the clean baseline. Confirmed clean afterward.
    Removed the local scratch config-dump file.

## Verdict
**Approved and committed.** The wire-format fix is correct, narrowly
scoped, and tested at the right layer (patch bytes, not just the typed
struct). The new pending proposal (Istio's DestinationRule→CDS translation
treating an explicit `maxRetries: 0` as unset, rendering
`4294967295`/unlimited instead) is independently confirmed real — this is
not the same bug reappearing, it's Istio's own control-plane translation
layer, one level up from the JSON-marshal bug this slice fixed, and
correctly left as a separate, undecided `PROPOSALS.md` entry rather than
folded into or used to walk back this slice's fix. The primary
(`retries.attempts → 0`) is confirmed working end-to-end at Envoy
(`retry_policy: null`); only the secondary is affected by the new gap.

## Files touched
- `docs/worklog/2026-08-30-review-retry-storm-zero-value-patch.md` — this file
- `docs/worklog/README.md` — index this entry

## Testing
See "How" above — every claim in Cursor's own worklog was independently
re-derived, not assumed, including the Envoy-level check that is the entire
point of this slice.

## Follow-ups / known gaps
- The new pending proposal (Istio not translating `maxRetries: 0` at the
  xDS layer) needs a decision. Likely direction: research whether istiod's
  translator has an explicit "treat 0 as default" branch (this smells like
  the same class of proto3-zero-as-unset problem, just inside Istio's own
  Go code instead of this project's) before picking between "accept the
  secondary is a no-op for now" and "bump to a small nonzero value." Not
  resolving this in the same pass as the marshal fix — it deserves its own
  investigation given how much the *first* zero-value bug taught about not
  trusting a shallow check.
- Cursor's own flagged gap — restoration's final step could hit the same
  `omitempty` hole if a route's true original `attempts`/`maxRetries` was
  legitimately 0 — is real but low-priority (requires a user-authored
  policy that already had retries disabled) and correctly left out of
  scope for this slice.
- `kubectl apply` not reliably clearing fields on this CRD is a demo/dev
  workflow gotcha worth remembering for future manual cluster resets in
  this project, not a code defect.
