.PHONY: build build-server build-client build-agent build-ui test clean install \
        test-coverage coverage-check coverage-baseline \
        docker-build docker-build-server docker-build-agent docker-build-client \
        docker-push docker-up docker-down docker-logs docker-agent-up docker-agent-down \
        cve packages repos packaging-key sign-artifacts lab-compose-up lab-compose-down lab-bootstrap-admin \
        lab-qemu-images lab-qemu-images-refresh lab-qemu-up lab-qemu-down lab-qemu-restart lab-qemu-test \
        lab-qemu-connect-agents lab-qemu-collect-keys lab-qemu-register-agents \
        lab-qemu-clean lab-qemu-start lab-qemu-update

# Build variables
BINARY_NAME=orion-belt
BUILD_DIR=bin
GO=go
GOFLAGS=-v
NODE_BIN ?= $(CURDIR)/.tools/node/bin
export PATH := $(NODE_BIN):$(PATH)

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS = -s -w \
	-X github.com/zrougamed/orion-belt/pkg/version.Version=$(VERSION) \
	-X github.com/zrougamed/orion-belt/pkg/version.Commit=$(COMMIT) \
	-X github.com/zrougamed/orion-belt/pkg/version.Date=$(DATE)

# Docker variables
DOCKER_REGISTRY ?=
DOCKER_IMAGE ?= orion-belt
DOCKER_TAG ?= latest
DOCKER_DIR=docker
DOCKERFILE=$(DOCKER_DIR)/Dockerfile
DOCKERFILE_AGENT=$(DOCKER_DIR)/Dockerfile.agent

# Helper to prefix image name with registry if set
ifdef DOCKER_REGISTRY
  IMAGE_PREFIX=$(DOCKER_REGISTRY)/$(DOCKER_IMAGE)
else
  IMAGE_PREFIX=$(DOCKER_IMAGE)
endif

# Build all components
build: build-ui build-server build-client build-admin build-agent

# Build React console into web/static (embedded by the server)
build-ui:
	@echo "Building web UI..."
	@command -v npm >/dev/null || { echo "npm not found — install Node.js or set NODE_BIN=$(NODE_BIN)"; exit 1; }
	cd web/ui && npm install --no-fund --no-audit && npm run build

# Build server. Depends on build-ui so the embedded web/static (go:embed)
# is never a stale, previously-committed snapshot — the UI ships with
# whatever's actually in web/ui/src, every time.
build-server: build-ui
	@echo "Building server $(VERSION) ($(COMMIT))..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)-server ./cmd/server

# Build client
build-client:
	@echo "Building client $(VERSION)..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/osh ./cmd/osh
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/ocp ./cmd/ocp

# Build admin
build-admin:
	@echo "Building admin $(VERSION)..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/oadmin ./cmd/oadmin

# Build agent
build-agent:
	@echo "Building agent $(VERSION)..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY_NAME)-agent ./cmd/agent

# Run tests
test:
	$(GO) test -v ./...

# Clean build artifacts
clean:
	@echo "Cleaning..."
	@rm -rf $(BUILD_DIR)
	@rm -f coverage.out

# Install binaries to $GOPATH/bin
install: build
	@echo "Installing..."
	@cp $(BUILD_DIR)/$(BINARY_NAME)-server $(GOPATH)/bin/
	@cp $(BUILD_DIR)/$(BINARY_NAME)-agent $(GOPATH)/bin/
	@cp $(BUILD_DIR)/osh $(GOPATH)/bin/
	@cp $(BUILD_DIR)/ocp $(GOPATH)/bin/

# Run server
run-server: build-server
	$(BUILD_DIR)/$(BINARY_NAME)-server

# Run tests with coverage
test-coverage:
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out

# Fail if any package's coverage dropped below coverage-baseline.txt
coverage-check:
	bash scripts/coverage-check.sh

# Re-record coverage-baseline.txt after adding tests
coverage-baseline:
	bash scripts/coverage-check.sh --update

# Format code
fmt:
	$(GO) fmt ./...

# Lint code
lint:
	golangci-lint run

# Download dependencies
deps:
	$(GO) mod download
	$(GO) mod verify

# Update dependencies
update-deps:
	$(GO) get -u ./...
	$(GO) mod tidy


# ────────────────────────────────────────────────────────────
# Docker targets
# ────────────────────────────────────────────────────────────

