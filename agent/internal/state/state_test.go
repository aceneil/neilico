package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveLoadPermissionsAndAtomicReplacement(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "private", "state.json")
	value := State{NodeID: "node-1", AgentToken: "abcdefgh", PrivateKey: "secret-private", PublicKey: "public", AppliedVersion: 3}
	if err := Save(path, value); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state mode = %o, want 600", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("state directory mode = %o, want 700", dirInfo.Mode().Perm())
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("temporary state files were not cleaned: %#v", entries)
	}
	value.AppliedVersion = 4
	if err := Save(path, value); err != nil {
		t.Fatalf("replace Save() error = %v", err)
	}
	loaded, exists, err := Load(path)
	if err != nil || !exists || loaded.AppliedVersion != 4 {
		t.Fatalf("Load() = %#v, %v, %v", loaded, exists, err)
	}
}

func TestSecretRedaction(t *testing.T) {
	token := "1234567890"
	if got := TokenPrefix(token); got != "1234***" {
		t.Fatalf("TokenPrefix() = %q", got)
	}
	text := Sanitize("token="+token+" private=secret", token, "secret")
	if strings.Contains(text, token) || strings.Contains(text, "secret") {
		t.Fatalf("Sanitize() leaked secrets: %q", text)
	}
}
