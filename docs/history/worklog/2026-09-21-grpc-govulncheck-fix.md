# Bump google.golang.org/grpc to v1.83.1 (GO-2026-6348)

**Date:** 2026-09-21
**Author:** Claude (solo)
**Type:** fix (dependency / security)

## What
Bumped the indirect dependency `google.golang.org/grpc` from v1.82.1 to
v1.83.1 (plus the `otel` and `genproto` modules `go get` pulled along with it).

## Why
`govulncheck` began failing CI on every PR once GO-2026-6348 was disclosed
against grpc v1.82.1 (fixed in v1.83.1). The failure showed up even on
Dependabot PRs that touch no Go code (#52, #54), which is how it was
identified as a baseline problem on `main` rather than something any single
bump introduced.

## How
`go get google.golang.org/grpc@v1.83.1 && go mod tidy`. No source changes.

## Testing
`go build ./...`, `go vet ./...`, `go test ./... -race` all pass. `govulncheck
./...` reports 0 affected vulnerabilities (previously 1, via grpc).

## Follow-ups / known gaps
- Open Dependabot PRs #52-#56 need a rebase onto this fix to go green.
- #51's failure was a separate transient `sum.golang.org` fetch error during
  the golangci-lint install, unrelated to this.
