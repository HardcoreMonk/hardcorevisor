#!/usr/bin/env bash
# deploy-production.sh — Build and deploy HardCoreVisor to a production host.
#
# Usage:
#   ./scripts/deploy-production.sh                        # default: hardcoremonk@192.168.3.50
#   ./scripts/deploy-production.sh --host user@10.0.0.5   # custom target
#   ./scripts/deploy-production.sh --docker               # Docker Compose deployment
#   ./scripts/deploy-production.sh --dry-run              # preview without executing
#
# Environment:
#   TARGET_HOST   Override default target (hardcoremonk@192.168.3.50)
#   SSH_KEY       Path to SSH private key (optional)

set -euo pipefail

# ── Defaults ──────────────────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

TARGET_HOST="${TARGET_HOST:-hardcoremonk@192.168.3.50}"
SSH_KEY="${SSH_KEY:-}"
DRY_RUN=false
DOCKER_MODE=false

REMOTE_BIN_DIR="/usr/local/bin"
REMOTE_CONF_DIR="/etc/hardcorevisor"
REMOTE_TLS_DIR="/etc/hardcorevisor/tls"
REMOTE_DATA_DIR="/var/lib/hardcorevisor"
REMOTE_LOG_DIR="/var/log/hardcorevisor"
SERVICE_USER="hcv"
SERVICE_NAME="hardcorevisor"

# ── Color helpers ─────────────────────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
err()   { echo -e "${RED}[ERROR]${NC} $*" >&2; }
step()  { echo -e "${CYAN}[STEP]${NC}  $*"; }
dry()   { echo -e "${YELLOW}[DRY-RUN]${NC} $*"; }

# ── Argument parsing ──────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
    case "$1" in
        --host)
            TARGET_HOST="$2"; shift 2 ;;
        --docker)
            DOCKER_MODE=true; shift ;;
        --dry-run)
            DRY_RUN=true; shift ;;
        --ssh-key)
            SSH_KEY="$2"; shift 2 ;;
        -h|--help)
            echo "Usage: $0 [--host user@host] [--docker] [--dry-run] [--ssh-key path]"
            echo ""
            echo "Options:"
            echo "  --host user@host   Target host (default: \$TARGET_HOST or hardcoremonk@192.168.3.50)"
            echo "  --docker           Deploy via Docker Compose instead of bare-metal"
            echo "  --dry-run          Show what would be done without executing"
            echo "  --ssh-key path     SSH private key for authentication"
            echo ""
            echo "Environment variables:"
            echo "  TARGET_HOST        Override default target host"
            echo "  SSH_KEY            Path to SSH private key"
            exit 0 ;;
        *)
            err "Unknown option: $1"; exit 1 ;;
    esac
done

SSH_OPTS="-o StrictHostKeyChecking=accept-new -o ConnectTimeout=10"
if [[ -n "$SSH_KEY" ]]; then
    SSH_OPTS="$SSH_OPTS -i $SSH_KEY"
fi

# ── Helper: run or dry-run ────────────────────────────────────────────────────
run() {
    if $DRY_RUN; then
        dry "$*"
    else
        eval "$@"
    fi
}

remote() {
    if $DRY_RUN; then
        dry "ssh $TARGET_HOST: $*"
    else
        ssh $SSH_OPTS "$TARGET_HOST" "$@"
    fi
}

# ── Pre-flight checks ────────────────────────────────────────────────────────
preflight() {
    step "Pre-flight checks"

    if ! command -v cargo &>/dev/null; then
        err "cargo not found. Install Rust toolchain first."
        exit 1
    fi

    if ! command -v go &>/dev/null; then
        err "go not found. Install Go 1.24+ first."
        exit 1
    fi

    if ! command -v rsync &>/dev/null && ! command -v scp &>/dev/null; then
        err "rsync or scp required for file transfer."
        exit 1
    fi

    if ! $DRY_RUN; then
        info "Testing SSH connectivity to $TARGET_HOST ..."
        if ! ssh $SSH_OPTS "$TARGET_HOST" "echo ok" &>/dev/null; then
            err "Cannot connect to $TARGET_HOST via SSH."
            exit 1
        fi
        info "SSH connection OK."
    fi
}

