#!/usr/bin/env bash
# gen-tls-certs.sh — Generate self-signed CA + server TLS certificates.
#
# Output:
#   deploy/tls/ca.pem           CA certificate
#   deploy/tls/ca-key.pem       CA private key (keep secret)
#   deploy/tls/server.pem       Server certificate
#   deploy/tls/server-key.pem   Server private key
#
# SAN: localhost, 127.0.0.1, 192.168.3.50, *.hardcorevisor.local
# Validity: 365 days

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
TLS_DIR="$PROJECT_ROOT/deploy/tls"

DAYS=365
KEY_BITS=4096
CA_SUBJECT="/C=KR/ST=Seoul/O=HardCoreVisor/CN=HardCoreVisor CA"
SERVER_SUBJECT="/C=KR/ST=Seoul/O=HardCoreVisor/CN=hardcorevisor.local"

GREEN='\033[0;32m'
CYAN='\033[0;36m'
NC='\033[0m'

info() { echo -e "${GREEN}[INFO]${NC}  $*"; }
step() { echo -e "${CYAN}[STEP]${NC}  $*"; }

# ── Pre-flight ────────────────────────────────────────────────────────────────
if ! command -v openssl &>/dev/null; then
    echo "ERROR: openssl is required but not found." >&2
    exit 1
fi

# ── Setup output directory ────────────────────────────────────────────────────
mkdir -p "$TLS_DIR"

# Warn if certs already exist
if [[ -f "$TLS_DIR/server.pem" ]]; then
    echo ""
    echo "WARNING: Certificates already exist in $TLS_DIR"
    read -rp "Overwrite? [y/N] " answer
    if [[ "$answer" != "y" && "$answer" != "Y" ]]; then
        echo "Aborted."
        exit 0
    fi
fi

echo ""
echo "============================================"
echo "  HardCoreVisor TLS Certificate Generator"
echo "============================================"
echo "  Output:    $TLS_DIR"
echo "  Validity:  $DAYS days"
echo "  Key size:  $KEY_BITS bits"
echo "============================================"
echo ""

# ── Step 1: Generate CA ──────────────────────────────────────────────────────
step "Generating CA private key ..."
openssl genrsa -out "$TLS_DIR/ca-key.pem" "$KEY_BITS" 2>/dev/null

step "Generating CA certificate ..."
openssl req -new -x509 \
    -key "$TLS_DIR/ca-key.pem" \
    -out "$TLS_DIR/ca.pem" \
    -days "$DAYS" \
    -subj "$CA_SUBJECT" \
    -sha256

# ── Step 2: Generate server key + CSR ────────────────────────────────────────
step "Generating server private key ..."
openssl genrsa -out "$TLS_DIR/server-key.pem" "$KEY_BITS" 2>/dev/null

step "Generating server CSR ..."

# Create OpenSSL config with SAN
OPENSSL_CNF=$(mktemp)
trap "rm -f '$OPENSSL_CNF'" EXIT

cat > "$OPENSSL_CNF" <<EOF
[req]
default_bits       = $KEY_BITS
distinguished_name = req_dn
req_extensions     = v3_req
prompt             = no

[req_dn]
C  = KR
ST = Seoul
O  = HardCoreVisor
CN = hardcorevisor.local

[v3_req]
basicConstraints     = CA:FALSE
keyUsage             = digitalSignature, keyEncipherment
extendedKeyUsage     = serverAuth, clientAuth
subjectAltName       = @alt_names

[alt_names]
DNS.1 = localhost
DNS.2 = hardcorevisor.local
DNS.3 = *.hardcorevisor.local
IP.1  = 127.0.0.1
IP.2  = 192.168.3.50
EOF

openssl req -new \
    -key "$TLS_DIR/server-key.pem" \
    -out "$TLS_DIR/server.csr" \
    -config "$OPENSSL_CNF"

# ── Step 3: Sign server certificate with CA ──────────────────────────────────
step "Signing server certificate with CA ..."

# Create extensions file for signing
EXT_CNF=$(mktemp)
trap "rm -f '$OPENSSL_CNF' '$EXT_CNF'" EXIT

cat > "$EXT_CNF" <<EOF
basicConstraints     = CA:FALSE
keyUsage             = digitalSignature, keyEncipherment
extendedKeyUsage     = serverAuth, clientAuth
subjectAltName       = @alt_names

[alt_names]
DNS.1 = localhost
DNS.2 = hardcorevisor.local
DNS.3 = *.hardcorevisor.local
IP.1  = 127.0.0.1
IP.2  = 192.168.3.50
EOF

openssl x509 -req \
    -in "$TLS_DIR/server.csr" \
    -CA "$TLS_DIR/ca.pem" \
    -CAkey "$TLS_DIR/ca-key.pem" \
    -CAcreateserial \
    -out "$TLS_DIR/server.pem" \
    -days "$DAYS" \
    -sha256 \
    -extfile "$EXT_CNF"

# ── Cleanup intermediate files ────────────────────────────────────────────────
rm -f "$TLS_DIR/server.csr" "$TLS_DIR/ca.srl"

# ── Set permissions ───────────────────────────────────────────────────────────
chmod 644 "$TLS_DIR/ca.pem" "$TLS_DIR/server.pem"
chmod 600 "$TLS_DIR/ca-key.pem" "$TLS_DIR/server-key.pem"

# ── Verify ────────────────────────────────────────────────────────────────────
step "Verifying certificate chain ..."
openssl verify -CAfile "$TLS_DIR/ca.pem" "$TLS_DIR/server.pem"

echo ""
info "TLS certificates generated successfully:"
echo ""
ls -la "$TLS_DIR/"
echo ""

step "Certificate details:"
openssl x509 -in "$TLS_DIR/server.pem" -noout -subject -issuer -dates -ext subjectAltName 2>/dev/null || \
    openssl x509 -in "$TLS_DIR/server.pem" -noout -subject -issuer -dates

echo ""
info "To use with HardCoreVisor:"
echo "  export HCV_TLS_CERT=$TLS_DIR/server.pem"
echo "  export HCV_TLS_KEY=$TLS_DIR/server-key.pem"
echo ""
info "To trust the CA on clients:"
echo "  sudo cp $TLS_DIR/ca.pem /usr/local/share/ca-certificates/hardcorevisor-ca.crt"
echo "  sudo update-ca-certificates"
