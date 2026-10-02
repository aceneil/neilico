package nps

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"neilico/control-plane/internal/config"
	"neilico/control-plane/internal/db"
	neilicoproxy "neilico/control-plane/internal/service/proxy"
)

func TestRenderConfigGolden(t *testing.T) {
	t.Parallel()
	routes := []neilicoproxy.Route{{
		RuleID:         uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		TenantID:       uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		DomainID:       uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		Host:           "app.example.com",
		Path:           "/",
		TargetType:     "node",
		Target:         "100.64.0.10:8080",
		OriginalTarget: "44444444-4444-4444-4444-444444444444:8080",
		AccessControl:  accessControlValue(),
	}}
	got, err := renderConfig(routes, "file")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "nps-config.golden.json")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("NPS config mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

type recordingObserver struct {
	kind string
	up   bool
}

func (o *recordingObserver) SetProxyProviderUp(kind string, up bool) {
	o.kind = kind
	o.up = up
}

func (o *recordingObserver) ObserveProxyRequest(string, string) {}

func TestReloadWritesConfigAndDegradesWithoutBinary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	handle, err := db.Open(config.Database{Driver: "sqlite", DSN: "file:nps-reload?mode=memory&cache=shared"}, "error")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(handle); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := handle.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	observer := &recordingObserver{}
	provider := New(handle, Options{
		ConfigPath:     filepath.Join(dir, "nps", "config.json"),
		BinaryPath:     filepath.Join(dir, "missing-nps"),
		PIDFile:        filepath.Join(dir, "nps.pid"),
		ReloadStrategy: "file",
	}, nil, observer)
	if err := provider.Reload(t.Context()); err != nil {
		t.Fatalf("missing external NPS binary became fatal: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "nps", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"clients": []`)) {
		t.Fatalf("unexpected NPS config: %s", data)
	}
	if provider.State().Status != "degraded" || observer.kind != "nps" || observer.up {
		t.Fatalf("unexpected unavailable NPS state: %#v %#v", provider.State(), observer)
	}
}
