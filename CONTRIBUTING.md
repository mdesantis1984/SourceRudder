[English](CONTRIBUTING.md) | [Español](CONTRIBUTING.es.md)

# Contributing to SourceRudder

Contributions should be small, verifiable, and tied to a concrete issue. For local development and QA details, see the [development guide](docs/development.md).

## Before coding

1. Check for an existing issue; open or request one for significant work.
2. Agree on scope, observable behavior, and acceptance criteria.
3. Report vulnerabilities privately through the process in [SECURITY.md](SECURITY.md).

Maintainers may exempt trivial text corrections from the issue-first rule.

## Set up

```bash
git clone https://github.com/mdesantis1984/SourceRudder.git
cd SourceRudder
go mod download
go test ./...
```

SourceRudder 2.0 uses Go `1.26.6`. Docker Compose v2 and Python 3 are required for the QA and quality suites.

## Verify your change

Run the smallest relevant check while iterating, then the full set before requesting review:

```bash
# Focused Go test
go test ./internal/auth -run TestValidator

# Full Go checks
go build ./...
go vet ./...
go test ./...
go test -race ./...

# Python quality tests
python3 -m unittest discover -s scripts/quality -p 'test_*.py' -v

# Validate the QA Compose definition without starting containers
docker compose -f deploy/qa/docker-compose.yml config --quiet
```

Run the standalone Kubernetes-manifest and QA shell tests when the affected scope requires them:

```bash
bash tests/k8s/k8s_deployment_test.sh
bash tests/qa/qa_scripts_test.sh
```

When a change affects the local QA stack, also run its scoped lifecycle:

```bash
make qa-up
make qa-smoke
make qa-down
```

The QA scripts only manage the `sourcerudder-qa` project. They are local verification, not a production rollout.

## Branches, commits, and pull requests

Use descriptive branches such as `feat/local-index-ranking` or `fix/http-auth-header`. Keep each commit a reviewable work unit: include its tests and documentation, and do not mix unrelated refactors or bulk formatting.

Use Conventional Commits:

```text
feat(search): add provider filter
fix(auth): reject empty bearer tokens
docs(clients): clarify remote headers
```

Do not add `Co-Authored-By` trailers, automated attribution, secrets, local output, binaries, or temporary evidence.

Pull requests must link the approved issue (for example, `Closes #123`), explain the behavioral change, list commands actually run, and state risks, limits, and rollback steps. The release gate enforces a 1,000-line authored budget against the merge base; split larger work into reviewable units unless an approved, tracked exception applies.

Wait for CI and the release gate before requesting merge. This repository documents and verifies changes; it does not authorize or perform production rollouts.

## Compatibility and license

Keep MCP tool names, JSON fields, flags, environment variables, and other wire contracts exactly as implemented. In particular, use `sourcerudder` and `SOURCERUDDER_AUTH_KEY`; do not rename external contracts in documentation.

The root [LICENSE](LICENSE) remains MIT pending professional legal review. Do not treat this pending status as approval of any draft or license change.
