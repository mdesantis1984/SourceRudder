.PHONY: build run test clean deps lint release-gate release-readiness

BINARY=sourcerudder
VERSION=2.0.0
GO=go

build:
	$(GO) build -o bin/$(BINARY) ./cmd/sourcerudder

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

# release-image renders the exact tagged image digest into separate deployment
# assets so production references an immutable artifact while the templates
# remain reusable and unchanged.
# Operators MUST use this target (not a mutable tag like `:latest` or
# `:2.0.0`) so a rollback resolves to a known, reviewed image. The
# image reference and systemd identity placeholders are rendered into DIST_DIR.
#
# Usage:
#   make release-image IMAGE_DIGEST=sha256:<64-hex-digest>
#   # writes ghcr.io/mdesantis1984/sourcerudder:2.0.0@sha256:<digest>
#   # writes rendered deployment files plus VERSION and IMAGE into dist/
RELEASE_SHA ?= $(shell git rev-parse HEAD)
GHCR ?= ghcr.io/mdesantis1984
REGISTRY ?= sourcerudder
IMAGE_DIGEST ?=
RELEASE_IMAGE ?= $(GHCR)/$(REGISTRY):$(VERSION)@$(IMAGE_DIGEST)
DIST_DIR ?= dist
release-image:
	@printf '%s\n' "$(IMAGE_DIGEST)" | grep -Eq '^sha256:[0-9a-f]{64}$$' || { echo "release-image: IMAGE_DIGEST must be sha256:<64 lowercase hex chars>" >&2; exit 1; }
	@grep -qF 'image: <IMAGE>' deploy/kubernetes/deployment.yaml || { echo "release-image: Kubernetes template has no <IMAGE> placeholder" >&2; exit 1; }
	@grep -qF '@VERSION@' deploy/systemd/sourcerudder.service || { echo "release-image: systemd template has no @VERSION@ placeholder" >&2; exit 1; }
	@grep -qF '@IMAGE@' deploy/systemd/sourcerudder.service || { echo "release-image: systemd template has no @IMAGE@ placeholder" >&2; exit 1; }
	@echo "release-image: SOURCE_SHA=$(RELEASE_SHA) IMAGE=$(RELEASE_IMAGE)"
	@mkdir -p "$(DIST_DIR)"
	@sed 's|image: <IMAGE>|image: $(RELEASE_IMAGE)|' deploy/kubernetes/deployment.yaml > "$(DIST_DIR)/sourcerudder-kubernetes.yaml"
	@sed 's|@VERSION@|$(VERSION)|g; s|@IMAGE@|$(RELEASE_IMAGE)|g' deploy/systemd/sourcerudder.service > "$(DIST_DIR)/sourcerudder.service"
	@printf '%s\n' "$(VERSION)" > "$(DIST_DIR)/VERSION"
	@printf '%s\n' "$(RELEASE_IMAGE)" > "$(DIST_DIR)/IMAGE"
	@! grep -Eq '^[[:space:]]*image:[[:space:]]*<IMAGE>|@VERSION@|@IMAGE@' "$(DIST_DIR)/sourcerudder-kubernetes.yaml" "$(DIST_DIR)/sourcerudder.service" || { echo "release-image: unresolved release placeholder" >&2; exit 1; }
	@echo "release-image: immutable deployment assets written to $(DIST_DIR)"

# Release gate: dirty-worktree, review placeholder, line budget (with
# branch-exact carve-out), and go build/vet/test/test -race.
# RELEASE_GATE_SIZE_EXCEPTION must match the current branch name
# EXACTLY to unlock an over-budget PR.
release-gate:
	bash scripts/release-gate.sh

# Static migration checks run in ordinary CI. A tagged release additionally
# requires repository cutover, an activated reviewed license, and a legal
# approval reference.
release-readiness:
	bash scripts/release-readiness.sh --identity-only

# Local Docker QA stack targets. Additive — does not modify any
# existing target. Every qa-* command refuses argv to keep cleanup
# authority scoped to the sourcerudder-qa project.
.PHONY: qa-build qa-up qa-down qa-logs qa-smoke qa-clean

QA_COMPOSE_FILE := deploy/qa/docker-compose.yml

qa-build:
	docker compose -p sourcerudder-qa -f $(QA_COMPOSE_FILE) build

qa-up:
	bash scripts/qa-up.sh

qa-down:
	bash scripts/qa-down.sh

qa-logs:
	docker compose -p sourcerudder-qa -f $(QA_COMPOSE_FILE) logs --tail=200 -f

qa-smoke:
	bash scripts/qa-smoke.sh

qa-clean: qa-down
	docker compose -p sourcerudder-qa -f $(QA_COMPOSE_FILE) down --volumes
	-@docker network rm sourcerudder-qa_qa-net 2>/dev/null || true
	-@docker network rm qa-net 2>/dev/null || true
