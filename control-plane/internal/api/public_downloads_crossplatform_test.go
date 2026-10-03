package api_test

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"neilico/control-plane/internal/api"
)

func TestInstallPowerShellScript(t *testing.T) {
	app := newTestAppWithOptions(t, api.ProxyOptions{
		Enabled: true, Kind: "builtin", Listen: "127.0.0.1:0",
		Downloads: api.DownloadsOptions{Dir: t.TempDir()},
	})
	defer app.server.Close()

	response, err := app.server.Client().Get(app.server.URL + "/install.ps1")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("/install.ps1 status = %d", response.StatusCode)
	}
	if got := response.Header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("/install.ps1 content type = %q", got)
	}
	scriptBytes, err := readAll(response)
	if err != nil {
		t.Fatal(err)
	}
	script := string(scriptBytes)
	for _, expected := range []string{
		"param(", "[Parameter(Mandatory = $true)]", "-DryRun", "Test-Administrator",
		"neilico-agent-windows-amd64.exe", "X-Neilico-Sha256", "Protect-TokenFile",
		"New-Service", "Start-Service", "checksum mismatch",
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("install.ps1 omitted %q", expected)
		}
	}

	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("PowerShell parser unavailable locally; manager will run [Parser]::ParseFile")
	}
	scriptPath := filepath.Join(t.TempDir(), "install.ps1")
	if err := os.WriteFile(scriptPath, scriptBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	parse := `$tokens = $null; $parseErrors = $null; [System.Management.Automation.Language.Parser]::ParseFile($env:NEILICO_PARSER_FILE, [ref]$tokens, [ref]$parseErrors) > $null; if ($parseErrors.Count -gt 0) { $parseErrors | ForEach-Object { Write-Error $_ }; exit 1 }`
	command := exec.Command(pwsh, "-NoProfile", "-Command", parse)
	command.Env = append(os.Environ(), "NEILICO_PARSER_FILE="+scriptPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("PowerShell parser failed: %v\n%s", err, output)
	}

	command = exec.Command(pwsh, "-NoProfile", "-File", scriptPath, "-DryRun", "-Token", "neilico-enroll.test.test", "-Server", "https://control.example.test")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("install.ps1 -DryRun failed: %v\n%s", err, output)
	}
	for _, expected := range []string{"DRY-RUN", "would download", "would verify", "would write", "New-Service", "Start-Service"} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("install.ps1 -DryRun omitted %q:\n%s", expected, output)
		}
	}
}

func TestInstallScriptDarwinDryRunWithShims(t *testing.T) {
	app := newTestAppWithOptions(t, api.ProxyOptions{
		Enabled: true, Kind: "builtin", Listen: "127.0.0.1:0",
		Downloads: api.DownloadsOptions{Dir: t.TempDir()},
	})
	defer app.server.Close()
	response, err := app.server.Client().Get(app.server.URL + "/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	scriptBytes, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(t.TempDir(), "install.sh")
	if err := os.WriteFile(scriptPath, scriptBytes, 0o700); err != nil {
		t.Fatal(err)
	}

	shimDir := t.TempDir()
	marker := filepath.Join(shimDir, "launchctl-invoked")
	shims := map[string]string{
		"uname":     "#!/bin/sh\ncase \"$1\" in\n  -s) printf '%s\\n' Darwin ;;\n  -m) printf '%s\\n' arm64 ;;\n  *) printf '%s\\n' Darwin ;;\nesac\n",
		"sw_vers":   "#!/bin/sh\nprintf '%s\\n' 14.5\n",
		"launchctl": "#!/bin/sh\n: > \"$NEILICO_MARKER\"\nexit 97\n",
	}
	for name, content := range shims {
		if err := os.WriteFile(filepath.Join(shimDir, name), []byte(content), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	command := exec.Command("bash", scriptPath, "--dry-run", "--server", "https://control.example.test", "--token", "neilico-enroll.test.test", "--name", "edge-01")
	command.Env = append(os.Environ(), "PATH="+shimDir+string(os.PathListSeparator)+os.Getenv("PATH"), "NEILICO_MARKER="+marker)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Darwin dry-run failed: %v\n%s", err, output)
	}
	for _, expected := range []string{
		"detected platform Darwin", "neilico-agent-darwin-arm64", "detected macOS version 14.5",
		"/Library/LaunchDaemons/com.neilico.agent.plist", "launchctl bootstrap system",
		"com.neilico.agent",
	} {
		if !strings.Contains(string(output), expected) {
			t.Fatalf("Darwin dry-run omitted %q:\n%s", expected, output)
		}
	}
	if strings.Contains(string(output), "systemctl") {
		t.Fatalf("Darwin dry-run took Linux branch:\n%s", output)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("Darwin dry-run invoked launchctl: %v", err)
	}
}

func TestInstallScriptRejectsUnsupportedPlatform(t *testing.T) {
	scriptPath := filepath.Join(t.TempDir(), "install.sh")
	app := newTestApp(t)
	defer app.server.Close()
	getResponse, err := app.server.Client().Get(app.server.URL + "/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	scriptBytes, err := io.ReadAll(getResponse.Body)
	getResponse.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scriptPath, scriptBytes, 0o700); err != nil {
		t.Fatal(err)
	}
	shimDir := t.TempDir()
	uname := "#!/bin/sh\nprintf '%s\\n' SunOS\n"
	if err := os.WriteFile(filepath.Join(shimDir, "uname"), []byte(uname), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", scriptPath, "--dry-run", "--server", "https://control.example.test", "--token", "neilico-enroll.test.test")
	command.Env = append(os.Environ(), "PATH="+shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("unsupported platform unexpectedly succeeded:\n%s", output)
	}
	if !strings.Contains(string(output), "supports Linux and macOS") || !strings.Contains(string(output), "SunOS") {
		t.Fatalf("unsupported-platform error was unclear:\n%s", output)
	}
}

func TestAgentDownloadAllowlistServesSixArtifacts(t *testing.T) {
	downloadDir := t.TempDir()
	app := newTestAppWithOptions(t, api.ProxyOptions{
		Enabled: true, Kind: "builtin", Listen: "127.0.0.1:0",
		Downloads: api.DownloadsOptions{Dir: downloadDir},
	})
	defer app.server.Close()

	names := []string{
		"neilico-agent-linux-amd64",
		"neilico-agent-linux-arm64",
		"neilico-agent-linux-armv7",
		"neilico-agent-darwin-amd64",
		"neilico-agent-darwin-arm64",
		"neilico-agent-windows-amd64.exe",
	}
	for _, name := range names {
		content := []byte("artifact:" + name)
		if err := os.WriteFile(filepath.Join(downloadDir, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
		response, err := app.server.Client().Get(app.server.URL + "/downloads/" + name)
		if err != nil {
			t.Fatal(err)
		}
		payload, err := readAll(response)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d", name, response.StatusCode)
		}
		if string(payload) != string(content) {
			t.Fatalf("%s payload = %q", name, payload)
		}
		sum := sha256.Sum256(content)
		if got := response.Header.Get("X-Neilico-Sha256"); got != hex.EncodeToString(sum[:]) {
			t.Fatalf("%s checksum = %q", name, got)
		}
	}

	status, body := mustRequest(t, app.server, http.MethodGet, "/downloads/neilico-agent-plan9-mips", "", nil)
	requireStatus(t, status, http.StatusNotFound)
	if strings.Contains(strings.ToLower(string(body)), "<html") {
		t.Fatalf("unknown download returned HTML: %s", body)
	}
}

func readAll(response *http.Response) ([]byte, error) {
	defer response.Body.Close()
	return io.ReadAll(response.Body)
}
