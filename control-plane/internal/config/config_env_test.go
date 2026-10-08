package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestRemoteDesktopEnvironmentOverrides(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(p, []byte("database:\n  driver: sqlite\n  dsn: file:x\n"), 0600)

	// 默认值来自代码：ID/中继服务器 + 公钥文件路径 + 五个标准端口。
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.RemoteDesktop.IDServer != DefaultRemoteDesktopServer ||
		c.RemoteDesktop.RelayServer != DefaultRemoteDesktopServer ||
		c.RemoteDesktop.PublicKeyFile != DefaultRemoteDesktopPublicKeyFile ||
		!c.RemoteDesktop.Enabled {
		t.Fatalf("unexpected remote desktop defaults: %#v", c.RemoteDesktop)
	}
	if len(c.RemoteDesktop.Ports) != 5 || c.RemoteDesktop.Ports[0] != 21115 || c.RemoteDesktop.Ports[4] != 21119 {
		t.Fatalf("unexpected default ports: %#v", c.RemoteDesktop.Ports)
	}
	// 自托管生命周期默认：on_demand + 10 分钟空闲 + 数据目录下的密钥目录。
	if c.RemoteDesktop.ServerMode != DefaultRemoteDesktopServerMode ||
		c.RemoteDesktop.IdleTimeout != DefaultRemoteDesktopIdleTimeout ||
		c.RemoteDesktop.KeyDir != DefaultRemoteDesktopKeyDir ||
		c.RemoteDesktop.HBBSPath != DefaultRemoteDesktopHBBSPath ||
		c.RemoteDesktop.HBBRPath != DefaultRemoteDesktopHBBRPath {
		t.Fatalf("unexpected self-hosted defaults: %#v", c.RemoteDesktop)
	}

	t.Setenv("NEILICO_RD_ENABLED", "false")
	t.Setenv("NEILICO_RD_ID_SERVER", "10.0.0.5:21116")
	t.Setenv("NEILICO_RD_RELAY_SERVER", "10.0.0.6:21117")
	t.Setenv("NEILICO_RD_PUBLIC_KEY_FILE", "/tmp/neilico-rd.pub")
	t.Setenv("NEILICO_RD_PORTS", "21116, 21117")
	t.Setenv("NEILICO_RD_SERVER_MODE", "always_on")
	t.Setenv("NEILICO_RD_IDLE_TIMEOUT", "3m")
	t.Setenv("NEILICO_RD_KEY_DIR", "/data/rd-keys")
	t.Setenv("NEILICO_RD_RELAY_HOST", "relay.example.com:21117")
	c, err = Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.RemoteDesktop.Enabled {
		t.Fatal("NEILICO_RD_ENABLED=false was not applied")
	}
	if c.RemoteDesktop.IDServer != "10.0.0.5:21116" || c.RemoteDesktop.RelayServer != "10.0.0.6:21117" {
		t.Fatalf("environment server override failed: %#v", c.RemoteDesktop)
	}
	if c.RemoteDesktop.PublicKeyFile != "/tmp/neilico-rd.pub" {
		t.Fatalf("environment public key override failed: %#v", c.RemoteDesktop)
	}
	if len(c.RemoteDesktop.Ports) != 2 || c.RemoteDesktop.Ports[0] != 21116 || c.RemoteDesktop.Ports[1] != 21117 {
		t.Fatalf("environment ports override failed: %#v", c.RemoteDesktop.Ports)
	}
	if c.RemoteDesktop.ServerMode != "always_on" || c.RemoteDesktop.IdleTimeout != 3*time.Minute ||
		c.RemoteDesktop.KeyDir != "/data/rd-keys" || c.RemoteDesktop.RelayHost != "relay.example.com:21117" {
		t.Fatalf("environment lifecycle override failed: %#v", c.RemoteDesktop)
	}
}

func TestRemoteDesktopServerModeValidation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(p, []byte("database:\n  driver: sqlite\n  dsn: file:x\n"), 0600)
	t.Setenv("NEILICO_RD_SERVER_MODE", "bogus")
	if _, err := Load(p); err == nil {
		t.Fatal("expected invalid server_mode to fail validation")
	}
}
