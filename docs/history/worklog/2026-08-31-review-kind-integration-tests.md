# Review: Kind integration test suite — approved with one fix (broken doc links)

**Date:** 2026-08-31
**Author:** Claude
**Type:** docs, fix

## What
Reviewed and committed `test/integration/` (`TestRetryStormTripAndRestoreWireFormat`),
the `make test-integration` target, the `demo/k6/retry-storm.js` /
`demo/k6/README.md` doc updates, and the PLAN.md checklist flip. Found and
fixed one real defect along the way: three new links in
`demo/k6/README.md` pointed one directory level too shallow.

## Why
Same reviewer role as every slice. This one closes PLAN.md's last open
checklist item and is specifically supposed to prevent a repeat of the
last three slices' bug class (wire-format gaps invisible to typed-struct
reads), so it earned the same "run it myself against the live cluster,
don't just read the pasted output" treatment as everything before it.

## How
- `git status`/`git diff` confirmed scope: three new files under
  `test/integration/`, a Makefile addition (new `test-integration` target,
  `test` target updated to exclude it), the two `demo/k6/` doc files, one
  checklist-only line in PLAN.md, and the worklog/README index.
- Read all three new Go files in full:
  - `cluster.go`'s harness: builds a real `controller-runtime` client from
    the `kind-cascade-operator` kubeconfig context (overridable via
    `INTEGRATION_CONTEXT`, defaulted safely rather than pointed at
    whatever context happens to be current), reads objects back through
    `unstructured.Unstructured` rather than the typed `networkingv1`
    structs specifically so `omitempty`/Istio-translation gaps can't hide
    behind a typed read — exactly the discipline this whole bug thread
    has been demanding of every live check by hand until now.
  - `resetPaymentsObjects`'s delete-then-reapply of `payments-service`'s
    `DestinationRule`/`VirtualService` (rather than the patch-based field
    removal I'd been doing by hand) is a better fix for the "`kubectl
    apply` doesn't clear fields absent from the fixture" gotcha noted in
    two previous reviews — delete+reapply can't leave a stray field behind
    regardless of what got added since the last apply, where a targeted
    JSON-patch removal has to know in advance which field to strip.
  - `retry_storm_test.go`: asserts literal `"attempts":0` and
    `"maxRetries":1` against the raw JSON string, and separately asserts
    `"attempts":0` is *gone* and `"attempts":3` (the demo fixture's real
    value) is present after restore, plus no `trafficPolicy` at all on the
    `DestinationRule` — matches the exact pattern this session's manual
    checks have been doing for three slices, now automated.
  - `t.Cleanup` is registered immediately after `initCheck`, before
    `baselineSetup` or any assertions run — Go's testing semantics run
    registered cleanups even after `t.Fatalf`/`FailNow`, so failure-path
    cleanup is provably correct by placement, not just by the happy path
    I exercised.
- **Found a real defect while reading the doc diff, not just skimming it:**
  the three new worklog links in `demo/k6/README.md` used `../docs/worklog/...`.
  That file lives at `demo/k6/README.md`; one `../` only reaches `demo/`,
  not the repo root, so the links resolved to a nonexistent
  `demo/docs/worklog/...`. Confirmed by checking the filesystem directly
  (the target file only exists at `docs/worklog/...` from the repo root)
  and by checking every *other* worklog-pointing link already in that same
  file, all of which correctly use `../../docs/worklog/...`. Fixed all
  three to match.
- `go build ./...`, `gofmt -l .`, `go vet ./...`, and — since this
  introduces a second build tag — `go vet -tags=integration ./...` as well,
  to catch anything that only the integration tag would expose. All clean.
- `go clean -testcache && go test $(go list ./... | grep -v /e2e | grep -v /test/integration) -race -count=1 -cover` —
  full existing suite, confirms the new package is correctly excluded from
  plain `make test` (matches the Makefile diff's intent) and nothing else
  regressed: controller 79.3%, metrics 80.4%, mitigation 90.9%, signatures
  94.1%.
- `make lint` — 0 issues.
- **Ran `make test-integration` myself against the live dev cluster** —
  confirmed the cluster was on a clean baseline first (`kubectl get
  destinationrule -o custom-columns=...` showed both `payments-service`
  and `inventory-service` at `{host: ...}` only), then ran it. Passed in
  4.47s. The raw JSON logged by my own run matches Cursor's pasted output
  exactly — `"attempts":0` / `"maxRetries":1` on trip, full restore to
  `attempts:3`/`perTryTimeout:2s`/`retryOn:...` and no `trafficPolicy` at
  all afterward, with the six-tick restore ramp (`Restoring` steps 0–4,
  then `Normal`) visible in the test's own log output.
- **Confirmed cleanup actually leaves the cluster clean, not just that the
  test passed:** after the run, both DestinationRules are back to
  `{host: ...}` with no cascade annotations, the `VirtualService` is back
  to its retry fixture with no cascade annotations, and the
  `CascadePolicy`'s status is fully reset (`Phase: Normal`, no
  `lastSignature`, `restoreStep` cleared) — this is the check that
  actually matters for a test suite meant to be re-run repeatedly, not
  just "did the assertions pass this once."

## Verdict
**Approved and committed**, with the doc-link fix folded into the same
commit. This closes the last open PLAN.md checklist item and directly
delivers on the standing concern that's run through this whole
retry-storm bug thread: raw wire-format assertions against a real
apiserver and a real Istio-validated CRD, now automated and repeatable
instead of living only in worklog prose and my own manual `kubectl`
sessions.

## Files touched
- `demo/k6/README.md` — fixed three broken relative links
  (`../docs/worklog/...` → `../../docs/worklog/...`)
- `docs/worklog/2026-08-31-review-kind-integration-tests.md` — this file
- `docs/worklog/README.md` — index this entry

## Testing
See "How" above — full independent re-run against the live cluster,
including a post-run cleanliness check the pasted transcript alone
couldn't prove.

## Follow-ups / known gaps
- Only retry storm has integration coverage. Latency/error-cascade and
  fan-out amplification's own wire-format-sensitive fields (none currently
  known to have an `omitempty`-at-zero problem, but that was true of retry
  storm too until it wasn't) would benefit from the same treatment — not
  urgent, flagged as the worklog's own follow-up.
- CI still doesn't run this suite (Kind+Istio in Actions remains out of
  scope per PLAN.md §2.8's original CI decision) — it's a local-only
  developer check for now, same as every other live-cluster verification
  this session has done by hand up to this point, just automated.
