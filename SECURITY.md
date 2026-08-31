# Security Policy

## Supported versions

This project is under active development (v1alpha1 API). Security fixes are
applied on `main` only. There are no long-term release branches yet.

| Version | Supported          |
| ------- | ------------------ |
| main    | :white_check_mark: |

## Reporting a vulnerability

**Please do not open a public GitHub issue for security vulnerabilities.**

Use one of these channels:

1. **GitHub Security Advisories (preferred):** open a
   [private vulnerability report](https://github.com/gasthecreator/Cascade-Operator/security/advisories/new)
   on this repository if you have access to the GitHub UI.
2. **Email:** send details to **gideonsanni2023@gmail.com** with the subject
   `Cascade-Operator security`.

Include:

- A description of the issue and its impact
- Steps to reproduce (proof-of-concept if available)
- Affected versions or commits
- Any suggested fix, if you have one

You should receive an acknowledgment within a few business days. We will
coordinate on disclosure timing and credit if you want it.

## Scope

In scope: this repository's operator code, CRD validation/webhook logic (when
present), RBAC manifests, and documented deployment paths.

Out of scope: vulnerabilities in upstream dependencies (report those to the
upstream project), misconfigurations in your own cluster, or issues in Istio /
Prometheus themselves unless this operator introduces a specific unsafe default.
