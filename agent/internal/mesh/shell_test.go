package mesh

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestShellApplierCommandGolden(t *testing.T) {
	content, err := os.ReadFile("testdata/wireguard.conf")
	if err != nil {
		t.Fatal(err)
	}
	applier := NewShellApplier(&recordingExecutor{}, t.TempDir())
	commands, err := applier.Plan(Config{
		Interface:       "wg0",
		MTU:             1420,
		ListenPort:      51820,
		WireGuardConfig: string(content),
	})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	for _, command := range commands {
		output.WriteString(command.String())
		output.WriteByte('\n')
	}
	goldenPath := filepath.Join("testdata", "shell-commands.golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(goldenPath, output.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output.Bytes(), want) {
		t.Fatalf("command list differs:\ngot:\n%s\nwant:\n%s", output.Bytes(), want)
	}
}

func TestShellApplyUsesSecureTemporaryConfig(t *testing.T) {
	executor := &recordingExecutor{}
	tempDir := t.TempDir()
	applier := NewShellApplier(executor, tempDir)
	config := Config{Interface: "wg0", MTU: 1420, WireGuardConfig: "[Interface]\nPrivateKey = LOCAL_PRIVATE_KEY\n"}
	if err := applier.Apply(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, command := range executor.commands {
		if command.Name == "wg" {
			found = true
			if len(command.Args) != 3 || command.Args[0] != "setconf" {
				t.Fatalf("unexpected wg command: %#v", command)
			}
			info, err := os.Stat(command.Args[2])
			if !os.IsNotExist(err) {
				if err == nil && info.Mode().Perm() != 0o600 {
					t.Fatalf("temporary config mode = %o", info.Mode().Perm())
				}
			}
		}
	}
	if !found {
		t.Fatal("wg setconf was not executed")
	}
}