# ── Build release binaries ────────────────────────────────────────────────────
build_release() {
    step "Building release binaries"
    cd "$PROJECT_ROOT"

    info "Building Rust workspace (release) ..."
    run "cargo build --workspace --release"

    info "Building Go controller ..."
    local ldflags="-s -w"
    ldflags="$ldflags -X main.version=$(git describe --tags --always 2>/dev/null || echo dev)"
    ldflags="$ldflags -X main.commit=$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
    ldflags="$ldflags -X main.buildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)"

    run "cd '$PROJECT_ROOT/controller' && go build -ldflags='$ldflags' -o '$PROJECT_ROOT/target/release/hcv-controller' ./cmd/controller"

    info "Building hcvctl CLI ..."
    run "cd '$PROJECT_ROOT/controller' && go build -ldflags='$ldflags' -o '$PROJECT_ROOT/target/release/hcvctl' ./cmd/hcvctl"

    if ! $DRY_RUN; then
        info "Build artifacts:"
        ls -lh "$PROJECT_ROOT/target/release/hcv-controller" \
               "$PROJECT_ROOT/target/release/hcvctl" \
               "$PROJECT_ROOT/target/release/libvmcore.a" 2>/dev/null || true
    fi
}

# ── Generate systemd unit ────────────────────────────────────────────────────
generate_systemd_unit() {
    local unit_file="$PROJECT_ROOT/target/release/hardcorevisor.service"
    if $DRY_RUN; then
        dry "Generate systemd unit: $unit_file"
        return
    fi

    cat > "$unit_file" <<'UNIT'
[Unit]
Description=HardCoreVisor Controller (REST + gRPC)
Documentation=https://github.com/hardcoremonk/hardcorevisor
After=network-online.target etcd.service
Wants=network-online.target
Requires=etcd.service

[Service]
Type=simple
User=hcv
Group=hcv
ExecStart=/usr/local/bin/hcv-controller
WorkingDirectory=/etc/hardcorevisor
Restart=on-failure
RestartSec=5
StartLimitBurst=5
StartLimitIntervalSec=60

# Configuration
EnvironmentFile=-/etc/hardcorevisor/hcv.env

# Security hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/lib/hardcorevisor /var/log/hardcorevisor
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
MemoryDenyWriteExecute=false
LockPersonality=true

# Resource limits
LimitNOFILE=65536
LimitNPROC=4096
LimitMEMLOCK=infinity

# Logging
StandardOutput=journal
StandardError=journal
SyslogIdentifier=hardcorevisor

[Install]
WantedBy=multi-user.target
UNIT
    info "Generated systemd unit: $unit_file"
}

# ── Transfer files to remote ─────────────────────────────────────────────────
transfer_files() {
    step "Transferring files to $TARGET_HOST"

    local staging="/tmp/hcv-deploy-$$"
    run "mkdir -p '$staging'"

    if ! $DRY_RUN; then
        # Collect files into staging directory
        cp "$PROJECT_ROOT/target/release/hcv-controller" "$staging/"
        cp "$PROJECT_ROOT/target/release/hcvctl" "$staging/"
        cp "$PROJECT_ROOT/target/release/hardcorevisor.service" "$staging/"
        cp "$PROJECT_ROOT/deploy/hcv-production.yaml" "$staging/hcv.yaml"
        cp "$PROJECT_ROOT/docs/openapi.yaml" "$staging/" 2>/dev/null || true

        # Transfer TLS certs if they exist
        if [[ -d "$PROJECT_ROOT/deploy/tls" ]]; then
            cp -r "$PROJECT_ROOT/deploy/tls" "$staging/tls"
        fi
    fi

    if command -v rsync &>/dev/null; then
        info "Using rsync for transfer ..."
        run "rsync -avz --progress -e 'ssh $SSH_OPTS' '$staging/' '$TARGET_HOST:/tmp/hcv-deploy/'"
    else
        info "Using scp for transfer ..."
        run "scp $SSH_OPTS -r '$staging/' '$TARGET_HOST:/tmp/hcv-deploy/'"
    fi

    run "rm -rf '$staging'"
}

