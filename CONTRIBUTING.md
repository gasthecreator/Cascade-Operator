# Contributing to Cascade Operator

Thank you for your interest in contributing. This document covers how to work
in this repository day to day. For building, running, and live-demo setup, see
the [README](README.md) — that file is the source of truth for dev
environment instructions; this file does not duplicate them.

## Code of conduct

Participation is governed by [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).

## License

By contributing, you agree that your contributions will be licensed under the
[Apache License 2.0](LICENSE), matching the headers in source files.

## Branch and commit conventions

- Branch from `main` with a short, descriptive prefix when it helps review:
  `feat/…`, `fix/…`, `docs/…`, `chore/…`.
- Keep commits focused. One logical change per commit is preferred.
- Write commit messages in the imperative mood (`Add …`, `Fix …`, `Update …`)
  with a subject line that explains *why* the change matters, not just what
  files moved.
- Do not force-push to `main`. Branch protection on `main` (required reviews,
  status checks, etc.) is configured in **GitHub repository settings** — it
  cannot be enforced from a file in this tree. Ask a maintainer if you need
  those settings adjusted.

## Before you open a PR

```bash
make fmt
make lint
make test
```

If your change touches Istio patch paths or reconciliation wire-format,
also run against the dev Kind cluster:

```bash
make test-integration   # requires kind-cascade-operator context; see README
```

## How this repository is governed (read this)

This project uses an unusual but deliberate documentation protocol. It keeps
architecture decisions stable while still moving fast with AI-assisted
implementation.

### `PLAN.md` — current truth

- Reflects **what is decided and built now**: architecture, patch matrix,
  checklist status.
- **Do not edit** `PLAN.md`'s **Architecture Decisions** (§2) or **Open
  Questions** (§4) directly in a feature PR. Those sections change only after
  review.
- Checklist items in §3 and §5 *may* be flipped to `[x]` when the corresponding
  work is actually done — often by the implementer, always with a worklog entry
  backing it up.

### `PROPOSALS.md` — proposed changes to decided architecture

- File a proposal when you want to change something already locked in
  `PLAN.md` (CRD shape, patch matrix, CI policy, multi-mesh direction, etc.).
- Proposals are reviewed **before** implementation. Approved proposals get
  merged into `PLAN.md`; rejected ones stay documented for context.
- Small, non-architectural bug fixes and tests do not need a proposal.

### `docs/worklog/` — history and reasoning

- Every meaningful slice of work gets an append-only worklog entry
  (`YYYY-MM-DD-short-slug.md`) explaining **what**, **why**, **how**, and
  **what was tested**.
- Reasoning lives here, not in `PLAN.md`. If a checklist item flips, the
  worklog is where reviewers learn *why*.
- Index new entries in `docs/worklog/README.md`.

### Typical flow

1. **Architecture change?** → `PROPOSALS.md` entry → review → update `PLAN.md`
   → implement → worklog.
2. **Implementation within existing architecture?** → branch → code → worklog →
   PR (use the PR template checklist).
3. **Independent review** (human or designated reviewer) rebuilds, runs tests,
   and re-verifies any live-cluster claims before merge.

### `AGENTS.md`

- Cursor and other agents read [AGENTS.md](AGENTS.md) for kubebuilder
  conventions, test commands, and scaffold rules. Humans can ignore it unless
  you're editing agent instructions.

## Pull requests

Use the [pull request template](.github/PULL_REQUEST_TEMPLATE.md). Link
related issues. For user-visible behavior changes, add a line to
[CHANGELOG.md](CHANGELOG.md) under `[Unreleased]`.

## Security

See [SECURITY.md](SECURITY.md) for vulnerability reporting — do not file
security issues publicly.

## Questions

Open a [feature request](.github/ISSUE_TEMPLATE/feature_request.yml) or
[bug report](.github/ISSUE_TEMPLATE/bug_report.yml) issue, or reach out via
the contact in [SECURITY.md](SECURITY.md) for sensitive topics.
