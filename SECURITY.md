# Security Policy

## Reporting a vulnerability

Do not open a public issue for suspected vulnerabilities or exposed secrets.
Use GitHub private vulnerability reporting from the repository **Security** tab.
Include the affected version, impact, reproduction steps, and a minimal proof of
concept without real credentials or private infrastructure identifiers.

If private reporting is unavailable, contact the repository owner through their
GitHub profile and request a private channel before sharing technical details.

## Supported version

Security fixes target the latest release line. Older development candidates may
receive a fix only when the vulnerable behavior is still present in the latest
version.

## Secret handling

- Never commit API keys, passwords, tokens, private keys, `.env` files, or
  production corpus data.
- Use `IA_BUSCAR_AUTH_KEY` and `MEMORY_APIKEY` through the process environment or
  a secret manager. Do not pass secrets through CLI flags.
- Rotate a credential immediately if it appears in Git history, CI logs, issue
  text, artifacts, container metadata, or process arguments.
- Treat local-index corpus content as data visible to every authorized MCP
  client. The loader validates structure and URL safety, not semantic secrecy.

## Security boundaries

- HTTP `/mcp` and `/metrics` fail closed when authentication is not configured.
- `/healthz` is intentionally unauthenticated and returns only a fixed status.
- URL fetching rejects non-public targets and validates every redirect hop.
- The local index reads one explicitly configured regular file at startup,
  rejects symlinks and unsafe permissions, and performs no crawling or network
  access.
- Optional IA_Recuerdo integration can transmit payloads to `MEMORY_URL`; leave
  it unset when external memory is not approved.

## Public deployment guidance

Run the service behind TLS and network access controls. Use a dedicated
least-privilege account, a read-only filesystem where possible, bounded resource
limits, immutable images, and centralized secret injection. Do not publish a
development SearXNG instance, local corpus, or observability endpoint directly
to the Internet.