# ── Install on remote host ────────────────────────────────────────────────────
install_remote() {
    step "Installing on $TARGET_HOST"

    remote "sudo bash -s" <<'REMOTE_SCRIPT'
set -euo pipefail

DEPLOY_DIR="/tmp/hcv-deploy"
BIN_DIR="/usr/local/bin"
CONF_DIR="/etc/hardcorevisor"
TLS_DIR="/etc/hardcorevisor/tls"
DATA_DIR="/var/lib/hardcorevisor"
LOG_DIR="/var/log/hardcorevisor"
SERVICE_USER="hcv"

echo "[REMOTE] Creating service user if needed ..."
if ! id "$SERVICE_USER" &>/dev/null; then
    useradd --system --no-create-home --shell /usr/sbin/nologin "$SERVICE_USER"
    echo "[REMOTE] Created user: $SERVICE_USER"
else
    echo "[REMOTE] User $SERVICE_USER already exists."
fi

echo "[REMOTE] Creating directories ..."
mkdir -p "$CONF_DIR" "$TLS_DIR" "$DATA_DIR" "$LOG_DIR"

echo "[REMOTE] Installing binaries ..."
install -m 0755 "$DEPLOY_DIR/hcv-controller" "$BIN_DIR/hcv-controller"
install -m 0755 "$DEPLOY_DIR/hcvctl" "$BIN_DIR/hcvctl"

echo "[REMOTE] Installing configuration ..."
if [[ ! -f "$CONF_DIR/hcv.yaml" ]]; then
    install -m 0640 "$DEPLOY_DIR/hcv.yaml" "$CONF_DIR/hcv.yaml"
    echo "[REMOTE] Installed fresh hcv.yaml"
else
    install -m 0640 "$DEPLOY_DIR/hcv.yaml" "$CONF_DIR/hcv.yaml.new"
    echo "[REMOTE] Config exists; new version saved as hcv.yaml.new"
fi

if [[ -f "$DEPLOY_DIR/openapi.yaml" ]]; then
    install -m 0644 "$DEPLOY_DIR/openapi.yaml" "$CONF_DIR/openapi.yaml"
fi

# Install TLS certificates if provided
if [[ -d "$DEPLOY_DIR/tls" ]]; then
    echo "[REMOTE] Installing TLS certificates ..."
    cp "$DEPLOY_DIR/tls/"*.pem "$TLS_DIR/" 2>/dev/null || true
    chmod 0640 "$TLS_DIR/"*.pem 2>/dev/null || true
fi

# Create environment file template if not present
if [[ ! -f "$CONF_DIR/hcv.env" ]]; then
    cat > "$CONF_DIR/hcv.env" <<'ENV'
# HardCoreVisor environment overrides
# HCV_JWT_SECRET=<set-a-random-64-char-string>
# HCV_ETCD_ENDPOINTS=localhost:2379
# HCV_LOG_LEVEL=info
# HCV_LOG_FORMAT=json
# HCV_TLS_CERT=/etc/hardcorevisor/tls/server.pem
# HCV_TLS_KEY=/etc/hardcorevisor/tls/server-key.pem
ENV
    echo "[REMOTE] Created template hcv.env"
fi

echo "[REMOTE] Setting ownership ..."
chown -R "$SERVICE_USER:$SERVICE_USER" "$CONF_DIR" "$DATA_DIR" "$LOG_DIR"

echo "[REMOTE] Installing systemd service ..."
install -m 0644 "$DEPLOY_DIR/hardcorevisor.service" /etc/systemd/system/hardcorevisor.service
systemctl daemon-reload

echo "[REMOTE] Enabling and starting service ..."
systemctl enable hardcorevisor.service
systemctl restart hardcorevisor.service

echo "[REMOTE] Waiting for service to start ..."
sleep 2
if systemctl is-active --quiet hardcorevisor.service; then
    echo "[REMOTE] HardCoreVisor is running."
    systemctl status hardcorevisor.service --no-pager || true
else
    echo "[REMOTE] WARNING: Service failed to start. Check logs:"
    echo "  journalctl -u hardcorevisor -n 50 --no-pager"
fi

echo "[REMOTE] Cleaning up staging ..."
rm -rf "$DEPLOY_DIR"

echo "[REMOTE] Deployment complete."
REMOTE_SCRIPT
}

