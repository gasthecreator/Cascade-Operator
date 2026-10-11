# Restoration ramp for the latency/error-cascade outlierDetection patch

**Date:** 2026-08-29
**Author:** Cursor
**Type:** feature

## What
When a tripped latency/error cascade goes quiet, the reconciler now ramps
the same three `DestinationRule` `outlierDetection` fields back toward their
pre-trip values across `status.restoreStep` 0–4, then returns to `Normal`.
A regression at any restore step re-enters `Tripped` at step 0 with full
trip values. No VirtualService timeout, no new CRD fields.

## Why
PLAN.md §2.6: restoration loosens what was tightened, with a metrics gate
between each step — not a traffic-weight canary. The trip path already
stores the original on the DestinationRule; this slice is the unwind, and
it has to leave the object looking like the operator never touched it so a
later trip can snapshot a fresh original.

## How
- **Which object to restore:** no new status field. When phase is `Tripped`
  or `Restoring`, each `dependsOn` FQDN is resolved the same way as the trip
  slice; whichever DestinationRule carries
  `cascade.gideonsanni.dev/managed-by: cascade-operator` is under management.
  Survives a `dependsOn` reorder, needs no CRD change, reuses the annotation
  the last slice already writes.
- **Detector reuse:** the same `DetectLatencyError` scan as trip. Restore
  only advances when at least one dependsOn produced a complete reading *and*
  none tripped. A Prometheus outage (`evaluated == 0`) is not "healthy" — we
  stay in `Tripped`/`Restoring` rather than loosening on missing data. That
  is not a second "is it safe" function; it is refusing to treat a skipped
  edge as a negative.
- **State machine:** `Tripped` + healthy → `Restoring` step 0 (first
  loosening). Each later healthy tick increments the step. At step 4 the
  stored original is applied (or `outlierDetection` is cleared if the
  annotation is `{"unset":true}`). One more healthy tick at step 4 completes:
  both annotations are removed, phase `Normal`, `restoreStep=0`. A trip
  from `Restoring` reuses the existing `phase != Tripped` LastTrippedAt bump
  and sets `restoreStep=0`.
- **Interpolation:** `t = (step+1)/5`, so step 0 is 20% of the way from trip
  values toward the original and step 4 is 100%. Linear lerp on durations
  (nanosecond resolution, so interval moves every step: 5s → 6s → … → 10s
  when the target is Istio's default). `consecutive5xxErrors` is rounded to
  nearest uint32 — the trip/default gap is only 2, so that field can plateau
  across adjacent steps; interval is the smooth signal. When a field (or the
  whole block) was unset, Istio defaults (5 / 10s / 30s) are the *target*
  for steps 0–3 only. Step 4 with `{"unset":true}` still clears the block
  rather than writing those defaults — otherwise we would invent a traffic
  policy the user never had. Other TrafficPolicy settings (TLS, pool, …)
  are left alone; if clearing OD leaves TrafficPolicy empty, that pointer
  is niled too so the object matches pre-operator.
- **Annotation cleanup on complete:** leaving `managed-by` in place would
  make the next trip skip re-snapshotting (the first-patch-only rule keys
  off it). Restoration therefore deletes both annotations. Agreed with the
  prompt; not a PROPOSALS.md item.
- **Mode:** DetectOnly still advances status so a demo can show the ramp;
  the DestinationRule is not Updated.

## Files touched
- `internal/mitigation/restore.go` — lerp, apply step, complete + strip annotations
- `internal/mitigation/restore_test.go` — monotonic ramp, complete with values, unset clears OD
- `internal/controller/restore.go` — find managed DRs, begin/advance/complete
- `internal/controller/cascadepolicy_controller.go` — state machine; `evaluated` count
- `internal/controller/restore_test.go` — fake-client ramp, complete, regression, query-error
- `internal/controller/istio_patch_test.go` — shared `patchReconcileWith`
- `PLAN.md` — status line + checklist
- `docs/worklog/README.md` — index this entry

## Testing
- `go test ./internal/mitigation/` — pass, 82.6% (lerp + complete paths; duration-parse
  fallbacks and a few TrafficPolicy-empty branches unhit).
- `go test ./internal/controller/ -run 'TestRestore|TestQueryError'` — fake
  client: Tripped+healthy → Restoring 0; five more healthy ticks advance 1→4
  then `Normal` with annotations gone and OD cleared (unset original);
  stored original values restored on complete; Restoring 2 + trip →
  Tripped step 0, LastTrippedAt bumped, original annotation kept; query
  error while Tripped does not restore.
- `make test` — controller 79.8%, metrics 80.4%, signatures 87.0%.
- `make lint` — 0 issues.

## Follow-ups / known gaps
- Secondary cell (VirtualService timeout) still unbuilt.
- Retry-storm / fan-out detectors and patches still unbuilt.
- Kind still has no Istio; this is not an Envoy-ejection integration test.
- `histogram_quantile` without `sum by (le)` and `response_flags=UR` still
  carried forward.
