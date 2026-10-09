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

OS_NAME=$(uname -s)
case "$OS_NAME" in
    Linux|Darwin) ;;
    *) fail "this installer supports Linux and macOS (Darwin); detected $OS_NAME" ;;
esac

MACHINE=$(uname -m)
case "$OS_NAME:$MACHINE" in
    Linux:x86_64|Linux:amd64) ARCH=amd64 ;;
    Linux:aarch64|Linux:arm64) ARCH=arm64 ;;
    Linux:armv7l|Linux:armv7|Linux:armhf) ARCH=armv7 ;;
    Darwin:x86_64|Darwin:amd64) ARCH=amd64 ;;
    Darwin:arm64|Darwin:aarch64) ARCH=arm64 ;;
    *) fail "unsupported architecture for $OS_NAME: $MACHINE" ;;
esac

if [ "$OS_NAME" = "Darwin" ]; then
    PLATFORM=macos
    BINARY_NAME="neilico-agent-darwin-${ARCH}"
else
    PLATFORM=linux
    BINARY_NAME="neilico-agent-linux-${ARCH}"
fi
DOWNLOAD_URL="${SERVER}/downloads/${BINARY_NAME}"
INSTALL_PATH="/usr/local/bin/neilico-agent"
TOKEN_PATH="/etc/neilico/agent.token"
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

if [ "$PLATFORM" = "macos" ]; then
    SERVICE_PATH="/Library/LaunchDaemons/com.neilico.agent.plist"
    SERVICE_LABEL="com.neilico.agent"
    LOG_PATH="/var/log/neilico-agent.log"
    MACOS_VERSION=$(sw_vers -productVersion 2>/dev/null || true)
    [ -n "$MACOS_VERSION" ] || MACOS_VERSION="10.15"
    MACOS_MAJOR=${MACOS_VERSION%%.*}
    MACOS_MINOR=${MACOS_VERSION#*.}
    MACOS_MINOR=${MACOS_MINOR%%.*}
    case "$MACOS_MAJOR:$MACOS_MINOR" in
        *[!0-9:]*|:*) MACOS_MAJOR=10; MACOS_MINOR=15 ;;
    esac
    if [ "$MACOS_MAJOR" -gt 10 ] || { [ "$MACOS_MAJOR" -eq 10 ] && [ "$MACOS_MINOR" -ge 11 ]; }; then
        LAUNCHCTL_COMMAND="launchctl bootstrap system ${SERVICE_PATH}"
    else
        LAUNCHCTL_COMMAND="launchctl load -w ${SERVICE_PATH}"
    fi
fi

if [ "$DRY_RUN" -eq 1 ]; then
    printf 'DRY-RUN: detected platform %s\n' "$OS_NAME"
    printf 'DRY-RUN: would detect architecture %s\n' "$ARCH"
    printf 'DRY-RUN: would download %s\n' "$DOWNLOAD_URL"
    printf 'DRY-RUN: would verify X-Neilico-Sha256 with sha256\n'
    printf 'DRY-RUN: would install binary to %s (mode 0755)\n' "$INSTALL_PATH"
    printf 'DRY-RUN: would write %s (mode 0600, contents redacted)\n' "$TOKEN_PATH"
    if [ "$PLATFORM" = "macos" ]; then
        printf 'DRY-RUN: detected macOS version %s\n' "$MACOS_VERSION"
        printf 'DRY-RUN: would write launchd plist %s\n' "$SERVICE_PATH"
        printf 'DRY-RUN: would run: %s\n' "$LAUNCHCTL_COMMAND"
        printf 'DRY-RUN: would run: launchctl kickstart -k system/%s\n' "$SERVICE_LABEL"
    else
        printf 'DRY-RUN: would write systemd unit /etc/systemd/system/neilico-agent.service\n'
        printf 'DRY-RUN: would run: systemctl daemon-reload\n'
        printf 'DRY-RUN: would run: systemctl enable --now neilico-agent\n'
        printf 'DRY-RUN: would run: systemctl restart neilico-agent\n'
    fi
    exit 0
fi

command -v curl >/dev/null 2>&1 || fail "curl is required"
if [ "$PLATFORM" = "macos" ]; then
    [ "$(id -u)" -eq 0 ] || fail "macOS installation requires sudo/root privileges"
    command -v launchctl >/dev/null 2>&1 || fail "launchctl is required"
