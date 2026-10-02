package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTransportEnvironmentOverrides(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(p, []byte("database:\n  driver: sqlite\n  dsn: file:x\n"), 0600)
	t.Setenv("NEILICO_PKI_ENABLED", "true")
	t.Setenv("NEILICO_SERVER_TLS_ENABLED", "true")
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !c.PKI.Enabled || !c.Server.TLS.Enabled {
		t.Fatalf("%#v", c)
	}
}
