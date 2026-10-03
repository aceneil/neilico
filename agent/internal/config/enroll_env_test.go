package config

import (
	"path/filepath"
	"testing"
)

func TestNEILICO_TOKENAndStateDirectory(t *testing.T) {
	t.Setenv("NEILICO_TOKEN", "neilico-enroll.test.test")
	t.Setenv("NEILICO_STATE_DIR", "/tmp/neilico-state-test")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EnrollToken != "neilico-enroll.test.test" {
		t.Fatalf("EnrollToken = %q", cfg.EnrollToken)
	}
	if cfg.StatePath != filepath.Join("/tmp/neilico-state-test", "state.json") {
		t.Fatalf("StatePath = %q", cfg.StatePath)
	}
}