else
    command -v systemctl >/dev/null 2>&1 || fail "systemctl is required"
fi

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
if [ "$PLATFORM" = "macos" ]; then
    chown root:wheel "$TOKEN_TMP"
else
    chown root:root "$TOKEN_TMP"
fi
chmod 0600 "$TOKEN_TMP"
mv "$TOKEN_TMP" "$TOKEN_PATH"

if [ "$PLATFORM" = "macos" ]; then
    xml_escape() {
        printf '%s' "$1" | sed 's/&/\&amp;/g; s/</\&lt;/g; s/>/\&gt;/g; s/"/\&quot;/g; s/'"'"'/\&apos;/g'
    }
    ESCAPED_NAME=$(xml_escape "$NAME")
    NAME_XML=""
    if [ -n "$ESCAPED_NAME" ]; then
        NAME_XML="<string>--name</string><string>${ESCAPED_NAME}</string>"
    fi
    install -d -m 0755 /var/log
    touch "$LOG_PATH"
    cat > "$SERVICE_PATH" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key><string>com.neilico.agent</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/neilico-agent</string>
        <string>run</string>
        <string>--token-file</string><string>/etc/neilico/agent.token</string>
        <string>--state-dir</string><string>/var/lib/neilico-agent</string>
        ${NAME_XML}
    </array>
    <key>RunAtLoad</key><true/>
    <key>KeepAlive</key><true/>
    <key>StandardOutPath</key><string>/var/log/neilico-agent.log</string>
    <key>StandardErrorPath</key><string>/var/log/neilico-agent.log</string>
</dict>
</plist>
PLIST
    chown root:wheel "$SERVICE_PATH"
    chmod 0644 "$SERVICE_PATH"
    if command -v plutil >/dev/null 2>&1; then
        plutil -lint "$SERVICE_PATH" >/dev/null
    fi
    launchctl bootout system "$SERVICE_PATH" >/dev/null 2>&1 || true
    if [ "$MACOS_MAJOR" -gt 10 ] || { [ "$MACOS_MAJOR" -eq 10 ] && [ "$MACOS_MINOR" -ge 11 ]; }; then
        if ! launchctl bootstrap system "$SERVICE_PATH"; then
            launchctl load -w "$SERVICE_PATH"
        fi
    else
        launchctl load -w "$SERVICE_PATH"
    fi
    launchctl kickstart -k "system/${SERVICE_LABEL}" >/dev/null 2>&1 || launchctl restart "$SERVICE_LABEL" >/dev/null 2>&1 || true
    printf 'NEILICO Agent installed and started with launchd.\n'
    exit 0
fi

NAME_ENV=""
if [ -n "$NAME" ]; then
    ESCAPED_NAME=$(printf '%s' "$NAME" | sed 's/\\/\\\\/g; s/"/\\"/g')
    NAME_ENV="Environment=\"NEILICO_AGENT_NODE_NAME=${ESCAPED_NAME}\""
fi
cat > /etc/systemd/system/neilico-agent.service <<UNIT
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
chmod 0644 /etc/systemd/system/neilico-agent.service

systemctl daemon-reload
systemctl enable --now neilico-agent
systemctl restart neilico-agent
printf 'NEILICO Agent installed and started.\n'
`

const installPowerShellScript = `# NEILICO Agent installer for Windows PowerShell 5.1 and PowerShell 7+.
# Parameters: -Token <TOKEN> [-Name <NODE_NAME>] [-Server <URL>] [-DryRun]
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$Token,
    [string]$Name = $env:COMPUTERNAME,
    [string]$Server,
    [switch]$DryRun
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Write-InstallFailure {
    param([string]$Message)
    [Console]::Error.WriteLine("neilico install: $Message")
    exit 1
}

function Test-Administrator {
    if ($env:OS -ne 'Windows_NT') {
        return $true
    }
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Get-ServerFromToken {
    param([string]$EnrollToken)
    $parts = $EnrollToken.Split('.')
    if ($parts.Count -lt 2 -or [string]::IsNullOrWhiteSpace($parts[1])) {
        Write-InstallFailure 'cannot read server from token; pass -Server https://control.example.com'
    }
    $payload = $parts[1].Replace('-', '+').Replace('_', '/')
    switch ($payload.Length % 4) {
        2 { $payload += '==' }
        3 { $payload += '=' }
    }
    try {
        $json = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($payload)) | ConvertFrom-Json
    }
    catch {
        Write-InstallFailure 'cannot read server from token; pass -Server https://control.example.com'
    }
    if ($null -eq $json.PSObject.Properties['srv'] -or [string]::IsNullOrWhiteSpace([string]$json.srv)) {
        Write-InstallFailure 'cannot read server from token; pass -Server https://control.example.com'
    }
    return [string]$json.srv
}

