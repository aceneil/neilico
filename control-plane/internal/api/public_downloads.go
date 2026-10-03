package api

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const installScript = `#!/bin/sh
set -eu

TOKEN=""
NAME=""
SERVER=""
DRY_RUN=0

fail() {
    printf 'neilico install: %s\n' "$1" >&2
    exit 1
}

need_value() {
    [ "$#" -ge 2 ] || fail "$1 requires a value"
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --token)
            need_value "$@"
            TOKEN=$2
            shift 2
            ;;
        --token=*)
            TOKEN=${1#--token=}
            shift
            ;;
        --name)
            need_value "$@"
            NAME=$2
            shift 2
            ;;
        --name=*)
            NAME=${1#--name=}
            shift
            ;;
        --server)
            need_value "$@"
            SERVER=$2
            shift 2
            ;;
        --server=*)
            SERVER=${1#--server=}
            shift
            ;;
        --dry-run)
            DRY_RUN=1
            shift
            ;;
        *)
            fail "unknown option: $1"
            ;;
    esac
done

[ -n "$TOKEN" ] || fail "--token is required"
if [ -n "$NAME" ] && printf '%s' "$NAME" | LC_ALL=C grep -q '[[:cntrl:]]'; then
    fail "--name must not contain control characters"
fi

token_server() {
    token_payload=${1#*.}
    token_payload=${token_payload%%.*}
    token_json=""
    normalized=$(printf '%s' "$token_payload" | tr '_-' '/+')
    case $((${#normalized} % 4)) in
        2) normalized="${normalized}==" ;;
        3) normalized="${normalized}=" ;;
    esac
    if command -v base64 >/dev/null 2>&1; then
        token_json=$(printf '%s' "$normalized" | base64 -d 2>/dev/null || true)
        if [ -z "$token_json" ]; then
            token_json=$(printf '%s' "$normalized" | base64 -D 2>/dev/null || true)
        fi
    fi
    printf '%s' "$token_json" | sed -n 's/.*"srv":"\([^"]*\)".*/\1/p'
}

if [ -z "$SERVER" ]; then
    SERVER=$(token_server "$TOKEN")
fi
[ -n "$SERVER" ] || fail "cannot read server from token; pass --server https://control.example.com"
SERVER=${SERVER%/}

case "$(uname -s)" in
    Linux) ;;
    *) fail "this installer supports Linux only" ;;
esac

case "$(uname -m)" in
    x86_64|amd64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    armv7l|armv7|armhf) ARCH=armv7 ;;
    *) fail "unsupported architecture: $(uname -m)" ;;
esac

BINARY_NAME="neilico-agent-linux-${ARCH}"
DOWNLOAD_URL="${SERVER}/downloads/${BINARY_NAME}"
INSTALL_PATH="/usr/local/bin/neilico-agent"
TOKEN_PATH="/etc/neilico/agent.token"
SERVICE_PATH="/etc/systemd/system/neilico-agent.service"
STATE_DIR="/var/lib/neilico-agent"

sha256_file() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print $1}'
    else
        fail "sha256sum or shasum is required"
    fi
}

if [ "$DRY_RUN" -eq 1 ]; then
    printf 'DRY-RUN: would detect architecture %s\n' "$ARCH"
    printf 'DRY-RUN: would download %s\n' "$DOWNLOAD_URL"
    printf 'DRY-RUN: would verify X-Neilico-Sha256 with sha256\n'
    printf 'DRY-RUN: would install binary to %s (mode 0755)\n' "$INSTALL_PATH"
    printf 'DRY-RUN: would write %s (mode 0600, contents redacted)\n' "$TOKEN_PATH"
    printf 'DRY-RUN: would write systemd unit %s\n' "$SERVICE_PATH"
    printf 'DRY-RUN: would run: systemctl daemon-reload\n'
    printf 'DRY-RUN: would run: systemctl enable --now neilico-agent\n'
    printf 'DRY-RUN: would run: systemctl restart neilico-agent\n'
    exit 0
fi

command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v systemctl >/dev/null 2>&1 || fail "systemctl is required"

TMP_DIR=$(mktemp -d)
cleanup() {
    rm -rf "$TMP_DIR"
}
trap cleanup EXIT INT TERM
BINARY_TMP="${TMP_DIR}/${BINARY_NAME}"
HEADER_TMP="${TMP_DIR}/headers"
curl -fsSL -D "$HEADER_TMP" -o "$BINARY_TMP" "$DOWNLOAD_URL"

EXPECTED=$(awk 'BEGIN { IGNORECASE=1 } /^X-Neilico-Sha256:/ { gsub("\r", "", $2); print tolower($2) }' "$HEADER_TMP" | tail -n 1)
[ -n "$EXPECTED" ] || fail "download response omitted X-Neilico-Sha256"
ACTUAL=$(sha256_file "$BINARY_TMP")
[ "$ACTUAL" = "$EXPECTED" ] || fail "download checksum mismatch"

install -d -m 0755 /usr/local/bin /etc/neilico "$STATE_DIR"
install -m 0755 "$BINARY_TMP" "$INSTALL_PATH"

TOKEN_TMP=$(mktemp /etc/neilico/.agent.token.XXXXXX)
printf '%s\n' "$TOKEN" > "$TOKEN_TMP"
chown root:root "$TOKEN_TMP"
chmod 0600 "$TOKEN_TMP"
mv "$TOKEN_TMP" "$TOKEN_PATH"

NAME_ENV=""
if [ -n "$NAME" ]; then
    ESCAPED_NAME=$(printf '%s' "$NAME" | sed 's/\\/\\\\/g; s/"/\\"/g')
    NAME_ENV="Environment=\"NEILICO_AGENT_NODE_NAME=${ESCAPED_NAME}\""
fi
cat > "$SERVICE_PATH" <<UNIT
[Unit]
Description=NEILICO Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/neilico-agent run --token-file /etc/neilico/agent.token
Environment=NEILICO_STATE_DIR=/var/lib/neilico-agent
${NAME_ENV}
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
UNIT
chmod 0644 "$SERVICE_PATH"

systemctl daemon-reload
systemctl enable --now neilico-agent
systemctl restart neilico-agent
printf 'NEILICO Agent installed and started.\n'
`

func (s *Server) registerPublicDownloads(mux *http.ServeMux) {
	mux.HandleFunc("/install.sh", s.handleInstallScript)
	mux.HandleFunc("/downloads/{filename}", s.handleAgentDownload)
}

func (s *Server) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		s.methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	w.Header().Set("Content-Type", "text/x-shellscript")
	w.Header().Set("Content-Disposition", `inline; filename="install.sh"`)
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.WriteString(w, installScript)
}

func (s *Server) handleAgentDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		s.methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	filename := r.PathValue("filename")
	if !validAgentDownloadName(filename) {
		writeError(w, http.StatusNotFound, "not_found", "download not found")
		return
	}
	path := filepath.Join(s.downloadsDir, filename)
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "not_found", "download not found")
			return
		}
		s.internalError(w, err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		writeError(w, http.StatusNotFound, "not_found", "download not found")
		return
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		s.internalError(w, err)
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		s.internalError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("X-Neilico-Sha256", hex.EncodeToString(hash.Sum(nil)))
	http.ServeContent(w, r, filename, info.ModTime(), file)
}

func validAgentDownloadName(filename string) bool {
	switch filename {
	case "neilico-agent-linux-amd64",
		"neilico-agent-linux-arm64",
		"neilico-agent-linux-armv7",
		"neilico-agent-darwin-amd64",
		"neilico-agent-darwin-arm64":
		return true
	default:
		return false
	}
}