# Build server image
docker-build-server:
	@echo "Building server image: $(IMAGE_PREFIX)-server:$(DOCKER_TAG)..."
	docker build \
		--file $(DOCKERFILE) \
		--target server \
		--tag $(IMAGE_PREFIX)-server:$(DOCKER_TAG) \
		.

# Build agent image
docker-build-agent:
	@echo "Building agent image: $(IMAGE_PREFIX)-agent:$(DOCKER_TAG)..."
	docker build \
		--file $(DOCKERFILE_AGENT) \
		--target agent \
		--tag $(IMAGE_PREFIX)-agent:$(DOCKER_TAG) \
		.

# Start the server + Postgres via Docker Compose (see .env.server.example)
docker-up:
	docker compose -f docker-compose.server.yml --env-file .env.server up -d
	@echo "Server is up. SSH: localhost:2222  Web console: http://localhost:8080/ui"

# Stop the server stack
docker-down:
	docker compose -f docker-compose.server.yml --env-file .env.server down

# Tail server-stack logs (optionally filter: make docker-logs SERVICE=server)
docker-logs:
	docker compose -f docker-compose.server.yml --env-file .env.server logs -f $(SERVICE)

# Start an agent via Docker Compose (see .env.agent.example — register the
# agent on the server first and save its key to ./agent-key)
docker-agent-up:
	docker compose -f docker-compose.agent.yml --env-file .env.agent up -d

# Stop the agent
docker-agent-down:
	docker compose -f docker-compose.agent.yml --env-file .env.agent down

# ────────────────────────────────────────────────────────────
# Security / packaging / labs
# ────────────────────────────────────────────────────────────

# Fail CI if any CVE affects our code or release binaries (0CVE gate)
cve:
	bash scripts/cve-check.sh

# Build deb/rpm/apk (+ binaries) into dist/
packages:
	bash scripts/package.sh

# Build static apt/rpm/apk repos under repos/ (after make packages)
# Signed: ./scripts/gen-packaging-key.sh && ORION_REQUIRE_SIGN=1 make repos
repos:
	bash scripts/publish-repos.sh

# Generate packaging GPG key → packaging/keys/ (private key is gitignored)
packaging-key:
	bash scripts/gen-packaging-key.sh

# Detached GPG signatures + SHA256SUMS for dist/
sign-artifacts:
	bash scripts/sign-artifacts.sh

lab-compose-up:
	bash lab/compose/bootstrap-keys.sh
	docker compose -f lab/compose/docker-compose.yml up -d --build
	@echo "Waiting for API, then bootstrapping admin…"
	bash lab/bootstrap-admin.sh
	@echo "Lab up. SSH gateway :2222  API :8080  UI :8080/ui"

lab-compose-down:
	docker compose -f lab/compose/docker-compose.yml down

# Create/reuse lab admin SSH key and register is_admin user for /ui login
lab-bootstrap-admin:
	bash lab/bootstrap-admin.sh

lab-qemu-images:
	bash lab/qemu/download-images.sh

lab-qemu-images-refresh:
	ORION_REFRESH_IMAGES=1 bash lab/qemu/download-images.sh

lab-qemu-up:
	bash lab/qemu/up.sh

lab-qemu-down:
	bash lab/qemu/down.sh

# Restart QEMU VMs in place (keep disks). Optional: VMS="server alpine"
lab-qemu-restart:
	bash lab/qemu/restart.sh $(VMS)

lab-qemu-test:
	bash lab/qemu/test-e2e.sh

# HTTP release checks (see docs/RELEASE_SMOKE.md). Optional: ORION_API_KEY=... ORION_API=http://...
release-smoke:
	bash scripts/release-smoke.sh


# Collect keys → register on server → restart agents (optional: AGENTS="alpine debian")
lab-qemu-connect-agents:
	bash lab/qemu/connect-agents.sh $(AGENTS)

lab-qemu-collect-keys:
	bash lab/qemu/collect-agent-keys.sh

lab-qemu-register-agents:
	bash lab/qemu/register-agents.sh
	bash lab/qemu/restart-agents.sh


# Full wipe (VMs + images + credentials). Opt-out: KEEP_IMAGES=1 KEEP_CREDS=1
lab-qemu-clean:
	bash lab/qemu/clean.sh

# Clean (default) + boot + admin + agents + RBAC users + SSH howto
lab-qemu-start:
	bash lab/qemu/start.sh

# Rebuild binaries, scp into running VMs, reload server + agents
# Optional: AGENTS="server" | AGENTS="alpine debian" | SKIP_BUILD=1
lab-qemu-update:
	bash lab/qemu/update-bins.sh $(AGENTS)