# ── Docker Compose deployment ─────────────────────────────────────────────────
deploy_docker() {
    step "Docker Compose deployment to $TARGET_HOST"

    local staging="/tmp/hcv-docker-deploy-$$"
    run "mkdir -p '$staging'"

    if ! $DRY_RUN; then
        # Collect Docker deployment files
        cp "$PROJECT_ROOT/deploy/docker-compose.yml" "$staging/"
        cp "$PROJECT_ROOT/deploy/docker-compose.prod.yml" "$staging/"
        cp "$PROJECT_ROOT/deploy/Dockerfile.controller" "$staging/"
        cp "$PROJECT_ROOT/deploy/prometheus.yml" "$staging/"
        cp "$PROJECT_ROOT/deploy/alert-rules.yml" "$staging/"
        cp "$PROJECT_ROOT/deploy/alertmanager.yml" "$staging/" 2>/dev/null || true
        cp "$PROJECT_ROOT/deploy/loki-config.yaml" "$staging/" 2>/dev/null || true
        cp "$PROJECT_ROOT/deploy/promtail-config.yaml" "$staging/" 2>/dev/null || true
        cp -r "$PROJECT_ROOT/deploy/grafana" "$staging/" 2>/dev/null || true

        # Copy TLS certs if available
        if [[ -d "$PROJECT_ROOT/deploy/tls" ]]; then
            cp -r "$PROJECT_ROOT/deploy/tls" "$staging/tls"
        fi

        # Copy production .env if it exists, otherwise create from example
        if [[ -f "$PROJECT_ROOT/deploy/.env" ]]; then
            cp "$PROJECT_ROOT/deploy/.env" "$staging/.env"
        elif [[ -f "$PROJECT_ROOT/deploy/.env.example" ]]; then
            cp "$PROJECT_ROOT/deploy/.env.example" "$staging/.env"
            warn ".env created from .env.example -- edit secrets before starting!"
        fi

        # Copy the full source context needed for Dockerfile build
        # We tar the controller + Cargo workspace for the build context
        tar -czf "$staging/source.tar.gz" \
            -C "$PROJECT_ROOT" \
            controller/ Cargo.toml Cargo.lock vmcore/ 2>/dev/null || true
    fi

    info "Transferring Docker deployment files ..."
    if command -v rsync &>/dev/null; then
        run "rsync -avz --progress -e 'ssh $SSH_OPTS' '$staging/' '$TARGET_HOST:/opt/hardcorevisor/'"
    else
        run "scp $SSH_OPTS -r '$staging/' '$TARGET_HOST:/opt/hardcorevisor/'"
    fi

    run "rm -rf '$staging'"

    info "Starting Docker Compose stack on remote ..."
    remote "cd /opt/hardcorevisor && docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d"

    info "Docker Compose deployment complete."
    info "Check status: ssh $TARGET_HOST 'cd /opt/hardcorevisor && docker compose ps'"
}

# ── Main ──────────────────────────────────────────────────────────────────────
main() {
    echo ""
    echo "============================================"
    echo "  HardCoreVisor Production Deployment"
    echo "============================================"
    echo "  Target:      $TARGET_HOST"
    echo "  Mode:        $(if $DOCKER_MODE; then echo "Docker Compose"; else echo "Bare-metal (systemd)"; fi)"
    echo "  Dry run:     $DRY_RUN"
    echo "============================================"
    echo ""

    preflight

    if $DOCKER_MODE; then
        deploy_docker
    else
        build_release
        generate_systemd_unit
        transfer_files
        install_remote
    fi

    echo ""
    info "Deployment finished successfully."
    info "Verify with: ssh $TARGET_HOST 'curl -s http://localhost:18080/healthz | jq'"
}

main
