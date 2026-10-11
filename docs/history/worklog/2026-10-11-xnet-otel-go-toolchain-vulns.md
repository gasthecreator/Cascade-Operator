# Bump golang.org/x/net, OpenTelemetry and the govulncheck Go toolchain

**Date:** 2026-10-11
**Author:** Claude (solo)
**Type:** fix (dependency / security)

## What
Delivered in its own PR (#66), recorded here because the worklog lives on this
branch.
- `golang.org/x/net` v0.57.0 to v0.61.0 (GO-2026-6617, fixed in v0.60.0).
- OpenTelemetry `otel`, `sdk`, `otlptrace`, `otlptracegrpc` v1.44.0 to v1.45.0
  (GO-2026-6505), with the `otlp` proto and `genproto` modules `go get` moved.
- `govulncheck.yml` Go toolchain `1.26.6` to `1.26.9`, which carries the fixes
  for the reported `net/http`, `net/textproto`, `crypto/tls` and
  `html/template` standard-library findings.

## Why
`govulncheck` failed on every PR after new advisories were published, and
`main` had the same exposure. Same shape as the earlier grpc fix: a baseline
break, not something a single PR introduced. It surfaced on this branch's
repo-standards PR (#65), which changes none of the affected code.

## How
`go get` plus `go mod tidy`, and one pinned-version edit in the workflow. No
source changes. The standard-library findings are fixed by toolchain rather
than by module, which is why the workflow pin moves.

## Testing
`go build`, `go vet`, `make fmt vet lint` and `make test` pass.
`GOTOOLCHAIN=go1.26.9 govulncheck ./...` reports 0 affected vulnerabilities.
With the local go1.27.0 toolchain it still reports standard-library findings
fixed in go1.27.2, which CI does not use.

## Follow-ups / known gaps
`setup-go` in the other workflows resolves the version from `go.mod`
(`go 1.26.0`); only the vulnerability scan needs the newest patch release, so
that pin must be bumped again when the next Go security release ships.