function Protect-TokenFile {
    param([string]$Path)
    $acl = New-Object Security.AccessControl.FileSecurity
    $acl.SetAccessRuleProtection($true, $false)
    $administrators = New-Object Security.Principal.SecurityIdentifier('S-1-5-32-544')
    $system = New-Object Security.Principal.SecurityIdentifier('S-1-5-18')
    foreach ($sid in @($administrators, $system)) {
        $rule = New-Object Security.AccessControl.FileSystemAccessRule($sid, [Security.AccessControl.FileSystemRights]::FullControl, [Security.AccessControl.AccessControlType]::Allow)
        [void]$acl.AddAccessRule($rule)
    }
    [IO.File]::SetAccessControl($Path, $acl)
}

function Quote-ServiceArgument {
    param([string]$Value)
    return '"' + $Value.Replace('"', '\"') + '"'
}

if ([string]::IsNullOrWhiteSpace($Token)) {
    Write-InstallFailure '-Token is required'
}
if ([string]::IsNullOrWhiteSpace($Name)) {
    $Name = [Environment]::MachineName
}
if ($Name -match '[\x00-\x1F\x7F]') {
    Write-InstallFailure '-Name must not contain control characters'
}
if (-not (Test-Administrator)) {
    Write-InstallFailure 'administrator privileges are required; open PowerShell as Administrator and retry'
}
if ([string]::IsNullOrWhiteSpace($Server)) {
    $Server = Get-ServerFromToken -EnrollToken $Token
}
$Server = $Server.Trim().TrimEnd('/')
$serverUri = $null
$validServer = [Uri]::TryCreate($Server, [UriKind]::Absolute, [ref]$serverUri)
if (-not $validServer -or $serverUri.Scheme -notin @('http', 'https')) {
    Write-InstallFailure '-Server must be an absolute http or https URL'
}

$runningOnWindows = $env:OS -eq 'Windows_NT'
if ($runningOnWindows) {
    $osArchitecture = $env:PROCESSOR_ARCHITEW6432
    if ([string]::IsNullOrWhiteSpace($osArchitecture)) {
        $osArchitecture = $env:PROCESSOR_ARCHITECTURE
    }
    if ($osArchitecture -ne 'AMD64') {
        Write-InstallFailure "unsupported Windows architecture: $osArchitecture"
    }
}
elseif (-not $DryRun) {
    Write-InstallFailure 'this installer requires Windows'
}

$binaryName = 'neilico-agent-windows-amd64.exe'
$downloadUrl = "$Server/downloads/$binaryName"
$programFiles = $env:ProgramFiles
if ([string]::IsNullOrWhiteSpace($programFiles)) {
    $programFiles = '/Program Files'
}
$programData = $env:ProgramData
if ([string]::IsNullOrWhiteSpace($programData)) {
    $programData = '/ProgramData'
}
$installDir = Join-Path $programFiles 'NEILICO'
$installPath = Join-Path $installDir $binaryName
$tokenPath = Join-Path $installDir 'agent.token'
$stateDir = Join-Path $programData 'NEILICO\Agent'
$serviceName = 'NEILICOAgent'

if ($DryRun) {
    if ($runningOnWindows) {
        Write-Host 'DRY-RUN: detected Windows architecture AMD64'
    }
    else {
        Write-Host 'DRY-RUN: non-Windows execution host; simulating Windows architecture AMD64'
    }
    Write-Host "DRY-RUN: would download $downloadUrl"
    Write-Host 'DRY-RUN: would verify X-Neilico-Sha256 with SHA256'
    Write-Host "DRY-RUN: would install binary to $installPath"
    Write-Host "DRY-RUN: would write $tokenPath with Administrators/System-only ACL (contents redacted)"
    Write-Host "DRY-RUN: would create state directory $stateDir"
    Write-Host "DRY-RUN: would register service $serviceName with New-Service"
    Write-Host "DRY-RUN: would run: Start-Service -Name $serviceName"
    exit 0
}

