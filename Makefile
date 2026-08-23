.PHONY: build run test clean deps lint release-gate

BINARY=ia-buscar
VERSION=1.2.0
GO=go

build:
	$(GO) build -o bin/$(BINARY) ./cmd/ia-buscar

run: build
	./bin/$(BINARY) -transport stdio

run-http: build
	./bin/$(BINARY) -transport http -http-addr :8080

test:
	$(GO) test ./...

clean:
	rm -rf bin/$(BINARY)

deps:
	$(GO) mod download
	$(GO) mod tidy

lint:
	golangci-lint run

fmt:
	$(GO) fmt ./...

# release-image tags the container image with the current commit SHA
# so the production deployment references an immutable artifact.
# Operators MUST use this target (not a mutable tag like `:latest` or
# `:1.2.0`) so a rollback resolves to a known, reviewed image. The
# image reference is also written into deploy/kubernetes/deployment.yaml
# and the systemd unit ExecStartPre placeholders are substituted;
# commit the result with the release.
#
# Usage:
#   make release-image GHCR=ghcr.io/thiscloud REGISTRY=ia-buscar
#   # writes image: ghcr.io/thiscloud/ia-buscar:<sha> into deployment.yaml
#   # and substitutes @VERSION@/@IMAGE@ in deploy/systemd/ia-buscar.service
RELEASE_SHA ?= $(shell git rev-parse HEAD)
RELEASE_IMAGE ?= $(GHCR)/$(REGISTRY):$(RELEASE_SHA)
release-image:
	@echo "release-image: SHA=$(RELEASE_SHA) IMAGE=$(RELEASE_IMAGE)"
	@sed -i.bak 's|image: <IMAGE>|image: $(RELEASE_IMAGE)|' deploy/kubernetes/deployment.yaml && rm -f deploy/kubernetes/deployment.yaml.bak
	@sed -i.bak 's|@VERSION@|$(RELEASE_SHA)|g; s|@IMAGE@|$(RELEASE_IMAGE)|g' deploy/systemd/ia-buscar.service && rm -f deploy/systemd/ia-buscar.service.bak
	@echo "release-image: deployment.yaml + ia-buscar.service updated; verify with 'git diff' before commit"

# Release gate: dirty-worktree, review placeholder, line budget (with
# branch-exact carve-out), and go build/vet/test/test -race.
# RELEASE_GATE_SIZE_EXCEPTION must match the current branch name
# EXACTLY to unlock an over-budget PR.
release-gate:
	bash scripts/release-gate.sh

# Local Docker QA stack targets. Additive — does not modify any
# existing target. Every qa-* command refuses argv to keep cleanup
# authority scoped to the ia-buscar-qa project.
.PHONY: qa-build qa-up qa-down qa-logs qa-smoke qa-clean

QA_COMPOSE_FILE := deploy/qa/docker-compose.yml

qa-build:
	docker compose -p ia-buscar-qa -f $(QA_COMPOSE_FILE) build

qa-up:
	bash scripts/qa-up.sh

qa-down:
	bash scripts/qa-down.sh

qa-logs:
	docker compose -p ia-buscar-qa -f $(QA_COMPOSE_FILE) logs --tail=200 -f

qa-smoke:
	bash scripts/qa-smoke.sh

qa-clean: qa-down
	docker compose -p ia-buscar-qa -f $(QA_COMPOSE_FILE) down --volumes
	-@docker network rm ia-buscar-qa_qa-net 2>/dev/null || true
	-@docker network rm qa-net 2>/dev/null || true