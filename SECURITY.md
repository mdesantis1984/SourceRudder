[English](SECURITY.md) | [Español](SECURITY.es.md)

# SourceRudder Security Policy

## Supported versions

| Version line | Status |
|---|---|
| SourceRudder 2.0 | Unreleased development line; security fixes are evaluated against current `main`. |
| Historical 1.x releases | Historical only; they receive no automatic security support. |

Forks, modified deployments, and unpinned snapshots are outside automatic support.

## Report a vulnerability

Do not disclose exploitable vulnerabilities in issues, discussions, pull requests, or other public channels.

While this repository is private, send reports directly to a maintainer through
the private collaboration channel used to grant repository access. Do not
disclose the vulnerability in an issue, discussion, pull request, or other
shared channel. Before public access is enabled, maintainers must configure
GitHub private vulnerability reporting and replace this interim route.

Include the affected version, commit, or image; safe reproduction steps; observed impact; redacted logs; and known mitigations. Do not include secrets. Response priority depends on impact, exploitability, and the availability of a safe mitigation.

## Security model

- Authentication fails closed: an absent or empty `SOURCERUDDER_AUTH_KEY` does not permit anonymous access to protected HTTP endpoints.
- Credentials are MAC-compared in constant time. `X-Api-Key` takes precedence over `Authorization: Bearer`; `/healthz` remains the public probe endpoint.
- Fetching accepts only `http` and `https`, validates every redirect, blocks loopback and other non-public destinations, and pins a connection to the validated IP to reduce SSRF and DNS-rebinding exposure.
- Fetch response size, redirects, attempts, and concurrent work are bounded to contain resource use.
- Secrets belong in protected environment variables or a secrets manager, never Git, shell history, visible command arguments, logs, or issue bodies.
- Deployment images must be pinned by immutable digest. QA Compose definitions set memory, memory-swap, CPU, PID, restart, and log-rotation limits; preserve equivalent controls in operator-managed deployments.

## Operator responsibilities

- Use distinct, randomly generated keys per environment and rotate a suspected exposed key immediately.
- Keep the service on loopback or a private network and terminate TLS in a trusted proxy.
- Protect `/metrics` as strictly as `/mcp`.
- Treat queries, URLs, logs, and fetched content as sensitive operational data.
- Review `warnings`, `errors`, `partial`, and `strategy`; degraded results are not complete evidence.
- Keep Go, container images, SearXNG, the host, and project dependencies current.

## Coordinated disclosure

Publish technical details only after a fix or mitigation exists and a disclosure date is agreed with the reporter. Credit is coordinated with the reporter and may be withheld at their request.