[void](New-Item -ItemType Directory -Path $installDir -Force)
[void](New-Item -ItemType Directory -Path $stateDir -Force)

$existingService = Get-Service -Name $serviceName -ErrorAction SilentlyContinue
if ($null -ne $existingService) {
    if ($existingService.Status -ne 'Stopped') {
        Stop-Service -Name $serviceName -Force
    }
}

$tempDirectory = [IO.Path]::GetTempPath()
$tempBinary = Join-Path $tempDirectory ("neilico-agent-" + [Guid]::NewGuid().ToString('N') + '.exe')
try {
    $response = Invoke-WebRequest -Uri $downloadUrl -OutFile $tempBinary -UseBasicParsing
    $expected = @($response.Headers['X-Neilico-Sha256'])[0]
    if ([string]::IsNullOrWhiteSpace([string]$expected)) {
        Write-InstallFailure 'download response omitted X-Neilico-Sha256'
    }
    $actual = (Get-FileHash -Algorithm SHA256 -Path $tempBinary).Hash.ToLowerInvariant()
    if ($actual -ne ([string]$expected).ToLowerInvariant()) {
        Write-InstallFailure 'download checksum mismatch'
    }
    Copy-Item -LiteralPath $tempBinary -Destination $installPath -Force
}
finally {
    Remove-Item -LiteralPath $tempBinary -Force -ErrorAction SilentlyContinue
}

[void](New-Item -ItemType File -Path $tokenPath -Force)
Protect-TokenFile -Path $tokenPath
$utf8NoBom = New-Object Text.UTF8Encoding($false)
[IO.File]::WriteAllText($tokenPath, $Token + [Environment]::NewLine, $utf8NoBom)

$serviceArguments = @(
    (Quote-ServiceArgument $installPath),
    'run',
    '--token-file', (Quote-ServiceArgument $tokenPath),
    '--state-dir', (Quote-ServiceArgument $stateDir),
    '--name', (Quote-ServiceArgument $Name)
)
$binaryPath = $serviceArguments -join ' '
if ($null -eq $existingService) {
    New-Service -Name $serviceName -BinaryPathName $binaryPath -DisplayName 'NEILICO Agent' -Description 'NEILICO unified mesh and proxy agent' -StartupType Automatic | Out-Null
}
else {
    & sc.exe config $serviceName binPath= $binaryPath | Out-Null
    if ($LASTEXITCODE -ne 0) {
        Write-InstallFailure 'failed to update the existing NEILICO Agent service'
    }
    Set-Service -Name $serviceName -StartupType Automatic
}
Start-Service -Name $serviceName
Write-Host 'NEILICO Agent installed and started.'
`

func (s *Server) registerPublicDownloads(mux *http.ServeMux) {
	mux.HandleFunc("/install.sh", s.handleInstallScript)
	mux.HandleFunc("/install.ps1", s.handleInstallPowerShellScript)
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

func (s *Server) handleInstallPowerShellScript(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		s.methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `inline; filename="install.ps1"`)
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.WriteString(w, installPowerShellScript)
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

// validAgentDownloadName 是 /downloads/{filename} 的**文件名白名单**：只有在此列出的
// 名称才会被服务，其余一律 404（防目录穿越/任意文件读取）。除内置的 agent 二进制外，
// 这里也放行 NEILICO 自有远程桌面客户端的发布包（由部署机放进下载目录自行分发，
// 见 docs/REMOTE_DESKTOP.md「客户端分发」）。
func validAgentDownloadName(filename string) bool {
	switch filename {
	case "neilico-agent-linux-amd64",
		"neilico-agent-linux-arm64",
		"neilico-agent-linux-armv7",
		"neilico-agent-darwin-amd64",
		"neilico-agent-darwin-arm64",
		"neilico-agent-windows-amd64.exe",
		// NEILICO 远程桌面客户端发布包（当前仅 Windows 已构建，Linux/macOS 预留）。
		"neilico-client-windows-x64.zip",
		"neilico-client-linux-x64.zip",
		"neilico-client-macos-x64.zip",
		// 可选：客户端包校验清单。
		"neilico-client-SHA256SUMS":
		return true
	default:
		return false
	}
}
