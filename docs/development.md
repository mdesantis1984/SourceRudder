[English](development.md) | [Español](development.es.md)

# SourceRudder development guide

This guide defines the local development and verification path for SourceRudder 2.0. It does not authorize a production rollout.

## Local setup

```bash
git clone https://github.com/mdesantis1984/SourceRudder.git
cd SourceRudder
go mod download
make build
./bin/sourcerudder -transport stdio
```

The module is `github.com/mdesantis1984/SourceRudder`; the binary is `sourcerudder`. Use `SOURCERUDDER_AUTH_KEY` for HTTP authentication rather than placing a secret in command arguments:

```bash
export SOURCERUDDER_AUTH_KEY='replace-with-a-local-secret'
./bin/sourcerudder -transport http -http-addr :8080
```

## Package map

| Path | Responsibility |
|---|---|
| `cmd/sourcerudder` | Binary entry point, flags, transport, and service wiring. |
| `internal/mcp` | MCP server, HTTP and stdio transports, and protected endpoints. |
| `internal/auth` | Fail-closed HTTP credential validation. |
| `internal/connectors` | Search-provider and official-documentation connectors. |
| `internal/fetch` | Bounded remote retrieval and SSRF protections. |
| `internal/search` | Planning and connector management. |
| `internal/cache`, `internal/observability` | Runtime support services. |
| `pkg/types` | Shared public Go request and response types. |
| `deploy/qa`, `scripts/quality` | Isolated QA stack and Python quality baseline. |

## Verification commands

Use a focused test while iterating, then run the full checks:

```bash
# Focused package test
go test ./internal/auth -run TestValidator

# Full Go suite
go build ./...
go vet ./...
go test ./...
go test -race ./...

# Python quality suite
python3 -m unittest discover -s scripts/quality -p 'test_*.py' -v

# Compose schema and interpolation validation; no containers are started
docker compose -f deploy/qa/docker-compose.yml config --quiet
```

Run the standalone Kubernetes-manifest and QA shell tests when the affected scope requires them:

```bash
bash tests/k8s/k8s_deployment_test.sh
bash tests/qa/qa_scripts_test.sh
```

Run the local QA stack only when the change affects it. The lifecycle is intentionally scoped to `sourcerudder-qa`:

```bash
make qa-up
make qa-smoke
make qa-down
```

For the live-search baseline, inspect the manifest first and write evidence outside the repository:

```bash
python3 scripts/quality/live_baseline.py manifest
python3 scripts/quality/live_baseline.py run --output /tmp/sourcerudder-quality/baseline.json
```

## Release gates and review

Before merge, run the applicable checks and record the commands actually run in the pull request. `make release-gate` verifies a clean worktree, the authored line budget, and `go build`, `go vet`, full tests, and race tests. It is a merge-quality gate, not a deployment command.

Keep changes as reviewable work units: one coherent behavior per commit or pull request, with its tests and documentation. Do not combine unrelated refactors, generated output, or bulk formatting. The release gate applies a 1,000-line authored budget unless an approved, tracked exception applies.

## Documentation parity

Public contributor, security, and development guides are bilingual. Every paired document begins with visible `English | Español` links. Update both language files in the same change, preserve equivalent meaning, and keep external contracts unchanged: MCP tool names, JSON fields, flags, `sourcerudder`, and `SOURCERUDDER_AUTH_KEY` are not translation targets.

## Rollback boundary

Development verification never rolls out production. If an operator needs to recover a deployment, they must restore the previously reviewed immutable image digest and source revision, then verify `/healthz`, MCP initialization, and representative requests in their controlled environment. Preserve the previous digest and revision until the observation window closes; a healthy endpoint alone does not prove the expected binary is running.
