package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCredentialsFilePermissionsAndEnvServer(t *testing.T) {
	t.Setenv("NEILICO_SERVER", "https://env.example.test")
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	value := Credentials{Server: "https://file.example.test", AccessToken: "secret"}
	if err := Save(path, value); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o", info.Mode().Perm())
	}
	loaded, err := Load(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Server != "https://env.example.test" || loaded.AccessToken != "secret" {
		t.Fatalf("Load() = %#v", loaded)
	}
	loaded, err = Load(path, "https://flag.example.test")
	if err != nil || loaded.Server != "https://flag.example.test" {
		t.Fatalf("flag override = %#v, %v", loaded, err)
	}
}
