# Security Policy

## Supported versions

Thrum is in early development and has not had a stable release yet. Security fixes are made on the `main` branch.

| Version | Supported |
|---|---|
| `main` | Yes |

## Reporting a vulnerability

**Please do not report security vulnerabilities through public GitHub issues, discussions or pull requests.**

Report them privately through [GitHub's private vulnerability reporting](https://github.com/yeixio/Thrum/security/advisories/new). Include as much of the following as you can:

- The kind of issue (for example credential exposure, privilege escalation, injection)
- The files or components involved
- Steps to reproduce, or a proof of concept
- The impact, and how an attacker might use it

You can expect an acknowledgement within 3 business days and an update on next steps within 7 days. We'll keep you informed as we work on a fix and credit you in the release notes if you'd like.

## Scope

Thrum is designed to be **read-only** against Kubernetes clusters. Issues that are especially important to us include:

- Anything that lets Thrum, or someone using it, change cluster state
- Leaking kubeconfig contents, tokens or other credentials
- RBAC in the Helm chart that grants more than read access
- Running untrusted input as code in the desktop app
