# HardCoreVisor — Unified Build System
# Usage: just <recipe>    List: just --list

set dotenv-load

# Default: show help
default:
    @just --list

# ═══════════════════════════════════════════════════════════
# Rust Recipes
# ═══════════════════════════════════════════════════════════

# Build all Rust crates
rust-build:
    cargo build --workspace

# Build Rust in release mode
rust-release:
    cargo build --workspace --release

# Run all Rust tests (serial for shared state safety)
rust-test:
    cargo nextest run --workspace 2>/dev/null || cargo test --workspace -- --test-threads=1

# Run vmcore tests only
rust-test-vmcore:
    cargo test -p vmcore -- --test-threads=1

# Run KVM ioctl tests only (/dev/kvm required)
rust-test-kvm:
    cargo test -p vmcore kvm_sys -- --test-threads=1 --nocapture

# Run vmcore tests for a specific module
rust-test-mod module:
    cargo test -p vmcore {{module}} -- --test-threads=1

# Clippy lint (deny warnings)
rust-clippy:
    cargo clippy --workspace --all-targets -- -D warnings

# Format check
rust-fmt:
    cargo fmt --all -- --check

# Format fix
rust-fmt-fix:
    cargo fmt --all

# Watch vmcore tests on change
rust-watch-vmcore:
    cargo watch -x 'test -p vmcore -- --test-threads=1' -c

# Watch & run TUI
rust-watch-tui:
    cargo watch -x 'run -p hcvtui' -c

# Run TUI directly
tui:
    cargo run -p hcvtui

# Code coverage (Linux only)
rust-coverage:
    cargo tarpaulin --workspace --out Html --output-dir target/coverage -- --test-threads=1

# Expand macros for debugging
rust-expand crate:
    cargo expand -p {{crate}}

# ═══════════════════════════════════════════════════════════
# Documentation
# ═══════════════════════════════════════════════════════════

# Build documentation site
docs:
    cd docs/book && mdbook build

# Serve documentation locally
docs-serve:
    cd docs/book && mdbook serve

# ═══════════════════════════════════════════════════════════
# Go Recipes
# ═══════════════════════════════════════════════════════════

# Initialize Go dependencies (run once after clone)
go-init:
    cd controller && go mod tidy

# Build all Go binaries
go-build:
    cd controller && go build ./...

# Build Go Controller binary
go-controller:
    cd controller && go build -o ../target/controller ./cmd/controller

# Build hcvctl CLI binary with version injection
go-hcvctl:
    cd controller && go build \
        -ldflags "-X main.version=$(git describe --tags --always) \
                  -X main.commit=$(git rev-parse --short HEAD) \
                  -X main.buildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        -o ../target/hcvctl ./cmd/hcvctl

# Run all Go tests with race detection
go-test:
    cd controller && go test -v -race -count=1 ./...

# Run Go E2E tests only
go-test-e2e:
    cd controller && go test -v -race -count=1 -run TestE2E ./tests/

# Run Go API unit tests only
go-test-api:
    cd controller && go test -v -race -count=1 ./internal/api/

# Lint Go code
go-lint:
    cd controller && golangci-lint run --fast

# Go vet
go-vet:
    cd controller && go vet ./...

# Format Go code
go-fmt:
    cd controller && gofmt -l -w .

# Go vulnerability check
go-vuln:
    cd controller && govulncheck ./...

# Run Controller (REST :8080 + gRPC :9090, full service mode)
go-run:
    cd controller && go run ./cmd/controller

# ═══════════════════════════════════════════════════════════
# Proto Recipes
# ═══════════════════════════════════════════════════════════

# Generate Go code from proto files
proto-gen:
    ./scripts/proto-gen.sh

# ═══════════════════════════════════════════════════════════
# Unified Recipes
# ═══════════════════════════════════════════════════════════

# Build everything
build: rust-build go-build

# Build release binaries
release: rust-release go-controller go-hcvctl

# Run all tests (Rust 48 + Go 11 = 59 tests)
test: rust-test go-test

# Run all lints
lint: rust-clippy rust-fmt go-vet

# Full pre-commit check (lint + test)
check: lint test

# Clean all build artifacts
clean:
    cargo clean
    cd controller && go clean ./...
    rm -rf target/controller target/hcvctl

# ═══════════════════════════════════════════════════════════
# Web UI Recipes
# ═══════════════════════════════════════════════════════════

# Start Web UI dev server (Vite, :5173)
webui-dev:
    cd webui && npm run dev

# Build Web UI for production
webui-build:
    cd webui && npm run build

# Install Web UI dependencies
webui-install:
    cd webui && npm install

# ═══════════════════════════════════════════════════════════
# Docker / Dev Services
# ═══════════════════════════════════════════════════════════

# Start dev services (etcd, prometheus, grafana)
dev-up:
    docker compose -f deploy/docker-compose.yml up -d

# Stop dev services
dev-down:
    docker compose -f deploy/docker-compose.yml down

# Show dev service logs
dev-logs:
    docker compose -f deploy/docker-compose.yml logs -f

# ═══════════════════════════════════════════════════════════
# Integration / E2E Tests
# ═══════════════════════════════════════════════════════════

# Run full E2E integration tests
e2e:
    chmod +x scripts/run-e2e.sh
    ./scripts/run-e2e.sh

# Run E2E with Docker stack
e2e-stack:
    chmod +x scripts/run-e2e.sh
    ./scripts/run-e2e.sh --with-stack

# Run only Rust E2E tests
e2e-rust:
    chmod +x scripts/run-e2e.sh
    ./scripts/run-e2e.sh --rust-only

# Run only Go E2E tests
e2e-go:
    chmod +x scripts/run-e2e.sh
    ./scripts/run-e2e.sh --go-only

# Quick pre-commit check (<30s)
quick:
    chmod +x scripts/quick-check.sh
    ./scripts/quick-check.sh

# Build Docker images
docker-build:
    docker compose -f deploy/docker-compose.yml build

# Run stack smoke test (build + start + test + teardown)
stack-test:
    chmod +x scripts/stack-smoke-test.sh
    ./scripts/stack-smoke-test.sh --build --down

# Run stack smoke test (keep running after test)
stack-test-keep:
    chmod +x scripts/stack-smoke-test.sh
    ./scripts/stack-smoke-test.sh --build

# ═══════════════════════════════════════════════════════════
# Coverage
# ═══════════════════════════════════════════════════════════

# Run Go code coverage
go-coverage:
    cd controller && GOTOOLCHAIN=local go test -coverprofile=coverage.out -count=1 ./...
    cd controller && GOTOOLCHAIN=local go tool cover -html=coverage.out -o coverage.html
    @echo "Go coverage report: controller/coverage.html"

# ═══════════════════════════════════════════════════════════
# Security
# ═══════════════════════════════════════════════════════════

# Run security audits
audit:
    cargo audit
    cd controller && govulncheck ./...

# ═══════════════════════════════════════════════════════════
# Benchmarks
# ═══════════════════════════════════════════════════════════

# Run performance benchmarks
bench:
    cargo bench -p vmcore

# Run Go benchmarks
go-bench:
    cd controller && GOTOOLCHAIN=local go test -bench=. -benchmem ./internal/api/

# Run API load test
load-test:
    chmod +x scripts/load-test.sh
    ./scripts/load-test.sh

# Run io_uring benchmark
io-bench:
    chmod +x scripts/io-bench.sh
    ./scripts/io-bench.sh

# ═══════════════════════════════════════════════════════════
# Release
# ═══════════════════════════════════════════════════════════

# Tag a release version
release-tag version:
    git tag -a v{{version}} -m "Release v{{version}}"
    @echo "Tagged v{{version}}. Push with: git push origin v{{version}}"

# Generate man pages from hcvctl CLI
man-pages:
    cd controller && go run ./cmd/hcvctl gendoc --type man --dir ../docs/man

# Generate markdown docs from hcvctl CLI
cli-docs:
    cd controller && go run ./cmd/hcvctl gendoc --type markdown --dir ../docs/cli

# Build .deb package
deb:
    chmod +x scripts/build-deb.sh
    ./scripts/build-deb.sh

# Build release binaries
release-build: rust-release go-controller go-hcvctl
    @echo "Release binaries:"
    @ls -lh target/release/libvmcore.a target/controller target/hcvctl 2>/dev/null || true

# ═══════════════════════════════════════════════════════════
# Production Deployment
# ═══════════════════════════════════════════════════════════

# Deploy to production host (default: hardcoremonk@192.168.3.50)
deploy-prod host="hardcoremonk@192.168.3.50":
    chmod +x scripts/deploy-production.sh
    TARGET_HOST={{host}} ./scripts/deploy-production.sh

# Generate self-signed TLS certificates for dev/testing
gen-tls:
    chmod +x scripts/gen-tls-certs.sh
    ./scripts/gen-tls-certs.sh

# Start production Docker Compose stack
prod-up:
    docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.prod.yml up -d

# Stop production Docker Compose stack
prod-down:
    docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.prod.yml down
